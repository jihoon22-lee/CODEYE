package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

type runRef struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type projectView struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Source json.RawMessage `json:"source"`
	// Analysis are the project's analysis choices (defaults when none were
	// recorded); the agent `codeye up` starts follows them.
	Analysis       domain.AnalysisChoices `json:"analysis"`
	Revision       int64                  `json:"revision"`
	SourceStatus   string                 `json:"source_status"`
	CapabilityGaps []string               `json:"capability_gaps"`
	LastRun        *runRef                `json:"last_run"`
	Activity       activityView           `json:"activity"`
	// Role is the caller's role (owner, manager, runner, viewer); CanRun and
	// CanManage follow from it. The API enforces them on every request.
	Role      string `json:"role"`
	CanRun    bool   `json:"can_run"`
	CanManage bool   `json:"can_manage"`
}

func viewOf(p postgres.ProjectSummary, gaps []string) projectView {
	gaps = append([]string{}, gaps...)
	if !p.HasReport {
		gaps = append(gaps, string(domain.GapReportUnavailable))
	}
	v := projectView{ID: p.ID, Name: p.Name, Source: p.Source, Analysis: p.Analysis, Revision: p.Revision,
		SourceStatus: "declared_unverified", CapabilityGaps: gaps, Activity: activityOf(p.Activity), Role: p.Role}
	switch p.Role {
	case postgres.RoleOwner, string(domain.RoleManager):
		v.CanRun, v.CanManage = true, true
	case string(domain.RoleRunner):
		v.CanRun = true
	}
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
		LocalPath     *string         `json:"local_path"`
		Analysis      json.RawMessage `json:"analysis"`
		Description   *string         `json:"description"`
		AttachmentIDs []string        `json:"attachment_ids"`
		// OpenExisting: the same folder or repository again answers the
		// caller's existing project (200) instead of a new one. Default true
		// for local_path, false for source.
		OpenExisting *bool `json:"open_existing"`
	}
	// Exactly one of source and local_path (an absolute folder on this server).
	if domain.DecodeStrict(body, &in) != nil || (in.Source != nil && string(in.Source) != "null") == (in.LocalPath != nil) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be a project object with a source or a local_path", false)
		return
	}
	if len(in.AttachmentIDs) > 0 {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "ATTACHMENTS_UNSUPPORTED", "attachments are not supported yet", false)
		return
	}
	var sourceName string
	var src domain.Source
	var err error
	if in.LocalPath != nil {
		real, err := s.inst.resolveFolder(*in.LocalPath)
		if err != nil {
			s.folderError(q, err)
			return
		}
		sourceName = filepath.Base(real)
		ctx, cancel := q.ctx()
		src, err = s.store.LocalSourceForPath(ctx, real)
		cancel()
		if err != nil {
			s.storeError(q, err)
			return
		}
	} else if src, err = domain.ParseSource(in.Source); err != nil {
		s.storeError(q, err)
		return
	} else if src.Kind == domain.SourceLocal {
		if err := s.checkLocalSource(q, src); err != nil {
			s.folderError(q, err)
			return
		}
	} else {
		host, _ := domain.RepositoryHost(src.RepositoryURL)
		platform := s.inst.platform()
		if !github.HostAllowed(host, platform.Offline, platform.Hosts) {
			writeError(q.w, q.r, http.StatusUnprocessableEntity, "SOURCE_UNSUPPORTED", "offline mode allows only configured internal Git hosts", false)
			return
		}
	}
	reg := postgres.ProjectRegistration{Source: src, Name: sourceName}
	if in.Analysis != nil && string(in.Analysis) != "null" {
		choices, err := domain.ParseAnalysisChoices(in.Analysis)
		if err != nil {
			writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "analysis needs the booleans send_source_to_model and run_build_scripts (fetch_dependencies and comment_on_pull_request are optional)", false)
			return
		}
		reg.Analysis = &choices
	}
	// The same folder or repository again opens the caller's project.
	reuse := in.LocalPath != nil
	if in.OpenExisting != nil {
		reuse = *in.OpenExisting
	}
	if p, found, ok := s.existingProject(q, src, reuse); !ok {
		return
	} else if found {
		gaps, ok := s.executionGaps(q)
		if !ok {
			return
		}
		q.w.Header().Set("Location", apiPrefix+"/projects/"+p.ID)
		writeJSON(q.w, http.StatusOK, viewOf(p, gaps))
		return
	}
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
	created := postgres.ProjectSummary{ID: p.ID, Name: p.Name, Revision: p.Revision, Source: cfg.Source, Role: postgres.RoleOwner,
		Analysis: domain.DefaultAnalysisChoices(src.Kind), Activity: postgres.Activity{CheckedAt: time.Now().UTC()}}
	if reg.Analysis != nil {
		created.Analysis = *reg.Analysis
	}
	writeJSON(q.w, http.StatusCreated, viewOf(created, gaps))
}

// existingProject finds a project the caller owns whose current source
// is src. ok is false when the error response was written.
func (s *Server) existingProject(q *request, src domain.Source, reuse bool) (postgres.ProjectSummary, bool, bool) {
	if !reuse {
		return postgres.ProjectSummary{}, false, true
	}
	ctx, cancel := q.ctx()
	defer cancel()
	for cursor := ""; ; {
		page, err := s.store.ListProjects(ctx, q.session.UserID, cursor, 100)
		if err != nil {
			s.storeError(q, err)
			return postgres.ProjectSummary{}, false, false
		}
		for _, p := range page.Items {
			if p.Role == postgres.RoleOwner && sameSource(p.Source, src) {
				return p, true, true
			}
		}
		if page.NextCursor == "" {
			return postgres.ProjectSummary{}, false, true
		}
		cursor = page.NextCursor
	}
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

// putProjectAnalysis replaces the project's analysis choices (owner or
// manager) and answers the project.
func (s *Server) putProjectAnalysis(q *request) {
	raw, ok := q.readBody()
	if !ok {
		return
	}
	c, err := domain.ParseAnalysisChoices(raw)
	if err != nil {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "analysis needs the booleans send_source_to_model and run_build_scripts (fetch_dependencies and comment_on_pull_request are optional)", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	if _, err := s.store.SetProjectAnalysis(ctx, q.session.UserID, q.params[0], c); err != nil {
		s.storeError(q, err)
		return
	}
	s.getProject(q)
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
