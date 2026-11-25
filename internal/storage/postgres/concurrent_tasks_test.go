package postgres

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// TC-308 (docs/REVIEW.md#호출-상한과-승인): the worker's concurrent review
// pairs post task status changes of one attempt at the same time, while the
// user cancels the run. Every writer locks project -> run -> attempt -> task
// rows (lockAttempt, authorizeRun, closeTasks after the run), so the changes
// serialize on the run row and none deadlocks: each change commits or is
// refused for a reason of its own (a task cannot start once the run is
// cancel requested), the cancel commits, and the attempt completes
// cancelled with every task closed. PostgreSQL's own deadlock counter for
// the database stays where it was. Skips (NOT executed) without
// CODEYE_TEST_DATABASE_URL.
func TestConcurrentReviewTaskStatusAndCancel(t *testing.T) {
	e := newStageEnv(t)
	n := e.enroll("a", e.root.ID)
	deadlocks := func() int64 {
		t.Helper()
		var d int64
		if _, err := e.s.pool.Exec(e.ctx, "SELECT pg_stat_clear_snapshot()"); err != nil {
			t.Fatal(err)
		}
		if err := e.s.pool.QueryRow(e.ctx, "SELECT deadlocks FROM pg_stat_database WHERE datname=current_database()").Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	before := deadlocks()
	const rounds, slices = 12, 8
	refused := 0
	for round := range rounds {
		e.idle(n)
		e.enqueue(fmt.Sprintf("concurrent-%d", round), deepReview)
		o, run := e.startRun(n, n.workers[0])
		var decls []taskDecl
		for i := 1; i <= slices; i++ {
			decls = append(decls, taskDecl{fmt.Sprintf("review.investigate.s%03d", i), "code_review"}, taskDecl{fmt.Sprintf("review.verify.s%03d", i), "code_review"})
		}
		tasks := e.plan(n, o, true, decls...)
		start := make(chan struct{})
		var wg sync.WaitGroup
		var mu sync.Mutex
		var failures []string
		for key, p := range tasks {
			wg.Go(func() {
				<-start
				v := p.version
				for _, st := range []string{"running", "succeeded"} {
					out, err := e.event(n, o, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: p.id, ExpectedVersion: v, Status: st})
					if err != nil {
						mu.Lock()
						defer mu.Unlock()
						// The only refusal of its own: starting a task of a
						// run that is cancel requested.
						if st == "running" && errors.Is(err, domain.ErrConflict) {
							refused++
							return
						}
						failures = append(failures, fmt.Sprintf("%s -> %s: %v", key, st, err))
						return
					}
					v = *out.Version
				}
			})
		}
		wg.Go(func() {
			<-start
			time.Sleep(time.Duration(round%4) * time.Millisecond)
			if _, _, err := e.s.CancelRun(e.ctx, e.user, run); err != nil {
				mu.Lock()
				failures = append(failures, fmt.Sprintf("cancel: %v", err))
				mu.Unlock()
			}
		})
		close(start)
		wg.Wait()
		if len(failures) != 0 {
			t.Fatalf("round %d: %v", round, failures)
		}
		if _, err := e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: "cancelled", Cleanup: "confirmed"}); err != nil {
			t.Fatalf("round %d: complete: %v", round, err)
		}
		if open := e.count("SELECT count(*) FROM tasks WHERE run_id=$1 AND status IN ('queued','blocked','running')", run); open != 0 {
			t.Fatalf("round %d: %d tasks left open", round, open)
		}
		if st := e.count("SELECT count(*) FROM runs WHERE id=$1 AND status='cancelled'", run); st != 1 {
			t.Fatalf("round %d: run not cancelled", round)
		}
	}
	// A backend reports a deadlock to the statistics when it goes idle; give
	// it the reporting interval.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if d := deadlocks(); d != before {
			t.Fatalf("PostgreSQL detected %d deadlocks", d-before)
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("%d rounds of %d concurrent review tasks with a cancel; %d starts refused after the cancel", rounds, 2*slices, refused)
}
