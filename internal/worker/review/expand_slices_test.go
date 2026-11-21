package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestVerifyLateGoSliceReceivesCallersAndTests(t *testing.T) {
	const marker = "return amount + 1"
	const title = "계산 결과에 추가된 단위"
	tree := map[string]string{
		"go.mod": "module example.org/billing\n\ngo 1.23\n",
		"value.go": "package billing\n\nfunc Amount(amount int) int {\n" + strings.Repeat("// preprocessing\n", 70) +
			"\t" + marker + "\n}\n",
		"request.go": "package billing\n\nfunc Recalculate(amount int) int {\nreturn Amount(amount)\n}\n",
		"amount_test.go": "package billing\n\nimport \"testing\"\n" +
			"\nfunc TestBillingAmount(t *testing.T) {\nif got := Amount(4); got != 5 { t.Fatal(got) }\n}\n",
	}
	j := treeJob(t, tree)
	script := coverageScript(t)
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		for _, e := range in.Scope.Excerpts {
			if e.Path == "value.go" && strings.Contains(e.Text, marker) && e.Start > 1 {
				return asJSON(map[string]any{"candidates": []record.CandidateText{{
					Title: title, Condition: "Amount로 수를 계산한다.", Impact: "계산된 값이 달라진다.",
					Improvement: "경계의 단위를 확인한다.", Counterpoint: "한 단위를 의도적으로 추가할 수 있다.",
					Evidence: []string{e.ID},
				}}})
			}
		}
		return asJSON(map[string]any{"candidates": []any{}})
	}
	verify := script["verify"]
	sawCandidate := false
	script["verify"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		if slices.ContainsFunc(in.Candidates, func(c record.Candidate) bool { return c.Title == title }) {
			sawCandidate = true
			if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return e.Path == "request.go" && e.Symbol == "Amount" && e.Origin == "caller" && strings.Contains(e.Text, "return Amount(amount)")
			}) || !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool {
				return e.Path == "amount_test.go" && e.Symbol == "Amount" && e.Origin == "test" && strings.Contains(e.Text, "Amount(4)")
			}) {
				t.Fatalf("candidate from a later window did not receive actual caller and test source: %+v", in.Scope.Excerpts)
			}
		}
		return verify(n, q)
	}
	_, results := runRoleWith(t, j, Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
	if !sawCandidate || !slices.ContainsFunc(filledSlots(t, j), func(key string) bool {
		return results["review.verify."+key].Status == domain.TaskSucceeded
	}) {
		t.Fatal("the later source slice did not reach verification")
	}
}

