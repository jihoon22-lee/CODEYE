package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/history"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// W23 run comparison and human finding reviews (docs/REPORT.md). Every route checks
// the project ACL in the store: readers (viewer included) see comparisons and
// reviews; only the owner, managers and runners set dispositions.

type baselineView struct {
	RunID           string    `json:"run_id"`
	Revision        int       `json:"revision"`
	ReportCreatedAt time.Time `json:"report_created_at"`
	Selection       string    `json:"selection"`
}

type comparisonView struct {
	RunID           string        `json:"run_id"`
	ProjectID       string        `json:"project_id"`
	Revision        int           `json:"revision"`
	ReportCreatedAt time.Time     `json:"report_created_at"`
	Baseline        *baselineView `json:"baseline"`
	history.Comparison
	history.Overlay
	CanReview bool      `json:"can_review"`
	CheckedAt time.Time `json:"checked_at"`
}

func positiveQuery(q *request, name string) (int, bool) {
	v := q.r.URL.Query().Get(name)
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 1_000_000 || strconv.Itoa(n) != v {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", name+" must be a positive integer", false)
		return 0, false
	}
	return n, true
}

func onlyQuery(q *request, allowed ...string) bool {
	for k, v := range q.r.URL.Query() {
		if !slices.Contains(allowed, k) || len(v) != 1 {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "unknown or repeated query parameter", false)
			return false
		}
	}
	return true
}

func (s *Server) side(q *request, c postgres.ComparisonSide) (history.Side, bool) {
	r, err := report.Unmarshal(c.Body)
	if err != nil || domain.Digest(c.Body) != c.BodySHA256 {
		s.log.Error("stored report failed validation", "run_id", c.RunID, "revision", c.Revision)
		writeError(q.w, q.r, http.StatusInternalServerError, "REPORT_INVALID", "the stored report failed validation", false)
		return history.Side{}, false
	}
	return history.Side{RunID: c.RunID, Revision: c.Revision, CreatedAt: c.CreatedAt, Profile: c.Profile, Scope: c.Scope, Source: c.Source, Report: r}, true
}

// GET /runs/{id}/comparison?revision=N&baseline=<run_id>&baseline_revision=M
func (s *Server) getComparison(q *request) {
	if !onlyQuery(q, "revision", "baseline", "baseline_revision") {
		return
	}
	revision, ok := positiveQuery(q, "revision")
	if !ok {
		return
	}
	baseRevision, ok := positiveQuery(q, "baseline_revision")
	if !ok {
		return
	}
	baseline := q.r.URL.Query().Get("baseline")
	if (baseline != "" && !domain.ValidID(baseline)) || (baseline == "" && baseRevision != 0) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "baseline must be a run ID; baseline_revision needs baseline", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	d, err := s.store.LoadComparison(ctx, q.session.UserID, q.params[0], revision, baseline, baseRevision)
	if err != nil {
		s.storeError(q, err)
		return
	}
	cur, ok := s.side(q, d.Current)
	if !ok {
		return
	}
	v := comparisonView{RunID: d.Current.RunID, ProjectID: d.ProjectID, Revision: d.Current.Revision, ReportCreatedAt: d.Current.CreatedAt, CanReview: d.CanReview, CheckedAt: d.Now}
	if d.Baseline == nil {
		v.Comparison = history.NoBaseline()
	} else {
		base, ok := s.side(q, *d.Baseline)
		if !ok {
			return
		}
		v.Baseline = &baselineView{RunID: base.RunID, Revision: base.Revision, ReportCreatedAt: base.CreatedAt, Selection: d.Selection}
		v.Comparison = history.Compare(cur, base)
	}
	reviews := map[string]history.Review{}
	for _, r := range d.Reviews {
		reviews[r.Fingerprint] = reviewOf(r)
	}
	v.Overlay = history.ApplyReviews(&v.Comparison, cur, reviews, d.Now)
	writeJSON(q.w, http.StatusOK, v)
}

func reviewOf(r postgres.FindingReview) history.Review {
	return history.Review{Fingerprint: r.Fingerprint, Version: r.Version, Disposition: r.Disposition, Reason: r.Reason, PathPrefix: r.PathPrefix, ExpiresAt: r.ExpiresAt,
		ActorID: r.ActorID, CreatedAt: r.CreatedAt, RunID: r.RunID, ReportRevision: r.ReportRevision, FindingID: r.FindingID}
}

type findingReviewView struct {
	ID             string     `json:"id"`
	ProjectID      string     `json:"project_id"`
	Fingerprint    string     `json:"fingerprint"`
	Version        int64      `json:"version"`
	Disposition    string     `json:"disposition"`
	Reason         string     `json:"reason"`
	PathPrefix     *string    `json:"path_prefix"`
	ExpiresAt      *time.Time `json:"expires_at"`
	ActorID        string     `json:"actor_id"`
	RunID          string     `json:"run_id"`
	ReportRevision int        `json:"report_revision"`
	FindingID      string     `json:"finding_id"`
	CreatedAt      time.Time  `json:"created_at"`
}

