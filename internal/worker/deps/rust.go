package deps

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Rust reasons (docs/ANALYZERS.md#rust-의존성-받기).
const (
	ReasonLockfileOutdated   = "lockfile_outdated"
	ReasonDependencyNotFound = "dependency_not_found"
	ReasonManifestInvalid    = "manifest_invalid"
	ReasonRegistryAuth       = "registry_auth_required"
)

// RustCacheKey is CARGO_HOME under the dependency cache root. cargo checks
// every crate against the checksum Cargo.lock records, so one home serves
// every project of the node; cargo locks it for concurrent use.
const RustCacheKey = "rust/cargo"

// RustFetcher runs `cargo fetch` for every module cargo check plans.
type RustFetcher struct{}

// Ecosystem implements Fetcher.
func (RustFetcher) Ecosystem() string { return "rust" }

// Plan uses cargo check's module planner: one module per outermost
// Cargo.toml, keyed with cargo check's suffix.
func (RustFetcher) Plan(job analysis.Job) ([]Module, error) {
	mods, err := rustpack.PlanModules(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	out := make([]Module, 0, len(mods))
	for _, m := range mods {
		out = append(out, Module{Suffix: m.Suffix, Dir: m.Dir, Title: "의존성 받기 (Rust " + m.Dir + ")"})
	}
	return out, nil
}

// Fetch implements Fetcher.
func (RustFetcher) Fetch(ctx context.Context, env *Env, m Module) Outcome {
	out := Outcome{Manager: depdoc.Manager{Name: "cargo"}}
	fail := func(status domain.TaskStatus, state, reason string) Outcome {
		out.Status, out.State, out.Reason = status, state, reason
		return out
	}
	job, cfg := env.Job, env.Config
	// cargo reads the repository's Cargo configuration while it fetches,
	// and cargo check runs build scripts: both need the repository-code
	// grant, so without it nothing starts.
	if !job.AllowRepositoryCode {
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonDependencyConsumersNotAllowed)
	}
	if cfg.CargoExecutable == "" {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
	}
	if !cfg.RustConsumer {
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencyConsumer)
	}
	cargo, err := rustpack.New(rustpack.KindCheck, rustpack.Config{Cargo: cfg.CargoExecutable})
	if err != nil {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	if vendoredCrates(job.Snapshot.SnapshotDir, files, m.Dir) {
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonVendoredDependencies)
	}
	read := func(rel string) ([]byte, bool) {
		e, ok := files[rel]
		if !ok {
			return nil, false
		}
		b, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, e.Path)
		return b, err == nil && sha256Hex(b) == e.SHA256
	}
	if _, ok := read(govet.JoinRel(m.Dir, "Cargo.toml")); !ok {
		return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
	}
	lockRel := govet.JoinRel(m.Dir, "Cargo.lock")
	var lock []byte
	if _, listed := files[lockRel]; listed {
		b, ok := read(lockRel)
		if !ok {
			return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
		}
		lock, out.Pinned = b, true
		out.Lockfile = &depdoc.Lockfile{Path: lockRel, SHA256: files[lockRel].SHA256}
		if cratesInLock(lock) == 0 {
			zero := 0
			out.Counts.Declared = &zero
			return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencies)
		}
	}
	cache := env.CacheDir(RustCacheKey)
	if os.MkdirAll(cache, 0o700) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonCacheQuotaExceeded)
	}
	target := filepath.Join(env.TaskDir, "target")
	if os.Mkdir(target, 0o700) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	workspace := job.Snapshot.WorkspaceDir
	moduleDir := filepath.Join(workspace, filepath.FromSlash(m.Dir))
	vars := cargo.FetchEnv(cache, target, "")
	spec := func(exe string, args ...string) executor.Spec {
		return executor.Spec{Executable: exe, Args: args, WorkspaceRoot: workspace, Dir: moduleDir,
			ProtectedRoots: []string{job.Snapshot.SnapshotDir}, Env: vars, Redact: []string{cfg.CacheRoot}}
	}
	stopped := func() Outcome {
		status, reason := StopOutcome(env.Stop())
		return fail(status, depdoc.StateFailed, reason)
	}
	res, ok := env.Exec(ctx, "rustc.version", spec(cargo.Rustc(), "-vV"), "")
	if !ok {
		return stopped()
	}
	rc, parsed := rustpack.ParseRustcVV(res.Stdout.Data)
	if !parsed || !exitedZero(res) {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonToolchainUnavailable)
	}
	vars = cargo.FetchEnv(cache, target, rc.Host)
	res, ok = env.Exec(ctx, "cargo.version", spec(cargo.Cargo(), "--version"), "")
	if !ok {
		return stopped()
	}
	v, parsed := rustpack.ParseCargoVersion(res.Stdout.Data)
	if !parsed || !exitedZero(res) {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonToolchainUnavailable)
	}
	if f := strings.Fields(v); len(f) > 0 {
		v = f[0] // "1.99.1 (5f94df478 2026-08-27)" is 1.99.1
	}
	out.Manager.Version = &v
	if lock == nil {
		// No Cargo.lock: cargo resolves the versions now. The lock it writes
		// into the workspace copy is kept in the task directory, where the
		// cargo tasks of this attempt find it; the copy is removed again.
		out.Limitations = append(out.Limitations, depdoc.LimVersionsNotPinned)
		generated := filepath.Join(moduleDir, "Cargo.lock")
		defer os.Remove(generated)
		res, ok = env.Exec(ctx, "cargo.generate-lockfile", spec(cargo.Cargo(), "generate-lockfile"), cache)
		if !ok {
			return stopped()
		}
		if !exitedZero(res) {
			return fail(domain.TaskFailed, depdoc.StateFailed, classifyCargo(string(res.Stderr.Data)))
		}
		b, err := os.ReadFile(generated)
		if err != nil || analysis.WriteFileExclusive(filepath.Join(env.TaskDir, rustpack.GeneratedLockfile), b) != nil {
			return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
		}
		lock = b
		if cratesInLock(lock) == 0 {
			zero := 0
			out.Counts.Declared = &zero
			return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencies)
		}
	}
	declared := cratesInLock(lock)
	out.Counts.Declared = &declared
	out.CacheKind, out.CacheKey = depdoc.CacheShared, RustCacheKey
	// The whole CARGO_HOME is watched: the downloaded .crate files and the
	// sources unpacked from them both count.
	res, ok = env.Exec(ctx, "cargo.fetch", spec(cargo.Cargo(), "fetch", "--locked", "--target", rc.Host), cache)
	if !ok {
		return stopped()
	}
	for _, name := range []string{"Cargo.toml", "Cargo.lock"} {
		rel := govet.JoinRel(m.Dir, name)
		if e, listed := files[rel]; listed {
			b, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
			if err != nil || sha256Hex(b) != e.SHA256 {
				return fail(domain.TaskFailed, depdoc.StateFailed, ReasonWorkspaceDrift)
			}
		}
	}
	if !exitedZero(res) {
		out.Counts.Failed = declared
		return fail(domain.TaskFailed, depdoc.StateFailed, classifyCargo(string(res.Stderr.Data)))
	}
	out.Counts.Prepared = declared
	out.Status, out.State = domain.TaskSucceeded, depdoc.StatePrepared
	return out
}