func TestVerifyMethodReceivesOnlyBoundCallerAndTest(t *testing.T) {
	const (
		marker = "return value + quota.offset"
		title  = "음수 수량의 보정 누락"
	)
	const implementation = "package billing\n\ntype Quota struct{ offset int }\n\n" +
		"func (quota Quota) Adjust(value int) int {\n"
	const callerSource = `package billing

func RequestAmount(value int) int {
	if value < 0 { return 0 }
	quota := Quota{offset: 2}
	return quota.Adjust(value)
}
`
	const testSource = `package billing

import "testing"

func TestAdjustment(t *testing.T) {
	if got := (Quota{offset: 2}).Adjust(5); got != 7 {
		t.Fatal(got)
	}
}
`
	for _, test := range []struct {
		name              string
		remove, ambiguous bool
	}{
		{name: "concrete receiver"},
		{name: "caller and test removed", remove: true},
		{name: "competing implementation", ambiguous: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			tree := map[string]string{
				"go.mod":   "module example.org/billing\n\ngo 1.23\n",
				"quota.go": implementation + strings.Repeat("// normalize input\n", 70) + marker + "\n}\n",
				"unrelated.go": `package billing

type Discount struct{}
func (Discount) Adjust(value int) int { return value }

func Promo(value int) int {
	return (Discount{}).Adjust(value)
}

func Dispatch(value int, rule interface{ Adjust(int) int }) int {
	return rule.Adjust(value)
}
`,
				"unrelated_test.go": `package billing

import "testing"

func TestDiscount(t *testing.T) {
	if got := (Discount{}).Adjust(5); got != 5 { t.Fatal(got) }
}

func TestUnrelatedAssertion(t *testing.T) {
	_ = (Quota{offset: 2}).Adjust(5)
	if 2 != 2 { t.Fatal("different condition") }
}
`,
			}
			if !test.remove {
				tree["request.go"] = callerSource
				tree["quota_test.go"] = testSource
			}
			if test.ambiguous {
				tree["extra_windows.go"] = "//go:build windows\n\npackage billing\n" +
					"func (Quota) Adjust(value int) int { return value }\n"
			}
			j := treeJob(t, tree)
			script := coverageScript(t)
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				for _, e := range in.Scope.Excerpts {
					if e.Path == "quota.go" && strings.Contains(e.Text, marker) && e.Start > 1 {
						return asJSON(map[string]any{"candidates": []record.CandidateText{{
							Title: title, Condition: "음수 수량이 보정되지 않는다.", Impact: "요청 수량이 음수가 된다.",
							Improvement: "호출 경계에서 음수 입력을 제어한다.", Counterpoint: "호출자가 값을 제한할 수 있다.",
							Evidence: []string{e.ID},
						}}})
					}
				}
				return asJSON(map[string]any{"candidates": []any{}})
			}
			fallback := script["verify"]
			seen := false
			var evidence []string
			wantFound := !test.remove && !test.ambiguous
			script["verify"] = func(attempt int, q opencode.Request) opencode.Reply {
				in := decodeAs[verifyInput](t, q)
				if len(in.Candidates) != 1 || in.Candidates[0].Title != title {
					return fallback(attempt, q)
				}
				seen = true
				for _, e := range in.Scope.Excerpts {
					switch e.Origin {
					case "caller":
						if !wantFound || e.Path != "request.go" || e.Symbol != "Adjust" ||
							!strings.Contains(e.Text, "value < 0") || !strings.Contains(e.Text, "quota.Adjust(value)") {
							t.Fatalf("unbound method caller: %+v", e)
						}
					case "test":
						if !wantFound || e.Path != "quota_test.go" || e.Symbol != "Adjust" ||
							!strings.Contains(e.Text, "Adjust(5); got != 7") || !strings.Contains(e.Text, "t.Fatal(got)") {
							t.Fatalf("unbound method test: %+v", e)
						}
					default:
						continue
					}
					if e.ID != record.ExcerptID(j.Snapshot.Digest, e.Path, e.SHA256, e.Start, e.End) {
						t.Fatalf("extension lacks snapshot identity: %+v", e)
					}
					evidence = append(evidence, e.ID)
				}
				if wantFound {
					if len(evidence) != 2 {
						t.Fatalf("expected both caller and assertion in the real request: %+v", in.Scope.Excerpts)
					}
					return asJSON(map[string]any{"verifications": []any{verdict(in.Candidates[0].ID, "refuted", func(v map[string]any) {
						v["counterevidence_ids"] = evidence
						v["invalid_conditions"] = []string{"요청 진입점에서 음수를 먼저 반환 처리한다."}
					})}})
				}
				if test.remove {
					missing := "ev_" + strings.Repeat("0", 24)
					if in.Repair == nil {
						return asJSON(map[string]any{"verifications": []any{verdict(in.Candidates[0].ID, "refuted", func(v map[string]any) {
							v["counterevidence_ids"] = []string{missing}
							v["invalid_conditions"] = []string{"누락된 호출자가 음수를 처리한다고 주장한다."}
						})}})
					}
					if !slices.ContainsFunc(in.Repair.Candidates, func(c repairItem) bool {
						return c.ID == in.Candidates[0].ID && slices.Contains(c.Errors, "evidence_id_unknown")
					}) || slices.Contains(in.Repair.AcceptedIDs, missing) {
						t.Fatalf("a source ID absent from the request was treated as valid: %+v", in.Repair)
					}
				}
				return asJSON(map[string]any{"verifications": []any{verdict(in.Candidates[0].ID, "inconclusive", func(v map[string]any) {
					v["unresolved_assumptions"] = []string{"구체 호출자의 입력 범위를 확인하지 못했다."}
				})}})
			}
			_, results := runRoleWith(t, j, Handler{Static: secretsStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
			if !seen {
				t.Fatal("the candidate from the late source window did not reach verification")
			}
			for _, key := range filledSlots(t, j) {
				task := "review.verify." + key
				if results[task].Status != domain.TaskSucceeded {
					continue
				}
				name := "verify." + key
				raw := mustRead(t, recordPath(j, name))
				r, err := record.Decode(raw, expectRecord(j, name))
				if err != nil || strings.Contains(string(raw), `"text":`) {
					t.Fatalf("invalid slice record: %v", err)
				}
				if len(r.Candidates) != 1 || r.Candidates[0].Title != title {
					continue
				}
				if !r.Accepted() || len(r.Verifications) != 1 {
					t.Fatalf("verification was not accepted: %+v", r.Verifications)
				}
				v := r.Verifications[0]
				if wantFound && (v.Verdict != "refuted" || !slices.Equal(v.Counterevidence, evidence)) ||
					!wantFound && (v.Verdict != "inconclusive" || len(v.Counterevidence) != 0) {
					t.Fatalf("unexpected accepted verdict: %+v", v)
				}
				return
			}
			t.Fatal("could not find the accepted verification record")
		})
	}
}

