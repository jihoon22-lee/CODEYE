//go:build !linux && !windows

package agent

import (
	"os"
	"os/exec"
)

// spawner on non-Linux platforms starts workers directly. Windows worker
// reconciliation is not verified (identity unsupported → uncertain).
type spawner struct{}

func newSpawner() *spawner { return &spawner{} }

func (s *spawner) start(cmd *exec.Cmd) (release func(), err error) {
	return func() {}, cmd.Start()
}

func (s *spawner) close() {}

// lockStateDir is not implemented on this platform fallback: a second agent
// on the same state dir is not prevented here (documented limitation; the
// Windows integration supplies a LockFileEx variant).
func lockStateDir(dir string) (func(), error) { return func() {}, nil }

func platformEnv(env []string) []string {
	if v := os.Getenv("SystemRoot"); v != "" {
		env = append(env, "SystemRoot="+v)
	}
	return env
}

func openNoFollow(path string) (*os.File, error) { return os.Open(path) }

func hostCapacity(dir string) (mem, disk int64) { return 0, 0 }
