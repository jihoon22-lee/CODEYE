package staticcheck

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// goModSum is the go.sum "h1:" hash of a module's go.mod file (dirhash Hash1
// over the single file "go.mod").
func goModSum(mod []byte) string {
	file := sha256.Sum256(mod)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%x  go.mod\n", file)))
	return "h1:" + base64.StdEncoding.EncodeToString(sum[:])
}

// The success path with an operator module cache (--go-mod-cache): a module
// whose dependency is in the cache is analyzed offline, the cache is a
// protected read-only root and does not change. CODEYE_TEST_GOMODCACHE names
// a cache holding golang.org/x/sync v0.11.0; the go.sum lines are computed
// from the cache files, nothing is downloaded or written there.
func TestRealStaticcheckOperatorModuleCache(t *testing.T) {
	testStaticcheck(t)
	cache := os.Getenv("CODEYE_TEST_GOMODCACHE")
	if cache == "" {
		t.Skip("set CODEYE_TEST_GOMODCACHE to an offline module cache holding golang.org/x/sync v0.11.0")
	}
	dl := filepath.Join(cache, "cache", "download", "golang.org", "x", "sync", "@v")
	mod, err := os.ReadFile(filepath.Join(dl, "v0.11.0.mod"))
	if err != nil {
		t.Skipf("golang.org/x/sync v0.11.0 not in the cache: %v", err)
	}
	zipHash, err := os.ReadFile(filepath.Join(dl, "v0.11.0.ziphash"))
	if err != nil {
		t.Skipf("golang.org/x/sync v0.11.0 zip hash not in the cache: %v", err)
	}
	extracted := filepath.Join(cache, "golang.org", "x", "sync@v0.11.0")
	if _, err := os.Stat(extracted); err != nil {
		t.Skipf("golang.org/x/sync v0.11.0 not extracted in the cache: %v", err)
	}
	gosum := fmt.Sprintf("golang.org/x/sync v0.11.0 %s\ngolang.org/x/sync v0.11.0/go.mod %s\n", strings.TrimSpace(string(zipHash)), goModSum(mod))
	files := map[string]string{
		"go.mod": "module example.test/dep\n\ngo 1.22\n\nrequire golang.org/x/sync v0.11.0\n",
		"go.sum": gosum,
		"dep.go": "package dep\n\nimport (\n\t\"regexp\"\n\n\t\"golang.org/x/sync/errgroup\"\n)\n\nfunc Group() *errgroup.Group { return new(errgroup.Group) }\n\nfunc Re() *regexp.Regexp { return regexp.MustCompile(\"[\") }\n",
	}
	before := []string{packtest.TreeHash(t, dl), packtest.TreeHash(t, extracted)}
	fx := newFixture(t, "", files, domain.Scope{})
	a := newAnalyzer(t, cache)
	runner := &mutatingRunner{inner: &executor.Executor{}}
	res := packtest.RunTask(t, a, fx.Job, "staticcheck", runner)
	doc, _ := readEnvelope(t, fx.Job, res)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"dep.go"}) {
		t.Fatalf("operator cache run %s/%s %+v %+v %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage, doc.ToolErrors)
	}
	if slices.Contains(doc.Limitations, "no_offline_module_cache") || packtest.Rules(doc)["SA1000"] != 1 {
		t.Fatalf("limitations %v rules %v", doc.Limitations, packtest.Rules(doc))
	}
	for _, s := range runner.specs {
		if s.Env["GOMODCACHE"] != cache || !slices.Contains(s.ProtectedRoots, cache) || s.Env["GOPROXY"] != "off" {
			t.Fatalf("spec %s %v: cache %q protected %v", s.Executable, s.Args, s.Env["GOMODCACHE"], s.ProtectedRoots)
		}
	}
	if after := []string{packtest.TreeHash(t, dl), packtest.TreeHash(t, extracted)}; !slices.Equal(before, after) {
		t.Fatal("the operator module cache changed")
	}
}