func TestVerifyExpansionPreservesScopeScreeningAndPRFunctionContext(t *testing.T) {
	j := treeJob(t, map[string]string{
		"go.mod":      "module example.org/billing\n\ngo 1.23\n",
		"amount.go":   "package billing\ntype Quota struct{}\nfunc (Quota) Adjust(n int) int { return n + 1 }\nfunc Amount(n int) int { return n+1 }\n",
		"request.go":  "package billing\nfunc Recalculate(n int) int { return (Quota{}).Adjust(n) }\n",
		"defaults.go": "package billing\nfunc DefaultAmount(n int) int { return Amount(n) }\n",
	})
	catalog := wholeExcerpts(t, j, "amount.go")
	candidates := []CandidateText{{Evidence: []string{catalog[0].ID}}}
	for _, tc := range []struct {
		name              string
		scope             domain.Scope
		withheld, changed map[string]bool
		want              []string
	}{
		{name: "full", scope: domain.Scope{Kind: "full"}, want: []string{"request.go", "defaults.go"}},
		{name: "paths", scope: domain.Scope{Kind: "paths", Paths: []string{"amount.go"}}},
		{name: "secret hold", scope: domain.Scope{Kind: "full"}, withheld: map[string]bool{"request.go": true}, want: []string{"defaults.go"}},
		{name: "pull request", scope: domain.Scope{Kind: "full"}, changed: map[string]bool{"amount.go": true}, want: []string{"defaults.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, lims, err := expand(t.Context(), j.Snapshot, tc.scope, catalog, candidates, tc.withheld, tc.changed)
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, e := range out {
				paths = append(paths, e.Path)
			}
			if !slices.Equal(paths, tc.want) || (tc.withheld != nil) != slices.Contains(lims, limSecretWithheld) {
				t.Fatalf("wrong extension scope: paths=%v limits=%v", paths, lims)
			}
		})
	}
}

func TestVerifyExpansionDoesNotGuessFromAmbiguousCitation(t *testing.T) {
	j := treeJob(t, map[string]string{
		"go.mod": "module example.org/billing\n\ngo 1.23\n",
		"amount.go": `package billing
type Quota struct{}
func (Quota) Adjust(n int) int { return n + 1 }
type Discount struct{}
func (Discount) Adjust(n int) int { return n - 1 }
`,
		"request.go": "package billing\nfunc Recalculate(n int) int { return (Quota{}).Adjust(n) }\n",
		"promo.go":   "package billing\nfunc Promo(n int) int { return (Discount{}).Adjust(n) }\n",
	})
	catalog := wholeExcerpts(t, j, "amount.go")
	candidate := CandidateText{Title: "보정 함수 `Adjust`", Evidence: []string{catalog[0].ID}}
	out, lims, err := expand(t.Context(), j.Snapshot, j.Scope, catalog, []CandidateText{candidate}, nil, nil)
	if err != nil || len(out) != 0 || !slices.Contains(lims, limNoSymbol) {
		t.Fatalf("unqualified name selected an ambiguous symbol: %v %v %v", out, lims, err)
	}
	candidate.Title = "보정 함수 `Quota.Adjust`"
	out, _, err = expand(t.Context(), j.Snapshot, j.Scope, catalog, []CandidateText{candidate}, nil, nil)
	if err != nil || len(out) != 1 || out[0].Path != "request.go" {
		t.Fatalf("qualified name did not select the cited declaration: %v %v", out, err)
	}
}

