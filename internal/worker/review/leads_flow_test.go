package review

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// chainTree is n Go packages, each calling the next one's function below
// a long comment, so the smallest request budget reads them in slots and
// a slot's last call reaches code outside its request.
func chainTree(n int) map[string]string {
	tree := map[string]string{"go.mod": "module example.org/chain\n\ngo 1.23\n"}
	pad := strings.Repeat("// The chain steps down one package per call and adds its own index.\n", 40)
	for i := range n {
		body := fmt.Sprintf("package p%02d\n\n", i)
		if i < n-1 {
			body += fmt.Sprintf("import \"example.org/chain/p%02d\"\n\n", i+1)
		}
		body += pad
		if i < n-1 {
			body += fmt.Sprintf("\n// F%02d steps down the chain.\nfunc F%02d(n int) int {\n\tif n <= 0 {\n\t\treturn 0\n\t}\n\treturn p%02d.F%02d(n-1) + %d\n}\n", i, i, i+1, i+1, i)
		} else {
			body += fmt.Sprintf("\n// F%02d ends the chain; a negative input gives -1.\nfunc F%02d(n int) int {\n\tif n < 0 {\n\t\treturn -1\n\t}\n\treturn n\n}\n", i, i)
		}
		tree[fmt.Sprintf("p%02d/f.go", i)] = body
	}
	return tree
}

var chainCall = regexp.MustCompile(`return (p\d\d)\.(F\d\d)\(`)

// chainCite finds a sent line calling the next package, preferring one
// whose callee's file the request does not carry: its citation, its callee
// name, and false when the request holds none.
func chainCite(list []record.Excerpt) (string, string, bool) {
	sent := map[string]bool{}
	for _, e := range list {
		sent[path.Dir(e.Path)] = true
	}
	cite, callee := "", ""
	for _, e := range list {
		for i, line := range strings.Split(e.Text, "\n") {
			m := chainCall.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			n := e.Start + i
			if cite == "" || !sent[m[1]] {
				cite, callee = fmt.Sprintf("%s:%d-%d", e.ID, n, n), m[2]
			}
			if !sent[m[1]] {
				return cite, callee, true
			}
		}
	}
	return cite, callee, cite != ""
}

func chainCandidate(cite string) map[string]any {
	return map[string]any{"title": "다음 패키지의 음수 처리에 따라 결과가 달라질 수 있음", "condition": "n이 0보다 큰 호출", "mechanism": "인용한 줄이 다음 패키지 함수의 반환값을 그대로 더한다.",
		"impact": "끝 패키지가 음수를 돌려주면 합계가 줄어든다.", "improvement": "반환값의 범위를 확인한다.", "counterpoint": "다음 패키지가 음수를 돌려주지 않을 수 있다.", "evidence_ids": []string{cite}}
}

// leadsScript answers every role validly; the investigation cites a chain
// call and, when offered, names its definition as a lead of candidate 1;
// leads answers the follow-up (nil: the follow-up refines candidate 1
// with a resolved line). requests keeps every follow-up request.
type leadsScript struct {
	mu       sync.Mutex
	requests []opencode.Request
	asked    bool
	leads    func(n int, q opencode.Request) opencode.Reply
}

func (l *leadsScript) script(t *testing.T) map[string]func(int, opencode.Request) opencode.Reply {
	s := goBuildFilesScript(t)
	s["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		cite, callee, ok := chainCite(in.Scope.Excerpts)
		if !ok {
			return asJSON(map[string]any{"candidates": []map[string]any{chainCandidate(shippedExcerpt(in.Scope.Excerpts).ID)}})
		}
		out := map[string]any{"candidates": []map[string]any{chainCandidate(cite)}}
		if in.Scope.LeadLimit > 0 && l.asked {
			out["leads"] = []map[string]any{{"need": "definition", "evidence_id": cite, "symbol": callee, "suspicion": "다음 패키지 함수가 음수를 돌려주는 조건이 이 요청에 없다.", "candidate": 1}}
		}
		return asJSON(out)
	}
	s["leads"] = func(n int, q opencode.Request) opencode.Reply {
		l.mu.Lock()
		l.requests = append(l.requests, q)
		l.mu.Unlock()
		if l.leads != nil {
			return l.leads(n, q)
		}
		in := decodeAs[leadsInput](t, q)
		catalog := catalogOf(in.Scope.Excerpts)
		target := catalog[in.Leads[0].Resolved[0]]
		line := target.Start
		for i, x := range strings.Split(target.Text, "\n") {
			if strings.Contains(x, "if n") {
				line = target.Start + i
			}
		}
		refined := chainCandidate(in.Leads[0].EvidenceID)
		refined["condition"] = "n이 0보다 크고 다음 패키지가 0 이하를 받는 호출"
		refined["evidence_ids"] = []string{in.Leads[0].EvidenceID, fmt.Sprintf("%s:%d-%d", target.ID, line, line)}
		refined["basis"], refined["disposition"] = in.Candidates[0].ID, "refined"
		return asJSON(map[string]any{"results": []map[string]any{refined}})
	}
	return s
}

