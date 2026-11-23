// Package toolfind finds the tools CODEYE can use on this machine: the Go
// toolchain, git, the GitHub CLI, node, OpenCode and every analyzer of the
// builtin catalog.
//
// A tool is looked for in the user's PATH, in the usual per-user install
// directories and in the CODEYE tool prefix (docs/ANALYZERS.md#도구-찾기).
// Tools that are JS entry scripts (pyright, eslint, tsc, jscpd) are searched
// as fixed entry files under node_modules directories, and cargo and clippy
// are taken from a rustup toolchain's own bin directory, never from the
// rustup proxy. Every candidate is validated with the version probe the agent
// itself runs at startup (builtin.CheckTool), so a tool reported as found is a
// tool the agent accepts. Explicit configuration always wins and is never
// replaced by a detected tool.
//
// Detection only reads and runs `--version`; it never installs anything.
package toolfind

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// SchemaVersion of tools.json.
const SchemaVersion = "0.1"

// FileName is the name of the detection result in the state directory.
const FileName = "tools.json"

// Status of one tool.
type Status string

// Detection outcomes.
const (
	// Found: a candidate passed the agent's own validation.
	Found Status = "found"
	// NotFound: no candidate exists in any searched location.
	NotFound Status = "not_found"
	// Unusable: candidates exist but none passed validation (wrong shape,
	// version output not recognized, a rustup proxy, ...).
	Unusable Status = "unusable"
)

// Source labels of where a tool was found.
const (
	SourceConfig = "config"
	SourcePath   = "PATH"
)

// NoteVersionUntested is recorded for a usable tool whose version is outside
// the tested series.
const NoteVersionUntested = "tool_version_untested"

// Rejected is a candidate that exists but failed validation.
type Rejected struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Tool is the detection result of one tool. Name is the agent flag name
// (docs/ANALYZERS.md#에이전트-플래그), which is also the key under agent.tools in the
// service configuration; go, git and opencode have their own settings.
type Tool struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	// Path is the absolute path the agent is given.
	Path string `json:"path,omitempty"`
	// ResolvedPath is Path after resolving symbolic links, when different.
	ResolvedPath string `json:"resolved_path,omitempty"`
	Version      string `json:"version,omitempty"`
	// VersionTested reports a version of the tested series (always true for
	// tools without one). Only meaningful when Status is Found.
	VersionTested bool   `json:"version_tested"`
	TestedSeries  string `json:"tested_series,omitempty"`
	// Source says where the winning candidate was found.
	Source string   `json:"source,omitempty"`
	Notes  []string `json:"notes,omitempty"`
	// Reason is why the tool is unusable or missing (no paths).
	Reason string `json:"reason,omitempty"`
	// Tried lists the candidates that failed validation.
	Tried []Rejected `json:"tried,omitempty"`
	// Hint is the one-line way to add a missing or unusable tool.
	Hint string `json:"hint,omitempty"`
}

// Result is what Detect finds. Tools are in the fixed order of Names.
type Result struct {
	SchemaVersion string    `json:"schema_version"`
	DetectedAt    time.Time `json:"detected_at"`
	// GoModCache is the module cache of the Go toolchain found
	// (`go env GOMODCACHE`); "" when there is no usable Go or no such directory.
	GoModCache string `json:"go_mod_cache,omitempty"`
	// GoSettings are the module download settings of the user's Go
	// environment that differ from Go's defaults (`go env`); the dependency
	// fetch follows them. Absent when the user changed none.
	GoSettings *GoSettings `json:"go_settings,omitempty"`
	// NpmRegistry is the registry of the user's npm configuration when it
	// differs from npm's default (`npm config get registry`); the JS
	// dependency fetch uses it. Absent otherwise.
	NpmRegistry string `json:"npm_registry,omitempty"`
	// PythonBothTypeCheckers is true when mypy and pyright were both found:
	// the agent then needs --python-both-type-checkers.
	PythonBothTypeCheckers bool   `json:"python_both_type_checkers"`
	Tools                  []Tool `json:"tools"`
	// ToolchainPath lists the programs the agent's toolchain path offers
	// (absent on Windows without a configured path).
	ToolchainPath *ToolchainPath `json:"toolchain_path,omitempty"`
}

