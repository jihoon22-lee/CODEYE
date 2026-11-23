package review

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func TestInvestigateKeepsFindingsWhenProbeIsInvalid(t *testing.T) {
	// The files are longer than the request holds, so the contract's
	// implementation line stays unread and navigation can offer it.
	longPrefix := strings.Repeat("// unrelated setup\n", 700)
	impl, tests := "shade/control.ts", "shade/control.test.ts"
	base := map[string]string{
		"shade/README.md":    "# 조명\n\n창을 기울이면 빛이 줄어든다.\n",
		"shade/package.json": "{}\n",
		impl:                 longPrefix + "export function tiltShutter() { return 2; }\n",
		tests:                longPrefix + "test('shade', () => expect(tiltShutter()).toBe(2));\n",
		"shade/secret.ts":    longPrefix + "export function blockedCell() { return 5; }\n",
		"shade/demo/ui.ts":   "export function demoShutter() { return 3; }\n",
		"shade/build/ui.ts":  "export function builtShutter() { return 4; }\n",
		"elsewhere/door.ts":  "export function openDoor() { return 8; }\n",
	}
	for _, name := range []string{"valid", "canonical", "absent", "null", "wrong type", "unoffered id", "inexact alias", "injected literal", "duplicate id", "invented literal", "test only", "known comparison"} {
		t.Run(name, func(t *testing.T) {
			j := treeJob(t, maps.Clone(base))
			stub := langStub{eco: "javascript", path: impl, line: 701, manifest: "shade/package.json", secrets: "hit", secretPath: "shade/secret.ts"}
			script := maxScript(t)
			script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[docsInput](t, q)
				ids := []string{in.Documents[0].EvidenceID}
				return asJSON(docsOutput{
					Contracts: []record.Contract{{Statement: "창을 기울이면 빛이 줄어든다.", AppliesTo: "shade", EvidenceIDs: ids}},
					Questions: []record.Question{{Question: "빛의 실제 단계를 어디서 정하는가?", AppliesTo: "shade", EvidenceIDs: ids}},
					Conflicts: []record.Conflict{}, InstructionNotes: []record.InstructionNote{},
				})
			}
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				if !in.Scope.WholeRun {
					if len(in.NavigationCandidates) != 0 {
						t.Fatal("whole-run navigation reached a module review")
					}
					return asJSON(map[string]any{"candidates": []any{}})
				}
				selected := map[string]contractCandidateInput{}
				for i, candidate := range in.NavigationCandidates {
					if candidate.ID != navigationAlias(i) || (candidate.Path != impl && candidate.Path != tests) {
						t.Fatalf("navigation offered an ineligible file: %+v", candidate)
					}
					selected[candidate.Path] = candidate
				}
				if selected[impl].Start < 600 || selected[impl].Symbol != "tiltShutter" ||
					selected[tests].Start < 600 || selected[tests].Kind != "test" || len(in.Documents.Questions) != 1 {
					t.Fatalf("translated contract missing code or test locations: %+v", in)
				}
				i := slices.IndexFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == impl })
				if i < 0 {
					t.Fatal("navigation displaced an existing code excerpt")
				}
				id := selected[impl].ID
				comparisons := []record.ContractComparison{{
					ContractIndex: 1, AppliesTo: "shade", Status: "unknown", Reason: "빛의 결과가 결정되는 구현을 찾지 못했다.",
					DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{},
				}}
				if name == "valid" || name == "canonical" {
					comparisons[0].Reason += " " + id + "에서 구현 여부와 밝기 단계를 확인할 수 있다."
				}
				result := map[string]any{
					"candidates": []record.CandidateText{{
						Title: "빛 조정 오류를 사용자에게 표시하지 않음", Condition: "상태 표시가 필요한 경우", Mechanism: "조정 결과가 화면 상태로 전달되지 않는다.",
						Impact: "사용자가 밝기를 판단하기 어렵다.", Improvement: "조정 결과를 표시한다.",
						Counterpoint: "다른 화면의 상태 표시를 읽지 않았다.", Evidence: []string{cite(in.Scope.Excerpts[i])},
					}},
					"contract_comparisons": comparisons,
				}
				probe := map[string]any{"contract_index": 1, "candidate_ids": []string{id, selected[tests].ID}, "literals": []string{"tiltShutter"}}
				result["contract_probe"] = probe
				implementation := selected[impl]
				s := roleStage{j: j}
				canonical := record.ContractLocationID(j.Snapshot.Digest, impl, s.entries()[impl].SHA256, implementation.Start, implementation.End)
				switch name {
				case "canonical":
					probe["candidate_ids"] = []string{canonical, selected[tests].ID}
				case "absent":
					delete(result, "contract_probe")
				case "null":
					result["contract_probe"] = nil
				case "wrong type":
					result["contract_probe"] = "not an object"
				case "unoffered id":
					probe["candidate_ids"] = []string{"loc_" + strings.Repeat("1", 24)}
				case "inexact alias":
					probe["candidate_ids"] = []string{strings.ToLower(id)}
				case "injected literal":
					probe["literals"] = []string{"tiltShutter()"}
				case "duplicate id":
					probe["candidate_ids"] = []string{id, canonical}
				case "invented literal":
					probe["literals"] = []string{"unseenFunction"}
				case "test only":
					probe["candidate_ids"] = []string{selected[tests].ID}
				case "known comparison":
					comparisons[0].Status = "partial"
					comparisons[0].CodeEvidenceIDs = []string{in.Scope.Excerpts[i].ID}
				}
				return asJSON(result)
			}
			f := &scriptedCaller{script: script}
			_, results := runRoleSmall(t, j, stub, f, "1.27.4")
			if r := results["review.investigate"]; r.Status != domain.TaskSucceeded {
				t.Fatalf("probe affected the initial investigation: %+v", r)
			}
			r := readRecord(t, j, "investigate")
			if len(r.Candidates) != 1 || len(r.ContractComparisons) != 1 || len(r.Calls) != 1 {
				t.Fatalf("ordinary result affected by optional probe: %+v", r)
			}
			wantObservation := record.LimNavigationChoiceInvalid
			switch name {
			case "valid", "canonical", "known comparison":
				wantObservation = ""
			case "absent", "null":
				wantObservation = record.LimNavigationNoChoice
			}
			for _, code := range []string{record.LimNavigationNoChoice, record.LimNavigationChoiceInvalid} {
				if slices.Contains(r.Limitations, code) != (wantObservation == code) ||
					slices.Contains(results["review.investigate"].Limitations, code) || slices.Contains(r.PublicLimitations(), code) {
					t.Fatalf("unexpected internal or public navigation observation: %s, raw=%v", code, r.Limitations)
				}
			}
			if name != "valid" && name != "canonical" {
				if r.ContractProbe != nil || r.Schema != record.SchemaVersionCitedLines {
					t.Fatalf("invalid selection was persisted: %+v", r.ContractProbe)
				}
				return
			}
			p := r.ContractProbe
			if p == nil || r.Schema != record.SchemaVersionCitedLines || len(p.Candidates) != 2 || len(p.Literals) != 1 ||
				!p.Valid(j.Snapshot.Digest, r.ContractComparisons[0]) {
				t.Fatalf("valid snapshot-bound probe missing: %+v", r)
			}
			x, comparison := p.Candidates[0], r.ContractComparisons[0]
			if !strings.Contains(comparison.Reason, fmt.Sprintf("%s:%d–%d에서", x.Path, x.Start, x.End)) ||
				comparison.Status != "unknown" || len(comparison.CodeEvidenceIDs) != 0 {
				t.Fatalf("navigation reference or comparison state changed unexpectedly: %+v", comparison)
			}
			for _, x := range r.Evidence {
				if strings.HasPrefix(x.ID, "loc_") || x.Path == tests && x.Start > 600 {
					t.Fatalf("location metadata was counted as evidence: %+v", x)
				}
			}
		})
	}
}