// cratesInLock counts the packages of a Cargo.lock that come from a
// registry or a git repository (path packages carry no source).
func cratesInLock(lock []byte) int {
	n := 0
	for _, line := range bytes.Split(lock, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("source = ")) {
			n++
		}
	}
	return n
}

// vendoredCrates reports a Cargo configuration in the module directory or
// a snapshot ancestor that replaces a source with a directory (cargo
// vendor): the crates are in the snapshot and nothing is fetched.
func vendoredCrates(snapshotDir string, files map[string]source.Entry, dir string) bool {
	for d := dir; ; d = parentDir(d) {
		for _, n := range []string{".cargo/config.toml", ".cargo/config"} {
			rel := govet.JoinRel(d, n)
			if _, ok := files[rel]; !ok {
				continue
			}
			b, err := govet.ReadSnapshotFile(snapshotDir, rel)
			if err == nil && bytes.Contains(b, []byte("replace-with")) && bytes.Contains(b, []byte("directory")) {
				return true
			}
		}
		if d == "." {
			return false
		}
	}
}

func parentDir(d string) string {
	p := path.Dir(d)
	if p == "" || d == "." {
		return "."
	}
	return p
}

// classifyCargo maps cargo error text to a reason.
func classifyCargo(text string) string {
	t := strings.ToLower(text)
	has := func(s ...string) bool {
		for _, x := range s {
			if strings.Contains(t, x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("needs to be updated but --locked", "lock file needs to be updated", "--locked was passed"):
		return ReasonLockfileOutdated
	case has("failed to parse manifest", "failed to load manifest", "could not parse", "error inheriting"):
		return ReasonManifestInvalid
	case has("requires rustc", "is not supported by this version of cargo", "feature `edition", "but this version of cargo is"):
		return depdoc.ReasonToolchainUnavailable
	case has("401", "403 forbidden", "failed to authenticate", "no token found", "authentication required"):
		return ReasonRegistryAuth
	case has("couldn't resolve host", "could not resolve host", "failed to connect", "couldn't connect", "connection refused", "network failure", "spurious network error", "timed out", "ssl connect error", "failed to query replaced source registry", "download of config.json failed"):
		return depdoc.ReasonRegistryUnreachable
	case has("no matching package", "failed to select a version", "failed to get `", "not found", "failed to download"):
		return ReasonDependencyNotFound
	}
	return depdoc.ReasonFetchFailed
}