// Tool returns the entry named name.
func (r Result) Tool(name string) (Tool, bool) {
	for _, t := range r.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// BaselineUsable reports whether at least one baseline analyzer of an
// ecosystem (go vet, cppcheck, ruff, ESLint, cargo check, PMD) is usable, that is,
// whether an analysis can produce any tool finding at all.
func (r Result) BaselineUsable() bool {
	for _, name := range []string{"go", "cppcheck", "ruff", "eslint", "cargo", "pmd"} {
		if t, ok := r.Tool(name); ok && t.Status == Found {
			return true
		}
	}
	return false
}

// Explicit is the configuration that always wins over detection. Its fields
// map one-to-one onto the service configuration (agent.go, agent.git,
// agent.go_mod_cache, agent.tools, agent.model.opencode).
type Explicit struct {
	Go         string
	GoModCache string
	Git        string
	OpenCode   string
	// Tools are agent.tools: analyzer tool flag names and "node".
	Tools map[string]string
	// ToolchainPath is agent.toolchain_path ("" is the default path).
	ToolchainPath string
}

func (e Explicit) Path(name string) string {
	switch name {
	case "go":
		return e.Go
	case "git":
		return e.Git
	case "opencode":
		return e.OpenCode
	}
	return e.Tools[name]
}

// Options configure Detect. The zero value detects on this machine.
type Options struct {
	Env      Env
	Explicit Explicit
	// OnlyExplicit searches nothing: a tool without an explicit path is
	// reported not found (agent.auto_detect false).
	OnlyExplicit bool
	// ProbeDir is an existing absolute writable directory for the version
	// probes; "" creates (and removes) a temporary one.
	ProbeDir string
	// Runner runs the version probes; nil uses the native executor.
	Runner executor.Runner
	// GoEnv returns `go env GOMODCACHE`; nil runs the found go. Tests inject.
	GoEnv func(ctx context.Context, goExe string) string
	// GoSettingsEnv returns the user's module download settings; nil runs
	// the found go. Tests inject.
	GoSettingsEnv func(ctx context.Context, goExe string) GoSettings
	// NpmRegistryEnv returns the user's npm registry ("" = npm's default);
	// nil runs the found npm. Tests inject.
	NpmRegistryEnv func(ctx context.Context, node, npm string) string
	// Now stamps the result; nil uses the clock.
	Now func() time.Time
}

// order is the fixed order of Result.Tools.
var order = append([]string{"go", "git", "gh", "node", "opencode", "staticcheck", "errcheck", "gitleaks", "ruff", "mypy", "pyright", "bandit", "lizard",
	"cppcheck", "clang-tidy", "gcc", "eslint", "tsc", "dependency-cruiser", "import-linter", "jscpd", "cargo", "clippy", "java", "pmd", "spotbugs"},
	// The package managers of the dependency fetch.
	append(append([]string{}, jsManagerNames...), pythonManagerNames...)...)

// Names are the tool names of a Result, in order.
func Names() []string { return append([]string{}, order...) }

// maxParallel bounds concurrent version probes.
const maxParallel = 6

// Detect finds every tool. It never fails: a tool it cannot use is reported
// as not found or unusable with the reason.
func Detect(ctx context.Context, opts Options) Result {
	env := opts.Env
	if env.Home == "" && env.Getenv == nil && env.Path == nil {
		env = SystemEnv()
	}
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	runner := opts.Runner
	if runner == nil {
		runner = &executor.Executor{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	probeDir := opts.ProbeDir
	if probeDir == "" {
		dir, err := os.MkdirTemp("", "codeye-toolfind-")
		if err == nil {
			probeDir = dir
			defer os.RemoveAll(dir)
		}
	}
	d := &detector{ctx: ctx, env: env, ex: opts.Explicit, runner: runner, probeDir: probeDir, onlyExplicit: opts.OnlyExplicit, found: map[string]Tool{}}

	// Phase one: tools that need no other tool. Phase two: tools run by node
	// or needing go (Staticcheck), validated against the phase-one results.
	d.run([]string{"go", "git", "gh", "node", "opencode", "cargo", "java", "gitleaks", "ruff", "mypy", "bandit", "import-linter", "lizard", "cppcheck", "clang-tidy", "gcc"})
	d.run([]string{"staticcheck", "errcheck", "pyright", "eslint", "tsc", "dependency-cruiser", "jscpd"})
	d.run([]string{"clippy", "pmd", "spotbugs"})
	// Package managers: the JS ones after node, python after uv (an
	// interpreter without pip is usable with uv).
	d.run(append(append([]string{}, jsManagerNames...), pythonManagerNames[0]))
	d.run(pythonManagerNames[1:])

	res := Result{SchemaVersion: SchemaVersion, DetectedAt: now().UTC().Truncate(time.Second), ToolchainPath: LookToolchainPath(opts.Explicit.ToolchainPath)}
	for _, name := range order {
		res.Tools = append(res.Tools, d.found[name])
	}
	if t := d.found["go"]; t.Status == Found {
		goEnv := opts.GoEnv
		if goEnv == nil {
			goEnv = func(ctx context.Context, exe string) string { return goModCache(ctx, env, exe, probeDir) }
		}
		if dir := goEnv(ctx, t.Path); dir != "" && filepath.IsAbs(dir) && filepath.Clean(dir) == dir {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				res.GoModCache = dir
			}
		}
		settings := opts.GoSettingsEnv
		if settings == nil {
			settings = func(ctx context.Context, exe string) GoSettings { return goSettings(ctx, env, exe, probeDir) }
		}
		if s := settings(ctx, t.Path); s != (GoSettings{}) {
			res.GoSettings = &s
		}
	}
	if opts.Explicit.GoModCache != "" {
		res.GoModCache = opts.Explicit.GoModCache
	}
	if npm, node := d.found["npm"], d.found["node"]; npm.Status == Found && node.Status == Found {
		registry := opts.NpmRegistryEnv
		if registry == nil {
			registry = func(ctx context.Context, n, m string) string { return npmRegistry(ctx, n, m, probeDir) }
		}
		res.NpmRegistry = registryOf(registry(ctx, node.Path, npm.Path))
	}
	res.PythonBothTypeCheckers = d.found["mypy"].Status == Found && d.found["pyright"].Status == Found
	return res
}

// detector is one Detect run.
type detector struct {
	ctx      context.Context
	env      Env
	ex       Explicit
	runner   executor.Runner
	probeDir string
	// onlyExplicit: nothing is searched.
	onlyExplicit bool

	mu    sync.Mutex
	found map[string]Tool
}

func (d *detector) get(name string) Tool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.found[name]
}

func (d *detector) set(t Tool) {
	d.mu.Lock()
	d.found[t.Name] = t
	d.mu.Unlock()
}

// cfg is the builtin configuration of what has been found so far.
func (d *detector) cfg() builtin.Config {
	c := builtin.Config{ProbeDir: d.probeDir, Tools: map[string]string{}}
	d.mu.Lock()
	defer d.mu.Unlock()
	for name, t := range d.found {
		if t.Status != Found {
			continue
		}
		switch name {
		case "go":
			c.GoExecutable = t.Path
		case "git", "gh", "opencode":
		default:
			if isManager(name) {
				// Package managers are not analyzer tools.
				continue
			}
			c.Tools[name] = t.Path
		}
	}
	// An explicit path the validation rejected is still the tool the agent
	// gets; dependants are validated against it like the agent would.
	if p := d.ex.Go; p != "" {
		c.GoExecutable = p
	}
	for name, p := range d.ex.Tools {
		if p != "" && !isManager(name) {
			c.Tools[name] = p
		}
	}
	c.GoModCache = d.ex.GoModCache
	return c
}

func (d *detector) run(names []string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)
	for _, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			d.set(d.detect(name))
		}()
	}
	wg.Wait()
}

// sortedKeys is used by tests and the table.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
