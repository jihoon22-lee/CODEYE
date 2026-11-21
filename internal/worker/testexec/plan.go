package testexec

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	goKey     = "test.go"
	jsUnit    = "test.javascript-unit"
	jsModule  = "test.javascript-module"
	jsE2E     = "test.javascript-e2e"
	pythonKey = "test.python"
	rustKey   = "test.rust"
	noEntries = "test.unavailable"
)

type testPlan struct {
	spec             analysis.TaskSpec
	dir              string
	args             []string
	dependencies     bool
	unsupportedScope bool
}

func task(key, dir, title string, args ...string) testPlan {
	return testPlan{spec: analysis.TaskSpec{Key: key, Title: title, Stage: domain.StageTest, Required: true}, dir: dir, args: args}
}

func plans(job analysis.Job) ([]testPlan, error) {
	manifest := job.Snapshot.Manifest
	files := govet.IncludedFiles(manifest)
	goModules, err := govet.PlanModules(manifest, job.Scope, goKey, "Go 테스트")
	if err != nil {
		return nil, err
	}
	var out []testPlan
	goDirs := govet.ModuleDirs(files)
	for _, mod := range goModules {
		inScope := govet.GoFilesInScope(files, goDirs, mod.ModuleDir, mod.Patterns)
		if slices.ContainsFunc(inScope, func(p string) bool { return strings.HasSuffix(p, "_test.go") }) {
			out = append(out, task(mod.Key, mod.ModuleDir, mod.Title, mod.Patterns...))
		}
	}
	out = append(out, javascriptPlans(job)...)
	pyModules, err := (deps.PythonFetcher{}).Plan(job)
	if err != nil {
		return nil, err
	}
	for _, mod := range pyModules {
		if !pack.PathInScope(mod.Dir, job.Scope) {
			continue
		}
		// unittest's conventional test directory may be a namespace-free
		// directory, so use it directly when present.
		if start := pythonTests(mod.Dir, pyModules, files); start != "" {
			out = append(out, task(pythonKey+mod.Suffix, mod.Dir, "Python 테스트", start))
		}
	}
	rustModules, err := rustpack.PlanModules(manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	for _, mod := range rustModules {
		if pack.PathInScope(mod.Dir, job.Scope) {
			out = append(out, task(rustKey+mod.Suffix, mod.Dir, "Rust 테스트"))
		}
	}
	if len(out) == 0 {
		out = append(out, task(noEntries, ".", "기존 테스트 진입점 확인"))
	}
	return out, nil
}

func pythonTests(dir string, modules []deps.Module, files map[string]source.Entry) string {
	var moduleDirs []string
	for _, mod := range modules {
		moduleDirs = append(moduleDirs, mod.Dir)
	}
	start := ""
	for file := range files {
		base := path.Base(file)
		if !strings.HasPrefix(base, "test") || !strings.HasSuffix(base, ".py") || anypack.UnderDataDir(file) {
			continue
		}
		owner, ok := govet.OwningModule(file, moduleDirs)
		if !ok || owner != dir {
			continue
		}
		relative := strings.TrimPrefix(file, strings.TrimPrefix(dir+"/", "./"))
		if strings.HasPrefix(relative, "tests/") {
			return "tests"
		}
		start = "."
	}
	return start
}

type packageScripts struct {
	Scripts              map[string]string `json:"scripts"`
	Dependencies         json.RawMessage   `json:"dependencies"`
	DevDependencies      json.RawMessage   `json:"devDependencies"`
	OptionalDependencies json.RawMessage   `json:"optionalDependencies"`
}

func javascriptPlans(job analysis.Job) []testPlan {
	var out []testPlan
	var n int
	full := job
	full.Scope = domain.Scope{Kind: "full"}
	for _, file := range anypack.Files(full, func(p string) bool { return path.Base(p) == "package.json" }, true) {
		if file.Size == nil || *file.Size > 1<<20 || anypack.ThirdPartyDirectory(file.Path) {
			continue
		}
		body, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, file.Path)
		var pkg packageScripts
		if err != nil || json.Unmarshal(body, &pkg) != nil {
			continue
		}
		dir := path.Dir(file.Path)
		wholeModule := pack.PathInScope(dir, job.Scope)
		if !wholeModule && dir != "." && !slices.ContainsFunc(job.Scope.Paths, func(p string) bool { return pack.UnderDir(strings.TrimPrefix(p, "./"), dir) }) {
			continue
		}
		var suffix string
		if dir != "." {
			n++
			suffix = fmt.Sprintf(".m%02d", n)
		}
		for _, candidate := range []struct {
			key, title string
			scripts    []string
		}{
			{jsUnit, "JavaScript 단위 테스트", []string{"test:unit", "test"}},
			{jsModule, "JavaScript 모듈 테스트", []string{"test:module", "test:integration"}},
			{jsE2E, "JavaScript E2E 테스트", []string{"test:e2e", "e2e"}},
		} {
			for _, script := range candidate.scripts {
				if strings.TrimSpace(pkg.Scripts[script]) == "" {
					continue
				}
				p := task(candidate.key+suffix, dir, candidate.title, script)
				p.unsupportedScope = !wholeModule
				if script == "test" {
					p.spec.Title = "JavaScript 테스트"
				}
				p.dependencies = declaredDependencies(pkg.Dependencies, pkg.DevDependencies, pkg.OptionalDependencies)
				out = append(out, p)
				break
			}
		}
	}
	return out
}

func declaredDependencies(groups ...json.RawMessage) bool {
	for _, group := range groups {
		var values map[string]json.RawMessage
		if len(group) != 0 && (json.Unmarshal(group, &values) != nil || len(values) != 0) {
			return true
		}
	}
	return false
}
