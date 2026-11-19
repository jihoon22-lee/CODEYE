package postgres

import (
	"context"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// Scheduler and autoscaler storage tests on real PostgreSQL (isolated
// schema). Node agents are simulated by heartbeats here; the real agent and
// worker processes are exercised by scripts/scheduler_smoke.py.

func intp(v int) *int { return &v }

// policy sets the default pool policy.
func (e *nodeEnv) policy(u PoolPolicyUpdate) PoolPolicy {
	e.t.Helper()
	if u.Name == "" {
		u.Name = "default"
	}
	p, _, err := e.s.SetPoolPolicy(e.ctx, u)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

// ctlBeat sends a controller-scaled heartbeat with the given worker states.
func (e *nodeEnv) ctlBeat(n testNode, maxWorkers int, cursor int64, caps []string, workers map[string]string) nodeproto.NodeHeartbeatResponse {
	e.t.Helper()
	in := hb(nil, "", caps)
	in.WorkerScaling, in.MaxWorkers, in.LastCommandSequence = nodeproto.ScalingController, maxWorkers, cursor
	ids := make([]string, 0, len(workers))
	for id := range workers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		in.ObservedWorkers = append(in.ObservedWorkers, nodeproto.ObservedWorker{ID: id, State: workers[id]})
	}
	return e.beat(n, in)
}

func (e *nodeEnv) reconcile() PoolReconcile {
	e.t.Helper()
	return reconcileWith(e.t, e.s)
}

func reconcileWith(t *testing.T, s *Store) PoolReconcile {
	t.Helper()
	rs, err := s.ReconcilePools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("pools %d", len(rs))
	}
	return rs[0]
}

func kinds(as []ScaleAction) map[string][]string {
	out := map[string][]string{}
	for _, a := range as {
		out[a.Kind] = append(out[a.Kind], a.WorkerID)
	}
	return out
}

// finish completes an attempt as failed with confirmed cleanup (releases its
// reservation) and reports its worker idle.
func (e *nodeEnv) finish(n testNode, c *nodeproto.ClaimResponse) {
	e.t.Helper()
	reason := "test_done"
	if _, err := e.s.CompleteAttempt(e.ctx, n.id, owner(n, c), Completion{Outcome: "failed", Cleanup: "confirmed", Reason: &reason}); err != nil {
		e.t.Fatal(err)
	}
}

