package rustpack

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func TestParseVersions(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"cargo 1.99.1 (5f94df478 2026-08-27)\n", "1.99.1 (5f94df478 2026-08-27)", true},
		{"cargo 1.75.0\n", "1.75.0", true},
		{"cargo 1.99.0-nightly (abcdef123 2026-11-01)", "1.99.0-nightly (abcdef123 2026-11-01)", true},
		{"cargo 1.99.1 (5f94df478 2026-08-27)\nextra\n", "1.99.1 (5f94df478 2026-08-27)", true},
		{"cargo nightly\n", "", false},
		{"rustup 1.28.0\n", "", false},
		{"", "", false},
		{"cargo 1.99\n", "", false},
	} {
		if got, ok := ParseCargoVersion([]byte(tc.in)); ok != tc.ok || got != tc.want {
			t.Errorf("cargo %q = %q %v", tc.in, got, ok)
		}
	}
	if got, ok := ParseClippyVersion([]byte("clippy 0.1.99 (58cf8f9c35 2026-10-13)\n")); !ok || got != "0.1.99 (58cf8f9c35 2026-10-13)" {
		t.Errorf("clippy %q %v", got, ok)
	}
	if _, ok := ParseClippyVersion([]byte("cargo 1.99.1\n")); ok {
		t.Error("clippy accepted cargo output")
	}
	r, ok := ParseRustcVV([]byte("rustc 1.99.1 (58cf8f9c3 2026-10-13)\nbinary: rustc\nhost: x86_64-unknown-linux-gnu\nrelease: 1.99.1\nLLVM version: 23.1.1\n"))
	if !ok || r.Release != "1.99.1" || r.Host != "x86_64-unknown-linux-gnu" {
		t.Errorf("rustc %+v %v", r, ok)
	}
	for _, bad := range []string{"", "rustc 1.99.1\nrelease: 1.99.1\n", "rustc 1.99.1\nhost: x86_64-unknown-linux-gnu\n", "host: x86_64-unknown-linux-gnu\nrelease: 1.99.1\n"} {
		if _, ok := ParseRustcVV([]byte(bad)); ok {
			t.Errorf("rustc accepted %q", bad)
		}
	}
	if !CargoVersionTested("1.99.4 (abc1234 2026-12-01)") || CargoVersionTested("1.100.0") || CargoVersionTested("1.99.0-nightly (abc1234 2026-11-01)") ||
		!ClippyVersionTested("0.1.99 (x)") || ClippyVersionTested("0.1.100") || !RustcVersionTested("1.99.2") || RustcVersionTested("1.98.1") {
		t.Error("version series")
	}
}

func TestParseStreamIsStrict(t *testing.T) {
	good := `{"reason":"build-finished","success":true}` + "\n"
	st := ParseStream([]byte(good))
	if st.ShapeErrors != 0 || st.UnparsedBytes != 0 || len(st.Finished) != 1 || !st.Finished[0] {
		t.Fatalf("good %+v", st)
	}
	for name, in := range map[string]string{
		"not an object":    "[1]\n",
		"no reason":        `{"success":true}` + "\n",
		"unknown reason":   `{"reason":"future","package_id":"x"}` + "\n",
		"unknown field":    `{"reason":"build-finished","success":true,"x":1}` + "\n",
		"missing success":  `{"reason":"build-finished"}` + "\n",
		"artifact lacks":   `{"reason":"compiler-artifact","package_id":"x"}` + "\n",
		"message no level": `{"reason":"compiler-message","package_id":"x","manifest_path":"m","target":{"kind":["lib"],"name":"n"},"message":{"message":"m","code":null,"spans":[],"children":[],"rendered":null,"$message_type":"diagnostic"}}` + "\n",
		"span lacks line":  `{"reason":"compiler-message","package_id":"x","manifest_path":"m","target":{"kind":["lib"],"name":"n"},"message":{"message":"m","level":"warning","code":null,"spans":[{"file_name":"a.rs"}],"children":[],"rendered":null,"$message_type":"diagnostic"}}` + "\n",
	} {
		if s := ParseStream([]byte(in)); s.ShapeErrors != 1 || len(s.Messages)+len(s.Artifacts)+len(s.Finished) != 0 {
			t.Errorf("%s: %+v", name, s)
		}
	}
	if s := ParseStream([]byte(good + `{"reason":`)); s.DecodeError != "unexpected_eof" || s.UnparsedBytes == 0 || len(s.Finished) != 1 {
		t.Errorf("cut %+v", s)
	}
	if s := ParseStream([]byte("garbage")); s.DecodeError == "" || s.UnparsedBytes != 7 {
		t.Errorf("garbage %+v", s)
	}
}

