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

// TC-345: the dry-run upstream's project lead mode, on a TypeScript project
// whose imports name .js files of .ts sources (the callee divides, which
// the shared call reader gives up on) and on a C project that includes
// system headers beside its own: the lead resolves to the definition (for C
// the defining source first, then the header prototype marked partial), the
// follow-up runs and its refinement passes the record checks.
func TestDryRunUpstreamProjectLeadsAcrossLanguages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dry runs with scripts/fake_model_upstream.py are a Linux procedure")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	padding := strings.Repeat("// setup note\n", 900)
	for _, c := range []struct {
		name    string
		tree    map[string]string
		targets []record.LeadTarget
	}{
		{"TypeScript", map[string]string{
			"README.md":    "# Bill\n\nThe bill is split between the people present.\n",
			"app/run.ts":   "import { share } from './share.js';\n\n// run splits the bill.\nexport function run(total: number, people: number): number {\n  return share(total, people) + 1;\n}\n",
			"app/share.ts": padding + "\n// share divides the total.\nexport function share(total: number, people: number): number {\n  return total / people;\n}\n",
		}, []record.LeadTarget{{Path: "app/share.ts", Start: 903, End: 905}}},
		{"C", map[string]string{
			"README.md":      "# Bill\n\nThe bill is split between the people present.\n",
			"native/main.c":  "#include <stdio.h>\n#include <stdlib.h>\n#include \"split.h\"\n\n// main prints each share.\nint main(void) {\n  printf(\"%d\\n\", share(10, 3) + 1);\n  return EXIT_SUCCESS;\n}\n",
			"native/split.h": "#ifndef SPLIT_H\n#define SPLIT_H\n\nint share(int total, int people);\n\n#endif\n",
			"native/split.c": "#include \"split.h\"\n" + padding + "\n// share divides the total.\nint share(int total, int people) {\n  return total / people;\n}\n",
		}, []record.LeadTarget{{Path: "native/split.c", Start: 904, End: 906}, {Path: "native/split.h", Start: 4, End: 4, Partial: true}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			j := treeJob(t, c.tree)
			script := goBuildFilesScript(t)
			for _, role := range []string{record.StageInvestigate, record.StageLeads, record.StageVerify} {
				script[role] = func(_ int, q opencode.Request) opencode.Reply { return dryRunLeads(t, python, role, q, "project") }
			}
			_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
			inv := readRecord(t, j, record.StageInvestigate)
			named := slices.IndexFunc(inv.Leads, func(l record.Lead) bool { return l.Symbol == "share" })
			followup := results[record.TaskLeads]
			if named < 0 || !inv.Leads[named].Resolved() || followup.Status != domain.TaskSucceeded {
				t.Fatalf("leads %+v, follow-up %+v", inv.Leads, followup)
			}
			// The overview may have sent the header: its prototype is then
			// already in the request and only the definition is new.
			got := inv.Leads[named].Targets
			want := slices.DeleteFunc(slices.Clone(c.targets), func(w record.LeadTarget) bool {
				return w.Partial && !slices.ContainsFunc(got, func(g record.LeadTarget) bool { return g.Path == w.Path })
			})
			if len(got) != len(want) {
				t.Fatalf("targets %+v, want %+v", got, want)
			}
			for k, w := range want {
				if g := got[k]; g.Path != w.Path || g.Start != w.Start || g.End != w.End || g.Partial != w.Partial {
					t.Fatalf("target %d %+v, want %+v", k, g, w)
				}
			}
			d := readRecord(t, j, record.StageLeads).LeadFollowup.Dispositions
			if len(d) == 0 || d[0].Disposition != record.DispositionRefined || d[0].Original == nil {
				t.Fatalf("dispositions %+v", d)
			}
		})
	}
}
