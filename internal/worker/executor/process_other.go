//go:build !linux && !windows

package executor

import (
	"os"
	"os/exec"
)

// Other platforms have no implemented containment; Run refuses to execute
// rather than reporting an unverified cleanup as success.
func platformCheck() *specError {
	return &specError{status: StatusUnsupported, code: "platform_unsupported",
		detail: "native process containment is implemented only for Linux and Windows"}
}

func startContained(*exec.Cmd) (platformProc, *startFailure) {
	return nil, &startFailure{status: StatusUnsupported, code: "platform_unsupported", cleanup: CleanupNotStarted}
}

func fillExit(*Result, *os.ProcessState) {}
