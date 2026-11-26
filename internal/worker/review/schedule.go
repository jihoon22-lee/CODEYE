package review

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Concurrent model calls of independent review pairs
// (docs/REVIEW.md#호출-상한과-승인).
//
// The whole-run roles and review.scope keep running one after another. After
// them the plan holds independent units: each module slice pair
// (review.investigate.sNNN, review.verify.sNNN), the contract follow-up and
// each diagnostic triage pair (review.triage.tNNN, review.verify.tNNN). No
// unit reads another unit's output except the contract follow-up, which
// reads the slice records and so starts only once every earlier unit has
// ended. The two tasks of a pair stay in order. Schedule.Run runs up to the
// operator's degree of parallelism of these units at once, taken in plan
// order.
//
// Every decision of the call ceiling (a pair's admission and each call it
// makes) is the decision sequential execution makes: it depends only on the
// calls of the units before it in plan order and its own. A unit decides at
// once when that holds whatever the unfinished earlier units still call
// (each is bounded by the calls its tasks may make, callUnit.cap); otherwise
// it waits until enough of them have ended. The earliest unfinished unit
// never waits, so the region always progresses, and no call is refused that
// sequential execution would have granted.

// Degree of parallelism of the review pairs (codeye agent --review-parallel,
// agent.model.review_parallel).
const (
	DefaultParallel = record.DefaultParallel
	MaxParallel     = record.MaxParallel
)

// ValidParallel reports an accepted degree of parallelism.
func ValidParallel(n int) bool { return record.ValidParallel(n) }

// errCallsCancelled: the task's context ended while it waited for a
// decision of the call ceiling.
var errCallsCancelled = errors.New("review_calls_cancelled")

// errCallCapExceeded: the task asked for more calls than its own bound
// (record.TaskMaxCalls). The schedule bounds an unfinished unit by these
// bounds, so a call past one is refused whatever the other units do.
var errCallCapExceeded = errors.New("review_call_cap_exceeded")

// callUnit is one independent unit of the plan: consecutive task indices.
type callUnit struct {
	tasks []int
	// cap bounds the calls the unit's tasks may make together.
	cap int
	// barrier: the unit reads the records of the units before it and
	// starts only once they have all ended (the contract follow-up).
	barrier bool
}

// Schedule is the region of a plan whose units may run concurrently and the
// attempt's call ledger over them.
type Schedule struct {
	start, end int
	units      []callUnit
	ledger     *callLedger
}

// NewSchedule finds the region in the attempt's plan keys: the module slice
// pairs, the contract follow-up and the triage pairs, contiguous from the
// first of them.
func NewSchedule(keys []string) *Schedule {
	s := &Schedule{start: len(keys), end: len(keys)}
	unitKey := func(k string) (string, int, bool) {
		if k == record.TaskContract {
			return k, record.TaskMaxCalls(k), true
		}
		if _, slot, ok := record.SliceTask(k); ok {
			return "s:" + slot, record.TaskMaxCalls(k), true
		}
		// A slot's lead follow-up belongs to its slot's unit.
		if slot, ok := record.LeadsTask(k); ok && slot != "" {
			return "s:" + slot, record.TaskMaxCalls(k), true
		}
		if _, slot, ok := record.TriageTask(k); ok {
			return "t:" + slot, record.TaskMaxCalls(k), true
		}
		return "", 0, false
	}
	l := &callLedger{unitOf: map[string]int{}, taskCap: map[string]int{}, changed: make(chan struct{})}
	last := ""
	for i, k := range keys {
		id, calls, ok := unitKey(k)
		if !ok {
			if len(s.units) > 0 {
				break
			}
			continue
		}
		if len(s.units) == 0 {
			s.start = i
		}
		if id != last || id == record.TaskContract {
			s.units = append(s.units, callUnit{barrier: id == record.TaskContract})
			l.caps = append(l.caps, 0)
			l.settled = append(l.settled, false)
		}
		u := len(s.units) - 1
		s.units[u].tasks = append(s.units[u].tasks, i)
		s.units[u].cap += calls
		l.caps[u] += calls
		l.unitOf[k] = u
		l.taskCap[k] = calls
		s.end, last = i+1, id
	}
	s.ledger = l
	return s
}

// Region is the [start, end) range of plan indices the units cover; start
// equals end when the plan has none.
func (s *Schedule) Region() (start, end int) { return s.start, s.end }

type scheduleKey struct{}

// WithSchedule makes the schedule's ledger decide the call ceiling for the
// review tasks run with the returned context. Without it every decision
// reads the attempt's count as a whole (sequential execution).
func WithSchedule(ctx context.Context, s *Schedule) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, scheduleKey{}, s.ledger)
}

func ledgerOf(ctx context.Context) *callLedger {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(scheduleKey{}).(*callLedger)
	return l
}

// Run executes the region's units, up to parallel at once, taking them in
// plan order. run executes the task at plan index i and reports whether it
// was started; a unit stops at its first task not started. Every unit is
// marked ended, run or not, so no later unit waits for it. Run returns once
// every unit has ended.
func (s *Schedule) Run(ctx context.Context, parallel int, run func(i int) bool) {
	parallel = max(1, min(parallel, len(s.units)))
	var mu sync.Mutex
	next := 0
	work := func() {
		for {
			mu.Lock()
			u := next
			next++
			mu.Unlock()
			if u >= len(s.units) {
				return
			}
			s.runUnit(ctx, u, run)
		}
	}
	if parallel <= 1 {
		work()
		return
	}
	var wg sync.WaitGroup
	for range parallel {
		wg.Go(work)
	}
	wg.Wait()
}

