package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

type RuntimePathSettings struct {
	Editable       bool              `json:"editable"`
	AutoDetect     bool              `json:"auto_detect"`
	Paths          map[string]string `json:"paths"`
	AutomaticPaths map[string]string `json:"automatic_paths"`
}

type RuntimePathManager interface {
	Get() (RuntimePathSettings, error)
	Save(context.Context, map[string]string) (RuntimePathSettings, error)
}

type InvalidRuntimePath struct{ Name string }

func (e *InvalidRuntimePath) Error() string { return "invalid runtime executable" }

func (s *Server) adminRuntimePaths(q *request) {
	if !onlyQuery(q) {
		return
	}
	if s.inst == nil || s.inst.RuntimePaths == nil {
		if q.r.Method == http.MethodGet {
			writeJSON(q.w, 200, RuntimePathSettings{Paths: map[string]string{}, AutomaticPaths: map[string]string{}})
		} else {
			writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "runtime paths are managed on the service machine", false)
		}
		return
	}
	var view RuntimePathSettings
	var err error
	if q.r.Method == http.MethodGet {
		view, err = s.inst.RuntimePaths.Get()
	} else {
		raw, ok := q.readBody()
		if !ok {
			return
		}
		var in struct {
			Paths map[string]json.RawMessage `json:"paths"`
		}
		if domain.DecodeStrict(raw, &in) != nil || len(in.Paths) == 0 {
			writeError(q.w, q.r, 400, "INVALID_REQUEST", "provide runtime paths to update", false)
			return
		}
		paths := map[string]string{}
		for name, v := range in.Paths {
			var path *string
			if !slices.Contains(instance.RuntimeToolNames[:], name) || json.Unmarshal(v, &path) != nil || path == nil {
				writeError(q.w, q.r, 400, "INVALID_REQUEST", "unknown runtime or non-string path", false)
				return
			}
			if len(*path) > 4096 || strings.ContainsAny(*path, "\x00\r\n") {
				writeErrorDetails(q.w, q.r, 422, "VALIDATION_FAILED", "invalid runtime executable path", false, map[string]any{"reason": name})
				return
			}
			paths[name] = *path
		}
		ctx, cancel := q.ctx()
		err = s.store.RecordLocalSettingsOperation(ctx, q.principal(), "runtime_paths", "save")
		cancel()
		if err != nil {
			s.storeError(q, err)
			return
		}
		view, err = s.inst.RuntimePaths.Save(q.r.Context(), paths)
	}
	var invalid *InvalidRuntimePath
	switch {
	case err == nil:
		writeJSON(q.w, 200, view)
	case errors.As(err, &invalid):
		writeErrorDetails(q.w, q.r, 422, "VALIDATION_FAILED", "check this executable on the service machine", false, map[string]any{"reason": invalid.Name})
	default:
		writeError(q.w, q.r, 503, "UNAVAILABLE", "cannot access the installation configuration", true)
	}
}
