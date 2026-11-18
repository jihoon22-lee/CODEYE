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
	// The controller reads a stored revision back with Unmarshal.
	for _, body := range [][]byte{b, fb} {
		if _, err := Unmarshal(body); err != nil {
			t.Fatalf("stored report does not read back: %v", err)
		}
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

// Two detections of one rule in one file are two findings with one
// fingerprint (rule, path, fixed template): the report is valid, and the
// identity is ambiguous, so no review disposition carries to either.
func TestW21SameRuleTwiceInOneFile(t *testing.T) {
	doc := readExample(t, "gitl"+"eaks")
	obs := doc["observations"].([]any)
	var dup map[string]any
	for _, o := range obs {
		if m := o.(map[string]any); m["path"] == "src/app.py" {
			b, _ := json.Marshal(m)
			_ = json.Unmarshal(b, &dup)
		}
	}
	dup["id"] = "obs_00000000000000000001"
	loc := dup["location"].(map[string]any)
	loc["start_line"], loc["end_line"] = 1.0, 1.0
	doc["observations"] = append(obs, dup)
	parse := doc["parse"].(map[string]any)
	parse["observations"] = parse["observations"].(float64) + 1
	f := analyzerFixture(t, nil, taskSpec{key: "gitl" + "eaks", status: "succeeded", doc: doc})
	r := build(t, f.in, DefaultRegistry())
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	var same []Finding
	for _, x := range r.Findings {
		if x.Locations[0].Path == "src/app.py" {
			same = append(same, x)
		}
	}
	if len(r.Findings) != 5 || len(same) != 2 || same[0].Fingerprint != same[1].Fingerprint || same[0].ID == same[1].ID {
		t.Fatalf("findings %d, src/app.py %+v", len(r.Findings), same)
	}
	if DispositionCarries(same[0].FingerprintBasis, 2) {
		t.Fatal("a disposition carries to an ambiguous secret identity")
	}
}

// A metric the adapter could not place in the snapshot (the measured file
// changed under the tool: source_drift) is counted unresolved like any
// observation and keeps the verdict inconclusive: its evidence did not
// check out, whatever the observation measures (A28).
func TestW21UnresolvedMetricIsInconclusive(t *testing.T) {
	doc := readExample(t, "lizard")
	o := doc["observations"].([]any)[0].(map[string]any)
	o["location"], o["location_state"] = nil, "source_drift"
	parse := doc["parse"].(map[string]any)
	parse["unresolved_observations"] = parse["unresolved_observations"].(float64) + 1
	f := analyzerFixture(t, nil, taskSpec{key: "lizard", status: "succeeded", doc: doc})
	r := build(t, f.in, DefaultRegistry())
	if r.Summary.UnresolvedObservations != 1 || r.Summary.MetricObservations != 2 || r.Summary.TotalFindings != 0 {
		t.Fatalf("summary %+v limitations %v", r.Summary, r.Limitations)
	}
	if v := DecideVerdict(StatusCompleted, r.Summary, AIReviewPerformed); v != VerdictInconclusive {
		t.Fatalf("verdict %s", v)
	}
}

// gitleaks honors the repository .gitleaksignore; a snapshot without one
// is a recorded context with no file: the digest of the empty list and no
// paths. The stored revision reads back (the JSON form omits the empty
// path list) and its key differs from a context with an ignore file.
func TestW21HonoredWithoutConfigFilesReadsBack(t *testing.T) {
	doc := readExample(t, "gitl"+"eaks")
	doc["rule_pack"].(map[string]any)["config_files"] = []any{}
	f := analyzerFixture(t, nil, taskSpec{key: "gitl" + "eaks", status: "succeeded", doc: doc})
	r := build(t, f.in, DefaultRegistry())
	rc := toolRow(r, "gitl"+"eaks").RuleContext
	if rc == nil || rc.Config != ConfigRecorded || len(rc.ConfigPaths) != 0 || rc.ConfigDigest != ConfigDigest(nil) || !rc.Known() {
		t.Fatalf("rule context %+v", rc)
	}
	if rc.Key() == toolRow(w21Report(t), "gitl"+"eaks").RuleContext.Key() {
		t.Fatal("an ignore file did not change the rule context key")
	}
	for _, include := range []bool{true, false} {
		b, err := Marshal(Project(r, include))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Unmarshal(b); err != nil {
			t.Fatalf("include_code=%v: %v", include, err)
		}
	}
}
