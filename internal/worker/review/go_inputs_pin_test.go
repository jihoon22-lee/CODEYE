package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Review input selection covers C/C++, Python, JS/TS and Rust besides Go.
// The pins catch unplanned request drift by hashing every request, with fixed
// snapshot, run and attempt identities, on the modules fixture, its worst
// case and a Go tree that also holds build files of other ecosystems,
// vendored and data directories (no non-Go source, so no other ecosystem is
// in scope).

const (
	pinSnapshot = "7777777777777777777777777777777777777777777777777777777777777777"
	pinRun      = "0195f3a0-0000-7000-8000-000000000031"
	pinAttempt  = "0195f3a0-0000-7000-8000-000000000032"
)

func fixIdentity(j analysis.Job) analysis.Job {
	j.Snapshot.Digest, j.RunID, j.AttemptID = pinSnapshot, pinRun, pinAttempt
	return j
}

// dirJob acquires a copy of a repository directory as a snapshot.
func dirJob(t *testing.T, dir string) analysis.Job {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		tree[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return treeJob(t, tree)
}

// goBuildFilesTree is Go source with build files of every other ecosystem
// (no source of them), vendored, data and hidden Go directories.
var goBuildFilesTree = map[string]string{
	"go.mod":                "module example.test/mixed\n\ngo 1.23\n",
	"README.md":             "# Mixed\n\nThe service reads a configuration and serves requests.\n",
	"main.go":               "package main\n\nfunc main() { serve(load()) }\n",
	"serve.go":              "package main\n\nfunc serve(c config) { _ = c }\n",
	"load.go":               "package main\n\ntype config struct{ addr string }\n\nfunc load() config { return config{addr: \":8080\"} }\n",
	"load_test.go":          "package main\n\nimport \"testing\"\n\nfunc TestLoad(t *testing.T) { _ = load() }\n",
	"vendor/dep/dep.go":     "package dep\n\nfunc Dep() int { return 1 }\n",
	"testdata/in.go":        "package data\n",
	".tools/gen.go":         "package tools\n",
	"Makefile":              "all:\n\tgo build ./...\n",
	"package.json":          "{\"name\": \"mixed\", \"private\": true}\n",
	"tsconfig.json":         "{\"compilerOptions\": {\"strict\": true}}\n",
	"pyproject.toml":        "[project]\nname = \"mixed\"\n",
	"setup.cfg":             "[metadata]\nname = mixed\n",
	"Cargo.toml":            "[package]\nname = \"mixed\"\nversion = \"0.1.0\"\n",
	"CMakeLists.txt":        "cmake_minimum_required(VERSION 3.20)\nproject(mixed)\n",
	"compile_commands.json": "[]\n",
}

func pinHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:])[:16] }

// reviewRequests hashes the whole-run requests.
func reviewRequests(t *testing.T, j analysis.Job, static analysis.Handler, script map[string]func(int, opencode.Request) opencode.Reply) []string {
	t.Helper()
	return reviewRequestsWith(t, j, Handler{Static: static}, script)
}

