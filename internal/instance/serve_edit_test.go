package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// codeye up --listen ... replaces how the service listens; --public-origin
// alone replaces only the addresses people use and keeps the rest.
func TestEditFileServeAndPublicOrigins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(cfg(filepath.Join(dir, "state"), "")), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := EditFile(path, tools, ConfigEdit{Serve: &ServeEdit{Listen: "0.0.0.0:7331", PlainHTTP: true}})
	if err != nil || c.Listen != "0.0.0.0:7331" || !c.PlainHTTP || len(c.PublicOrigins) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = EditFile(path, tools, ConfigEdit{PublicOrigins: []string{"http://codeye.lan:7331", "http://Other.example:7331"}})
	if err != nil || c.Listen != "0.0.0.0:7331" || !c.PlainHTTP || !slices.Equal(c.PublicOrigins, []string{"http://codeye.lan:7331", "http://other.example:7331"}) {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = EditFile(path, tools, ConfigEdit{PublicOrigins: []string{"http://codeye.vpn:7331"}})
	if err != nil || !slices.Equal(c.PublicOrigins, []string{"http://codeye.vpn:7331"}) {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := EditFile(path, tools, ConfigEdit{PublicOrigins: []string{"https://codeye.vpn"}}); err == nil {
		t.Fatal("an https origin on a plain-HTTP service accepted")
	}
	c, err = EditFile(path, tools, ConfigEdit{Serve: &ServeEdit{Listen: "127.0.0.1:7331"}})
	if err != nil || c.PlainHTTP || len(c.PublicOrigins) != 0 {
		t.Fatalf("back to loopback: %+v %v", c, err)
	}
}

func TestOpenCodeAndSourceRootEditsShareAConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"0.1","agent":{"max_workers":2,"model":{"timeout_seconds":42}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	model := OpenCodeEdit{Executable: filepath.Join(dir, "opencode"), ConfigFile: filepath.Join(dir, "native.jsonc"), Model: "lab/review"}
	start := make(chan struct{})
	done := make(chan error, 9)
	edit := func(e ConfigEdit) {
		<-start
		_, err := EditFile(path, tools, e)
		done <- err
	}
	go edit(ConfigEdit{OpenCode: &model})
	for i := range 8 {
		label := fmt.Sprintf("src%d", i)
		go edit(ConfigEdit{AddRoot: &SourceRoot{Label: label, Path: filepath.Join(dir, label)}})
	}
	close(start)
	for range 9 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	saved, err := Load(path, tools)
	if err != nil || len(saved.SourceRoots) != 8 || saved.Agent.Model == nil || saved.Agent.Model.ConfigFile != model.ConfigFile || saved.Agent.Model.Name != model.Model {
		t.Fatalf("lost an edit: %+v, %v", saved, err)
	}
	cleared, err := EditFile(path, tools, ConfigEdit{OpenCode: &OpenCodeEdit{}})
	if err != nil || cleared.Agent.MaxWorkers != 2 || cleared.Agent.Model == nil || cleared.Agent.Model.TimeoutSeconds != 42 || cleared.Agent.Model.Name != "" || cleared.Agent.Model.ConfigFile != "" {
		t.Fatalf("clearing the connection changed other settings: %+v, %v", cleared, err)
	}
}
