package python

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// maxModules bounds the Python tasks one analyzer plans for a snapshot.
const maxModules = 64

// ReasonNoAnalyzableFiles: Python source is in scope (pack.SourceFiles) but
// every file lies in a directory the pack never analyzes (a virtual
// environment, site-packages, __pycache__): the task is planned and ends
// unsupported so the gap stays visible.
const ReasonNoAnalyzableFiles = "no_analyzable_python_files"

// moduleManifest reports a file name that makes its directory a Python
// module, the same names env.discover uses (docs/ARCHITECTURE.md): pyproject.toml,
// setup.cfg, .python-version and requirements*.txt.
func moduleManifest(base string) bool {
	switch base {
	case "pyproject.toml", "setup.cfg", ".python-version":
		return true
	}
	return strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt")
}

// skippedDir reports a directory segment the pack never analyzes: the data
// and vendored directories of pack.SourceFiles (hidden, testdata, vendor,
// node_modules) and Python environment or cache directories that hold
// installed third-party code, not the project's.
func skippedDir(seg string) bool {
	switch seg {
	case "testdata", "vendor", "node_modules", "__pycache__", "venv", "site-packages", "__pypackages__":
		return true
	}
	return strings.HasPrefix(seg, ".")
}

// ExcludedDirs are the directory names (besides hidden ones) whose files
// the pack never hands to a tool; ruff and bandit receive them as
// exclusions so their own discovery skips the same directories.
var ExcludedDirs = []string{"__pycache__", "__pypackages__", "node_modules", "site-packages", "testdata", "vendor", "venv"}

func inSkippedDir(p string) bool {
	segs := strings.Split(p, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], skippedDir)
}

// isSource reports a Python source or stub file name.
// dotTargets prefixes module-relative path arguments with "./" so a name
// like -x.py never reads as an option (the argv also ends options with
// "--"); "." stays as is.
func dotTargets(targets []string) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		if t == "." {
			out = append(out, t)
		} else {
			out = append(out, "./"+t)
		}
	}
	return out
}

// safeNested reports whether every nested module directory can be written
// into ruff's and bandit's comma-separated glob exclusion lists unchanged.
func safeNested(nested []string) bool {
	for _, n := range nested {
		if strings.ContainsAny(n, ",*?[]{}") {
			return false
		}
	}
	return true
}

func isSource(p string) bool { return strings.HasSuffix(p, ".py") || strings.HasSuffix(p, ".pyi") }

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

func underDir(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

func relTo(p, dir string) string {
	if dir == "." {
		return p
	}
	if p == dir {
		return "."
	}
	return strings.TrimPrefix(p, dir+"/")
}

func joinRel(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	if name == "." {
		return dir
	}
	return dir + "/" + name
}

// includedFiles maps snapshot-relative paths of included regular files to
// manifest entries.
func includedFiles(m source.Manifest) map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range m.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			out[e.Path] = e
		}
	}
	return out
}

