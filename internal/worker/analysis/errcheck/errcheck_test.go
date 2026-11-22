package errcheck

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func TestRealIgnoredErrorAndCheckedReturn(t *testing.T) {
	exe := os.Getenv("CODEYE_TEST_ERRCHECK")
	if exe == "" {
		t.Skip("CODEYE_TEST_ERRCHECK is not set")
	}
	g, err := govet.New(govet.Config{GoExecutable: packtest.GoExecutable(t)})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(exe, g)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Probe(context.Background(), &executor.Executor{}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, statement string
		count           int
	}{{"ignored", `os.Remove(path); return nil`, 1}, {"returned", `return os.Remove(path)`, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			f := packtest.NewFixture(t, "", map[string]string{"go.mod": "module example.org/check\n\ngo 1.22\n", "clean.go": "package item\nimport \"os\"\nfunc clean(path string) error { " + tc.statement + " }\n"}, domain.Scope{Kind: "full"})
			r := packtest.RunTask(t, a, f.Job, Name, &executor.Executor{})
			if r.Status != domain.TaskSucceeded {
				t.Fatalf("status %s, reason %s, limits %v", r.Status, r.Reason, r.Limitations)
			}
			d, _ := packtest.ReadEnvelope(t, f.Job, r, "CODEYE_ERRCHECK_DUMP")
			if len(d.Observations) != tc.count {
				t.Fatalf("got %d diagnostics", len(d.Observations))
			}
			if tc.count == 1 {
				o := d.Observations[0]
				if o.Path == nil || *o.Path != "clean.go" || o.Location == nil || o.Location.StartLine != 3 {
					t.Fatalf("location: %+v", o)
				}
			}
		})
	}
}

func TestDiagnosticRangesAndSelectedPackageFiles(t *testing.T) {
	fixture := packtest.NewFixture(t, "", map[string]string{
		"go.mod":   "module example.org/check\n",
		"keep.go":  "package item\nfunc drop() {}\n",
		"other.go": "package item\n",
	}, domain.Scope{Kind: "paths", Paths: []string{"keep.go"}})
	snap := fixture.Job.Snapshot
	selected := []source.Entry{govet.IncludedFiles(snap.Manifest)["keep.go"]}
	resolver := anypack.NewResolver(snap.SnapshotDir, selected, nil)
	first := "keep.go:2:6:\tprivateCall()\n"
	data := []byte(first + first + "other.go:1:1:\tother()\n" + "unparseable")
	out := obsdoc.Document{}
	out.Analyzer.ID = "go.errcheck"
	parseDiagnostics(&out, data, ".", []string{snap.WorkspaceDir}, selected, resolver)
	if out.Parse.Records != 3 || out.Parse.ShapeErrors != 1 || len(out.Observations) != 2 {
		t.Fatalf("parse %+v observations %d", out.Parse, len(out.Observations))
	}
	for i, o := range out.Observations {
		if o.Location == nil || o.Location.Path != "keep.go" || o.Location.StartLine != 2 || o.Location.BlobSHA256 != selected[0].SHA256 ||
			o.RawRef.StreamSHA256 != obsdoc.SHA256Hex(data) || int(o.RawRef.ByteStart) != i*len(first) || int(o.RawRef.ByteEnd) != (i+1)*len(first) ||
			strings.Contains(o.RawMessage, "privateCall") {
			t.Fatalf("diagnostic %d = %+v", i, o)
		}
	}
	if out.Observations[0].ID == out.Observations[1].ID {
		t.Fatal("two observations at one position have a shared ID")
	}
}