func (s *Schedule) runUnit(ctx context.Context, u int, run func(int) bool) {
	defer s.ledger.settle(u)
	if s.units[u].barrier && !s.ledger.awaitSettled(ctx, u) {
		// Cancelled while waiting: the unit's tasks stay not started.
		return
	}
	for _, i := range s.units[u].tasks {
		if !run(i) {
			return
		}
	}
}

// callLedger decides the call ceiling for the units of one attempt. The
// count file (one line per call, the task key, written before the call) is
// the record of the calls made; the ledger adds which units have ended.
type callLedger struct {
	mu     sync.Mutex
	unitOf map[string]int
	// taskCap is each unit task's own call bound; caps[u] is the sum over
	// the unit's tasks.
	taskCap map[string]int
	caps    []int
	settled []bool
	// changed is closed and replaced whenever a unit ends.
	changed chan struct{}
	// holdMu makes the triage share be decided once.
	holdMu sync.Mutex
	// countFault, set by tests, fails the count of a call of a task.
	countFault func(task string) bool
	// waits counts the decisions that had to wait for an earlier unit.
	waits int
}

func (l *callLedger) settle(u int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.settled[u] {
		return
	}
	l.settled[u] = true
	close(l.changed)
	l.changed = make(chan struct{})
}

// awaitSettled waits until every unit before u has ended; false when ctx
// ended first.
func (l *callLedger) awaitSettled(ctx context.Context, u int) bool {
	for {
		l.mu.Lock()
		done := true
		for i := range u {
			done = done && l.settled[i]
		}
		ch := l.changed
		l.mu.Unlock()
		if done {
			return true
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		}
	}
}

// below decides whether the calls before task's unit in plan order plus the
// unit's own are fewer than limit, and with count records one call of task
// when they are. It waits while the unfinished earlier units could still
// change the answer.
func (l *callLedger) below(ctx context.Context, j analysis.Job, task string, limit int, count bool) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		b, err := readBounded(callsPath(j), 64<<10)
		if errors.Is(err, fs.ErrNotExist) {
			b, err = nil, nil
		}
		if err != nil {
			return false, err
		}
		per := countsByTask(b)
		if count && per[task] >= l.capOf(task) {
			return false, errCallCapExceeded
		}
		low, high := l.span(per, task)
		switch {
		case high < limit:
			if count {
				return true, l.append(j, task)
			}
			return true, nil
		case low >= limit:
			return false, nil
		}
		ch := l.changed
		l.waits++
		l.mu.Unlock()
		select {
		case <-ch:
			l.mu.Lock()
		case <-ctx.Done():
			l.mu.Lock()
			return false, errCallsCancelled
		}
	}
}

// span is the least and the most the sequential count can be for task: the
// calls outside every unit (the whole-run roles), the task's unit's own, and
// each earlier unit's calls, at most its cap while it has not ended.
func (l *callLedger) span(per map[string]int, task string) (low, high int) {
	unit, inUnit := l.unitOf[task]
	byUnit := make([]int, len(l.caps))
	for k, n := range per {
		u, ok := l.unitOf[k]
		if !ok {
			low += n
			continue
		}
		byUnit[u] += n
	}
	if !inUnit {
		// A task outside the units runs before them: the count is exact.
		for _, n := range byUnit {
			low += n
		}
		return low, low
	}
	low += byUnit[unit]
	high = low
	for u := range unit {
		low += byUnit[u]
		if l.settled[u] {
			high += byUnit[u]
		} else {
			high += max(byUnit[u], l.caps[u])
		}
	}
	return low, high
}

// capOf is task's own call bound.
func (l *callLedger) capOf(task string) int {
	if n, ok := l.taskCap[task]; ok {
		return n
	}
	return record.TaskMaxCalls(task)
}

// countsByTask is the number of counted calls per task key.
func countsByTask(counts []byte) map[string]int {
	per := map[string]int{}
	for line := range strings.SplitSeq(string(counts), "\n") {
		if line != "" {
			per[line]++
		}
	}
	return per
}

func (l *callLedger) append(j analysis.Job, task string) error {
	if l.countFault != nil && l.countFault(task) {
		return errors.New("count fault")
	}
	return appendCount(j, task)
}

// appendCount writes one line, the task key, to the attempt's count file.
func appendCount(j analysis.Job, task string) error {
	f, err := os.OpenFile(callsPath(j), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(task + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// callsBelow is the one check of the call ceiling: whether the attempt's
// calls before this task in plan order are fewer than limit, recording one
// call of the task when count is set and they are. Without a schedule it
// reads the whole count, which is that number when tasks run one after
// another.
func (s *roleStage) callsBelow(limit int, count bool) (bool, error) {
	if l := ledgerOf(s.ctx); l != nil {
		return l.below(s.ctx, s.j, s.task, limit, count)
	}
	b, err := readBounded(callsPath(s.j), 64<<10)
	if errors.Is(err, fs.ErrNotExist) {
		b, err = nil, nil
	}
	if err != nil {
		return false, err
	}
	if count && countsByTask(b)[s.task] >= record.TaskMaxCalls(s.task) {
		return false, errCallCapExceeded
	}
	if bytes.Count(b, []byte("\n")) >= limit {
		return false, nil
	}
	if count {
		return true, appendCount(s.j, s.task)
	}
	return true, nil
}