func TestPackageName(t *testing.T) {
	for in, want := range map[string]string{
		"path+file:///tmp/x/macros#0.1.0":                                     "macros",
		"path+file:///tmp/x#findings@0.1.0":                                   "findings",
		"path+file://[REDACTED]#findings@0.1.0":                               "findings",
		"registry+https://github.com/rust-lang/crates.io-index#serde@1.0.219": "serde",
	} {
		if got := PackageName(in); got != want {
			t.Errorf("%s = %q, want %q", in, got, want)
		}
	}
	// Redaction can erase the directory name an id ends with.
	if got := unitName("path+file://[REDACTED]#0.1.0", "[REDACTED]/Cargo.toml"); got != "root" {
		t.Errorf("redacted root = %q", got)
	}
	if got := unitName("path+file://[REDACTED]#0.1.0", "[REDACTED]/crates/app/Cargo.toml"); got != "app" {
		t.Errorf("redacted member = %q", got)
	}
}

func TestClassifyStderrOnCapturedBytes(t *testing.T) {
	for name, check := range map[string]func(StderrFacts) bool{
		"check-nodeps":      func(f StderrFacts) bool { return f.Dependency && !f.LockMissing },
		"check-missinglock": func(f StderrFacts) bool { return f.LockMissing && !f.Dependency },
		"check-buildfail":   func(f StderrFacts) bool { return f.BuildScript },
		"check-findings": func(f StderrFacts) bool {
			return !f.Dependency && !f.LockMissing && !f.BuildScript && !f.Toolchain && !f.Linker && f.Other == 0
		},
		"check-typeerr": func(f StderrFacts) bool { return f.CompileError },
	} {
		if f := ClassifyStderr(readTestdata(t, name+".stderr")); !check(f) {
			t.Errorf("%s: %+v", name, f)
		}
	}
	for in, check := range map[string]func(StderrFacts) bool{
		"error: package `x v0.1.0` cannot be built because it requires rustc 1.88 or newer, while the currently active rustc version is 1.75.0\n": func(f StderrFacts) bool { return f.Toolchain },
		"error: failed to parse manifest at `/w/Cargo.toml`\n":                                             func(f StderrFacts) bool { return f.ManifestInvalid },
		"error: the lock file /w/Cargo.lock needs to be updated but --locked was passed to prevent this\n": func(f StderrFacts) bool { return f.LockOutdated },
		"warning: user-defined alias `check` is ignored, because it is shadowed by a built-in command\n":   func(f StderrFacts) bool { return f.Lines == 1 && f.Other == 0 && !f.Dependency },
	} {
		if f := ClassifyStderr([]byte(in)); !check(f) {
			t.Errorf("%q: %+v", in, f)
		}
	}
}

func TestParseDepInfo(t *testing.T) {
	in := "/s/target/debug/deps/a-1.d: src/lib.rs src/my\\ mod.rs\n\n/s/target/debug/deps/liba-1.rmeta: src/lib.rs src/my\\ mod.rs\n\nsrc/lib.rs:\nsrc/my\\ mod.rs:\n# env-dep:CARGO_PKG_NAME=a\n"
	if got := ParseDepInfo([]byte(in)); !slices.Equal(got, []string{"src/lib.rs", "src/my mod.rs"}) {
		t.Errorf("dep-info %q", got)
	}
}

