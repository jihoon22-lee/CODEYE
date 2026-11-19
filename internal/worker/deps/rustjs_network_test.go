package deps

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/tsc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The real fetches need the network and the operator's tools, so they run
// only with CODEYE_TEST_DEPS_NETWORK=1 and the tool variables.

// cargo fetch from crates.io, then the real cargo check reading the
// fetched CARGO_HOME offline.
func TestRealRustFetchThenCheck(t *testing.T) {
	if os.Getenv("CODEYE_TEST_DEPS_NETWORK") != "1" {
		t.Skip("set CODEYE_TEST_DEPS_NETWORK=1 to fetch from crates.io")
	}
	cargo := os.Getenv("CODEYE_TEST_CARGO")
	if cargo == "" {
		t.Skip("set CODEYE_TEST_CARGO to the toolchain's cargo")
	}
	files := map[string]string{"Cargo.toml": cargoToml, "Cargo.lock": cargoLock,
		"src/lib.rs": "pub fn show(n: u32) -> String {\n    itoa::Buffer::new().format(n).to_owned()\n}\n"}
	job, src := rustJob(t, files)
	cacheRoot := filepath.Join(t.TempDir(), "cache", "deps")
	check, err := rustpack.New(rustpack.KindCheck, rustpack.Config{Cargo: cargo, ToolPath: "/usr/bin:/bin", DependencyCacheRoot: cacheRoot})
	if err != nil {
		t.Fatal(err)
	}
	c, err := pack.NewComposite(check)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Wrap(c, Config{CacheRoot: cacheRoot, CargoExecutable: cargo, RustConsumer: true}, Fetchers()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tasks, err := h.Plan(ctx, job)
	if err != nil || len(tasks) != 2 || tasks[0].Key != "deps.rust" || tasks[1].Key != rustpack.CheckName {
		t.Fatalf("plan %v %v", tasks, err)
	}
	for _, task := range tasks {
		if res := h.Run(ctx, job, task, &executor.Executor{}); res.Status != domain.TaskSucceeded {
			t.Fatalf("%s: %s/%s", task.Key, res.Status, res.Reason)
		}
	}
	if rec := readRecord(t, job, "deps.rust"); rec.Counts.Prepared != 1 || !rec.Usable() {
		t.Fatalf("record %+v", rec)
	}
	if m, _ := filepath.Glob(filepath.Join(cacheRoot, "rust", "cargo", "registry", "src", "*", "itoa-1.0.15", "src", "lib.rs")); len(m) != 1 {
		t.Fatal("itoa is not in the fetched CARGO_HOME")
	}
	for rel, body := range files {
		if got, err := os.ReadFile(filepath.Join(src, rel)); err != nil || string(got) != body {
			t.Fatalf("source %s changed", rel)
		}
	}
	if err := os.RemoveAll(cacheRoot); err != nil {
		t.Fatalf("cache not removable: %v", err)
	}
}

// npm install from the public registry into the workspace copy, then the
// real tsc resolving the installed types.
func TestRealJSFetchThenTsc(t *testing.T) {
	if os.Getenv("CODEYE_TEST_DEPS_NETWORK") != "1" {
		t.Skip("set CODEYE_TEST_DEPS_NETWORK=1 to fetch from the npm registry")
	}
	npm := os.Getenv("CODEYE_TEST_NPM")
	if npm == "" {
		t.Skip("set CODEYE_TEST_NPM to npm's bin/npm-cli.js")
	}
	node := jststest.Node(t)
	script := jststest.Tsc(t)
	files := map[string]string{
		"package.json":  `{"name":"app","private":true,"dependencies":{"type-fest":"4.26.1"}}` + "\n",
		"tsconfig.json": `{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"Bundler","strict":true,"lib":["ES2022"]},"include":["src"]}` + "\n",
		"src/a.ts":      "import type { Simplify } from \"type-fest\";\n\nexport const n: Simplify<{ a: number }> = { a: \"x\" };\n",
	}
	job, src := jsJob(t, files)
	cacheRoot := filepath.Join(t.TempDir(), "cache", "deps")
	a, err := tsc.New(tsc.Config{Script: script, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	c, err := pack.NewComposite(a)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Wrap(c, Config{CacheRoot: cacheRoot, NodeExecutable: node.Executable, NpmScript: npm, JSConsumer: true}, Fetchers()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tasks, err := h.Plan(ctx, job)
	if err != nil || len(tasks) != 2 || tasks[0].Key != "deps.javascript" || tasks[1].Key != "tsc" {
		t.Fatalf("plan %v %v", tasks, err)
	}
	var last analysis.TaskResult
	for _, task := range tasks {
		if last = h.Run(ctx, job, task, &executor.Executor{}); last.Status != domain.TaskSucceeded {
			t.Fatalf("%s: %s/%s", task.Key, last.Status, last.Reason)
		}
	}
	// tsc resolved type-fest and reports the real type error.
	doc, _ := jststest.ReadEnvelope(t, job, last)
	if len(doc.Observations) != 1 || doc.Observations[0].RuleID != "TS2322" {
		t.Fatalf("observations %+v errors %+v", doc.Observations, doc.ToolErrors)
	}
	if rec := readRecord(t, job, "deps.javascript"); !slices.Equal(rec.InstalledDirs, []string{"node_modules"}) || !rec.TreeUsable() {
		t.Fatalf("record %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(src, "node_modules")); err == nil {
		t.Fatal("node_modules written into the source")
	}
}