func TestNavigationFitsAfterExistingExcerpts(t *testing.T) {
	s := roleStage{role: record.StageInvestigate}
	snapshot := strings.Repeat("1", 64)
	in := investigateInput{
		Scope:     investigateScope{Snapshot: snapshot, Excerpts: []record.Excerpt{{Text: "implementation already selected"}}},
		Documents: documentsHandoff{Contracts: []record.Contract{{AppliesTo: "."}}},
	}
	candidates := []contractNavigationCandidate{
		{location: record.ContractLocation{Path: "first.go", Kind: "code", SHA256: strings.Repeat("2", 64), Start: 2, End: 3}, indices: []int{1}},
		{location: record.ContractLocation{Path: "second.go", Kind: "code", SHA256: strings.Repeat("3", 64), Start: 4, End: 5}, indices: []int{1}},
	}
	for i := range candidates {
		x := &candidates[i].location
		x.ID = record.ContractLocationID(snapshot, x.Path, x.SHA256, x.Start, x.End)
	}
	full := in
	full.NavigationCandidates = []contractCandidateInput{candidates[0].input(0), candidates[1].input(1)}
	b, err := marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	contract := fmt.Sprintf(investigateContract, 4)
	instructions := s.instructions(contract + investigateProbeContract)
	fullSize, _ := s.fits(instructions, b, true)
	s.pack.Text = strings.Repeat("x", PromptBudget-fullSize+1)
	kept := s.fitNavigation(&in, candidates, contract)
	if len(kept) != 1 || len(in.NavigationCandidates) != 1 || in.NavigationCandidates[0].Path != "first.go" || in.NavigationCandidates[0].ID != "L1" ||
		!slices.Equal(in.Scope.Excerpts, full.Scope.Excerpts) {
		t.Fatalf("request did not trim location metadata before code: %+v", in)
	}
	comparisons := []record.ContractComparison{{AppliesTo: ".", ContractIndex: 1, Status: "unknown"}}
	probe, lim := selectContractProbe(asJSON(map[string]any{"contract_index": 1, "candidate_ids": []string{"L1"}}).Output, kept, comparisons, in)
	if probe == nil || lim != "" || probe.Candidates[0] != candidates[0].location {
		t.Fatalf("retained alias lost its canonical location: %+v %s", probe, lim)
	}
	for _, id := range []string{"L2", candidates[1].location.ID} {
		p, observation := selectContractProbe(asJSON(map[string]any{"contract_index": 1, "candidate_ids": []string{id}}).Output, kept, comparisons, in)
		if p != nil || observation != record.LimNavigationChoiceInvalid {
			t.Fatalf("trimmed location accepted on this request: %s %+v %s", id, p, observation)
		}
	}
	comparisons[0].AppliesTo = "other"
	if p, observation := selectContractProbe(nil, kept, comparisons, in); p != nil || observation != "" {
		t.Fatalf("no location applied to the selected scope: %+v %s", p, observation)
	}
}

