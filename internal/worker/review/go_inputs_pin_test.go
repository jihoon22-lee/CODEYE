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
)

// Review input selection covers C/C++, Python, JS/TS and Rust besides Go.
// Go inputs must not move: these pins were captured on the code before the
// other ecosystems were added (aa2ae13) and hash every request of both
// packs, with fixed snapshot, run and attempt identities, on the existing Go
// fixtures and on a Go tree that also holds build files of other ecosystems,
// vendored and data directories (no non-Go source, so no other ecosystem is
// in scope). TestLegacyPackIsPinned, TestRolePackExampleRecords and
// TestGoSelectionDigestsPinned stay as they were.

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

// legacyGeneric answers every legacy-pack role validly on any corpus.
func legacyGeneric(t *testing.T) *fakeCaller {
	return &fakeCaller{t: t, reply: func(_ int, q opencode.Request) opencode.Reply {
		in := decodeInput(t, q)
		code := ""
		for _, e := range in.Scope.Excerpts {
			if code == "" && (e.Kind == "code" || e.Kind == "test") {
				code = e.ID
			}
		}
		switch q.Role {
		case "context":
			return ok(map[string]any{"facts": []any{map[string]any{"kind": "observed", "statement": "발췌한 코드는 설정을 읽어 요청을 처리한다.", "evidence_ids": []string{in.Scope.Excerpts[0].ID}}}})
		case "investigate":
			return ok(map[string]any{"candidates": []any{map[string]any{"title": "입력 검증 누락 가능성", "condition": "빈 입력이 들어오는 경우", "impact": "잘못된 결과를 반환할 수 있다.",
				"improvement": "입력 계약을 확인한다.", "counterpoint": "호출자가 미리 검사할 수 있다.", "evidence_ids": []string{code}}}})
		}
		var out []any
		for _, c := range in.Candidates {
			out = append(out, verdict(c.ID, "inconclusive", func(m map[string]any) {
				m["unresolved_assumptions"] = []string{"호출자의 입력 검사를 확인하지 못했다."}
			}))
		}
		return ok(map[string]any{"verifications": out})
	}}
}

func pinHash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:])[:16] }

// legacyRequests runs the three legacy-pack roles and hashes every request.
func legacyRequests(t *testing.T, j analysis.Job) []string {
	t.Helper()
	f := legacyGeneric(t)
	if _, r := runStages(t, j, f); r.Status != domain.TaskSucceeded {
		t.Fatalf("verify %+v", r)
	}
	var out []string
	for _, q := range f.requests {
		out = append(out, fmt.Sprintf("%s %s %s", q.Role, pinHash([]byte(q.Instructions)), pinHash(q.Input)))
	}
	return out
}

// roleRequests runs every role-pack role and hashes every request.
func roleRequests(t *testing.T, j analysis.Job, static analysis.Handler, script map[string]func(int, opencode.Request) opencode.Reply) []string {
	t.Helper()
	return roleRequestsWith(t, j, RoleHandler{Static: static}, script)
}

