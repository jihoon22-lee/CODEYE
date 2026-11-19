package report

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Fingerprint identity (docs/REPORT.md#발견-식별-fp2).

func TestToolFingerprintIgnoresPositions(t *testing.T) {
	base, b := ToolFingerprint("go.vet", "lostcancel", "pkg/a.go", "", "this return statement may be reached without using the cancel var defined on line 12")
	if !ValidFingerprint(base) || b.Context != ContextUnavailable || !ValidFingerprint(b.MoveKey) || !slices.Equal(b.Paths, []string{"pkg/a.go"}) || b.Symbols == nil {
		t.Fatalf("basis %q %+v", base, b)
	}
	same := []string{
		"this return statement may be reached without using the cancel var defined on line 40",
		"this  return statement may be reached without using the cancel var defined on line 40 ",
	}
	for _, msg := range same {
		if fp, _ := ToolFingerprint("go.vet", "lostcancel", "pkg/a.go", "", msg); fp != base {
			t.Fatalf("line reference changed the fingerprint: %q", msg)
		}
	}
	ws1, _ := ToolFingerprint("go.vet", "copylocks", "a.go", "", "/tmp/ws-1/a.go:12:3: call of f copies lock value")
	ws2, _ := ToolFingerprint("go.vet", "copylocks", "a.go", "", "/var/x/ws-2/a.go:80:1: call of f copies lock value")
	if ws1 != ws2 {
		t.Fatal("workspace position changed the fingerprint")
	}
	other, ob := ToolFingerprint("go.vet", "lostcancel", "pkg/b.go", "", same[0])
	rule, _ := ToolFingerprint("go.vet", "printf", "pkg/a.go", "", same[0])
	msg, _ := ToolFingerprint("go.vet", "lostcancel", "pkg/a.go", "", "the cancel function is not used on all paths")
	if other == base || rule == base || msg == base {
		t.Fatal("path, rule or condition did not change the fingerprint")
	}
	if ob.MoveKey != b.MoveKey {
		t.Fatal("a moved diagnostic must share the move key (candidate match)")
	}
}

// TC-09: the identity of a model candidate comes from the evidence the
// platform fixed, not from the model's wording or citation order.
func TestModelFingerprintStableAcrossTitles(t *testing.T) {
	compose := func(edit func(states map[string]*taskState)) Finding {
		states, base := exampleRoleStages(t)
		if edit != nil {
			edit(states)
		}
		r, _, lim := compose(states, base)
		if len(r.Findings) != 2 || len(lim) != 0 {
			t.Fatalf("findings=%d limitations %v", len(r.Findings), lim)
		}
		if err := validateFinding(r.Findings[0], "snap", nil, nil); err != nil {
			t.Fatal(err)
		}
		return r.Findings[0]
	}
	first := compose(nil)
	b := first.FingerprintBasis
	if b == nil || b.Producer != ProducerModel || b.Rule != CauseCandidate+":"+first.Category || first.Validation == "unreviewed" || len(b.Paths) == 0 || !ValidFingerprint(first.Fingerprint) {
		t.Fatalf("model basis %+v (%s)", b, first.Validation)
	}
	reworded := compose(func(states map[string]*taskState) {
		for _, key := range []string{"review.investigate", "review.verify"} {
			c := &states[key].reviewResult.Role.Candidates[0]
			c.Title, c.Condition, c.Impact, c.Improvement = "다르게 쓴 제목: "+c.Title, "다른 조건", "다른 영향", "다른 개선"
			slices.Reverse(c.Evidence)
		}
	})
	if reworded.Title == first.Title || reworded.Fingerprint != first.Fingerprint {
		t.Fatalf("reworded candidate changed identity: %q vs %q", reworded.Fingerprint, first.Fingerprint)
	}
	// The verifier's category is not part of the identity either.
	if reworded.Category != first.Category {
		t.Fatal("fixture changed category")
	}
	// The cause class separates candidates: an unverified candidate and one
	// the verifier classified differently are other identities, so a
	// disposition on one never carries to the other.
	unverified := compose(func(states map[string]*taskState) { states["review.verify"].facts.Status = "failed" })
	if unverified.Validation != "unreviewed" || unverified.FingerprintBasis.Rule != CauseCandidate+":unclassified" || unverified.Fingerprint == first.Fingerprint {
		t.Fatalf("unverified candidate shares the verified identity (%s)", unverified.Validation)
	}
	recategorized := compose(func(states map[string]*taskState) {
		v := &states["review.verify"].reviewResult.Role.Verifications[0]
		if v.Category == "security" {
			v.Category = "concurrency"
		} else {
			v.Category = "security"
		}
	})
	if recategorized.Fingerprint == first.Fingerprint {
		t.Fatal("verifier cause class is not part of the identity")
	}
}

// Security review: a disposition never hides an ambiguous or weak match.
func TestDispositionNeverCarriesToAmbiguousOrWeakMatch(t *testing.T) {
	strong := &FingerprintBasis{Producer: ProducerTool, Analyzer: "go.vet", Rule: "printf", Paths: []string{"a.go"}}
	weak := &FingerprintBasis{Producer: ProducerModel, Analyzer: AnalyzerReview, Rule: CauseCandidate + ":correctness", Paths: []string{"a.go"}}
	withSymbol := &FingerprintBasis{Producer: ProducerModel, Analyzer: AnalyzerReview, Rule: CauseCandidate + ":correctness", Paths: []string{"a.go"}, Symbols: []string{"Run"}}
	fs, fw, fy := fingerprint(strong, "msg"), fingerprint(weak, ""), fingerprint(withSymbol, "")
	if weak.MoveKey != "" || !WeakIdentity(weak) || WeakIdentity(withSymbol) || WeakIdentity(strong) {
		t.Fatal("weak identity classification")
	}
	if !DispositionCarries(strong, 1) || DispositionCarries(strong, 2) || DispositionCarries(weak, 1) || !DispositionCarries(withSymbol, 1) {
		t.Fatal("carry rule")
	}
	finding := func(id, fp string, b *FingerprintBasis) Finding {
		bb := *b
		return Finding{ID: id, Fingerprint: fp, FingerprintBasis: &bb, Disposition: "open", Validation: "supported"}
	}
	r := Report{Findings: []Finding{finding("t1", fs, strong), finding("t2", fs, strong), finding("w1", fw, weak), finding("s1", fy, withSymbol)}}
	reviews := []ReviewFact{{Fingerprint: fs, Version: 1, Disposition: "suppressed"}, {Fingerprint: fw, Version: 1, Disposition: "false_positive"}, {Fingerprint: fy, Version: 2, Disposition: "accepted_risk"}}
	applyDispositions(&r, reviews, time.Now())
	got := map[string]string{}
	for _, f := range r.Findings {
		got[f.ID] = f.Disposition
	}
	if got["t1"] != "open" || got["t2"] != "open" || got["w1"] != "open" || got["s1"] != "accepted_risk" {
		t.Fatalf("dispositions %v", got)
	}
	if s := ComputeSummary(r.Findings, nil); s.ActionableFindings != 3 || s.HiddenFindings != 1 {
		t.Fatalf("summary %+v", s)
	}
}

func TestBuildStampsPublicationDisposition(t *testing.T) {
	f := newFixture(t, goVet(t))
	plain := build(t, f.in, DefaultRegistry())
	if len(plain.Coverage.Tools) != 1 || !slices.Contains(plain.Coverage.Tools[0].ProcessedPaths, plain.Findings[0].Locations[0].Path) {
		t.Fatalf("processed paths %+v", plain.Coverage.Tools)
	}
	count := map[string]int{}
	for _, x := range plain.Findings {
		count[x.Fingerprint]++
	}
	var target Finding
	for _, x := range plain.Findings {
		if count[x.Fingerprint] == 1 {
			target = x
			break
		}
	}
	if target.FingerprintBasis == nil || !strings.HasPrefix(target.Fingerprint, "fp2:") || plain.Builder.Version != BuilderVersion {
		t.Fatalf("finding identity %+v", target)
	}
	path := target.FingerprintBasis.Paths[0]
	dir := path
	if i := strings.LastIndex(path, "/"); i > 0 {
		dir = path[:i]
	}
	past := f.in.Now.Add(-time.Second)
	future := f.in.Now.Add(time.Hour)
	other := "elsewhere"
	for _, tc := range []struct {
		name   string
		review ReviewFact
		want   string
	}{
		{"applied", ReviewFact{Version: 2, Disposition: "false_positive", ExpiresAt: &future}, "false_positive"},
		{"path scope covers", ReviewFact{Version: 1, Disposition: "suppressed", PathPrefix: &dir}, "suppressed"},
		{"expired", ReviewFact{Version: 3, Disposition: "accepted_risk", ExpiresAt: &past}, "open"},
		{"path scope excludes", ReviewFact{Version: 1, Disposition: "suppressed", PathPrefix: &other}, "open"},
		{"reverted to open", ReviewFact{Version: 4, Disposition: "open"}, "open"},
		{"acknowledged stays actionable", ReviewFact{Version: 1, Disposition: "acknowledged"}, "acknowledged"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := f.in
			tc.review.Fingerprint = target.Fingerprint
			in.Reviews = []ReviewFact{tc.review}
			if InputDigest(in) == InputDigest(f.in) {
				t.Fatal("reviews are not part of the input digest")
			}
			r := build(t, in, DefaultRegistry())
			hidden := 0
			for _, x := range r.Findings {
				want := "open"
				if x.Fingerprint == target.Fingerprint {
					want = tc.want
				}
				if x.Disposition != want || (want == "open") != (x.DispositionVersion == nil) {
					t.Fatalf("finding %s disposition %s (version %v), want %s", x.ID, x.Disposition, x.DispositionVersion, want)
				}
				if HiddenDisposition(x.Disposition) {
					hidden++
				}
			}
			// Hidden items stay in the totals and pending counts.
			if r.Summary.HiddenFindings != hidden || r.Summary.TotalFindings != plain.Summary.TotalFindings || r.Summary.PendingCandidates != plain.Summary.PendingCandidates {
				t.Fatalf("summary %+v vs %+v", r.Summary, plain.Summary)
			}
			if err := ValidateReport(r); err != nil {
				t.Fatal(err)
			}
			body, err := Marshal(Project(r, false))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Unmarshal(body); err != nil {
				t.Fatal(err)
			}
		})
	}
	bad := f.in
	bad.Reviews = []ReviewFact{{Fingerprint: "go-vet-fp-v1:x", Version: 1, Disposition: "suppressed"}}
	if _, err := Build(bad, DefaultRegistry()); err == nil {
		t.Fatal("accepted a review for a non-fp2 fingerprint")
	}
}

