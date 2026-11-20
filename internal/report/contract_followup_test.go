package report

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// TC-269: the report independently checks the final record before replacing
// an earlier comparison, with model exposure accounted even on failure.
func TestFinalContractReportRequiresAcceptedLineage(t *testing.T) {
	for _, tc := range []struct {
		name, taskState, prior, final string
		edit                          func(*record.Record)
		replaced, sent, rejected      bool
		observed, reuseOld            bool
	}{
		{name: "matching implementation", taskState: "succeeded", prior: "unknown", final: "match", replaced: true, sent: true},
		{name: "partial implementation", taskState: "succeeded", prior: "unknown", final: "partial", replaced: true, sent: true},
		{name: "mismatch", taskState: "succeeded", prior: "unknown", final: "mismatch", replaced: true, sent: true},
		{name: "unknown with newly cited implementation", taskState: "succeeded", prior: "unknown", final: "unknown", sent: true, observed: true},
		{name: "unknown with old implementation and new test", taskState: "succeeded", prior: "unknown", final: "unknown", sent: true, reuseOld: true},
		{name: "unknown without citations", taskState: "succeeded", prior: "unknown", final: "unknown", sent: true,
			edit: func(r *record.Record) { r.ContractComparisons[0].CodeEvidenceIDs = []string{} }},
		{name: "unknown with declarations only", taskState: "succeeded", prior: "unknown", final: "unknown", sent: true,
			edit: func(r *record.Record) {
				r.Evidence[0] = newContractTestExcerpt(r.Snapshot, "src/client.d.ts", "code")
				r.ContractComparisons[0].CodeEvidenceIDs = []string{r.Evidence[0].ID}
			}},
		{name: "unknown with stale lineage", taskState: "succeeded", prior: "unknown", final: "unknown", sent: true, rejected: true,
			edit: func(r *record.Record) { r.Upstream[0].SHA256 = domain.Digest([]byte("different inputs")) }},
		{name: "preexisting match", taskState: "succeeded", prior: "match", final: "mismatch", sent: true, rejected: true},
		{name: "preexisting mismatch", taskState: "succeeded", prior: "mismatch", final: "match", sent: true, rejected: true},
		{name: "preexisting partial", taskState: "succeeded", prior: "partial", final: "match", sent: true, rejected: true},
		{name: "stale document", taskState: "succeeded", prior: "unknown", final: "match", sent: true, rejected: true,
			edit: func(r *record.Record) { r.Upstream[0].SHA256 = domain.Digest([]byte("different inputs")) }},
		{name: "different scope", taskState: "succeeded", prior: "unknown", final: "match", sent: true, rejected: true,
			edit: func(r *record.Record) { r.ContractComparisons[0].AppliesTo = "src" }},
		{name: "test only", taskState: "succeeded", prior: "unknown", final: "match", sent: true, rejected: true,
			edit: func(r *record.Record) {
				r.ContractComparisons[0].CodeEvidenceIDs = r.ContractComparisons[0].CodeEvidenceIDs[1:]
			}},
		{name: "failed response", taskState: "failed", prior: "unknown", final: "", sent: true},
		{name: "model disabled", taskState: "skipped_policy", prior: "unknown"},
		{name: "budget exhausted", taskState: "budget_exhausted", prior: "unknown"},
		{name: "older attempt", prior: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			states, base := exampleStages(t)
			docState, invState := states["review.documents"], states["review.investigate"]
			docs, inv := docState.reviewResult.Record, invState.reviewResult.Record
			docs.Conflicts = nil
			priorCode := inv.Evidence[0]
			impl := newContractTestExcerpt(inv.Snapshot, "src/checkout.go", "code")
			test := newContractTestExcerpt(inv.Snapshot, "src/checkout_test.go", "test")
			loc := record.ContractLocation{Path: impl.Path, SHA256: impl.SHA256, Start: impl.Start, End: impl.End, Kind: impl.Kind}
			loc.ID = record.ContractLocationID(inv.Snapshot, loc.Path, loc.SHA256, loc.Start, loc.End)
			inv.ContractProbe = &record.ContractProbe{ContractIndex: 1, Candidates: []record.ContractLocation{loc}}
			prior := record.ContractComparison{ContractIndex: 1, AppliesTo: ".", Status: tc.prior, Reason: "앞서 기록한 확인 범위입니다.",
				DocumentEvidenceIDs: docs.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{priorCode.ID}}
			inv.ContractComparisons = []record.ContractComparison{prior}

			call := inv.Calls[0]
			call.Role, call.RequestBytes = record.StageContract, 1024
			final := &record.Record{Stage: record.StageContract, Snapshot: docs.Snapshot, RunID: docs.RunID, AttemptID: docs.AttemptID,
				Schema: record.SchemaVersionContract, Pack: inv.Pack, Outcome: record.OutcomeAccepted, Calls: []record.Call{call},
				Observations: []record.ObservationRef{}, Limitations: []string{},
				Upstream: []record.Upstream{
					{Source: "review.documents", State: record.UpstreamAccepted, SHA256: docState.decodedSHA[record.MediaType]},
					{Source: "review.investigate", State: record.UpstreamAccepted, SHA256: invState.decodedSHA[record.MediaType]},
				},
				Evidence: []record.Excerpt{impl, test},
			}
			for _, e := range docs.Evidence {
				if slices.Contains(prior.DocumentEvidenceIDs, e.ID) {
					final.Evidence = append(final.Evidence, e)
				}
			}
			changed := prior
			changed.Status, changed.Reason, changed.CodeEvidenceIDs = tc.final, "유효한 항목에서 콜백 호출은 보이지만 공개 진입점과의 연결은 확인되지 않았습니다.", []string{impl.ID, test.ID}
			if tc.reuseOld {
				final.Evidence = append(final.Evidence, priorCode)
				changed.CodeEvidenceIDs = []string{priorCode.ID, test.ID}
			}
			if tc.final == "partial" {
				final.Evidence = append(final.Evidence, priorCode)
				changed.CodeEvidenceIDs = append(changed.CodeEvidenceIDs, priorCode.ID)
				final.PartialSupport = &record.ContractPartialSupport{
					Matching:  record.ContractConditionEvidence{Condition: "유효한 항목에서 정해진 콜백을 호출한다.", EvidenceID: impl.ID},
					Violating: record.ContractConditionEvidence{Condition: "잘못된 항목에서도 정해진 콜백을 호출한다.", EvidenceID: priorCode.ID},
				}
			}
			final.ContractComparisons = []record.ContractComparison{changed}
			if tc.taskState == "failed" {
				final.Outcome, final.Calls[0].Outcome, final.ContractComparisons = record.OutcomeFailed, "invalid_output", nil
			}
			if tc.edit != nil {
				tc.edit(final)
			}
			if tc.taskState != "" {
				st := &taskState{facts: TaskFacts{Key: record.TaskContract, Status: tc.taskState}}
				if tc.sent {
					st.reviewResult = &ReviewResult{Accepted: final.Accepted(), Record: final}
					st.review = final.Accepted()
				}
				if tc.replaced || tc.observed {
					index := &SourceIndex{entries: map[string]string{}}
					for _, e := range final.Evidence {
						index.entries[e.Path] = e.SHA256
					}
					body, err := record.Marshal(*final, record.Expect{Snapshot: final.Snapshot, RunID: final.RunID, AttemptID: final.AttemptID, Lookup: index.Lookup})
					if err != nil {
						t.Fatal(err)
					}
					art := ArtifactRef{Role: "other", MediaType: record.MediaType}
					decoder, ok := DefaultRegistry().lookup(art, body)
					if !ok {
						t.Fatal("the default publisher does not recognize the contract record")
					}
					c, err := decoder.Decode(DecodeContext{RunID: final.RunID, AttemptID: final.AttemptID,
						Task: st.facts, Snapshot: SnapshotFacts{ManifestDigest: final.Snapshot}, Source: index}, art, body)
					if err != nil {
						t.Fatal(err)
					}
					st.reviewResult = c.Review
				}
				states[record.TaskContract] = st
			}

			base.Coverage.TestStatus = "not_run"
			r, ai, limits := compose(states, base)
			wantCalls, wantFiles := 5, 7
			if tc.sent {
				wantCalls++
				wantFiles += 2
			}
			if len(r.Coverage.ReviewCalls) != wantCalls || r.Coverage.LLMExposedFiles == nil || *r.Coverage.LLMExposedFiles != wantFiles ||
				r.Coverage.TestStatus != "not_run" || ai.Status != AIReviewPerformed ||
				slices.Contains(limits, LimReviewLineage+":"+record.TaskContract) != tc.rejected {
				t.Fatalf("calls, exposure, test or lineage changed incorrectly: %+v, %+v, %v", r.Coverage, ai, limits)
			}
			if sentPaths(states, reviewRecordKeys(states))[test.Path] != tc.sent {
				t.Fatal("PR accounting did not use sent final requests")
			}
			section := &DocumentsSection{Contracts: make([]DocContract, len(docs.Contracts)), Evidence: evidenceOf(docs, prior.DocumentEvidenceIDs)}
			sectionContext{states: states}.addComparisons(section, docs)
			first := section.Comparisons[0]
			want := prior
			if tc.replaced {
				want = changed
			}
			if first.Status != want.Status || first.Reason != want.Reason || !slices.Equal(first.CodeEvidenceIDs, want.CodeEvidenceIDs) ||
				first.Followup != tc.replaced || section.Comparisons[1].Status != "unknown" {
				t.Fatalf("original or unrelated contract replaced incorrectly: %+v", section.Comparisons)
			}
			if (first.AdditionalObservation != nil) != tc.observed || tc.observed &&
				(first.AdditionalObservation.Reason != changed.Reason || !slices.Equal(first.AdditionalObservation.CodeEvidenceIDs, changed.CodeEvidenceIDs)) {
				t.Fatalf("additional observation %+v", first.AdditionalObservation)
			}
			if tc.replaced || tc.observed {
				r.Documents, r.Environment, r.CodeReview, r.Secrets = section, &EnvironmentSection{}, &CodeReviewSection{}, &SecretsSection{}
				key := sourceexcerpt.Key{Path: impl.Path, SHA256: impl.SHA256, Line: impl.Start}
				attachExcerpts(&r, map[sourceexcerpt.Key][]sourceexcerpt.Excerpt{
					key: {{StartLine: impl.Start, EndLine: impl.End, Text: "return checkout()"}},
				}, false)
				found := slices.ContainsFunc(section.Evidence, func(e SectionEvidence) bool { return e.ID == impl.ID && e.Excerpt != nil })
				if !found {
					t.Fatal("the newly cited source range lost its saved excerpt")
				}
			}
		})
	}
}