// reviewRequestsWith is reviewRequests with a configured role handler (call
// ceiling); slices the ceiling refuses end budget_exhausted.
func reviewRequestsWith(t *testing.T, j analysis.Job, rh Handler, script map[string]func(int, opencode.Request) opencode.Reply) []string {
	t.Helper()
	f := &scriptedCaller{script: script}
	rh.Caller = f
	plan, res := runRoleWith(t, j, rh, "1.27.4")
	for _, p := range plan {
		if !wholeRunRole(p.Key) && p.Key != record.TaskContract {
			continue
		}
		if r := res[p.Key]; strings.HasPrefix(p.Key, "review.") && r.Status != domain.TaskSucceeded && r.Status != domain.TaskSkippedPolicy && r.Status != domain.TaskBudgetExhausted {
			t.Fatalf("%s %+v", p.Key, r)
		}
	}
	var out []string
	for _, p := range plan {
		if wholeRunRole(p.Key) || p.Key == record.TaskContract {
			out = append(out, fmt.Sprintf("%s %s/%s", p.Key, res[p.Key].Status, res[p.Key].Reason))
		}
	}
	pending := map[string]bool{}
	for _, q := range f.requests {
		if pending[q.Role] {
			continue
		}
		pending[q.Role] = true
		if q.Role == record.StageVerify && strings.Contains(string(q.Input), limVerifyHashesOmitted) {
			in := decodeAs[verifyInput](t, q)
			catalog := catalogOf(in.Scope.Excerpts)
			for _, candidate := range in.Candidates {
				for _, id := range candidate.Evidence {
					if e := catalog[id]; e.ID == "" || e.Text == "" || e.SHA256 != "" {
						t.Fatalf("compact verification request changed the cited source: %+v", e)
					}
				}
			}
			verify := readRecord(t, j, record.StageVerify)
			for _, e := range verify.Evidence {
				if e.ID != record.ExcerptID(verify.Snapshot, e.Path, e.SHA256, e.Start, e.End) {
					t.Fatalf("compact request changed the source binding in the record: %+v", e)
				}
			}
		}
		out = append(out, fmt.Sprintf("%s %s %s", q.Role, pinHash([]byte(q.Instructions)), pinHash(q.Input)))
	}
	return out
}