func TestNavigationReachesNestedTestAssertions(t *testing.T) {
	const nested = "test/flows/retention.spec.ts"
	const helper = "test/helpers/setup.spec.ts"
	tree := map[string]string{
		"package.json": "{}\n",
		"app/panel.ts": "export function indicator() { return 1; }\n",
		nested: "function preparePanel() { return 0; }\n" + strings.Repeat("// setup for the panel\n", 24) +
			"test('opening the cover leaves the indicator unchanged', function () {\n" +
			"  const before = indicator();\n" +
			"  closeCover();\n" +
			"  openCover();\n" +
			"  expect(indicator()).toBe(before);\n" +
			"  expect(eventsForPanel()).toHaveLength(2);\n" +
			"});\n",
		helper: "function prepareBox() { expect(seed()).toBe(1); }\n" +
			"test('only setup', () => { function ignored() { expect(seed()).toBe(1); } prepareBox(); });\n",
	}
	for i := 0; i < navigationMaxFiles/2+2; i++ {
		tree[fmt.Sprintf("test/check%02d.spec.ts", i)] = "test('basic shape', () => { expect(marker()).toBe(1); });\n"
	}
	j := treeJob(t, tree)
	s := roleStage{j: j, ctx: t.Context()}
	groups := planCodeGroupsFor(j.Snapshot.Manifest, j.Scope, s.h.secondary(), nil)
	ordinary := s.navigationFiles(groups, []string{"."})
	if slices.ContainsFunc(ordinary, func(e source.Entry) bool { return e.Path == nested }) {
		t.Fatal("the shallow suite failed to occupy the original read lane")
	}
	files := s.boundedNavigationFiles(groups, []string{"."}, true)
	if len(files) != 1+navigationMaxFiles/2 {
		t.Fatalf("directory breadth changed the code/test read bounds: %d", len(files))
	}
	if reached := slices.IndexFunc(files, func(e source.Entry) bool { return e.Path == nested }); reached < 0 || reached > 3 {
		t.Fatalf("nested suite did not receive an early directory slot: %d", reached)
	}
	docs := record.Record{Contracts: []record.Contract{{Statement: "뚜껑을 닫았다 열어도 표시 상태가 유지된다.", AppliesTo: "."}}}
	cat := s.navigationCatalog(docs, nil, nil)
	i := slices.IndexFunc(cat, func(c contractNavigationCandidate) bool { return c.location.Path == nested })
	if i < 0 || cat[i].location.Start <= 24 || cat[i].location.Kind != "test" {
		t.Fatalf("the nested actual test was not offered: %+v", cat)
	}
	location := cat[i].location
	text, _, ok, err := s.readLines(s.entries()[nested], location.Start, location.End)
	if err != nil || !ok || !strings.Contains(text, "expect(indicator()).toBe(before)") || strings.Contains(text, "preparePanel") {
		t.Fatalf("the location missed the test's assertions: %q %v", text, err)
	}
	entry := s.entries()[helper]
	content := strings.Split(tree[helper], "\n")
	if _, ok := s.testNavigationLocation(entry, content); ok {
		t.Fatal("an assertion in a setup function was attributed to the named test")
	}
	if location, _, ok := s.structuralNavigationLocation(entry, content, nil); !ok || location.Symbol != "prepareBox" {
		t.Fatalf("ordinary anchor was unavailable after unbound test setup: %+v", location)
	}
}

