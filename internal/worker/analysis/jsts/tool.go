package jsts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// ProbeTimeout matches the env.discover tool probe.
const ProbeTimeout = 30 * time.Second

// Script is an operator-installed Node tool: the absolute entry script of an
// npm package (--<tool>), run as <node> <script> <args> with PATH set to the
// node directory only (docs/ANALYZERS.md#공유-interpreter-toolchain), never through its #! line.
type Script struct {
	// Path is the operator's absolute path (argv[1]); Resolved is the
	// symlink-resolved file; Package is the npm package directory that
	// contains it (the directory of the nearest package.json named Name).
	Path, Resolved, Package string
	// Node is the validated node toolchain handle.
	Node pack.Toolchain
}

// shimExtensions are wrappers node cannot run as a script.
var shimExtensions = []string{".cmd", ".bat", ".ps1", ".exe", ".sh", ".com"}

// NewScript validates an operator path to the entry script of npm package
// name: absolute, a regular file inside that package, not a shell or .cmd
// shim. Nothing is executed.
func NewScript(p, name string, node pack.Toolchain) (Script, error) {
	if !filepath.IsAbs(p) {
		return Script{}, errors.New(name + " script must be an absolute path")
	}
	if node.Executable == "" || node.Dir == "" {
		return Script{}, errors.New(name + " needs the node toolchain")
	}
	resolved, err := nativepath.Resolve(p)
	if err != nil {
		return Script{}, errors.New(name + " script cannot be resolved")
	}
	st, err := os.Stat(resolved)
	if err != nil || !st.Mode().IsRegular() {
		return Script{}, errors.New(name + " script is not a regular file")
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	for _, e := range shimExtensions {
		if ext == e {
			return Script{}, errors.New(name + " script is a shell or command shim, not a JS entry script")
		}
	}
	head := make([]byte, 128)
	if f, err := os.Open(resolved); err == nil {
		n, _ := io.ReadFull(f, head)
		head = head[:n]
		_ = f.Close()
	}
	if bytes.HasPrefix(head, []byte("#!")) {
		line, _, _ := bytes.Cut(head, []byte("\n"))
		if !bytes.Contains(line, []byte("node")) {
			return Script{}, errors.New(name + " script is a shell wrapper, not a JS entry script")
		}
	}
	pkg, ok := packageDirOf(resolved, name)
	if !ok {
		return Script{}, errors.New(name + " script is not inside the " + name + " npm package")
	}
	return Script{Path: p, Resolved: resolved, Package: pkg, Node: node}, nil
}

// packageDirOf finds the nearest ancestor directory of file whose
// package.json names the package.
func packageDirOf(file, name string) (string, bool) {
	for d := filepath.Dir(file); ; d = filepath.Dir(d) {
		if v, ok := ReadPackage(d); ok && v.Name == name {
			return d, true
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}

// PackageInfo is what CODEYE reads from an installed package.json (data,
// never executed): name, version and the CommonJS entry.
type PackageInfo struct {
	Name    string
	Version string
	Entry   string // absolute entry file ("" if none)
}

// ReadPackage reads dir/package.json of an installed package.
func ReadPackage(dir string) (PackageInfo, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || len(b) > 1<<20 {
		return PackageInfo{}, false
	}
	var raw struct {
		Name    string          `json:"name"`
		Version string          `json:"version"`
		Main    string          `json:"main"`
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(b, &raw) != nil || raw.Name == "" {
		return PackageInfo{}, false
	}
	info := PackageInfo{Name: raw.Name, Version: raw.Version}
	entry := raw.Main
	if len(raw.Exports) > 0 {
		var s string
		var m map[string]json.RawMessage
		switch {
		case json.Unmarshal(raw.Exports, &s) == nil:
			entry = s
		case json.Unmarshal(raw.Exports, &m) == nil:
			if dot, ok := m["."]; ok {
				var cond map[string]json.RawMessage
				if json.Unmarshal(dot, &s) == nil {
					entry = s
				} else if json.Unmarshal(dot, &cond) == nil {
					for _, k := range []string{"require", "default"} {
						if v, ok := cond[k]; ok && json.Unmarshal(v, &s) == nil {
							entry = s
							break
						}
					}
				}
			}
		}
	}
	if entry == "" {
		entry = "index.js"
	}
	full := filepath.Clean(filepath.Join(dir, filepath.FromSlash(entry)))
	if rel, err := filepath.Rel(dir, full); err == nil && !strings.HasPrefix(rel, "..") {
		if st, err := os.Stat(full); err == nil && st.Mode().IsRegular() {
			info.Entry = full
		}
	}
	return info, true
}

// Env is the complete child environment: PATH is the node directory only
// (no repository or user tool is found by name), LANG is fixed, Windows gets
// SystemRoot. HOME and TMP are the executor's per-run directories. Nothing
// else is inherited: no NODE_OPTIONS, NODE_PATH or NODE_ENV.
func (s Script) Env() map[string]string {
	env := map[string]string{"PATH": s.Node.Dir, "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	return env
}

// Argv is the argv after the node executable: the script, then args.
func (s Script) Argv(args ...string) []string { return append([]string{s.Path}, args...) }

// Probe runs <node> <script> <args> in a fresh directory under dir (outside
// any source tree) with the tool-probe rules and returns stdout. Errors name
// no path.
func (s Script) Probe(ctx context.Context, runner executor.Runner, dir, label string, args ...string) ([]byte, error) {
	if !filepath.IsAbs(dir) {
		return nil, errors.New("probe dir must be absolute")
	}
	if runner == nil {
		return nil, errors.New("no runner")
	}
	work, err := os.MkdirTemp(dir, label+"-probe-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	ws := filepath.Join(work, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		return nil, err
	}
	res := runner.Run(ctx, executor.Spec{Executable: s.Node.Executable, Args: s.Argv(args...), WorkspaceRoot: ws, Dir: ws, TempParent: work,
		Env: s.Env(), Timeout: ProbeTimeout, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	switch {
	case res.Cleanup == executor.CleanupUncertain:
		return nil, errors.New(label + " version probe cleanup uncertain")
	case res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0:
		return nil, fmt.Errorf("%s version probe did not succeed: %s %s", label, res.Status, res.Reason)
	case res.Stdout.Truncated || res.Stdout.Incomplete:
		return nil, errors.New(label + " version output incomplete")
	}
	return res.Stdout.Data, nil
}

// Masker replaces node-absolute prefixes in tool text: workspace paths become
// relative (govet.Masker), the tool package, node, scratch and snapshot
// become stable placeholders.
func Masker(roots []string, s Script, toolRoot, scratch, snapshotRoot string) func(string) string {
	mask := govet.Masker(roots, "", "", "", scratch, snapshotRoot)
	type pair struct{ from, to string }
	var pairs []pair
	add := func(p, to string) {
		if p == "" {
			return
		}
		pairs = append(pairs, pair{p, to})
		if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
			pairs = append(pairs, pair{r, to})
		}
	}
	add(toolRoot, "$TOOL")
	add(s.Node.Dir, "$NODE")
	return func(t string) string {
		t = mask(t)
		for _, p := range pairs {
			t = strings.ReplaceAll(t, p.from, p.to)
		}
		return t
	}
}

// RedactForms are the workspace roots in every form the executor redacts.
func RedactForms(roots []string) []string { return govet.RedactForms(roots) }

// Roots are the workspace root and its symlink-resolved form.
func Roots(workspace string) []string {
	roots := []string{workspace}
	if r, err := filepath.EvalSymlinks(workspace); err == nil && r != workspace {
		roots = append(roots, r)
	}
	return roots
}

// StripWorkspace maps a tool path (absolute workspace, redacted or
// module-relative) to a snapshot-relative path.
func StripWorkspace(p, moduleDir string, roots []string) (string, bool) {
	return govet.StripWorkspace(p, moduleDir, roots, true)
}

// ForeignNodeModules reports a node_modules entry on the path from the
// module directory to the filesystem root. Module resolution of tsc and of
// typescript-eslint walks these directories; one outside the snapshot would
// silently feed dependency code from another location into the analysis,
// so the adapters refuse to run (docs/ANALYZERS.md#저장소-코드와-tsconfig-검사).
func ForeignNodeModules(workspace, moduleDir string) bool {
	d := filepath.Join(workspace, filepath.FromSlash(moduleDir))
	for {
		if _, err := os.Lstat(filepath.Join(d, "node_modules")); err == nil {
			return true
		}
		up := filepath.Dir(d)
		if up == d {
			return false
		}
		d = up
	}
}

// WriteExecution stores a run's streams (analysis.WriteExecution).
func WriteExecution(dir, name string, r executor.Result) (analysis.Execution, error) {
	return analysis.WriteExecution(dir, name, r)
}

// Stoppable reports a result after which a task runs nothing more.
func Stoppable(r *executor.Result) bool {
	return r.Cleanup == executor.CleanupUncertain || r.Status == executor.StatusCancelled || r.Status == executor.StatusTimedOut ||
		r.Status == executor.StatusRejected || r.Status == executor.StatusUnsupported || r.Status == executor.StatusStartFailed
}

// ExitedZero reports a clean exit 0.
func ExitedZero(r *executor.Result) bool {
	return r != nil && r.Status == executor.StatusExited && r.ExitCode != nil && *r.ExitCode == 0
}

// StreamsComplete reports both streams captured whole.
func StreamsComplete(r *executor.Result) bool {
	return r != nil && !r.Stdout.Truncated && !r.Stdout.Incomplete && !r.Stderr.Truncated && !r.Stderr.Incomplete
}

// maxFile bounds a workspace file read for drift checks.
const maxFile = 64 << 20

// Drift compares the given snapshot paths in the workspace after the runs
// with their manifest SHA-256; a changed or missing file is drift.
func Drift(workspace string, files map[string]source.Entry, paths []string) map[string]bool {
	drift := map[string]bool{}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		for _, p := range paths {
			drift[p] = true
		}
		return drift
	}
	defer root.Close()
	for _, p := range paths {
		e, ok := files[p]
		if !ok {
			continue
		}
		f, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			drift[p] = true
			continue
		}
		b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
		_ = f.Close()
		if err != nil || len(b) > maxFile || obsdoc.SHA256Hex(b) != e.SHA256 {
			drift[p] = true
		}
	}
	return drift
}
