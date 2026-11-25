package record

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// ShippedSourceIn is a pure function of the included file paths, the run scope
// and, for a pull request, the changed files: the worker, the record
// validation and the report builder all compute it.
func TestShippedSourceIn(t *testing.T) {
	all := []string{"go.mod", "svc/a.go", "svc/a_test.go", "examples/main.go", "README.md"}
	testsOnly := []string{"go.mod", "svc/a_test.go", "testdata/in.go", "examples/main.go"}
	full, paths := domain.Scope{Kind: "full"}, func(p ...string) domain.Scope { return domain.Scope{Kind: "paths", Paths: p} }
	for _, c := range []struct {
		name    string
		files   []string
		scope   domain.Scope
		pr      bool
		changed []string
		want    bool
	}{
		{"mixed scope", all, full, false, nil, true},
		{"empty scope kind is full", all, domain.Scope{}, false, nil, true},
		{"examples and tests only", testsOnly, full, false, nil, false},
		{"scope inside the examples", all, paths("examples"), false, nil, false},
		{"scope inside the tests", all, paths("svc/a_test.go"), false, nil, false},
		{"scope with shipped code", all, paths("svc"), false, nil, true},
		{"no source at all", []string{"README.md", "go.mod"}, full, false, nil, false},
		{"pull request that changes tests only", all, full, true, []string{"svc/a_test.go"}, false},
		{"pull request that changes shipped code", all, full, true, []string{"svc/a.go", "svc/a_test.go"}, true},
		{"pull request without reviewable files", all, full, true, nil, false},
		{"guessed directory names stay shipped", []string{"src/test/../demo/main.go", "internal/gen/g.go"}, full, false, nil, true},
	} {
		if got := ShippedSourceIn(c.files, c.scope, c.pr, c.changed); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// With no shipped code in the scope no candidate carries the marker.
func TestNotShippedMatchesHonoursTheScope(t *testing.T) {
	catalog := map[string]Excerpt{"ev_a": {ID: "ev_a", Path: "svc/a_test.go", Kind: "test"}}
	cand := []Candidate{{CandidateText: CandidateText{ID: "investigate_1", Evidence: []string{"ev_a"}}, Origin: StageInvestigate}}
	marked := []Verification{{CandidateID: "investigate_1", Verdict: VerdictUnreviewed, Limitations: []string{LimCandidateNotShipped}}}
	unmarked := []Verification{{CandidateID: "investigate_1", Verdict: VerdictInconclusive}}
	for _, c := range []struct {
		shipped  bool
		verdicts []Verification
		want     bool
	}{{true, marked, true}, {true, unmarked, false}, {false, marked, false}, {false, unmarked, true}} {
		if got := NotShippedMatches(cand, c.verdicts, catalog, c.shipped); got != c.want {
			t.Errorf("shipped %v marked %v: %v, want %v", c.shipped, c.verdicts[0].Verdict == VerdictUnreviewed, got, c.want)
		}
	}
}
