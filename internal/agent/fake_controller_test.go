package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// fakeController is a TEST-ONLY strict in-memory implementation of the node
// HTTP API (contract §3). It is not the controller: it exists so agent tests
// run without PostgreSQL. Real DB semantics (TC-21) are tested by the
// controller branch and the parent smoke test.
type fakeController struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	token  string
	nodeID string
	secret string

	workers    map[string]string // id → last reported state
	jobs       []nodeproto.Job   // queue of claimable jobs
	claims     int
	claimKeys  map[string]*nodeproto.ClaimResponse
	attempts   map[string]*fakeAttempt
	commands   []nodeproto.Command
	commandSeq int64
	reports    []nodeproto.CleanupReport
	hbCount    int
	lastHB     nodeproto.NodeHeartbeatRequest

	hangAttemptHB bool // never answer attempt heartbeats (lease watchdog test)
	staleAttempts bool // answer attempt heartbeats with 409
	completeCh    chan nodeproto.CompleteRequest
	reportCh      chan nodeproto.CleanupReport
	eventCh       chan string
}

type fakeAttempt struct {
	lease     nodeproto.Lease
	artifacts map[string]fakeArtifact // client id → artifact
	byID      map[string]fakeArtifact
	events    []string
	tasks     map[string]int64
	runStatus string
	complete  *nodeproto.CompleteRequest
}

type fakeArtifact struct {
	id, role, sha string
	body          []byte
}

func newFakeController(t *testing.T) *fakeController {
	f := &fakeController{t: t, token: strings.Repeat("t", 43), workers: map[string]string{}, claimKeys: map[string]*nodeproto.ClaimResponse{}, attempts: map[string]*fakeAttempt{}, completeCh: make(chan nodeproto.CompleteRequest, 16), reportCh: make(chan nodeproto.CleanupReport, 16), eventCh: make(chan string, 256)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeController) addJob(j nodeproto.Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, j)
}

func (f *fakeController) queueCommand(kind, attemptID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commandSeq++
	a := attemptID
	f.commands = append(f.commands, nodeproto.Command{CommandID: domain.NewID(), Sequence: f.commandSeq, Kind: kind, AttemptID: &a})
}

func fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": code, "retryable": false, "request_id": "x", "details": map[string]any{}}})
}

