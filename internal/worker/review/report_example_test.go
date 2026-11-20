package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gitleaks"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jscpd"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/lizard"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/staticcheck"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// The report example (examples/report.json, examples/report.md) is the
// builder's own output for fixtures/modules/input: the real discovery and
// review handlers run in process with a scripted model caller (no model
// is contacted) and synthetic analyzer results, so every analysis section is
// populated. The analyzer diagnostics, the metric and the secret detection
// are synthetic: no tool ran and the fixture holds no secret. Identities and
// times are fixed, so a regenerated run is byte-identical.
// CODEYE_UPDATE_EXAMPLES=1 rewrites both files; otherwise they must match.
const (
	reportExampleJSON = "../../../examples/report.json"
	reportExampleMD   = "../../../examples/report.md"
)

var reportExampleTime = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

// exampleIDs hands out fixed UUIDs in call order.
type exampleIDs struct{ n int }

func (g *exampleIDs) next() string {
	g.n++
	return fmt.Sprintf("0195f3a0-0000-7000-8000-%012x", 0x500+g.n)
}

// pinExampleSnapshot rewrites the acquired manifest with a fixed acquisition time:
// the digest then depends on the fixture content only.
func pinExampleSnapshot(t *testing.T, j *analysis.Job) {
	t.Helper()
	j.Snapshot.Manifest.FixedAt = reportExampleTime
	raw, err := json.Marshal(j.Snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	b, err := domain.CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(j.Snapshot.ManifestPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.Snapshot.ManifestPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	j.Snapshot.Digest = domain.Digest(b)
}

func exampleSHA(t *testing.T, j analysis.Job, p string) string {
	t.Helper()
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Path == p {
			return e.SHA256
		}
	}
	t.Fatalf("%s not in the snapshot", p)
	return ""
}

// exampleExec writes one execution's stdout (and an empty stderr) into
// scratch and returns its record as the executor would.
func exampleExec(t *testing.T, j analysis.Job, task, name string, argv []string, code int, stdout string) analysis.Execution {
	t.Helper()
	dir := filepath.Join(j.Scratch, "example-streams", task)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	out, errp := filepath.Join(dir, name+".stdout"), filepath.Join(dir, name+".stderr")
	if err := os.WriteFile(out, []byte(stdout), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(errp, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c := code
	res := executor.Result{Status: executor.StatusExited, ExitCode: &c, Cleanup: executor.CleanupConfirmed,
		Command:   executor.CommandRecord{Executable: argv[0], Argv: argv, Dir: analysis.RedactedMarker, EnvKeys: []string{"HOME", "PATH"}},
		Stdout:    executor.Stream{Data: []byte(stdout), TotalBytes: int64(len(stdout))},
		Stderr:    executor.Stream{Data: []byte{}},
		StartedAt: reportExampleTime.Add(time.Minute), EndedAt: reportExampleTime.Add(time.Minute + time.Second)}
	return analysis.Execution{Name: name, Result: res, StdoutPath: out, StderrPath: errp}
}

// exampleDepsRunner answers the go commands of the dependency tasks with
// fixed output; the recorded command names no node path.
type exampleDepsRunner struct{ j analysis.Job }

func (r exampleDepsRunner) Run(_ context.Context, s executor.Spec) executor.Result {
	module, _ := filepath.Rel(r.j.Snapshot.WorkspaceDir, s.Dir)
	module = filepath.ToSlash(module)
	argv := []string{"go"}
	for _, a := range s.Args {
		argv = append(argv, strings.ReplaceAll(filepath.ToSlash(a), filepath.ToSlash(r.j.Scratch), "$SCRATCH"))
	}
	out := func(code int, stdout, stderr string) executor.Result {
		c := code
		return executor.Result{Status: executor.StatusExited, ExitCode: &c, Cleanup: executor.CleanupConfirmed,
			Command:   executor.CommandRecord{Executable: "go", Argv: argv, Dir: analysis.RedactedMarker, EnvKeys: []string{"GOMODCACHE", "GOPROXY", "PATH"}},
			Stdout:    executor.Stream{Data: []byte(stdout), TotalBytes: int64(len(stdout))},
			Stderr:    executor.Stream{Data: []byte(stderr), TotalBytes: int64(len(stderr))},
			StartedAt: reportExampleTime.Add(30 * time.Second), EndedAt: reportExampleTime.Add(31 * time.Second)}
	}
	requires := map[string]string{
		".":     `{"Module":{"Path":"example.test/modules"},"Require":[{"Path":"github.com/spf13/pflag","Version":"v1.0.9"},{"Path":"golang.org/x/text","Version":"v0.21.0"}]}`,
		"svc/a": `{"Module":{"Path":"example.test/modules/svc/a"},"Require":[{"Path":"example.test/private/lib","Version":"v0.3.0"}]}`,
		"svc/b": `{"Module":{"Path":"example.test/modules/svc/b"}}`,
	}
	switch {
	case s.Args[0] == "version":
		return out(0, "go version go1.27.4 linux/amd64\n", "")
	case s.Args[1] == "edit":
		return out(0, requires[module], "")
	case module == ".":
		return out(0, "{\n\t\"Path\": \"github.com/spf13/pflag\",\n\t\"Version\": \"v1.0.9\"\n}\n{\n\t\"Path\": \"golang.org/x/text\",\n\t\"Version\": \"v0.21.0\"\n}\n", "")
	}
	return out(1, "", "go: example.test/private/lib@v0.3.0: Get \"https://proxy.golang.org/example.test/private/lib/@v/v0.3.0.mod\": dial tcp: lookup proxy.golang.org: no such host\n")
}

// exampleObserver reports the node as the probes would: go 1.27.4 and the
// analyzer tools staticcheck, lizard and gitleaks, each backed by a
// recorded version execution; jscpd is not configured.
type exampleObserver struct{ t *testing.T }

func (o exampleObserver) Observe(_ context.Context, j analysis.Job, tools []string, _ executor.Runner) discovery.Observed {
	var out discovery.Observed
	probe := func(tool, version, stdout string) *string {
		name := tool + ".version"
		out.Executions = append(out.Executions, exampleExec(o.t, j, envdoc.TaskEnvironment, name, []string{tool, "version"}, 0, stdout))
		return &name
	}
	for _, tool := range tools {
		if tool != "go" {
			out.Observations = append(out.Observations, envdoc.Observation{Tool: tool, Status: envdoc.ObsNotProbed})
			continue
		}
		v := "1.27.4"
		out.Observations = append(out.Observations, envdoc.Observation{Tool: "go", Status: envdoc.ObsObserved, Version: &v, Execution: probe("go", v, "go version go1.27.4 linux/amd64\n")})
	}
	for _, a := range []struct{ tool, analyzer, version string }{
		{"gitl" + "eaks", gitleaks.Descriptor().ID, "8.30.1"}, {"lizard", lizard.Descriptor().ID, "1.26.1"}, {"staticcheck", staticcheck.Descriptor().ID, "2026.2.2"},
	} {
		v := a.version
		out.AnalyzerTools = append(out.AnalyzerTools, envdoc.AnalyzerTool{Tool: a.tool, Analyzers: []string{a.analyzer}, Status: envdoc.ObsObserved, Version: &v, Execution: probe(a.tool, v, v+"\n")})
	}
	out.AnalyzerTools = append(out.AnalyzerTools, envdoc.AnalyzerTool{Tool: "jscpd", Analyzers: []string{jscpd.Descriptor().ID}, Status: envdoc.ObsNotProbed})
	slices.SortFunc(out.AnalyzerTools, func(a, b envdoc.AnalyzerTool) int { return strings.Compare(a.Tool, b.Tool) })
	return out
}

// exampleMember is a composite member with the analyzer's real descriptor
// and planner and a scripted Run.
type exampleMember struct {
	d    pack.Descriptor
	plan func(context.Context, analysis.Job) ([]pack.Task, error)
	run  func(analysis.Job, pack.Task) analysis.TaskResult
}

func (m exampleMember) Descriptor() pack.Descriptor { return m.d }
func (m exampleMember) Plan(ctx context.Context, j analysis.Job) ([]pack.Task, error) {
	return m.plan(ctx, j)
}
func (m exampleMember) Run(ctx context.Context, j analysis.Job, spec analysis.TaskSpec, _ executor.Runner) analysis.TaskResult {
	tasks, err := m.plan(ctx, j)
	if err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: "plan_failed"}
	}
	for _, task := range tasks {
		if task.Spec.Key == spec.Key {
			return m.run(j, task)
		}
	}
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: pack.ReasonUnknownTask}
}

