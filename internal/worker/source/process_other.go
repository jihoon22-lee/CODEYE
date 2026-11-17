//go:build !linux

package source

import "os/exec"

func configureGitProcess(cmd *exec.Cmd) {}