func newContractTestExcerpt(snapshot, path, kind string) record.Excerpt {
	sha := domain.Digest([]byte(path))
	return record.Excerpt{ID: record.ExcerptID(snapshot, path, sha, 20, 20), Path: path, SHA256: sha, Start: 20, End: 20, Kind: kind, Origin: "context"}
}

func TestContractFollowupCodeFreeUsesStoredStatusAndLocation(t *testing.T) {
	full := exampleReport(t)
	withAcceptedContractRole(t, &full)
	row := &full.Documents.Comparisons[0]
	row.Followup = true
	overview := overviewOf(full)
	full.Overview = &overview
	for _, include := range []bool{true, false} {
		r := Project(full, include)
		raw, err := Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Unmarshal(raw); err != nil {
			t.Fatal(err)
		}
		for _, detail := range []string{DetailSummary, DetailFull} {
			html, err := RenderHTMLAt(r, "0", Korean, detail)
			if err != nil {
				t.Fatal(err)
			}
			md := RenderMarkdownAt(r, "0", Korean, detail)
			for _, output := range []string{string(html), string(md)} {
				contains := func(s string) bool { return strings.Contains(output, s) || strings.Contains(output, mdText(s)) }
				if !contains(Korean.Label("decision.followup")) || !contains("gateway.go") || contains(row.Reason) != include {
					t.Fatalf("followup projection detail=%s include_code=%t is inconsistent", detail, include)
				}
			}
		}
	}
	full.Coverage.AIReview.OptionalTasks = nil
	if err := ValidateReport(full); err == nil {
		t.Fatal("followup without an accepted optional role validated")
	}
}