// exampleDiag is one synthetic diagnostic line of a tool's stdout.
type exampleDiag struct {
	path, rule, category, message string
	line, endLine                 int
	metric                        *obsdoc.Metric
}

// envelope writes an analyzer envelope for task with a version execution and
// one run execution whose stdout holds one line per diagnostic.
func exampleEnvelope(t *testing.T, j analysis.Job, d pack.Descriptor, task pack.Task, version string, rp obsdoc.RulePack, processed []string, lims []string, diags []exampleDiag) analysis.TaskResult {
	t.Helper()
	key := task.Spec.Key
	doc := obsdoc.New(d.Analyzer(), obsdoc.Component{ID: "codeye-" + d.Name + "-adapter", Version: "1"}, obsdoc.Component{ID: "codeye-" + d.Name + "-text", Version: "1"}, key, j.Snapshot.Digest)
	doc.Module.Dir = task.Module
	if doc.Module.Dir == "" {
		doc.Module.Dir = "."
	}
	doc.Patterns = append([]string{}, task.Patterns...)
	v := version
	doc.Tool = obsdoc.Tool{Name: d.Tool, Version: &v, VersionTested: true}
	doc.RulePack = rp
	var stdout strings.Builder
	type span struct{ start, end int64 }
	var spans []span
	for _, x := range diags {
		start := int64(stdout.Len())
		fmt.Fprintf(&stdout, "%s:%d: %s: %s\n", x.path, x.line, x.rule, x.message)
		spans = append(spans, span{start, int64(stdout.Len()) - 1})
	}
	verExec := exampleExec(t, j, key, d.Tool+".version", []string{d.Tool, "--version"}, 0, version+"\n")
	code := 0
	if len(diags) > 0 && d.Purpose != obsdoc.PurposeMetrics {
		code = 1
	}
	runExec := exampleExec(t, j, key, d.Tool+".run", append([]string{d.Tool}, task.Patterns...), code, stdout.String())
	doc.Executions = []obsdoc.Execution{obsdoc.ExecutionOf(verExec.Name, verExec.Result), obsdoc.ExecutionOf(runExec.Name, runExec.Result)}
	n := len(processed)
	if task.FilesInScope != nil {
		n = *task.FilesInScope
	}
	doc.Coverage = obsdoc.Coverage{Unit: "file", FilesInScope: &n, ProcessedPaths: processed, Units: map[string]int{"analyzed": len(processed)}}
	for i, x := range diags {
		p := x.path
		end := x.endLine
		if end == 0 {
			end = x.line
		}
		ref := obsdoc.RawRef{Execution: runExec.Name, Stream: "stdout", StreamSHA256: obsdoc.SHA256Hex([]byte(stdout.String())), RecordIndex: i, ByteStart: spans[i].start, ByteEnd: spans[i].end}
		doc.Observations = append(doc.Observations, obsdoc.Observation{
			ID:     obsdoc.ObservationID(d.ID, doc.Module.Dir, "", x.rule, fmt.Sprintf("%s:%d", x.path, x.line), fmt.Sprintf("%d", end), x.message, 0),
			RuleID: x.rule, Category: x.category, RawMessage: x.message, Path: &p, LocationState: obsdoc.LocationResolved,
			Location: &obsdoc.Location{Path: p, StartLine: x.line, EndLine: end, ColumnUnit: "byte", BlobSHA256: exampleSHA(t, j, p)},
			Related:  []obsdoc.Related{}, RawRef: ref, Metric: x.metric})
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: obsdoc.ExitSuccessWithoutFindings}
	doc.Parse = obsdoc.Parse{OK: true, Records: len(diags)}
	doc.Limitations = append([]string{}, lims...)
	if doc.Active() > 0 {
		doc.Outcome.ExitClass = obsdoc.ExitSuccessWithFindings
	}
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		t.Fatalf("%s envelope: %v", key, err)
	}
	return analysis.TaskResult{Status: domain.TaskSucceeded, Executions: []analysis.Execution{verExec, runExec}, Artifacts: []analysis.ArtifactFile{art}, Limitations: doc.Limitations}
}

