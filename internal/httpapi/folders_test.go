package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// A folder on the server is registered by its absolute path (mapped onto
// the longest root the user may use), with or without a state directory;
// the same folder or repository again opens its project; the data
// directories and folders outside the roots are refused with their own
// codes, and GET /setup reports the model, Git, readable folders and the
// detected tools without any key (nulls where the controller has no state
// directory).
func TestFolderPathsAndSetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux paths")
	}
	e := newEnv(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	state := filepath.Join(home, ".local", "state", "codeye")
	for _, d := range []string{filepath.Join(home, "src", "app"), filepath.Join(home, "src", "a:b"), filepath.Join(state, "run"), filepath.Join(base, "elsewhere"), filepath.Join(home, "work", "svc")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	u := e.newUser()
	rootHome, err := e.store.AddSourceRoot(ctx, "home", home)
	if err != nil {
		t.Fatal(err)
	}
	rootWork, err := e.store.AddSourceRoot(ctx, "work", filepath.Join(home, "work"))
	if err != nil {
		t.Fatal(err)
	}
	// A controller without a state directory maps paths too; it knows no
	// model or tools and cannot store GitHub tokens.
	svc := e.do("POST", "/api/v1/projects", map[string]any{"local_path": filepath.Join(home, "work", "svc")}, u.o()).want(t, 201, "")
	if s := svc.body["source"].(map[string]any); s["source_root_id"] != rootWork.ID || s["relative_path"] != "svc" {
		t.Fatalf("%s", svc.raw)
	}
	if v := e.do("GET", "/api/v1/setup", nil, u.o()).want(t, 200, ""); v.body["model"] != nil || v.body["tools"] != nil ||
		v.body["available"].(map[string]any)["local_path"].(map[string]any)["available"] != true {
		t.Fatalf("setup without state %s", v.raw)
	}

	e.inst = &Instance{StateDir: state}
	e.start(t.TempDir())
	u = e.login(u.id)
	created := e.do("POST", "/api/v1/projects", map[string]any{"local_path": filepath.Join(home, "src", "app"), "analysis": map[string]bool{"send_source_to_model": false, "run_build_scripts": true}}, u.o()).want(t, 201, "")
	src := created.body["source"].(map[string]any)
	if src["source_root_id"] != rootHome.ID || src["relative_path"] != "src/app" || created.body["name"] != "app" {
		t.Fatalf("%s", created.raw)
	}
	if a := created.body["analysis"].(map[string]any); a["send_source_to_model"] != false || a["run_build_scripts"] != true {
		t.Fatalf("analysis %s", created.raw)
	}
	// The same folder again (through a symlink, with a trailing slash) opens the project.
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(home, "src"), link); err != nil {
		t.Fatal(err)
	}
	again := e.do("POST", "/api/v1/projects", map[string]any{"local_path": link + "/app/"}, u.o()).want(t, 200, "")
	if again.body["id"] != created.body["id"] {
		t.Fatalf("not reused: %s", again.raw)
	}
	// The longest root wins; the root itself is ".".
	work := e.do("POST", "/api/v1/projects", map[string]any{"local_path": filepath.Join(home, "work")}, u.o()).want(t, 201, "")
	if s := work.body["source"].(map[string]any); s["source_root_id"] != rootWork.ID || s["relative_path"] != "." {
		t.Fatalf("%s", work.raw)
	}
	if a := work.body["analysis"].(map[string]any); a["send_source_to_model"] != true || a["run_build_scripts"] != true {
		t.Fatalf("defaults %s", work.raw)
	}
	for path, code := range map[string]string{

		filepath.Join(home, "missing"): "FOLDER_NOT_FOUND",
		"relative/path":                "FOLDER_NOT_FOUND",
		home:                           "FOLDER_HOLDS_CODEYE_DATA",
		filepath.Join(state, "run"):    "FOLDER_HOLDS_CODEYE_DATA",
	} {
		e.do("POST", "/api/v1/projects", map[string]any{"local_path": path}, u.o()).want(t, 422, code)
	}
	for _, path := range []string{filepath.Join(base, "elsewhere"), filepath.Join(home, "src", "a:b")} {
		e.do("POST", "/api/v1/projects", map[string]any{"local_path": path}, u.o()).want(t, 201, "")
	}
	e.do("POST", "/api/v1/projects", map[string]any{"local_path": filepath.Join(home, "src", "app"), "source": map[string]any{"kind": "local", "source_root_id": rootHome.ID, "relative_path": "."}}, u.o()).want(t, 400, "INVALID_REQUEST")
	// The root-and-relative-path form gets the same check: "." under $HOME holds the state directory.
	held := e.do("POST", "/api/v1/projects", map[string]any{"source": map[string]any{"kind": "local", "source_root_id": rootHome.ID, "relative_path": "."}}, u.o()).want(t, 422, "FOLDER_HOLDS_CODEYE_DATA")
	if msg := held.body["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "the folder "+home+" contains CODEYE's data directory "+state) || !strings.Contains(msg, "subfolder") {
		t.Fatal(msg)
	}
	e.do("POST", "/api/v1/projects", map[string]any{"source": map[string]any{"kind": "local", "source_root_id": rootHome.ID, "relative_path": ".local/state/codeye/run"}}, u.o()).want(t, 422, "FOLDER_HOLDS_CODEYE_DATA")
	e.do("POST", "/api/v1/projects", map[string]any{"source": map[string]any{"kind": "local", "source_root_id": rootHome.ID, "relative_path": "src"}}, u.o()).want(t, 201, "")
	e.do("POST", "/api/v1/projects", map[string]any{"local_path": filepath.Join(home, "work"), "analysis": map[string]bool{"send_source_to_model": true}}, u.o()).want(t, 422, "VALIDATION_FAILED")

	// A repository without a branch (the default branch); the same again is reused.
	prov, _, err := e.store.EnsureProvider(ctx, "github.com")
	if err != nil {
		t.Fatal(err)
	}
	gitBody := map[string]any{"source": map[string]any{"kind": "git", "provider_id": prov.ID, "repository_url": "https://github.com/o/r"}}
	g := e.do("POST", "/api/v1/projects", gitBody, u.o()).want(t, 201, "")
	if s := g.body["source"].(map[string]any); s["branch"] != nil || g.body["name"] != "r" {
		t.Fatalf("%s", g.raw)
	}
	// A repository's build scripts do not run unless the project turns that on.
	if a := g.body["analysis"].(map[string]any); a["send_source_to_model"] != true || a["run_build_scripts"] != false {
		t.Fatalf("git defaults %s", g.raw)
	}
	// Turning the build scripts on records a new revision and keeps the source.
	on := e.do("PUT", "/api/v1/projects/"+g.body["id"].(string)+"/analysis", map[string]bool{"send_source_to_model": true, "run_build_scripts": true}, u.o()).want(t, 200, "")
	if a := on.body["analysis"].(map[string]any); a["run_build_scripts"] != true || on.body["revision"] != float64(2) || on.body["source"].(map[string]any)["repository_url"] != "https://github.com/o/r" {
		t.Fatalf("analysis put %s", on.raw)
	}
	e.do("PUT", "/api/v1/projects/"+g.body["id"].(string)+"/analysis", map[string]bool{"run_build_scripts": true}, u.o()).want(t, 422, "VALIDATION_FAILED")
	gitBody["source"].(map[string]any)["repository_url"] = "https://github.com/O/r.git"
	gitBody["open_existing"] = true
	if r := e.do("POST", "/api/v1/projects", gitBody, u.o()).want(t, 200, ""); r.body["id"] != g.body["id"] {
		t.Fatalf("repository not reused: %s", r.raw)
	}

	// GET /setup: nothing recorded yet, then the summary and the tool table.
	v := e.do("GET", "/api/v1/setup", nil, u.o()).want(t, 200, "")
	if v.body["tools"] != nil || v.body["model"] != nil || v.body["git"].(map[string]any)["hosts"].([]any)[0] != "github.com" {
		t.Fatalf("empty setup %s", v.raw)
	}
	raw, _ := json.Marshal(InstanceSetup{SchemaVersion: "0.1", Model: &SetupModel{ID: "openai/gpt-6.2-sol", Probe: ProbeVerified}, Git: true, GitHosts: []string{"github.com"}})
	if err := os.WriteFile(filepath.Join(state, "run", InstanceSetupFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := toolfind.Write(filepath.Join(state, toolfind.FileName), toolfind.Result{SchemaVersion: toolfind.SchemaVersion, Tools: []toolfind.Tool{{Name: "cppcheck", Status: toolfind.NotFound, Hint: "install cppcheck"}}}); err != nil {
		t.Fatal(err)
	}
	v = e.do("GET", "/api/v1/setup", nil, u.o()).want(t, 200, "")
	m := v.body["model"].(map[string]any)
	folders := v.body["readable_folders"].([]any)
	tools := v.body["tools"].(map[string]any)["tools"].([]any)
	if m["configured"] != true || m["id"] != "openai/gpt-6.2-sol" || m["probe"] != "verified" || v.body["git"].(map[string]any)["available"] != true ||
		len(folders) != 0 || len(tools) != 1 || tools[0].(map[string]any)["hint"] != nil {
		t.Fatalf("setup %s", v.raw)
	}
	// A user who is not an administrator: build scripts are not offered by
	// default, so the defaults leave them off and say why.
	d := v.body["analysis_defaults"].(map[string]any)
	if l, g := d["local"].(map[string]any), d["git"].(map[string]any); l["send_source_to_model"] != true || l["run_build_scripts"] != false || g["send_source_to_model"] != true || g["run_build_scripts"] != false ||
		v.body["available"].(map[string]any)["build_scripts"].(map[string]any)["reason"] != "not_offered" {
		t.Fatalf("defaults %s", v.raw)
	}
	// The administrator sees the tool hints and gets the build scripts.
	admin := e.login(e.newAdminAccount("admin"))
	v = e.do("GET", "/api/v1/setup", nil, admin.o()).want(t, 200, "")
	if v.body["is_admin"] != true || v.body["tools"].(map[string]any)["tools"].([]any)[0].(map[string]any)["hint"] != "install cppcheck" ||
		v.body["analysis_defaults"].(map[string]any)["local"].(map[string]any)["run_build_scripts"] != true || len(v.body["readable_folders"].([]any)) != 0 {
		t.Fatalf("admin setup %s", v.raw)
	}
}

// POST /runs/{id}/retry: a failed run whose snapshot the node holds runs
// again on that snapshot (same run); a cancelled run without a held
// snapshot gets a new run that the client follows; an unfinished run is
// refused; a viewer may not retry.
func TestRetryRunRoute(t *testing.T) {
	e := newEnv(t)
	a := e.driveAttempt("failed")
	if st, _ := e.runStatus(a.runID); st != "failed" {
		t.Fatalf("driven run is %s", st)
	}
	r := e.do("POST", "/api/v1/runs/"+a.runID+"/retry", nil, a.f.user.o()).want(t, 202, "")
	if r.body["id"] != a.runID || r.body["retry"] != "same_run" || r.body["status"] != "queued" {
		t.Fatalf("%s", r.raw)
	}
	e.do("POST", "/api/v1/runs/"+a.runID+"/retry", nil, a.f.user.o()).want(t, 409, "RUN_NOT_FINISHED")
	viewer := e.newUser()
	if err := e.store.SetMember(context.Background(), a.f.user.id, a.f.projectID, viewer.id, "viewer"); err != nil {
		t.Fatal(err)
	}
	queued := e.createRun(a.f.user, a.f.projectID)
	if _, _, err := e.store.CancelRun(context.Background(), a.f.user.id, queued); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/v1/runs/"+queued+"/retry", nil, viewer.o()).want(t, 403, "FORBIDDEN")
	n := e.do("POST", "/api/v1/runs/"+queued+"/retry", nil, a.f.user.o()).want(t, 202, "")
	if n.body["retry"] != "new_run" || n.body["id"] == queued || n.header.Get("Location") != "/api/v1/runs/"+n.body["id"].(string) {
		t.Fatalf("%s", n.raw)
	}
}

// After the project's analysis choices changed, a failed run whose snapshot
// the node holds gets a new run, which follows the new choices.
func TestRetryAfterAnalysisChange(t *testing.T) {
	e := newEnv(t)
	b := e.driveAttempt("failed")
	e.do("PUT", "/api/v1/projects/"+b.f.projectID+"/analysis", map[string]bool{"send_source_to_model": false, "run_build_scripts": true}, b.f.user.o()).want(t, 200, "")
	if r := e.do("POST", "/api/v1/runs/"+b.runID+"/retry", nil, b.f.user.o()).want(t, 202, ""); r.body["retry"] != "new_run" || r.body["id"] == b.runID {
		t.Fatalf("%s", r.raw)
	}
}
