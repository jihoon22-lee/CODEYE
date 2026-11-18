package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/reportsvc"
)

// GET /runs/{id}/report?revision=N&format=json|markdown|html&include_code=true|false
//
// The stored report is canonical JSON with code (tool messages). The default
// export is code-free; include_code=true returns the full evidence to the same
// project readers. Markdown and HTML are rendered at request time from the
// stored bytes (never rebuilt from the run). ETag is the sha256 of the bytes sent.
func (s *Server) getReport(q *request) {
	query := q.r.URL.Query()
	for k := range query {
		if k != "revision" && k != "format" && k != "include_code" {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "unknown query parameter", false)
			return
		}
	}
	revision := 0
	if v := query.Get("revision"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1_000_000 {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "revision must be a positive integer", false)
			return
		}
		revision = n
	}
	format := query.Get("format")
	if format == "" {
		format = report.FormatJSON
	}
	if format != report.FormatJSON && format != report.FormatMarkdown && format != report.FormatHTML {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "format must be json, markdown or html", false)
		return
	}
	includeCode := false
	switch query.Get("include_code") {
	case "", "false":
	case "true":
		includeCode = true
	default:
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "include_code must be true or false", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	rec, err := s.store.GetReport(ctx, q.session.UserID, q.params[0], revision)
	if err != nil {
		s.storeError(q, err)
		return
	}
	stored, err := report.Unmarshal(rec.Body)
	if err != nil || domain.Digest(rec.Body) != rec.BodySHA256 {
		s.log.Error("stored report failed validation", "report_id", rec.ID)
		writeError(q.w, q.r, http.StatusInternalServerError, "REPORT_INVALID", "the stored report failed validation", false)
		return
	}
	doc := report.Project(stored, includeCode)
	body := rec.Body
	if !includeCode {
		if body, err = report.Marshal(doc); err != nil {
			writeError(q.w, q.r, http.StatusInternalServerError, "REPORT_INVALID", "the report export failed validation", false)
			return
		}
	}
	// The digest printed in every format is that of the JSON document of the
	// same projection, so a reader can match Markdown/HTML to the JSON export.
	digest := report.Digest(body)
	h := q.w.Header()
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-CODEYE-Report-Revision", strconv.Itoa(rec.Revision))
	h.Set("X-CODEYE-Report-SHA256", digest)
	h.Set("X-CODEYE-Include-Code", strconv.FormatBool(includeCode))
	name := "codeye-report-" + rec.RunID + "-r" + strconv.Itoa(rec.Revision)
	if !includeCode {
		name += "-nocode"
	}
	switch format {
	case report.FormatJSON:
		h.Set("Content-Type", "application/json; charset=utf-8")
	case report.FormatMarkdown:
		body = report.RenderMarkdown(doc, digest, report.Korean)
		h.Set("Content-Type", "text/markdown; charset=utf-8")
		h.Set("Content-Disposition", `attachment; filename="`+name+`.md"`)
	case report.FormatHTML:
		if body, err = report.RenderHTML(doc, digest, report.Korean); err != nil {
			writeError(q.w, q.r, http.StatusInternalServerError, "REPORT_INVALID", "the report could not be rendered", false)
			return
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", report.HTMLContentSecurityPolicy)
		h.Set("Content-Disposition", `attachment; filename="`+name+`.html"`)
	}
	h.Set("ETag", `"`+report.Digest(body)+`"`)
	q.w.WriteHeader(http.StatusOK)
	_, _ = q.w.Write(body)
}

type reportRevisionView struct {
	ID             string    `json:"id"`
	Revision       int       `json:"revision"`
	Status         string    `json:"status"`
	QualityVerdict string    `json:"quality_verdict"`
	SHA256         string    `json:"sha256"`
	SizeBytes      int64     `json:"size_bytes"`
	CreatedAt      time.Time `json:"created_at"`
}

// GET /runs/{id}/reports lists published revisions and export formats.
func (s *Server) listReports(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	list, err := s.store.ListReports(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	items := make([]reportRevisionView, 0, len(list))
	for _, r := range list {
		items = append(items, reportRevisionView{r.ID, r.Revision, r.Status, r.QualityVerdict, r.BodySHA256, r.SizeBytes, r.CreatedAt})
	}
	var latest *int
	if len(items) > 0 {
		n := items[len(items)-1].Revision
		latest = &n
	}
	// W23: the run the comparison view uses by default (same project, source
	// variant, profile and scope; an earlier fp2 report), or null.
	ref, err := s.store.DefaultBaseline(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	var baseline any
	if ref != nil {
		baseline = map[string]any{"run_id": ref.RunID, "revision": ref.Revision, "url": apiPrefix + "/runs/" + q.params[0] + "/comparison"}
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items, "latest_revision": latest, "formats": report.Formats, "default_include_code": false, "default_baseline": baseline})
}

// publishLoop reconciles pending reports at start and then on every tick or
// kick (after an accepted attempt completion) until ctx ends.
func (s *Server) publishLoop(ctx context.Context) {
	t := time.NewTicker(s.publishEvery)
	defer t.Stop()
	for {
		pctx, cancel := context.WithTimeout(ctx, 2*storeTimeout)
		n, err := s.publisher.Reconcile(pctx)
		cancel()
		if err != nil && ctx.Err() == nil && !errors.Is(err, reportsvc.ErrTransient) {
			s.log.Warn("report reconcile failed", "error_class", errorClass(err))
		} else if n > 0 {
			s.log.Info("report reconcile", "published", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.publishKick:
		}
	}
}

// kickPublisher asks the publish loop for an early pass (non-blocking).
func (s *Server) kickPublisher() {
	if s.publishKick == nil {
		return
	}
	select {
	case s.publishKick <- struct{}{}:
	default:
	}
}
