//go:build windows

package executor

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const pollInterval = 20 * time.Millisecond

func platformCheck() *specError { return nil }

// jobAccounting mirrors JOBOBJECT_BASIC_ACCOUNTING_INFORMATION (48 bytes).
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// winProc contains the main process in a dedicated Job Object. The process
// is created suspended, assigned to the job, and only then resumed, so no
// child can be created outside the job. The job has KILL_ON_JOB_CLOSE and no
// BREAKAWAY_OK, so descendants cannot leave it and die if the agent exits.
type winProc struct {
	pid   int
	start uint64
	proc  windows.Handle
	job   windows.Handle
	stop  windows.Handle
	done  chan struct{}
	quit  chan struct{}
}

func startContained(cmd *exec.Cmd) (platformProc, *startFailure) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		detail := "process start failed"
		if errors.Is(err, os.ErrPermission) {
			detail = "permission denied"
		}
		return nil, &startFailure{status: StatusStartFailed, code: "exec_failed", detail: detail, cleanup: CleanupNotStarted}
	}
	p := &winProc{pid: cmd.Process.Pid, done: make(chan struct{}), quit: make(chan struct{})}
	fail := func(code, detail string) (platformProc, *startFailure) {
		// The process never ran user code; terminate it (job first if any).
		if p.job != 0 {
			_ = windows.TerminateJobObject(p.job, 1)
		}
		_ = cmd.Process.Kill()
		cleanup, note := CleanupConfirmed, ""
		waited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			cleanup, note = CleanupUncertain, "suspended_process_not_reaped"
		}
		p.closeHandles()
		return nil, &startFailure{status: StatusUnsupported, code: code, detail: detail, cleanup: cleanup, note: note}
	}
	// cmd.Process holds a handle, so the PID cannot be reused here.
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.pid))
	if err != nil {
		return fail("job_object_unavailable", "cannot open process handle for job assignment")
	}
	p.proc = h
	var ct, et, kt, ut windows.Filetime
	if windows.GetProcessTimes(h, &ct, &et, &kt, &ut) == nil {
		p.start = uint64(ct.HighDateTime)<<32 | uint64(ct.LowDateTime)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fail("job_object_unavailable", "CreateJobObject failed")
	}
	p.job = job
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fail("job_object_unavailable", "cannot set KILL_ON_JOB_CLOSE")
	}
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fail("job_object_assign_failed", "AssignProcessToJobObject failed")
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return fail("job_object_unavailable", "CreateEvent failed")
	}
	p.stop = stop
	if code, detail := resumeOnlyThread(uint32(p.pid)); code != "" {
		return fail(code, detail)
	}
	go p.waitExit()
	return p, nil
}

// resumeOnlyThread resumes the single primary thread of a suspended process.
// More than one thread, or a suspend count other than 1, is refused because
// the process state would not be the one created by this executor.
func resumeOnlyThread(pid uint32) (string, string) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return "resume_failed", "thread snapshot failed"
	}
	defer windows.CloseHandle(snap)
	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var tids []uint32
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID == pid {
			tids = append(tids, entry.ThreadID)
		}
	}
	if len(tids) != 1 {
		return "resume_failed", "suspended process does not have exactly one thread"
	}
	th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, tids[0])
	if err != nil {
		return "resume_failed", "OpenThread failed"
	}
	defer windows.CloseHandle(th)
	prev, err := windows.ResumeThread(th)
	if err != nil || prev != 1 {
		return "resume_failed", "unexpected suspend count"
	}
	return "", ""
}

func (p *winProc) waitExit() {
	defer close(p.quit)
	ev, err := windows.WaitForMultipleObjects([]windows.Handle{p.proc, p.stop}, false, windows.INFINITE)
	if err == nil && ev == windows.WAIT_OBJECT_0 {
		close(p.done)
	}
}

func (p *winProc) identity() ProcessIdentity { return ProcessIdentity{PID: p.pid, StartTime: p.start} }
func (p *winProc) exited() <-chan struct{}   { return p.done }
func (p *winProc) containment() Containment  { return ContainmentJobObject }

func (p *winProc) mainExited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *winProc) accounting() (jobAccounting, error) {
	var info jobAccounting
	err := windows.QueryInformationJobObject(p.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	return info, err
}

func (p *winProc) leftover() (bool, error) {
	info, err := p.accounting()
	if err != nil {
		return false, err
	}
	return info.ActiveProcesses > 0, nil
}

// terminate has no graceful phase: Windows has no portable SIGTERM for
// console tools, so the whole job is terminated at once.
func (p *winProc) terminate(_, killWait time.Duration) stopReport {
	if err := windows.TerminateJobObject(p.job, 1); err != nil {
		return stopReport{note: "terminate_job_failed"}
	}
	deadline := time.Now().Add(killWait)
	for {
		info, err := p.accounting()
		if err != nil {
			return stopReport{note: "job_query_failed"}
		}
		if info.ActiveProcesses == 0 && p.mainExited() {
			return stopReport{confirmed: true}
		}
		if time.Now().After(deadline) {
			return stopReport{note: "job_processes_remain_after_kill"}
		}
		time.Sleep(pollInterval)
	}
}

func (p *winProc) afterReap() (bool, string) {
	info, err := p.accounting()
	if err != nil {
		return false, "job_query_failed"
	}
	if info.ActiveProcesses != 0 {
		return false, "job_processes_remain"
	}
	return true, ""
}

func (p *winProc) observe(r *ResourceReport) {
	if info, err := p.accounting(); err == nil {
		r.UserCPU = time.Duration(info.TotalUserTime) * 100
		r.SystemCPU = time.Duration(info.TotalKernelTime) * 100
		r.CPUScope = "job"
	}
	var ext windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if windows.QueryInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&ext)), uint32(unsafe.Sizeof(ext)), nil) == nil {
		r.PeakMemoryBytes = int64(ext.PeakJobMemoryUsed)
		r.PeakMemoryScope = "job"
	}
}

// release closes the job handle; KILL_ON_JOB_CLOSE terminates anything that
// is somehow still inside (cleanup is then already reported uncertain).
func (p *winProc) release() {
	if p.stop != 0 {
		_ = windows.SetEvent(p.stop)
		<-p.quit
	}
	p.closeHandles()
}

func (p *winProc) closeHandles() {
	for _, h := range []*windows.Handle{&p.stop, &p.proc, &p.job} {
		if *h != 0 {
			_ = windows.CloseHandle(*h)
			*h = 0
		}
	}
}

func fillExit(res *Result, ps *os.ProcessState) {
	if ps == nil {
		return
	}
	code := ps.ExitCode()
	res.ExitCode = &code
	res.Resources.UserCPU = ps.UserTime()
	res.Resources.SystemCPU = ps.SystemTime()
	res.Resources.CPUScope = "main_process"
}
