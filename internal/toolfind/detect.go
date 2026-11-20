package toolfind

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

type kind int

const (
	// kindExec is a single executable looked up by name.
	kindExec kind = iota
	// kindJS is a JS entry script under a node_modules directory.
	kindJS
	kindCargo
	kindClippy
	kindGit
	kindOpenCode
	// kindGH is the GitHub CLI: the service asks it for the signed-in
	// account's token (docs/GITHUB.md). It is no analyzer and no agent flag.
	kindGH
)

// spec describes how one tool is found.
type spec struct {
	kind kind
	// bin is the executable name (kindExec, kindGit, kindOpenCode) and the
	// shim name for kindJS.
	bin string
	// entry is the JS entry script relative to node_modules (kindJS).
	entry string
	// hint is how to add the tool.
	hint string
}

const (
	npmPrefix = "~/" + ToolPrefixRel + "/node"
)

func uvHint(tool string) string {
	return "`uv tool install " + tool + "` (or `pipx install " + tool + "`) or set `agent.tools." + tool + "`"
}

func npmHint(tool, pkgs string) string {
	return "`npm install --prefix " + npmPrefix + " " + pkgs + "` or set `agent.tools." + tool + "`"
}

// specs has one entry per name of Names.
var specs = map[string]spec{
	"go":          {kindExec, "go", "", "install Go from https://go.dev/dl or set `agent.go`"},
	"git":         {kindGit, "git", "", "install git (Ubuntu: `sudo apt install git`) or set `agent.git`"},
	"node":        {kindExec, "node", "", "install Node.js from https://nodejs.org or set `agent.tools.node`"},
	"gh":          {kindGH, "gh", "", "install the GitHub CLI (https://cli.github.com) and run `gh auth login`; or set GH_TOKEN for `codeye up`"},
	"opencode":    {kindOpenCode, "opencode", "", "install OpenCode (`curl -fsSL https://opencode.ai/install | bash`) or set `agent.model.opencode`"},
	"staticcheck": {kindExec, "staticcheck", "", "`go install honnef.co/go/tools/cmd/staticcheck@latest` or set `agent.tools.staticcheck`"},
	"gitleaks":    {kindExec, "gitleaks", "", "download a release from https://github.com/gitleaks/gitleaks/releases into ~/" + ToolPrefixRel + "/bin or set `agent.tools.gitleaks`"},
	"ruff":        {kindExec, "ruff", "", uvHint("ruff")},
	"mypy":        {kindExec, "mypy", "", uvHint("mypy")},
	"bandit":      {kindExec, "bandit", "", uvHint("bandit")},
	"lizard":      {kindExec, "lizard", "", uvHint("lizard")},
	"cppcheck":    {kindExec, "cppcheck", "", "`micromamba install -p ~/" + ToolPrefixRel + "/cpp cppcheck` (or `sudo apt install cppcheck`) or set `agent.tools.cppcheck`"},
	"clang-tidy":  {kindExec, "clang-tidy", "", "`micromamba install -p ~/" + ToolPrefixRel + "/cpp clang-tools` (or `sudo apt install clang-tidy`) or set `agent.tools.clang-tidy`"},
	"pyright":     {kindJS, "pyright", "pyright/index.js", npmHint("pyright", "pyright")},
	"eslint":      {kindJS, "eslint", "eslint/bin/eslint.js", npmHint("eslint", "eslint @eslint/js typescript-eslint")},
	"tsc":         {kindJS, "tsc", "typescript/bin/tsc", npmHint("tsc", "typescript")},
	"jscpd":       {kindJS, "jscpd", "jscpd/run-jscpd.js", npmHint("jscpd", "jscpd")},
	"cargo":       {kindCargo, "cargo", "", "install Rust with rustup (https://rustup.rs) or set `agent.tools.cargo` to a toolchain's own bin/cargo (not the rustup proxy)"},
	"clippy":      {kindClippy, "cargo-clippy", "", "`rustup component add clippy` or set `agent.tools.clippy`"},
}

// candidate is one path to validate and the place it came from.
type candidate struct{ path, source string }

