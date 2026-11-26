package errcheck

import (
	"context"
	"os"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// TC-325: a module that vendors its dependencies is checked from vendor/
// without a module cache or network, like go vet; before, -mod=readonly sent
// go list to the offline proxy and the task failed.
func TestRealVendoredModule(t *testing.T) {
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
	f := packtest.NewFixture(t, "", map[string]string{
		"go.mod":                        "module example.org/check\n\ngo 1.22\n\nrequire example.org/dep v1.0.0\n",
		"vendor/modules.txt":            "# example.org/dep v1.0.0\n## explicit\nexample.org/dep\n",
		"vendor/example.org/dep/dep.go": "package dep\n\nfunc Do() error { return nil }\n",
		"clean.go":                      "package item\n\nimport \"example.org/dep\"\n\nfunc clean() {\n\tdep.Do()\n}\n",
	}, domain.Scope{Kind: "full"})
	r := packtest.RunTask(t, a, f.Job, Name, &executor.Executor{})
	d, body := packtest.ReadEnvelope(t, f.Job, r, "CODEYE_ERRCHECK_DUMP")
	if r.Status != domain.TaskSucceeded || len(d.Observations) != 1 || *d.Observations[0].Path != "clean.go" || d.Observations[0].Location.StartLine != 6 {
		t.Fatalf("status %s/%s\n%s", r.Status, r.Reason, body)
	}
	if !slices.Equal(d.RulePack.Selection, []string{"-mod=vendor"}) || !slices.Contains(d.Limitations, "vendor_directory_used") || !slices.Equal(d.Coverage.ProcessedPaths, []string{"clean.go"}) {
		t.Fatalf("selection %v limitations %v processed %v", d.RulePack.Selection, d.Limitations, d.Coverage.ProcessedPaths)
	}
}
