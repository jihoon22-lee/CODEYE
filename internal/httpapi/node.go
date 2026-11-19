package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/artifactstore"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// nodeAuthKind classifies node-protocol routes.
type nodeAuthKind int

const (
	nodeNone   nodeAuthKind = iota // browser/session route
	nodePublic                     // POST /nodes/enroll: no bearer
	nodeAuth                       // Authorization: Bearer <node secret>
)

func (s *Server) nodeRoutes() []route {
	r := func(method, path, name string, kind nodeAuthKind, body bool, h func(*Server, *request)) route {
		return route{method: method, parts: strings.Split(path, "/"), name: name, body: body, node: kind, handle: h}
	}
	return []route{
		r("POST", "nodes/enroll", "nodes.enroll", nodePublic, true, (*Server).enrollNode),
		r("POST", "nodes/{id}/heartbeat", "nodes.heartbeat", nodeAuth, true, (*Server).nodeHeartbeat),
		r("POST", "nodes/{id}/claims", "nodes.claim", nodeAuth, true, (*Server).claimAttempt),
		r("POST", "attempts/{id}/heartbeat", "attempts.heartbeat", nodeAuth, true, (*Server).renewAttempt),
		r("POST", "attempts/{id}/events", "attempts.events", nodeAuth, true, (*Server).attemptEvent),
		r("PUT", "attempts/{id}/artifacts/{id}", "attempts.artifact", nodeAuth, false, (*Server).putArtifact),
		r("POST", "attempts/{id}/complete", "attempts.complete", nodeAuth, true, (*Server).completeAttempt),
		r("POST", "attempts/{id}/cleanup", "attempts.cleanup", nodeAuth, true, (*Server).cleanupAttempt),
	}
}

// dispatchNode authenticates node routes. Nodes are not browsers: any Origin
// header is rejected (a browser always sends one on these methods), no cookie
// is read and no CSRF token applies.
func (s *Server) dispatchNode(rt route, q *request) {
	w, r := q.w, q.r
	if _, has := r.Header["Origin"]; has {
		writeError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "node routes do not accept browser requests", false)
		return
	}
	if rt.node == nodeAuth {
		auth := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || !validToken(token) {
			writeError(w, r, http.StatusUnauthorized, nodeproto.CodeNodeUnauthenticated, "node authentication required", false)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), storeTimeout)
		n, err := s.store.AuthenticateNode(ctx, TokenHash(token))
		cancel()
		if err != nil {
			s.nodeError(q, err)
			return
		}
		q.node = n
		if rt.parts[0] == "nodes" && q.params[0] != n.ID {
			writeError(w, r, http.StatusNotFound, "NOT_FOUND", "not found", false)
			return
		}
	}
	if rt.body {
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
			writeError(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "content type must be application/json", false)
			return
		}
	}
	rt.handle(s, q)
}

// nodeError maps store errors for node routes.
func (s *Server) nodeError(q *request, err error) {
	switch {
	case errors.Is(err, domain.ErrUnauthenticated):
		writeError(q.w, q.r, http.StatusUnauthorized, nodeproto.CodeNodeUnauthenticated, "node authentication required", false)
	case errors.Is(err, postgres.ErrIdempotencyConflict):
		writeError(q.w, q.r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used for a different worker", false)
	case errors.Is(err, domain.ErrUnsupported):
		writeError(q.w, q.r, http.StatusUnprocessableEntity, nodeproto.CodeProtocolUnsupported, "protocol version is not supported", false)
	default:
		s.storeError(q, err)
	}
}

func (q *request) decode(v any) bool {
	body, ok := q.readBody()
	if !ok {
		return false
	}
	if domain.DecodeStrict(body, v) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body does not match the node protocol", false)
		return false
	}
	return true
}

func invalid(q *request, msg string) {
	writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", msg, false)
}

// --- enrollment and node heartbeat -------------------------------------------