func withAcceptedContractRole(t *testing.T, full *Report) {
	t.Helper()
	full.Builder.Version = BuilderVersion
	full.Tasks = append(slices.DeleteFunc(full.Tasks, func(t Task) bool { return t.Key == record.TaskContract }),
		Task{ID: domain.NewID(), Key: record.TaskContract, Status: "succeeded"})
	role := AIReviewTask{Key: record.TaskContract, Result: "accepted", TaskStatus: strPtr("succeeded")}
	full.Coverage.AIReview.OptionalTasks = append(slices.DeleteFunc(full.Coverage.AIReview.OptionalTasks,
		func(t AIReviewTask) bool { return t.Key == record.TaskContract }), role)
	full.CodeReview.Roles = append(slices.DeleteFunc(full.CodeReview.Roles, func(t AIReviewTask) bool { return t.Key == record.TaskContract }), role)
	call := full.Coverage.ReviewCalls[0]
	call.Role, call.TaskKey = record.StageContract, record.TaskContract
	full.Coverage.ReviewCalls = append(full.Coverage.ReviewCalls, call)
	full.CodeReview.Calls++
	full.CodeReview.AcceptedCalls++
}

func TestAdditionalUnknownObservationRendersWithoutChangingVerdict(t *testing.T) {
	full := exampleReport(t)
	withAcceptedContractRole(t, &full)
	for i := range full.Documents.Comparisons {
		full.Documents.Comparisons[i].Status = "unknown"
	}
	row := &full.Documents.Comparisons[0]
	row.Reason = "이전 조사에서는 공개 진입점까지 확인하지 못했습니다."
	priorOverview, priorVerdict := overviewOf(full), reportVerdict(full)
	extra := newContractTestExcerpt(full.Inputs.ManifestSHA256, "src/routes.go", "code")
	full.Documents.Evidence = append(full.Documents.Evidence, SectionEvidence{ID: extra.ID, Kind: extra.Kind, Path: extra.Path,
		BlobSHA256: extra.SHA256, StartLine: extra.Start, EndLine: extra.End})
	row.AdditionalObservation = &AdditionalCodeObservation{Reason: "유효한 항목에서 콜백을 호출하지만 공개 진입점과의 연결은 미확인입니다.", CodeEvidenceIDs: []string{extra.ID}}
	overview := overviewOf(full)
	full.Overview = &overview
	full.QualityVerdict = reportVerdict(full)
	originalCount := contractDifferences(full.Documents)
	for _, include := range []bool{true, false} {
		r := Project(full, include)
		raw, err := Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		read, err := Unmarshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		c := read.Documents.Comparisons[0]
		if c.AdditionalObservation == nil || c.Status != "unknown" || c.Followup || contractDifferences(read.Documents) != originalCount ||
			!slices.Equal(c.CodeEvidenceIDs, row.CodeEvidenceIDs) || !slices.Equal(c.AdditionalObservation.CodeEvidenceIDs, []string{extra.ID}) {
			t.Fatalf("additional observation changed the original comparison or its references: %+v", c)
		}
		decision := newSummaryView(r, "0", Korean).Decision
		if !reflect.DeepEqual(*r.Overview, priorOverview) || r.QualityVerdict != priorVerdict {
			t.Fatal("observed source changed quality or coverage counts")
		}
		if decision.Followup || !decision.AdditionalObservation || decision.State != Korean.Label("decision.comparison.unknown") ||
			len(decision.Sources) != 2 || decision.Sources[1].Location != "src/routes.go:20-20" ||
			(decision.Reason == row.AdditionalObservation.Reason) != include {
			t.Fatalf("unresolved observation opening: %+v", decision)
		}
		for _, detail := range []string{DetailSummary, DetailFull} {
			html, err := RenderHTMLAt(r, "0", Korean, detail)
			if err != nil {
				t.Fatal(err)
			}
			md := RenderMarkdownAt(r, "0", Korean, detail)
			for _, output := range []string{string(html), string(md)} {
				contains := func(s string) bool { return strings.Contains(output, s) || strings.Contains(output, mdText(s)) }
				if !contains(Korean.Label("decision.additional_observation")) || !contains("src/routes.go") ||
					contains(row.Reason) != include || contains(row.AdditionalObservation.Reason) != include {
					t.Fatalf("additional observation projection detail=%s include_code=%t is inconsistent", detail, include)
				}
			}
		}
	}
	row.AdditionalObservation.CodeEvidenceIDs = slices.Clone(row.CodeEvidenceIDs)
	if err := ValidateReport(full); err == nil {
		t.Fatal("additional observation with only the original lines validated")
	}
}
