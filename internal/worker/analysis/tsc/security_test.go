package tsc

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts/jststest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// A repository tsconfig chain is untrusted input (docs/43 §3.2). These run
// in CI: a refused task starts no process, so the tools are fakes and the
// runner fails the test when called.

type neverRunner struct{ t *testing.T }

func (r neverRunner) Run(_ context.Context, s executor.Spec) executor.Result {
	r.t.Errorf("a refused task started %v", s.Args)
	return executor.Result{Status: executor.StatusStartFailed, Cleanup: executor.CleanupNotStarted}
}

func fakeAnalyzer(t *testing.T) *Analyzer {
	t.Helper()
	_, script, node := jststest.FakeTools(t)
	a, err := New(Config{Script: script, Node: node})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// Write options: generateTrace writes trace files even with --noEmit
// (measured), generateCpuProfile has no command-line off value; emit and
// tsbuildinfo paths leaving the snapshot are refused too. Path options
// naming host files are refused before tsc reads them. Only option names
// are recorded, never their values.
func TestTsconfigWriteAndHostPathOptionsRefused(t *testing.T) {
	a := fakeAnalyzer(t)
	co := func(kv string) string {
		return "{\"compilerOptions\":{\"strict\":true," + kv + "},\"include\":[\"src\"]}\n"
	}
	cases := []struct {
		name, config, reason, limitation string
	}{
		{"generateTrace", co(`"generateTrace":"./trace"`), ReasonWriteOption, "tsconfig_option_generate_trace"},
		{"generateCpuProfile", co(`"generateCpuProfile":"./cpu.cpuprofile"`), ReasonWriteOption, "tsconfig_option_generate_cpu_profile"},
		{"outDir", co(`"outDir":"../../canary-secret"`), ReasonWriteOption, "tsconfig_option_out_dir"},
		{"declarationDir", co(`"declarationDir":"/tmp/canary-secret"`), ReasonWriteOption, "tsconfig_option_declaration_dir"},
		{"outFile", co(`"outFile":"C:\\canary-secret\\out.js"`), ReasonWriteOption, "tsconfig_option_out_file"},
		{"tsBuildInfoFile", co(`"tsBuildInfoFile":"${configDir}/../canary-secret/info"`), ReasonWriteOption, "tsconfig_option_ts_build_info_file"},
		{"files", "{\"files\":[\"/etc/canary-secret.ts\"]}\n", ReasonConfigOutside, "tsconfig_option_files"},
		{"include", "{\"include\":[\"../**/*\"]}\n", ReasonConfigOutside, "tsconfig_option_include"},
		{"exclude", "{\"include\":[\"src\"],\"exclude\":[\"../canary-secret\"]}\n", ReasonConfigOutside, "tsconfig_option_exclude"},
		{"baseUrl", co(`"baseUrl":"/canary-secret"`), ReasonConfigOutside, "tsconfig_option_base_url"},
		{"rootDir", co(`"rootDir":".."`), ReasonConfigOutside, "tsconfig_option_root_dir"},
		{"rootDirs", co(`"rootDirs":["src","../canary-secret"]`), ReasonConfigOutside, "tsconfig_option_root_dirs"},
		{"typeRoots", co(`"typeRoots":["../../canary-secret/types"]`), ReasonConfigOutside, "tsconfig_option_type_roots"},
		{"types", co(`"types":["node","../canary-secret"]`), ReasonConfigOutside, "tsconfig_option_types"},
		{"paths", co(`"baseUrl":".","paths":{"@x/*":["../canary-secret/*"]}`), ReasonConfigOutside, "tsconfig_option_paths"},
		{"paths-without-baseUrl", co(`"paths":{"x":["/canary-secret/x.ts"]}`), ReasonConfigOutside, "tsconfig_option_paths"},
		{"extends", "{\"extends\":\"/canary-secret/base.json\"}\n", ReasonConfigOutside, "tsconfig_option_extends"},
		{"references", "{\"files\":[],\"references\":[{\"path\":\"../canary-secret\"}]}\n", ReasonConfigOutside, "tsconfig_option_references"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// The option sits in an extended base, so the whole chain is checked.
			fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": "{\"extends\":\"./base.json\"}\n", "base.json": c.config,
				"src/a.ts": fixClean}), domain.Scope{})
			if c.name == "extends" || c.name == "references" {
				fx = jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": c.config, "src/a.ts": fixClean}), domain.Scope{})
			}
			res := jststest.RunTask(t, a, fx.Job, "tsc", neverRunner{t})
			doc, raw := jststest.ReadEnvelope(t, fx.Job, res)
			if res.Status != domain.TaskSkippedPolicy || res.Reason != c.reason || !slices.Contains(doc.Limitations, c.limitation) || len(doc.Executions) != 0 {
				t.Fatalf("%s: %s/%s limitations %v", c.name, res.Status, res.Reason, doc.Limitations)
			}
			if strings.Contains(string(raw), "canary-secret") || strings.Contains(string(raw), "/etc/") {
				t.Fatalf("%s: option value echoed: %s", c.name, raw)
			}
		})
	}
}