// bodyChainTree is chainTree with the long comment inside each function,
// so a request of units carries whole functions and the overview's last
// call reaches the next package's function, which it does not carry.
func bodyChainTree(n int) map[string]string {
	tree := map[string]string{"go.mod": "module example.org/chain\n\ngo 1.23\n"}
	pad := strings.Repeat("\t// The chain steps down one package per call and adds its own index.\n", 28)
	for i := range n {
		body := fmt.Sprintf("package p%02d\n\n", i)
		if i < n-1 {
			body += fmt.Sprintf("import \"example.org/chain/p%02d\"\n\n// F%02d steps down the chain.\nfunc F%02d(n int) int {\n%s\tif n <= 0 {\n\t\treturn 0\n\t}\n\treturn p%02d.F%02d(n-1) + %d\n}\n", i+1, i, i, pad, i+1, i+1, i)
		} else {
			body += fmt.Sprintf("// F%02d ends the chain; a negative input gives -1.\nfunc F%02d(n int) int {\n%s\tif n < 0 {\n\t\treturn -1\n\t}\n\treturn n\n}\n", i, i, pad)
		}
		tree[fmt.Sprintf("p%02d/f.go", i)] = body
	}
	return tree
}

func chainRun(t *testing.T, l *leadsScript, packages, maxCalls int) (analysis.Job, []analysis.TaskSpec, map[string]analysis.TaskResult) {
	t.Helper()
	return treeRun(t, l, chainTree(packages), maxCalls)
}

func treeRun(t *testing.T, l *leadsScript, tree map[string]string, maxCalls int) (analysis.Job, []analysis.TaskSpec, map[string]analysis.TaskResult) {
	t.Helper()
	j := treeJob(t, tree)
	f := &scriptedCaller{script: l.script(t)}
	plan, res := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: f, Budget: smallBudget, MaxCalls: maxCalls}, "1.27.4")
	return j, plan, res
}