func ok(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func strict(r *http.Request, v any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (f *fakeController) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" {
		fail(w, 403, "ORIGIN_REJECTED")
		return
	}
	if r.Header.Get("Cookie") != "" {
		f.t.Errorf("agent sent a cookie")
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if path == "/nodes/enroll" && r.Method == http.MethodPost {
		f.enroll(w, r)
		return
	}
	f.mu.Lock()
	authed := f.secret != "" && r.Header.Get("Authorization") == "Bearer "+f.secret
	f.mu.Unlock()
	if !authed {
		fail(w, 401, nodeproto.CodeNodeUnauthenticated)
		return
	}
	if r.Method != http.MethodPut && r.Header.Get("Content-Type") != "application/json" {
		fail(w, 415, "UNSUPPORTED_MEDIA_TYPE")
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "heartbeat":
		f.nodeHeartbeat(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "nodes" && parts[2] == "claims":
		f.claim(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "attempts" && parts[2] == "heartbeat":
		f.attemptHeartbeat(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "attempts" && parts[2] == "events":
		f.event(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "attempts" && parts[2] == "artifacts" && r.Method == http.MethodPut:
		f.artifact(w, r, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "attempts" && parts[2] == "complete":
		f.complete(w, r, parts[1])
	default:
		fail(w, 404, "NOT_FOUND")
	}
}

func (f *fakeController) enroll(w http.ResponseWriter, r *http.Request) {
	var req nodeproto.EnrollRequest
	if strict(r, &req) != nil || req.ProtocolVersion != nodeproto.ProtocolVersion {
		fail(w, 422, nodeproto.CodeProtocolUnsupported)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.EnrollmentToken != f.token || f.secret != "" {
		fail(w, 401, "UNAUTHENTICATED")
		return
	}
	f.nodeID, f.secret = domain.NewID(), strings.Repeat("s", 40)+"SEC"
	ok(w, 201, nodeproto.EnrollResponse{NodeID: f.nodeID, NodeSecret: f.secret, PoolID: domain.NewID(), HeartbeatSeconds: 5, LeaseSeconds: 30})
}

func (f *fakeController) nodeHeartbeat(w http.ResponseWriter, r *http.Request, node string) {
	var req nodeproto.NodeHeartbeatRequest
	if err := strict(r, &req); err != nil {
		f.t.Errorf("heartbeat body: %v", err)
		fail(w, 400, "INVALID")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if node != f.nodeID {
		fail(w, 404, "NOT_FOUND")
		return
	}
	f.hbCount++
	f.lastHB = req
	f.workers = map[string]string{}
	for _, ow := range req.ObservedWorkers {
		f.workers[ow.ID] = ow.State
	}
	resp := nodeproto.NodeHeartbeatResponse{ServerTime: time.Now().UTC(), Commands: []nodeproto.Command{}, CleanupResults: []nodeproto.CleanupResult{}}
	for _, c := range f.commands {
		if c.Sequence > req.LastCommandSequence {
			resp.Commands = append(resp.Commands, c)
		}
	}
	for _, rep := range req.CleanupReports {
		f.reports = append(f.reports, rep)
		select {
		case f.reportCh <- rep:
		default:
		}
		resp.CleanupResults = append(resp.CleanupResults, nodeproto.CleanupResult{AttemptID: rep.AttemptID, Accepted: rep.CleanupState == nodeproto.CleanupConfirmed})
	}
	ok(w, 200, resp)
}

func (f *fakeController) claim(w http.ResponseWriter, r *http.Request, node string) {
	var req nodeproto.ClaimRequest
	key := r.Header.Get(nodeproto.HeaderIdempotencyKey)
	if strict(r, &req) != nil || key == "" || len(key) > 200 {
		fail(w, 400, "INVALID")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims++
	if prev, seen := f.claimKeys[key]; seen {
		if prev == nil {
			ok(w, 204, nil)
		} else {
			ok(w, 200, prev)
		}
		return
	}
	if f.workers[req.WorkerID] != nodeproto.WorkerIdle {
		fail(w, 409, "CONFLICT")
		return
	}
	if len(f.jobs) == 0 {
		f.claimKeys[key] = nil
		ok(w, 204, nil)
		return
	}
	job := f.jobs[0]
	f.jobs = f.jobs[1:]
	lease := nodeproto.Lease{RunID: domain.NewID(), AttemptID: domain.NewID(), WorkerID: req.WorkerID, LeaseEpoch: 1, ExpiresAt: time.Now().Add(30 * time.Second), ProjectRevision: 1, RunVersion: 2}
	f.attempts[lease.AttemptID] = &fakeAttempt{lease: lease, artifacts: map[string]fakeArtifact{}, byID: map[string]fakeArtifact{}, tasks: map[string]int64{}, runStatus: "preparing"}
	resp := &nodeproto.ClaimResponse{Lease: lease, Job: job}
	f.claimKeys[key] = resp
	f.workers[req.WorkerID] = nodeproto.WorkerBusy
	ok(w, 200, resp)
}

func (f *fakeController) owned(a *fakeAttempt, worker string, epoch int64) bool {
	return a != nil && a.lease.WorkerID == worker && a.lease.LeaseEpoch == epoch && a.complete == nil
}

func (f *fakeController) attemptHeartbeat(w http.ResponseWriter, r *http.Request, id string) {
	var req nodeproto.AttemptHeartbeatRequest
	if strict(r, &req) != nil {
		fail(w, 400, "INVALID")
		return
	}
	f.mu.Lock()
	hang, stale := f.hangAttemptHB, f.staleAttempts
	a := f.attempts[id]
	owned := f.owned(a, req.WorkerID, req.LeaseEpoch)
	f.mu.Unlock()
	if hang {
		<-r.Context().Done()
		return
	}
	if stale || !owned {
		fail(w, 409, nodeproto.CodeStaleAttempt)
		return
	}
	ok(w, 200, nodeproto.AttemptHeartbeatResponse{ExpiresAt: time.Now().Add(30 * time.Second)})
}

func (f *fakeController) event(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		WorkerID      string          `json:"worker_id"`
		LeaseEpoch    int64           `json:"lease_epoch"`
		ClientEventID string          `json:"client_event_id"`
		Kind          string          `json:"kind"`
		Payload       json.RawMessage `json:"payload"`
	}
	if strict(r, &req) != nil || !domain.ValidID(req.ClientEventID) {
		fail(w, 400, "INVALID")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.attempts[id]
	if !f.owned(a, req.WorkerID, req.LeaseEpoch) || f.staleAttempts {
		fail(w, 409, nodeproto.CodeStaleAttempt)
		return
	}
	a.events = append(a.events, req.Kind)
	select {
	case f.eventCh <- req.Kind:
	default:
	}
	resp := nodeproto.EventResponse{Sequence: int64(len(a.events)), RunVersion: 3}
	switch req.Kind {
	case nodeproto.EventSnapshotFixed:
		var p nodeproto.SnapshotFixedPayload
		if json.Unmarshal(req.Payload, &p) != nil {
			fail(w, 400, "INVALID")
			return
		}
		art, found := a.byID[p.ManifestArtifactID]
		if !found || art.role != nodeproto.RoleSourceManifest || art.sha != p.ManifestDigest {
			fail(w, 422, "INVALID")
			return
		}
		sid := domain.NewID()
		resp.SnapshotID = &sid
	case nodeproto.EventTaskCreated:
		tid, v := domain.NewID(), int64(1)
		a.tasks[tid] = 1
		resp.TaskID, resp.Version = &tid, &v
	case nodeproto.EventTaskStatus:
		var p nodeproto.TaskStatusPayload
		if json.Unmarshal(req.Payload, &p) != nil || a.tasks[p.TaskID] != p.ExpectedVersion {
			fail(w, 409, "CONFLICT")
			return
		}
		a.tasks[p.TaskID]++
		v := a.tasks[p.TaskID]
		resp.Version = &v
	case nodeproto.EventRunAdvance:
		var p nodeproto.RunAdvancePayload
		_ = json.Unmarshal(req.Payload, &p)
		a.runStatus = p.Status
	}
	ok(w, 200, resp)
}

func (f *fakeController) artifact(w http.ResponseWriter, r *http.Request, id, client string) {
	body, _ := io.ReadAll(r.Body)
	sum := sha256.Sum256(body)
	epoch, _ := strconv.ParseInt(r.Header.Get(nodeproto.HeaderLeaseEpoch), 10, 64)
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.attempts[id]
	if !f.owned(a, r.Header.Get(nodeproto.HeaderWorkerID), epoch) {
		fail(w, 409, nodeproto.CodeStaleAttempt)
		return
	}
	if r.ContentLength != int64(len(body)) || hex.EncodeToString(sum[:]) != r.Header.Get(nodeproto.HeaderContentSHA256) {
		fail(w, 422, nodeproto.CodeIntegrityMismatch)
		return
	}
	if !nodeproto.ValidArtifactRole(r.Header.Get(nodeproto.HeaderArtifactRole)) || r.Header.Get("Content-Type") == "" {
		fail(w, 400, "INVALID")
		return
	}
	art, seen := a.artifacts[client]
	if !seen {
		art = fakeArtifact{id: domain.NewID(), role: r.Header.Get(nodeproto.HeaderArtifactRole), sha: hex.EncodeToString(sum[:]), body: body}
		a.artifacts[client] = art
		a.byID[art.id] = art
	}
	ok(w, 201, nodeproto.ArtifactResponse{ArtifactID: art.id, SHA256: art.sha, SizeBytes: int64(len(body)), Finalized: true})
}

func (f *fakeController) complete(w http.ResponseWriter, r *http.Request, id string) {
	var req nodeproto.CompleteRequest
	if strict(r, &req) != nil {
		fail(w, 400, "INVALID")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.attempts[id]
	if !f.owned(a, req.WorkerID, req.LeaseEpoch) || f.staleAttempts {
		fail(w, 409, nodeproto.CodeStaleAttempt)
		return
	}
	if req.Outcome == nodeproto.OutcomeSucceeded || req.Outcome == nodeproto.OutcomePartial {
		art, found := a.byID[deref(req.ResultManifestID)]
		if !found || art.role != nodeproto.RoleResultManifest || a.runStatus != "finalizing" {
			fail(w, 409, "CONFLICT")
			return
		}
	}
	a.complete = &req
	f.completeCh <- req
	ok(w, 202, nodeproto.RunAck{ID: a.lease.RunID, Status: "finalizing", Version: 4, URL: "/runs/" + a.lease.RunID})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (f *fakeController) claimCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.claims
}