func TestNavigationRanksFreshContractDecisionsBeforeTestSupport(t *testing.T) {
	const documented = "`GRID_ROOT`와 `FALLBACK_ROOT` 순서로 설정을 적용한다."
	const impl, tests, setup = "lib/resolve.ts", "test/resolve.spec.ts", "bootstrap.ts"
	implement := "export function gridRoot(env: Environment) {\n" +
		"  if (env.GRID_ROOT) return env.GRID_ROOT;\n" +
		"  return standardRoot(env);\n}\n" + strings.Repeat("// spacer\n", record.MaxContractLocationLines) +
		"export function fallbackRoot(env: Environment) {\n" +
		"  if (env.FALLBACK_ROOT) return env.FALLBACK_ROOT;\n" +
		"  return defaultRoot();\n}\n"
	tree := map[string]string{
		"README.md":    documented + "\n",
		"package.json": "{}\n",
		setup: "export function prepare() {\n" +
			"  const defaults = {GRID_ROOT: '/one', FALLBACK_ROOT: '/two'};\n" +
			"  registerDefaults(defaults);\n}\n",
		impl: implement,
		tests: "test('root precedence', () => {\n" +
			"  expect(gridRoot({GRID_ROOT: 'one', FALLBACK_ROOT: 'two'})).toBe('one');\n" +
			"  expect(fallbackRoot({FALLBACK_ROOT: 'two'})).toBe('two');\n});\n",
		"lib/maybe.ts": "export function candidate() {\n" +
			"  const notes = `\nif (env.GRID_ROOT) return env.GRID_ROOT;\n`;\n" +
			"  return notes;\n}\n",
	}
	for _, mode := range []string{"whole project", "unvisited witness", "PR excludes implementation", "withheld", "fully reviewed"} {
		t.Run(mode, func(t *testing.T) {
			files := maps.Clone(tree)
			var cached map[string]string
			if mode == "unvisited witness" {
				for i := range navigationMaxFiles / 2 {
					files[fmt.Sprintf("lib/a%02d.ts", i)] = "export function other() { return 2; }\n"
				}
				cached = map[string]string{impl: strings.TrimSuffix(implement, "\n")}
			}
			j := treeJob(t, files)
			if mode == "PR excludes implementation" {
				j.PullRequest = &domain.PullRequestJob{Number: 1, Changed: []string{tests, setup, "lib/maybe.ts"}}
			}
			s := roleStage{j: j, ctx: t.Context()}
			if mode == "withheld" {
				s.screen.withheld = map[string]bool{impl: true}
			}
			entries := s.entries()
			doc, _, ok, err := s.excerpt(entries["README.md"], "document", "context", 1, 1, roleMaxWindowBytes)
			if err != nil || !ok {
				t.Fatalf("document excerpt: %v", err)
			}
			docs := record.Record{Evidence: []record.Excerpt{doc}, Contracts: []record.Contract{
				{Statement: documented, AppliesTo: ".", EvidenceIDs: []string{doc.ID}},
			}}
			overview := []record.Excerpt{{Path: setup, SHA256: entries[setup].SHA256, Kind: "code", Start: 1, End: 2}}
			if mode == "fully reviewed" {
				overview = append(overview, record.Excerpt{Path: impl, SHA256: entries[impl].SHA256, Kind: "code", Start: 1, End: strings.Count(implement, "\n")})
			}
			if cached != nil {
				g := planCodeGroupsFor(j.Snapshot.Manifest, j.Scope, s.h.secondary(), changedPaths(j))
				if slices.ContainsFunc(s.boundedNavigationFiles(g, []string{"."}, true), func(e source.Entry) bool { return e.Path == impl }) {
					t.Fatal("bounded extra reads unexpectedly include the cached implementation")
				}
			}
			got := s.navigationCatalog(docs, overview, cached)
			if mode == "PR excludes implementation" || mode == "withheld" || mode == "fully reviewed" {
				if len(got) != 0 {
					t.Fatalf("unrelated or test-only source was offered: %+v", got)
				}
				return
			}
			if len(got) != 3 || got[0].location.Path != impl || got[1].location.Path != tests ||
				got[2].location.Path != impl || navigationOverlap(got[0].location, got[2].location) {
				t.Fatalf("expected two distinct implementation windows and supporting test: %+v", got)
			}
			for _, candidate := range []contractNavigationCandidate{got[0], got[2]} {
				p := candidate.location
				excerpt := record.Excerpt{Path: p.Path, SHA256: p.SHA256, Kind: p.Kind, Start: p.Start, End: p.End}
				_, fresh, err := s.contractImplementation(excerpt, overview, entries)
				if err != nil || !fresh {
					t.Fatalf("catalog and follow-up disagree at %+v: %v", p, err)
				}
			}
		})
	}
}

