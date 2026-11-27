package review

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// dryRunLeads answers one role request with scripts/fake_model_upstream.py
// in a --leads mode ("qualified" or "project").
func dryRunLeads(t *testing.T, python, role string, q opencode.Request, mode string) opencode.Reply {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), python, "-I", "-B", "-c",
		"import json, sys\nsys.path.insert(0, sys.argv[1])\nimport fake_model_upstream as f\n"+
			"print(json.dumps(f.result_for(sys.argv[2], json.load(sys.stdin.buffer), None, 0.0, 0.0, sys.argv[3])))",
		dir, role, mode)
	cmd.Stdin = bytes.NewReader(q.Input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("fake upstream %s: %v %s", role, err, stderr.String())
	}
	return opencode.Reply{Output: bytes.TrimSpace(out)}
}

// TC-338: the dry-run upstream's project lead mode names an unqualified
// call whose function lies in a file the overview did not send; the
// platform resolves it, the follow-up runs and its refinement passes the
// record checks (it keeps the candidate's citations and cites the whole
// resolved declaration). The qualified mode names no lead for that call.
func TestDryRunUpstreamProjectLeads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dry runs with scripts/fake_model_upstream.py are a Linux procedure")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	tree := map[string]string{
		"go.mod":     "module example.org/bill\n\ngo 1.23\n",
		"README.md":  "# Bill\n\nThe bill is split between the people present.\n",
		"app/run.go": "package app\n\n// Run splits the bill.\nfunc Run(total, people int) int {\n\treturn share(total, people) + 1\n}\n",
		"app/share.go": "package app\n\n" + strings.Repeat("// setup note\n", 900) +
			"\n// share divides the total.\nfunc share(total, people int) int {\n\treturn total / people\n}\n",
	}
	for _, mode := range []string{"project", "qualified"} {
		t.Run(mode, func(t *testing.T) {
			j := treeJob(t, tree)
			script := goBuildFilesScript(t)
			for _, role := range []string{record.StageInvestigate, record.StageLeads, record.StageVerify} {
				script[role] = func(_ int, q opencode.Request) opencode.Reply { return dryRunLeads(t, python, role, q, mode) }
			}
			_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
			inv := readRecord(t, j, record.StageInvestigate)
			if inv.LeadLimit != record.MaxLeads {
				t.Fatalf("the overview was not offered leads: %v", inv.Limitations)
			}
			named := slices.IndexFunc(inv.Leads, func(l record.Lead) bool { return l.Symbol == "share" })
			followup := results[record.TaskLeads]
			if mode == "qualified" {
				if named >= 0 || followup.Status != domain.TaskSkippedPolicy {
					t.Fatalf("the qualified mode named %+v, follow-up %+v", inv.Leads, followup)
				}
				return
			}
			if named < 0 || !inv.Leads[named].Resolved() || inv.Leads[named].Targets[0].Path != "app/share.go" || followup.Status != domain.TaskSucceeded {
				t.Fatalf("leads %+v, follow-up %+v", inv.Leads, followup)
			}
			d := readRecord(t, j, record.StageLeads).LeadFollowup.Dispositions
			if len(d) == 0 || d[0].Disposition != record.DispositionRefined || d[0].Original == nil {
				t.Fatalf("dispositions %+v", d)
			}
			if s := results["review.verify"]; s.Status != domain.TaskSucceeded {
				t.Fatalf("verify %+v", s)
			}
		})
	}
}
