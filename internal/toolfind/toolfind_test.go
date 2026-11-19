package toolfind

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// The fake tool trees below are shell scripts that answer their version
// command, so the tests do not depend on what is installed on the machine
// (Linux and macOS only).

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script tools")
	}
}

func write(t *testing.T, path, body string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// printfLines is a shell builtin command printing out (probes run with
// nothing but the tool's own directory on PATH).
func printfLines(out string) string {
	args := ""
	for _, l := range strings.Split(out, "\n") {
		args += " '" + l + "'"
	}
	return "printf '%s\\n'" + args + "\n"
}

// script writes an executable that prints out for any argument.
func script(t *testing.T, path, out string) string {
	t.Helper()
	return write(t, path, "#!/bin/sh\n"+printfLines(out), 0o755)
}

// versionOutputs are the version commands' output of the supported series.
var versionOutputs = map[string]string{
	"go":          "go version go1.27.4 linux/amd64",
	"staticcheck": "staticcheck 2026.2.2 (0.8.2)",
	"gitleaks":    "v8.30.1",
	"ruff":        "ruff 0.16.3",
	"mypy":        "mypy 2.4.1 (compiled: yes)",
	"bandit":      "bandit 1.9.4\n  python version = 3.12.3",
	"lizard":      "1.26.1",
	"cppcheck":    "Cppcheck 2.22.0",
	"clang-tidy":  "LLVM (http://llvm.org/):\n  LLVM version 23.1.5\n  Optimized build.",
	"pyright":     "pyright 1.1.414",
	"eslint":      "v10.15.0",
	"tsc":         "Version 6.0.3",
	"jscpd":       "jscpd 5.4.3",
	"npm":         "11.16.0",
	"pnpm":        "11.28.13",
	"yarn":        "1.22.22",
}

// fakeNode is a node whose "scripts" are shell scripts: it runs the entry
// script it is given with sh.
func fakeNode(t *testing.T, path string) string {
	return write(t, path, "#!/bin/sh\nif [ \"$1\" = --version ]; then echo v24.18.0; exit 0; fi\nexec /bin/sh \"$@\"\n", 0o755)
}

// tree is a fake machine.
type tree struct {
	home string
	env  Env
}

func newTree(t *testing.T) *tree {
	t.Helper()
	skipWindows(t)
	home := t.TempDir()
	return &tree{home: home, env: Env{Home: home, Getenv: func(string) string { return "" }}}
}

func (tr *tree) tools() string { return filepath.Join(tr.home, filepath.FromSlash(ToolPrefixRel)) }

// pathDir adds a directory to PATH and returns it.
func (tr *tree) pathDir(t *testing.T, name string) string {
	t.Helper()
	d := filepath.Join(tr.home, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	tr.env.Path = append(tr.env.Path, d)
	return d
}

// npmTool writes an npm tool's entry script below <nm>.
func npmTool(t *testing.T, nm, entry, out string) string {
	t.Helper()
	pkg, _, _ := strings.Cut(entry, "/")
	write(t, filepath.Join(nm, pkg, "package.json"), `{"name":"`+pkg+`","version":"1.0.0"}`, 0o644)
	return write(t, filepath.Join(nm, filepath.FromSlash(entry)), "#!/usr/bin/env node\n"+printfLines(out), 0o755)
}

// rustToolchain writes a rustup toolchain bin directory.
func rustToolchain(t *testing.T, rustup, name string, clippy bool) string {
	t.Helper()
	bin := filepath.Join(rustup, "toolchains", name, "bin")
	script(t, filepath.Join(bin, "cargo"), "cargo 1.99.1 (5f94df478 2026-08-27)")
	write(t, filepath.Join(bin, "rustc"), "#!/bin/sh\nprintf 'rustc 1.99.1 (58cf8f9c3 2026-10-13)\\nhost: x86_64-unknown-linux-gnu\\nrelease: 1.99.1\\n'\n", 0o755)
	if clippy {
		script(t, filepath.Join(bin, "cargo-clippy"), "clippy 0.1.99 (58cf8f9c35 2026-10-13)")
		script(t, filepath.Join(bin, "clippy-driver"), "")
	}
	return bin
}

func detect(t *testing.T, tr *tree, ex Explicit) Result {
	t.Helper()
	probe := t.TempDir()
	res := Detect(context.Background(), Options{Env: tr.env, Explicit: ex, ProbeDir: probe,
		GoEnv: func(context.Context, string) string { return "" }, NpmRegistryEnv: func(context.Context, string, string) string { return "" },
		Now: func() time.Time { return time.Unix(0, 0) }})
	if entries, _ := os.ReadDir(probe); len(entries) != 0 {
		t.Fatalf("probes left %v", entries)
	}
	return res
}

func mustTool(t *testing.T, res Result, name string) Tool {
	t.Helper()
	tool, ok := res.Tool(name)
	if !ok {
		t.Fatalf("no result for %s", name)
	}
	return tool
}

// A fully equipped fake machine: every tool in the place the documentation
// names. Each one must be found, in that place, with its version.
func TestDetectFindsEveryToolInItsLocation(t *testing.T) {
	tr := newTree(t)
	usrbin := tr.pathDir(t, "usr/bin")
	script(t, filepath.Join(usrbin, "git"), "git version 2.53.0")
	script(t, filepath.Join(usrbin, "gh"), "gh version 2.86.0 (2026-02-14)")
	// go must sit in a GOROOT.
	goroot := filepath.Join(tr.home, "goroot")
	if err := os.MkdirAll(filepath.Join(goroot, "src", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	script(t, filepath.Join(goroot, "bin", "go"), versionOutputs["go"])
	tr.env.Path = append(tr.env.Path, filepath.Join(goroot, "bin"))
	// node and its global npm modules (tsc lives there, next to node).
	nodeBin := filepath.Join(tr.home, "node-prefix", "bin")
	fakeNode(t, filepath.Join(nodeBin, "node"))
	tr.env.Path = append(tr.env.Path, nodeBin)
	globalModules := filepath.Join(tr.home, "node-prefix", "lib", "node_modules")
	npmTool(t, globalModules, "jscpd/run-jscpd.js", versionOutputs["jscpd"])
	// npm comes with node; pnpm and yarn are in the CODEYE tool prefix.
	npmTool(t, globalModules, "npm/bin/npm-cli.js", versionOutputs["npm"])
	// ~/.local/bin, ~/.opencode/bin and the CODEYE tool prefix.
	script(t, filepath.Join(tr.home, ".local", "bin", "ruff"), versionOutputs["ruff"])
	script(t, filepath.Join(tr.home, ".opencode", "bin", "opencode"), "1.19.5")
	tools := tr.tools()
	for _, n := range []string{"mypy", "bandit", "lizard"} {
		script(t, filepath.Join(tools, "bin", n), versionOutputs[n])
	}
	for _, n := range []string{"cppcheck", "clang-tidy"} {
		script(t, filepath.Join(tools, "cpp", "bin", n), versionOutputs[n])
	}
	for _, n := range []string{"staticcheck", "gitleaks"} {
		script(t, filepath.Join(tools, "go", "bin", n), versionOutputs[n])
	}
	nm := filepath.Join(tools, "node", "node_modules")
	npmTool(t, nm, "pyright/index.js", versionOutputs["pyright"])
	npmTool(t, nm, "eslint/bin/eslint.js", versionOutputs["eslint"])
	for _, p := range []string{"@eslint/js", "typescript-eslint"} {
		write(t, filepath.Join(nm, filepath.FromSlash(p), "package.json"), `{"name":"`+p+`","version":"1.0.0"}`, 0o644)
		write(t, filepath.Join(nm, filepath.FromSlash(p), "index.js"), "", 0o644)
	}
	npmTool(t, nm, "typescript/bin/tsc", versionOutputs["tsc"])
	npmTool(t, nm, "pnpm/bin/pnpm.cjs", versionOutputs["pnpm"])
	npmTool(t, nm, "yarn/bin/yarn.js", versionOutputs["yarn"])
	write(t, filepath.Join(nm, "typescript", "index.js"), "", 0o644)
	// rustup: the default toolchain, not the proxy.
	rustup := filepath.Join(tr.home, ".rustup")
	write(t, filepath.Join(rustup, "settings.toml"), "version = \"12\"\ndefault_toolchain = \"stable-x86_64-unknown-linux-gnu\"\n", 0o644)
	stable := rustToolchain(t, rustup, "stable-x86_64-unknown-linux-gnu", true)
	rustToolchain(t, rustup, "1.98.1-x86_64-unknown-linux-gnu", true)
	cargoBin := tr.pathDir(t, ".cargo/bin")
	script(t, filepath.Join(cargoBin, "cargo"), "cargo 1.99.1 (proxy)")
	script(t, filepath.Join(cargoBin, "rustup"), "rustup 1.28.0")
	// The Python package managers: uv in ~/.local/bin, the interpreter on
	// PATH (its fake pip answers nothing, which uv makes acceptable).
	script(t, filepath.Join(tr.home, ".local", "bin", "uv"), "uv 0.12.5 (x86_64-unknown-linux-gnu)")
	script(t, filepath.Join(usrbin, "python3"), "Python 3.14.4")

	res := detect(t, tr, Explicit{})
	want := map[string]struct{ path, version, source string }{
		"go":          {filepath.Join(goroot, "bin", "go"), "1.27.4", SourcePath},
		"git":         {filepath.Join(usrbin, "git"), "2.53.0", SourcePath},
		"gh":          {filepath.Join(usrbin, "gh"), "2.86.0", SourcePath},
		"node":        {filepath.Join(nodeBin, "node"), "24.18.0", SourcePath},
		"opencode":    {filepath.Join(tr.home, ".opencode", "bin", "opencode"), "1.19.5", "~/.opencode/bin"},
		"ruff":        {filepath.Join(tr.home, ".local", "bin", "ruff"), "0.16.3", "~/.local/bin"},
		"mypy":        {filepath.Join(tools, "bin", "mypy"), "2.4.1", "codeye-tools/bin"},
		"bandit":      {filepath.Join(tools, "bin", "bandit"), "1.9.4", "codeye-tools/bin"},
		"lizard":      {filepath.Join(tools, "bin", "lizard"), "1.26.1", "codeye-tools/bin"},
		"cppcheck":    {filepath.Join(tools, "cpp", "bin", "cppcheck"), "2.22.0", "codeye-tools/cpp/bin"},
		"clang-tidy":  {filepath.Join(tools, "cpp", "bin", "clang-tidy"), "23.1.5", "codeye-tools/cpp/bin"},
		"staticcheck": {filepath.Join(tools, "go", "bin", "staticcheck"), "2026.2.2", "codeye-tools/go/bin"},
		"gitleaks":    {filepath.Join(tools, "go", "bin", "gitleaks"), "8.30.1", "codeye-tools/go/bin"},
		"pyright":     {filepath.Join(nm, "pyright", "index.js"), "1.1.414", "codeye-tools/node"},
		"eslint":      {filepath.Join(nm, "eslint", "bin", "eslint.js"), "10.15.0", "codeye-tools/node"},
		"jscpd":       {filepath.Join(globalModules, "jscpd", "run-jscpd.js"), "5.4.3", "node installation"},
		"tsc":         {filepath.Join(nm, "typescript", "bin", "tsc"), "6.0.3", "codeye-tools/node"},
		"cargo":       {filepath.Join(stable, "cargo"), "1.99.1", "rustup default"},
		"clippy":      {filepath.Join(stable, "cargo-clippy"), "0.1.99", "rustup default"},
		"npm":         {filepath.Join(globalModules, "npm", "bin", "npm-cli.js"), "11.16.0", "node installation"},
		"pnpm":        {filepath.Join(nm, "pnpm", "bin", "pnpm.cjs"), "11.28.13", "codeye-tools/node"},
		"yarn":        {filepath.Join(nm, "yarn", "bin", "yarn.js"), "1.22.22", "codeye-tools/node"},
		"uv":          {filepath.Join(tr.home, ".local", "bin", "uv"), "0.12.5", "~/.local/bin"},
		"python":      {filepath.Join(usrbin, "python3"), "3.14.4", SourcePath},
	}
	if len(res.Tools) != len(Names()) || len(want) != len(Names()) {
		t.Fatalf("%d tools, %d expected, %d names", len(res.Tools), len(want), len(Names()))
	}
	for name, w := range want {
		got := mustTool(t, res, name)
		if got.Status != Found || got.Path != w.path || got.Version != w.version || !got.VersionTested || got.Source != w.source || len(got.Notes) != 0 || got.Hint != "" {
			t.Errorf("%s: %+v, want %+v", name, got, w)
		}
	}
	if !res.PythonBothTypeCheckers || !res.BaselineUsable() {
		t.Fatalf("both=%v baseline=%v", res.PythonBothTypeCheckers, res.BaselineUsable())
	}
	if res.SchemaVersion != SchemaVersion || !slices.Equal(toolNames(res), Names()) {
		t.Fatalf("result %v", toolNames(res))
	}
}

func toolNames(res Result) []string {
	var out []string
	for _, t := range res.Tools {
		out = append(out, t.Name)
	}
	return out
}

// An empty machine: every tool is missing with a one-line way to add it, and
// nothing counts as usable.
func TestDetectEmptyMachine(t *testing.T) {
	tr := newTree(t)
	res := detect(t, tr, Explicit{})
	for _, tool := range res.Tools {
		if tool.Status != NotFound || tool.Hint == "" || tool.Path != "" || tool.Version != "" {
			t.Errorf("%+v", tool)
		}
	}
	if res.BaselineUsable() || res.PythonBothTypeCheckers || res.GoModCache != "" {
		t.Fatalf("%+v", res)
	}
	if h := mustTool(t, res, "cppcheck").Hint; !strings.Contains(h, "micromamba install") || !strings.Contains(h, "agent.tools.cppcheck") {
		t.Fatalf("hint %q", h)
	}
	found, missing, unusable := res.Summary()
	if found != 0 || missing != len(Names()) || unusable != 0 {
		t.Fatal(found, missing, unusable)
	}
}

// A usable tool of an untested series is used and says so; a candidate whose
// version is not recognized is skipped for the next location, and with no
// other one the tool is unusable with the reasons.
func TestDetectVersionHandling(t *testing.T) {
	tr := newTree(t)
	a := tr.pathDir(t, "a")
	b := tr.pathDir(t, "b")
	// ruff: untested in the first place, tested in the second: the tested wins.
	script(t, filepath.Join(a, "ruff"), "ruff 0.15.2")
	script(t, filepath.Join(b, "ruff"), versionOutputs["ruff"])
	// lizard: only an untested one: used, with the note.
	script(t, filepath.Join(a, "lizard"), "1.20.0")
	// cppcheck: garbage first, a good one second.
	script(t, filepath.Join(a, "cppcheck"), "not a version")
	script(t, filepath.Join(b, "cppcheck"), versionOutputs["cppcheck"])
	// gitleaks: only garbage.
	script(t, filepath.Join(a, "gitleaks"), "dev build")
	res := detect(t, tr, Explicit{})
	if r := mustTool(t, res, "ruff"); r.Status != Found || r.Path != filepath.Join(b, "ruff") || r.Version != "0.16.3" || !r.VersionTested || len(r.Notes) != 0 {
		t.Errorf("ruff %+v", r)
	}
	if l := mustTool(t, res, "lizard"); l.Status != Found || l.Version != "1.20.0" || l.VersionTested || !slices.Equal(l.Notes, []string{NoteVersionUntested}) || l.TestedSeries != "1.26" {
		t.Errorf("lizard %+v", l)
	}
	if c := mustTool(t, res, "cppcheck"); c.Status != Found || c.Path != filepath.Join(b, "cppcheck") || len(c.Tried) != 1 {
		t.Errorf("cppcheck %+v", c)
	}
	g := mustTool(t, res, "gitleaks")
	if g.Status != Unusable || g.Reason == "" || len(g.Tried) != 1 || g.Hint == "" || g.Path != "" {
		t.Errorf("gitleaks %+v", g)
	}
	if strings.Contains(g.Tried[0].Path, tr.home) {
		t.Errorf("tried path not shortened: %s", g.Tried[0].Path)
	}
}

// The rustup proxy is never taken: with only the proxy on PATH cargo is
// unusable and says why; a toolchain directory is taken over the proxy even
// when the proxy comes first on PATH.
func TestDetectCargoNeverTakesTheRustupProxy(t *testing.T) {
	tr := newTree(t)
	cargoBin := tr.pathDir(t, ".cargo/bin")
	script(t, filepath.Join(cargoBin, "cargo"), "cargo 1.99.1 (proxy)")
	script(t, filepath.Join(cargoBin, "rustc"), "rustc 1.99.1 (proxy)")
	script(t, filepath.Join(cargoBin, "rustup"), "rustup 1.28.0")
	res := detect(t, tr, Explicit{})
	c := mustTool(t, res, "cargo")
	if c.Status != Unusable || !strings.Contains(c.Reason, "rustup") || mustTool(t, res, "clippy").Status != NotFound {
		t.Fatalf("proxy only: %+v", c)
	}
	rustup := filepath.Join(tr.home, ".rustup")
	bin := rustToolchain(t, rustup, "stable-x86_64-unknown-linux-gnu", false)
	res = detect(t, tr, Explicit{})
	c = mustTool(t, res, "cargo")
	if c.Status != Found || c.Path != filepath.Join(bin, "cargo") || c.Source != "rustup" {
		t.Fatalf("toolchain: %+v", c)
	}
	// This toolchain has no clippy: it is not taken from elsewhere.
	if cl := mustTool(t, res, "clippy"); cl.Status != NotFound || !strings.Contains(cl.Hint, "rustup component add clippy") {
		t.Fatalf("clippy %+v", cl)
	}
}

// Explicit configuration wins: an explicit path is the only candidate even
// when a better tool is on PATH, and an explicit path that fails validation
// is reported unusable instead of being replaced.
func TestExplicitConfigurationWins(t *testing.T) {
	tr := newTree(t)
	a := tr.pathDir(t, "a")
	script(t, filepath.Join(a, "ruff"), versionOutputs["ruff"])
	other := script(t, filepath.Join(tr.home, "elsewhere", "ruff"), "ruff 0.15.0")
	bad := script(t, filepath.Join(tr.home, "elsewhere", "lizard"), "garbage")
	script(t, filepath.Join(a, "lizard"), versionOutputs["lizard"])
	ex := Explicit{Tools: map[string]string{"ruff": other, "lizard": bad}}
	res := detect(t, tr, ex)
	r := mustTool(t, res, "ruff")
	if r.Status != Found || r.Path != other || r.Source != SourceConfig || r.VersionTested {
		t.Fatalf("ruff %+v", r)
	}
	l := mustTool(t, res, "lizard")
	if l.Status != Unusable || l.Path != bad || l.Source != SourceConfig {
		t.Fatalf("lizard %+v", l)
	}
	got := Resolve(res, ex)
	if got.Tools["ruff"] != other || got.Tools["lizard"] != bad || got.Sources["lizard"] != SourceConfig {
		t.Fatalf("resolved %+v", got)
	}
}

func TestResolveAndArgs(t *testing.T) {
	cache := t.TempDir()
	res := Result{GoModCache: cache, Tools: []Tool{
		{Name: "go", Status: Found, Path: "/d/go", Source: SourcePath},
		{Name: "git", Status: Found, Path: "/d/git"},
		{Name: "opencode", Status: Found, Path: "/d/opencode"},
		{Name: "node", Status: Found, Path: "/d/node"},
		{Name: "mypy", Status: Found, Path: "/d/mypy"},
		{Name: "pyright", Status: Found, Path: "/d/pyright/index.js"},
		{Name: "ruff", Status: Unusable, Reason: "x"},
		{Name: "lizard", Status: NotFound},
		{Name: "bandit", Status: Found, Path: "/d/bandit", VersionTested: false},
	}}
	r := Resolve(res, Explicit{Tools: map[string]string{"lizard": "/x/lizard"}})
	if r.Go != "/d/go" || r.GoModCache != cache || r.Git != "/d/git" || r.OpenCode != "/d/opencode" || !r.PythonBothTypeCheckers {
		t.Fatalf("%+v", r)
	}
	if _, ok := r.Tools["ruff"]; ok {
		t.Fatal("an unusable detected tool must not be passed on")
	}
	want := []string{"--go", "/d/go", "--go-mod-cache", cache, "--bandit", "/d/bandit", "--lizard", "/x/lizard", "--mypy", "/d/mypy",
		"--node", "/d/node", "--pyright", "/d/pyright/index.js", "--python-both-type-checkers"}
	if got := r.Args(); !slices.Equal(got, want) {
		t.Fatalf("args %q\nwant %q", got, want)
	}
	// An explicit go and module cache win; the module cache of another go is
	// not mixed in when there is no go.
	r = Resolve(res, Explicit{Go: "/mine/go", GoModCache: "/mine/mod"})
	if r.Go != "/mine/go" || r.GoModCache != "/mine/mod" || r.Sources["go"] != SourceConfig {
		t.Fatalf("%+v", r)
	}
	noGo := Result{GoModCache: cache, Tools: []Tool{{Name: "go", Status: NotFound}}}
	if r = Resolve(noGo, Explicit{}); r.Go != "" || r.GoModCache != "" || slices.Contains(r.Args(), "--go-mod-cache") {
		t.Fatalf("%+v", r)
	}
	// One type checker alone never turns the flag on, also when it comes
	// from the configuration (the agent refuses every other combination).
	r = Resolve(Result{}, Explicit{Tools: map[string]string{"mypy": "/x/mypy"}})
	if r.PythonBothTypeCheckers || slices.Contains(r.Args(), "--python-both-type-checkers") {
		t.Fatalf("%+v", r)
	}
	r = Resolve(Result{}, Explicit{Tools: map[string]string{"mypy": "/x/mypy", "pyright": "/x/pyright"}})
	if !r.PythonBothTypeCheckers {
		t.Fatalf("%+v", r)
	}
}

func TestWriteReadAndTable(t *testing.T) {
	home := t.TempDir()
	bin := func(name string) string { return filepath.Join(home, "bin", name) }
	res := Result{SchemaVersion: SchemaVersion, DetectedAt: time.Unix(1700000000, 0).UTC(), GoModCache: filepath.Join(home, "go", "pkg", "mod"), PythonBothTypeCheckers: true, Tools: []Tool{
		{Name: "go", Status: Found, Path: bin("go"), Version: "1.27.4", VersionTested: true, TestedSeries: "1.27", Source: SourcePath},
		{Name: "ruff", Status: Found, Path: bin("ruff"), Version: "0.15.0", TestedSeries: "0.16", Source: SourceConfig, Notes: []string{NoteVersionUntested}},
		{Name: "cppcheck", Status: NotFound, Hint: specs["cppcheck"].hint},
		{Name: "gitleaks", Status: Unusable, Reason: "version output not recognized", Hint: specs["gitleaks"].hint},
	}}
	path := filepath.Join(t.TempDir(), FileName)
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Fatalf("%v %v", st, err)
	}
	back, err := Read(path)
	if err != nil || back.GoModCache != res.GoModCache || len(back.Tools) != 4 || back.Tools[1].Notes[0] != NoteVersionUntested || !back.DetectedAt.Equal(res.DetectedAt) {
		t.Fatalf("%+v %v", back, err)
	}
	write(t, path, `{"schema_version":"9"}`, 0o600)
	if _, err := Read(path); err == nil {
		t.Fatal("another schema version must be refused")
	}
	write(t, path, `{"schema_version":"0.1","surprise":1}`, 0o600)
	if _, err := Read(path); err == nil {
		t.Fatal("unknown fields must be refused")
	}
	var b bytes.Buffer
	if err := WriteTable(&b, res, home); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"go  ", "found", "~" + string(filepath.Separator) + filepath.Join("bin", "go"), "[untested version, tested 0.16]", "(config)",
		"cppcheck", "missing", "not found - `micromamba install", "or set `agent.tools.cppcheck`", "unusable", "version output not recognized",
		"go module cache: ~" + string(filepath.Separator) + filepath.Join("go", "pkg", "mod"), "mypy and pyright"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestShortVersion(t *testing.T) {
	for in, want := range map[string]string{"go1.27.4": "1.27.4", "1.99.1 (5f94df478 2026-08-27)": "1.99.1", "2026.2.2 (0.8.2)": "2026.2.2", "24.18.0": "24.18.0", "": "", "gopher": "gopher"} {
		if got := shortVersion(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// The real machine: run only when CODEYE_TEST_REAL_TOOLS lists the tool
// names (comma separated) that must be found; "all" requires every tool.
func TestRealMachine(t *testing.T) {
	want := os.Getenv("CODEYE_TEST_REAL_TOOLS")
	if want == "" {
		t.Skip("set CODEYE_TEST_REAL_TOOLS=go,git,... (or all) to detect on this machine")
	}
	res := Detect(context.Background(), Options{})
	var b bytes.Buffer
	home, _ := os.UserHomeDir()
	_ = WriteTable(&b, res, home)
	t.Log("\n" + b.String())
	names := strings.Split(want, ",")
	if want == "all" {
		names = Names()
	}
	for _, name := range names {
		if tool, ok := res.Tool(strings.TrimSpace(name)); !ok || tool.Status != Found {
			t.Errorf("%s: %+v", name, tool)
		}
	}
	if slices.Contains(names, "go") && res.GoModCache == "" {
		t.Error("no go module cache")
	}
}
