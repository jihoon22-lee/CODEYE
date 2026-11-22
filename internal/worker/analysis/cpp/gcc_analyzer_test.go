package cpp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const gccFixture = "../../../../fixtures/cpp/gcc-analyzer/input"

func gccFiles(t *testing.T) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, file := range []string{"compile_commands.json", "src/descriptor.c", "src/clean.c"} {
		b, err := os.ReadFile(filepath.Join(gccFixture, file))
		if err != nil {
			t.Fatal(err)
		}
		out[file] = string(b)
	}
	return out
}

func TestGCCAnalyzerPlanAndSanitizedArgs(t *testing.T) {
	f := newFixture(t, gccFiles(t), domain.Scope{})
	tasks, err := PlanGCCAnalyzer(t.Context(), f.Job)
	if err != nil || len(tasks) != 1 || tasks[0].Spec.Required || tasks[0].RepositoryCode {
		t.Fatalf("GCC should be optional and avoid repository execution: %+v, %v", tasks, err)
	}
	l, err := layoutOf(f.Job)
	if err != nil {
		t.Fatal(err)
	}
	data, _, ok := readDBs(f.Job, l)
	if !ok {
		t.Fatal("could not read snapped compile DB")
	}
	m, err := MapDBs(l.DBs, data, l.Files, func(string) bool { return true })
	if err != nil || len(m.Units) != 2 || m.Dropped[DropCodeLoading] == 0 || m.Dropped[DropResponseFile] == 0 {
		t.Fatalf("unexpected mapping: %+v, %v", m, err)
	}
	for _, u := range m.Units {
		args := gccUnitArgs(u, f.Job.Snapshot.WorkspaceDir)
		for _, arg := range args {
			if strings.Contains(arg, "plugin") || strings.Contains(arg, "unsafe") || strings.HasSuffix(arg, ".o") || arg == "ccache" || arg == "gcc" {
				t.Fatalf("repository driver or unsafe flag reached GCC: %+v", args)
			}
		}
		if !slices.Contains(args, "-fanalyzer") || !slices.Contains(args, "-S") || args[len(args)-2] != os.DevNull ||
			args[len(args)-1] != u.File(f.Job.Snapshot.WorkspaceDir) {
			t.Fatalf("unexpected execution: %+v", args)
		}
	}
	cpp := newFixture(t, map[string]string{"src/layout.cpp": "int main() {}\n"}, domain.Scope{})
	if tasks, err := PlanGCCAnalyzer(t.Context(), cpp.Job); err != nil || len(tasks) != 0 {
		t.Fatalf("C++ tasks are outside the measured GCC scope: %+v, %v", tasks, err)
	}
}

func TestGCCAnalyzerSanitizesAndBindsDiagnosticBytes(t *testing.T) {
	const inside = "/workspace/entry.c"
	const outside = "/other/host/source.c"
	for _, test := range []struct {
		name, content string
		invalid       bool
		outside       int
		want          int
	}{
		{"measured shape", gccJSONWarning(inside, inside), false, 0, 1},
		{"outside related location", gccJSONWarning(inside, outside), false, 1, 0},
		{"outside primary location", gccJSONWarning(outside, inside), false, 1, 0},
		{"corrupted stream", `[ { "kind": "warning", "message": "outside" `, true, 0, 0},
		{"already-covered rule", strings.ReplaceAll(gccJSONWarning(inside, inside), "analyzer-fd-use-after-close", "analyzer-double-free"), false, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := executor.Result{Stderr: executor.Stream{Data: []byte(test.content), TotalBytes: int64(len(test.content))}}
			got := sanitizeGCCAnalyzer(&r, func(p string) bool { return p == inside }, func(s string) string { return s })
			if got.invalid != test.invalid || got.outOfScope != test.outside || len(got.records) != test.want || bytes.Contains(r.Stderr.Data, []byte(outside)) {
				t.Fatalf("unsafe or incorrect capture: %+v %s", got, r.Stderr.Data)
			}
			for _, rec := range got.records {
				var d gccDiagnostic
				if json.Unmarshal(r.Stderr.Data[rec.span.Start:rec.span.End], &d) != nil || d.Message != rec.Message || len(rec.events) != 1 {
					t.Fatalf("diagnostic span is not bound: %+v", rec)
				}
				span := rec.events[0]
				var e gccEvent
				if json.Unmarshal(r.Stderr.Data[span.Start:span.End], &e) != nil || e.Description != "closed here" || e.Location.File != inside {
					t.Fatalf("related event span is not bound: %+v", rec)
				}
			}
		})
	}
}

