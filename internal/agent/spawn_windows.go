//go:build windows

package agent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"unsafe"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"golang.org/x/sys/windows"
)

type spawner struct{}

func newSpawner() *spawner { return &spawner{} }
func (*spawner) close()    {}
func (*spawner) start(cmd *exec.Cmd) (func(), error) {
	return executor.StartWorkerJob(cmd)
}

func platformEnv(env []string) []string {
	if root := os.Getenv("SystemRoot"); root != "" {
		env = append(env, "SystemRoot="+root)
	}
	return env
}

func openNoFollow(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil ||
		info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, errors.New("cannot read artifact as a regular file")
	}
	return os.NewFile(uintptr(h), path), nil
}

// MEMORYSTATUSEX from the Win32 SDK. Length must be set before the call.
type memoryStatusEx struct {
	Length, MemoryLoad                                 uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

var globalMemoryStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func hostCapacity(dir string) (mem, disk int64) {
	var m memoryStatusEx
	m.Length = uint32(unsafe.Sizeof(m))
	if ok, _, _ := globalMemoryStatus.Call(uintptr(unsafe.Pointer(&m))); ok != 0 {
		mem = int64(m.AvailPhys)
	}
	name, err := windows.UTF16PtrFromString(dir)
	var available uint64
	if err == nil && windows.GetDiskFreeSpaceEx(name, &available, nil, nil) == nil {
		disk = int64(available)
	}
	return mem, disk
}

// Exclusive access lasts for the lifetime of the non-inheritable handle.
// Windows releases it on a crash, so the next agent can reconcile the journal.
func lockStateDir(dir string) (func(), error) {
	name, err := windows.UTF16PtrFromString(filepath.Join(dir, "agent.lock"))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, errors.New("state directory unavailable or already in use")
	}
	return func() { _ = windows.CloseHandle(h) }, nil
}