// exampleFailure ends task with a failed tool run (exit 1, nothing parsed).
func exampleFailure(t *testing.T, j analysis.Job, d pack.Descriptor, task pack.Task, version, reason, stdout string) analysis.TaskResult {
	t.Helper()
	key := task.Spec.Key
	doc := obsdoc.New(d.Analyzer(), obsdoc.Component{ID: "codeye-" + d.Name + "-adapter", Version: "1"}, obsdoc.Component{ID: "codeye-" + d.Name + "-text", Version: "1"}, key, j.Snapshot.Digest)
	doc.Module.Dir = task.Module
	doc.Patterns = append([]string{}, task.Patterns...)
	v := version
	doc.Tool = obsdoc.Tool{Name: d.Tool, Version: &v, VersionTested: true}
	doc.RulePack = obsdoc.RulePack{ID: "codeye-go-staticcheck", Version: "2", Selection: []string{"-checks", "SA*"}, RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	ex := exampleExec(t, j, key, d.Tool+".run", append([]string{d.Tool}, task.Patterns...), 1, stdout)
	doc.Executions = []obsdoc.Execution{obsdoc.ExecutionOf(ex.Name, ex.Result)}
	r := reason
	doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskFailed, Reason: &r, ExitClass: obsdoc.ExitExecutionError}
	doc.Coverage = obsdoc.Coverage{Unit: "package", FilesInScope: task.FilesInScope, Units: map[string]int{}}
	doc.Parse = obsdoc.Parse{OK: false, DecodeError: "tool reported no result"}
	doc.Limitations = []string{reason}
	art, err := pack.WriteEnvelope(j, doc)
	if err != nil {
		t.Fatalf("%s envelope: %v", key, err)
	}
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: reason, Executions: []analysis.Execution{ex}, Artifacts: []analysis.ArtifactFile{art}, Limitations: doc.Limitations}
}

