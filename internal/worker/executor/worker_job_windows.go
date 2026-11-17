//go:build windows

package executor

import (
	"errors"
	"os/exec"
	"sync"
)

// StartWorkerJob starts a long-lived worker in an agent-owned kill-on-close
// Job. The caller owns cmd.Wait and must release the Job after exit or forced
// shutdown. Windows also closes the Job when the agent process dies.
func StartWorkerJob(cmd *exec.Cmd) (release func(), err error) {
	p, failure := startContained(cmd)
	if failure != nil {
		return nil, errors.New(failure.code)
	}
	return sync.OnceFunc(p.release), nil
}