func TestNavigationOmitsAmbiguousStructuralCode(t *testing.T) {
	j := treeJob(t, map[string]string{
		"lib/frame.ts":  "export function takeFrame() { return 1; }\n",
		"lib/format.ts": "export function printFormat() { return 2; }\n",
		"test/spec.ts":  "test('shape', () => { expect(marker()).toBe(1); });\n",
	})
	s := roleStage{j: j, ctx: t.Context()}
	docs := record.Record{Contracts: []record.Contract{{Statement: "껐다 켜도 기록이 남는다.", AppliesTo: "."}}}
	if got := s.navigationCatalog(docs, nil, nil); len(got) != 0 {
		t.Fatalf("unrelated routine positions were attributed to an ambiguous contract: %+v", got)
	}
}

func TestContractProbeRereadsExactLocationsWithinScopeAndChanges(t *testing.T) {
	const chosen, matching = "unit/window.go", "unit/check.go"
	const hidden, unchanged = "unit/secret.go", "unit/unchanged.go"
	j := treeJob(t, map[string]string{
		"go.mod":         "module synthetic.test/panel\n\ngo 1.24\n",
		chosen:           "package unit\n\nfunc tiltPanel() int { return 4 }\n",
		matching:         "package unit\n\n// tiltPanel belongs in the test\nfunc testPanel() int { return tiltPanel() }\n",
		"unit/misfit.go": "package unit\nfunc tiltPaneltiltPanel() {}\n",
		hidden:           "package unit\nfunc hiddenCall() int { return tiltPanel() }\n",
		unchanged:        "package unit\nfunc unchangedCall() int { return tiltPanel() }\n",
		"outside/run.go": "package outside\nfunc outOfScope() int { return tiltPanel() }\n",
	})
	j.Scope = domain.Scope{Kind: "paths", Paths: []string{"unit"}}
	j.PullRequest = &domain.PullRequestJob{Number: 1, Changed: []string{chosen, matching, hidden, "unit/misfit.go", "outside/run.go"}}
	s := roleStage{j: j, ctx: t.Context(), screen: secretScreen{withheld: map[string]bool{hidden: true}}}
	e := s.entries()[chosen]
	location := record.ContractLocation{Path: chosen, SHA256: e.SHA256, Start: 3, End: 3, Kind: "code", Symbol: "tiltPanel"}
	location.ID = record.ContractLocationID(j.Snapshot.Digest, chosen, e.SHA256, location.Start, location.End)
	probe := record.ContractProbe{ContractIndex: 1, Candidates: []record.ContractLocation{location}, Literals: []string{"tiltPanel"}}
	comparison := record.ContractComparison{ContractIndex: 1, AppliesTo: ".", Status: "unknown"}
	got, err := s.contractProbeExcerpts(probe, comparison)
	if err != nil || len(got) != 2 || got[0].Path != chosen || got[0].Start != location.Start || got[0].End != location.End ||
		got[0].ID == location.ID || got[1].Path != matching || !strings.Contains(got[1].Text, "return tiltPanel()") {
		t.Fatalf("wrong source locations or ineligible literal search: %+v, %v", got, err)
	}
	for _, path := range []string{hidden, unchanged, "outside/run.go"} {
		c := &probe.Candidates[0]
		e := s.entries()[path]
		c.Path, c.SHA256, c.Start, c.End, c.Symbol = path, e.SHA256, 1, 2, ""
		c.ID = record.ContractLocationID(j.Snapshot.Digest, path, e.SHA256, 1, 2)
		if got, err := s.contractProbeExcerpts(probe, comparison); err == nil || got != nil {
			t.Fatalf("ineligible selected location %s was read: %+v, %v", path, got, err)
		}
	}
}

