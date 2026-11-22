package testexec

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var (
	cmakeCTest  = regexp.MustCompile(`(?im)^[ \t]*(?:enable_testing\s*\(|include\s*\(\s*CTest(?:\s|\)))`)
	cmakeTest   = regexp.MustCompile(`(?im)^[ \t]*(?:add_test|gtest_discover_tests|gtest_add_tests)\s*\(`)
	cmakeSubdir = regexp.MustCompile(`(?im)^[ \t]*add_subdirectory\s*\(\s*"?([a-zA-Z0-9_.+/-]+)"?(?:\s|\))`)
)

func cmakePlans(job analysis.Job, files map[string]source.Entry) []testPlan {
	type cmakeProject struct {
		dir  string
		body []byte
	}
	var leaves []cmakeProject
	for rel, entry := range files {
		if path.Base(rel) != "CMakeLists.txt" || anypack.UnderDataDir(rel) ||
			!pack.PathInScope(rel, job.Scope) || entry.Size == nil || *entry.Size > 1<<20 {
			continue
		}
		if body, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, rel); err == nil {
			leaves = append(leaves, cmakeProject{path.Dir(rel), body})
		}
	}
	slices.SortFunc(leaves, func(a, b cmakeProject) int { return strings.Compare(a.dir, b.dir) })
	included := map[string]bool{}
	var out []testPlan
	for _, project := range leaves {
		if included[project.dir] || !cmakeCTest.Match(project.body) {
			continue
		}
		var candidate []string
		hasTests := false
		for _, child := range leaves {
			if child.dir != project.dir && !slices.Contains(candidate, child.dir) {
				continue
			}
			hasTests = hasTests || cmakeTest.Match(child.body)
			for _, sub := range cmakeSubdir.FindAllSubmatch(child.body, -1) {
				if dir := path.Join(child.dir, string(sub[1])); dir != child.dir && pack.UnderDir(dir, child.dir) {
					candidate = append(candidate, dir)
				}
			}
		}
		if !hasTests {
			continue
		}
		key := "test.cmake"
		if project.dir != "." {
			key += fmt.Sprintf(".m%02d", len(out)+1)
		}
		p := task(key, project.dir, "CMake 테스트")
		p.runner = "ctest"
		p.unsupportedScope = !pack.PathInScope(project.dir, job.Scope)
		out = append(out, p)
		for _, child := range candidate {
			included[child] = true
		}
	}
	return out
}

type preparation struct {
	name string
	spec executor.Spec
}

func (h *Handler) cmakeCommand(scratch, dir string) (executor.Spec, []preparation, string) {
	cmake, ctest := h.tool(h.tools.CMake, "cmake"), h.tool(h.tools.CTest, "ctest")
	if cmake == "" || ctest == "" {
		return executor.Spec{}, nil, toolUnavailable
	}
	buildDir := filepath.Join(scratch, "cmake-build")
	spec := executor.Spec{Env: nativeEnv(joinPath([]string{filepath.Dir(cmake), filepath.Dir(ctest)}, h.toolPath()))}
	configure, build := spec, spec
	configure.Executable, build.Executable = cmake, cmake
	configure.Args = []string{"-S", dir, "-B", buildDir, "-DBUILD_TESTING=ON", "-DCMAKE_BUILD_TYPE=Debug", "-DFETCHCONTENT_FULLY_DISCONNECTED=ON"}
	build.Args = []string{"--build", buildDir, "--config", "Debug", "--parallel", "2"}
	build.Timeout = 9 * time.Minute
	spec.Executable = ctest
	spec.Args = []string{"--test-dir", buildDir, "-C", "Debug", "--output-junit", filepath.Join(scratch, "framework-report"), "--output-on-failure", "--no-tests=error"}
	return spec, []preparation{{"test.cmake-configure", configure}, {"test.cmake-build", build}}, ""
}
