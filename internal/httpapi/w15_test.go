package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// W15 activity/progress over real HTTP and PostgreSQL (TC-98/TC-99). Skips
// (NOT executed) without CODEYE_TEST_DATABASE_URL.

// activityWithoutClock drops checked_at (the read time) for comparisons.
func activityWithoutClock(t *testing.T, a any) map[string]any {
	t.Helper()
	m, ok := a.(map[string]any)
	if !ok {
		t.Fatalf("activity %v", a)
	}
	if _, err := time.Parse(time.RFC3339Nano, m["checked_at"].(string)); err != nil {
		t.Fatalf("checked_at %v", m["checked_at"])
	}
	out := map[string]any{}
	for k, v := range m {
		if k != "checked_at" {
			out[k] = v
		}
	}
	return out
}

func stageStates(t *testing.T, stages any) string {
	t.Helper()
	var out []string
	for _, s := range stages.([]any) {
		m := s.(map[string]any)
		out = append(out, m["key"].(string)+"="+m["state"].(string))
	}
	return strings.Join(out, ",")
}

type httpAttempt struct {
	e       *env
	id      string
	worker  string
	epoch   int
	sec     nreq
	nodeID  string
	runID   string
	tasks   map[string]string
	version map[string]float64
}

func (a *httpAttempt) path() string { return "/api/v1/attempts/" + a.id }

func (a *httpAttempt) send(clientID, kind string, payload any) resp {
	return a.e.node("POST", a.path()+"/events", map[string]any{"worker_id": a.worker, "lease_epoch": a.epoch, "client_event_id": clientID, "kind": kind, "payload": payload}, a.sec)
}

func (a *httpAttempt) ev(kind string, payload any) resp {
	a.e.t.Helper()
	return a.send(domain.NewID(), kind, payload).want(a.e.t, 200, "")
}

func (a *httpAttempt) snapshot() {
	t := a.e.t
	t.Helper()
	manifest := []byte(`{"schema_version":"0.1","run":"` + a.runID + `"}`)
	sha := domain.Digest(manifest)
	o := nreq{secret: a.sec.secret, ctype: "application/json", header: map[string]string{nodeproto.HeaderWorkerID: a.worker, nodeproto.HeaderLeaseEpoch: strconv.Itoa(a.epoch), nodeproto.HeaderArtifactRole: "source_manifest", nodeproto.HeaderContentSHA256: sha}}
	mid := a.e.node("PUT", a.path()+"/artifacts/"+domain.NewID(), manifest, o).want(t, 201, "").body["artifact_id"]
	a.ev("snapshot_fixed", map[string]any{"manifest_digest": sha, "kind": "local", "consistency": "stable_copy_best_effort", "source_sha": nil, "manifest_artifact_id": mid})
}

func (a *httpAttempt) task(key, stage string) {
	a.e.t.Helper()
	p := map[string]any{"key": key, "required": true}
	if stage != "" {
		p["stage"] = stage
	}
	r := a.ev("task_created", p)
	a.tasks[key], a.version[key] = r.body["task_id"].(string), r.body["version"].(float64)
}

func (a *httpAttempt) statusBody(key, status string) map[string]any {
	return map[string]any{"task_id": a.tasks[key], "expected_version": a.version[key], "status": status, "reason": nil}
}

func (a *httpAttempt) move(key, status string) {
	a.e.t.Helper()
	a.version[key] = a.ev("task_status", a.statusBody(key, status)).body["version"].(float64)
}

func (e *env) claimHTTP(f nodeFixture, n nodeproto.EnrollResponse, worker string, epoch int) *httpAttempt {
	e.t.Helper()
	sec := nreq{secret: n.NodeSecret}
	caps := []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.LocalSourceCapability(f.root.ID)}
	e.node("POST", "/api/v1/nodes/"+n.NodeID+"/heartbeat", heartbeatBody([]string{worker}, caps), sec).want(e.t, 200, "")
	cl := e.node("POST", "/api/v1/nodes/"+n.NodeID+"/claims", nodeproto.ClaimRequest{WorkerID: worker}, nreq{secret: n.NodeSecret, header: map[string]string{"Idempotency-Key": domain.NewID()}}).want(e.t, 200, "")
	var claim nodeproto.ClaimResponse
	if err := json.Unmarshal(cl.raw, &claim); err != nil || claim.Lease.LeaseEpoch != int64(epoch) {
		e.t.Fatalf("claim %s %v", cl.raw, err)
	}
	return &httpAttempt{e: e, id: claim.Lease.AttemptID, worker: worker, epoch: epoch, sec: sec, nodeID: n.NodeID, runID: claim.Lease.RunID, tasks: map[string]string{}, version: map[string]float64{}}
}