// A slot's lead resolves to the next package's function outside its
// request; the slot's follow-up sends it with a platform evidence ID, its
// record keeps the sent and new excerpts and the refined candidate, and the
// slot's verify checks the refined candidate with the follow-up as upstream.
func TestLeadFollowupRefinesSlotCandidates(t *testing.T) {
	l := &leadsScript{asked: true}
	j, plan, res := chainRun(t, l, 12, 0)
	followed := 0
	for _, p := range plan {
		slot, ok := record.LeadsTask(p.Key)
		if !ok || slot == "" || res[p.Key].Status != domain.TaskSucceeded {
			continue
		}
		followed++
		inv, fu, ver := readRecord(t, j, "investigate."+slot), readRecord(t, j, "leads."+slot), readRecord(t, j, "verify."+slot)
		if inv.Schema != record.SchemaVersionLeads || len(inv.Leads) != 1 || !inv.Leads[0].Resolved() || inv.Leads[0].CandidateID != "investigate_1" {
			t.Fatalf("%s leads %+v", slot, inv.Leads)
		}
		target := inv.Leads[0].Targets[0]
		if slices.ContainsFunc(inv.Evidence, func(e record.Excerpt) bool { return e.ID == target.ID }) {
			t.Fatalf("%s target was already evidence of the investigation", slot)
		}
		f := fu.LeadFollowup
		if fu.Schema != record.SchemaVersionLeads || f == nil || !slices.Equal(f.New, []string{target.ID}) || f.NewLines != target.End-target.Start+1 ||
			len(f.Dispositions) != 1 || f.Dispositions[0].Disposition != record.DispositionRefined || f.Dispositions[0].Lead != "lead_1" ||
			!record.LeadFollowupMatches(fu, inv) {
			t.Fatalf("%s follow-up %+v", slot, f)
		}
		if !slices.ContainsFunc(ver.Upstream, func(u record.Upstream) bool {
			return u.Source == record.LeadsTaskKey(slot) && u.State == record.UpstreamAccepted
		}) || len(ver.Candidates) != 1 || ver.Candidates[0].Condition != fu.Candidates[0].Condition || ver.Candidates[0].Condition == inv.Candidates[0].Condition {
			t.Fatalf("%s verify %+v %+v", slot, ver.Upstream, ver.Candidates)
		}
		if !slices.ContainsFunc(ver.Evidence, func(e record.Excerpt) bool { return e.ID == target.ID }) {
			t.Fatalf("%s verify lacks the resolved excerpt", slot)
		}
	}
	if followed == 0 || len(l.requests) != followed {
		t.Fatalf("follow-ups %d, requests %d: %v", followed, len(l.requests), rows(plan, res))
	}
	for _, q := range l.requests {
		in := decodeAs[leadsInput](t, q)
		ids := map[string]bool{}
		for _, e := range in.Scope.Excerpts {
			ids[e.ID] = true
		}
		for _, x := range in.Leads {
			if !ids[x.EvidenceID] || len(x.Resolved) == 0 || !ids[x.Resolved[0]] {
				t.Fatalf("lead %+v outside the request's excerpts", x)
			}
		}
		if q.Role != record.StageLeads || len(opencode.Prompt(q)) > smallBudget.limits().request {
			t.Fatalf("follow-up request %s %d bytes", q.Role, len(opencode.Prompt(q)))
		}
	}
}

// The follow-up is planned for every investigation and makes no call when
// none asks: the same plan, statuses and calls of every other task, and
// each follow-up skipped with no_leads.
func TestLeadFollowupWithoutLeadsChangesNothing(t *testing.T) {
	for _, c := range []struct{ packages, ceiling int }{{12, record.WholeRunMaxCalls}, {12, record.WholeRunMaxCalls + 5}, {12, 0}, {16, record.WholeRunMaxCalls}} {
		t.Run(fmt.Sprint(c), func(t *testing.T) {
			_, plan, base := chainRun(t, &leadsScript{}, c.packages, c.ceiling)
			_, _, asked := chainRun(t, &leadsScript{asked: true}, c.packages, c.ceiling)
			for _, p := range plan {
				if _, ok := record.LeadsTask(p.Key); ok {
					if r := base[p.Key]; r.Status != domain.TaskSkippedPolicy || (r.Reason != record.ReasonNoLeads && r.Reason != domain.ReasonNoReviewScope && r.Reason != reasonDependency) {
						t.Fatalf("%s %+v", p.Key, r)
					}
					continue
				}
				// Asking for leads never takes a call another task would
				// have made: every other task ends the same way.
				if base[p.Key].Status != asked[p.Key].Status || base[p.Key].Reason != asked[p.Key].Reason {
					t.Fatalf("%s: %+v without leads, %+v with", p.Key, base[p.Key], asked[p.Key])
				}
			}
		})
	}
}

