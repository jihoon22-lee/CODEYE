package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Command is for fixed, read-only doctor probes, not repository tasks (W05).
type Command struct {
	Name    string
	Args    []string
	Env     []string
	Timeout time.Duration
}
type Result struct {
	Output string
	Reason string
}
type Runner interface {
	Run(context.Context, Command) Result
}
type NativeRunner struct{}

type cappedWriter struct {
	data     []byte
	exceeded bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 8192 - len(w.data)
	if n > remaining {
		w.exceeded = true
		p = p[:remaining]
	}
	w.data = append(w.data, p...)
	return n, nil
}

func (NativeRunner) Run(parent context.Context, spec Command) Result {
	path, err := exec.LookPath(spec.Name)
	if err != nil {
		if os.IsPermission(err) {
			return Result{Reason: "permission_denied"}
		}
		return Result{Reason: "executable_missing"}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return Result{Reason: "invalid_executable"}
	}
	dir, err := os.MkdirTemp("", "codeye-probe-")
	if err != nil {
		return Result{Reason: "workspace_unavailable"}
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(parent, spec.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, spec.Args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "USERPROFILE=" + dir, "TMPDIR=" + dir, "TMP=" + dir, "TEMP=" + dir, "LC_ALL=C", "LANG=C", "GOTOOLCHAIN=local", "GOWORK=off", "GOENV=off", "GOPROXY=off", "GOSUMDB=off"}
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, spec.Env...)
	cmd.WaitDelay = 250 * time.Millisecond
	configureProcess(cmd)
	// os/exec serializes writes when stdout and stderr use the same writer.
	var output cappedWriter
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	result := Result{Output: string(output.data)}
	switch {
	case ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay):
		result.Reason = "recovery_required"
	case output.exceeded:
		result.Reason = "output_limit"
	case os.IsPermission(err):
		result.Reason = "permission_denied"
	case err != nil:
		result.Reason = "probe_failed"
	}
	return result
}
