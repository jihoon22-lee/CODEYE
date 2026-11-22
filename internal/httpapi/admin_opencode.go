package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// OpenCodeFields are paths on the service machine and an optional model override.
type OpenCodeFields struct {
	Executable     string `json:"executable"`
	ConfigFile     string `json:"config_file"`
	CredentialFile string `json:"credential_file"`
	Model          string `json:"model"`
}

type OpenCodeSettings struct {
	OpenCodeFields
	Editable       bool    `json:"editable"`
	Configured     bool    `json:"configured"`
	EffectiveModel *string `json:"effective_model"`
	Probe          *string `json:"probe"`
	Reason         string  `json:"reason,omitempty"`
}

// OpenCodeConnection is present only on a controller managing its local installation.
type OpenCodeConnection interface {
	Get() (OpenCodeSettings, error)
	Save(OpenCodeFields) (OpenCodeSettings, error)
	Probe(context.Context) (OpenCodeSettings, error)
}

var ErrOpenCodeValidation = errors.New("invalid OpenCode connection")
var ErrOpenCodeBusy = errors.New("OpenCode connection is busy")

func (s *Server) adminGetOpenCode(q *request) {
	if !onlyQuery(q) {
		return
	}
	if s.inst != nil && s.inst.OpenCode != nil {
		settings, err := s.inst.OpenCode.Get()
		s.writeOpenCode(q, settings, err)
		return
	}
	var view OpenCodeSettings
	if st := s.inst.readSetup(); st != nil && st.Model != nil {
		view.Configured, view.EffectiveModel, view.Probe = true, &st.Model.ID, &st.Model.Probe
	}
	writeJSON(q.w, http.StatusOK, view)
}

func (s *Server) adminPutOpenCode(q *request) {
	if !onlyQuery(q) {
		return
	}
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Executable     *string `json:"executable"`
		ConfigFile     *string `json:"config_file"`
		CredentialFile *string `json:"credential_file"`
		Model          *string `json:"model"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Executable == nil || in.ConfigFile == nil || in.CredentialFile == nil || in.Model == nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "provide executable, config_file, credential_file and model as strings", false)
		return
	}
	fields := OpenCodeFields{Executable: *in.Executable, ConfigFile: *in.ConfigFile, CredentialFile: *in.CredentialFile, Model: *in.Model}
	for _, value := range []string{fields.Executable, fields.ConfigFile, fields.CredentialFile} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") || (value != "" && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "~/")) {
			s.writeOpenCode(q, OpenCodeSettings{}, ErrOpenCodeValidation)
			return
		}
	}
	if !s.allowOpenCodeChange(q, "save") {
		return
	}
	settings, err := s.inst.OpenCode.Save(fields)
	s.writeOpenCode(q, settings, err)
}

func (s *Server) adminProbeOpenCode(q *request) {
	if !onlyQuery(q) {
		return
	}
	body, ok := q.readBody()
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) != 0 {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "the probe uses the saved connection and takes no input", false)
		return
	}
	if !s.allowOpenCodeChange(q, "probe") {
		return
	}
	// A local model may take longer than the general HTTP write deadline.
	// The adapter enforces its configured timeout and observes disconnection.
	_ = http.NewResponseController(q.w).SetWriteDeadline(time.Now().Add(opencode.MaxTimeout + time.Minute))
	settings, err := s.inst.OpenCode.Probe(q.r.Context())
	s.writeOpenCode(q, settings, err)
}

func (s *Server) allowOpenCodeChange(q *request, operation string) bool {
	if s.inst == nil || s.inst.OpenCode == nil {
		writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "OpenCode settings are managed on the service machine", false)
		return false
	}
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RecordLocalSettingsOperation(ctx, q.principal(), "opencode", operation); err != nil {
		s.storeError(q, err)
		return false
	}
	return true
}

func (s *Server) writeOpenCode(q *request, settings OpenCodeSettings, err error) {
	switch {
	case err == nil:
		writeJSON(q.w, http.StatusOK, settings)
	case errors.Is(err, ErrOpenCodeValidation):
		details := map[string]any{}
		switch settings.Reason {
		case "opencode_unavailable", "model_credential_unavailable", "model_endpoint_not_configured", "model_not_configured", "opencode_config_invalid":
			details["reason"] = settings.Reason
		}
		writeErrorDetails(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "check the service paths and model name", false, details)
	case errors.Is(err, ErrOpenCodeBusy):
		writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "another OpenCode operation is in progress", true)
	default:
		writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "cannot access the installation configuration", true)
	}
}
