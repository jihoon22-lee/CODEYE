package jsts

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Lockfiles are the package lockfile names an install root is found by,
// in the order a directory holding several is resolved
// (docs/ANALYZERS.md#jsts-의존성-받기).
var Lockfiles = []string{"pnpm-lock.yaml", "yarn.lock", "npm-shrinkwrap.json", "package-lock.json", "bun.lock", "bun.lockb"}

// InstallRoot is one directory where a package manager installs the
// dependencies of JS/TS modules: the nearest directory at or above a module
// that holds a lockfile (a workspace root serves its members), or, for a
// module under no lockfile, the outermost package.json directory above it.
type InstallRoot struct {
	// Dir is the snapshot-relative directory ("." = root).
	Dir string
	// Suffix is the task key suffix of deps.javascript ("" for the root
	// directory, .m01... for the others in path order).
	Suffix string
	// Lockfiles are the lockfile paths in Dir, in Lockfiles order; empty
	// when the module has none.
	Lockfiles []string
	// Modules are the planned JS/TS modules it serves, in plan order.
	Modules []Module
}

// InstallRoots groups the planned JS/TS modules of a manifest and scope by
// install root, from the manifest only. An implicit root module without a
// package.json and without a lockfile above it has no install root.
func InstallRoots(m source.Manifest, scope domain.Scope) ([]InstallRoot, error) {
	mods, err := Modules(m, scope)
	if err != nil {
		return nil, err
	}
	files := Included(m)
	dirs, _ := PackageModules(files)
	pkg := map[string]bool{}
	for _, d := range dirs {
		pkg[d] = true
	}
	locks := func(dir string) []string {
		var out []string
		for _, n := range Lockfiles {
			if _, ok := files[Join(dir, n)]; ok {
				out = append(out, Join(dir, n))
			}
		}
		return out
	}
	byDir := map[string]*InstallRoot{}
	var order []string
	for _, mod := range mods {
		root := ""
		for d := mod.Dir; ; d = parent(d) {
			if len(locks(d)) > 0 {
				root = d
				break
			}
			if d == "." {
				break
			}
		}
		if root == "" {
			// No lockfile: the outermost package.json directory at or above
			// the module installs it (a workspace root without a lockfile).
			for d := mod.Dir; ; d = parent(d) {
				if pkg[d] {
					root = d
				}
				if d == "." {
					break
				}
			}
		}
		if root == "" {
			continue
		}
		r := byDir[root]
		if r == nil {
			r = &InstallRoot{Dir: root, Lockfiles: locks(root)}
			byDir[root] = r
			order = append(order, root)
		}
		r.Modules = append(r.Modules, mod)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i] == "." || order[j] == "." {
			return order[i] == "."
		}
		return order[i] < order[j]
	})
	out := make([]InstallRoot, 0, len(order))
	n := 0
	for _, d := range order {
		r := *byDir[d]
		if d != "." {
			n++
			r.Suffix = fmt.Sprintf(".m%02d", n)
		}
		out = append(out, r)
	}
	if len(out) > MaxModules {
		return nil, fmt.Errorf("snapshot has %d JS/TS install roots; at most %d are planned", len(out), MaxModules)
	}
	return out, nil
}

// parent is the snapshot-relative parent directory ("." stays ".").
func parent(dir string) string {
	if dir == "." || dir == "" {
		return "."
	}
	return envdoc.Dir(dir)
}

// InstallRootOf returns the install root serving the module at dir.
func InstallRootOf(roots []InstallRoot, dir string) (InstallRoot, bool) {
	for _, r := range roots {
		for _, m := range r.Modules {
			if m.Dir == dir {
				return r, true
			}
		}
	}
	return InstallRoot{}, false
}

// Limitations of a tsc or type-aware ESLint run that read the dependencies
// this attempt's deps.javascript task installed.
const (
	LimDependenciesPrepared          = "dependencies_prepared"
	LimDependenciesPartiallyPrepared = "dependencies_partially_prepared"
)

// PreparedTrees returns the node_modules directories (snapshot-relative)
// the deps.javascript task of this attempt installed for the module at
// moduleDir, read and validated again, with the run's limitation. ok is
// false when there is no usable record: the analyzer then runs exactly as
// without one.
func PreparedTrees(job analysis.Job, moduleDir string) (dirs []string, limitation string, ok bool) {
	roots, err := InstallRoots(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, "", false
	}
	root, found := InstallRootOf(roots, moduleDir)
	if !found {
		return nil, "", false
	}
	rec, ok := depdoc.ReadForModule(job.Scratch, depdoc.TaskKey("javascript", root.Suffix), "javascript", root.Dir, Expect(job))
	if !ok || !rec.TreeUsable() {
		return nil, "", false
	}
	limitation = LimDependenciesPrepared
	if rec.State != depdoc.StatePrepared {
		limitation = LimDependenciesPartiallyPrepared
	}
	return rec.InstalledDirs, limitation, true
}

// Expect binds dependency records to the job (snapshot, run, attempt and
// the manifest's files).
func Expect(job analysis.Job) depdoc.Expect {
	files := Included(job.Snapshot.Manifest)
	return depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
		Lookup: func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }}
}

// ForeignNodeModulesExcept is ForeignNodeModules that accepts the listed
// snapshot-relative node_modules directories when each is a real directory
// in the workspace copy (not a link): the dependencies this attempt
// installed there.
func ForeignNodeModulesExcept(workspace, moduleDir string, accepted []string) bool {
	ok := map[string]bool{}
	for _, a := range accepted {
		ok[filepath.Join(workspace, filepath.FromSlash(a))] = true
	}
	d := filepath.Join(workspace, filepath.FromSlash(moduleDir))
	for {
		nm := filepath.Join(d, "node_modules")
		if fi, err := os.Lstat(nm); err == nil {
			if !ok[nm] || !fi.IsDir() {
				return true
			}
		}
		up := filepath.Dir(d)
		if up == d {
			return false
		}
		d = up
	}
}