// roleRequestsWith is roleRequests with a configured role handler (call
// ceiling); slices the ceiling refuses end budget_exhausted.
func roleRequestsWith(t *testing.T, j analysis.Job, rh RoleHandler, script map[string]func(int, opencode.Request) opencode.Reply) []string {
	t.Helper()
	f := &roleCaller{script: script}
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

var pinnedGoLegacy = map[string][]string{
	"go-build-files": {"context 355003a09ceebfb7 267de9a5d3e3f492", "investigate 5320a19e77e14cdf 8e2e97d3a545a576", "verify bdd78ed463a1e7f8 556edce420344247"},
	"go-vet":         {"context 355003a09ceebfb7 396b5ab108ec2076", "investigate 5320a19e77e14cdf 78f861b79d289f70", "verify bdd78ed463a1e7f8 860e0e4b571b3e7a"},
	"metrics-docs":   {"context 355003a09ceebfb7 18ebce24d6a3c9de", "investigate 5320a19e77e14cdf 9ceb2b66139bb81a", "verify bdd78ed463a1e7f8 bd22987692c57292"},
	"metrics-nodocs": {"context 355003a09ceebfb7 4240c78b372a9fee", "investigate 5320a19e77e14cdf 673859093323b077", "verify bdd78ed463a1e7f8 cfde2d1b3852aeab"},
	"metrics-tree":   {"context 355003a09ceebfb7 ae7bc3615292281f", "investigate 5320a19e77e14cdf 4bf20e752f4534d9", "verify bdd78ed463a1e7f8 d883e8e3baea8079"},
	"verify-docs":    {"context 355003a09ceebfb7 f4fe9aec8d34c643", "investigate 5320a19e77e14cdf 7a78b71ea5f997d2", "verify bdd78ed463a1e7f8 300f6bcd8454edd8"},
	"verify-nodocs":  {"context 355003a09ceebfb7 2b3579abee6cf0e4", "investigate 5320a19e77e14cdf 42bffbe217ff1098", "verify bdd78ed463a1e7f8 20162075f564aa1a"},
	"modules":        {"context 355003a09ceebfb7 89fa61077d06084d", "investigate 5320a19e77e14cdf ae6d304680c96536", "verify bdd78ed463a1e7f8 b3e3b2ed3305f8f3"},
}

var pinnedGoRole = map[string][]string{
	"go-build-files": {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret skipped_policy/no_tool_observations", "review.verify succeeded/", "environment e44b353d678a2b55 0f35e48b1837906c", "documents 6cd0e37915e181ac 96f75808572d08e4", "investigate 0811ea5731925461 0abc469c5f262c1d", "verify 1237fcad33c639ca e0f027c525ea4547"},
	"modules":        {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/", "environment e44b353d678a2b55 025087360cbe1b62", "documents 6cd0e37915e181ac 1c7ade98e8c6af78", "investigate 0811ea5731925461 507e8ddbf32be184", "interpret 7031ffa47de47a37 6c30210249e618c0", "verify 1237fcad33c639ca 739ea4868928c8e1"},
	// The worst case has more code than the overview offers, so it plans
	// review.scope and ten slices; its go vet stand-in is no secrets
	// screen, so they all skip without a request and every pinned request
	// stays as it was.
	"modules-worst": {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/",
		"review.scope skipped_policy/secret_screen_unavailable",
		"review.investigate.s001 skipped_policy/secret_screen_unavailable", "review.verify.s001 skipped_policy/secret_screen_unavailable",
		"review.investigate.s002 skipped_policy/secret_screen_unavailable", "review.verify.s002 skipped_policy/secret_screen_unavailable",
		"review.investigate.s003 skipped_policy/secret_screen_unavailable", "review.verify.s003 skipped_policy/secret_screen_unavailable",
		"review.investigate.s004 skipped_policy/secret_screen_unavailable", "review.verify.s004 skipped_policy/secret_screen_unavailable",
		"review.investigate.s005 skipped_policy/secret_screen_unavailable", "review.verify.s005 skipped_policy/secret_screen_unavailable",
		"review.investigate.s006 skipped_policy/secret_screen_unavailable", "review.verify.s006 skipped_policy/secret_screen_unavailable",
		"review.investigate.s007 skipped_policy/secret_screen_unavailable", "review.verify.s007 skipped_policy/secret_screen_unavailable",
		"review.investigate.s008 skipped_policy/secret_screen_unavailable", "review.verify.s008 skipped_policy/secret_screen_unavailable",
		"review.investigate.s009 skipped_policy/secret_screen_unavailable", "review.verify.s009 skipped_policy/secret_screen_unavailable",
		"review.investigate.s010 skipped_policy/secret_screen_unavailable", "review.verify.s010 skipped_policy/secret_screen_unavailable",
		"environment e44b353d678a2b55 a26534f4af3e1545", "documents 6cd0e37915e181ac 8d417c5a891273d2", "investigate 0811ea5731925461 4164875e5225f917", "interpret 7031ffa47de47a37 0ba79c5a6cd9bbf3", "verify 1237fcad33c639ca 11e709f191c805fa"},
	// The worst case behind a completed secrets screen at the default
	// ceiling: the whole-run requests are those of modules-worst except
	// interpret (it also lists the secrets task), and every slice request
	// carries the overview's investigate and verify instructions.
	"modules-worst-screened": {"review.environment succeeded/", "review.documents succeeded/", "review.investigate succeeded/", "review.interpret succeeded/", "review.verify succeeded/",
		"review.scope succeeded/",
		"review.investigate.s001 succeeded/", "review.verify.s001 succeeded/", "review.investigate.s002 succeeded/", "review.verify.s002 succeeded/",
		"review.investigate.s003 succeeded/", "review.verify.s003 succeeded/", "review.investigate.s004 succeeded/", "review.verify.s004 succeeded/",
		"review.investigate.s005 succeeded/", "review.verify.s005 succeeded/", "review.investigate.s006 succeeded/", "review.verify.s006 succeeded/",
		"review.investigate.s007 succeeded/", "review.verify.s007 succeeded/", "review.investigate.s008 succeeded/", "review.verify.s008 succeeded/",
		"review.investigate.s009 succeeded/", "review.verify.s009 succeeded/", "review.investigate.s010 succeeded/", "review.verify.s010 succeeded/",
		"environment e44b353d678a2b55 a26534f4af3e1545", "documents 6cd0e37915e181ac 8d417c5a891273d2", "investigate 0811ea5731925461 4164875e5225f917", "interpret 7031ffa47de47a37 71915d5d7b19dc70", "verify 1237fcad33c639ca 11e709f191c805fa",
		"investigate 0811ea5731925461 0230103a64b4f629", "verify 1237fcad33c639ca 918cf1817e0dbc0c", "investigate 0811ea5731925461 e94e03021a3d0014", "verify 1237fcad33c639ca 397ec23c578ff138",
		"investigate 0811ea5731925461 0e1cf93437f0f7de", "verify 1237fcad33c639ca 7ca3104f8841d0e6", "investigate 0811ea5731925461 f20763b374cce1f5", "verify 1237fcad33c639ca 1c1d0eabbd7b895a",
		"investigate 0811ea5731925461 cda1131e3caa5a7d", "verify 1237fcad33c639ca 8f6f7c11ecdc2fee", "investigate 0811ea5731925461 fe80c514c44d1b5e", "verify 1237fcad33c639ca 32ddd0eae6007e35",
		"investigate 0811ea5731925461 cd4116252bb5622a", "verify 1237fcad33c639ca b7644da7839ba1bd", "investigate 0811ea5731925461 4404598010e25840", "verify 1237fcad33c639ca b7dfa8adeb020346",
		"investigate 0811ea5731925461 0473de8e53d975db", "verify 1237fcad33c639ca c7310f7b18225e0f", "investigate 0811ea5731925461 8ddaf8993b5bdc01", "verify 1237fcad33c639ca 632777bcd35aec7c"},
}

func TestGoReviewInputsUnchanged(t *testing.T) {
	got := map[string][]string{}
	for name, j := range goPinJobs(t) {
		got[name] = legacyRequests(t, fixIdentity(j))
	}
	gotRole := map[string][]string{
		"modules":        roleRequests(t, fixIdentity(modulesJob(t, nil)), vetStub{obs: gatewayObservation}, validScript(t)),
		"modules-worst":  roleRequests(t, fixIdentity(worstCaseJob(t, nil)), vetStub{obs: worstCaseObservations}, maxScript(t)),
		"go-build-files": roleRequests(t, fixIdentity(treeJob(t, goBuildFilesTree)), vetStub{}, goBuildFilesScript(t)),
		"modules-worst-screened": roleRequestsWith(t, fixIdentity(worstCaseJob(t, nil)),
			RoleHandler{Static: secretsStub{obs: worstCaseObservations}, MaxCalls: DefaultMaxCalls}, maxScript(t)),
	}
	if os.Getenv("CODEYE_PRINT_PINS") == "1" {
		for _, name := range sortedKeys(got) {
			t.Logf("legacy %q: %q,", name, got[name])
		}
		for _, name := range sortedKeys(gotRole) {
			t.Logf("role %q: %q,", name, gotRole[name])
		}
	}
	for name, want := range pinnedGoLegacy {
		if !slices.Equal(got[name], want) {
			t.Errorf("review-v1 requests on %s changed:\n got %q\nwant %q", name, got[name], want)
		}
	}
	for name, want := range pinnedGoRole {
		if !slices.Equal(gotRole[name], want) {
			t.Errorf("review-r1 requests on %s changed:\n got %q\nwant %q", name, gotRole[name], want)
		}
	}
	if len(pinnedGoLegacy) != len(got) || len(pinnedGoRole) != len(gotRole) {
		t.Fatalf("pins cover %d/%d legacy and %d/%d role-pack fixtures", len(pinnedGoLegacy), len(got), len(pinnedGoRole), len(gotRole))
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