func TestVerifyPromotesExistingEvidenceWithoutBreakingPublication(t *testing.T) {
	const (
		callerPath = "request.go"
		otherPath  = "a_defaults.go"
		title      = "음수 수량의 보정 누락"
	)
	// Put each declaration at the beginning of a later slice window. The
	// caller window can then match the investigation context byte for byte.
	prefix := "package billing\n" + strings.Repeat("// Context.\n", roleMaxCodeLines-1)
	j := treeJob(t, map[string]string{
		"go.mod": "module example.org/billing\n\ngo 1.23\n",
		"quota.go": prefix + `type Quota struct{}
func (Quota) Adjust(n int) int { return n + 1 }
`,
		otherPath: prefix + "func DefaultThreshold() int { return 3 }\n",
		callerPath: prefix + `func RequestAmount(n int) int {
	if n < 0 {
		return 0
	}
	return (Quota{}).Adjust(n)
}
`,
	})
	script := coverageScript(t)
	var prior, promoted record.Excerpt
	investigationOrder := func(excerpts []record.Excerpt, path string) int {
		return slices.IndexFunc(excerpts, func(e record.Excerpt) bool {
			return e.Path == path && e.Start == roleMaxCodeLines+1
		})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		anchor := investigationOrder(in.Scope.Excerpts, "quota.go")
		caller := investigationOrder(in.Scope.Excerpts, callerPath)
		other := investigationOrder(in.Scope.Excerpts, otherPath)
		if anchor < 0 || caller < 0 || other < 0 {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		if other >= caller {
			t.Fatal("an unrelated upstream excerpt must precede the caller in this fixture")
		}
		prior = in.Scope.Excerpts[caller]
		return asJSON(map[string]any{"candidates": []record.CandidateText{{
			Title: title, Condition: "음수 수량이 요청에 들어온다.", Impact: "계산된 결과의 부호가 바뀐다.",
			Improvement: "입력 범위를 확인한다.", Counterpoint: "호출자가 먼저 입력을 제한할 수 있다.",
			Evidence: []string{in.Scope.Excerpts[anchor].ID},
		}}})
	}
	script["verify"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[verifyInput](t, q)
		caller := investigationOrder(in.Scope.Excerpts, callerPath)
		other := investigationOrder(in.Scope.Excerpts, otherPath)
		if len(in.Candidates) != 1 || caller != 1 || other <= caller {
			t.Fatalf("the related context must move ahead of unrelated context: %+v", in.Scope.Excerpts)
		}
		promoted = in.Scope.Excerpts[caller]
		if promoted.ID != prior.ID || !strings.Contains(promoted.Text, "if n < 0") ||
			slices.ContainsFunc(in.Scope.Excerpts[caller+1:], func(e record.Excerpt) bool { return e.ID == prior.ID }) {
			t.Fatalf("verifier should receive the existing caller once: prior=%+v promoted=%+v", prior, promoted)
		}
		return asJSON(map[string]any{"verifications": []any{verdict(in.Candidates[0].ID, "refuted", func(v map[string]any) {
			v["counterevidence_ids"] = []string{promoted.ID}
			v["invalid_conditions"] = []string{"요청 진입점은 음수 수량에서 반환한다."}
		})}})
	}
	pinExampleSnapshot(t, &j)
	published := sliceReportWithCaller(t, j, secretsStub{}, DefaultMaxCalls, &exampleIDs{n: 0x602},
		&scriptedCaller{script: script})
	accepted := false
	for _, key := range filledSlots(t, j) {
		name := "verify." + key
		verified, err := record.Decode(mustRead(t, recordPath(j, name)), expectRecord(j, name))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(verified.Candidates, func(candidate record.Candidate) bool { return candidate.Title == title }) {
			continue
		}
		accepted = verified.Accepted() && len(verified.Verifications) == 1 &&
			verified.Verifications[0].Verdict == "refuted"
	}
	if !accepted {
		t.Fatal("the worker did not accept the scripted verification")
	}
	index := slices.IndexFunc(published.Findings, func(f report.Finding) bool { return strings.HasSuffix(f.Title, title) })
	if prior.ID == "" || promoted.ID == "" || index < 0 {
		t.Fatal("the real slice verification did not reach the report")
	}
	finding := published.Findings[index]
	if finding.Validation != report.ValidationRefuted || !slices.Equal(finding.CounterevidenceIDs, []string{prior.ID}) {
		t.Fatalf("report rejected the accepted verification: validation=%s, limits=%v, provenance=%s/%s",
			finding.Validation, finding.Limitations, promoted.Origin, promoted.Symbol)
	}
	if prior != promoted || prior.Origin != "context" || prior.Symbol != "" {
		t.Fatalf("promoting the caller changed its upstream evidence metadata: prior=%+v promoted=%+v", prior, promoted)
	}
}
