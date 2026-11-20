package instance

import (
	"net"
	"sort"
	"time"
)

// Consistency problems between the state directory and the processes.
const (
	// IssueStaleRecord: a process record exists but its process is gone or
	// its pid now belongs to another process.
	IssueStaleRecord = "stale_record"
	// IssueAgentWithoutRecord: some process holds the agent state lock but
	// no running agent is recorded (an agent not started by codeye start).
	IssueAgentWithoutRecord = "agent_running_without_record"
	// IssueListenWithoutRecord: the listen address accepts connections but no
	// running controller is recorded.
	IssueListenWithoutRecord = "listen_in_use_without_record"
	// IssuePartial: only one of the controller and the agent runs.
	IssuePartial = "partially_running"
	// IssueRecordUnreadable: a record or the lock cannot be read.
	IssueRecordUnreadable = "record_unreadable"
)

// ProcessState is one role's recorded process and its live state.
type ProcessState struct {
	Role   string
	Record *Record
	State  string
}

// Consistency is the start/stop state of a service installation.
type Consistency struct {
	Processes []ProcessState
	Issues    []string
}

// Running reports whether role's recorded process is alive.
func (c Consistency) Running(role string) bool {
	for _, p := range c.Processes {
		if p.Role == role {
			return p.State == ProcRunning
		}
	}
	return false
}

// Inspect reads the process records and checks them against the live
// processes, the agent state lock and the listen address. probeAgentLock
// must be false while another start/stop may be starting an agent (the probe
// takes the lock for an instant).
func (l Layout) Inspect(listen string, probeAgentLock bool) Consistency {
	var c Consistency
	issues := map[string]bool{}
	for _, role := range Roles {
		rec, err := l.ReadRecord(role)
		p := ProcessState{Role: role, Record: rec, State: ProcNone}
		if err != nil {
			p.State = ProcUnknown
			issues[IssueRecordUnreadable] = true
		} else if rec != nil {
			p.State = rec.State()
			if p.State == ProcExited || p.State == ProcReused {
				issues[IssueStaleRecord] = true
			}
			if p.State == ProcUnknown {
				issues[IssueRecordUnreadable] = true
			}
		}
		c.Processes = append(c.Processes, p)
	}
	ctrl, agent := c.Running(RoleController), c.Running(RoleAgent)
	if ctrl != agent {
		issues[IssuePartial] = true
	}
	if !agent && probeAgentLock {
		held, err := l.AgentLockHeld()
		if err != nil && Supported() == nil {
			issues[IssueRecordUnreadable] = true
		}
		if held {
			issues[IssueAgentWithoutRecord] = true
		}
	}
	if !ctrl && listen != "" {
		if conn, err := net.DialTimeout("tcp", listen, 500*time.Millisecond); err == nil {
			_ = conn.Close()
			issues[IssueListenWithoutRecord] = true
		}
	}
	for k := range issues {
		c.Issues = append(c.Issues, k)
	}
	sort.Strings(c.Issues)
	return c
}