// fixtureJob is a one-package Go snapshot (a metrics function with an
// off-by-one loop), with or without a README.
func fixtureJob(t *testing.T, docs bool) analysis.Job {
	t.Helper()
	root := t.TempDir()
	write := func(name, s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("metrics.go", "package metrics\n// Sum returns the sum of every sample.\nfunc Sum(samples []int) int {\n n:=0\n for i:=0;i+1<len(samples);i++ { n+=samples[i] }\n return n\n}\n")
	if docs {
		write("README.md", "Sum includes every sample, including the last. An empty input sums to zero.\n")
	}
	write("service.go", "package metrics\nconst password = \"long_fixture_dummy_value\"")
	s, err := source.AcquireLocal(t.Context(), root, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Snapshot: s, Scratch: t.TempDir(), Scope: domain.Scope{Kind: "full"}, AllowModelReview: true}
}

// verifyJob acquires the testdata fixture as an immutable snapshot.
func verifyJob(t *testing.T, docs bool) analysis.Job {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"store.go", "load.go", "store_test.go", "go.mod"} {
		b, err := os.ReadFile(filepath.Join("testdata", "verify", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if docs {
		if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("store.Open hands the file to the caller, which closes it.\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := source.AcquireLocal(t.Context(), root, ".", t.TempDir(), source.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return analysis.Job{RunID: domain.NewID(), AttemptID: domain.NewID(), Epoch: 1, Snapshot: s, Scratch: t.TempDir(), Scope: domain.Scope{Kind: "full"}, AllowModelReview: true}
}

// goPinJobs are small Go snapshots; none plans a review slice.
func goPinJobs(t *testing.T) map[string]analysis.Job {
	metricsTree := map[string]string{
		"go.mod":     "module sample.test/metrics\n\ngo 1.23\n",
		"metrics.go": "package metrics\n\n// Mean returns the mean of the samples; an empty input is an error.\nfunc Mean(xs []float64) (float64, error) {\n\tsum := 0.0\n\tfor _, x := range xs {\n\t\tsum += x\n\t}\n\treturn sum / float64(len(xs)), nil\n}\n",
		"README.md":  "# Metrics\n\nMean returns the mean of the samples; an empty input is an error.\n",
	}
	return map[string]analysis.Job{
		"verify-docs":    verifyJob(t, true),
		"verify-nodocs":  verifyJob(t, false),
		"metrics-docs":   fixtureJob(t, true),
		"metrics-nodocs": fixtureJob(t, false),
		"modules":        modulesJob(t, nil),
		"go-vet":         dirJob(t, "../../../fixtures/go-vet"),
		"metrics-tree":   treeJob(t, metricsTree),
		"go-build-files": treeJob(t, goBuildFilesTree),
	}
}

var pinnedGo = map[string][]string{
	"go-build-files":         {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret skipped_policy/no_tool_observations", "review.verify succeeded/", "review.contract skipped_policy/no_review_scope", "environment aa8c6caaf704d2c6 0f35e48b1837906c", "documents 20cb92232bdd441e cbb9d0c83ce64f50", "investigate 262d742e8c88ce2c 119fede47bae06bc", "verify 3c8997d0bf649588 61eb76f4c28efe0e"},
	"modules":                {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/", "review.contract skipped_policy/no_review_scope", "environment aa8c6caaf704d2c6 1591d2065785bf02", "documents 20cb92232bdd441e 044b26c350ee8aac", "investigate 262d742e8c88ce2c 5b8413e0e1c249c2", "interpret e6268a7c7d386d77 c9450e5228781aca", "verify 3c8997d0bf649588 0e7dfe8686abbccc"},
	"modules-worst":          {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/", "review.contract skipped_policy/no_review_scope", "environment aa8c6caaf704d2c6 a26534f4af3e1545", "documents 20cb92232bdd441e 8ea8deedb933224b", "investigate 600b0aab6c7a3a25 d0cbf04835661545", "interpret e6268a7c7d386d77 7f67451825912287", "verify 3c8997d0bf649588 ab9f4ca60f342e36", "triage e9a1ff682b156f2d 88a9f74a9fd6711f"},
	"modules-worst-screened": {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/", "review.contract skipped_policy/no_review_scope", "environment aa8c6caaf704d2c6 a26534f4af3e1545", "documents 20cb92232bdd441e 8ea8deedb933224b", "investigate 600b0aab6c7a3a25 d0cbf04835661545", "interpret e6268a7c7d386d77 229298a67ef0c9b9", "verify 3c8997d0bf649588 ab9f4ca60f342e36", "triage e9a1ff682b156f2d 88a9f74a9fd6711f"},
}

func TestGoReviewInputsUnchanged(t *testing.T) {
	got := map[string][]string{
		"modules":        reviewRequests(t, fixIdentity(modulesJob(t, nil)), vetStub{obs: gatewayObservation}, validScript(t)),
		"modules-worst":  reviewRequests(t, fixIdentity(worstCaseJob(t, nil)), vetStub{obs: worstCaseObservations}, maxScript(t)),
		"go-build-files": reviewRequests(t, fixIdentity(treeJob(t, goBuildFilesTree)), vetStub{}, goBuildFilesScript(t)),
		"modules-worst-screened": reviewRequestsWith(t, fixIdentity(worstCaseJob(t, nil)),
			Handler{Static: secretsStub{obs: worstCaseObservations}, MaxCalls: DefaultMaxCalls}, maxScript(t)),
	}
	if os.Getenv("CODEYE_PRINT_PINS") == "1" {
		for _, name := range sortedKeys(got) {
			t.Logf("role %q: %q,", name, got[name])
		}
	}
	for name, want := range pinnedGo {
		if !slices.Equal(got[name], want) {
			t.Errorf("review requests on %s changed:\n got %q\nwant %q", name, got[name], want)
		}
	}
	if len(pinnedGo) != len(got) {
		t.Fatalf("pins cover %d/%d fixtures", len(pinnedGo), len(got))
	}
}

// goBuildFilesScript is validScript without the modules fixture specifics.
func goBuildFilesScript(t *testing.T) map[string]func(int, opencode.Request) opencode.Reply {
	s := validScript(t)
	s["environment"] = maxScript(t)["environment"]
	s["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		return asJSON(map[string]any{"contracts": []map[string]any{{"statement": "서비스는 설정을 읽어 요청을 처리한다.", "applies_to": ".", "evidence_ids": []string{in.Documents[0].EvidenceID}}},
			"conflicts": []any{}, "questions": []any{}, "instruction_notes": []any{}})
	}
	s["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		return asJSON(map[string]any{"candidates": []map[string]any{{"title": "설정 주소 검증 누락", "condition": "주소가 비어 있는 경우", "mechanism": "빈 주소가 검사 없이 연결에 쓰인다.", "impact": "서비스가 잘못된 주소로 시작한다.",
			"improvement": "주소를 검사한다.", "counterpoint": "기본값이 항상 채워질 수 있다.", "evidence_ids": []string{in.Scope.Excerpts[0].ID}}}})
	}
	return s
}