func (s *Server) enrollNode(q *request) {
	var in nodeproto.EnrollRequest
	if !q.decode(&in) {
		return
	}
	if !validToken(in.EnrollmentToken) {
		writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "enrollment token is invalid, used, expired or revoked", false)
		return
	}
	if in.ProtocolVersion != nodeproto.ProtocolVersion {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, nodeproto.CodeProtocolUnsupported, "protocol version is not supported", false)
		return
	}
	secret := NewToken()
	ctx, cancel := q.ctx()
	defer cancel()
	n, err := s.store.EnrollNode(ctx, postgres.Enrollment{TokenSHA256: TokenHash(in.EnrollmentToken), SecretSHA256: TokenHash(secret),
		Name: in.Name, OS: in.OS, Arch: in.Arch, Protocol: in.ProtocolVersion})
	if err != nil {
		if errors.Is(err, domain.ErrUnauthenticated) {
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "enrollment token is invalid, used, expired or revoked", false)
		} else {
			s.nodeError(q, err)
		}
		return
	}
	writeJSON(q.w, http.StatusCreated, nodeproto.EnrollResponse{NodeID: n.ID, NodeSecret: secret, PoolID: n.PoolID,
		HeartbeatSeconds: nodeproto.HeartbeatSeconds, LeaseSeconds: nodeproto.LeaseSeconds})
}