// moduleDirs lists the Python module directories (outside skipped
// directories), sorted.
func moduleDirs(files map[string]source.Entry) []string {
	seen := map[string]bool{}
	for p := range files {
		if moduleManifest(path.Base(p)) && !inSkippedDir(p) {
			seen[dirOf(p)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// owningModule is the deepest module directory containing p; "." (the
// implicit root module) when none does.
func owningModule(p string, modules []string) string {
	best := "."
	for _, m := range modules {
		if m != "." && underDir(p, m) && (best == "." || len(m) > len(best)) {
			best = m
		}
	}
	return best
}

func inScope(p string, scope domain.Scope) bool {
	if scope.Kind == "" || scope.Kind == "full" {
		return true
	}
	for _, s := range scope.Paths {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "./"), "/")
		if s == "" || s == "." || p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

// module is one planned Python module of a snapshot and scope.
type module struct {
	Dir string
	// Files are the snapshot-relative Python sources (.py, .pyi) of the
	// module in scope, outside nested modules and skipped directories,
	// sorted. They are what mypy and pyright receive explicitly.
	Files []string
	// Targets are the module-relative tool scope arguments (ruff, bandit):
	// "." for the whole module or the scope paths inside it.
	Targets []string
	// Nested are the module-relative directories of nested modules, which
	// ruff and bandit must exclude.
	Nested []string
	// Placeholder: Python source is in scope but none of it is analyzable.
	Placeholder bool
}

// planModules is deterministic for a manifest and scope; Run re-derives it.
func planModules(m source.Manifest, scope domain.Scope) ([]module, error) {
	if scope.Kind != "" && scope.Kind != "full" && scope.Kind != "paths" {
		return nil, fmt.Errorf("scope kind %q is not supported", scope.Kind)
	}
	for _, p := range scope.Paths {
		if err := domain.ValidateEvidencePath(p); err != nil {
			return nil, fmt.Errorf("scope path: %w", err)
		}
	}
	files := includedFiles(m)
	modules := moduleDirs(files)
	byModule := map[string][]string{}
	for p := range files {
		// Hidden files are skipped like hidden directories (ruff and bandit
		// exclude both).
		if !isSource(p) || inSkippedDir(p) || strings.HasPrefix(path.Base(p), ".") || !inScope(p, scope) {
			continue
		}
		mod := owningModule(p, modules)
		byModule[mod] = append(byModule[mod], p)
	}
	if len(byModule) == 0 {
		if pack.SourceFiles(m, scope)["python"] > 0 {
			return []module{{Dir: ".", Files: []string{}, Targets: []string{}, Nested: []string{}, Placeholder: true}}, nil
		}
		return nil, nil
	}
	dirs := make([]string, 0, len(byModule))
	for d := range byModule {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	if len(dirs) > maxModules {
		return nil, fmt.Errorf("snapshot has %d Python modules; at most %d are planned", len(dirs), maxModules)
	}
	out := make([]module, 0, len(dirs))
	for _, d := range dirs {
		mod := module{Dir: d, Files: byModule[d], Nested: []string{}}
		sort.Strings(mod.Files)
		for _, n := range modules {
			if n != d && underDir(n, d) {
				mod.Nested = append(mod.Nested, relTo(n, d))
			}
		}
		mod.Targets = targets(d, scope, files, modules)
		out = append(out, mod)
	}
	return out, nil
}

// targets are the module-relative scope arguments of module d.
func targets(d string, scope domain.Scope, files map[string]source.Entry, modules []string) []string {
	if scope.Kind == "" || scope.Kind == "full" {
		return []string{"."}
	}
	set := map[string]bool{}
	for _, s := range scope.Paths {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "./"), "/")
		switch {
		case s == "" || s == "." || underDir(d, s):
			// The scope covers the whole module.
			return []string{"."}
		case underDir(s, d):
			if _, isFile := files[s]; (isFile && !isSource(s)) || owningModule(s, modules) != d {
				// A scope path inside a nested module is that module's
				// target, never this one's.
				continue
			}
			set[relTo(s, d)] = true
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// planned is one task of an analyzer with its module.
type planned struct {
	Task   pack.Task
	Module module
}

// planFor returns the tool-independent planner of the analyzer d: one task
// per Python module, keyed <name> for the root module and <name>.mNN for the
// others (sorted by directory), like go vet.
func planFor(d pack.Descriptor) pack.PlanFunc {
	return func(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ps, err := planTasks(d, job)
		if err != nil {
			return nil, err
		}
		out := make([]pack.Task, len(ps))
		for i, p := range ps {
			out[i] = p.Task
		}
		return out, nil
	}
}

func planTasks(d pack.Descriptor, job analysis.Job) ([]planned, error) {
	mods, err := planModules(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	out := make([]planned, 0, len(mods))
	index := 0
	for _, m := range mods {
		key := d.Name
		if m.Dir != "." {
			index++
			key = fmt.Sprintf("%s.m%02d", d.Name, index)
		}
		n := len(toolFiles(d, m.Files))
		title := d.Name + " " + strings.Join(m.Targets, " ") + " in " + m.Dir
		if m.Placeholder {
			title = d.Name + " (no analyzable Python files)"
		}
		out = append(out, planned{Module: m, Task: pack.Task{
			Spec:   analysis.TaskSpec{Key: key, Required: d.Required, Title: title, Stage: domain.StageStaticAnalysis},
			Module: m.Dir, Patterns: append([]string{}, m.Targets...), FilesInScope: &n,
			// No Python task runs repository-controlled code (docs/ANALYZERS.md#저장소-설정과-interpreter).
			RepositoryCode: false,
		}})
	}
	return out, nil
}

// toolFiles are the module files the tool of d analyzes: bandit reads .py
// files only (stubs hold no code to test), the other tools both kinds.
func toolFiles(d pack.Descriptor, files []string) []string {
	if d.Tool != BanditTool {
		return files
	}
	out := []string{}
	for _, f := range files {
		if strings.HasSuffix(f, ".py") {
			out = append(out, f)
		}
	}
	return out
}

func findPlanned(ps []planned, key string) *planned {
	for i := range ps {
		if ps[i].Task.Spec.Key == key {
			return &ps[i]
		}
	}
	return nil
}
