package review

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
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
// with a resolved line). The first reply of every request of a role in
// repaired is not JSON, so each of those requests needs a second call. A
// role in retried takes all three calls of a request: its first reply lacks
// the role's list (a format repair), the repair's reply is not JSON (a
// retry), and the retry is answered.
// requests keeps every follow-up request.
type leadsScript struct {
	mu       sync.Mutex
	requests []opencode.Request
	asked    bool
	leads    func(n int, q opencode.Request) opencode.Reply
	repaired []string
	retried  []string
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
	for _, role := range l.repaired {
		answer := s[role]
		s[role] = func(n int, q opencode.Request) opencode.Reply {
			if !strings.Contains(string(q.Input), `"repair"`) {
				return opencode.Reply{Reason: "model_output_invalid"}
			}
			return answer(n, q)
		}
	}
	for _, role := range l.retried {
		answer := s[role]
		s[role] = func(n int, q opencode.Request) opencode.Reply {
			switch in := string(q.Input); {
			case !strings.Contains(in, `"repair"`):
				return asJSON(map[string]any{})
			case !strings.Contains(in, "output_not_json"):
				return opencode.Reply{Reason: "model_output_invalid"}
			}
			return answer(n, q)
		}
	}
	return s
}

// sameOtherTasks fails unless every task but the follow-ups ends the same
// way in both runs: asking for leads never takes a call another task would
// have made.
func sameOtherTasks(t *testing.T, plan []analysis.TaskSpec, base, asked map[string]analysis.TaskResult) {
	t.Helper()
	for _, p := range plan {
		if _, ok := record.LeadsTask(p.Key); !ok && (base[p.Key].Status != asked[p.Key].Status || base[p.Key].Reason != asked[p.Key].Reason) {
			t.Fatalf("%s: %+v without leads, %+v with: %v", p.Key, base[p.Key], asked[p.Key], rows(plan, asked))
		}
	}
}