func (s *Server) nodeHeartbeat(q *request) {
	var in nodeproto.NodeHeartbeatRequest
	if !q.decode(&in) {
		return
	}
	if err := postgres.ValidateHeartbeat(in); err != nil {
		if errors.Is(err, domain.ErrUnsupported) {
			s.nodeError(q, err)
		} else {
			invalid(q, "heartbeat failed validation")
		}
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	out, err := s.store.NodeHeartbeat(ctx, q.node.ID, in)
	if err != nil {
		s.nodeError(q, err)
		return
	}
	for _, rep := range in.CleanupReports {
		res := nodeproto.CleanupResult{AttemptID: rep.AttemptID}
		if rep.CleanupState != nodeproto.CleanupConfirmed {
			// Uncertain cleanup is informational: the attempt stays recovery_required.
			code := "uncertain_not_applied"
			res.Code = &code
		} else {
			_, err := s.store.ConfirmCleanupNode(ctx, q.node.ID, postgres.Ownership{AttemptID: rep.AttemptID, NodeID: q.node.ID, WorkerID: rep.WorkerID, Epoch: rep.LeaseEpoch})
			if err == nil {
				res.Accepted = true
			} else {
				code := postgres.CleanupCode(err)
				res.Code = &code
			}
		}
		out.CleanupResults = append(out.CleanupResults, res)
	}
	writeJSON(q.w, http.StatusOK, out)
}

// --- claim -------------------------------------------------------------------

func (s *Server) claimAttempt(q *request) {
	key := q.r.Header.Get(nodeproto.HeaderIdempotencyKey)
	if !validIdempotencyKey(key) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header must be 1..200 visible ASCII characters", false)
		return
	}
	var in nodeproto.ClaimRequest
	if !q.decode(&in) {
		return
	}
	if !domain.ValidID(in.WorkerID) {
		invalid(q, "worker_id must be a UUID")
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	res, err := s.store.ClaimAttempt(ctx, q.node.ID, in.WorkerID, key)
	if err != nil {
		s.nodeError(q, err)
		return
	}
	if res.Response == nil {
		q.w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(q.w, http.StatusOK, res.Response)
}

// --- attempt routes ----------------------------------------------------------

func (q *request) owner(worker string, epoch int64) (postgres.Ownership, bool) {
	if !domain.ValidID(worker) || epoch < 1 {
		invalid(q, "worker_id must be a UUID and lease_epoch >= 1")
		return postgres.Ownership{}, false
	}
	return postgres.Ownership{AttemptID: q.params[0], NodeID: q.node.ID, WorkerID: worker, Epoch: epoch}, true
}

func (s *Server) renewAttempt(q *request) {
	var in nodeproto.AttemptHeartbeatRequest
	if !q.decode(&in) {
		return
	}
	o, ok := q.owner(in.WorkerID, in.LeaseEpoch)
	if !ok {
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	out, err := s.store.RenewAttempt(ctx, q.node.ID, o)
	if err != nil {
		s.nodeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, out)
}

func (s *Server) attemptEvent(q *request) {
	var in struct {
		WorkerID      string          `json:"worker_id"`
		LeaseEpoch    int64           `json:"lease_epoch"`
		ClientEventID string          `json:"client_event_id"`
		Kind          string          `json:"kind"`
		Payload       json.RawMessage `json:"payload"`
	}
	if !q.decode(&in) {
		return
	}
	o, ok := q.owner(in.WorkerID, in.LeaseEpoch)
	if !ok {
		return
	}
	if !domain.ValidID(in.ClientEventID) || len(in.Payload) == 0 || in.Payload[0] != '{' {
		invalid(q, "client_event_id must be a UUID and payload an object")
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	out, err := s.store.RecordAttemptEvent(ctx, q.node.ID, o, postgres.AttemptEvent{ClientEventID: in.ClientEventID, Kind: in.Kind, Payload: in.Payload})
	if err != nil {
		s.nodeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, out)
}

var sha256Header = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Server) putArtifact(q *request) {
	if s.artifacts == nil {
		writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "artifact storage is not configured", true)
		return
	}
	h := q.r.Header
	epoch, err := strconv.ParseInt(h.Get(nodeproto.HeaderLeaseEpoch), 10, 64)
	if err != nil {
		epoch = 0
	}
	o, ok := q.owner(h.Get(nodeproto.HeaderWorkerID), epoch)
	if !ok {
		return
	}
	role := h.Get(nodeproto.HeaderArtifactRole)
	sha := h.Get(nodeproto.HeaderContentSHA256)
	mt, _, mtErr := mime.ParseMediaType(h.Get("Content-Type"))
	if !nodeproto.ValidArtifactRole(role) || !sha256Header.MatchString(sha) || mtErr != nil {
		invalid(q, "artifact role, X-Content-SHA256 or Content-Type is invalid")
		return
	}
	if q.r.ContentLength < 0 {
		writeError(q.w, q.r, http.StatusLengthRequired, "LENGTH_REQUIRED", "Content-Length is required", false)
		return
	}
	if q.r.ContentLength > nodeproto.MaxArtifactBytes {
		writeError(q.w, q.r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "artifact is too large", false)
		return
	}
	size := q.r.ContentLength
	client := q.params[1]
	ctx, cancel := context.WithTimeout(q.r.Context(), storeTimeout)
	err = s.store.CheckArtifactLease(ctx, q.node.ID, o, client)
	cancel()
	if err != nil {
		s.nodeError(q, err)
		return
	}
	key, err := s.artifacts.Put(http.MaxBytesReader(q.w, q.r.Body, size), sha, size)
	if err != nil {
		switch {
		case errors.Is(err, artifactstore.ErrIntegrity):
			writeError(q.w, q.r, http.StatusUnprocessableEntity, nodeproto.CodeIntegrityMismatch, "artifact size or sha256 does not match", false)
		case errors.Is(err, artifactstore.ErrTooLarge):
			writeError(q.w, q.r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "artifact is too large", false)
		default:
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(q.w, q.r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "artifact is too large", false)
			} else {
				s.log.Warn("artifact store write failed", "request_id", requestID(q.r))
				writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "artifact storage temporarily unavailable", true)
			}
		}
		return
	}
	ctx, cancel = q.ctx()
	defer cancel()
	out, err := s.store.FinalizeArtifact(ctx, q.node.ID, o, postgres.ArtifactUpload{ClientArtifactID: client, Role: role, MediaType: strings.ToLower(mt), SHA256: sha, StorageKey: key, SizeBytes: size})
	if err != nil {
		s.nodeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusCreated, out)
}

