package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// W17 (docs/42 §2): ruff is the Python baseline, so a node with only
// --ruff advertises deep-review.
func TestRuffOnlyNodeAdvertisesDeepReview(t *testing.T) {
	ruff := os.Getenv("CODEYE_TEST_RUFF")
	if ruff == "" {
		t.Skip("set CODEYE_TEST_RUFF to the ruff binary")
	}
	hs, missing := Handlers(context.Background(), Config{ProbeDir: t.TempDir(), Tools: map[string]string{"ruff": ruff}}, &executor.Executor{})
	want := slices.Concat([]string{"go-vet:not_configured", "staticcheck:not_configured"}, cppUnconfigured, []string{"mypy:not_configured", "pyright:not_configured", "bandit:not_configured"}, jsUnconfigured, rustUnconfigured)
	if len(hs) != 1 || hs[0].Info().Name != pack.CompositeName || !slices.Equal(reasons(missing), want) {
		t.Fatalf("ruff only: %d handlers, missing %v", len(hs), reasons(missing))
	}
}

// W17 (docs/42 §5): with both type checker paths (the agent accepted
// --python-both-type-checkers) the selection is recorded on the checker
// that runs even when the other cannot (pyright without --node ends
// toolchain_not_configured).
func TestBothTypeCheckersSelectionIsRecorded(t *testing.T) {
	mypy := os.Getenv("CODEYE_TEST_MYPY")
	if mypy == "" {
		t.Skip("set CODEYE_TEST_MYPY to the mypy script")
	}
	ctx := context.Background()
	cfg := Config{GoExecutable: goPath(t), ProbeDir: t.TempDir(), Tools: map[string]string{"mypy": mypy, "pyright": filepath.Join(t.TempDir(), "index.js")}}
	c, missing, err := Composite(ctx, cfg, &executor.Executor{})
	if err != nil || !slices.Contains(reasons(missing), "pyright:toolchain_not_configured") {
		t.Fatalf("%v %v", err, reasons(missing))
	}
	job, _ := snapshotJobs(t, map[string]string{"pyproject.toml": "[project]\nrequires-python = \">=3.12\"\n", "a.py": "def f() -> int:\n    return 'x'\n"})
	specs, err := c.Plan(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	status := map[string]domain.TaskStatus{}
	for _, s := range specs {
		if s.Key != "mypy" && s.Key != "pyright" {
			continue
		}
		res := c.Run(ctx, job, s, &executor.Executor{})
		status[s.Key] = res.Status
		raw, err := os.ReadFile(res.Artifacts[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Limitations []string `json:"limitations"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		got[s.Key] = doc.Limitations
	}
	if status["mypy"] != domain.TaskSucceeded || !slices.Contains(got["mypy"], "python_type_checkers_both_selected") || status["pyright"] != domain.TaskUnsupported {
		t.Fatalf("status %v limitations %v", status, got)
	}
}
