package httpapi

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

type testOpenCodeConnection struct {
	mu       sync.Mutex
	settings OpenCodeSettings
}

func (m *testOpenCodeConnection) Get() (OpenCodeSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	view := m.settings
	view.Editable = true
	return view, nil
}

func (m *testOpenCodeConnection) Save(fields OpenCodeFields) (OpenCodeSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	probe := "not_probed"
	m.settings = OpenCodeSettings{OpenCodeFields: fields, Editable: true, Configured: true, EffectiveModel: &fields.Model, Probe: &probe}
	return m.settings, nil
}

func (m *testOpenCodeConnection) Probe(ctx context.Context) (OpenCodeSettings, error) {
	if err := ctx.Err(); err != nil {
		return OpenCodeSettings{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	probe := "failed"
	m.settings.Probe = &probe
	view := m.settings
	view.Reason = "model_auth_failed"
	return view, nil
}

func TestAdminOpenCodeConnection(t *testing.T) {
	const path = "/api/v1/admin/opencode"
	e := newEnv(t)
	e.inst = &Instance{StateDir: filepath.Join(t.TempDir(), "state"), OpenCode: &testOpenCodeConnection{}}
	e.start(t.TempDir())
	admin := e.login(e.newAdminAccount("model-admin"))
	member := e.newUser()
	fields := OpenCodeFields{Executable: "~/bin/opencode", ConfigFile: "/service/opencode.jsonc", CredentialFile: "/service/private/auth.json", Model: "example/model"}

	initial := e.do("GET", path, nil, admin.o()).want(t, 200, "")
	if initial.body["editable"] != true || initial.body["configured"] != false || initial.body["effective_model"] != nil || initial.body["probe"] != nil {
		t.Fatalf("unexpected initial connection: %s", initial.raw)
	}
	e.do("GET", path, nil, member.o()).want(t, 403, "FORBIDDEN")
	e.do("PUT", path, fields, member.o()).want(t, 403, "FORBIDDEN")
	e.do("POST", path+"/probe", nil, member.o()).want(t, 403, "FORBIDDEN")
	missingCSRF := admin.o()
	missingCSRF.csrf = ""
	e.do("POST", path+"/probe", nil, missingCSRF).want(t, 403, "CSRF_REJECTED")
	e.do("PUT", path, map[string]any{"model": "example/model"}, admin.o()).want(t, 400, "INVALID_REQUEST")
	e.do("PUT", path, map[string]any{"model": "example/model", "executable": "", "config_file": "", "credential_file": "", "api_key": "must-never-be-echoed"}, admin.o()).want(t, 400, "INVALID_REQUEST")
	badPath := fields
	badPath.ConfigFile = "relative/config.json"
	e.do("PUT", path, badPath, admin.o()).want(t, 422, "VALIDATION_FAILED")
	e.do("POST", path+"/probe", `{"model":"unsaved/override"}`, admin.o()).want(t, 400, "INVALID_REQUEST")

	saved := e.do("PUT", path, fields, admin.o()).want(t, 200, "")
	for _, r := range []resp{saved, e.do("GET", path, nil, admin.o()).want(t, 200, "")} {
		if r.body["effective_model"] != fields.Model || r.body["credential_file"] != fields.CredentialFile || r.body["probe"] != "not_probed" || r.body["configured"] != true {
			t.Fatalf("expected saved, unprobed settings: %s", r.raw)
		}
	}
	adminSetup := e.do("GET", "/api/v1/setup", nil, admin.o()).want(t, 200, "")
	if model := adminSetup.body["model"].(map[string]any); model["id"] != fields.Model || model["probe"] != "not_probed" {
		t.Fatalf("expected live model metadata in setup: %s", adminSetup.raw)
	}
	memberSetup := e.do("GET", "/api/v1/setup", nil, member.o()).want(t, 200, "")
	if model := memberSetup.body["model"].(map[string]any); model["configured"] != true || model["id"] != nil || model["probe"] != nil || strings.Contains(string(memberSetup.raw), fields.CredentialFile) {
		t.Fatalf("unexpected model metadata in member setup: %s", memberSetup.raw)
	}
	probed := e.do("POST", path+"/probe", nil, admin.o()).want(t, 200, "")
	if probed.body["effective_model"] != fields.Model || probed.body["probe"] != "failed" || probed.body["reason"] != "model_auth_failed" {
		t.Fatalf("probe did not report the saved model's outcome: %s", probed.raw)
	}
	ctx := context.Background()
	if err := e.store.RecordLocalSettingsOperation(ctx, postgres.UserPrincipal(member.id), "opencode", "probe"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin store authorization = %v", err)
	}
	if err := e.store.RecordLocalSettingsOperation(ctx, postgres.UserPrincipal(admin.id), "opencode", "unknown"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("unknown operation = %v", err)
	}
	var events []string
	if err := e.pool.QueryRow(ctx, `SELECT array_agg(action || ':' || detail::text ORDER BY id) FROM audit_events WHERE target_type='service' AND target_id='opencode'`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != `service.settings_set:{"kind": "save"}` || events[1] != `service.settings_set:{"kind": "probe"}` {
		t.Fatalf("unexpected OpenCode audit detail: %v", events)
	}
	if strings.Contains(e.logs.String(), fields.CredentialFile) || strings.Contains(e.logs.String(), "must-never-be-echoed") {
		t.Fatal("request values reached server logs")
	}
}

func TestAdminOpenCodeWithoutManagedInstance(t *testing.T) {
	e := newEnv(t)
	admin := e.login(e.newAdminAccount("model-admin"))
	const path = "/api/v1/admin/opencode"
	r := e.do("GET", path, nil, admin.o()).want(t, 200, "")
	if r.body["editable"] != false || r.body["configured"] != false || r.body["effective_model"] != nil || r.body["probe"] != nil {
		t.Fatalf("unexpected read-only settings: %s", r.raw)
	}
	e.do("PUT", path, OpenCodeFields{}, admin.o()).want(t, 409, "CONFLICT")
	e.do("POST", path+"/probe", nil, admin.o()).want(t, 409, "CONFLICT")
}