func contractExampleJob(t *testing.T, change func(string)) analysis.Job {
	t.Helper()
	j := modulesJob(t, func(root string) {
		// Occupy the ordinary slots with files that have no contract
		// implementation, so the payment implementation remains unread.
		for i := range roleMaxCodeFiles {
			writeFile(t, root, fmt.Sprintf("svc/b/auxiliary_%02d.go", i), "package b\n")
		}
		if change != nil {
			change(root)
		}
	})
	j.Snapshot.Digest, j.RunID, j.AttemptID = exampleSnapshot, exampleRun, exampleAttempt
	return j
}

func TestContractProbeRecordExample(t *testing.T) {
	j := contractExampleJob(t, nil)
	script := validScript(t)
	investigate := script[record.StageInvestigate]
	script[record.StageInvestigate] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		index := slices.IndexFunc(in.NavigationCandidates, func(c contractCandidateInput) bool {
			return c.Path == "svc/b/charge.go" && c.Symbol == "Charge" && slices.Contains(c.ContractIndices, 2)
		})
		if index < 0 {
			t.Fatalf("the fixture's charge implementation location was not offered: %+v", in.NavigationCandidates)
		}
		var out map[string]any
		if err := json.Unmarshal(investigate(n, q).Output, &out); err != nil {
			t.Fatal(err)
		}
		out["contract_comparisons"] = []record.ContractComparison{{
			ContractIndex: 2, AppliesTo: "svc/b", Status: "unknown", Reason: "결제 래퍼의 호출 경로를 읽어야 한다.",
			DocumentEvidenceIDs: in.Documents.Contracts[1].EvidenceIDs, CodeEvidenceIDs: []string{},
		}}
		out["contract_probe"] = map[string]any{
			"contract_index": 2, "candidate_ids": []string{in.NavigationCandidates[index].ID}, "literals": []string{"Charge"},
		}
		return asJSON(out)
	}
	// The smallest request leaves the charge implementation unsent, so the
	// investigation is offered its location; the default output budget keeps
	// the documents record the role examples publish (its upstream digest).
	_, results := runRoleWith(t, j, Handler{Static: vetStub{obs: gatewayObservation}, Caller: &scriptedCaller{script: script},
		Budget: Budget{RequestBytes: MinRequestBytes}}, "1.27.4")
	if r := results["review.investigate"]; r.Status != domain.TaskSucceeded {
		t.Fatalf("example probe failed: %+v", r)
	}
	path := filepath.Join(exampleDir, "investigate-probe.json")
	raw := mustRead(t, recordPath(j, record.StageInvestigate))
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	pretty.WriteByte('\n')
	if os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1" {
		if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	example := mustRead(t, path)
	if !bytes.Equal(example, pretty.Bytes()) {
		t.Fatalf("%s differs from the regenerated record", path)
	}
	want := expectStage(j, record.StageInvestigate)
	r, err := record.Decode(example, want)
	if err != nil || r.ContractProbe == nil || len(r.ContractComparisons) != 1 {
		t.Fatalf("example probe rejected: %+v, %v", r.ContractProbe, err)
	}
	s := roleStage{j: j, ctx: t.Context()}
	excerpts, err := s.contractProbeExcerpts(*r.ContractProbe, r.ContractComparisons[0])
	if err != nil || len(excerpts) != 1 || excerpts[0].Start != 5 || excerpts[0].End != 8 ||
		!strings.Contains(excerpts[0].Text, "return pay()") {
		t.Fatalf("example does not resolve to its snapshot source: %+v, %v", excerpts, err)
	}
	for name, edit := range map[string]func(*record.Record){
		"wrong version":     func(r *record.Record) { r.Schema = record.SchemaVersion },
		"another contract":  func(r *record.Record) { r.ContractProbe.ContractIndex = 1 },
		"comparison known":  func(r *record.Record) { r.ContractComparisons[0].Status = "partial" },
		"nonliteral search": func(r *record.Record) { r.ContractProbe.Literals = []string{"Charge()"} },
		"fabricated digest": func(r *record.Record) { r.ContractProbe.Candidates[0].SHA256 = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			bad.ContractComparisons = slices.Clone(r.ContractComparisons)
			probe := *r.ContractProbe
			probe.Candidates = slices.Clone(probe.Candidates)
			bad.ContractProbe = &probe
			edit(&bad)
			if _, err := record.Marshal(bad, want); err == nil {
				t.Fatal("invalid probe record accepted")
			}
		})
	}
}