// The overview's follow-up counts the slots the scope will give lines (the
// same computation as review.scope) and runs at the default ceiling; its
// verify checks the refined candidates. At every ceiling the other tasks
// end as they do without leads: the follow-up never takes a slot's call.
func TestOverviewLeadFollowupKeepsTheSlots(t *testing.T) {
	for _, ceiling := range []int{0, 20, record.WholeRunMaxCalls} {
		t.Run(fmt.Sprint(ceiling), func(t *testing.T) {
			_, plan, base := treeRun(t, &leadsScript{}, bodyChainTree(8), ceiling)
			l := &leadsScript{asked: true}
			j, _, asked := treeRun(t, l, bodyChainTree(8), ceiling)
			for _, p := range plan {
				if _, ok := record.LeadsTask(p.Key); !ok && (base[p.Key].Status != asked[p.Key].Status || base[p.Key].Reason != asked[p.Key].Reason) {
					t.Fatalf("%s: %+v without leads, %+v with", p.Key, base[p.Key], asked[p.Key])
				}
			}
			slices := 0
			for _, p := range plan {
				if stage, _, ok := record.SliceTask(p.Key); ok && stage == record.StageInvestigate && asked[p.Key].Status == domain.TaskSucceeded {
					slices++
				}
			}
			r := asked[record.TaskLeads]
			if ceiling != 0 {
				if r.Status == domain.TaskSucceeded {
					t.Logf("ceiling %d: overview follow-up admitted with %d slices", ceiling, slices)
				}
				return
			}
			if r.Status != domain.TaskSucceeded || slices == 0 {
				t.Fatalf("overview follow-up %+v with %d slices: %v", r, slices, rows(plan, asked))
			}
			fu, ver := readRecord(t, j, record.StageLeads), readRecord(t, j, record.StageVerify)
			if !slicesContainUpstream(ver.Upstream, record.TaskLeads) || ver.Candidates[0].Condition != fu.Candidates[0].Condition {
				t.Fatalf("overview verify %+v %+v", ver.Upstream, ver.Candidates)
			}
		})
	}
}

func slicesContainUpstream(list []record.Upstream, source string) bool {
	return slices.ContainsFunc(list, func(u record.Upstream) bool { return u.Source == source && u.State == record.UpstreamAccepted })
}

// Under a tight ceiling the follow-ups are dropped first: a follow-up is
// refused (budget_exhausted, no call) while the slots after it still need
// their admission pair, and its verify checks the investigation's own
// candidates.
func TestLeadFollowupRefusedUnderTightCeiling(t *testing.T) {
	l := &leadsScript{asked: true}
	j, plan, res := chainRun(t, l, 16, record.WholeRunMaxCalls)
	refused := 0
	for _, p := range plan {
		slot, ok := record.LeadsTask(p.Key)
		if !ok || slot == "" || res[p.Key].Status != domain.TaskBudgetExhausted {
			continue
		}
		refused++
		if res[p.Key].Reason != reasonBudgetExhausted || slices.Contains(countedCalls(t, j), p.Key) {
			t.Fatalf("%s %+v", p.Key, res[p.Key])
		}
		if res["review.verify."+slot].Status == domain.TaskSucceeded {
			ver, inv := readRecord(t, j, "verify."+slot), readRecord(t, j, "investigate."+slot)
			if slices.ContainsFunc(ver.Upstream, func(u record.Upstream) bool { return u.Source == record.LeadsTaskKey(slot) }) ||
				ver.Candidates[0].Condition != inv.Candidates[0].Condition {
				t.Fatalf("%s verify after a refused follow-up %+v", slot, ver.Upstream)
			}
		}
	}
	if refused == 0 {
		t.Fatalf("no follow-up refused: %v", rows(plan, res))
	}
	if len(l.requests) != 0 {
		// A follow-up admitted at this ceiling keeps the later pairs' calls;
		// TestLeadFollowupWithoutLeadsChangesNothing checks they all ran.
		t.Logf("%d follow-ups admitted", len(l.requests))
	}
}

