package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// TC-246 over HTTP: the confirmation must be stated in the body; only a
// recovery_required run is accepted; the answer names the run that runs
// again and the attempt whose cleanup was confirmed.
func TestRecoverRunNeedsTheStatedConfirmation(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	u := f.user
	queued := e.createRun(u, f.projectID)
	confirm := map[string]bool{"cleanup_confirmed": true}
	e.do("POST", "/api/v1/runs/"+queued+"/recover", confirm, u.o()).want(t, 409, "RUN_NOT_RECOVERY_REQUIRED")

	ctx := context.Background()
	a, err := e.store.BeginAttempt(ctx, queued, domain.NewID(), domain.NewID(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE attempts SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ExpireAttempt(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/v1/runs/"+queued+"/recover", nil, u.o()).want(t, 415, "UNSUPPORTED_MEDIA_TYPE")
	for _, body := range []any{map[string]bool{"cleanup_confirmed": false}, map[string]any{}, map[string]any{"cleanup_confirmed": true, "note": "x"}} {
		e.do("POST", "/api/v1/runs/"+queued+"/recover", body, u.o()).want(t, 400, "INVALID_REQUEST")
	}
	if got := e.do("GET", "/api/v1/runs/"+queued, nil, u.o()).want(t, 200, ""); got.body["status"] != "recovery_required" {
		t.Fatalf("refused bodies changed the run: %v", got.body["status"])
	}
	r := e.do("POST", "/api/v1/runs/"+queued+"/recover", confirm, u.o()).want(t, 202, "")
	if r.body["retry"] != "new_run" || r.body["id"] == queued || r.body["status"] != "queued" || r.body["attempt_id"] != a.ID || r.body["epoch"] != float64(a.Epoch) {
		t.Fatalf("answer %s", r.raw)
	}
	if r.header.Get("Location") != "/api/v1/runs/"+r.body["id"].(string) {
		t.Fatal(r.header.Get("Location"))
	}
	if got := e.do("GET", "/api/v1/runs/"+queued, nil, u.o()).want(t, 200, ""); got.body["status"] != "failed" {
		t.Fatalf("confirmed run %v", got.body["status"])
	}
	e.do("POST", "/api/v1/runs/"+queued+"/recover", confirm, u.o()).want(t, 409, "RUN_NOT_RECOVERY_REQUIRED")
}
