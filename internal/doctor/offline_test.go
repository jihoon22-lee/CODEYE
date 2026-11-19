package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

func offlineConfig(t *testing.T, extra string) *personal.Config {
	t.Helper()
	base := t.TempDir()
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	raw := `{"schema_version":"0.1","state_dir":` + q(filepath.Join(base, "state")) + `,"database":{"mode":"external"}` + extra + `}`
	cfg, err := personal.Parse([]byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func destinationOf(o *Offline, kind string) Destination {
	for _, d := range o.Destinations {
		if d.Kind == kind {
			return d
		}
	}
	return Destination{}
}

func languageOf(o *Offline, name string) Language {
	for _, l := range o.Languages {
		if l.Language == name {
			return l
		}
	}
	return Language{}
}

// The offline readiness section (docs/OPERATIONS.md#오프라인-준비-점검): each
// destination with its origin, no public default in offline mode, the user's
// own go env and npm settings counted as configured, and per language what
// is missing.
func TestOfflineReadiness(t *testing.T) {
	menv := personal.ModelEnv{Getenv: func(string) string { return "" }, Home: t.TempDir()}
	detect := func(context.Context) toolfind.Result {
		res := detectedTools("go", "ruff", "mypy", "python", "eslint", "node", "npm")(context.Background())
		res.GoSettings = &toolfind.GoSettings{GoProxy: "https://goproxy.corp.example"}
		res.NpmRegistry = "https://npm.corp.example/"
		return res
	}
	online := Collect(context.Background(), Options{WorkDir: t.TempDir(), Timeout: time.Second, Personal: offlineConfig(t, ""), Detect: detect, ModelEnv: &menv}, &fakeRunner{result: Result{Reason: "executable_missing"}})
	o := online.Offline
	if o == nil || o.Enabled || o.CABundle.Status != "not_configured" {
		t.Fatalf("online section %+v", o)
	}
	if d := destinationOf(o, "pip_index"); d.Origin != "builtin_default" || d.Value != defaultPipIndex {
		t.Fatalf("online pip index %+v", d)
	}
	if d := destinationOf(o, "go_proxy"); d.Origin != "user_go_env" || d.Value != "https://goproxy.corp.example" {
		t.Fatalf("user go proxy %+v", d)
	}
	if d := destinationOf(o, "git_host"); d.Value != "github.com" || d.Origin != "builtin_default" {
		t.Fatalf("online git host %+v", d)
	}

	cfg := offlineConfig(t, `,"offline":true,"agent":{"dependencies":{"pip_index_url":"https://pypi.corp.example/simple"}}`)
	report := Collect(context.Background(), Options{WorkDir: t.TempDir(), Timeout: time.Second, Personal: cfg, Detect: detect, ModelEnv: &menv}, &fakeRunner{result: Result{Reason: "executable_missing"}})
	o = report.Offline
	if !o.Enabled {
		t.Fatal("offline not reported")
	}
	for kind, want := range map[string]Destination{
		"go_proxy":        {Kind: "go_proxy", Value: "https://goproxy.corp.example", Origin: "user_go_env"},
		"go_sumdb":        {Kind: "go_sumdb", Origin: "none", Reason: "checksum_database_off"},
		"npm_registry":    {Kind: "npm_registry", Value: "https://npm.corp.example/", Origin: "user_npm_config"},
		"pip_index":       {Kind: "pip_index", Value: "https://pypi.corp.example/simple", Origin: "config"},
		"crates_registry": {Kind: "crates_registry", Origin: "none", Reason: "offline_no_registry_configured"},
		"git_host":        {Kind: "git_host", Origin: "none", Reason: "git_hosts_empty"},
		"model_endpoint":  {Kind: "model_endpoint", Origin: "none", Reason: "model_not_configured"},
	} {
		if got := destinationOf(o, kind); got != want {
			t.Errorf("%s = %+v, want %+v", kind, got, want)
		}
	}
	if l := languageOf(o, "go"); l.Status != "partial" || l.Dependencies != "internal_registry" || strings.Join(l.Missing, ",") != "staticcheck" {
		t.Errorf("go %+v", l)
	}
	if l := languageOf(o, "rust"); l.Status != "unavailable" || l.Dependencies != "none" || !strings.Contains(strings.Join(l.Missing, ","), "dependency_source") {
		t.Errorf("rust %+v", l)
	}
	if l := languageOf(o, "model"); !strings.Contains(strings.Join(l.Missing, ","), "model_endpoint") {
		t.Errorf("model %+v", l)
	}

	// An unreadable bundle is reported without its path.
	bad := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(bad, []byte("not PEM"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.CABundle = bad
	report = Collect(context.Background(), Options{WorkDir: t.TempDir(), Timeout: time.Second, Personal: cfg, Detect: detect, ModelEnv: &menv}, &fakeRunner{result: Result{Reason: "executable_missing"}})
	var out bytes.Buffer
	if err := report.Write(&out, "json"); err != nil || report.Offline.CABundle.Status != "unavailable" || strings.Contains(out.String(), bad) {
		t.Fatalf("bundle %+v %v", report.Offline.CABundle, err)
	}
	out.Reset()
	if err := report.Write(&out, "text"); err != nil || !strings.Contains(out.String(), "Offline readiness (offline mode on, ca_bundle unavailable)") {
		t.Fatalf("text %q %v", out.String(), err)
	}
}
