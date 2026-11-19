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
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Review input selection covers C/C++, Python, JS/TS and Rust besides Go.
// Go inputs must not move: these pins hash every request, with fixed
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

// reviewRequests runs every review role and hashes every request.
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
		if r := res[p.Key]; strings.HasPrefix(p.Key, "review.") && r.Status != domain.TaskSucceeded && r.Status != domain.TaskSkippedPolicy && r.Status != domain.TaskBudgetExhausted {
			t.Fatalf("%s %+v", p.Key, r)
		}
	}
	var out []string
	for _, p := range plan {
		if strings.HasPrefix(p.Key, "review.") {
			out = append(out, fmt.Sprintf("%s %s/%s", p.Key, res[p.Key].Status, res[p.Key].Reason))
		}
	}
	for _, q := range f.requests {
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
	"go-build-files": {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret skipped_policy/no_tool_observations", "review.verify succeeded/", "environment f8cad434e236e657 0f35e48b1837906c", "documents 733c8eaaa63abb74 96f75808572d08e4", "investigate 968170384ee26d9d 0abc469c5f262c1d", "verify b33ecdaaf28cf4bc e0f027c525ea4547"},
	"modules":        {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/", "environment f8cad434e236e657 025087360cbe1b62", "documents 733c8eaaa63abb74 1c7ade98e8c6af78", "investigate 968170384ee26d9d 507e8ddbf32be184", "interpret 1dd362236e2820bb 6c30210249e618c0", "verify b33ecdaaf28cf4bc 739ea4868928c8e1"},
	// The worst case has more code than the overview offers, so it plans
	// review.scope and nine slots: the seven the plan-time packing needs and
	// two of slack, which review.scope leaves empty. Its go vet stand-in is
	// no secrets screen: the whole-run requests stay as they were, and the
	// slots run unscreened (their records say
	// model_input_not_secret_screened) with the same requests as behind a
	// completed screen that withholds nothing. The slot requests follow the
	// review priority: the files with go vet diagnostics come first.
	"modules-worst": {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/",
		"review.scope succeeded/",
		"review.investigate.s001 succeeded/", "review.verify.s001 succeeded/", "review.investigate.s002 succeeded/", "review.verify.s002 succeeded/",
		"review.investigate.s003 succeeded/", "review.verify.s003 succeeded/", "review.investigate.s004 succeeded/", "review.verify.s004 succeeded/",
		"review.investigate.s005 succeeded/", "review.verify.s005 succeeded/", "review.investigate.s006 succeeded/", "review.verify.s006 succeeded/",
		"review.investigate.s007 succeeded/", "review.verify.s007 succeeded/", "review.investigate.s008 skipped_policy/no_review_scope", "review.verify.s008 skipped_policy/no_review_scope",
		"review.investigate.s009 skipped_policy/no_review_scope", "review.verify.s009 skipped_policy/no_review_scope",
		"environment f8cad434e236e657 a26534f4af3e1545", "documents 733c8eaaa63abb74 8d417c5a891273d2", "investigate 968170384ee26d9d 4164875e5225f917", "interpret 1dd362236e2820bb 0ba79c5a6cd9bbf3", "verify b33ecdaaf28cf4bc 11e709f191c805fa",
		"investigate 968170384ee26d9d 2bcd0f8700b89737", "verify b33ecdaaf28cf4bc 1e0b3bf55f240a5a", "investigate 968170384ee26d9d 3b87f684621ff34c", "verify b33ecdaaf28cf4bc 0d99b05c7e7f51df",
		"investigate 968170384ee26d9d 360f2ee9ae2d69af", "verify b33ecdaaf28cf4bc 7e1901d8a090ab12", "investigate 968170384ee26d9d 3afca3570b302a87", "verify b33ecdaaf28cf4bc 9b0b4be18897149f",
		"investigate 968170384ee26d9d 5d014697e0b54014", "verify b33ecdaaf28cf4bc b8fe29c64fa3881a", "investigate 968170384ee26d9d c9494c60bcbeb2c9", "verify b33ecdaaf28cf4bc cf20fd606eed6738",
		"investigate 968170384ee26d9d 0a1345f0a1616ef1", "verify b33ecdaaf28cf4bc e6fe2f48261ec762"},
	// The worst case behind a completed secrets screen at the default
	// ceiling: the whole-run requests are those of modules-worst except
	// interpret (it also lists the secrets task), and every slot request
	// carries the overview's investigate and verify instructions.
	"modules-worst-screened": {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/",
		"review.scope succeeded/",
		"review.investigate.s001 succeeded/", "review.verify.s001 succeeded/", "review.investigate.s002 succeeded/", "review.verify.s002 succeeded/",
		"review.investigate.s003 succeeded/", "review.verify.s003 succeeded/", "review.investigate.s004 succeeded/", "review.verify.s004 succeeded/",
		"review.investigate.s005 succeeded/", "review.verify.s005 succeeded/", "review.investigate.s006 succeeded/", "review.verify.s006 succeeded/",
		"review.investigate.s007 succeeded/", "review.verify.s007 succeeded/", "review.investigate.s008 skipped_policy/no_review_scope", "review.verify.s008 skipped_policy/no_review_scope",
		"review.investigate.s009 skipped_policy/no_review_scope", "review.verify.s009 skipped_policy/no_review_scope",
		"environment f8cad434e236e657 a26534f4af3e1545", "documents 733c8eaaa63abb74 8d417c5a891273d2", "investigate 968170384ee26d9d 4164875e5225f917", "interpret 1dd362236e2820bb 71915d5d7b19dc70", "verify b33ecdaaf28cf4bc 11e709f191c805fa",
		"investigate 968170384ee26d9d 2bcd0f8700b89737", "verify b33ecdaaf28cf4bc 1e0b3bf55f240a5a", "investigate 968170384ee26d9d 3b87f684621ff34c", "verify b33ecdaaf28cf4bc 0d99b05c7e7f51df",
		"investigate 968170384ee26d9d 360f2ee9ae2d69af", "verify b33ecdaaf28cf4bc 7e1901d8a090ab12", "investigate 968170384ee26d9d 3afca3570b302a87", "verify b33ecdaaf28cf4bc 9b0b4be18897149f",
		"investigate 968170384ee26d9d 5d014697e0b54014", "verify b33ecdaaf28cf4bc b8fe29c64fa3881a", "investigate 968170384ee26d9d c9494c60bcbeb2c9", "verify b33ecdaaf28cf4bc cf20fd606eed6738",
		"investigate 968170384ee26d9d 0a1345f0a1616ef1", "verify b33ecdaaf28cf4bc e6fe2f48261ec762"},
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
		return asJSON(map[string]any{"candidates": []map[string]any{{"title": "설정 주소 검증 누락", "condition": "주소가 비어 있는 경우", "impact": "서비스가 잘못된 주소로 시작한다.",
			"improvement": "주소를 검사한다.", "counterpoint": "기본값이 항상 채워질 수 있다.", "evidence_ids": []string{in.Scope.Excerpts[0].ID}}}})
	}
	return s
}