// Inside-workspace emit and tsbuildinfo paths are inert under the adapter
// flags; a symlink reaching outside the workspace copy is refused.
func TestTsconfigSymlinkOutsideWorkspaceRefused(t *testing.T) {
	a := fakeAnalyzer(t)
	fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": "{\"include\":[\"linked/**/*\"]}\n", "src/a.ts": fixClean}), domain.Scope{})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(fx.Job.Snapshot.WorkspaceDir, "linked")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	res := jststest.RunTask(t, a, fx.Job, "tsc", neverRunner{t})
	doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != ReasonConfigOutside || !slices.Contains(doc.Limitations, "tsconfig_option_symlink") {
		t.Fatalf("symlink %s/%s %v", res.Status, res.Reason, doc.Limitations)
	}
}

// The effective configuration is checked too (CheckEffective), before
// the check step: tsc --showConfig output whose overrides did not hold,
// or that names a host path, stops the task after tsc.config.
func TestEffectiveConfigRefusedBeforeCheck(t *testing.T) {
	a := fakeAnalyzer(t)
	for _, c := range []struct {
		name, shown, reason, limitation string
	}{
		{"incremental", "{\"compilerOptions\":{\"noEmit\":true,\"incremental\":true,\"composite\":false},\"files\":[\"./src/a.ts\"]}\n", ReasonWriteOption, "tsconfig_option_incremental"},
		{"noEmit", "{\"compilerOptions\":{\"incremental\":false,\"composite\":false},\"files\":[\"./src/a.ts\"]}\n", ReasonWriteOption, "tsconfig_option_no_emit"},
		{"files", "{\"compilerOptions\":{\"noEmit\":true},\"files\":[\"../../host/a.ts\"]}\n", ReasonConfigOutside, "tsconfig_option_files"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": fixConfig, "src/a.ts": fixClean}), domain.Scope{})
			replay := &jststest.Replay{T: t, Steps: []jststest.Recorded{{Name: execVersion, Stdout: []byte("Version 6.0.3\n")}, {Name: execConfig, Stdout: []byte(c.shown)}}}
			res := jststest.RunTask(t, a, fx.Job, "tsc", replay)
			doc, _ := jststest.ReadEnvelope(t, fx.Job, res)
			if res.Status != domain.TaskSkippedPolicy || res.Reason != c.reason || !slices.Contains(doc.Limitations, c.limitation) || len(replay.Specs) != 2 {
				t.Fatalf("%s: %s/%s %v specs %d", c.name, res.Status, res.Reason, doc.Limitations, len(replay.Specs))
			}
		})
	}
}

// Repository-derived argv elements are "./"-prefixed: a reference
// directory named "-ref" becomes "-p ./-ref/tsconfig.json".
func TestRepositoryArgvNeverAnOption(t *testing.T) {
	for _, p := range []string{"tsconfig.json", "-ref/tsconfig.json", "--help"} {
		args := CheckArgs(p)
		if v := args[len(args)-1]; !strings.HasPrefix(v, "./") || args[len(args)-2] != "-p" {
			t.Fatalf("argv %v", args)
		}
	}
}