// candidates lists the paths to try for name, best location first. Candidates
// that resolve to the same file are listed once.
func (d *detector) candidates(name string, sp spec) []candidate {
	var out []candidate
	seen := map[string]bool{}
	add := func(path, source string) {
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			real = path
		}
		if !seen[real] {
			seen[real] = true
			out = append(out, candidate{filepath.Clean(path), source})
		}
	}
	switch sp.kind {
	case kindExec, kindGit, kindOpenCode, kindGH:
		for _, dir := range d.env.execDirs() {
			for _, n := range exeNames(sp.bin) {
				if p := filepath.Join(dir.path, n); isExecutable(p) {
					add(p, dir.label)
					break
				}
			}
		}
		if name == "go" {
			if goroot := d.env.getenv("GOROOT"); goroot != "" && filepath.IsAbs(goroot) {
				if p := filepath.Join(goroot, "bin", exeNames("go")[0]); isExecutable(p) {
					add(p, "GOROOT")
				}
			}
		}
	case kindJS:
		node := d.get("node").Path
		for _, dir := range d.env.moduleDirs(node) {
			if p := filepath.Join(dir.path, filepath.FromSlash(sp.entry)); isRegular(p) {
				add(p, dir.label)
			}
		}
		// A shim on PATH (npm's global bin) points at the entry script.
		suffix := string(filepath.Separator) + filepath.FromSlash(sp.entry)
		for _, dir := range d.env.execDirs() {
			for _, n := range exeNames(sp.bin) {
				p := filepath.Join(dir.path, n)
				if real, err := filepath.EvalSymlinks(p); err == nil && strings.HasSuffix(real, suffix) && isRegular(real) {
					add(real, dir.label)
					break
				}
			}
		}
	case kindCargo:
		for _, tc := range d.rustToolchains() {
			p := filepath.Join(tc.path, "bin", exeNames("cargo")[0])
			if isExecutable(p) {
				add(p, tc.label)
			}
		}
		// A cargo that is not a rustup toolchain's own (a distribution package).
		for _, dir := range d.env.execDirs() {
			for _, n := range exeNames("cargo") {
				if p := filepath.Join(dir.path, n); isExecutable(p) {
					add(p, dir.label)
					break
				}
			}
		}
	case kindClippy:
		if cargo := d.cargoPath(); cargo != "" {
			if real, err := filepath.EvalSymlinks(cargo); err == nil {
				cargo = real
			}
			if p := filepath.Join(filepath.Dir(cargo), exeNames("cargo-clippy")[0]); isExecutable(p) {
				add(p, d.get("cargo").Source)
			}
		}
	}
	return out
}

// cargoPath is the cargo clippy is validated against: the explicit one, else
// the detected one.
func (d *detector) cargoPath() string {
	if p := d.ex.Tools["cargo"]; p != "" {
		return p
	}
	if t := d.get("cargo"); t.Status == Found {
		return t.Path
	}
	return ""
}

var defaultToolchain = regexp.MustCompile(`(?m)^\s*default_toolchain\s*=\s*"([^"]+)"`)

// rustToolchains lists the rustup toolchain directories: the default one
// from settings.toml first, then the others, newest name first. The proxies in
// ~/.cargo/bin are not among them.
func (d *detector) rustToolchains() []dir {
	home := d.env.rustupHome()
	if home == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(home, "toolchains"))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	def := ""
	if raw, err := os.ReadFile(filepath.Join(home, "settings.toml")); err == nil {
		if m := defaultToolchain.FindSubmatch(raw); m != nil {
			want := string(m[1])
			for _, n := range names {
				if n == want || strings.HasPrefix(n, want+"-") {
					def = n
					break
				}
			}
		}
	}
	var out []dir
	if def != "" {
		out = append(out, dir{filepath.Join(home, "toolchains", def), "rustup default"})
	}
	for _, n := range names {
		if n != def {
			out = append(out, dir{filepath.Join(home, "toolchains", n), "rustup"})
		}
	}
	return out
}

// outcome is one candidate's validation.
type outcome struct {
	ok      bool
	version string
	tested  bool
	series  string
	reason  string
}

// validate runs the agent's own validation of path as the tool name.
func (d *detector) validate(name string, sp spec, path string) outcome {
	if !filepath.IsAbs(path) {
		return outcome{reason: "path must be absolute"}
	}
	switch sp.kind {
	case kindGit:
		v, err := d.version(path, "--version")
		if err != "" {
			return outcome{reason: err}
		}
		m := gitVersion.FindStringSubmatch(v)
		if m == nil {
			return outcome{reason: "version output not recognized"}
		}
		return outcome{ok: true, version: m[1], tested: true}
	case kindGH:
		v, err := d.version(path, "--version")
		if err != "" {
			return outcome{reason: err}
		}
		m := ghVersion.FindStringSubmatch(v)
		if m == nil {
			return outcome{reason: "version output not recognized"}
		}
		return outcome{ok: true, version: m[1], tested: true}
	case kindOpenCode:
		v, err := d.version(path, "--version")
		if err != "" {
			return outcome{reason: err}
		}
		m := plainVersion.FindStringSubmatch(v)
		if m == nil {
			return outcome{reason: "version output not recognized"}
		}
		return outcome{ok: true, version: m[1], tested: m[1] == opencode.Version, series: opencode.Version}
	}
	if out, ok := d.validateManager(name, path); ok {
		return out
	}
	cfg := d.cfg()
	if name == "go" {
		cfg.GoExecutable = path
	} else {
		cfg.Tools[name] = path
	}
	chk := builtin.CheckTool(d.ctx, cfg, d.runner, name)
	if !chk.OK() {
		return outcome{reason: failure(chk), series: chk.Series}
	}
	return outcome{ok: true, version: shortVersion(chk.Version), tested: chk.Tested, series: chk.Series}
}