// TC-71: min0/max2, demand arrives, then idle: 0 → 2 → 0 with pending starts
// counted (no duplicate starts on repeat or after a controller restart).
func TestTC71AutoscalerZeroToMaxToZero(t *testing.T) {
	e := newNodeEnv(t)
	e.projectLimit(3)
	e.policy(PoolPolicyUpdate{MinWorkers: intp(0), MaxWorkers: intp(2), IdleTimeoutSeconds: intp(1), ScaleUpCooldownSeconds: intp(0), ScaleDownCooldownSeconds: intp(0)})
	n := e.enroll("a", e.root.ID)
	caps := capable(e.root.ID)
	e.ctlBeat(n, 3, 0, caps, nil)
	if r := e.reconcile(); len(r.Actions) != 0 || r.Status.CapacityState != "healthy" || r.Status.LiveWorkers != 0 {
		t.Fatalf("idle pool scaled: %+v", r)
	}
	runs := []Run{e.enqueue("r1", deepReview), e.enqueue("r2", deepReview), e.enqueue("r3", deepReview)}
	if got := e.waiting(runs[0].ID); got != "" {
		t.Fatalf("claimable run waiting reason %q", got)
	}
	r := e.reconcile()
	starts := kinds(r.Actions)[nodeproto.CommandStartWorker]
	if len(starts) != 2 || r.Status.DesiredWorkers != 2 || r.Status.QueuedDemand != 2 {
		t.Fatalf("scale up: %+v", r)
	}
	// Pending starts count as live: no second batch, even from a restarted
	// controller (a new pool on the same database).
	if again := e.reconcile(); len(again.Actions) != 0 || again.Status.PendingStarts != 2 {
		t.Fatalf("duplicate starts: %+v", again)
	}
	restarted := restartStore(t, e.s)
	if again := reconcileWith(t, restarted); len(again.Actions) != 0 {
		t.Fatalf("restarted controller re-issued starts: %+v", again)
	}
	// The agent receives both starts in sequence order.
	out := e.ctlBeat(n, 3, 0, caps, nil)
	if len(out.Commands) != 2 || out.Commands[0].Kind != nodeproto.CommandStartWorker || out.Commands[1].Sequence != out.Commands[0].Sequence+1 {
		t.Fatalf("commands %+v", out.Commands)
	}
	cursor := out.Commands[1].Sequence
	w := map[string]string{starts[0]: "idle", starts[1]: "idle"}
	e.ctlBeat(n, 3, cursor, caps, w)
	c1 := e.claimOK(n, starts[0], "k1")
	c2 := e.claimOK(n, starts[1], "k2")
	if r := e.reconcile(); len(r.Actions) != 0 || r.Status.BusyWorkers != 2 {
		t.Fatalf("busy at max: %+v", r)
	}
	e.finish(n, c1)
	e.finish(n, c2)
	e.ctlBeat(n, 3, cursor, caps, w)
	c3 := e.claimOK(n, starts[0], "k3")
	e.finish(n, c3)
	e.ctlBeat(n, 3, cursor, caps, w)
	// Idle for at least idle_timeout, no demand: both stop.
	time.Sleep(1100 * time.Millisecond)
	r = e.reconcile()
	if stops := kinds(r.Actions)[nodeproto.CommandStopWorker]; len(stops) != 2 || r.Status.LiveWorkers != 0 {
		t.Fatalf("scale to zero: %+v", r)
	}
	out = e.ctlBeat(n, 3, cursor, caps, w)
	if len(out.Commands) != 2 {
		t.Fatalf("stop commands %+v", out.Commands)
	}
	// A worker told to stop can no longer claim, even before the agent applied it.
	e.enqueue("r4", deepReview)
	if _, err := e.s.ClaimAttempt(e.ctx, n.id, starts[0], "k4"); err != domain.ErrConflict {
		t.Fatalf("stopping worker claimed: %v", err)
	}
	e.ctlBeat(n, 3, out.Commands[1].Sequence, caps, nil)
	// The stopped workers no longer count; the new run r4 is fresh demand and
	// gets a new worker (never one of the stopped IDs).
	r = e.reconcile()
	if s := kinds(r.Actions)[nodeproto.CommandStartWorker]; len(s) != 1 || s[0] == starts[0] || s[0] == starts[1] || r.Status.LiveWorkers != 1 || r.Status.BusyWorkers != 0 {
		t.Fatalf("after stop: %+v", r)
	}
}

