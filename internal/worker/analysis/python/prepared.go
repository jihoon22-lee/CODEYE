package python

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
)

// Limitations of a type checker run that read the third-party packages the
// attempt's deps.python task prepared (docs/ANALYZERS.md#python-의존성-받기).
const (
	// LimThirdPartyPrepared replaces LimNoThirdParty: the checker resolved
	// imports against the prepared packages.
	LimThirdPartyPrepared = "third_party_packages_prepared"
	// LimThirdPartyPartial: some declared packages could not be prepared.
	LimThirdPartyPartial = "third_party_packages_partially_prepared"
	// LimPreparedNotUsed: packages were prepared but this checker could not
	// read them on this node (mypy needs the operator's interpreter and a
	// symbolic link, which Windows does not give); it ran as without them.
	LimPreparedNotUsed = "prepared_packages_not_used"
)

// DependencyModule is one module whose third-party packages a dependency
// task prepares: its directory and the key suffix ("" or ".mNN") of its
// mypy and pyright tasks.
type DependencyModule struct {
	Suffix, Dir string
}

// DependencyModules lists the modules the type checkers plan for the job's
// manifest and scope, with the same key suffixes. A placeholder module (no
// analyzable file) has nothing to check and is left out.
func DependencyModules(job analysis.Job) ([]DependencyModule, error) {
	ps, err := planTasks(MypyDescriptor(), job)
	if err != nil {
		return nil, err
	}
	var out []DependencyModule
	for _, p := range ps {
		if p.Module.Placeholder {
			continue
		}
		out = append(out, DependencyModule{Suffix: strings.TrimPrefix(p.Task.Spec.Key, MypyName), Dir: p.Module.Dir})
	}
	return out, nil
}

// SitePackagesRel is the site-packages directory of a prepared tree,
// relative to the tree, for an interpreter minor ("3.14"): the layout of a
// virtual environment, which pyright reads without starting a Python.
func SitePackagesRel(minor string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join("Lib", "site-packages")
	}
	return filepath.Join("lib", "python"+minor, "site-packages")
}

// prepared is the tree a deps.python record of this attempt names for the
// task's module.
type prepared struct {
	// tree is the absolute tree root (a virtual environment layout without
	// an interpreter), site its site-packages and minor the interpreter
	// minor of the site-packages directory name ("" on Windows).
	tree, site, minor string
	partial           bool
}

// limitation is the limitation code of a checker that read the tree.
func (p *prepared) limitation() string {
	if p.partial {
		return LimThirdPartyPartial
	}
	return LimThirdPartyPrepared
}

// preparedTree reads and validates the record of the module's deps.python
// task. nil (no record, an invalid one, one that prepared nothing, or a
// tree that is not where the record says) leaves the checker on today's
// path.
func preparedTree(job analysis.Job, cfg Config, taskKey, name, moduleDir string) *prepared {
	if cfg.DependencyCacheRoot == "" || !filepath.IsAbs(cfg.DependencyCacheRoot) {
		return nil
	}
	index := map[string]string{}
	for _, e := range job.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			index[e.Path] = e.SHA256
		}
	}
	key := depdoc.TaskKey(Ecosystem, strings.TrimPrefix(taskKey, name))
	want := depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID, Lookup: func(p string) (string, bool) { s, ok := index[p]; return s, ok }}
	rec, ok := depdoc.ReadForModule(job.Scratch, key, Ecosystem, moduleDir, want)
	if !ok || (rec.State != depdoc.StatePrepared && rec.State != depdoc.StatePartial) {
		return nil
	}
	var tree string
	switch {
	case rec.Cache.Kind == depdoc.CacheShared && rec.Cache.Key != nil:
		tree = filepath.Join(cfg.DependencyCacheRoot, filepath.FromSlash(*rec.Cache.Key))
	case rec.Cache.Kind == depdoc.CacheAttempt:
		tree = depdoc.AttemptTree(job.Scratch, key)
	default:
		return nil
	}
	p := &prepared{tree: tree, partial: rec.State == depdoc.StatePartial}
	if runtime.GOOS == "windows" {
		p.site = filepath.Join(tree, SitePackagesRel(""))
	} else {
		matches, _ := filepath.Glob(filepath.Join(tree, "lib", "python3.*", "site-packages"))
		if len(matches) != 1 {
			return nil
		}
		p.site = matches[0]
		p.minor = strings.TrimPrefix(filepath.Base(filepath.Dir(p.site)), "python")
	}
	for _, d := range []string{tree, p.site} {
		if st, err := os.Lstat(d); err != nil || !st.IsDir() {
			return nil
		}
	}
	return p
}

// PreparedSite locates the dependency tree prepared for another consumer of
// the same module, such as the worker's existing test runner.
func PreparedSite(job analysis.Job, cacheRoot, taskKey, name, moduleDir string) string {
	if tree := preparedTree(job, Config{DependencyCacheRoot: cacheRoot}, taskKey, name, moduleDir); tree != nil {
		return tree.site
	}
	return ""
}

// mypyShim writes the virtual environment mypy's --python-executable
// points at: pyvenv.cfg naming the operator's interpreter, bin/python
// linking to it and the site-packages directory linking to the prepared
// one. mypy asks that interpreter for its search paths (`python pyinfo.py
// getsearchdirs`); the dependency task removed the startup hooks (*.pth,
// sitecustomize, usercustomize) a downloaded package could run there. The
// shim stays in task scratch, so the shared tree names no interpreter.
func mypyShim(dir, interpreter string, p *prepared) (string, bool) {
	if runtime.GOOS == "windows" || interpreter == "" || !filepath.IsAbs(interpreter) || p.minor == "" {
		return "", false
	}
	bin := filepath.Join(dir, "bin")
	lib := filepath.Join(dir, "lib", "python"+p.minor)
	if os.MkdirAll(bin, 0o700) != nil || os.MkdirAll(lib, 0o700) != nil {
		return "", false
	}
	cfg := "home = " + filepath.Dir(interpreter) + "\ninclude-system-site-packages = false\n"
	if analysis.WriteFileExclusive(filepath.Join(dir, "pyvenv.cfg"), []byte(cfg)) != nil {
		return "", false
	}
	exe := filepath.Join(bin, "python")
	if os.Symlink(interpreter, exe) != nil || os.Symlink(p.site, filepath.Join(lib, "site-packages")) != nil {
		return "", false
	}
	return exe, true
}