func (s *Server) completeAttempt(q *request) {
	var in nodeproto.CompleteRequest
	if !q.decode(&in) {
		return
	}
	o, ok := q.owner(in.WorkerID, in.LeaseEpoch)
	if !ok {
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	run, err := s.store.CompleteAttempt(ctx, q.node.ID, o, postgres.Completion{Outcome: in.Outcome, Cleanup: in.CleanupState, ResultManifestID: in.ResultManifestID, Reason: in.Reason})
	if err != nil {
		s.nodeError(q, err)
		return
	}
	s.kickPublisher()
	writeJSON(q.w, http.StatusAccepted, ackOf(run))
}

func (s *Server) cleanupAttempt(q *request) {
	var in nodeproto.CleanupRequest
	if !q.decode(&in) {
		return
	}
	o, ok := q.owner(in.WorkerID, in.LeaseEpoch)
	if !ok {
		return
	}
	if in.CleanupState != nodeproto.CleanupConfirmed || !nodeproto.ValidReason(in.Method) || in.VerifiedAt.IsZero() {
		invalid(q, "only confirmed cleanup with a method and verified_at is accepted")
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	run, err := s.store.ConfirmCleanupNode(ctx, q.node.ID, o)
	if err != nil {
		s.nodeError(q, err)
		return
	}
	s.kickPublisher()
	writeJSON(q.w, http.StatusAccepted, ackOf(run))
}

// sweepLoop expires lapsed attempt leases until ctx ends.
func (s *Server) sweepLoop(ctx context.Context) {
	t := time.NewTicker(s.sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		sctx, cancel := context.WithTimeout(ctx, storeTimeout)
		n, err := s.store.SweepExpiredAttempts(sctx)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.log.Warn("lease sweep failed", "error_class", errorClass(err))
		} else if n > 0 {
			s.log.Info("lease sweep", "expired_attempts", n)
		}
	}
}

// scaleLoop is the controller reconcile loop: it materializes due schedule
// occurrences and runs the pool autoscaler until ctx ends.
// Decisions and commands are in the database; the log lines are diagnostic
// only. Several controllers may run it (row locks and unique keys).
func (s *Server) scaleLoop(ctx context.Context) {
	t := time.NewTicker(s.scaleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// Due schedule occurrences are enqueued first so the autoscaler sees
		// their runs in this pass. Decisions are in the database.
		sctx, cancel := context.WithTimeout(ctx, storeTimeout)
		decisions, err := s.store.MaterializeSchedules(sctx)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.log.Warn("schedule materialization failed", "error_class", errorClass(err))
		}
		for _, d := range decisions {
			s.log.Info("schedule occurrence", "schedule_id", d.ScheduleID, "scheduled_for", d.ScheduledFor, "state", d.State, "reason", d.Reason, "run_id", d.RunID)
		}
		sctx, cancel = context.WithTimeout(ctx, storeTimeout)
		rs, err := s.store.ReconcilePools(sctx)
		cancel()
		if err != nil && ctx.Err() == nil {
			s.log.Warn("pool reconcile failed", "error_class", errorClass(err))
		}
		for _, r := range rs {
			if len(r.Actions) == 0 {
				continue
			}
			counts := map[string]int{}
			for _, a := range r.Actions {
				counts[a.Kind]++
			}
			s.log.Info("pool scaled", "pool_id", r.PoolID, "epoch", r.Status.Epoch, "capacity_state", r.Status.CapacityState,
				"start_worker", counts[nodeproto.CommandStartWorker], "drain_worker", counts[nodeproto.CommandDrainWorker], "stop_worker", counts[nodeproto.CommandStopWorker])
		}
	}
}

func errorClass(err error) string {
	for _, e := range []error{domain.ErrConflict, domain.ErrInvalid, domain.ErrNotFound, domain.ErrUnavailable, context.DeadlineExceeded, context.Canceled} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "unknown"
}