// TC-98/TC-99 over HTTP: JSON shape, list/detail/run agreement, hidden foreign
// projects, stale replay after a new attempt (409 STALE_ATTEMPT, view kept),
// duplicate client_event_id applied once, malformed stage rejected.
func TestTC98TC99ActivityOverHTTP(t *testing.T) {
	e := newEnv(t)
	f := e.nodeFixture()
	u := f.user

	// A project without runs, from create, list and detail alike.
	created := e.do("POST", "/api/v1/projects", map[string]any{"name": "empty", "source": map[string]any{"kind": "local", "source_root_id": f.root.ID, "relative_path": "."}}, u.o()).want(t, 201, "")
	emptyID := created.body["id"].(string)
	wantEmpty := map[string]any{"run": nil, "other_active_runs": float64(0), "detail": nil, "stages": []any{}, "waiting_reason": nil, "last_report": nil, "last_event_at": nil}
	for _, a := range []any{created.body["activity"], e.do("GET", "/api/v1/projects/"+emptyID, nil, u.o()).want(t, 200, "").body["activity"]} {
		if got := activityWithoutClock(t, a); !reflect.DeepEqual(got, wantEmpty) {
			t.Fatalf("empty activity %v", got)
		}
	}

	run := e.do("POST", "/api/v1/projects/"+f.projectID+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}, opts{cookie: u.cookie, csrf: u.csrf, idem: "r1"}).want(t, 202, "")
	runID := run.body["id"].(string)
	queued := e.do("GET", "/api/v1/projects/"+f.projectID, nil, u.o()).want(t, 200, "").body["activity"].(map[string]any)
	if queued["waiting_reason"] != nodeproto.WaitingExecutionUnavailable || queued["detail"] != "recorded" || queued["run"].(map[string]any)["id"] != runID {
		t.Fatalf("queued activity %v", queued)
	}
	if v := e.do("GET", "/api/v1/runs/"+runID, nil, u.o()).want(t, 200, "").body["progress"].(map[string]any); v["waiting_reason"] != queued["waiting_reason"] {
		t.Fatalf("waiting reasons differ: %v vs %v", v["waiting_reason"], queued["waiting_reason"])
	}

	n := e.enrollNode(f.root.ID)
	w1, w2 := domain.NewID(), domain.NewID()
	a1 := e.claimHTTP(f, n, w1, 1)
	a1.snapshot()
	a1.task("vet", "static_analysis")
	a1.task("review", "code_review")
	bad := a1.send(domain.NewID(), "task_created", map[string]any{"key": "lint", "required": true, "stage": "lint"})
	bad.want(t, 422, "VALIDATION_FAILED")
	a1.ev("run_advance", map[string]any{"status": "running"})
	oldClient := domain.NewID()
	oldBody := a1.statusBody("vet", "running")
	first := a1.send(oldClient, "task_status", oldBody).want(t, 200, "")
	a1.version["vet"] = first.body["version"].(float64)

	// List, project detail and run detail show the same representative stages.
	agree := func() map[string]any {
		t.Helper()
		var listed any
		for _, it := range e.do("GET", "/api/v1/projects?limit=100", nil, u.o()).want(t, 200, "").body["items"].([]any) {
			if it.(map[string]any)["id"] == f.projectID {
				listed = it.(map[string]any)["activity"]
			}
		}
		detail := e.do("GET", "/api/v1/projects/"+f.projectID, nil, u.o()).want(t, 200, "").body["activity"]
		l, d := activityWithoutClock(t, listed), activityWithoutClock(t, detail)
		if !reflect.DeepEqual(l, d) {
			t.Fatalf("list/detail differ:\n%v\n%v", l, d)
		}
		prog := e.do("GET", "/api/v1/runs/"+runID, nil, u.o()).want(t, 200, "").body["progress"].(map[string]any)
		if !reflect.DeepEqual(prog["stages"], d["stages"]) || prog["detail"] != d["detail"] || prog["last_event_at"] != d["last_event_at"] {
			t.Fatalf("run progress differs:\n%v\n%v", prog, d)
		}
		if _, err := time.Parse(time.RFC3339Nano, prog["checked_at"].(string)); err != nil || prog["stage"] != d["run"].(map[string]any)["status"] {
			t.Fatalf("run progress %v", prog)
		}
		return d
	}
	d := agree()
	if got := stageStates(t, d["stages"]); got != "source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=running,code_review=waiting,report=waiting" {
		t.Fatal(got)
	}
	vet := d["stages"].([]any)[3].(map[string]any)
	if vet["key"] != "static_analysis" || vet["planned"] != float64(1) || vet["finished"] != float64(0) || !reflect.DeepEqual(vet["counts"], map[string]any{}) || !reflect.DeepEqual(vet["reasons"], []any{}) || vet["started_at"] == nil || vet["finished_at"] != nil {
		t.Fatalf("stage JSON %v", vet)
	}
	if src := d["stages"].([]any)[0].(map[string]any); src["planned"] != nil || src["started_at"] == nil || src["finished_at"] == nil {
		t.Fatalf("source JSON %v", src)
	}

	// The attempt fails; the run is retried on a new attempt (epoch 2).
	e.node("POST", a1.path()+"/complete", nodeproto.CompleteRequest{WorkerID: w1, LeaseEpoch: 1, Outcome: "failed", CleanupState: "confirmed"}, a1.sec).want(t, 202, "")
	_, version := e.runStatus(runID)
	if _, err := e.store.Retry(context.Background(), u.id, runID, version); err != nil {
		t.Fatal(err)
	}
	a2 := e.claimHTTP(f, n, w2, 2)
	a2.snapshot()
	a2.task("vet", "static_analysis")
	a2.task("review", "")
	a2.ev("run_advance", map[string]any{"status": "running"})
	a2.move("vet", "running")
	before := agree()

	// Replaying the old attempt's accepted event, or a new old-epoch event, is
	// 409 STALE_ATTEMPT and changes nothing.
	a1.send(oldClient, "task_status", oldBody).want(t, 409, nodeproto.CodeStaleAttempt)
	a1.send(domain.NewID(), "task_status", map[string]any{"task_id": a1.tasks["vet"], "expected_version": a2.version["vet"], "status": "failed", "reason": "timeout"}).want(t, 409, nodeproto.CodeStaleAttempt)
	if after := agree(); !reflect.DeepEqual(before, after) {
		t.Fatalf("stale replay changed the view:\n%v\n%v", before, after)
	}

	// A duplicate client_event_id of the current attempt is applied once.
	dup := domain.NewID()
	body := a2.statusBody("vet", "succeeded")
	r1 := a2.send(dup, "task_status", body).want(t, 200, "")
	once := agree()
	r2 := a2.send(dup, "task_status", body).want(t, 200, "")
	if !bytes.Equal(bytes.TrimSpace(r1.raw), bytes.TrimSpace(r2.raw)) {
		t.Fatalf("replay differs %s %s", r1.raw, r2.raw)
	}
	if twice := agree(); !reflect.DeepEqual(once, twice) || stageStates(t, once["stages"]) != "source=succeeded,environment=no_tasks,documents=no_tasks,static_analysis=succeeded,code_review=waiting,report=waiting" {
		t.Fatalf("duplicate applied twice %v", twice)
	}

	// A user without access sees neither the project nor its run.
	stranger := e.newUser()
	for _, path := range []string{"/api/v1/projects/" + f.projectID, "/api/v1/runs/" + runID} {
		r := e.do("GET", path, nil, stranger.o()).want(t, 404, "NOT_FOUND")
		if strings.Contains(string(r.raw), runID) || strings.Contains(string(r.raw), "static_analysis") {
			t.Fatalf("%s leaked %s", path, r.raw)
		}
	}
	list := e.do("GET", "/api/v1/projects", nil, stranger.o()).want(t, 200, "")
	if len(list.body["items"].([]any)) != 0 || strings.Contains(string(list.raw), runID) {
		t.Fatalf("stranger list %s", list.raw)
	}
}
