package deps

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// The real fetch from the public Go proxy, then the real go vet reading what
// it fetched (offline). It needs the network, so it runs only with
// CODEYE_TEST_DEPS_NETWORK=1.
func TestRealGoFetchThenVet(t *testing.T) {
	if os.Getenv("CODEYE_TEST_DEPS_NETWORK") != "1" {
		t.Skip("set CODEYE_TEST_DEPS_NETWORK=1 to fetch from the public Go proxy")
	}
	main := "package main\n\nimport \"github.com/spf13/pflag\"\n\nfunc main() { pflag.Parse() }\n"
	files := map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": main}
	job, src := jobOf(t, files)
	goExe := packtest.GoExecutable(t)
	cacheRoot := filepath.Join(t.TempDir(), "cache", "deps")
	vet, err := govet.New(govet.Config{GoExecutable: goExe, DependencyCacheRoot: cacheRoot})
	if err != nil {
		t.Fatal(err)
	}
	h, err := Wrap(vet, Config{CacheRoot: cacheRoot, GoExecutable: goExe}, Fetchers()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tasks, err := h.Plan(ctx, job)
	if err != nil || len(tasks) != 2 || tasks[0].Key != "deps.go" || tasks[1].Key != "go-vet" {
		t.Fatalf("plan %v %v", tasks, err)
	}
	for _, task := range tasks {
		res := h.Run(ctx, job, task, &executor.Executor{})
		if res.Status != domain.TaskSucceeded {
			t.Fatalf("%s: %s/%s", task.Key, res.Status, res.Reason)
		}
	}
	rec := readRecord(t, job, "deps.go")
	if rec.Counts.Prepared != 1 || rec.Counts.Failed != 0 || rec.Manager.Version == nil {
		t.Fatalf("record %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "go", "mod", "github.com", "spf13", "pflag@v1.0.5", "flag.go")); err != nil {
		t.Fatalf("module not in the dependency cache: %v", err)
	}
	for rel, body := range files {
		got, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil || string(got) != body {
			t.Fatalf("source %s changed", rel)
		}
	}
	// The node can remove the cache without changing permissions.
	if err := os.RemoveAll(cacheRoot); err != nil {
		t.Fatalf("cache not removable: %v", err)
	}
}
