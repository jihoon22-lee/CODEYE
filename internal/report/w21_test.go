package report

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// W21 (docs/45): the real lizard 1.26.1, jscpd 5.4.3 and gitleaks 8.30.1
// envelopes (examples/observations-{lizard,jscpd,gitleaks}.json) through
// the generic decoder.

func readExample(t *testing.T, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../examples/observations-" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func w21Report(t *testing.T) Report {
	t.Helper()
	f := analyzerFixture(t, nil,
		taskSpec{key: "lizard", status: "succeeded", doc: readExample(t, "lizard")},
		taskSpec{key: "jscpd", status: "succeeded", doc: readExample(t, "jscpd")},
		taskSpec{key: "gitl" + "eaks", status: "succeeded", doc: readExample(t, "gitl"+"eaks")})
	return build(t, f.in, DefaultRegistry())
}

// TC-117: complexity and duplication metrics are observations with their
// numbers, never findings, candidates or actionable counts; the summary
// shows them apart. TC-118: secret detections are unreviewed low security
// findings whose text, identity and evidence hold the fixed template only.
func TestW21MetricsAreNotFindings(t *testing.T) {
	r := w21Report(t)
	s := r.Summary
	if s.RawObservations != 2+1+4 || s.MetricObservations != 3 || s.TotalFindings != 4 || s.PendingCandidates != 4 || s.ActionableFindings != 0 || s.UnresolvedObservations != 0 {
		t.Fatalf("summary %+v limitations %v", s, r.Limitations)
	}
	metrics := map[string]bool{}
	for _, o := range r.Observations {
		if (o.Metric != nil) != (o.Tool == "any.lizard" || o.Tool == "any.jscpd") {
			t.Fatalf("observation %+v", o)
		}
		if o.Metric != nil {
			metrics[o.ID] = true
		}
	}
	for _, f := range r.Findings {
		for _, id := range f.ObservationIDs {
			if metrics[id] {
				t.Fatalf("finding %s built from a metric", f.ID)
			}
		}
		if f.Category != "security" || f.Severity != "low" || f.Validation != "unreviewed" || !strings.HasSuffix(f.Title, "(value withheld)") || f.FingerprintBasis == nil || f.FingerprintBasis.Analyzer != "any.gitl"+"eaks" {
			t.Fatalf("secret finding %+v", f)
		}
		want, _ := ToolFingerprint("any.gitl"+"eaks", f.FingerprintBasis.Rule, f.Locations[0].Path, "", "secret detected by gitl"+"eaks rule "+f.FingerprintBasis.Rule+" (value withheld)")
		if f.Fingerprint != want {
			t.Fatal("secret fingerprint is not over the fixed template")
		}
	}
	if r.QualityVerdict == VerdictAttention {
		t.Fatal("metrics or unreviewed detections made the verdict attention")
	}
	for _, key := range []string{"lizard", "jscpd"} {
		row := toolRow(r, key)
		if row == nil || row.ExitClass != "success_without_findings" || !slices.Contains(row.Limitations, "metrics_not_defects") || row.ProcessedFiles == nil {
			t.Fatalf("%s row %+v", key, row)
		}
	}
	gl := toolRow(r, "gitl"+"eaks")
	if gl == nil || gl.RuleContext == nil || gl.RuleContext.RepositoryConfig != "honored" || gl.RuleContext.SourceSuppressions != "disabled" || len(gl.RuleContext.ConfigPaths) != 1 {
		t.Fatalf("secrets row %+v", gl)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
}

// The three formats show the metric count once, as "not defects"; the
// code-free export keeps the numbers and drops the messages.
func TestW21MetricsRendered(t *testing.T) {
	r := w21Report(t)
	b, _ := Marshal(r)
	digest := Digest(b)
	html, err := RenderHTML(r, digest, Korean)
	if err != nil {
		t.Fatal(err)
	}
	md := string(RenderMarkdown(r, digest, Korean))
	if !strings.Contains(string(html), `data-count="metric_observations">3<`) || !strings.Contains(md, "| "+mdText(Korean.Label("metric_observations"))+" | 3 |") {
		t.Fatal("metric count missing")
	}
	if strings.Count(md, Korean.Label("metric_marker")) != 3 {
		t.Fatal("metric observations not marked as non-defects")
	}
	free := Project(r, false)
	if err := ValidateReport(free); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, o := range free.Observations {
		if o.RawMessage != nil {
			t.Fatal("code-free export keeps a message")
		}
		if o.Metric != nil && o.Metric.Value >= o.Metric.Threshold {
			n++
		}
	}
	if n != 3 || free.Summary.MetricObservations != 3 {
		t.Fatalf("code-free metrics %d", n)
	}
	fb, _ := Marshal(free)
	if strings.Contains(string(fb), "classify(") || strings.Contains(string(fb), "duplicated on file") {
		t.Fatal("code-free export holds tool text")
	}
}

// A report whose finding cites a metric observation is invalid.
func TestW21ValidateRejectsMetricFinding(t *testing.T) {
	r := w21Report(t)
	for _, o := range r.Observations {
		if o.Metric != nil {
			r.Findings[0].ObservationIDs = append(r.Findings[0].ObservationIDs, o.ID)
			break
		}
	}
	if err := ValidateReport(r); err == nil || !strings.Contains(err.Error(), "metric") {
		t.Fatalf("metric finding accepted: %v", err)
	}
	r = w21Report(t)
	for i := range r.Observations {
		if r.Observations[i].Metric != nil {
			r.Observations[i].Metric.Value = 1
			break
		}
	}
	if err := ValidateReport(r); err == nil {
		t.Fatal("metric under its threshold accepted")
	}
}