// failure turns a check failure into a one-line reason.
func failure(c builtin.ToolCheck) string {
	switch c.Reason {
	case "toolchain_not_configured":
		// "pyright needs --node": the toolchain was not found or is unusable.
		_, need, _ := strings.Cut(c.Detail, "--")
		return "needs " + need + " (not found or unusable)"
	}
	if c.Detail != "" {
		return c.Detail
	}
	return c.Reason
}

// shortVersion keeps the version number of a probe's version string
// ("1.99.1 (5f94df478 2026-08-27)" is 1.99.1, "go1.27.4" is 1.27.4).
func shortVersion(v string) string {
	f := strings.Fields(v)
	if len(f) == 0 {
		return ""
	}
	if rest, ok := strings.CutPrefix(f[0], "go"); ok && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
		return rest
	}
	return f[0]
}

var (
	gitVersion   = regexp.MustCompile(`^git version ([0-9]+\.[0-9]+(?:\.[0-9]+)?)`)
	ghVersion    = regexp.MustCompile(`^gh version ([0-9]+\.[0-9]+\.[0-9]+)`)
	plainVersion = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)$`)
)

// version runs `<exe> args` and returns the trimmed first output line.
func (d *detector) version(exe string, args ...string) (string, string) {
	if d.probeDir == "" {
		return "", "no probe directory"
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", "cannot be resolved"
	}
	work, err := os.MkdirTemp(d.probeDir, "tool-probe-")
	if err != nil {
		return "", "probe directory is not writable"
	}
	defer os.RemoveAll(work)
	ws := filepath.Join(work, "ws")
	if err := os.Mkdir(ws, 0o700); err != nil {
		return "", "probe directory is not writable"
	}
	env := map[string]string{"PATH": filepath.Dir(resolved), "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			env["SystemRoot"] = v
		}
	}
	res := d.runner.Run(d.ctx, executor.Spec{Executable: exe, Args: args, WorkspaceRoot: ws, Dir: ws, TempParent: work,
		Env: env, Timeout: 30 * time.Second, StdoutLimit: 64 << 10, StderrLimit: 64 << 10})
	switch {
	case res.Cleanup == executor.CleanupUncertain:
		return "", "version probe cleanup uncertain"
	case res.Status != executor.StatusExited || res.ExitCode == nil || *res.ExitCode != 0:
		return "", "version probe did not succeed"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(res.Stdout.Data)), "\n")
	return strings.TrimSpace(line), ""
}

// detect finds one tool.
func (d *detector) detect(name string) Tool {
	sp, ok := specs[name]
	if !ok {
		sp = managerSpecs[name]
	}
	t := Tool{Name: name, Status: NotFound, Hint: sp.hint, VersionTested: false}
	var cands []candidate
	if p := d.ex.Path(name); p != "" {
		cands = []candidate{{filepath.Clean(p), SourceConfig}}
		if !filepath.IsAbs(p) {
			cands[0].path = p
		}
	} else if !d.onlyExplicit {
		cands = d.candidates(name, sp)
	}
	if sp.kind == kindClippy && len(cands) == 0 {
		if d.cargoPath() == "" {
			t.Reason = "needs cargo"
		}
		return t
	}
	var chosen *Tool
	var untested *Tool
	for _, c := range cands {
		out := d.validate(name, sp, c.path)
		t.TestedSeries = firstNonEmpty(t.TestedSeries, out.series)
		if !out.ok {
			t.Tried = append(t.Tried, Rejected{Path: tilde(d.env.Home, c.path), Reason: out.reason})
			continue
		}
		found := Tool{Name: name, Status: Found, Path: c.path, Version: out.version, VersionTested: out.tested, TestedSeries: out.series, Source: c.source}
		if real, err := filepath.EvalSymlinks(c.path); err == nil && real != c.path {
			found.ResolvedPath = real
		}
		if out.tested {
			chosen = &found
			break
		}
		if untested == nil {
			untested = &found
		}
	}
	if chosen == nil {
		chosen = untested
	}
	if chosen != nil {
		chosen.Notes = nil
		if !chosen.VersionTested {
			chosen.Notes = []string{NoteVersionUntested}
		}
		chosen.Tried = t.Tried
		return *chosen
	}
	if len(t.Tried) > 0 {
		t.Status = Unusable
		t.Reason = t.Tried[0].Reason
		if len(cands) == 1 && cands[0].source == SourceConfig {
			t.Path, t.Source = cands[0].path, SourceConfig
		}
	}
	return t
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
