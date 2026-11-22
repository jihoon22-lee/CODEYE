package httpapi

import (
	"context"
	"strings"
	"testing"
)

type testRuntimePaths struct{}

func (*testRuntimePaths) Get() (RuntimePathSettings, error) {
	return RuntimePathSettings{Editable: true, AutoDetect: true, Paths: map[string]string{}, AutomaticPaths: map[string]string{"go": "/service/bin/go"}}, nil
}
func (m *testRuntimePaths) Save(ctx context.Context, paths map[string]string) (RuntimePathSettings, error) {
	v, _ := m.Get()
	v.Paths = paths
	return v, nil
}

func TestRuntimePathsRequireAdminAndRecordNoPaths(t *testing.T) {
	e := newEnv(t)
	e.inst = &Instance{StateDir: t.TempDir(), RuntimePaths: &testRuntimePaths{}}
	e.start(t.TempDir())
	admin := e.login(e.newAdminAccount("runtime-admin"))
	member := e.newUser()
	const url = "/api/v1/admin/runtime-paths"
	patch := map[string]any{"paths": map[string]string{"go": "/service/bin/go"}}
	e.do("GET", url, nil, member.o()).want(t, 403, "FORBIDDEN")
	e.do("PATCH", url, patch, member.o()).want(t, 403, "FORBIDDEN")
	noCSRF := admin.o()
	noCSRF.csrf = ""
	e.do("PATCH", url, patch, noCSRF).want(t, 403, "CSRF_REJECTED")
	for _, raw := range []string{`{"paths":{}}`, `{"paths":{"java":null}}`, `{"paths":{"model":"/internal"}}`, `{"paths":{"go":"","go":""}}`} {
		e.do("PATCH", url, raw, admin.o()).want(t, 400, "INVALID_REQUEST")
	}
	e.do("PATCH", url, patch, admin.o()).want(t, 200, "")
	var detail string
	if err := e.pool.QueryRow(context.Background(), `SELECT detail::text FROM audit_events WHERE target_type='service' AND target_id='runtime_paths'`).Scan(&detail); err != nil || detail != `{"kind": "save"}` {
		t.Fatalf("audit %q, %v", detail, err)
	}
	if strings.Contains(e.logs.String(), "/service/bin/go") {
		t.Fatal("logged an executable path")
	}
}

func TestStandaloneRuntimeSettingsAreReadOnly(t *testing.T) {
	e := newEnv(t)
	e.start(t.TempDir())
	admin := e.login(e.newAdminAccount("standalone-admin"))
	const path = "/api/v1/admin/runtime-paths"
	res := e.do("GET", path, nil, admin.o()).want(t, 200, "")
	if res.body["editable"] != false {
		t.Fatalf("standalone editable: %s", res.raw)
	}
	e.do("PATCH", path, map[string]any{"paths": map[string]string{"go": ""}}, admin.o()).want(t, 409, "CONFLICT")
}