func TestByteColumn(t *testing.T) {
	for _, tc := range []struct {
		line  string
		col   int
		first bool
		want  int
		ok    bool
	}{
		{"let x = 1;", 5, false, 5, true},
		{"한글 tail", 4, false, 8, true}, // two 3-byte characters and a space precede
		{"한글", 3, false, 7, true},      // one past the last character
		{"한글", 4, false, 0, false},
		{"\tx", 2, false, 2, true},
		{"abc\r", 4, false, 4, true},
		{"\ufeffabc", 2, true, 5, true},  // BOM is not a column
		{"\ufeffabc", 2, false, 4, true}, // not the first line: U+FEFF is a character
	} {
		if got, ok := byteColumn([]byte(tc.line), tc.col, tc.first); ok != tc.ok || got != tc.want {
			t.Errorf("%q col %d = %d %v, want %d %v", tc.line, tc.col, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCategoryPolicy(t *testing.T) {
	for code, want := range map[string]string{
		"unused_variables": "maintainability", "E0308": "correctness", "overflowing_literals": "correctness", "clippy::eq_op": "correctness",
		"clippy::undocumented_unsafe_blocks": "security", "unsafe_code": "security", "clippy::todo": "error_handling", "clippy::panic_in_result_fn": "error_handling",
		"clippy::await_holding_lock": "concurrency", "clippy::redundant_clone": "performance", "clippy::len_zero": "maintainability", "clippy::mem_forget": "resource_lifecycle",
	} {
		if got := category(code); got != want || !slices.Contains(obsdoc.Categories, got) {
			t.Errorf("%s = %s, want %s", code, got, want)
		}
	}
	for code, cat := range categoryOf {
		if !slices.Contains(obsdoc.Categories, cat) {
			t.Errorf("%s has unknown category %s", code, cat)
		}
	}
	for _, l := range ClippyReviewLints {
		if category(l) != "security" && category(l) != "error_handling" {
			t.Errorf("review lint %s has category %s", l, category(l))
		}
	}
}

func TestDescriptorsAndComposite(t *testing.T) {
	for _, d := range []pack.Descriptor{CheckDescriptor(), ClippyDescriptor()} {
		if err := d.Validate(); err != nil {
			t.Fatal(err)
		}
		if d.Ecosystem != "rust" || len(d.Toolchains) != 0 || d.Interpreter != "" {
			t.Fatalf("descriptor %+v", d)
		}
	}
	if !CheckDescriptor().Required || ClippyDescriptor().Required {
		t.Fatal("cargo check is the baseline, clippy is extra depth")
	}
	if CheckDescriptor().Tool == "rust" || ClippyDescriptor().Tool == "rust" {
		t.Fatal("rust is a reserved runtime tool name")
	}
	if _, err := pack.NewComposite(replayAnalyzer(t, KindCheck), replayAnalyzer(t, KindClippy)); err != nil {
		t.Fatal(err)
	}
	if got := ClippyArgs(); got[0] != "clippy" || !slices.Contains(got, "--locked") || !slices.Contains(got, "--offline") || !slices.Contains(got, "--message-format=json") {
		t.Fatalf("clippy args %v", got)
	}
	if !slices.Contains(CheckArgs(), "--locked") || !slices.Contains(CheckArgs(), "--offline") || !slices.Contains(CheckArgs(), "--workspace") || !slices.Contains(CheckArgs(), "--all-targets") {
		t.Fatalf("check args %v", CheckArgs())
	}
	// The recorded selection matches the argv: the validator script compares both.
	for _, s := range CheckSelection {
		if !slices.Contains(CheckArgs(), s) {
			t.Fatalf("selection %s is not in the argv", s)
		}
	}
	sel := ClippySelection()
	for _, l := range ClippyReviewLints {
		if !slices.Contains(sel, l) || !slices.Contains(ClippyArgs(), l) {
			t.Fatalf("review lint %s missing from the selection or argv", l)
		}
	}
}

func planFixture(t *testing.T, files map[string]string, scope domain.Scope) fixture {
	t.Helper()
	return newFixture(t, "", files, scope, false)
}

func keysOf(t *testing.T, k Kind, fx fixture) []string {
	t.Helper()
	tasks, err := planTasks(context.Background(), k, fx.Job)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, task := range tasks {
		out = append(out, task.Spec.Key+"@"+task.Module)
		if !task.RepositoryCode {
			t.Fatalf("a Cargo task must declare repository code: %+v", task)
		}
	}
	return out
}

// TC-115: planning reads the manifest and scope only.
func TestPlanModules(t *testing.T) {
	rs := "pub fn f() {}\n"
	for _, tc := range []struct {
		name  string
		files map[string]string
		scope domain.Scope
		want  []string
		pat   [][]string
	}{
		{"root workspace with members", map[string]string{"Cargo.toml": "[workspace]\n", "crates/a/Cargo.toml": "x", "crates/a/src/lib.rs": rs, "crates/b/Cargo.toml": "x", "crates/b/src/lib.rs": rs}, domain.Scope{},
			[]string{"cargo-check@."}, [][]string{{"."}}},
		{"independent crates", map[string]string{"a/Cargo.toml": "x", "a/src/lib.rs": rs, "b/Cargo.toml": "x", "b/src/lib.rs": rs}, domain.Scope{},
			[]string{"cargo-check.m01@a", "cargo-check.m02@b"}, [][]string{{"."}, {"."}}},
		{"vendored, target, hidden and testdata are not modules", map[string]string{"Cargo.toml": "x", "src/lib.rs": rs, "vendor/dep/Cargo.toml": "x", "vendor/dep/src/lib.rs": rs, "target/debug/x.rs": rs, ".hidden/Cargo.toml": "x", "testdata/Cargo.toml": "x"}, domain.Scope{},
			[]string{"cargo-check@."}, [][]string{{"."}}},
		{"path scope picks the module", map[string]string{"a/Cargo.toml": "x", "a/src/lib.rs": rs, "b/Cargo.toml": "x", "b/src/lib.rs": rs}, domain.Scope{Kind: "paths", Paths: []string{"b"}},
			[]string{"cargo-check.m01@b"}, [][]string{{"."}}},
		{"path scope inside a module", map[string]string{"Cargo.toml": "x", "src/lib.rs": rs, "src/other.rs": rs}, domain.Scope{Kind: "paths", Paths: []string{"src/lib.rs"}},
			[]string{"cargo-check@."}, [][]string{{"src/lib.rs"}}},
		{"scope without Rust", map[string]string{"Cargo.toml": "x", "src/lib.rs": rs, "docs/a.md": "x"}, domain.Scope{Kind: "paths", Paths: []string{"docs"}}, nil, nil},
		{"no rust", map[string]string{"main.go": "package main\n"}, domain.Scope{}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := planFixture(t, tc.files, tc.scope)
			if got := keysOf(t, KindCheck, fx); !slices.Equal(got, tc.want) {
				t.Fatalf("check plan %v, want %v", got, tc.want)
			}
			wantClippy := make([]string, len(tc.want))
			for i, k := range tc.want {
				wantClippy[i] = strings.Replace(k, "cargo-check", "clippy", 1)
			}
			if got := keysOf(t, KindClippy, fx); len(tc.want) == 0 && len(got) != 0 || len(tc.want) > 0 && !slices.Equal(got, wantClippy) {
				t.Fatalf("clippy plan %v, want %v", got, wantClippy)
			}
			tasks, _ := CheckPlan(context.Background(), fx.Job)
			for i, task := range tasks {
				if !slices.Equal(task.Patterns, tc.pat[i]) || !task.RepositoryCode || !task.Spec.Required || task.Spec.Stage != domain.StageStaticAnalysis || task.FilesInScope == nil {
					t.Fatalf("task %+v", task)
				}
			}
			ct, _ := ClippyPlan(context.Background(), fx.Job)
			for _, task := range ct {
				if task.Spec.Required || !task.RepositoryCode {
					t.Fatalf("clippy task %+v", task)
				}
			}
		})
	}
}

func TestNewValidation(t *testing.T) {
	good := fakeToolchain(t)
	cargo, clippy := filepath.Join(good, exeName("cargo")), filepath.Join(good, exeName("cargo-clippy"))
	if _, err := New(KindCheck, Config{Cargo: cargo}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(KindClippy, Config{Cargo: cargo, Clippy: clippy}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(KindCheck, Config{Cargo: "relative/cargo"}); err == nil {
		t.Error("relative path accepted")
	}
	if _, err := New(KindCheck, Config{Cargo: filepath.Join(t.TempDir(), "cargo")}); err == nil {
		t.Error("missing file accepted")
	}
	// A directory that holds rustup is a rustup proxy directory: the proxy
	// would let the repository choose, and download, a toolchain.
	proxy := fakeToolchain(t)
	if err := os.WriteFile(filepath.Join(proxy, exeName("rustup")), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(KindCheck, Config{Cargo: filepath.Join(proxy, exeName("cargo"))}); err == nil || !strings.Contains(err.Error(), "rustup") {
		t.Errorf("rustup proxy directory: %v", err)
	}
	if runtime.GOOS != "windows" {
		// A symlink to rustup (the usual proxy layout) resolves to a binary that is not cargo.
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "rustup"), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "rustup"), filepath.Join(dir, "cargo")); err != nil {
			t.Fatal(err)
		}
		if _, err := New(KindCheck, Config{Cargo: filepath.Join(dir, "cargo")}); err == nil {
			t.Error("symlink to rustup accepted")
		}
		// A symlink to a real cargo is fine: rustc is next to the resolved binary.
		link := filepath.Join(t.TempDir(), "cargo")
		if err := os.Symlink(cargo, link); err != nil {
			t.Fatal(err)
		}
		if _, err := New(KindCheck, Config{Cargo: link}); err != nil {
			t.Errorf("symlink to cargo: %v", err)
		}
	}
	noRustc := fakeToolchain(t)
	if err := os.Remove(filepath.Join(noRustc, exeName("rustc"))); err != nil {
		t.Fatal(err)
	}
	if _, err := New(KindCheck, Config{Cargo: filepath.Join(noRustc, exeName("cargo"))}); err == nil {
		t.Error("cargo without rustc accepted")
	}
	other := fakeToolchain(t)
	if _, err := New(KindClippy, Config{Cargo: cargo, Clippy: filepath.Join(other, exeName("cargo-clippy"))}); err == nil {
		t.Error("clippy of another toolchain accepted")
	}
	if _, err := New(KindClippy, Config{Cargo: cargo, Clippy: filepath.Join(good, exeName("cargo"))}); err == nil {
		t.Error("cargo accepted as clippy")
	}
	if _, err := New(KindClippy, Config{Cargo: cargo}); err == nil {
		t.Error("clippy without a path accepted")
	}
}

func TestFindLinker(t *testing.T) {
	if runtime.GOOS == "windows" {
		if FindLinker(os.TempDir()) != "" {
			t.Fatal("windows has no cc lookup")
		}
		return
	}
	a, b := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(b, "cc"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a, "cc"), []byte("x"), 0o644); err != nil { // not executable
		t.Fatal(err)
	}
	list := strings.Join([]string{"relative", a, b}, string(os.PathListSeparator))
	if got := FindLinker(list); got != filepath.Join(b, "cc") {
		t.Fatalf("linker %q", got)
	}
	if FindLinker("") != "" || FindLinker(a) != "" {
		t.Fatal("a linker was invented")
	}
	// The linker is passed by absolute path as the host target's linker and the
	// tool path is never the child's PATH.
	an := replayAnalyzer(t, KindCheck)
	an.linker = filepath.Join(b, "cc")
	env := an.env("/h", "/t", "x86_64-unknown-linux-gnu")
	if env["CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_LINKER"] != an.linker || env["PATH"] != an.tcDir || strings.Contains(env["PATH"], b) {
		t.Fatalf("env %v", env)
	}
	if env := an.env("/h", "/t", ""); len(env) > 0 && env["CARGO_BUILD_TARGET"] != "" {
		t.Fatalf("target pinned before the host is known: %v", env)
	}
}

// docs/44 §5: the rule context lists the Cargo configuration, the lint
// tables and (clippy) clippy.toml of the snapshot, and is unknown when cargo
// can also read configuration outside it.
func TestRuleConfig(t *testing.T) {
	files := map[string]string{
		".cargo/config.toml": "[term]\n", "Cargo.toml": "[workspace]\nmembers = [\"m\"]\n\n[workspace.lints.rust]\nunused = \"warn\"\n",
		"m/Cargo.toml": "[package]\nname = \"m\"\n\n[lints]\nworkspace = true\n", "m/src/lib.rs": "pub fn f() {}\n",
		"n/Cargo.toml": "[package]\nname = \"n\"\n", "clippy.toml": "msrv = \"1.70\"\n", "other/.cargo/config": "x", "m/lints_notes.md": "[lints]\n",
	}
	list := func(c []obsdoc.ConfigFile) []string {
		var out []string
		for _, f := range c {
			out = append(out, f.Path)
		}
		return out
	}
	base := t.TempDir()
	fx := newFixture(t, base, files, domain.Scope{}, true)
	read := func(rel string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(fx.SourceRoot, filepath.FromSlash(rel)))
		return b, err == nil
	}
	entries := map[string]source.Entry{}
	for _, e := range fx.Job.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" {
			entries[e.Path] = e
		}
	}
	ws := fx.Job.Snapshot.WorkspaceDir
	cfg, known, external := ruleConfig(KindCheck, entries, ".", ws, read)
	if external {
		t.Skip("the test directory has Cargo configuration in an ancestor")
	}
	if !known || !slices.Equal(list(cfg), []string{".cargo/config.toml", "Cargo.toml", "m/Cargo.toml"}) {
		t.Fatalf("check config %v known %v", list(cfg), known)
	}
	cfg, known, _ = ruleConfig(KindClippy, entries, ".", ws, read)
	if !known || !slices.Equal(list(cfg), []string{".cargo/config.toml", "Cargo.toml", "clippy.toml", "m/Cargo.toml"}) {
		t.Fatalf("clippy config %v", list(cfg))
	}
	for _, f := range cfg {
		if f.SHA256 != entries[f.Path].SHA256 {
			t.Fatalf("config %s digest %s", f.Path, f.SHA256)
		}
	}
	// Cargo configuration of an ancestor directory of the module is read too.
	cfg, _, _ = ruleConfig(KindCheck, entries, "m", ws, read)
	if !slices.Contains(list(cfg), ".cargo/config.toml") || !slices.Contains(list(cfg), "m/Cargo.toml") || slices.Contains(list(cfg), "Cargo.toml") {
		t.Fatalf("module config %v", list(cfg))
	}
	// Configuration outside the snapshot makes the context unknown.
	if err := os.MkdirAll(filepath.Join(base, ".cargo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".cargo", "config.toml"), []byte("[build]\nrustflags = [\"-A\", \"warnings\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, known, external := ruleConfig(KindCheck, entries, ".", ws, read); known || !external {
		t.Fatalf("external cargo config: known %v external %v", known, external)
	}
}