// exampleVet writes a go vet v0.1 document: the root and svc/a modules are
// analyzed clean, svc/b needs go 1.30 and the node has go 1.27.4.
func exampleVet(t *testing.T, j analysis.Job, task pack.Task) analysis.TaskResult {
	t.Helper()
	key := task.Spec.Key
	version := "go1.27.4"
	platform := "linux/amd64"
	mod, ok := map[string]string{".": "example.test/modules", "svc/a": "example.test/modules/svc/a", "svc/b": "example.test/modules/svc/b"}[task.Module]
	if !ok {
		// A module of the slice example (fixtures/modules/slices/input).
		mod = "example.test/coverage/" + task.Module
	}
	d := govet.Document{SchemaVersion: govet.DocumentSchemaVersion, Kind: govet.DocumentKind, AnalyzerID: govet.AnalyzerID, AdapterVersion: govet.AdapterVersion,
		ParserID: govet.ParserID, ParserVersion: govet.ParserVersion, TaskKey: key, ManifestDigest: j.Snapshot.Digest, Module: govet.ModuleRef{Dir: task.Module, Path: mod},
		Patterns: append([]string{}, task.Patterns...), ToolVersion: &version, Platform: &platform, Inventory: []govet.PackageScope{}, Coverage: govet.Coverage{Packages: map[string]int{}},
		Observations: []govet.Observation{}, AnalyzerErrors: []govet.AnalyzerError{}, StderrFailures: []govet.StderrFailure{}, WorkspaceDrift: []string{}, Limitations: []string{"default_build_context_only"}}
	var execs []analysis.Execution
	add := func(name string, argv []string, code int, stdout string) {
		e := exampleExec(t, j, key, name, argv, code, stdout)
		execs = append(execs, e)
		d.Executions = append(d.Executions, govet.ExecSummary{Name: name, Status: e.Result.Status, ExitCode: e.Result.ExitCode, Cleanup: e.Result.Cleanup, Command: analysis.CommandFor(e.Result.Command),
			Stdout: govet.StreamRef{SHA256: obsdoc.SHA256Hex(e.Result.Stdout.Data), CapturedBytes: int64(len(e.Result.Stdout.Data)), TotalBytes: e.Result.Stdout.TotalBytes},
			Stderr: govet.StreamRef{SHA256: obsdoc.SHA256Hex(nil)}})
	}
	add("go.version", []string{"go", "version"}, 0, "go version go1.27.4 linux/amd64\n")
	files := 0
	if task.FilesInScope != nil {
		files = *task.FilesInScope
	}
	d.Coverage.GoFilesInScope = files
	var res analysis.TaskResult
	if task.Module == "svc/b" {
		add("go.modedit", []string{"go", "mod", "edit", "-json"}, 1, "")
		reason := govet.ReasonToolchain
		d.Outcome = govet.Outcome{TaskStatus: domain.TaskFailed, Reason: &reason, ExitClass: govet.ExitConfigurationError}
		d.Limitations = append(d.Limitations, reason)
		res = analysis.TaskResult{Status: domain.TaskFailed, Reason: reason}
	} else {
		add("go.modedit", []string{"go", "mod", "edit", "-json"}, 0, `{"Module":{"Path":"`+mod+`"}}`+"\n")
		pkg := mod
		dir := task.Module
		var goFiles []string
		for _, e := range j.Snapshot.Manifest.Entries {
			if strings.HasSuffix(e.Path, ".go") && filepath.ToSlash(filepath.Dir(e.Path)) == dir {
				goFiles = append(goFiles, e.Path)
			}
		}
		add("go.list", []string{"go", "list", "-e", "-json", "./..."}, 0, `{"ImportPath":"`+pkg+`"}`+"\n")
		add("go.vet", []string{"go", "vet", "-json", "./..."}, 0, "")
		d.Inventory = []govet.PackageScope{{ImportPath: pkg, Dir: dir, State: govet.PackageAnalyzedClean, GoFiles: goFiles, IgnoredGoFiles: []string{}, Errors: []string{}}}
		d.Coverage.Packages = map[string]int{govet.PackageAnalyzedClean: 1}
		n := len(goFiles)
		d.Coverage.ProcessedGoFiles = &n
		d.Outcome = govet.Outcome{TaskStatus: domain.TaskSucceeded, ExitClass: govet.ExitSuccessWithoutFindings}
		d.Parse = govet.ParseReport{OK: true}
		res = analysis.TaskResult{Status: domain.TaskSucceeded}
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	p := govet.ObservationsPath(j.Scratch, key)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	res.Executions = execs
	res.Artifacts = []analysis.ArtifactFile{{Path: p, Role: analysis.RoleObservations, MediaType: analysis.MediaObservations}}
	res.Limitations = d.Limitations
	return res
}

// exampleStatic is the static composite of the example: go vet and
// staticcheck per Go module, lizard, jscpd (not configured) and gitleaks.
func exampleStatic(t *testing.T) *pack.Composite {
	t.Helper()
	sc, lz, js, gl := staticcheck.Descriptor(), lizard.Descriptor(), jscpd.Descriptor(), gitleaks.Descriptor()
	scPack := obsdoc.RulePack{ID: "codeye-go-staticcheck", Version: "2", Selection: []string{"-checks", "SA*,U1000", "-tests=true", "-show-ignored"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	members := []pack.Analyzer{
		exampleMember{d: govet.Descriptor(), plan: govet.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult { return exampleVet(t, j, task) }},
		exampleMember{d: sc, plan: staticcheck.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			switch task.Module {
			case ".":
				return exampleEnvelope(t, j, sc, task, "2026.2.2", scPack, []string{"gateway.go"}, []string{"default_build_context_only"}, []exampleDiag{{path: "gateway.go", line: 13, rule: "SA4006", category: "correctness",
					message: "this value of err is overwritten before the previous attempt's error is logged (synthetic example)"}})
			case "svc/a":
				return exampleEnvelope(t, j, sc, task, "2026.2.2", scPack, []string{"svc/a/normalize.go"}, []string{"default_build_context_only"}, []exampleDiag{{path: "svc/a/normalize.go", line: 8, rule: "SA6005", category: "performance",
					message: "should use strings.EqualFold instead (synthetic example)"}})
			}
			return exampleFailure(t, j, sc, task, "2026.2.2", staticcheck.ReasonToolchain, "go: go.mod requires go >= 1.30 (running go 1.27.4)\n")
		}},
		exampleMember{d: lz, plan: lizard.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			rp := obsdoc.RulePack{ID: "codeye-structure-lizard", Version: "1", Selection: []string{"--xml", "cyclomatic_complexity>=16"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
				SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
			return exampleEnvelope(t, j, lz, task, "1.26.1", rp, []string{"gateway.go", "svc/a/normalize.go", "svc/b/charge.go"}, []string{"function_end_lines_not_recorded", "metrics_not_defects"},
				[]exampleDiag{{path: "gateway.go", line: 10, endLine: 16, rule: "cyclomatic_complexity", category: "maintainability", message: "Forward(...) at gateway.go:10: CCN 17 (synthetic example)",
					metric: &obsdoc.Metric{Name: "cyclomatic_complexity", Value: 17, Threshold: 16, Unit: "count"}}})
		}},
		exampleMember{d: js, plan: jscpd.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			return pack.NotRun(j, js, task, pack.ReasonToolchainNotConfigured)
		}},
		exampleMember{d: gl, plan: gitleaks.Plan, run: func(j analysis.Job, task pack.Task) analysis.TaskResult {
			rp := obsdoc.RulePack{ID: "codeye-secrets-gitl" + "eaks", Version: "1", Selection: []string{"config=extend.useDefault"}, RepositoryConfig: obsdoc.RepoConfigOverridden,
				SourceSuppressions: obsdoc.SuppressDisabled, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
			var processed []string
			for _, e := range j.Snapshot.Manifest.Entries {
				if e.Kind == "file" && e.ExcludedReason == "" {
					processed = append(processed, e.Path)
				}
			}
			return exampleEnvelope(t, j, gl, task, "8.30.1", rp, processed, []string{"history_not_scanned", "secret_columns_not_recorded", "secret_values_withheld"},
				[]exampleDiag{{path: "svc/b/charge.go", line: 6, rule: "generic-api-key", category: "security", message: "secret detected by gitl" + "eaks rule generic-api-key (value withheld)"}})
		}},
	}
	c, err := pack.NewComposite(members...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// exampleScript is validScript with an interpretation limit and two verdicts
// (the interpret candidate supported, the investigate candidate refuted).
func exampleScript(t *testing.T) map[string]func(int, opencode.Request) opencode.Reply {
	s := validScript(t)
	withOutput := func(role string, extra func(opencode.Request, map[string]any)) {
		base := s[role]
		s[role] = func(i int, q opencode.Request) opencode.Reply {
			var out map[string]any
			if err := json.Unmarshal(base(i, q).Output, &out); err != nil {
				t.Fatal(err)
			}
			extra(q, out)
			return asJSON(out)
		}
	}
	withOutput("documents", func(q opencode.Request, out map[string]any) {
		in := decodeAs[docsInput](t, q)
		rootEvidence := []string{docID(in, "README.md")}
		out["contracts"] = []record.Contract{
			{Statement: "게이트웨이에서 하위 호출이 실패하면 최대 3번 재시도한다.", AppliesTo: ".", EvidenceIDs: rootEvidence},
			{Statement: "결제 호출은 요청마다 정확히 한 번만 한다.", AppliesTo: "svc/b", EvidenceIDs: []string{docID(in, "svc/b/README.md")}},
			{Statement: "게이트웨이에서 하위 호출이 재시도 끝까지 실패하면 호출자에게 마지막 오류를 반환한다.", AppliesTo: ".", EvidenceIDs: rootEvidence},
		}
		out["document_quality"] = []record.DocumentQuality{
			{Aspect: "actionability", Assessment: "strength", Statement: "루트 README는 최초 호출과 재시도를 합한 호출 상한을 명시한다.", EvidenceIDs: []string{docID(in, "README.md")}},
			{Aspect: "consistency", Assessment: "weakness", Statement: "실패 시 호출을 반복할지 여부가 루트 안내와 결제 모듈 안내에서 달라 적용 범위를 확인해야 한다.", EvidenceIDs: []string{docID(in, "README.md"), docID(in, "svc/b/README.md")}},
		}
	})
	withOutput("investigate", func(q opencode.Request, out map[string]any) {
		in := decodeAs[investigateInput](t, q)
		gw := excerptID(in.Scope.Excerpts, "gateway.go")
		out["strengths"] = []record.Strength{{Title: "성공하면 즉시 종료하는 제한된 재시도", Benefit: "호출 성공 후에는 더 요청하지 않고, 연속 실패 시에는 재시도 횟수를 제한한다.", Scope: ".", EvidenceIDs: []string{gw}}}
		out["contract_comparisons"] = []record.ContractComparison{
			{ContractIndex: 1, AppliesTo: ".", Status: "match", Reason: "Forward는 send의 오류가 남아 있는 동안 MaxRetries에 도달할 때까지만 send를 다시 호출한다.", DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{gw}},
			{ContractIndex: 3, AppliesTo: ".", Status: "match", Reason: "Forward는 반복에서 send의 오류를 err에 대입하고 재시도를 마치면 그 값을 반환한다.", DocumentEvidenceIDs: in.Documents.Contracts[2].EvidenceIDs, CodeEvidenceIDs: []string{gw}},
		}
	})
	s["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[interpretInput](t, q)
		var gw, other *obsInput
		for i := range in.Observations {
			if strings.Contains(in.Observations[i].Message, "previous attempt") {
				gw = &in.Observations[i]
			} else if other == nil {
				other = &in.Observations[i]
			}
		}
		if gw == nil || other == nil {
			t.Fatalf("interpret observations %+v", in.Observations)
		}
		return asJSON(map[string]any{"hypotheses": []map[string]any{{"observation_ids": []string{gw.ID}, "title": "재시도 반복에서 앞선 시도의 오류를 덮어씀",
			"condition": "send가 두 번 이상 실패하는 경우", "impact": "호출자는 마지막 오류만 받고 앞선 실패 원인을 잃는다.", "improvement": "시도마다 오류를 기록하거나 errors.Join으로 묶어 반환한다.",
			"counterpoint": "마지막 오류만 반환하는 것이 Forward 주석의 계약이다.", "evidence_ids": gw.EvidenceIDs}},
			"limits": []map[string]any{{"observation_ids": []string{other.ID}, "statement": "Normalize의 호출자와 비교 용도가 입력에 없어 진단의 영향을 판단할 수 없다."}}})
	}
	s["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		var out []map[string]any
		for _, c := range in.Candidates {
			if strings.HasPrefix(c.ID, "interpret") {
				out = append(out, verdict(c.ID, "supported", func(m map[string]any) {
					m["category"], m["severity"], m["severity_reason"] = "error_handling", "low", "실패 원인 추적이 어려워지지만 동작 결과는 바뀌지 않는다."
					m["impact"] = "재시도가 모두 실패하면 첫 실패의 원인을 로그에서 찾을 수 없다."
					m["valid_conditions"] = []string{"send가 두 번 이상 실패한다."}
					m["supporting_evidence_ids"] = []string{c.Evidence[0]}
					m["next_verification"] = []string{"send가 연속으로 실패하는 테스트로 반환 오류를 확인한다."}
				}))
				continue
			}
			out = append(out, verdict(c.ID, "refuted", func(m map[string]any) {
				m["category"], m["severity"], m["severity_reason"] = "api_contract", "medium", "결제가 중복되면 고객에게 직접 영향이 있다."
				m["impact"] = "발췌 안에서 Forward가 결제 호출을 감싸는 경로가 없다."
				m["invalid_conditions"] = []string{"Forward는 인자로 받은 send만 재시도하며 발췌 안에서 결제 호출을 감싸는 곳이 없다."}
				m["counterevidence_ids"] = []string{c.Evidence[0]}
				m["next_verification"] = []string{"저장소 밖 호출자가 Charge를 Forward로 감싸는지 확인한다."}
			}))
		}
		return asJSON(map[string]any{"verifications": out})
	}
	return s
}

// exampleInput turns the plan and results into the builder input the
// publisher would read: tasks with their stored stage, the result manifest,
// and an artifact row for every stream and file.
func exampleInput(t *testing.T, j analysis.Job, h analysis.Handler, plan []analysis.TaskSpec, results map[string]analysis.TaskResult, ids *exampleIDs) report.Input {
	t.Helper()
	srcBytes, err := os.ReadFile(j.Snapshot.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshotID := ids.next()
	in := report.Input{ReportID: ids.next(), Revision: 1, Now: reportExampleTime.Add(10 * time.Minute),
		Run:      report.RunFacts{ID: j.RunID, ProjectID: ids.next(), Profile: j.Profile, ScopeKind: j.Scope.Kind},
		Snapshot: report.SnapshotFacts{ID: snapshotID, ManifestDigest: j.Snapshot.Digest, Consistency: j.Snapshot.Manifest.Consistency},
		Attempt:  report.AttemptFacts{ID: j.AttemptID, Number: 1, Epoch: j.Epoch, CompletionStatus: "partial", Cleanup: "confirmed"}, SourceManifest: srcBytes, Bodies: map[string][]byte{}}
	reg := report.DefaultRegistry()
	add := func(role, media string, body []byte) report.ArtifactRef {
		a := report.ArtifactRef{ID: ids.next(), RunID: j.RunID, AttemptID: j.AttemptID, Role: role, MediaType: media, SHA256: domain.Digest(body), SizeBytes: int64(len(body))}
		in.Artifacts = append(in.Artifacts, a)
		if reg.Wants(media) {
			in.Bodies[a.ID] = body
		}
		return a
	}
	add(nodeproto.RoleSourceManifest, "application/vnd.codeye.source-manifest+json", srcBytes)
	info := h.Info()
	rm := analysis.ResultManifest{SchemaVersion: analysis.ResultManifestSchemaVersion, RunID: j.RunID, AttemptID: j.AttemptID, LeaseEpoch: j.Epoch,
		Snapshot: analysis.ManifestSnapshot{ID: snapshotID, ManifestDigest: j.Snapshot.Digest}, Handlers: []analysis.ManifestHandler{{Name: info.Name, Version: info.Version, Capability: info.Capability}},
		Tasks: []analysis.ManifestTask{}, CleanupState: "confirmed", Limitations: []string{}}
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for _, spec := range plan {
		res := results[spec.Key]
		id := ids.next()
		var reason *string
		if res.Reason != "" {
			r := res.Reason
			reason = &r
		}
		in.Tasks = append(in.Tasks, report.TaskFacts{ID: id, Key: spec.Key, Status: string(res.Status), Required: spec.Required, Reason: reason, ExecutionOrdinal: 1, Stage: string(spec.Stage)})
		mt := analysis.ManifestTask{TaskID: id, Key: spec.Key, Required: spec.Required, Status: res.Status, Reason: reason, Executions: []analysis.ManifestExecution{}, Artifacts: []analysis.ManifestArtifact{}, Limitations: []string{}}
		for _, l := range res.Limitations {
			if nodeproto.ValidReason(l) {
				mt.Limitations = append(mt.Limitations, l)
			}
		}
		for _, e := range res.Executions {
			cmd := analysis.CommandFor(e.Result.Command)
			me := analysis.ManifestExecution{Name: e.Name, Status: e.Result.Status, ExitCode: e.Result.ExitCode, Cleanup: e.Result.Cleanup, Command: cmd,
				StartedAt: analysis.FormatTime(e.Result.StartedAt), EndedAt: analysis.FormatTime(e.Result.EndedAt)}
			so := add(nodeproto.RoleStdout, "text/plain", read(e.StdoutPath))
			se := add(nodeproto.RoleStderr, "text/plain", read(e.StderrPath))
			me.StdoutArtifactID, me.StderrArtifactID = &so.ID, &se.ID
			mt.Executions = append(mt.Executions, me)
		}
		for _, f := range res.Artifacts {
			role := f.Role
			if !nodeproto.ValidArtifactRole(role) || role == nodeproto.RoleSourceManifest || role == nodeproto.RoleResultManifest {
				role = nodeproto.RoleOther
			}
			a := add(role, f.MediaType, read(f.Path))
			mt.Artifacts = append(mt.Artifacts, analysis.ManifestArtifact{ArtifactID: a.ID, Role: a.Role, MediaType: a.MediaType, SHA256: a.SHA256, SizeBytes: a.SizeBytes})
		}
		rm.Tasks = append(rm.Tasks, mt)
	}
	body, err := rm.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	m := add(nodeproto.RoleResultManifest, analysis.MediaResultManifest, body)
	in.Bodies[m.ID] = body
	in.ResultManifest = &m
	return in
}

// exampleBanner follows the title of the rendered example.
const exampleBanner = "> **합성 예시.** 아래는 `fixtures/modules/input`에 대해 CODEYE의 탐색·검토 역할을 실제로 실행하고 보고서 builder가 만든 출력이다. 모델 응답은 미리 정한 스크립트이며 모델을 부르지 않았다. 정적 분석 진단, 복잡도 지표, 비밀정보 탐지는 합성한 도구 결과이고 실제 도구를 실행하지 않았다. 사용자 프로젝트의 결함 발견 결과가 아니다. 기계 판독 원본은 [report.json](report.json)이다.\n"

// exampleMarkdown renders the example report with the synthetic-data
// banner after the title.
func exampleMarkdown(t *testing.T, r report.Report) []byte {
	t.Helper()
	b, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	md := string(report.RenderMarkdownAt(r, report.Digest(b), report.Korean, report.DetailSummary))
	title, rest, ok := strings.Cut(md, "\n")
	if !ok {
		t.Fatal("rendered markdown has no title line")
	}
	return []byte(title + "\n\n" + exampleBanner + rest)
}

func TestReportExample(t *testing.T) {
	j := modulesJob(t, nil)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000501", "0195f3a0-0000-7000-8000-000000000502"
	pinExampleSnapshot(t, &j)
	ids := &exampleIDs{n: 2}
	caller := &scriptedCaller{script: exampleScript(t)}
	// The dependency tasks run against scripted go commands: the root module
	// is prepared, svc/a cannot reach its registry, svc/b names no
	// dependency.
	j.AllowDependencyFetch = true
	fetched, err := deps.Wrap(Handler{Static: exampleStatic(t), Caller: caller}, deps.Config{CacheRoot: filepath.Join(t.TempDir(), "deps"), GoExecutable: "/opt/go/bin/go"}, deps.Fetchers()...)
	if err != nil {
		t.Fatal(err)
	}
	h, err := discovery.Wrap(fetched, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]analysis.TaskResult{}
	for _, task := range plan {
		var runner executor.Runner
		if strings.HasPrefix(task.Key, "deps.") {
			runner = exampleDepsRunner{j: j}
		}
		results[task.Key] = h.Run(t.Context(), j, task, runner)
	}
	if b, err := os.ReadFile(recordPath(j, "verify")); err == nil && os.Getenv("EXAMPLE_DEBUG") != "" {
		t.Logf("%s", b)
	}
	entries := map[string][]int{
		"AGENTS.md": {1}, "README.md": {1}, "gateway.go": {1, 10, 12}, "svc/a/normalize.go": {1, 4}, "svc/b/README.md": {1}, "svc/b/go.mod": {3},
	}
	root, err := os.OpenRoot(j.Snapshot.SnapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	doc := sourceexcerpt.Document{SchemaVersion: sourceexcerpt.LegacyVersion, RunID: j.RunID, AttemptID: j.AttemptID, Snapshot: j.Snapshot.Digest}
	for _, e := range j.Snapshot.Manifest.Entries {
		for _, line := range entries[e.Path] {
			if excerpt, ok := sourceexcerpt.Read(root, e, line); ok {
				doc.Items = append(doc.Items, sourceexcerpt.Item{Path: e.Path, SHA256: e.SHA256, Anchor: line, Excerpt: excerpt})
			}
		}
	}
	excerpts, err := sourceexcerpt.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	excerptPath := filepath.Join(j.Scratch, "report-example-excerpts.json")
	if err := os.WriteFile(excerptPath, excerpts, 0600); err != nil {
		t.Fatal(err)
	}
	first := results[plan[0].Key]
	first.Artifacts = append(first.Artifacts, analysis.ArtifactFile{Path: excerptPath, Role: nodeproto.RoleOther, MediaType: sourceexcerpt.MediaType})
	results[plan[0].Key] = first
	in := exampleInput(t, j, h, plan, results, ids)
	in.Project = &report.ProjectInfo{Name: "Billing gateway", Source: "fixtures/modules/input", StartedAt: reportExampleTime.Format(time.RFC3339), FinishedAt: in.Now.Format(time.RFC3339)}
	r, err := report.Build(in, report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	checkReportExample(t, r, plan, results)
	if len(r.Documents.Quality) != 2 || len(r.Documents.Comparisons) != 3 || r.Documents.Comparisons[0].Status != "match" ||
		r.Documents.Comparisons[1].Status != "unknown" || r.Documents.Comparisons[2].Status != "match" ||
		len(r.CodeReview.Strengths) != 1 || len(r.CodeReview.Evidence) != 1 || r.CodeReview.Evidence[0].Excerpt == nil {
		t.Fatalf("report insight sections were not carried through")
	}
	// The stored example is marked demo: Marshal accepts product reports
	// only, so the flag is set on the canonical bytes.
	raw, err := report.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(raw, []byte(`"demo":false`)) != 1 {
		t.Fatal("demo flag not found once")
	}
	raw = bytes.Replace(raw, []byte(`"demo":false`), []byte(`"demo":true`), 1)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	md := exampleMarkdown(t, r)
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		for p, b := range map[string][]byte{reportExampleJSON: pretty.Bytes(), reportExampleMD: md} {
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for p, b := range map[string][]byte{reportExampleJSON: pretty.Bytes(), reportExampleMD: md} {
		compareStoredExample(t, p, b)
	}
}

// checkReportExample fails unless the example exercises every section with
// accepted inputs: Build degrades a rejected artifact to a limitation, so a
// wrong digest would otherwise leave a section quietly empty.
func checkReportExample(t *testing.T, r report.Report, plan []analysis.TaskSpec, results map[string]analysis.TaskResult) {
	t.Helper()
	var rows []string
	for _, p := range plan {
		rows = append(rows, p.Key+"="+string(results[p.Key].Status)+":"+results[p.Key].Reason)
	}
	for _, l := range r.Limitations {
		if strings.HasPrefix(l, report.LimReviewLineage) || strings.HasPrefix(l, "analysis_artifact_") || strings.HasPrefix(l, "artifact_rejected") ||
			strings.HasPrefix(l, "unrecognized_artifact") || strings.HasPrefix(l, "result_manifest") || strings.HasPrefix(l, "task_not_in_result_manifest") {
			t.Fatalf("limitation %s in %v (tasks %v)", l, r.Limitations, rows)
		}
	}
	ai := r.Coverage.AIReview
	if ai.Status != report.AIReviewPerformed {
		t.Fatalf("ai review %+v (tasks %v)", ai, rows)
	}
	for _, x := range append(append([]report.AIReviewTask{}, ai.RequiredTasks...), ai.OptionalTasks...) {
		if x.Key == record.TaskContract && x.Result == "skipped" && x.Reason != nil && *x.Reason == domain.ReasonNoReviewScope {
			continue
		}
		if x.Result != "accepted" {
			t.Fatalf("review task %+v", x)
		}
	}
	if r.Overview == nil || len(r.Overview.Attention) == 0 || len(r.Gaps) == 0 {
		t.Fatalf("overview %+v gaps %v", r.Overview, r.Gaps)
	}
	env, docs, sa, mt, sec, cr := r.Environment, r.Documents, r.StaticAnalysis, r.Metrics, r.Secrets, r.CodeReview
	switch {
	case env == nil || len(env.Modules) == 0 || len(env.Requirements) == 0 || len(env.Observed) == 0 || len(env.Verdicts) == 0 || len(env.AnalyzerTools) == 0 || len(env.Notes) == 0 || len(env.Evidence) == 0 ||
		env.Model.State != report.ModelPerformed:
		t.Fatalf("environment %+v", env)
	case docs == nil || docs.Budget == nil || len(docs.Items) == 0 || len(docs.Modules) == 0 || len(docs.Overlaps) == 0 || len(docs.Contracts) == 0 || len(docs.Conflicts) == 0 ||
		len(docs.Questions) == 0 || len(docs.InstructionNotes) == 0 || len(docs.Evidence) == 0 || docs.Model.State != report.ModelPerformed:
		t.Fatalf("documents %+v", docs)
	case sa == nil || len(sa.Languages) < 2:
		t.Fatalf("static analysis %+v", sa)
	case mt == nil || len(mt.Metrics) == 0:
		t.Fatalf("metrics %+v", mt)
	case sec == nil || len(sec.Detections) == 0 || sec.ModelInput != report.ScreenWithheld:
		t.Fatalf("secrets %+v", sec)
	case cr == nil || cr.PackID == nil || len(cr.Roles) == 0 || len(cr.FindingIDs) == 0 || len(cr.Limits) == 0:
		t.Fatalf("code review %+v", cr)
	}
	validations := map[string]bool{}
	for _, f := range r.Findings {
		validations[f.Validation] = true
	}
	for _, v := range []string{"supported", "refuted", "unreviewed"} {
		if !validations[v] {
			t.Fatalf("no %s finding in %v", v, validations)
		}
	}
	for _, include := range []bool{true, false} {
		p := report.Project(r, include)
		b, err := report.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		back, err := report.Unmarshal(b)
		if err != nil {
			t.Fatalf("include_code=%v: %v", include, err)
		}
		if err := report.ValidateReport(back); err != nil {
			t.Fatalf("include_code=%v: %v", include, err)
		}
	}
	_ = record.MediaType
}

// firstDifference shows the first lines where the stored example and the
// regenerated one disagree, so a failure on another platform is diagnosable
// from the test log alone.
func firstDifference(have, want []byte) string {
	a, b := strings.Split(string(have), "\n"), strings.Split(string(want), "\n")
	var out strings.Builder
	shown := 0
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y string
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x == y {
			continue
		}
		fmt.Fprintf(&out, "line %d\n  stored:      %.200s\n  regenerated: %.200s\n", i+1, x, y)
		if shown++; shown == 8 {
			break
		}
	}
	fmt.Fprintf(&out, "stored %d lines, regenerated %d lines", len(a), len(b))
	return out.String()
}

// compareStoredExample fails unless the stored example file p has exactly the
// regenerated bytes. The snapshot manifest records each file's permission
// bits, and the manifest digest and every evidence ID derive from it; Windows
// reports different permission bits for the same files, so the bytes are
// compared on POSIX systems only. The structural checks of each example test
// run everywhere.
func compareStoredExample(t *testing.T, p string, want []byte) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Logf("%s: stored example bytes are compared on POSIX systems only: file modes are part of the manifest digest", p)
		return
	}
	have, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(have, want) {
		t.Fatalf("%s differs from the regenerated example; rerun with CODEYE_UPDATE_EXAMPLES=1 and review the diff\n%s", p, firstDifference(have, want))
	}
}
