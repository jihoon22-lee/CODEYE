package httpapi

import (
	"net/http"

	"github.com/jihoon22-lee/CODEYE/internal/github"
)

func (s *Server) listSourceRoots(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	roots, err := s.store.ListSourceRoots(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	type item struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	items := make([]item, 0, len(roots))
	for _, r := range roots {
		items = append(items, item{r.ID, r.Label})
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listProviders(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	provs, err := s.store.ListProviders(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	type item struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
		Host string `json:"host"`
	}
	items := make([]item, 0, len(provs))
	platform := s.inst.platform()
	for _, p := range provs {
		if !github.HostAllowed(p.Host, platform.Offline, platform.Hosts) {
			continue
		}
		items = append(items, item{p.ID, p.Kind, p.Host})
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items})
}
