package review

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// pairsCaller answers from the request content only: the same request gets
// the same answer whatever runs beside it. Each answer comes after a delay
// drawn from the seed and the request digest, outside any lock, so the
// completion order changes with the seed and the degree of parallelism.
// fault, when it returns true, replaces the scripted answer.
type pairsCaller struct {
	script map[string]func(int, opencode.Request) opencode.Reply
	fault  func(q opencode.Request) (opencode.Reply, bool)
	// onCall runs before each answer with the number of calls so far.
	onCall func(n int)

	mu       sync.Mutex
	seed     uint64
	delay    time.Duration
	calls    int
	inFlight int
	peak     int
	digests  []string
}

func (c *pairsCaller) set(seed uint64, delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seed, c.delay, c.calls, c.inFlight, c.peak, c.digests = seed, delay, 0, 0, 0, nil
}

func (c *pairsCaller) Review(ctx context.Context, _ executor.Runner, q opencode.Request) opencode.Reply {
	digest := domain.Digest([]byte(opencode.Prompt(q)))
	c.mu.Lock()
	c.calls++
	n := c.calls
	c.inFlight++
	c.peak = max(c.peak, c.inFlight)
	c.digests = append(c.digests, digest)
	seed, delay, onCall := c.seed, c.delay, c.onCall
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()
	if onCall != nil {
		onCall(n)
	}
	reply := opencode.Reply{Model: "fake/scripted", Version: opencode.Version, RequestDigest: digest}
	if delay > 0 {
		h := fnv.New64a()
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], seed)
		h.Write(b[:])
		h.Write([]byte(digest))
		select {
		case <-time.After(time.Duration(h.Sum64() % uint64(delay))):
		case <-ctx.Done():
		}
	}
	if ctx.Err() != nil {
		reply.Reason = "cancelled"
		return reply
	}
	var r opencode.Reply
	if c.fault != nil {
		if f, ok := c.fault(q); ok {
			r = f
		}
	}
	if r.Output == nil && r.Reason == "" {
		fn := c.script[q.Role]
		if fn == nil {
			r = opencode.Reply{Reason: "model_unavailable"}
		} else {
			r = fn(1, q)
		}
	}
	r.Model, r.Version, r.RequestDigest = reply.Model, reply.Version, digest
	if r.Usage == nil {
		r.Usage = &opencode.Usage{Input: int64(len(q.Input)), Output: 50}
	}
	return r
}

// pairsFixture is a run whose tasks before the concurrent region have run
// once; every variant runs the region on a copy of that scratch.
type pairsFixture struct {
	j       analysis.Job
	h       analysis.Handler
	caller  *pairsCaller
	plan    []analysis.TaskSpec
	before  map[string]analysis.TaskResult
	start   int
	end     int
	ceiling int
}

