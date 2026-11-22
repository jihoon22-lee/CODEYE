package testexec

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
)

const maxJavaReports = 1024

func javaPlans(job analysis.Job) ([]testPlan, error) {
	modules, err := javaproject.Scan(job.Snapshot.SnapshotDir, job.Snapshot.Manifest)
	if err != nil {
		return nil, err
	}
	type build struct {
		system, root string
		members      []string
		supported    bool
	}
	var groups []build
	index := map[string]int{}
	for _, mod := range modules {
		if mod.BuildSystem != "maven" && mod.BuildSystem != "gradle" {
			continue
		}
		id := mod.BuildSystem + " " + mod.BuildRoot
		at, ok := index[id]
		if !ok {
			at, index[id] = len(groups), len(groups)
			groups = append(groups, build{system: mod.BuildSystem, root: mod.BuildRoot})
		}
		group := &groups[at]
		group.members = append(group.members, mod.Dir)
		for _, source := range mod.Files {
			inTestRoot := pack.UnderDir(source.Path, path.Join(mod.Dir, "src/test")) ||
				pack.UnderDir(source.Path, path.Join(mod.Dir, "src/integrationTest"))
			if inTestRoot && (pack.PathInScope(source.Path, job.Scope) || pack.PathInScope(mod.Dir, job.Scope)) {
				group.supported = true
			}
		}
	}
	var out []testPlan
	for _, group := range groups {
		if !group.supported {
			continue
		}
		key := "test." + group.system
		if group.root != "." {
			key += fmt.Sprintf(".m%02d", len(out)+1)
		}
		p := task(key, group.root, "Java 테스트", group.members...)
		p.runner = group.system
		p.unsupportedScope = !pack.PathInScope(group.root, job.Scope)
		out = append(out, p)
	}
	return out, nil
}

func (h *Handler) javaCommand(p testPlan, scratch string) (executor.Spec, string) {
	java := h.tool(h.tools.Java, "java")
	if java == "" {
		return executor.Spec{}, toolUnavailable
	}
	jdk := filepath.Dir(filepath.Dir(java))
	if regularPath(filepath.Join(jdk, "bin", "javac"), true) == "" {
		return executor.Spec{}, toolUnavailable
	}
	env := nativeEnv(joinPath([]string{filepath.Join(jdk, "bin")}, h.toolPath()))
	env["JAVA_HOME"] = jdk
	switch p.runner {
	case "maven":
		maven := h.tool(h.tools.Maven, "mvn")
		if maven == "" {
			return executor.Spec{}, toolUnavailable
		}
		repo := filepath.Join(scratch, "maven-repository")
		if h.tools.MavenRepository != "" {
			repo = h.tools.MavenRepository
			if !validCacheDir(repo) {
				return executor.Spec{}, dependenciesUnavailable
			}
		}
		return executor.Spec{Executable: maven, Args: []string{"--batch-mode", "--offline", "--no-transfer-progress",
			"-Dstyle.color=never", "-Dmaven.repo.local=" + repo, "verify"}, Env: env, Redact: []string{repo}}, ""
	case "gradle":
		gradle := h.tool(h.tools.Gradle, "gradle")
		if gradle == "" {
			return executor.Spec{}, toolUnavailable
		}
		home := filepath.Join(scratch, "gradle-home")
		if h.tools.GradleHome != "" {
			home = h.tools.GradleHome
			if !validCacheDir(home) {
				return executor.Spec{}, dependenciesUnavailable
			}
		}
		env["GRADLE_USER_HOME"] = home
		return executor.Spec{Executable: gradle, Args: []string{"--offline", "--no-daemon", "--console=plain", "--no-build-cache",
			"--no-configuration-cache", "--rerun-tasks", "--max-workers=2", "test"}, Env: env, Redact: []string{home}}, ""
	}
	return executor.Spec{}, toolUnavailable
}

func validCacheDir(p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

type javaReport struct{ path, digest string }

// Examine only standard per-module report directories. The pre-run inventory
// prevents reports already present in the source or workspace from contributing.
func javaReports(workspace string, p testPlan) (reports []javaReport, complete bool) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, false
	}
	defer root.Close()
	dirs := []string{"target/surefire-reports", "target/failsafe-reports"}
	if p.runner == "gradle" {
		dirs = []string{"build/test-results/test"}
	}
	var size int
	for _, mod := range p.args {
		for _, suffix := range dirs {
			dir := path.Join(mod, suffix)
			info, err := root.Lstat(dir)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || !info.IsDir() {
				return nil, false
			}
			f, err := root.Open(dir)
			if err != nil {
				return nil, false
			}
			entries, err := f.ReadDir(maxJavaReports + 1)
			f.Close()
			if err != nil && err != io.EOF || len(entries) > maxJavaReports {
				return nil, false
			}
			slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), "TEST-") || !strings.HasSuffix(entry.Name(), ".xml") {
					continue
				}
				rel := path.Join(dir, entry.Name())
				body, ok := readTestReport(root, rel)
				size += len(body)
				if !ok || len(reports) >= maxJavaReports || size > 32<<20 {
					return nil, false
				}
				reports = append(reports, javaReport{rel, domain.Digest(body)})
			}
		}
	}
	return reports, true
}

func javaResults(workspace string, p testPlan, before []javaReport) (out testResults) {
	reports, ok := javaReports(workspace, p)
	if !ok {
		return out
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return out
	}
	defer root.Close()
	seen := make(map[string]string, len(before))
	for _, report := range before {
		seen[report.path] = report.digest
	}
	for _, report := range reports {
		if seen[report.path] == report.digest {
			continue
		}
		body, ok := readTestReport(root, report.path)
		if !ok || domain.Digest(body) != report.digest {
			return testResults{}
		}
		part, valid := xmlResults(body, p.runner)
		if !valid {
			return testResults{}
		}
		out.merge(part)
	}
	return out
}