func findingReviewViewOf(r postgres.FindingReview) findingReviewView {
	return findingReviewView{r.ID, r.ProjectID, r.Fingerprint, r.Version, r.Disposition, r.Reason, r.PathPrefix, r.ExpiresAt, r.ActorID, r.RunID, r.ReportRevision, r.FindingID, r.CreatedAt}
}

// GET /projects/{id}/finding-reviews?fingerprint=…|cursor=…&limit=N
func (s *Server) listFindingReviews(q *request) {
	if !onlyQuery(q, "fingerprint", "cursor", "limit") {
		return
	}
	query := q.r.URL.Query()
	limit := 50
	if query.Has("limit") {
		n, ok := positiveQuery(q, "limit")
		if !ok {
			return
		}
		if n > 200 {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "limit must be 1..200", false)
			return
		}
		limit = n
	}
	fp, cursor := query.Get("fingerprint"), query.Get("cursor")
	if (fp != "" && !report.ValidFingerprint(fp)) || (cursor != "" && !report.ValidFingerprint(cursor)) || (fp != "" && cursor != "") {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "fingerprint and cursor must be fp2 fingerprints and are exclusive", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	page, err := s.store.ListFindingReviews(ctx, q.session.UserID, q.params[0], fp, cursor, limit)
	if err != nil {
		s.storeError(q, err)
		return
	}
	items := make([]findingReviewView, 0, len(page.Items))
	for _, r := range page.Items {
		items = append(items, findingReviewViewOf(r))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items, "next_cursor": next, "can_review": page.CanReview})
}

// POST /projects/{id}/finding-reviews appends a review version. The
// precondition is If-Match "N" (the fingerprint's current version) or
// If-None-Match: * (no review yet); Idempotency-Key is required.
func (s *Server) setFindingReview(q *request) {
	key := q.r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header must be 1..200 visible ASCII characters", false)
		return
	}
	var expected int64
	none := strings.TrimSpace(q.r.Header.Get("If-None-Match"))
	switch {
	case q.r.Header.Get("If-Match") != "" && none == "":
		n, ok := ifMatch(q.r)
		if !ok {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "If-Match must be the review version", false)
			return
		}
		expected = n
	case none == "*" && q.r.Header.Get("If-Match") == "":
	default:
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `If-Match: "N" (current review version) or If-None-Match: * (first review) is required`, false)
		return
	}
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Fingerprint string     `json:"fingerprint"`
		Disposition string     `json:"disposition"`
		Reason      string     `json:"reason"`
		ExpiresAt   *time.Time `json:"expires_at"`
		PathPrefix  *string    `json:"path_prefix"`
		RunID       string     `json:"run_id"`
		Revision    int        `json:"revision"`
		FindingID   string     `json:"finding_id"`
	}
	canonical, err := domain.CanonicalJSON(body)
	if err != nil || domain.DecodeStrict(body, &in) != nil || in.Fingerprint == "" || in.Disposition == "" || in.RunID == "" || in.FindingID == "" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be a finding review object", false)
		return
	}
	if in.ExpiresAt != nil {
		t := in.ExpiresAt.UTC()
		in.ExpiresAt = &t
	}
	verify := func(stored []byte) error {
		r, err := report.Unmarshal(stored)
		if err != nil {
			return err
		}
		for _, f := range r.Findings {
			if f.ID == in.FindingID && f.Fingerprint == in.Fingerprint && f.FingerprintBasis != nil {
				return nil
			}
		}
		return domain.ErrInvalid
	}
	ctx, cancel := q.ctx()
	defer cancel()
	rec, created, err := s.store.SetFindingReview(ctx, q.session.UserID, q.params[0], postgres.ReviewRequest{Fingerprint: in.Fingerprint, Disposition: in.Disposition, Reason: in.Reason,
		PathPrefix: in.PathPrefix, ExpiresAt: in.ExpiresAt, RunID: in.RunID, ReportRevision: in.Revision, FindingID: in.FindingID, Expected: expected, Key: key,
		Digest: domain.Digest(append(append(canonical, 0), strconv.FormatInt(expected, 10)...)), Verify: verify})
	if err != nil {
		if errors.Is(err, postgres.ErrIdempotencyConflict) {
			writeError(q.w, q.r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key was already used with a different request", false)
			return
		}
		if errors.Is(err, domain.ErrConflict) {
			writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "the finding review version changed; reload and retry", false)
			return
		}
		s.storeError(q, err)
		return
	}
	q.w.Header().Set("ETag", etag(rec.Version))
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(q.w, status, findingReviewViewOf(rec))
}