// codeReviewStage is the code_review display stage of a run's results.
func codeReviewStage(plan []analysis.TaskSpec, res map[string]analysis.TaskResult) progress.State {
	var tasks []progress.Task
	for _, p := range plan {
		if p.Stage == domain.StageCodeReview {
			tasks = append(tasks, progress.Task{Stage: p.Stage, Status: res[p.Key].Status, Reason: res[p.Key].Reason})
		}
	}
	return progress.TaskStage(string(domain.StageCodeReview), tasks, true).State
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
// none asks: the same plan, statuses and calls of every other task. Each
// follow-up is skipped with no_leads when its request offered leads and
// leads_not_offered when it did not (the record's lead_limit tells), and
// with leads_slot_not_run when the ceiling refused its slot.
func TestLeadFollowupWithoutLeadsChangesNothing(t *testing.T) {
	for _, c := range []struct{ packages, ceiling int }{{12, record.WholeRunMaxCalls}, {12, record.WholeRunMaxCalls + 5}, {12, 0}, {16, record.WholeRunMaxCalls}} {
		t.Run(fmt.Sprint(c), func(t *testing.T) {
			j, plan, base := chainRun(t, &leadsScript{}, c.packages, c.ceiling)
			_, _, asked := chainRun(t, &leadsScript{asked: true}, c.packages, c.ceiling)
			reasons := map[string]int{}
			for _, p := range plan {
				slot, ok := record.LeadsTask(p.Key)
				if !ok {
					continue
				}
				r := base[p.Key]
				reasons[r.Reason]++
				switch {
				case r.Status != domain.TaskSkippedPolicy:
					t.Fatalf("%s %+v", p.Key, r)
				case r.Reason == record.ReasonNoLeads || r.Reason == record.ReasonLeadsNotOffered:
					inv := readRecord(t, j, strings.TrimPrefix(sliceTaskKey(record.StageInvestigate, slot), "review."))
					if offered := inv.LeadLimit == record.MaxLeads; offered != (r.Reason == record.ReasonNoLeads) || len(inv.Leads) != 0 {
						t.Fatalf("%s %+v with lead_limit %d", p.Key, r, inv.LeadLimit)
					}
				case r.Reason == record.ReasonLeadsSlotNotRun:
					if inv := base[sliceTaskKey(record.StageInvestigate, slot)]; slot == "" || inv.Status != domain.TaskBudgetExhausted {
						t.Fatalf("%s %+v after %+v", p.Key, r, inv)
					}
				case r.Reason != domain.ReasonNoReviewScope && r.Reason != reasonDependency:
					t.Fatalf("%s %+v", p.Key, r)
				}
			}
			if reasons[record.ReasonNoLeads] == 0 {
				t.Fatalf("no offered follow-up: %v", reasons)
			}
			sameOtherTasks(t, plan, base, asked)
		})
	}
}

// A follow-up is admitted only while the ceiling keeps, after both of its
// calls, every later task's call cap. With every verify and investigation
// taking all three calls of each request (a format repair, then a retry:
// the worst case the reserve is for), every other task ends as it does
// without leads at every ceiling from the smallest past the follow-up's
// admission; a reserve below the caps admits it where a later task then
// loses a call. With every verify's first reply not JSON, the same holds at
// ceilings 17 and 18.
func TestLeadFollowupLeavesTheLaterRepairs(t *testing.T) {
	worst := []int{}
	for ceiling := 15; ceiling <= 40; ceiling++ {
		worst = append(worst, ceiling)
	}
	var admitted atomic.Int32
	// The runs are independent (each its own snapshot and scratch); the
	// group returns when every parallel run has ended.
	t.Run("ceilings", func(t *testing.T) {
		for _, c := range []struct {
			repaired, retried []string
			ceilings          []int
		}{{repaired: []string{"verify"}, ceilings: []int{17, 18}}, {retried: []string{"investigate", "verify"}, ceilings: worst}} {
			for _, ceiling := range c.ceilings {
				t.Run(fmt.Sprint(c.repaired, c.retried, ceiling), func(t *testing.T) {
					t.Parallel()
					_, plan, base := chainRun(t, &leadsScript{repaired: c.repaired, retried: c.retried}, 12, ceiling)
					l := &leadsScript{asked: true, repaired: c.repaired, retried: c.retried}
					_, _, asked := chainRun(t, l, 12, ceiling)
					sameOtherTasks(t, plan, base, asked)
					if codeReviewStage(plan, base) != codeReviewStage(plan, asked) {
						t.Fatalf("stage %s without leads, %s with", codeReviewStage(plan, base), codeReviewStage(plan, asked))
					}
					for _, p := range plan {
						if _, ok := record.LeadsTask(p.Key); ok && c.retried != nil && asked[p.Key].Status == domain.TaskSucceeded {
							admitted.Add(1)
						}
					}
				})
			}
		}
	})
	if admitted.Load() == 0 {
		t.Fatal("no follow-up admitted up to ceiling 40: the sweep does not reach the admission")
	}
}

// The overview's follow-up counts the slots the scope will give lines (the
// same computation as review.scope) and runs at the default ceiling; its
// verify checks the refined candidates. At every ceiling, also when every
// verify or investigation needs a second call or all three, the other tasks
// end as they do without leads: the follow-up never takes a slot's call.
func TestOverviewLeadFollowupKeepsTheSlots(t *testing.T) {
	for _, c := range []struct {
		ceiling           int
		repaired, retried []string
	}{{0, nil, nil}, {20, nil, nil}, {record.WholeRunMaxCalls, nil, nil}, {0, []string{"verify"}, nil}, {20, []string{"verify"}, nil},
		{20, []string{"investigate"}, nil}, {30, []string{"investigate", "verify"}, nil},
		{0, nil, []string{"verify"}}, {20, nil, []string{"verify"}}, {25, nil, []string{"investigate"}}, {40, nil, []string{"investigate", "verify"}}} {
		ceiling := c.ceiling
		t.Run(fmt.Sprint(c), func(t *testing.T) {
			_, plan, base := treeRun(t, &leadsScript{repaired: c.repaired, retried: c.retried}, bodyChainTree(8), ceiling)
			l := &leadsScript{asked: true, repaired: c.repaired, retried: c.retried}
			j, _, asked := treeRun(t, l, bodyChainTree(8), ceiling)
			sameOtherTasks(t, plan, base, asked)
			slices := 0
			for _, p := range plan {
				if stage, _, ok := record.SliceTask(p.Key); ok && stage == record.StageInvestigate && asked[p.Key].Status == domain.TaskSucceeded {
					slices++
				}
			}
			r := asked[record.TaskLeads]
			if ceiling != 0 || c.repaired != nil || c.retried != nil {
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
// refused (skipped_policy, leads_not_admitted, no call) while the work after
// it still needs its calls, and its verify checks the investigation's own
// candidates. The refusal is not a failure of the review: the code review
// stage ends as it does without leads (chainTree(16) at ceilings 15 and 16).
func TestLeadFollowupRefusedUnderTightCeiling(t *testing.T) {
	for _, ceiling := range []int{record.WholeRunMaxCalls, record.WholeRunMaxCalls + 1} {
		_, plan, base := chainRun(t, &leadsScript{}, 16, ceiling)
		l := &leadsScript{asked: true}
		j, _, res := chainRun(t, l, 16, ceiling)
		if codeReviewStage(plan, base) != codeReviewStage(plan, res) {
			t.Fatalf("ceiling %d: stage %s without leads, %s with", ceiling, codeReviewStage(plan, base), codeReviewStage(plan, res))
		}
		refusedFollowups(t, j, plan, res, l)
	}
}

func refusedFollowups(t *testing.T, j analysis.Job, plan []analysis.TaskSpec, res map[string]analysis.TaskResult, l *leadsScript) {
	t.Helper()
	refused := 0
	for _, p := range plan {
		slot, ok := record.LeadsTask(p.Key)
		if !ok || slot == "" || res[p.Key].Reason != record.ReasonLeadsNotAdmitted {
			continue
		}
		refused++
		if res[p.Key].Status != domain.TaskSkippedPolicy || !domain.LeadsNotRun(res[p.Key].Reason) || slices.Contains(countedCalls(t, j), p.Key) {
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
	// The follow-up of a slot the ceiling refused says so, without a call.
	for _, p := range plan {
		if slot, ok := record.LeadsTask(p.Key); ok && slot != "" && res[sliceTaskKey(record.StageInvestigate, slot)].Status == domain.TaskBudgetExhausted &&
			(res[p.Key].Status != domain.TaskSkippedPolicy || res[p.Key].Reason != record.ReasonLeadsSlotNotRun) {
			t.Fatalf("%s after a refused slot %+v", p.Key, res[p.Key])
		}
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

// A follow-up record holds only what its request sent: a cited range lies
// in a sent excerpt (record.Decode), and the evidence is the
// investigation's entries, the resolved targets and ranges narrowed from
// sent excerpts (record.LeadFollowupMatches); an excerpt of another slot's
// file is neither.
func TestLeadFollowupRecordHoldsWhatItSent(t *testing.T) {
	j, plan, res := chainRun(t, &leadsScript{asked: true}, 12, 0)
	slot := ""
	var others []record.Excerpt
	for _, p := range plan {
		s, ok := record.LeadsTask(p.Key)
		if !ok || s == "" {
			continue
		}
		if slot == "" && res[p.Key].Status == domain.TaskSucceeded {
			slot = s
		} else if res[sliceTaskKey(record.StageInvestigate, s)].Status == domain.TaskSucceeded {
			others = append(others, readRecord(t, j, "investigate."+s).Evidence...)
		}
	}
	if slot == "" {
		t.Fatalf("no follow-up ran: %v", rows(plan, res))
	}
	name := "leads." + slot
	fu, inv := readRecord(t, j, name), readRecord(t, j, "investigate."+slot)
	if !record.LeadFollowupMatches(fu, inv) {
		t.Fatal("the follow-up does not match its investigation")
	}
	i := slices.IndexFunc(others, func(x record.Excerpt) bool {
		return x.End > x.Start && !slices.ContainsFunc(fu.Evidence, func(e record.Excerpt) bool { return e.Path == x.Path })
	})
	if i < 0 {
		t.Fatal("no excerpt of another file")
	}
	foreign := others[i]
	cited := fu
	cited.Evidence = append(slices.Clone(fu.Evidence), record.Narrow(foreign, j.Snapshot.Digest, foreign.Start, foreign.Start))
	b, err := json.Marshal(cited)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := record.Decode(b, expectRecord(j, name)); err == nil || !strings.Contains(err.Error(), "cited range outside the sent evidence") {
		t.Fatalf("a cited range of a file the follow-up never sent: %v", err)
	}
	whole := fu
	whole.Evidence = append(slices.Clone(fu.Evidence), foreign)
	if record.LeadFollowupMatches(whole, inv) {
		t.Fatal("an excerpt the follow-up never sent matches")
	}
}

// The lead contract travels only in an investigate request that offers
// leads (scope.lead_limit): a request that offers none carries the role's
// guide and contract alone, the same instruction text as before leads, and
// no guide file but the follow-up's speaks of leads.
func TestLeadContractOnlyWhereLeadsAreOffered(t *testing.T) {
	l := &leadsScript{asked: true}
	script := l.script(t)
	investigate := script["investigate"]
	var mu sync.Mutex
	offered, plain := 0, 0
	script["investigate"] = func(n int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		mu.Lock()
		defer mu.Unlock()
		lead := strings.Contains(q.Instructions, `Optionally return up to`)
		if (in.Scope.LeadLimit > 0) != lead || strings.Contains(q.Instructions, "단서") {
			t.Errorf("lead_limit %d, lead contract %v", in.Scope.LeadLimit, lead)
		}
		if lead {
			offered++
		} else {
			plain++
		}
		return investigate(n, q)
	}
	j := treeJob(t, chainTree(12))
	runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
	whole := treeJob(t, chainTree(2))
	runRoleWith(t, whole, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}, Budget: smallBudget}, "1.27.4")
	if offered == 0 || plain == 0 {
		t.Fatalf("%d offering requests, %d plain", offered, plain)
	}
}