func restartStore(t *testing.T, s *Store) *Store {
	t.Helper()
	p, err := pgxpool.NewWithConfig(context.Background(), s.pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return &Store{pool: p}
}

// TC-71: warm minimum, idle timeout and both cooldowns; a start that never
// shows up stops counting after StartTimeout.
func TestTC71WarmMinimumCooldownsAndStartTimeout(t *testing.T) {
	e := newNodeEnv(t)
	e.projectLimit(3)
	e.policy(PoolPolicyUpdate{MinWorkers: intp(1), MaxWorkers: intp(3), IdleTimeoutSeconds: intp(1), ScaleUpCooldownSeconds: intp(3600), ScaleDownCooldownSeconds: intp(0)})
	n := e.enroll("a", e.root.ID)
	caps := capable(e.root.ID)
	e.ctlBeat(n, 3, 0, caps, nil)
	r := e.reconcile()
	warm := kinds(r.Actions)[nodeproto.CommandStartWorker]
	if len(warm) != 1 {
		t.Fatalf("warm start: %+v", r)
	}
	out := e.ctlBeat(n, 3, 0, caps, map[string]string{warm[0]: "idle"})
	cursor := out.Commands[0].Sequence
	if r := e.reconcile(); len(r.Actions) != 0 {
		t.Fatalf("min satisfied but acted: %+v", r)
	}
	// Two runs: one matches the warm worker, the other needs a start that the
	// scale-up cooldown defers (reported, not silently dropped).
	e.enqueue("r1", deepReview)
	e.enqueue("r2", deepReview)
	r = e.reconcile()
	if len(r.Actions) != 0 || !contains(r.Status.Reasons, ScaleReasonScaleUpCooldown) {
		t.Fatalf("cooldown: %+v", r)
	}
	// The warm worker is never stopped below min, even idle past the timeout.
	time.Sleep(1100 * time.Millisecond)
	e.policy(PoolPolicyUpdate{ScaleUpCooldownSeconds: intp(0)})
	r = e.reconcile()
	if s := kinds(r.Actions); len(s[nodeproto.CommandStartWorker]) != 1 || len(s[nodeproto.CommandStopWorker]) != 0 {
		t.Fatalf("after cooldown: %+v", r)
	}
	lost := r.Actions[0].WorkerID
	// The agent never reports the started worker (crash between journal and
	// spawn): after StartTimeout it no longer counts and a replacement is issued.
	old := StartTimeout
	StartTimeout = 500 * time.Millisecond
	t.Cleanup(func() { StartTimeout = old })
	e.ctlBeat(n, 3, cursor, caps, map[string]string{warm[0]: "idle"})
	time.Sleep(700 * time.Millisecond)
	r = e.reconcile()
	if s := kinds(r.Actions)[nodeproto.CommandStartWorker]; len(s) != 1 || s[0] == lost {
		t.Fatalf("replacement start: %+v", r)
	}
	reconnected := e.ctlBeat(n, 3, cursor, caps, map[string]string{warm[0]: "idle"})
	if len(reconnected.Commands) != 1 || *reconnected.Commands[0].WorkerID != r.Actions[0].WorkerID {
		t.Fatalf("reconnected node received expired start: %+v", reconnected.Commands)
	}
}

// TC-72: busy 3 → max 1 drains without killing; new work is refused until
// the pool is back under max; max 0 pauses.
func TestTC72DrainWhenMaxFallsBelowBusy(t *testing.T) {
	e := newNodeEnv(t)
	e.projectLimit(5)
	if _, err := e.s.SetSchedulerLimits(e.ctx, intp(5), nil); err != nil {
		t.Fatal(err)
	}
	e.policy(PoolPolicyUpdate{MaxWorkers: intp(3), ScaleUpCooldownSeconds: intp(0), ScaleDownCooldownSeconds: intp(0)})
	n := e.enroll("a", e.root.ID)
	caps := capable(e.root.ID)
	e.ctlBeat(n, 4, 0, caps, nil)
	for _, k := range []string{"r1", "r2", "r3", "r4"} {
		e.enqueue(k, deepReview)
	}
	r := e.reconcile()
	starts := kinds(r.Actions)[nodeproto.CommandStartWorker]
	if len(starts) != 3 {
		t.Fatalf("starts %+v", r)
	}
	ws := map[string]string{}
	for _, w := range starts {
		ws[w] = "idle"
	}
	out := e.ctlBeat(n, 4, 0, caps, ws)
	cursor := out.Commands[len(out.Commands)-1].Sequence
	claims := map[string]*nodeproto.ClaimResponse{}
	for _, w := range starts {
		claims[w] = e.claimOK(n, w, "k-"+w)
		ws[w] = "busy"
	}
	e.ctlBeat(n, 4, cursor, caps, ws)
	e.policy(PoolPolicyUpdate{MaxWorkers: intp(1)})
	r = e.reconcile()
	drains := kinds(r.Actions)[nodeproto.CommandDrainWorker]
	if len(drains) != 2 || len(kinds(r.Actions)[nodeproto.CommandStopWorker]) != 0 || r.Status.CapacityState != "over_target_draining" {
		t.Fatalf("drain: %+v", r)
	}
	// Nothing is killed: all three leases stay, no cancel command exists.
	if e.count("SELECT count(*) FROM attempts WHERE status='leased'") != 3 || e.count("SELECT count(*) FROM node_commands WHERE kind='cancel_attempt'") != 0 {
		t.Fatal("drain cancelled work")
	}
	// Re-running the decision does not drain more.
	if again := e.reconcile(); len(again.Actions) != 0 || again.Status.CapacityState != "over_target_draining" || again.Status.BusyWorkers != 3 {
		t.Fatalf("second drain: %+v", again)
	}
	out = e.ctlBeat(n, 4, cursor, caps, ws)
	cursor = out.Commands[len(out.Commands)-1].Sequence
	kept := ""
	for _, w := range starts {
		if w != drains[0] && w != drains[1] {
			kept = w
		}
	}
	// The kept worker finishes; the pool is still over max (2 active), so its
	// next claim gets nothing and the queued run waits for capacity.
	e.finish(n, claims[kept])
	ws[kept] = "idle"
	e.ctlBeat(n, 4, cursor, caps, ws)
	e.noClaim(n, kept, "k-next")
	queued := e.count("SELECT count(*) FROM runs WHERE status='queued'")
	if queued != 1 {
		t.Fatalf("queued %d", queued)
	}
	// Drained workers may not claim; they exit after their attempt.
	for _, d := range drains {
		e.finish(n, claims[d])
		ws[d] = "idle"
	}
	e.ctlBeat(n, 4, cursor, caps, ws)
	if _, err := e.s.ClaimAttempt(e.ctx, n.id, drains[0], "k-drained"); err != domain.ErrConflict {
		t.Fatalf("drained worker claimed: %v", err)
	}
	delete(ws, drains[0])
	delete(ws, drains[1])
	e.ctlBeat(n, 4, cursor, caps, ws)
	if r := e.reconcile(); r.Status.CapacityState != "healthy" || r.Status.LiveWorkers != 1 {
		t.Fatalf("after drain: %+v", r)
	}
	e.claimOK(n, kept, "k-after")
	// max 0 (min 0) pauses the pool: the idle worker stops, runs wait.
	e.enqueue("r5", deepReview)
	r5 := e.enqueue("r6", deepReview)
	e.policy(PoolPolicyUpdate{MaxWorkers: intp(0)})
	if r := e.reconcile(); r.Status.CapacityState != "paused" {
		t.Fatalf("paused: %+v", r)
	}
	if got := e.waiting(r5.ID); got != nodeproto.WaitingPoolPaused {
		t.Fatalf("paused waiting reason %q", got)
	}
	// min > max is refused.
	if _, _, err := e.s.SetPoolPolicy(e.ctx, PoolPolicyUpdate{Name: "default", MinWorkers: intp(1)}); err != domain.ErrInvalid {
		t.Fatalf("min>max accepted: %v", err)
	}
}

// TC-73: per-project round-robin is the order, not only the cap.
func TestTC73ProjectRoundRobinFairness(t *testing.T) {
	e := newNodeEnv(t)
	e.projectLimit(10)
	other, err := e.s.RegisterProject(e.ctx, e.user, ProjectRegistration{Name: "other", Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: e.root.ID, RelativePath: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	n := e.enroll("a", e.root.ID)
	n.workers = n.workers[:1]
	a1, a2 := e.enqueue("a1", deepReview), e.enqueue("a2", deepReview)
	e.enqueue("a3", deepReview)
	b1, err := e.s.Enqueue(e.ctx, e.user, other.ID, "b1", []byte(deepReview))
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	for i := 0; i < 3; i++ {
		e.idle(n)
		c := e.claimOK(n, n.workers[0], "k"+strconv.Itoa(i))
		order = append(order, c.Lease.RunID)
		e.finish(n, c)
	}
	if order[0] != a1.ID || order[1] != b1.ID || order[2] != a2.ID {
		t.Fatalf("order %v want a1,b1,a2", order)
	}
}

// TC-73: project, global, pool and node budgets, and the autoscaler's demand
// uses the claim gate (capability, root grant, requester permission).
func TestTC73BudgetsAndDemandGate(t *testing.T) {
	e := newNodeEnv(t)
	e.policy(PoolPolicyUpdate{MaxWorkers: intp(4), ScaleUpCooldownSeconds: intp(0)})
	n := e.enroll("a", e.root.ID)
	caps := capable(e.root.ID)
	// No analysis capability (e.g. probe found no handler): no scale-up.
	e.ctlBeat(n, 4, 0, []string{nodeproto.LocalSourceCapability(e.root.ID)}, nil)
	r1 := e.enqueue("r1", deepReview)
	r2 := e.enqueue("r2", deepReview)
	if r := e.reconcile(); len(r.Actions) != 0 {
		t.Fatalf("started without capability: %+v", r)
	}
	// Project limit 1 (default): two runs of one project are demand 1.
	e.ctlBeat(n, 4, 0, caps, nil)
	r := e.reconcile()
	if len(r.Actions) != 1 || r.Status.QueuedDemand != 1 {
		t.Fatalf("project-limited demand: %+v", r)
	}
	w := r.Actions[0].WorkerID
	out := e.ctlBeat(n, 4, 0, caps, map[string]string{w: "idle"})
	cursor := out.Commands[0].Sequence
	c := e.claimOK(n, w, "k1")
	if got := e.waiting(r2.ID); got != nodeproto.WaitingProjectLimit {
		t.Fatalf("waiting %q want project_limit", got)
	}
	_ = r1
	// Global budget: with global 1 used, a second project's run is not demand.
	if _, err := e.s.SetSchedulerLimits(e.ctx, intp(1), nil); err != nil {
		t.Fatal(err)
	}
	other, err := e.s.RegisterProject(e.ctx, e.user, ProjectRegistration{Name: "other", Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: e.root.ID, RelativePath: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	o1, err := e.s.Enqueue(e.ctx, e.user, other.ID, "o1", []byte(deepReview))
	if err != nil {
		t.Fatal(err)
	}
	e.ctlBeat(n, 4, cursor, caps, map[string]string{w: "busy"})
	if r := e.reconcile(); len(r.Actions) != 0 {
		t.Fatalf("global budget ignored: %+v", r)
	}
	if got := e.waiting(o1.ID); got != nodeproto.WaitingCapacityFull {
		t.Fatalf("waiting %q want capacity_full", got)
	}
	if _, err := e.s.SetSchedulerLimits(e.ctx, intp(10), nil); err != nil {
		t.Fatal(err)
	}
	// Requester lost run permission: not demand (the next claim fails the run).
	if err := e.s.DisableUser(e.ctx, e.user); err != nil {
		t.Fatal(err)
	}
	if r := e.reconcile(); len(r.Actions) != 0 {
		t.Fatalf("revoked requester counted as demand: %+v", r)
	}
	e.finish(n, c)
}

// TC-73: node max_workers and resources bound placement (degraded_capacity),
// static nodes never receive scaling commands.
func TestTC73NodeBudgetAndStaticNodes(t *testing.T) {
	e := newNodeEnv(t)
	e.projectLimit(5)
	e.policy(PoolPolicyUpdate{MinWorkers: intp(1), MaxWorkers: intp(3), ScaleUpCooldownSeconds: intp(0)})
	static := e.enroll("static", e.root.ID)
	e.idle(static)
	if r := e.reconcile(); len(r.Actions) != 0 || r.Status.CapacityState != "degraded_capacity" || !contains(r.Status.Reasons, ScaleReasonNoControllerNode) {
		t.Fatalf("static-only pool: %+v", r)
	}
	if e.count("SELECT count(*) FROM node_commands WHERE node_id=$1", static.id) != 0 {
		t.Fatal("static node got scaling commands")
	}
	n := e.enroll("ctl", e.root.ID)
	e.ctlBeat(n, 1, 0, capable(e.root.ID), nil)
	e.enqueue("r1", deepReview)
	e.enqueue("r2", deepReview)
	e.enqueue("r3", deepReview)
	r := e.reconcile()
	if len(r.Actions) != 1 || r.Actions[0].NodeID != n.id || r.Status.CapacityState != "degraded_capacity" || !contains(r.Status.Reasons, ScaleReasonNodeCapacity) {
		t.Fatalf("node max_workers: %+v", r)
	}
	// Resource budget: 1 CPU core fits one attempt reservation.
	n2 := e.enroll("small", e.root.ID)
	in := hb(nil, "", capable(e.root.ID))
	in.WorkerScaling, in.MaxWorkers, in.Capacity.CPUCores = nodeproto.ScalingController, 8, 1
	e.beat(n2, in)
	r = e.reconcile()
	if len(kinds(r.Actions)[nodeproto.CommandStartWorker]) != 1 || r.Actions[0].NodeID != n2.id {
		t.Fatalf("resource budget: %+v", r)
	}
	// Warnings: min above the global limit / controller node capacity.
	_, warns, err := e.s.SetPoolPolicy(e.ctx, PoolPolicyUpdate{Name: "default", MinWorkers: intp(10), MaxWorkers: intp(20)})
	if err != nil || !contains(warns, WarnMinExceedsGlobalRunning) || !contains(warns, WarnMinExceedsNodeCapacity) {
		t.Fatalf("warnings %v %v", warns, err)
	}
	if r := e.reconcile(); r.Status.CapacityState != "degraded_capacity" || !contains(r.Status.Reasons, WarnMinExceedsGlobalRunning) {
		t.Fatalf("unreachable pool minimum: %+v", r)
	}
	// Optimistic revision check.
	stale := int64(1)
	if _, _, err := e.s.SetPoolPolicy(e.ctx, PoolPolicyUpdate{Name: "default", MaxWorkers: intp(4), ExpectRevision: &stale}); err != domain.ErrConflict {
		t.Fatalf("stale revision accepted: %v", err)
	}
}

// Heartbeat scaling fields are validated; static is the default.
func TestHeartbeatScalingValidation(t *testing.T) {
	e := newNodeEnv(t)
	n := e.enroll("a", e.root.ID)
	for _, bad := range []nodeproto.NodeHeartbeatRequest{
		func() nodeproto.NodeHeartbeatRequest { in := hb(nil, "", nil); in.WorkerScaling = "auto"; return in }(),
		func() nodeproto.NodeHeartbeatRequest {
			in := hb(nil, "", nil)
			in.WorkerScaling = "controller"
			return in
		}(),
		func() nodeproto.NodeHeartbeatRequest { in := hb(nil, "", nil); in.MaxWorkers = 2; return in }(),
		func() nodeproto.NodeHeartbeatRequest {
			in := hb(nil, "", nil)
			in.WorkerScaling, in.MaxWorkers = "controller", nodeproto.MaxNodeWorkers+1
			return in
		}(),
	} {
		if _, err := e.s.NodeHeartbeat(e.ctx, n.id, bad); err != domain.ErrInvalid {
			t.Fatalf("accepted %+v: %v", bad, err)
		}
	}
	e.ctlBeat(n, 2, 0, nil, nil)
	e.idle(n)
	var scaling string
	var maxW *int
	if err := e.s.pool.QueryRow(e.ctx, "SELECT worker_scaling,max_workers FROM nodes WHERE id=$1", n.id).Scan(&scaling, &maxW); err != nil || scaling != "static" || maxW != nil {
		t.Fatalf("back to static: %s %v %v", scaling, maxW, err)
	}
}

// A controller node may restart with a lower local cap or report extra idle workers.
// Existing reservations continue to count against the new cap at claim time.
func TestTC73ClaimHonoursReducedNodeCap(t *testing.T) {
	e := newNodeEnv(t)
	e.projectLimit(3)
	e.policy(PoolPolicyUpdate{MaxWorkers: intp(3)})
	n := e.enroll("ctl", e.root.ID)
	w1, w2 := domain.NewID(), domain.NewID()
	e.ctlBeat(n, 2, 0, capable(e.root.ID), map[string]string{w1: "idle", w2: "idle"})
	e.enqueue("node-one", deepReview)
	r2 := e.enqueue("node-two", deepReview)
	e.claimOK(n, w1, "node-one-claim")
	e.ctlBeat(n, 1, 0, capable(e.root.ID), map[string]string{w1: "busy", w2: "idle"})
	e.noClaim(n, w2, "node-two-claim")
	if got := e.waiting(r2.ID); got != nodeproto.WaitingCapacityFull {
		t.Fatalf("node cap waiting reason %q", got)
	}
}