// A follow-up whose output stays invalid after its retry and repair fails
// with its reason and leaves the slot's verify with the investigation's
// candidates (never fewer).
func TestLeadFollowupOutputFailureKeepsCandidates(t *testing.T) {
	l := &leadsScript{asked: true, leads: func(n int, q opencode.Request) opencode.Reply {
		if strings.Contains(string(q.Input), `"repair"`) {
			return asJSON(map[string]any{"unexpected": true})
		}
		return opencode.Reply{Reason: "model_output_invalid"}
	}}
	j, plan, res := chainRun(t, l, 12, 0)
	failed := 0
	for _, p := range plan {
		slot, ok := record.LeadsTask(p.Key)
		if !ok || slot == "" || res[p.Key].Status != domain.TaskFailed {
			continue
		}
		failed++
		fu := readRecord(t, j, "leads."+slot)
		if res[p.Key].Reason != reasonOutputInvalid || fu.Accepted() || fu.LeadFollowup != nil || len(fu.Candidates) != 0 || len(fu.Calls) != record.LeadsMaxCalls {
			t.Fatalf("%s %+v %+v", p.Key, res[p.Key], fu)
		}
		ver, inv := readRecord(t, j, "verify."+slot), readRecord(t, j, "investigate."+slot)
		if !slices.ContainsFunc(ver.Upstream, func(u record.Upstream) bool {
			return u.Source == record.LeadsTaskKey(slot) && u.State == record.UpstreamFailed
		}) || len(ver.Candidates) != len(inv.Candidates) || ver.Candidates[0].Condition != inv.Candidates[0].Condition {
			t.Fatalf("%s verify %+v", slot, ver.Upstream)
		}
	}
	if failed == 0 {
		t.Fatalf("no follow-up failed: %v", rows(plan, res))
	}
}

// A retried attempt plans the same follow-up keys, between each
// investigation and its verify; the slot's follow-up shares its slot's
// schedule unit.
func TestLeadFollowupPlanIsStable(t *testing.T) {
	j := treeJob(t, chainTree(12))
	h := Handler{Static: vetStub{}, Budget: smallBudget}
	first, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := h.Plan(t.Context(), j)
	if !slices.EqualFunc(first, again, func(a, b analysis.TaskSpec) bool { return a == b }) {
		t.Fatal("plan changed between attempts")
	}
	var keys []string
	for _, p := range first {
		keys = append(keys, p.Key)
	}
	at := func(k string) int { return slices.Index(keys, k) }
	if at(record.TaskLeads) != at("review.interpret")+1 || at("review.verify") != at(record.TaskLeads)+1 {
		t.Fatalf("whole-run follow-up out of place: %v", keys)
	}
	slots := 0
	for _, k := range keys {
		if slot, ok := record.LeadsTask(k); ok && slot != "" {
			slots++
			inv, ver := slotTaskKeys(slot)
			if at(k) != at(inv)+1 || at(ver) != at(k)+1 {
				t.Fatalf("%s out of place: %v", k, keys)
			}
		}
	}
	if slots == 0 {
		t.Fatalf("no slot planned: %v", keys)
	}
	s := NewSchedule(keys)
	if s.ledger.unitOf["review.leads.s001"] != s.ledger.unitOf["review.investigate.s001"] || s.ledger.taskCap["review.leads.s001"] != record.LeadsMaxCalls {
		t.Fatal("the slot's follow-up is not in its slot's unit")
	}
}

// The overview follow-up computes the scope before review.scope writes it
// (leadsCallLimit). Both run scopeDoc on the same inputs: the snapshot, the
// accepted overview investigation, the configured ceiling (not the calls
// left) and the analyzer results. The follow-up's record and the overview's
// verify are not inputs, so a computation made after the run equals the
// stored artifact in everything but calls_before_slices, which counts the
// whole-run calls made by then.
func TestOverviewScopeComputedEarlyIsTheScope(t *testing.T) {
	j, _, res := treeRun(t, &leadsScript{asked: true}, bodyChainTree(8), 0)
	if res[record.TaskLeads].Status != domain.TaskSucceeded || res[record.TaskScope].Status != domain.TaskSucceeded {
		t.Fatalf("follow-up %+v scope %+v", res[record.TaskLeads], res[record.TaskScope])
	}
	s := &roleStage{h: Handler{Static: vetStub{}, Budget: smallBudget}, ctx: t.Context(), j: j, role: record.StageLeads}
	early, r := s.scopeDoc()
	if r != nil {
		t.Fatalf("scope computation %+v", r)
	}
	stored, err := record.DecodeScope(mustRead(t, scopePath(j)), scopeExpect(j))
	if err != nil {
		t.Fatal(err)
	}
	early.Budget.CallsBeforeSlices = stored.Budget.CallsBeforeSlices
	a, _ := json.Marshal(early)
	b, _ := json.Marshal(stored)
	if string(a) != string(b) {
		t.Fatalf("early scope\n%s\nstored scope\n%s", a, b)
	}
}
