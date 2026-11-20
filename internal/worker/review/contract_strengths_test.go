package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestContractFollowupUsesPromisedTermFromAcceptedSliceStrength(t *testing.T) {
	j := coverageJob(t)
	h := Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: coverageScript(t)}}
	runRoleWith(t, j, h, "1.27.4")
	s := roleStage{j: j, ctx: t.Context(), h: h}
	_, docs := s.prior(record.StageDocuments)
	_, overview := s.prior(record.StageInvestigate)
	scope, scopeUp := s.readScope()
	if scopeUp.State != record.UpstreamAccepted {
		t.Fatalf("scope: %+v", scopeUp)
	}
	files := s.entries()
	var e record.Excerpt
	var bound record.Upstream
	for _, slot := range scope.Slices {
		slice, up := s.prior(record.StageInvestigate + "." + slot.Key)
		if up.State != record.UpstreamAccepted {
			continue
		}
		for _, x := range slice.Evidence {
			if x.Kind != "code" || !strings.HasPrefix(x.Path, "store/") {
				continue
			}
			_, text, err := s.contractReadWindow(x, files)
			if err != nil {
				t.Fatal(err)
			}
			if contractStrengthSource(x, text, []string{"s.limit"}, nil) {
				e = x
				break
			}
		}
		if e.ID != "" {
			slice.Strengths = []record.Strength{{Title: "상한 s.limit에 맞춰 저장소를 유지", Benefit: "설정한 상한을 넘으면 오래된 항목을 제거한다.", Scope: "store", EvidenceIDs: []string{e.ID}}}
			if _, err := persistRecord(j, slice); err != nil {
				t.Fatal(err)
			}
			_, bound = s.prior(record.StageInvestigate + "." + slot.Key)
			break
		}
	}
	if e.ID == "" || bound.State != record.UpstreamAccepted {
		t.Fatalf("missing code in accepted slice: %+v, %+v", e, bound)
	}
	prior := record.ContractComparison{AppliesTo: "store"}
	strengthTerms := contractStrengthTerms("s.limit을 넘으면 오래된 항목을 제거한다.", []record.Excerpt{{Text: "When s.limit is exceeded, evict the oldest entry."}})
	if !slices.Equal(strengthTerms, []string{"s.limit"}) {
		t.Fatalf("document binding: %+v", strengthTerms)
	}
	for _, test := range []struct {
		name   string
		up     record.Upstream
		terms  []string
		before []record.Excerpt
		want   bool
	}{
		{"cites fresh implementation", overview, strengthTerms, nil, true},
		{"no name in original document", overview, nil, nil, false},
		{"previously read", overview, strengthTerms, []record.Excerpt{e}, false},
		{"rejected lineage", record.Upstream{Source: overview.Source, State: overview.State, SHA256: strings.Repeat("0", 64)}, strengthTerms, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := s.contractSliceExcerpts(record.ContractProbe{}, prior, nil, docs, test.up, files, test.terms, test.before)
			if err != nil || (len(out) == 1) != test.want {
				t.Fatalf("slice selection: %+v, %v", out, err)
			}
			if test.want && (!out[0].fromStrength || out[0].excerpt.ID != e.ID || strings.TrimSpace(out[0].excerpt.Text) == "" ||
				!slices.Contains(out[0].sources, scopeUp) || !slices.Contains(out[0].sources, bound)) {
				t.Fatalf("strength selection lost source citation or accepted lineage: %+v", out)
			}
		})
	}
}

func TestContractFollowupIgnoresTermInTextWithoutRuntimeUse(t *testing.T) {
	const text = "// Previous release used Option.RangeV2.\nexport function max(limit: number) {\n  return limit - 1;\n}\n"
	if contractStrengthSource(record.Excerpt{Kind: "code", Path: "options.ts", Start: 1, End: 4}, text, []string{"Option.RangeV2"}, nil) {
		t.Fatal("comment or unrelated return treated as referenced implementation")
	}
}
