//go:build linux

package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// overlapCaller answers as sliceScripted. With gate, the first module
// slice investigation (the second investigate request) waits until another
// slice request is in flight: at a degree of parallelism above one two
// pairs then provably run at once. It records the most requests in flight.
type overlapCaller struct {
	inner    sliceScripted
	gate     bool
	mu       sync.Mutex
	inFlight int
	peak     int
	seen     int
	other    chan struct{}
}

func (c *overlapCaller) Review(ctx context.Context, r executor.Runner, q opencode.Request) opencode.Reply {
	c.mu.Lock()
	c.inFlight++
	c.peak = max(c.peak, c.inFlight)
	if q.Role == "investigate" {
		c.seen++
	}
	first := q.Role == "investigate" && c.seen == 2
	if c.gate && c.seen > 2 && c.other != nil {
		close(c.other)
		c.other = nil
	}
	wait := c.other
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()
	if c.gate && first && wait != nil {
		select {
		case <-wait:
		case <-time.After(30 * time.Second):
			return opencode.Reply{Reason: "model_unavailable"}
		}
	}
	return c.inner.Review(ctx, r, q)
}

// TC-307 (docs/REVIEW.md#호출-상한과-승인): through the real worker, the
// module slice pairs run concurrently at the hello's degree of parallelism
// and every task ends as it does one at a time.
func TestReviewSlicesRunConcurrentlyThroughTheWorker(t *testing.T) {
	rows := map[int][]string{}
	for _, parallel := range []int{1, 4} {
		caller := &overlapCaller{gate: parallel > 1, other: make(chan struct{})}
		c, got, fa, m := runSlicesAt(t, caller, 33, parallel, nil)
		if c.Outcome != "succeeded" || c.CleanupState != "confirmed" {
			t.Fatalf("parallel %d: completion %+v rows %v", parallel, c, got)
		}
		caller.mu.Lock()
		caller.inner.mu.Lock()
		peak, calls := caller.peak, len(caller.inner.roles)
		caller.inner.mu.Unlock()
		caller.mu.Unlock()
		if (parallel == 1 && peak != 1) || (parallel > 1 && peak < 2) || calls > 33 {
			t.Fatalf("parallel %d: %d requests in flight at most, %d calls", parallel, peak, calls)
		}
		r, err := report.Build(reportInput(t, fa, m, *c.ResultManifestClientArtifact), report.DefaultRegistry())
		if err != nil {
			t.Fatal(err)
		}
		if err := report.ValidateReport(r); err != nil {
			t.Fatal(err)
		}
		if r.Coverage.AIReview.Status != report.AIReviewPerformed || r.CodeReview.Ledger == nil {
			t.Fatalf("parallel %d: ai review %s ledger %v", parallel, r.Coverage.AIReview.Status, r.CodeReview.Ledger)
		}
		rows[parallel] = got
		t.Logf("parallel %d: %d calls, %d at most in flight", parallel, calls, peak)
	}
	if strings.Join(rows[1], " ") != strings.Join(rows[4], " ") {
		t.Fatalf("task results differ:\n%v\n%v", rows[1], rows[4])
	}
}

// slowOpenCode is a stand-in OpenCode: it answers --version and keeps a
// model request running in a child process until it is stopped.
const slowOpenCode = "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 1.19.5; exit 0; fi\n/bin/sleep 120 &\nwait\n"

// inFlightCaller answers the whole-run roles as sliceScripted and sends each
// module slice investigation to a stand-in OpenCode process that does not
// answer.
type inFlightCaller struct {
	inner sliceScripted
	model opencode.Config
	mu    sync.Mutex
	seen  int
}

func (c *inFlightCaller) Review(ctx context.Context, r executor.Runner, q opencode.Request) opencode.Reply {
	c.mu.Lock()
	if q.Role == "investigate" {
		c.seen++
	}
	slice := q.Role == "investigate" && c.seen > 1
	c.mu.Unlock()
	if slice {
		return c.model.Review(ctx, r, q)
	}
	return c.inner.Review(ctx, r, q)
}

// TC-307: cancelling the attempt while several pairs wait on the model stops
// every in-flight request: each request's process group is gone, the
// attempt ends cancelled with cleanup confirmed and no call over the
// ceiling.
func TestReviewSlicesCancelledInFlightThroughTheWorker(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "opencode")
	if err := os.WriteFile(exe, []byte(slowOpenCode), 0o700); err != nil {
		t.Fatal(err)
	}
	caller := &inFlightCaller{model: opencode.Config{Executable: exe, Model: "opencode/fixture", Timeout: 2 * time.Minute}}
	var mu sync.Mutex
	groups := map[int]bool{}
	c, rows, _, _ := runSlicesAt(t, caller, 33, 4, func(fa *fakeAgent) {
		perTask := map[string]int{}
		running := 0
		deadline := time.After(60 * time.Second)
		for running < 2 {
			select {
			case p := <-fa.started:
				mu.Lock()
				groups[p.PGID] = true
				mu.Unlock()
				// A slice task's version probe, then its model request.
				if !strings.HasPrefix(p.TaskKey, "review.investigate.s") {
					continue
				}
				if perTask[p.TaskKey]++; perTask[p.TaskKey] == 2 {
					running++
				}
			case <-deadline:
				t.Fatalf("two slice requests not in flight; messages %v", fa.kinds())
			}
		}
		go func() {
			for p := range fa.started {
				mu.Lock()
				groups[p.PGID] = true
				mu.Unlock()
			}
		}()
		if err := fa.conn.Send(&workerproto.Cancel{Reason: workerproto.CancelRunRequested}); err != nil {
			t.Fatal(err)
		}
	})
	if c.Outcome != "cancelled" || c.CleanupState != "confirmed" {
		t.Fatalf("completion %+v rows %v", c, rows)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(groups) < 4 {
		t.Fatalf("process groups %v", groups)
	}
	for pgid := range groups {
		if groupAlive(pgid) {
			t.Fatalf("process group %d still has processes", pgid)
		}
	}
	caller.mu.Lock()
	caller.inner.mu.Lock()
	defer caller.inner.mu.Unlock()
	defer caller.mu.Unlock()
	if calls := caller.seen - 1 + len(caller.inner.roles); calls > 33 {
		t.Fatalf("%d calls over the ceiling", calls)
	}
	t.Logf("%d process groups, %d slice investigations in flight", len(groups), caller.seen-1)
}
