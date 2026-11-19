//go:build linux

package run

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack/anypacktest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
)

// TC-119 (docs/ANALYZERS.md): the production composition with the real lizard,
// jscpd (on node) and secrets tools through the real worker. The metrics
// are never findings; the planted secret (generated now) is one unreviewed
// security finding and appears in no uploaded artifact or stream, no
// protocol message, no scratch file, neither the full nor the code-free
// report JSON and none of their HTML and Markdown renderings.
// Set CODEYE_TEST_LIZARD, CODEYE_TEST_JSCPD, CODEYE_TEST_NODE and
// CODEYE_TEST_GITLEAKS.
func TestW21PackWithRealTools(t *testing.T) {
	tools := map[string]string{"lizard": os.Getenv("CODEYE_TEST_LIZARD"), "jscpd": os.Getenv("CODEYE_TEST_JSCPD"), "node": os.Getenv("CODEYE_TEST_NODE"), "gitl" + "eaks": os.Getenv("CODEYE_TEST_GITL" + "EAKS")}
	for k, v := range tools {
		if v == "" {
			t.Skipf("set the W21 tool variables to run the real end-to-end test (%s missing)", k)
		}
	}
	secret := anypacktest.Secret(t)
	branches := ""
	for i := 1; i <= 16; i++ {
		branches += "    if v == " + itoaW21(i) + ":\n        return " + itoaW21(i) + "\n"
	}
	block := "function alpha(items, token) {\n  const key = \"" + secret + "\";\n  let total = 0;\n  for (const item of items) {\n    if (item.active && item.value > 10) {\n      total += item.value * 2;\n    } else if (item.value < 0) {\n      total -= item.value;\n    } else {\n      total += 1;\n    }\n  }\n  const label = \"total:\" + total + \":\" + key.length;\n  console.log(label, token);\n  return { total, label, count: items.length };\n}\nmodule.exports = { alpha };\n"
	r := runPackFiles(t, map[string]string{
		"go.mod":         "module example.test/w21\n\ngo 1.22\n",
		"main.go":        "package main\n\nfunc main() {}\n",
		"src/app.py":     "import os\n\nAWS_KEY = \"" + secret + "\"\n",
		"src/complex.py": "def route(v, token=\"" + secret + "\"):\n" + branches + "    return 0\n",
		"src/a.js":       block,
		"lib/b.js":       block,
	}, tools)
	rows := taskRows(r.manifest)
	for _, k := range []string{"lizard", "jscpd", "gitl" + "eaks"} {
		if rows[k] != "succeeded/" {
			t.Fatalf("tasks %v", rows)
		}
	}
	rep := r.report
	if rep.Summary.MetricObservations != 2 || rep.Summary.ActionableFindings != 0 {
		t.Fatalf("summary %+v", rep.Summary)
	}
	metrics := map[string]bool{}
	for _, o := range rep.Observations {
		if o.Metric != nil {
			metrics[o.ID] = true
		}
	}
	var secrets []string
	for _, f := range rep.Findings {
		for _, id := range f.ObservationIDs {
			if metrics[id] {
				t.Fatalf("finding %s from a metric", f.ID)
			}
		}
		if f.FingerprintBasis != nil && f.FingerprintBasis.Analyzer == "any.gitl"+"eaks" {
			secrets = append(secrets, f.Locations[0].Path)
			if f.Category != "security" || f.Validation != "unreviewed" || f.Severity != "low" {
				t.Fatalf("secret finding %+v", f)
			}
		}
	}
	slices.Sort(secrets)
	if !slices.Equal(secrets, []string{"lib/b.js", "src/a.js", "src/app.py", "src/complex.py"}) {
		t.Fatalf("secret findings %v", secrets)
	}
	if d := r.docs["gitl"+"eaks"]; d.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !slices.Contains(d.Limitations, "history_not_scanned") {
		t.Fatalf("secrets envelope %+v %v", d.Outcome, d.Limitations)
	}
	for i, b := range r.uploads {
		anypacktest.AssertAbsent(t, secret, "upload "+itoaW21(i), b)
	}
	anypacktest.AssertTreeAbsent(t, secret, r.scratch)
	for _, include := range []bool{true, false} {
		p := report.Project(rep, include)
		b, err := report.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		anypacktest.AssertAbsent(t, secret, "report json", b)
		// The controller reads the stored revision back before serving it.
		if _, err := report.Unmarshal(b); err != nil {
			t.Fatalf("include_code=%v: stored report does not read back: %v", include, err)
		}
		html, err := report.RenderHTML(p, report.Digest(b), report.Korean)
		if err != nil {
			t.Fatal(err)
		}
		anypacktest.AssertAbsent(t, secret, "report html", html)
		anypacktest.AssertAbsent(t, secret, "report markdown", report.RenderMarkdown(p, report.Digest(b), report.Korean))
		if !strings.Contains(string(html), `data-count="metric_observations">2<`) {
			t.Fatal("metric count not rendered")
		}
	}
	at := analyzerTools(r.envs)
	var probed []string
	for _, a := range at {
		probed = append(probed, a.Tool+"="+string(a.Status))
	}
	b, _ := json.Marshal(at)
	anypacktest.AssertAbsent(t, secret, "analyzer tools", b)
	t.Logf("tools %v; %d observations, %d findings", probed, len(rep.Observations), len(rep.Findings))
}

func itoaW21(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