// newPairsFixture is the admission tree (files of fns functions with
// diagnostics) with a README, so the plan has module slices, the contract
// follow-up and triage slots, at the smallest request budget.
func newPairsFixture(t *testing.T, files, fns, ceiling int) *pairsFixture {
	t.Helper()
	tree, names, diags := admissionTree(files, fns)
	tree["README.md"] = "# Triage\n\nThe functions return the larger of two values.\n"
	j := treeJob(t, tree)
	j.RunID, j.AttemptID = "0195f3a0-0000-7000-8000-000000000a01", "0195f3a0-0000-7000-8000-000000000a02"
	script := triageReviewScript(t)
	script["interpret"] = func(_ int, q opencode.Request) opencode.Reply {
		return asJSON(map[string]any{"hypotheses": []map[string]any{}, "limits": []map[string]any{{"observation_ids": []string{decodeAs[interpretInput](t, q).Observations[0].ID}, "statement": "도구 메시지만으로는 영향을 판단할 수 없다."}}})
	}
	caller := &pairsCaller{script: script}
	h, err := discovery.Wrap(Handler{Static: admissionStatic(t, names, diags), Caller: caller, MaxCalls: ceiling,
		Budget: Budget{RequestBytes: MinRequestBytes}}, discovery.Config{Observer: exampleObserver{t: t}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := h.Plan(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(plan))
	for i, p := range plan {
		keys[i] = p.Key
	}
	f := &pairsFixture{j: j, h: h, caller: caller, plan: plan, before: map[string]analysis.TaskResult{}, ceiling: ceiling}
	f.start, f.end = NewSchedule(keys).Region()
	if f.end != len(plan) || !slices.Contains(keys, record.TaskContract) || !slices.ContainsFunc(keys[f.start:], func(k string) bool { return strings.HasPrefix(k, "review.triage.") }) {
		t.Fatalf("region %d..%d of %v", f.start, f.end, keys)
	}
	ctx := WithAttemptSourceIndex(t.Context(), j)
	for _, task := range plan[:f.start] {
		f.before[task.Key] = h.Run(ctx, j, task, nil)
	}
	return f
}

func (f *pairsFixture) keys() []string {
	out := make([]string, len(f.plan))
	for i, p := range f.plan {
		out[i] = p.Key
	}
	return out
}

// pairsRun is what one execution of the region left: every task's result,
// the review record and scope bytes, the count file's lines (sorted: their
// order follows completion) and the report built from it all.
type pairsRun struct {
	results map[string]analysis.TaskResult
	files   map[string][]byte
	counts  []string
	report  []byte
	peak    int
	waits   int
}

// copyTree copies the files of src into a new directory.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// run executes the region with the given degree of parallelism (0: one
// after another without a schedule, the earlier sequential path), seed and
// largest answer delay. setup may adjust the schedule; ctx may be cancelled
// by the caller's onCall.
func (f *pairsFixture) run(t *testing.T, ctx context.Context, parallel int, seed uint64, delay time.Duration, setup func(*Schedule)) pairsRun {
	t.Helper()
	j := f.j
	j.Scratch = copyTree(t, f.j.Scratch)
	f.caller.set(seed, delay)
	results := map[string]analysis.TaskResult{}
	var mu sync.Mutex
	exec := func(ctx context.Context, i int) {
		task := f.plan[i]
		res := f.h.Run(ctx, j, task, nil)
		mu.Lock()
		results[task.Key] = res
		mu.Unlock()
	}
	base := WithAttemptSourceIndex(ctx, j)
	waits := 0
	if parallel == 0 {
		for i := f.start; i < f.end; i++ {
			if base.Err() != nil {
				break
			}
			exec(base, i)
		}
	} else {
		s := NewSchedule(f.keys())
		if setup != nil {
			setup(s)
		}
		sctx := WithSchedule(base, s)
		s.Run(sctx, parallel, func(i int) bool {
			if sctx.Err() != nil {
				return false
			}
			exec(sctx, i)
			return true
		})
		s.ledger.mu.Lock()
		waits = s.ledger.waits
		s.ledger.mu.Unlock()
	}
	out := pairsRun{results: results, files: map[string][]byte{}, waits: waits}
	f.caller.mu.Lock()
	out.peak = f.caller.peak
	f.caller.mu.Unlock()
	entries, err := os.ReadDir(j.Scratch)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "review.") || name == "review.calls" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(j.Scratch, name))
		if err != nil {
			t.Fatal(err)
		}
		out.files[name] = b
	}
	out.counts = countedCalls(t, j)
	slices.Sort(out.counts)
	if ctx.Err() == nil {
		all := map[string]analysis.TaskResult{}
		for k, v := range f.before {
			all[k] = v
		}
		for k, v := range results {
			all[k] = v
		}
		r, err := report.Build(exampleInput(t, j, f.h, f.plan, all, &exampleIDs{n: 0xa00}), report.DefaultRegistry())
		if err != nil {
			t.Fatal(err)
		}
		if err := report.ValidateReport(r); err != nil {
			t.Fatal(err)
		}
		if out.report, err = report.Marshal(r); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// same fails unless b is a with the same results, records, scope, counted
// calls and report bytes.
func samePairsRun(t *testing.T, label string, a, b pairsRun) {
	t.Helper()
	for k, ra := range a.results {
		rb, ok := b.results[k]
		if !ok || ra.Status != rb.Status || ra.Reason != rb.Reason || !slices.Equal(ra.Limitations, rb.Limitations) || len(ra.Artifacts) != len(rb.Artifacts) {
			t.Fatalf("%s: %s %+v, want %+v", label, k, rb, ra)
		}
	}
	if len(a.results) != len(b.results) {
		t.Fatalf("%s: %d results, want %d", label, len(b.results), len(a.results))
	}
	for name, ba := range a.files {
		if bb, ok := b.files[name]; !ok || string(ba) != string(bb) {
			t.Fatalf("%s: %s differs", label, name)
		}
	}
	if len(a.files) != len(b.files) {
		t.Fatalf("%s: files %d, want %d", label, len(b.files), len(a.files))
	}
	if !slices.Equal(a.counts, b.counts) {
		t.Fatalf("%s: counted calls differ:\n%v\n%v", label, b.counts, a.counts)
	}
	if string(a.report) != string(b.report) {
		t.Fatalf("%s: report differs", label)
	}
}

// statusOf counts the region results by status.
func statusOf(r pairsRun) map[domain.TaskStatus]int {
	out := map[domain.TaskStatus]int{}
	for _, res := range r.results {
		out[res.Status]++
	}
	return out
}

// The harness matrix: degrees of parallelism × seeds. Under -race each run
// is several times slower, so the race build checks the shared state with
// three degrees and one seed and the plain build runs the whole matrix.
var pairsParallel, pairsSeeds = func() ([]int, int) {
	if raceDetector {
		return []int{1, 4, 8}, 1
	}
	return []int{1, 2, 4, 8}, 3
}()

// checkPairs runs the region of f one after another without a schedule
// (the earlier execution) and then at every degree of parallelism with
// several seeds, and requires the same results, records, counted calls and
// report each time, within the ceiling.
func checkPairs(t *testing.T, f *pairsFixture, setup func(*Schedule)) (ref pairsRun, waited int) {
	t.Helper()
	ref = f.run(t, t.Context(), 0, 0, 0, nil)
	if len(ref.counts) > f.ceiling {
		t.Fatalf("reference made %d calls over the ceiling %d", len(ref.counts), f.ceiling)
	}
	if setup != nil {
		// The fault seam needs a schedule; compare to one at parallelism 1.
		ref = f.run(t, t.Context(), 1, 0, 0, setup)
	}
	peak := 0
	for _, p := range pairsParallel {
		for seed := range uint64(pairsSeeds) {
			got := f.run(t, t.Context(), p, seed+1, 2*time.Millisecond, setup)
			samePairsRun(t, fmt.Sprintf("parallel %d seed %d", p, seed+1), ref, got)
			if p == 1 && got.peak != 1 {
				t.Fatalf("parallel 1 had %d requests in flight", got.peak)
			}
			peak = max(peak, got.peak)
			if p > 1 {
				waited += got.waits
			}
		}
	}
	t.Logf("region calls %d, statuses %v, most requests in flight %d, decisions that waited %d", len(ref.counts)-len(countedCallsBefore(t, f)), statusOf(ref), peak, waited)
	return ref, waited
}

// countedCallsBefore are the calls the tasks before the region made.
func countedCallsBefore(t *testing.T, f *pairsFixture) []string {
	t.Helper()
	return countedCalls(t, f.j)
}

// TC-307: plenty of calls: every slice fits, the triage takes what the
// slices leave and runs beside them; the result does not depend on the
// degree of parallelism or the order answers arrive in.
func TestPairsPlentyOfCalls(t *testing.T) {
	f := newPairsFixture(t, 8, 40, 99)
	ref, _ := checkPairs(t, f, nil)
	if n := statusOf(ref)[domain.TaskBudgetExhausted]; n != 0 {
		t.Fatalf("%d tasks budget_exhausted with plenty of calls", n)
	}
}

// TC-307: the ceiling is reached mid-way: some slices are admitted and the
// rest refused, the triage holds its share; the same slots are admitted at
// every degree of parallelism.
func TestPairsCeilingReachedMidway(t *testing.T) {
	f := newPairsFixture(t, 30, 40, 20)
	ref, waited := checkPairs(t, f, nil)
	if waited == 0 {
		t.Fatal("no decision waited for an earlier pair at the ceiling")
	}
	if hold := ref.files["review.triage.hold"]; string(hold) != "2" {
		t.Fatalf("triage hold %q, want one pair", hold)
	}
	st := statusOf(ref)
	if st[domain.TaskBudgetExhausted] == 0 || st[domain.TaskSucceeded] == 0 {
		t.Fatalf("statuses %v, want admitted and refused slots", st)
	}
}

// repairFault makes the first answer of every other request (by digest) a
// reply that is not JSON and the answer asked again a rejected output, so
// pairs spend their repair calls and compete for the last calls.
func repairFault(q opencode.Request) (opencode.Reply, bool) {
	var in struct {
		Repair *struct {
			OutputErrors []string `json:"output_errors"`
		} `json:"repair"`
	}
	if json.Unmarshal(q.Input, &in) != nil || q.Role == "triage" {
		return opencode.Reply{}, false
	}
	h := fnv.New32a()
	h.Write(q.Input)
	switch {
	case in.Repair == nil && h.Sum32()%2 == 0:
		return opencode.Reply{Reason: "model_output_invalid"}, true
	case in.Repair != nil && slices.Equal(in.Repair.OutputErrors, []string{"output_not_json"}) && h.Sum32()%3 == 0:
		return asJSON(map[string]any{"unexpected": true}), true
	}
	return opencode.Reply{}, false
}

// TC-307: format repairs and retries compete for the last calls near the
// ceiling; the calls a pair is granted are those sequential execution
// grants.
func TestPairsRepairsCompeteNearTheCeiling(t *testing.T) {
	f := newPairsFixture(t, 30, 40, 40)
	f.caller.fault = repairFault
	ref, waited := checkPairs(t, f, nil)
	if st := statusOf(ref); st[domain.TaskBudgetExhausted] == 0 || waited == 0 {
		t.Fatalf("statuses %v, %d decisions waited: want the ceiling reached", st, waited)
	}
}

// TC-307: one pair times out; the others do not stop and the result is the
// same at every degree of parallelism.
func TestPairsOnePairTimesOut(t *testing.T) {
	f := newPairsFixture(t, 8, 40, 99)
	// The investigate of the first filled slot (the request that carries
	// the slot's last file) times out.
	doc := scopeOf(t, f.j)
	first := doc.Slices[0]
	path := first.Files[len(first.Files)-1].Path
	f.caller.fault = func(q opencode.Request) (opencode.Reply, bool) {
		if q.Role == "investigate" && strings.Contains(string(q.Input), `"path":"`+path+`"`) {
			return opencode.Reply{Reason: "model_timeout"}, true
		}
		return opencode.Reply{}, false
	}
	ref, _ := checkPairs(t, f, nil)
	st := statusOf(ref)
	if ref.results["review.investigate."+first.Key].Status != domain.TaskTimedOut || ref.results["review.verify."+first.Key].Status == domain.TaskSucceeded ||
		st[domain.TaskSucceeded] < 4 {
		t.Fatalf("statuses %v, want the slot timed out and the rest done", st)
	}
}

// scopeOf is the scope artifact review.scope wrote.
func scopeOf(t *testing.T, j analysis.Job) record.ScopeDoc {
	t.Helper()
	b, err := os.ReadFile(scopePath(j))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := record.DecodeScope(b, scopeExpect(j))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// TC-307: a count that cannot be written fails that call alone
// (review_call_count_failed); the other pairs go on.
func TestPairsCountWriteFailure(t *testing.T) {
	f := newPairsFixture(t, 8, 40, 99)
	slot := scopeOf(t, f.j).Slices[1].Key
	task := "review.verify." + slot
	fault := func(s *Schedule) { s.ledger.countFault = func(k string) bool { return k == task } }
	ref, _ := checkPairs(t, f, fault)
	if r := ref.results[task]; r.Status != domain.TaskFailed || r.Reason != reasonCallCountFailed {
		t.Fatalf("%s %+v", task, r)
	}
	if slices.Contains(ref.counts, task) {
		t.Fatal("an uncounted call was counted")
	}
	if r := ref.results["review.investigate."+slot]; r.Status != domain.TaskSucceeded {
		t.Fatalf("investigate %+v", r)
	}
}

// TC-307: cancelling the attempt while requests are in flight ends every
// pair: Run returns, no task succeeds after the cancel, every waiting pair
// is released, the ceiling holds and no goroutine is left behind.
func TestPairsCancelledMidFlight(t *testing.T) {
	f := newPairsFixture(t, 30, 40, 60)
	for _, p := range pairsParallel {
		t.Run(fmt.Sprint(p), func(t *testing.T) {
			before := runtime.NumGoroutine()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f.caller.onCall = func(n int) {
				if n == 6 {
					cancel()
				}
			}
			defer func() { f.caller.onCall = nil }()
			done := make(chan pairsRun, 1)
			go func() { done <- f.run(t, ctx, p, 7, 5*time.Millisecond, nil) }()
			var got pairsRun
			select {
			case got = <-done:
			case <-time.After(60 * time.Second):
				t.Fatal("the region did not end after the cancel")
			}
			if total := len(got.counts); total > f.ceiling {
				t.Fatalf("%d calls over the ceiling %d", total, f.ceiling)
			}
			if region := len(got.counts) - len(countedCallsBefore(t, f)); region > 6+p {
				t.Fatalf("%d region calls after cancelling at the sixth (parallelism %d)", region, p)
			}
			// The tasks in flight end cancelled; the rest were never
			// started (no result).
			st := statusOf(got)
			if st[domain.TaskCancelled] == 0 || st[domain.TaskFailed] != 0 || len(got.results) == f.end-f.start {
				t.Fatalf("statuses %v of %d tasks run, %d planned", st, len(got.results), f.end-f.start)
			}
			deadline := time.Now().Add(5 * time.Second)
			for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := runtime.NumGoroutine(); n > before+2 {
				t.Fatalf("goroutines %d after the region, %d before", n, before)
			}
		})
	}
}

// TC-307: a pair waits only while the earlier unfinished pairs could change
// its decision. With every earlier pair's worst case inside the ceiling it
// decides at once; at the ceiling it waits for them and decides as
// sequential execution does.
func TestLedgerDecidesInPlanOrder(t *testing.T) {
	keys := []string{"review.environment", "review.scope", "review.investigate.s001", "review.verify.s001", "review.investigate.s002", "review.verify.s002",
		record.TaskContract, "review.triage.t001", "review.verify.t001", "other.task"}
	s := NewSchedule(keys)
	if start, end := s.Region(); start != 2 || end != 9 || len(s.units) != 4 || !s.units[2].barrier || s.units[0].cap != 6 || s.units[2].cap != 2 {
		t.Fatalf("region %d..%d units %+v", start, end, s.units)
	}
	j := analysis.Job{Scratch: t.TempDir()}
	for range 3 {
		if err := appendCount(j, "review.environment"); err != nil {
			t.Fatal(err)
		}
	}
	ctx := t.Context()
	l := s.ledger
	// s002 at limit 3+6+1: at most 6 calls of s001 still come.
	if ok, err := l.below(ctx, j, "review.investigate.s002", 10, true); !ok || err != nil {
		t.Fatalf("below: %v %v", ok, err)
	}
	// The count already reaches the limit whatever s001 does: refused at once.
	if ok, err := l.below(ctx, j, "review.investigate.s002", 4, false); ok || err != nil {
		t.Fatalf("below: %v %v", ok, err)
	}
	// Undecided until s001 ends: it waits, then counts s001's actual calls.
	got := make(chan bool, 1)
	go func() {
		ok, err := l.below(ctx, j, "review.investigate.s002", 7, false)
		if err != nil {
			t.Error(err)
		}
		got <- ok
	}()
	select {
	case <-got:
		t.Fatal("decided before s001 ended")
	case <-time.After(50 * time.Millisecond):
	}
	if err := appendCount(j, "review.investigate.s001"); err != nil {
		t.Fatal(err)
	}
	l.settle(0)
	if ok := <-got; !ok {
		t.Fatal("refused: 3 whole-run, 1 of s001 and 1 of s002 are below 7")
	}
	// A later pair's calls never count for an earlier one.
	for range 20 {
		if err := appendCount(j, "review.triage.t001"); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := l.below(ctx, j, "review.verify.s002", 7, false); !ok || err != nil {
		t.Fatalf("below: %v %v", ok, err)
	}
	// A cancelled wait ends with errCallsCancelled.
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	// The contract follow-up at limit 7: 4 counted before it, s002 may
	// still make 6.
	if _, err := l.below(cctx, j, record.TaskContract, 7, false); !errors.Is(err, errCallsCancelled) {
		t.Fatalf("cancelled wait: %v", err)
	}
	// A count file that cannot be read fails the decision.
	if err := os.Remove(callsPath(j)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(callsPath(j), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := l.below(ctx, j, "review.investigate.s002", 100, true); err == nil {
		t.Fatal("unreadable count accepted")
	}
}