func gccJSONWarning(primary, related string) string {
	return `[{"kind":"warning","option":"-Wanalyzer-fd-use-after-close","message":"read on closed fd [-Wanalyzer-fd-use-after-close]","locations":[{"caret":{"file":"` + primary + `","line":8,"byte-column":17}}],"path":[{"location":{"file":"` + related + `","line":7,"byte-column":5},"description":"closed here"}]}]`
}

func TestRealGCCAnalyzerFileDescriptorFlow(t *testing.T) {
	exe := testTool(t, "CODEYE_TEST_GCC", "GCC 13 or newer")
	f := newFixture(t, gccFiles(t), domain.Scope{})
	before := packtest.TreeHash(t, f.SourceRoot)
	a, err := NewGCCAnalyzer(Config{Executable: exe})
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{inner: &executor.Executor{}}
	if version, err := a.Probe(t.Context(), runner, f.Job.Scratch); err != nil || !GCCAnalyzerVersionTested(version) {
		t.Fatalf("GCC probe %q: %v", version, err)
	}
	tasks, err := a.Plan(t.Context(), f.Job)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("plan %+v %v", tasks, err)
	}
	res := a.Run(t.Context(), f.Job, tasks[0].Spec, runner)
	d, data := readEnvelope(t, f.Job, res)
	if res.Status != domain.TaskSucceeded || len(d.Observations) != 1 || !slices.Equal(d.Coverage.ProcessedPaths, []string{"src/clean.c", "src/descriptor.c"}) ||
		d.Coverage.Units[TUAnalyzedClean] != 1 || d.Coverage.Units[TUAnalyzedWithFindings] != 1 || packtest.TreeHash(t, f.SourceRoot) != before {
		t.Fatalf("unexpected measured GCC result: %+v\n%s", res, data)
	}
	o := d.Observations[0]
	if o.RuleID != "analyzer-fd-use-after-close" || o.Location == nil || o.Location.Path != "src/descriptor.c" || o.Location.StartLine != 8 || len(o.Related) == 0 {
		t.Fatalf("missing diagnostic and location: %+v", o)
	}
	if !slices.ContainsFunc(o.Related, func(r obsdoc.Related) bool {
		return r.Location != nil && r.Location.StartLine == 7 && r.RawSpan != nil && r.RawMessage == "closed here"
	}) {
		t.Fatalf("missing verified closing location: %+v", o.Related)
	}
	for _, e := range res.Executions {
		if e.Name != o.RawRef.Execution {
			continue
		}
		var raw gccDiagnostic
		stream := e.Result.Stderr.Data
		if json.Unmarshal(stream[o.RawRef.ByteStart:o.RawRef.ByteEnd], &raw) != nil || raw.Option != "-W"+o.RuleID ||
			o.RawRef.StreamSHA256 != obsdoc.SHA256Hex(stream) {
			t.Fatalf("bad raw GCC record reference %+v", o.RawRef)
		}
	}
	if output := os.Getenv("CODEYE_GCC_EXAMPLE_OUTPUT"); output != "" {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		if err := os.WriteFile(output, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGCCAnalyzerNeedsCompileDatabase(t *testing.T) {
	f := newFixture(t, map[string]string{"a.c": "int main(void) {}\n"}, domain.Scope{})
	a := &GCCAnalyzer{}
	tasks, err := a.Plan(t.Context(), f.Job)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("plan %+v %v", tasks, err)
	}
	res := a.Run(context.Background(), f.Job, tasks[0].Spec, nil)
	if res.Status != domain.TaskUnsupported || res.Reason != ReasonCompileDBMissing || len(res.Executions) != 0 {
		t.Fatalf("GCC must not guess build configuration: %+v", res)
	}
}
