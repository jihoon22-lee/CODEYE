package rustpack

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Limitations of a run that read the crates this attempt's deps.rust task
// fetched (they replace offline_scratch_cargo_home).
const (
	LimDependenciesPrepared          = "dependencies_prepared"
	LimDependenciesPartiallyPrepared = "dependencies_partially_prepared"
)

// GeneratedLockfile is the file name under the deps.rust task directory
// that holds the Cargo.lock the fetch generated for a module without one.
const GeneratedLockfile = "Cargo.lock"

// Module is one planned Cargo module: the key suffix ("" or ".mNN") the
// cargo check, clippy and deps.rust task keys share, and its directory.
type Module struct {
	Suffix, Dir string
}

// PlanModules lists the Cargo modules of the manifest and scope with
// cargo check's planner (no placeholder: a snapshot without a module has
// none).
func PlanModules(m source.Manifest, scope domain.Scope) ([]Module, error) {
	pr, err := planModules(m, scope, CheckName, KindCheck.title())
	if err != nil {
		return nil, err
	}
	out := make([]Module, 0, len(pr.Modules))
	for _, mod := range pr.Modules {
		out = append(out, Module{Suffix: strings.TrimPrefix(mod.Key, CheckName), Dir: mod.Dir})
	}
	return out, nil
}

// prepared is what a cargo task takes from a usable deps.rust record.
type prepared struct {
	home       string // the fetched CARGO_HOME
	limitation string
	// lockfile is the generated Cargo.lock to put into the module
	// directory of the workspace copy ("" when the snapshot has one).
	lockfile string
}

// preparedCrates reads the record the deps.rust task of this attempt wrote
// for the module and validates it again. ok is false without a usable
// record: the task then runs exactly as without one (scratch CARGO_HOME).
func (a *Analyzer) preparedCrates(job analysis.Job, key, name, moduleDir string) (prepared, bool) {
	root := a.cfg.DependencyCacheRoot
	if root == "" || !filepath.IsAbs(root) {
		return prepared{}, false
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	want := depdoc.Expect{Snapshot: job.Snapshot.Digest, RunID: job.RunID, AttemptID: job.AttemptID,
		Lookup: func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }}
	depKey := depdoc.TaskKey("rust", strings.TrimPrefix(key, name))
	rec, ok := depdoc.ReadForModule(job.Scratch, depKey, "rust", moduleDir, want)
	if !ok || !rec.Usable() {
		return prepared{}, false
	}
	p := prepared{home: filepath.Join(root, filepath.FromSlash(*rec.Cache.Key)), limitation: LimDependenciesPrepared}
	if rec.State != depdoc.StatePrepared {
		p.limitation = LimDependenciesPartiallyPrepared
	}
	if _, inSnapshot := files[govet.JoinRel(moduleDir, "Cargo.lock")]; !inSnapshot {
		lock := filepath.Join(filepath.Dir(depdoc.Path(job.Scratch, depKey)), GeneratedLockfile)
		if fi, err := os.Lstat(lock); err != nil || !fi.Mode().IsRegular() {
			return prepared{}, false
		}
		p.lockfile = lock
	}
	return p, true
}

// placeLockfile copies the generated Cargo.lock into the module directory
// of the workspace copy, where cargo --locked reads it. The returned
// function removes it again.
func placeLockfile(src, moduleDir string) (func(), error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(moduleDir, "Cargo.lock")
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, errors.New("Cargo.lock already in the workspace copy")
		}
		return nil, err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(dst)
		return nil, werr
	}
	return func() { _ = os.Remove(dst) }, nil
}

// FetchEnv is the environment deps.rust runs cargo with: the analyzer's
// explicit environment with CARGO_HOME at the dependency cache and the
// network allowed (no CARGO_NET_OFFLINE). host is "" until `rustc -vV` has
// been read.
func (a *Analyzer) FetchEnv(home, target, host string) map[string]string {
	env := a.env(home, target, host)
	delete(env, "CARGO_NET_OFFLINE")
	return env
}

// Rustc is the rustc next to the configured cargo; Cargo the cargo as the
// operator named it; ToolchainDir their directory.
func (a *Analyzer) Rustc() string        { return a.rustc }
func (a *Analyzer) Cargo() string        { return a.cargo }
func (a *Analyzer) ToolchainDir() string { return a.tcDir }