// Evidence (real tsc, docs/43 §3.2): under the adapter flags, emit and
// tsbuildinfo options naming a canary directory outside the project write
// nothing; generateTrace does, which is why the adapter refuses it.
func TestRealTscWriteOptionsCanary(t *testing.T) {
	a := newReal(t)
	for _, c := range []struct {
		opt    string
		writes bool
	}{{"outDir", false}, {"declarationDir", false}, {"outFile", false}, {"tsBuildInfoFile", false}, {"generateTrace", true}} {
		dir, canary := t.TempDir(), t.TempDir()
		val := filepath.ToSlash(filepath.Join(canary, "out"))
		jststest.WriteTree(t, dir, map[string]string{
			"tsconfig.json": "{\"compilerOptions\":{" + opts + ",\"declaration\":true,\"incremental\":true,\"composite\":true,\"" + c.opt + "\":\"" + val + "\"},\"include\":[\"src\"]}\n",
			"src/a.ts":      fixClean,
		})
		res := (&executor.Executor{}).Run(context.Background(), executor.Spec{Executable: a.script.Node.Executable, Args: a.script.Argv(CheckArgs("tsconfig.json")...),
			WorkspaceRoot: dir, Dir: dir, TempParent: t.TempDir(), Env: a.script.Env(), Timeout: time.Minute, StdoutLimit: 1 << 20, StderrLimit: 1 << 20})
		var written []string
		_ = filepath.WalkDir(canary, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				written = append(written, p)
			}
			return nil
		})
		if (len(written) > 0) != c.writes {
			t.Fatalf("%s: exit %v, canary files %v (%s)", c.opt, res.ExitCode, written, res.Stdout.Data)
		}
		if !c.writes {
			if _, err := os.Stat(filepath.Join(dir, "tsconfig.tsbuildinfo")); err == nil {
				t.Fatalf("%s: tsbuildinfo written", c.opt)
			}
		}
	}
	// The adapter refuses generateTrace before tsc runs (see above); with
	// the chain validated, its own check of the effective config agrees.
	if w, _, err := jsts.CheckEffective([]byte("{\"compilerOptions\":{\"noEmit\":true,\"generateTrace\":\"./t\"}}"), "."); err != nil || !slices.Equal(w, []string{"generateTrace"}) {
		t.Fatalf("effective %v %v", w, err)
	}
}

// A typical library/Vite config with in-snapshot emit paths (outDir,
// declarationDir, rootDir, baseUrl+paths, composite, incremental,
// tsBuildInfoFile, and emitDeclarationOnly) passes the chain and effective
// checks (showConfig adds the outDir to exclude as a redacted absolute
// path), checks the program and leaves the workspace unchanged.
func TestRealTscTypicalEmitConfigRuns(t *testing.T) {
	a := newReal(t)
	for _, extra := range []string{"", ",\"emitDeclarationOnly\":true"} {
		config := "{\"compilerOptions\":{" + opts + ",\"outDir\":\"./dist\",\"declaration\":true,\"declarationDir\":\"./types\",\"rootDir\":\"./src\",\"baseUrl\":\".\"," +
			"\"paths\":{\"@/*\":[\"./src/*\"]},\"composite\":true,\"incremental\":true,\"tsBuildInfoFile\":\"./build/app.tsbuildinfo\"" + extra + "},\"include\":[\"src\"]}\n"
		fx := jststest.NewFixture(t, "", caseFiles(map[string]string{"tsconfig.json": config, "src/a.ts": fixClean,
			"src/b.ts": "import { add } from \"@/a\";\n\nexport const three: number = add(1, 2);\n"}), domain.Scope{})
		ws := jststest.TreeHash(t, fx.Job.Snapshot.WorkspaceDir)
		res := jststest.RunTask(t, a, fx.Job, "tsc", &executor.Executor{})
		doc, raw := jststest.ReadEnvelope(t, fx.Job, res)
		if res.Status != domain.TaskSucceeded || len(doc.Observations) != 0 || len(doc.Coverage.ProcessedPaths) != 2 {
			t.Fatalf("%q: %s/%s %+v %v\n%s", extra, res.Status, res.Reason, doc.ToolErrors, doc.Limitations, res.Executions[len(res.Executions)-1].Result.Stdout.Data)
		}
		if jststest.TreeHash(t, fx.Job.Snapshot.WorkspaceDir) != ws {
			t.Fatalf("%q: tsc wrote into the workspace", extra)
		}
		jststest.AssertNoNodePaths(t, raw, nodePaths(a, fx)...)
	}
}
