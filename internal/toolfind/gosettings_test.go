package toolfind

import "testing"

// Only settings that differ from Go's defaults are kept; GONOPROXY and
// GONOSUMDB equal to GOPRIVATE are its defaults.
func TestGoSettingsKeepOnlyUserChanges(t *testing.T) {
	if got := settingsOf(map[string]string{"GOPROXY": "https://proxy.golang.org,direct", "GOSUMDB": "sum.golang.org", "GOPRIVATE": "", "GONOPROXY": "", "GONOSUMDB": ""}); got != (GoSettings{}) {
		t.Fatalf("defaults kept: %+v", got)
	}
	got := settingsOf(map[string]string{"GOPROXY": "https://goproxy.example,direct", "GOSUMDB": "off", "GOPRIVATE": "git.corp/*", "GONOPROXY": "git.corp/*", "GONOSUMDB": "git.corp/*,other.corp"})
	want := GoSettings{GoProxy: "https://goproxy.example,direct", GoSumDB: "off", GoPrivate: "git.corp/*", GoNoSumDB: "git.corp/*,other.corp"}
	if got != want {
		t.Fatalf("settings %+v, want %+v", got, want)
	}
	if got := settingsOf(map[string]string{"GOPRIVATE": "a\nb"}); got.GoPrivate != "" {
		t.Fatalf("multi-line value kept: %+v", got)
	}
}
