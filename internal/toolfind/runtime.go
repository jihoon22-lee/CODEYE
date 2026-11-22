package toolfind

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// AutomaticPaths reuses startup observations. Only tools masked by explicit
// paths need a separate search to discover what clearing their override means.
func AutomaticPaths(ctx context.Context, names []string, res Result, ex Explicit) (map[string]string, error) {
	paths := map[string]string{}
	var masked []string
	for _, n := range names {
		if ex.Path(n) != "" {
			masked = append(masked, n)
			continue
		}
		if t, ok := res.Tool(n); ok && t.Status == Found {
			paths[n] = t.Path
		}
	}
	if len(masked) == 0 {
		return paths, nil
	}
	dir, err := os.MkdirTemp("", "codeye-toolfind-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	d := detector{ctx: ctx, env: SystemEnv(), runner: &executor.Executor{}, probeDir: dir, found: map[string]Tool{}}
	if uv, ok := res.Tool("uv"); ok {
		d.set(uv)
	}
	d.run(masked)
	for _, n := range masked {
		if t := d.get(n); t.Status == Found {
			paths[n] = t.Path
		}
	}
	return paths, ctx.Err()
}

// ValidRuntimeExecutable probes only the selected program using the worker's
// restricted version probe environment. It does not query user registry settings.
func ValidRuntimeExecutable(ctx context.Context, name, path string) bool {
	dir, err := os.MkdirTemp("", "codeye-toolfind-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	d := detector{ctx: ctx, env: SystemEnv(), runner: &executor.Executor{}, probeDir: dir, found: map[string]Tool{}}
	switch name {
	case "python":
		out, reason := d.version(path, "-I", "--version")
		version := pythonVersionLine.FindStringSubmatch(out)
		if reason != "" || version == nil {
			return false
		}
		minor, err := strconv.Atoi(version[2])
		return err == nil && minor >= 8
	case "java":
		java, err := filepath.EvalSymlinks(path)
		if err != nil {
			return false
		}
		// Java compilation and Maven/Gradle runs need the matching JDK compiler.
		compiler := filepath.Join(filepath.Dir(java), "javac"+filepath.Ext(java))
		version, reason := d.version(compiler, "--version")
		return reason == "" && strings.HasPrefix(version, "javac ") && d.validate(name, specs[name], path).ok
	case "go", "node", "cargo":
		return d.validate(name, specs[name], path).ok
	default:
		return false
	}
}
