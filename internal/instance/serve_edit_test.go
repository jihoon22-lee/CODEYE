package instance

import (
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
