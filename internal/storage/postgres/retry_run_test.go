package postgres

import (
	"encoding/json"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// RetryRun (POST /runs/{id}/retry): a finished run without a snapshot a node
// still holds gets a new run with the same request; repeating the call
// returns that run; an unfinished run is refused; a viewer may not retry.
func TestRetryRunQueuesANewRunWhenThereIsNoHeldSnapshot(t *testing.T) {
	s := testStore(t)
	ctx, actor, p, r := seed(t, s)
	_, _, err := s.RetryRun(ctx, actor, r.ID)
	expect(t, err, domain.ErrConflict)
	cancelled, err := s.Cancel(ctx, actor, r.ID)
	if err != nil || cancelled.Status != domain.RunCancelled {
		t.Fatal(cancelled, err)
	}
	next, how, err := s.RetryRun(ctx, actor, r.ID)
	if err != nil || how != RetryNewRun || next.ID == r.ID || next.Status != domain.RunQueued || next.ProjectID != p.ID {
		t.Fatal(next, how, err)
	}
	again, how, err := s.RetryRun(ctx, actor, r.ID)
	if err != nil || how != RetryNewRun || again.ID != next.ID {
		t.Fatalf("repeat gave another run: %v %v %v", again, how, err)
	}
	var request json.RawMessage
	if err := s.pool.QueryRow(ctx, "SELECT request FROM runs WHERE id=$1", next.ID).Scan(&request); err != nil || string(request) != `{"profile": "deep-review"}` {
		t.Fatalf("request %s %v", request, err)
	}
	// A failed run whose snapshot no node holds acquires anew.
	o := claim(t, s, next)
	snapshot(t, s, o)
	failed, err := s.FinishAttempt(ctx, o, "failed", "confirmed")
	if err != nil || failed.Status != domain.RunFailed || failed.SnapshotID == nil {
		t.Fatal(failed, err)
	}
	third, how, err := s.RetryRun(ctx, actor, next.ID)
	if err != nil || how != RetryNewRun || third.ID == next.ID || third.SnapshotID != nil {
		t.Fatal(third, how, err)
	}
	stranger, err := s.CreateUser(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.RetryRun(ctx, stranger, next.ID)
	expect(t, err, domain.ErrNotFound)
}
