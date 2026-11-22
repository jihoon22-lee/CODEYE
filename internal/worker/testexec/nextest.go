package testexec

import (
	"bytes"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const nextestKey = "test.rust-nextest"

func rustTestPlans(files map[string]source.Entry, mod rustpack.Module) []testPlan {
	spec := task(rustKey+mod.Suffix, mod.Dir, "Rust 테스트")
	spec.runner = "cargo"
	if _, ok := files[path.Join(mod.Dir, ".config/nextest.toml")]; !ok {
		return []testPlan{spec}
	}
	nextest := task(nextestKey+mod.Suffix, mod.Dir, "Rust nextest")
	nextest.runner = "nextest"
	if rustDocTargets(mod.Dir, files) {
		spec.args = []string{"--doc"}
		spec.spec.Title = "Rust 문서 테스트"
		return []testPlan{nextest, spec}
	}
	return []testPlan{nextest}
}

func rustDocTargets(dir string, files map[string]source.Entry) bool {
	for rel := range files {
		if !pack.UnderDir(rel, dir) {
			continue
		}
		if strings.HasSuffix(rel, "/src/lib.rs") || rel == "src/lib.rs" {
			return true
		}
	}
	return false
}

func (h *Handler) nextestCommand(job analysis.Job, p testPlan, scratch string, spec executor.Spec) (executor.Spec, string) {
	n := h.tool(h.tools.Nextest, "cargo-nextest")
	if n == "" {
		return executor.Spec{}, toolUnavailable
	}
	body, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, path.Join(p.dir, ".config/nextest.toml"))
	if err != nil || len(body) > 1<<20 || bytes.Contains(body, []byte("codeye-test-evidence")) {
		return executor.Spec{}, "test_results_unavailable"
	}
	config := filepath.Join(scratch, "nextest.toml")
	report := filepath.Join(scratch, "framework-report")
	body = append(body, ("\n[profile.codeye-test-evidence.junit]\npath = " + strconv.Quote(report) + "\n")...)
	if err := analysis.WriteFileExclusive(config, body); err != nil {
		return executor.Spec{}, "scratch_unavailable"
	}
	params := []string{"nextest", "run", "--config-file", config, "--profile", "codeye-test-evidence",
		"--retries", "0", "--no-fail-fast", "--no-tests=warn"}
	spec.Executable = n
	// The cargo base arguments begin with "test", which nextest replaces.
	spec.Args = append(params, spec.Args[1:]...)
	return spec, ""
}
