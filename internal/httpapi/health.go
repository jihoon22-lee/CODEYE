package httpapi

import (
	"context"
	"net/http"
)

func (s *Server) health(q *request) {
	writeJSON(q.w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(q *request) {
	ctx, cancel := context.WithTimeout(q.r.Context(), readyTimeout)
	defer cancel()
	st, err := s.store.SchemaStatus(ctx)
	reason := ""
	switch {
	case err != nil:
		reason = "database_unavailable"
	case st.State == "current":
		writeJSON(q.w, http.StatusOK, map[string]any{"status": "ready", "schema_version": st.Applied})
		return
	case st.State == "outdated":
		reason = "schema_outdated"
	case st.State == "newer":
		reason = "schema_newer"
	default:
		reason = "schema_inconsistent"
	}
	writeJSON(q.w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": reason})
}
