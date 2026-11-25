package opengrep

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The pack over a whole directory, as a measurement: with
// CODEYE_TEST_OPENGREP set, CODEYE_OPENGREP_MEASURE_SOURCE=/abs/dir is copied
// into a snapshot (version control directories left out), the real task
// runs, and the observations envelope is written to
// CODEYE_OPENGREP_MEASURE_OUT=/abs/file.json. This is how the benchmark and
// application measurements of docs/ANALYZERS.md were taken.
func TestRealOpengrepMeasureDirectory(t *testing.T) {
	tool := testOpengrep(t)
	root, out := os.Getenv("CODEYE_OPENGREP_MEASURE_SOURCE"), os.Getenv("CODEYE_OPENGREP_MEASURE_OUT")
	if root == "" || out == "" {
		t.Skip("CODEYE_OPENGREP_MEASURE_SOURCE or CODEYE_OPENGREP_MEASURE_OUT not set")
	}
	a, err := New(Settings{Executable: tool})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	fx := anypacktest.New(t, "", files, domain.Scope{})
	res := packtest.RunTask(t, a, fx.Job, Name, &executor.Executor{})
	doc, raw := anypacktest.ReadEnvelope(t, fx.Job, res)
	t.Logf("task %s/%s exit %s observations %d processed %d", res.Status, res.Reason, doc.Outcome.ExitClass, len(doc.Observations), len(doc.Coverage.ProcessedPaths))
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