// Security review: the code-free export carries no fingerprint, basis
// or move key (unkeyed digests over code-derived inputs).
func TestCodeFreeExportHasNoIdentity(t *testing.T) {
	v := int64(1)
	f := Finding{Disposition: "suppressed", DispositionVersion: &v, FingerprintBasis: &FingerprintBasis{Producer: ProducerModel, Analyzer: AnalyzerReview, Rule: CauseCandidate, Paths: []string{"a.go"}, Symbols: []string{"secretHelper"}}}
	f.Fingerprint = fingerprint(f.FingerprintBasis, "")
	out := Project(Report{Findings: []Finding{f}}, false)
	if g := out.Findings[0]; g.Fingerprint != "" || g.FingerprintBasis != nil || g.DispositionVersion != nil || g.Disposition != "suppressed" {
		t.Fatalf("code-free finding %+v", g)
	}
	if f.FingerprintBasis == nil || len(f.FingerprintBasis.Symbols) != 1 {
		t.Fatal("projection mutated the stored finding")
	}
	full := newFixture(t, goVet(t))
	body, err := Marshal(Project(build(t, full.in, DefaultRegistry()), false))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"fingerprint"`, `"fingerprint_basis"`, `"move_key"`, "fp2:"} {
		if strings.Contains(string(body), needle) {
			t.Fatalf("code-free export contains %s", needle)
		}
	}
}
