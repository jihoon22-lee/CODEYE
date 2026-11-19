package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

type runRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type projectView struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Source         json.RawMessage `json:"source"`
	Revision       int64           `json:"revision"`
	SourceStatus   string          `json:"source_status"`
	CapabilityGaps []string        `json:"capability_gaps"`
	LastRun        *runRef         `json:"last_run"`
	Activity       activityView    `json:"activity"`
}

func viewOf(p postgres.ProjectSummary, gaps []string) projectView {
	gaps = append([]string{}, gaps...)
	if !p.HasReport {
		gaps = append(gaps, string(domain.GapReportUnavailable))
	}
	v := projectView{ID: p.ID, Name: p.Name, Source: p.Source, Revision: p.Revision,
		SourceStatus: "declared_unverified", CapabilityGaps: gaps, Activity: activityOf(p.Activity)}
	if v.Source == nil {
		v.Source = json.RawMessage("null")
	}
	if p.LastRun != nil {
		v.LastRun = &runRef{p.LastRun.ID, string(p.LastRun.Status)}
	}
	return v
}

func (s *Server) createProject(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Name          *string         `json:"name"`
		Source        json.RawMessage `json:"source"`
		Description   *string         `json:"description"`
		AttachmentIDs []string        `json:"attachment_ids"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Source == nil || string(in.Source) == "null" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be a project object with a source", false)
		return
	}
	if len(in.AttachmentIDs) > 0 {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "ATTACHMENTS_UNSUPPORTED", "attachments are not supported yet", false)
		return
	}
	src, err := domain.ParseSource(in.Source)
	if err != nil {
		s.storeError(q, err)
		return
	}
	reg := postgres.ProjectRegistration{Source: src}
	if in.Name != nil {
		if strings.TrimSpace(*in.Name) == "" {
			writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "name must not be blank", false)
			return
		}
		reg.Name = *in.Name
	}
	if in.Description != nil {
		reg.Description = *in.Description
	}
	ctx, cancel := q.ctx()
	defer cancel()
	p, err := s.store.RegisterProject(ctx, q.session.UserID, reg)
	if err != nil {
		s.storeError(q, err)
		return
	}
	var cfg struct {
		Source json.RawMessage `json:"source"`
	}
	_ = json.Unmarshal(p.Config, &cfg)
	gaps, ok := s.executionGaps(q)
	if !ok {
		return
	}
	q.w.Header().Set("Location", apiPrefix+"/projects/"+p.ID)
	// A new project has no run: an empty activity checked now (server clock).
	created := postgres.ProjectSummary{ID: p.ID, Name: p.Name, Revision: p.Revision, Source: cfg.Source,
		Activity: postgres.Activity{CheckedAt: time.Now().UTC()}}
	writeJSON(q.w, http.StatusCreated, viewOf(created, gaps))
}

func (s *Server) listProjects(q *request) {
	query := q.r.URL.Query()
	limit := 20
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "limit must be 1..100", false)
			return
		}
		limit = n
	}
	cursor := query.Get("cursor")
	if len(cursor) > 400 {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "invalid cursor", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	page, err := s.store.ListProjects(ctx, q.session.UserID, cursor, limit)
	if err != nil {
		if errors.Is(err, domain.ErrInvalid) {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "invalid cursor", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	gaps, ok := s.executionGaps(q)
	if !ok {
		return
	}
	items := make([]projectView, 0, len(page.Items))
	for _, p := range page.Items {
		items = append(items, viewOf(p, gaps))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Server) getProject(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	p, err := s.store.GetProjectSummary(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	gaps, ok := s.executionGaps(q)
	if !ok {
		return
	}
	writeJSON(q.w, http.StatusOK, viewOf(p, gaps))
}
