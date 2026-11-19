//go:build !linux

package doctor

import "os/exec"

// Job Object/process containment is not available here; timeout is never reported as clean.
func configureProcess(cmd *exec.Cmd) {}
