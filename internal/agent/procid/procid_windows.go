//go:build windows

package procid

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func Read(pid int) (Identity, error) {
	if pid <= 0 {
		return Identity{}, ErrUnsupported
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return Identity{}, errGone
	}
	if err != nil {
		return Identity{}, err
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return Identity{}, err
	}
	state, err := windows.WaitForSingleObject(h, 0)
	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, StartTime: uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), Zombie: state == windows.WAIT_OBJECT_0}, nil
}

// An unnamed Job is scoped to the owning agent's handle. A fresh agent has
// no handle to the old Job and cannot confirm that tree's state from a PID.
func GroupMembers(int) ([]Identity, error) { return nil, ErrUnsupported }
func TerminateVerifiedGroup(int, uint64, time.Duration, time.Duration) (bool, error) {
	return false, ErrUnsupported
}
