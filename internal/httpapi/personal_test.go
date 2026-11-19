package httpapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// Personal mode: a folder is registered by its absolute path (mapped onto
// the longest granted root), the same folder or repository again opens its
// project, the data directories and folders outside the roots are refused
// with their own codes, and GET /setup reports the model, Git, readable
// folders and the detected tools without any key. Server mode refuses
// local_path and reports nulls.
func TestPersonalFolderPathsAndSetup(t *testing.T) {
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
	for _, r := range []string{rootHome.ID, rootWork.ID} {
		if err := e.store.GrantSourceRoot(ctx, r, u.id); err != nil {
			t.Fatal(err)
		}
	}
	// Server mode first.
	e.do("POST", "/api/v1/projects", map[string]any{"local_path": filepath.Join(home, "src", "app")}, u.o()).want(t, 422, "LOCAL_PATH_UNSUPPORTED")
	if v := e.do("GET", "/api/v1/setup", nil, u.o()).want(t, 200, ""); v.body["mode"] != "server" || v.body["model"] != nil || v.body["tools"] != nil {
		t.Fatalf("server setup %s", v.raw)
	}

	e.personal = &Personal{StateDir: state}
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
		filepath.Join(base, "elsewhere"):  "FOLDER_NOT_READABLE",
		filepath.Join(home, "missing"):    "FOLDER_NOT_FOUND",
		"relative/path":                   "FOLDER_NOT_FOUND",
		home:                              "FOLDER_HOLDS_CODEYE_DATA",
		filepath.Join(state, "run"):       "FOLDER_HOLDS_CODEYE_DATA",
		filepath.Join(home, "src", "a:b"): "FOLDER_NAME_UNSUPPORTED",
	} {
		r := e.do("POST", "/api/v1/projects", map[string]any{"local_path": path}, u.o()).want(t, 422, code)
		if code == "FOLDER_NOT_READABLE" && r.body["error"].(map[string]any)["message"] != "outside readable folders - run `codeye up <path>` in a terminal to add it" {
			t.Fatalf("%s", r.raw)
		}
	}
	e.do("POST", "/api/v1/projects", map[string]any{"local_path": filepath.Join(home, "src", "app"), "source": map[string]any{"kind": "local", "source_root_id": rootHome.ID, "relative_path": "."}}, u.o()).want(t, 400, "INVALID_REQUEST")
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
	gitBody["source"].(map[string]any)["repository_url"] = "https://github.com/O/r.git"
	if r := e.do("POST", "/api/v1/projects", gitBody, u.o()).want(t, 200, ""); r.body["id"] != g.body["id"] {
		t.Fatalf("repository not reused: %s", r.raw)
	}

	// GET /setup: nothing recorded yet, then the summary and the tool table.
	v := e.do("GET", "/api/v1/setup", nil, u.o()).want(t, 200, "")
	if v.body["mode"] != "personal" || v.body["git"] != nil || v.body["tools"] != nil || v.body["model"].(map[string]any)["configured"] != false {
		t.Fatalf("empty setup %s", v.raw)
	}
	raw, _ := json.Marshal(PersonalSetup{SchemaVersion: "0.1", Model: &SetupModel{ID: "openai/gpt-6.2-sol", Probe: ProbeVerified}, Git: true, GitHosts: []string{"github.com"}})
	if err := os.WriteFile(filepath.Join(state, "run", PersonalSetupFile), raw, 0o600); err != nil {
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
		len(folders) != 2 || len(tools) != 1 || tools[0].(map[string]any)["hint"] != "install cppcheck" {
		t.Fatalf("setup %s", v.raw)
	}
	if d := v.body["analysis_defaults"].(map[string]any); d["send_source_to_model"] != true || d["run_build_scripts"] != true {
		t.Fatalf("defaults %s", v.raw)
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
