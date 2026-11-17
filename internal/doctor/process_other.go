//go:build !linux

package doctor

import "os/exec"

// Job Object/process containment is W05; timeout is never reported as clean.
func configureProcess(cmd *exec.Cmd) {}
