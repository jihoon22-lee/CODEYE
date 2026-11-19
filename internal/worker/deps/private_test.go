package deps

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The operator's Go settings travel to the fetch, and the private modules
// already in the operator's module cache are copied into the dependency
// cache (a private module bypasses every proxy, the file seed included).
func TestGoFetchFollowsTheOperatorsPrivateModules(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	operator := t.TempDir()
	writeTree(t, operator, map[string]string{
		"cache/download/example.com/!corp/lib/@v/v1.2.0.zip":  "zip",
		"cache/download/example.com/!corp/lib/@v/v1.2.0.mod":  "module example.com/Corp/lib\n",
		"cache/download/example.com/!corp/lib/@v/v1.2.0.lock": "",
		"cache/download/github.com/spf13/pflag/@v/v1.0.5.zip": "public",
	})
	h := handler(t, Config{GoModCache: operator, GoProxy: "https://goproxy.example,direct", GoSumDB: "sum.example", GoPrivate: "example.com/Corp"})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result { return exited(0, downloadJSON, "") })}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("result = %+v", res)
	}
	env := r.specs[2].Env
	if want := fileProxyURL(filepath.Join(operator, "cache", "download")) + ",https://goproxy.example"; env["GOPROXY"] != want || env["GOSUMDB"] != "sum.example" || env["GOPRIVATE"] != "example.com/Corp" {
		t.Fatalf("GOPROXY %q GOSUMDB %q GOPRIVATE %q", env["GOPROXY"], env["GOSUMDB"], env["GOPRIVATE"])
	}
	if _, set := env["GONOPROXY"]; set {
		t.Fatal("an unset GONOPROXY keeps Go's default (GOPRIVATE)")
	}
	dl := filepath.Join(h.cfg.CacheRoot, "go", "mod", "cache", "download")
	for rel, want := range map[string]bool{"example.com/!corp/lib/@v/v1.2.0.zip": true, "example.com/!corp/lib/@v/v1.2.0.mod": true, "example.com/!corp/lib/@v/v1.2.0.lock": false, "github.com/spf13/pflag/@v/v1.0.5.zip": false} {
		if _, err := os.Stat(filepath.Join(dl, filepath.FromSlash(rel))); (err == nil) != want {
			t.Errorf("%s copied = %v, want %v", rel, err == nil, want)
		}
	}
	if rec := readRecord(t, job, "deps.go"); !slices.Contains(rec.Limitations, LimPrivateModulesSeeded) {
		t.Fatalf("limitations %v", rec.Limitations)
	}
}

// An operator cache that holds only lock files (the offline go command
// leaves them) serves nothing: no file proxy, no seed claimed.
func TestOperatorCacheWithoutModulesIsNoSeed(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	operator := t.TempDir()
	writeTree(t, operator, map[string]string{"cache/download/github.com/spf13/pflag/@v/v1.0.5.lock": ""})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result { return exited(0, downloadJSON, "") })}
	res := handler(t, Config{GoModCache: operator}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskSucceeded || r.specs[2].Env["GOPROXY"] != DefaultGoProxy {
		t.Fatalf("result %s GOPROXY %q", res.Status, r.specs[2].Env["GOPROXY"])
	}
	if rec := readRecord(t, job, "deps.go"); slices.Contains(rec.Limitations, "seeded_from_operator_cache") {
		t.Fatalf("limitations %v", rec.Limitations)
	}
}

func TestGoPrivateModuleNotFetched(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result {
		return exited(1, "", "go: github.com/spf13/pflag@v1.0.5: GOVCS disallows using git for private github.com/spf13/pflag; see 'go help vcs'")
	})}
	res := handler(t, Config{GoPrivate: "github.com/spf13"}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskFailed || res.Reason != ReasonPrivateModuleNotFetched {
		t.Fatalf("result = %s/%s", res.Status, res.Reason)
	}
	if env := r.specs[2].Env; env["GOPRIVATE"] != "github.com/spf13" || env["GOVCS"] != "*:off" {
		t.Fatalf("env %v", env)
	}
}

func TestRegistryProxiesAndPatterns(t *testing.T) {
	for in, want := range map[string]string{"": DefaultGoProxy, "https://a,direct": "https://a", "https://a|https://b,off": "https://a,https://b", "direct": "", "off": ""} {
		if got := strings.Join(registryProxies(in), ","); got != want {
			t.Errorf("registryProxies(%q) = %q, want %q", in, got, want)
		}
	}
	for target, want := range map[string]bool{"example.com/corp/lib": true, "example.com/corp": true, "example.com/other": false, "git.internal/x/y": true, "example.com": false} {
		if got := matchPrefixPatterns("example.com/corp, *.internal", target); got != want {
			t.Errorf("match %q = %v, want %v", target, got, want)
		}
	}
	if p, ok := unescapeModulePath("github.com/!burnt!sushi/toml"); !ok || p != "github.com/BurntSushi/toml" {
		t.Errorf("unescape = %q %v", p, ok)
	}
}

// With every proxy entry removed (GOPROXY=direct for the user) the fetch
// asks no registry at all.
func TestGoProxyOffWhenOnlyDirect(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result {
		return exited(1, "", "go: github.com/spf13/pflag@v1.0.5: module lookup disabled by GOPROXY=off")
	})}
	res := handler(t, Config{GoProxy: "direct"}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if r.specs[2].Env["GOPROXY"] != "off" || res.Reason != "registry_unreachable" {
		t.Fatalf("GOPROXY %q reason %s", r.specs[2].Env["GOPROXY"], res.Reason)
	}
}
