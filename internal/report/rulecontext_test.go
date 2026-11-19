package report

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
)

// W19 rule context (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제): the staticcheck row records the
// envelope's rule pack, the go vet row the synthesized context; a
// diagnostic staticcheck reported as suppressed in source stays an
// observation and never becomes a finding.
func TestRuleContextAndSuppressedObservations(t *testing.T) {
	sc := staticcheckTask(t)
	obs := sc.doc["observations"].([]any)
	hidden := obs[2].(map[string]any)
	hidden["suppressed"] = true
	f := analyzerFixture(t, nil, goVet(t), sc)
	r := build(t, f.in, DefaultRegistry())

	if r.Summary.RawObservations != 5+7 || r.Summary.TotalFindings != 5+6 {
		t.Fatalf("summary %+v", r.Summary)
	}
	for _, x := range r.Findings {
		if slices.Contains(x.ObservationIDs, hidden["id"].(string)) {
			t.Fatalf("finding %s from a suppressed observation", x.ID)
		}
	}
	var marked []string
	for _, o := range r.Observations {
		if o.Suppressed {
			marked = append(marked, o.ID)
		}
	}
	if !slices.Equal(marked, []string{hidden["id"].(string)}) {
		t.Fatalf("suppressed observations %v", marked)
	}
	loc := hidden["location"].(map[string]any)
	want, _ := ToolFingerprint("go.staticcheck", hidden["rule_id"].(string), loc["path"].(string), "", hidden["raw_message"].(string))
	if fps := SuppressedFingerprints(r, "go.staticcheck"); len(fps) != 1 || !fps[want] || len(SuppressedFingerprints(r, govet.AnalyzerID)) != 0 {
		t.Fatalf("suppressed fingerprints %v want %s", fps, want)
	}

	row := toolRow(r, "staticcheck").RuleContext
	if row == nil || row.RulePack != "codeye-go-staticcheck" || row.RulePackVersion != "2" || !slices.Contains(row.Selection, "-show-ignored") ||
		row.RepositoryConfig != obsdoc.RepoConfigOverridden || row.SourceSuppressions != obsdoc.SuppressReported || row.Config != ConfigNotRead || !row.Known() ||
		!slices.Equal(row.UnreportedSuppressionRules, []string{"U1000"}) || !row.HidesSuppressionsOf("U1000") || row.HidesSuppressionsOf("SA4006") {
		t.Fatalf("staticcheck rule context %+v", row)
	}
	vet := toolRow(r, "go-vet").RuleContext
	if vet == nil || vet.Key() != GoVetRuleContext().Key() || !vet.Known() {
		t.Fatalf("go vet rule context %+v", vet)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}

	// A finding citing the suppressed observation is rejected.
	bad := r
	bad.Findings = slices.Clone(r.Findings)
	bad.Findings[0].ObservationIDs = append(slices.Clone(bad.Findings[0].ObservationIDs), hidden["id"].(string))
	if err := ValidateReport(bad); err == nil || !strings.Contains(err.Error(), "suppressed") {
		t.Fatalf("finding from a suppressed observation accepted: %v", err)
	}
}

// Honored repository configuration: recorded files give a known context
// whose key follows the files; a pack that cannot enumerate them leaves the
// context unknown. The code-free export keeps the mode but strips the
// paths and digest without changing the full report.
func TestRuleContextConfigDigest(t *testing.T) {
	files := []obsdoc.ConfigFile{{Path: ".golangci.yml", SHA256: strings.Repeat("a", 64)}, {Path: "staticcheck.conf", SHA256: strings.Repeat("b", 64)}}
	rp := obsdoc.RulePack{ID: "codeye-x", Version: "1", Selection: []string{}, RepositoryConfig: obsdoc.RepoConfigHonored, SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: files}
	rec := ruleContextOf(rp)
	if rec.Config != ConfigRecorded || !slices.Equal(rec.ConfigPaths, []string{".golangci.yml", "staticcheck.conf"}) || rec.ConfigDigest != ConfigDigest(files) || !rec.Known() {
		t.Fatalf("recorded %+v", rec)
	}
	changed := slices.Clone(files)
	changed[1].SHA256 = strings.Repeat("c", 64)
	rp2 := rp
	rp2.ConfigFiles = changed
	if ruleContextOf(rp2).Key() == rec.Key() {
		t.Fatal("a changed configuration file kept the key")
	}
	rp3 := rp
	rp3.ConfigFiles = nil
	if u := ruleContextOf(rp3); u.Config != ConfigUnrecorded || u.Known() || u.ConfigPaths != nil || u.ConfigDigest != "" {
		t.Fatalf("unrecorded %+v", u)
	}

	r := build(t, analyzerFixture(t, nil, goVet(t), staticcheckTask(t)).in, DefaultRegistry())
	row := toolRow(r, "staticcheck")
	row.RuleContext = rec
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	p := Project(r, false)
	pc := toolRow(p, "staticcheck").RuleContext
	if pc == nil || pc.Config != ConfigRecorded || pc.ConfigPaths != nil || pc.ConfigDigest != "" || pc.RepositoryConfig != obsdoc.RepoConfigHonored {
		t.Fatalf("code-free context %+v", pc)
	}
	if row.RuleContext.ConfigDigest == "" || row.RuleContext.ConfigPaths == nil {
		t.Fatal("Project changed the full report")
	}
	if err := ValidateReport(p); err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), rec.ConfigDigest) || strings.Contains(string(b), "staticcheck.conf") {
		t.Fatal("code-free export carries the configuration digest or paths")
	}
	leak := p
	leak.Coverage.Tools = slices.Clone(p.Coverage.Tools)
	c := *pc
	c.ConfigDigest = rec.ConfigDigest
	toolRow(leak, "staticcheck").RuleContext = &c
	if err := ValidateReport(leak); err == nil {
		t.Fatal("code-free export with a configuration digest accepted")
	}
	// The state follows repository_config.
	for _, edit := range []func(c *RuleContext){
		func(c *RuleContext) { c.Config = ConfigNotRead },
		func(c *RuleContext) {
			c.RepositoryConfig, c.Config, c.ConfigPaths, c.ConfigDigest = obsdoc.RepoConfigOverridden, ConfigRecorded, nil, ""
		},
		func(c *RuleContext) { c.ConfigDigest = "" },
		func(c *RuleContext) {
			c.SourceSuppressions, c.UnreportedSuppressionRules = obsdoc.SuppressHonored, []string{"U1000"}
		},
		func(c *RuleContext) { c.UnreportedSuppressionRules = []string{"U1000", "SA4006"} },
	} {
		c := *rec
		edit(&c)
		row.RuleContext = &c
		if err := ValidateReport(r); err == nil {
			t.Fatalf("invalid context accepted %+v", c)
		}
	}
}
