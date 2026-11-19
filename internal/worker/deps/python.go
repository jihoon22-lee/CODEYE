package deps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/python"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// PythonManagers are the package manager flags of the Python fetch:
// --python (the interpreter whose pip installs, and which uv installs for)
// and --uv.
var PythonManagers = []string{"python", "uv"}

// Python reasons (docs/ANALYZERS.md#python-의존성-받기).
const (
	ReasonPyDynamicDependencies   = "dynamic_dependencies_not_read"
	ReasonPyNoBinaryDistribution  = "no_binary_distribution"
	ReasonPyNoMatchingDistibution = "no_matching_distribution"
	ReasonPyDependencyNotFound    = "dependency_not_found"
	ReasonPyResolutionConflict    = "resolution_conflict"
	ReasonPyRegistryAuthRequired  = "registry_auth_required"
	ReasonPyLockfileUnsupported   = "lockfile_format_unsupported"
	ReasonPyHashMismatch          = "hash_mismatch"
	ReasonPyProjectUnreadable     = "pyproject_unreadable"
	ReasonPyLockExportFailed      = "lockfile_export_failed"
	// ReasonPyTooManyRequirements: pyproject.toml expands past
	// pyprojectRequirementLimit requirements (include-group fan-out).
	ReasonPyTooManyRequirements = "too_many_requirements"
)

// pyprojectRequirementLimit bounds what the pyproject reader expands; the
// embedded script carries the same number.
const pyprojectRequirementLimit = 10000

// Inputs the Python fetch leaves out (dropped_inputs reasons).
const (
	DropPyEditable       = "editable_requirement_not_fetched"
	DropPyLocal          = "local_requirement_not_fetched"
	DropPyVCS            = "vcs_requirement_not_fetched"
	DropPyOption         = "installer_option_ignored"
	DropPyNotUnderstood  = "requirement_not_understood"
	DropPyIncludeMissing = "included_file_not_in_snapshot"
	DropPyUVMissing      = "uv_not_configured"
	DropPyPoetry         = "poetry_dependencies_not_read"
)

// Limitations of a Python fetch record.
const (
	// LimPyHashesNotChecked: some requirements carried hashes and others did
	// not; the installer would refuse the mix, so no hash was passed.
	LimPyHashesNotChecked = "requirement_hashes_not_checked"
	// LimPyStartupHooksRemoved: *.pth, sitecustomize or usercustomize files
	// of the installed packages were removed so no interpreter that reads
	// the tree runs them.
	LimPyStartupHooksRemoved = "interpreter_startup_hooks_removed"
	// LimPyTreeReused: a fully pinned tree prepared by an earlier run was
	// used as it was.
	LimPyTreeReused = "prepared_tree_reused"
)

// pythonMaxRetries bounds the installs after dropping a requirement that
// has no wheel or no distribution.
const pythonMaxRetries = 3

// maxDroppedInputs bounds dropped_inputs of one record.
const maxDroppedInputs = 100

// pythonCacheDir is the Python part of the dependency cache: the uv or pip
// download cache and the shared trees.
const pythonCacheDir = "python"

// PythonFetcher installs the third-party packages of every module mypy and
// pyright plan into a tree they read, wheels only.
type PythonFetcher struct{}

// Ecosystem implements Fetcher.
func (PythonFetcher) Ecosystem() string { return "python" }

// Plan uses the type checkers' module planner, keyed with their suffix.
func (PythonFetcher) Plan(job analysis.Job) ([]Module, error) {
	mods, err := python.DependencyModules(job)
	if err != nil {
		return nil, err
	}
	out := make([]Module, 0, len(mods))
	for _, m := range mods {
		out = append(out, Module{Suffix: m.Suffix, Dir: m.Dir, Title: "의존성 받기 (Python " + m.Dir + ")"})
	}
	return out, nil
}

// requirement is one line CODEYE writes into its own requirements file.
type requirement struct {
	// name is the normalized project name ("" for a bare wheel URL).
	name string
	// line is name[extras] spec ; marker, or name @ url ; marker.
	line   string
	hashes []string
	pinned bool
}

// pyInputs are the requirements read for one module.
type pyInputs struct {
	reqs        []requirement
	constraints []requirement
	dropped     []depdoc.Dropped
	lockfile    *depdoc.Lockfile
	fromLock    bool
	// unsupported is the reason when an input exists that cannot be read
	// at all (a poetry or Pipfile lock, dynamic dependencies).
	unsupported string
}

func (in *pyInputs) drop(p, reason string) {
	if len(in.dropped) < maxDroppedInputs {
		in.dropped = append(in.dropped, depdoc.Dropped{Path: p, Reason: reason})
	}
}

// pythonEnv is the environment of the operator's interpreter: no user site,
// no bytecode writes.
func pythonEnv(exe string) map[string]string {
	vars := map[string]string{"PATH": filepath.Dir(exe), "LANG": "C.UTF-8", "PYTHONNOUSERSITE": "1", "PYTHONDONTWRITEBYTECODE": "1"}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			vars["SystemRoot"] = v
		}
	}
	return vars
}

// uvEnv is the environment of uv: it never downloads an interpreter, and
// trusts bundle when one is set.
func uvEnv(uv, bundle string) map[string]string {
	vars := executor.AddCAEnv(map[string]string{"PATH": filepath.Dir(uv), "LANG": "C.UTF-8", "UV_PYTHON_DOWNLOADS": "never", "NO_COLOR": "1"}, bundle)
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			vars["SystemRoot"] = v
		}
	}
	return vars
}

const pyVersionScript = `import sys; print("%d.%d.%d" % sys.version_info[:3])`

// pyprojectScript reads pyproject.toml from stdin with the standard
// library's TOML parser (-I -S: no site-packages, nothing of the project is
// imported) and prints the static requirements: [project] dependencies,
// every optional dependency group and every PEP 735 dependency group with
// include-group expanded. Each group is expanded once and the reader stops
// with too_many_requirements past pyprojectRequirementLimit requirements.
const pyprojectScript = `import json, sys
LIMIT = 10000
class TooMany(Exception):
    pass
def main():
    try:
        import tomllib
    except ImportError:
        return {"error": "tomllib_unavailable"}
    try:
        d = tomllib.loads(sys.stdin.buffer.read().decode("utf-8"))
    except Exception:
        return {"error": "unparsable"}
    out = {"project": False, "dynamic": [], "requirements": [], "invalid": 0, "poetry": False}
    def add(v):
        if isinstance(v, str):
            if len(out["requirements"]) >= LIMIT:
                raise TooMany()
            out["requirements"].append(v)
        else:
            out["invalid"] += 1
    p = d.get("project")
    if isinstance(p, dict):
        out["project"] = True
        dyn = p.get("dynamic", [])
        if isinstance(dyn, list):
            out["dynamic"] = [x for x in dyn if isinstance(x, str)]
        deps = p.get("dependencies", [])
        for v in deps if isinstance(deps, list) else []:
            add(v)
        opt = p.get("optional-dependencies", {})
        if isinstance(opt, dict):
            for k in sorted(opt):
                for v in opt[k] if isinstance(opt[k], list) else []:
                    add(v)
    groups = d.get("dependency-groups", {})
    if isinstance(groups, dict):
        memo = {}
        def expand(name, stack):
            if name in memo:
                return memo[name]
            if name in stack or not isinstance(groups.get(name), list):
                out["invalid"] += 1
                return []
            got = []
            for v in groups[name]:
                if isinstance(v, dict) and isinstance(v.get("include-group"), str):
                    got.extend(expand(v["include-group"], stack | {name}))
                elif isinstance(v, str):
                    got.append(v)
                else:
                    out["invalid"] += 1
                if len(got) > LIMIT:
                    raise TooMany()
            memo[name] = got
            return got
        for k in sorted(groups):
            for v in expand(k, frozenset()):
                add(v)
    tool = d.get("tool")
    if isinstance(tool, dict) and isinstance(tool.get("poetry"), dict) and "dependencies" in tool["poetry"]:
        out["poetry"] = True
    return out
def guarded():
    try:
        return main()
    except (TooMany, RecursionError):
        return {"error": "too_many_requirements"}
print(json.dumps(guarded()))
`

type pyprojectFacts struct {
	Error        string   `json:"error"`
	Project      bool     `json:"project"`
	Dynamic      []string `json:"dynamic"`
	Requirements []string `json:"requirements"`
	Invalid      int      `json:"invalid"`
	Poetry       bool     `json:"poetry"`
}

// Fetch implements Fetcher.
func (PythonFetcher) Fetch(ctx context.Context, env *Env, m Module) Outcome {
	cfg := env.Config
	out := Outcome{Manager: depdoc.Manager{Name: "pip"}}
	if cfg.UV != "" {
		out.Manager.Name = "uv"
	}
	fail := func(status domain.TaskStatus, state, reason string) Outcome {
		out.Status, out.State, out.Reason = status, state, reason
		return out
	}
	if !cfg.PythonConsumers {
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencyConsumer)
	}
	if cfg.Python == "" || !filepath.IsAbs(cfg.Python) || (cfg.UV != "" && !filepath.IsAbs(cfg.UV)) {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
	}
	job := env.Job
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	work := filepath.Join(env.TaskDir, "work")
	if os.Mkdir(work, 0o700) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	// Every process starts in an empty scratch directory: nothing of the
	// project is on a path an installer or interpreter searches.
	spec := func(exe string, vars map[string]string, stdin []byte, args ...string) executor.Spec {
		return executor.Spec{Executable: exe, Args: args, WorkspaceRoot: work, Dir: work, ProtectedRoots: []string{job.Snapshot.SnapshotDir},
			Env: vars, Stdin: stdin, Redact: []string{cfg.CacheRoot, env.TaskDir}}
	}
	stopped := func() Outcome {
		status, reason := StopOutcome(env.Stop())
		return fail(status, depdoc.StateFailed, reason)
	}
	pyVars := executor.AddCAEnv(pythonEnv(cfg.Python), cfg.CABundle)
	res, ok := env.Exec(ctx, "python.version", spec(cfg.Python, pyVars, nil, "-I", "-S", "-c", pyVersionScript))
	if !ok {
		return stopped()
	}
	pyVersion := strings.TrimSpace(string(res.Stdout.Data))
	minor, valid := pythonMinor(pyVersion)
	if !exitedZero(res) || !valid {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonToolchainUnavailable)
	}
	if cfg.UV != "" {
		res, ok = env.Exec(ctx, "uv.version", spec(cfg.UV, uvEnv(cfg.UV, cfg.CABundle), nil, "--version"))
		if !ok {
			return stopped()
		}
		v, vok := parseUVVersion(res.Stdout.Data)
		if !exitedZero(res) || !vok {
			return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
		}
		out.Manager.Version = &v
	} else {
		res, ok = env.Exec(ctx, "pip.version", spec(cfg.Python, pyVars, nil, "-I", "-m", "pip", "--version"))
		if !ok {
			return stopped()
		}
		v, vok := parsePipVersion(res.Stdout.Data)
		if !exitedZero(res) || !vok {
			// The interpreter has no pip and no uv is configured.
			return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
		}
		out.Manager.Version = &v
	}

	read := func(rel string) ([]byte, bool) {
		e, ok := files[rel]
		if !ok {
			return nil, false
		}
		b, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, e.Path)
		if err != nil || sha256Hex(b) != e.SHA256 {
			return nil, false
		}
		return b, true
	}
	in := &pyInputs{}
	lock := govet.JoinRel(m.Dir, "uv.lock")
	_, hasLock := files[lock]
	switch {
	case hasLock && cfg.UV != "":
		if _, ok := read(lock); !ok {
			return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
		}
		args := []string{"export", "--frozen", "--offline", "--no-config", "--project", filepath.Join(job.Snapshot.WorkspaceDir, filepath.FromSlash(m.Dir)),
			"--format", "requirements-txt", "--all-extras", "--all-groups", "--no-emit-project", "--no-emit-workspace", "--no-emit-local",
			"--no-header", "--no-annotate", "--cache-dir", filepath.Join(env.TaskDir, "uv-export-cache")}
		res, ok = env.Exec(ctx, "uv.export", spec(cfg.UV, uvEnv(cfg.UV, cfg.CABundle), nil, args...))
		if !ok {
			return stopped()
		}
		if pyDrifted(job, files, m.Dir) {
			return fail(domain.TaskFailed, depdoc.StateFailed, ReasonWorkspaceDrift)
		}
		if !exitedZero(res) {
			return fail(domain.TaskFailed, depdoc.StateFailed, ReasonPyLockExportFailed)
		}
		in.fromLock = true
		in.lockfile = &depdoc.Lockfile{Path: lock, SHA256: files[lock].SHA256}
		parseRequirementsText(in, lock, res.Stdout.Data, nil, false)
	default:
		if hasLock {
			in.drop(lock, DropPyUVMissing)
		}
		for _, name := range []string{"poetry.lock", "Pipfile.lock", "Pipfile", "pdm.lock"} {
			if _, ok := files[govet.JoinRel(m.Dir, name)]; ok {
				in.drop(govet.JoinRel(m.Dir, name), ReasonPyLockfileUnsupported)
				in.unsupported = ReasonPyLockfileUnsupported
			}
		}
		if pp := govet.JoinRel(m.Dir, "pyproject.toml"); files[pp].Path != "" {
			b, ok := read(pp)
			if !ok {
				return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
			}
			res, ok = env.Exec(ctx, "python.pyproject", spec(cfg.Python, pyVars, b, "-I", "-S", "-c", pyprojectScript))
			if !ok {
				return stopped()
			}
			var facts pyprojectFacts
			if exitedZero(res) && json.Unmarshal(res.Stdout.Data, &facts) == nil && facts.Error == ReasonPyTooManyRequirements {
				return fail(domain.TaskFailed, depdoc.StateFailed, ReasonPyTooManyRequirements)
			}
			if !exitedZero(res) || json.Unmarshal(res.Stdout.Data, &facts) != nil || facts.Error != "" {
				in.drop(pp, ReasonPyProjectUnreadable)
			} else {
				if slices.Contains(facts.Dynamic, "dependencies") || slices.Contains(facts.Dynamic, "optional-dependencies") {
					in.drop(pp, ReasonPyDynamicDependencies)
					if in.unsupported == "" {
						in.unsupported = ReasonPyDynamicDependencies
					}
				}
				if facts.Poetry && !facts.Project {
					in.drop(pp, DropPyPoetry)
					in.unsupported = ReasonPyLockfileUnsupported
				}
				for range facts.Invalid {
					in.drop(pp, DropPyNotUnderstood)
				}
				for _, r := range facts.Requirements {
					addRequirement(in, pp, r, false)
				}
			}
		}
		for _, rel := range requirementFiles(files, m.Dir) {
			readRequirementsFile(in, rel, read, files, map[string]bool{}, false)
		}
	}
	out.DroppedInputs, out.Lockfile = in.dropped, in.lockfile
	reqs := in.reqs
	if len(reqs) == 0 {
		zero := 0
		out.Counts.Declared = &zero
		if in.unsupported != "" {
			return fail(domain.TaskUnsupported, depdoc.StateSkipped, in.unsupported)
		}
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencies)
	}
	hashed := 0
	for _, r := range reqs {
		if len(r.hashes) > 0 {
			hashed++
		}
	}
	if hashed > 0 && hashed < len(reqs) {
		for i := range reqs {
			reqs[i].hashes = nil
		}
		out.Limitations = append(out.Limitations, LimPyHashesNotChecked)
	}
	out.Pinned = in.fromLock || !slices.ContainsFunc(reqs, func(r requirement) bool { return !r.pinned })
	if !out.Pinned {
		out.Limitations = append(out.Limitations, depdoc.LimVersionsNotPinned)
	}
	declared := len(reqs)
	out.Counts.Declared = &declared

	inst := pyInstall{env: env, cfg: cfg, minor: minor, spec: spec, pyVars: pyVars, constraints: in.constraints}
	key := ""
	if out.Pinned {
		key = treeKey(reqs, in.constraints, pyVersion, out.Manager, cfg.PipIndexURL)
		final := env.CacheDir(path.Join(pythonCacheDir, "trees", key))
		if meta, ok := readTreeMeta(final, minor); ok {
			return meta.outcome(out, path.Join(pythonCacheDir, "trees", key), LimPyTreeReused)
		}
	}
	if cfg.Offline && cfg.PipIndexURL == "" {
		// PyPI is the built-in default; a tree prepared earlier is still read.
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonOfflineNoRegistry)
	}
	var tree string
	if key != "" {
		tree = env.CacheDir(path.Join(pythonCacheDir, "staging", job.AttemptID+"-"+key[:12]))
		_ = os.RemoveAll(tree)
	} else {
		tree = depdoc.AttemptTree(job.Scratch, depdoc.TaskKey("python", m.Suffix))
	}
	meta, o := inst.run(ctx, tree, reqs)
	if o != nil {
		if key != "" {
			_ = os.RemoveAll(tree)
		}
		o.Manager, o.Lockfile, o.Pinned, o.DroppedInputs, o.Counts = out.Manager, out.Lockfile, out.Pinned, out.DroppedInputs, out.Counts
		o.FailedItems, o.Counts.Failed = meta.Failed, len(meta.Failed)
		o.Limitations = append(o.Limitations, out.Limitations...)
		return *o
	}
	meta.Limitations = sortedUnique(append(meta.Limitations, out.Limitations...))
	if err := writeTreeMeta(tree, meta); err != nil {
		if key != "" {
			_ = os.RemoveAll(tree)
		}
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	if key == "" {
		out.CacheKind = depdoc.CacheAttempt
		return meta.outcome(out, "", "")
	}
	final := env.CacheDir(path.Join(pythonCacheDir, "trees", key))
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		_ = os.RemoveAll(tree)
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonCacheQuotaExceeded)
	}
	if err := os.Rename(tree, final); err != nil {
		// Another worker published the same tree first: use that one.
		_ = os.RemoveAll(tree)
		if m2, ok := readTreeMeta(final, minor); ok {
			return m2.outcome(out, path.Join(pythonCacheDir, "trees", key), LimPyTreeReused)
		}
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	return meta.outcome(out, path.Join(pythonCacheDir, "trees", key), "")
}

// treeMeta is what a prepared tree records about itself (codeye-tree.json
// next to lib/), so a reused tree reports the same counts.
type treeMeta struct {
	Version     int             `json:"version"`
	Declared    int             `json:"declared"`
	Failed      []depdoc.Failed `json:"failed"`
	Limitations []string        `json:"limitations"`
}

const treeMetaName = "codeye-tree.json"

func writeTreeMeta(tree string, m treeMeta) error {
	m.Version = 1
	if m.Failed == nil {
		m.Failed = []depdoc.Failed{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return analysis.WriteFileExclusive(filepath.Join(tree, treeMetaName), b)
}

// readTreeMeta reads a shared tree published earlier; ok only when it is
// complete (its metadata and site-packages directory exist).
func readTreeMeta(tree, minor string) (treeMeta, bool) {
	b, err := os.ReadFile(filepath.Join(tree, treeMetaName))
	if err != nil || len(b) > 1<<20 {
		return treeMeta{}, false
	}
	var m treeMeta
	if json.Unmarshal(b, &m) != nil || m.Version != 1 || m.Declared < len(m.Failed) {
		return treeMeta{}, false
	}
	if st, err := os.Lstat(filepath.Join(tree, python.SitePackagesRel(minor))); err != nil || !st.IsDir() {
		return treeMeta{}, false
	}
	return m, true
}

// outcome is the record outcome of a prepared tree: prepared, or partial
// when some requirements had to be left out.
func (m treeMeta) outcome(out Outcome, cacheKey, extra string) Outcome {
	if cacheKey != "" {
		out.CacheKind, out.CacheKey = depdoc.CacheShared, cacheKey
	}
	declared := m.Declared
	out.Counts = depdoc.Counts{Declared: &declared, Prepared: m.Declared - len(m.Failed), Failed: len(m.Failed)}
	out.FailedItems = m.Failed
	out.Limitations = append(out.Limitations, m.Limitations...)
	if extra != "" {
		out.Limitations = append(out.Limitations, extra)
	}
	if len(m.Failed) == 0 {
		out.Status, out.State, out.Reason = domain.TaskSucceeded, depdoc.StatePrepared, ""
		return out
	}
	out.Status, out.State, out.Reason = domain.TaskFailed, depdoc.StatePartial, m.Failed[0].Reason
	return out
}

// pyInstall is one module's install.
type pyInstall struct {
	env         *Env
	cfg         Config
	minor       string
	spec        func(exe string, vars map[string]string, stdin []byte, args ...string) executor.Spec
	pyVars      map[string]string
	constraints []requirement
}

// run installs reqs into tree, dropping a requirement without a wheel or
// distribution and trying again (at most pythonMaxRetries times). A nil
// outcome is a finished tree; meta.Failed lists what was left out.
func (p pyInstall) run(ctx context.Context, tree string, reqs []requirement) (treeMeta, *Outcome) {
	meta := treeMeta{Declared: len(reqs), Failed: []depdoc.Failed{}}
	fail := func(status domain.TaskStatus, reason string) (treeMeta, *Outcome) {
		state := depdoc.StateFailed
		return meta, &Outcome{Status: status, State: state, Reason: reason}
	}
	site := filepath.Join(tree, python.SitePackagesRel(p.minor))
	downloads := p.env.CacheDir(path.Join(pythonCacheDir, map[bool]string{true: "uv", false: "pip"}[p.cfg.UV != ""]))
	if os.MkdirAll(downloads, 0o755) != nil {
		return fail(domain.TaskFailed, depdoc.ReasonCacheQuotaExceeded)
	}
	constraints := ""
	if len(p.constraints) > 0 {
		constraints = filepath.Join(p.env.TaskDir, "constraints.txt")
		if analysis.WriteFileExclusive(constraints, requirementsText(p.constraints)) != nil {
			return fail(domain.TaskFailed, analysis.ReasonHandlerError)
		}
	}
	left := append([]requirement{}, reqs...)
	for attempt := 0; ; attempt++ {
		if len(left) == 0 {
			return fail(domain.TaskFailed, meta.Failed[0].Reason)
		}
		_ = os.RemoveAll(tree)
		if os.MkdirAll(site, 0o755) != nil {
			return fail(domain.TaskFailed, depdoc.ReasonCacheQuotaExceeded)
		}
		list := filepath.Join(p.env.TaskDir, "requirements."+strconv.Itoa(attempt)+".txt")
		if analysis.WriteFileExclusive(list, requirementsText(left)) != nil {
			return fail(domain.TaskFailed, analysis.ReasonHandlerError)
		}
		var exe string
		var vars map[string]string
		var args []string
		if p.cfg.UV != "" {
			exe, vars = p.cfg.UV, uvEnv(p.cfg.UV, p.cfg.CABundle)
			args = []string{"pip", "install", "--target", site, "--python", p.cfg.Python, "--no-build", "--no-config", "--no-progress", "--color", "never",
				"--cache-dir", downloads, "-r", list}
		} else {
			exe, vars = p.cfg.Python, p.pyVars
			args = []string{"-I", "-m", "pip", "install", "--target", site, "--only-binary=:all:", "--isolated", "--no-input", "--disable-pip-version-check",
				"--no-warn-script-location", "--progress-bar", "off", "--no-color", "--retries", "2", "--timeout", "30", "--cache-dir", downloads, "-r", list}
		}
		if constraints != "" {
			args = append(args, "-c", constraints)
		}
		if p.cfg.PipIndexURL != "" {
			args = append(args, "--index-url", p.cfg.PipIndexURL)
		}
		name := map[bool]string{true: "uv", false: "pip"}[p.cfg.UV != ""] + ".install"
		if attempt > 0 {
			name += ".r" + strconv.Itoa(attempt)
		}
		// The size cap counts the download cache, the tree being built and
		// (Exec adds it) the temp directory the installer unpacks in.
		res, ok := p.env.Exec(ctx, name, p.spec(exe, vars, nil, args...), downloads, tree)
		if !ok {
			status, reason := StopOutcome(p.env.Stop())
			return fail(status, reason)
		}
		if exitedZero(res) {
			break
		}
		reason, culprits := classifyPython(string(res.Stderr.Data) + "\n" + string(res.Stdout.Data))
		if attempt >= pythonMaxRetries || len(culprits) == 0 {
			return fail(domain.TaskFailed, reason)
		}
		dropped := 0
		for _, c := range culprits {
			i := slices.IndexFunc(left, func(r requirement) bool { return r.name != "" && r.name == c.name })
			if i < 0 {
				continue
			}
			meta.Failed = append(meta.Failed, depdoc.Failed{Name: left[i].name, Reason: c.reason})
			left = slices.Delete(left, i, i+1)
			dropped++
		}
		if dropped == 0 {
			return fail(domain.TaskFailed, reason)
		}
	}
	n, err := removeStartupHooks(site)
	if err != nil {
		return fail(domain.TaskFailed, analysis.ReasonHandlerError)
	}
	if n > 0 {
		meta.Limitations = append(meta.Limitations, LimPyStartupHooksRemoved)
	}
	return meta, nil
}

// removeStartupHooks deletes what the site module of an interpreter would
// execute from site-packages: *.pth files (their import lines run at
// start) and the sitecustomize and usercustomize modules.
func removeStartupHooks(site string) (int, error) {
	entries, err := os.ReadDir(site)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		low := strings.ToLower(name)
		hook := strings.HasSuffix(low, ".pth") && !e.IsDir()
		for _, mod := range []string{"sitecustomize", "usercustomize"} {
			if low == mod || strings.HasPrefix(low, mod+".") {
				hook = true
			}
		}
		if hook {
			if err := os.RemoveAll(filepath.Join(site, name)); err != nil {
				return n, err
			}
			n++
		}
	}
	cache := filepath.Join(site, "__pycache__")
	if entries, err := os.ReadDir(cache); err == nil {
		for _, e := range entries {
			low := strings.ToLower(e.Name())
			if strings.HasPrefix(low, "sitecustomize.") || strings.HasPrefix(low, "usercustomize.") {
				if err := os.RemoveAll(filepath.Join(cache, e.Name())); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	return n, nil
}

// treeKey is the shared tree of a fully pinned module: the requirements,
// constraints, interpreter version, platform, installer and index.
func treeKey(reqs, constraints []requirement, pyVersion string, mgr depdoc.Manager, index string) string {
	var b bytes.Buffer
	b.WriteString("codeye-python-tree 1\n")
	b.Write(requirementsText(reqs))
	b.WriteString("--constraints--\n")
	b.Write(requirementsText(constraints))
	ver := ""
	if mgr.Version != nil {
		ver = *mgr.Version
	}
	b.WriteString("--env--\n" + pyVersion + "\n" + runtime.GOOS + "/" + runtime.GOARCH + "\n" + mgr.Name + " " + ver + "\n" + index + "\n")
	return sha256Hex(b.Bytes())[:32]
}

// requirementsText is the requirements file CODEYE writes: one line per
// requirement with its hashes, sorted.
func requirementsText(reqs []requirement) []byte {
	lines := make([]string, 0, len(reqs))
	for _, r := range reqs {
		l := r.line
		for _, h := range r.hashes {
			l += " --hash=" + h
		}
		lines = append(lines, l)
	}
	sort.Strings(lines)
	var b bytes.Buffer
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	return b.Bytes()
}

// requirementFiles are the module's requirements*.txt, sorted.
func requirementFiles(files map[string]source.Entry, dir string) []string {
	var out []string
	for p := range files {
		base := path.Base(p)
		if pyDirOf(p) == dir && strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func pyDirOf(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

// readRequirementsFile reads one requirements file of the snapshot and the
// -r/-c files it names (relative to it, inside the snapshot).
func readRequirementsFile(in *pyInputs, rel string, read func(string) ([]byte, bool), files map[string]source.Entry, seen map[string]bool, constraint bool) {
	if seen[rel] || len(seen) > 64 {
		return
	}
	seen[rel] = true
	b, ok := read(rel)
	if !ok {
		in.drop(rel, DropPyIncludeMissing)
		return
	}
	include := func(target string, asConstraint bool) {
		target = strings.Trim(strings.TrimSpace(target), `"'`)
		joined := path.Clean(path.Join(pyDirOf(rel), filepath.ToSlash(target)))
		if path.IsAbs(target) || strings.Contains(target, "://") || joined == ".." || strings.HasPrefix(joined, "../") {
			in.drop(rel, DropPyIncludeMissing)
			return
		}
		if _, ok := files[joined]; !ok {
			in.drop(rel, DropPyIncludeMissing)
			return
		}
		readRequirementsFile(in, joined, read, files, seen, asConstraint)
	}
	parseRequirementsText(in, rel, b, include, constraint)
}

var (
	pyName    = regexp.MustCompile(`^([A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?)\s*(\[[A-Za-z0-9._,\s-]*\])?\s*`)
	pySpec    = regexp.MustCompile(`^[\s()<>=!~,.*0-9A-Za-z+_-]*$`)
	pyMarker  = regexp.MustCompile(`^[ -~]*$`)
	pyHash    = regexp.MustCompile(`^(sha256|sha384|sha512):[0-9a-f]{64,128}$`)
	pyPin     = regexp.MustCompile(`^===?\s*[0-9A-Za-z][0-9A-Za-z.+!_-]*$`)
	pyNormal  = regexp.MustCompile(`[-_.]+`)
	pyURLLine = regexp.MustCompile(`^https?://[^\s;]+$`)
)

// normalizeName is the PEP 503 normal form of a project name.
func normalizeName(n string) string { return pyNormal.ReplaceAllString(strings.ToLower(n), "-") }

// parseRequirementsText reads requirements lines (a requirements file or uv
// export output). include, when set, follows -r and -c.
func parseRequirementsText(in *pyInputs, rel string, b []byte, include func(string, bool), constraint bool) {
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\\\n", " ")
	for _, raw := range strings.Split(text, "\n") {
		line := stripComment(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		head := fields[0]
		optArg := func(short, long string) (string, bool) {
			switch {
			case head == short || head == long:
				if len(fields) > 1 {
					return fields[1], true
				}
				return "", true
			case strings.HasPrefix(head, long+"="):
				return strings.TrimPrefix(head, long+"="), true
			case short != "" && strings.HasPrefix(head, short) && len(head) > len(short) && !strings.HasPrefix(head, "--"):
				return strings.TrimPrefix(head, short), true
			}
			return "", false
		}
		if v, ok := optArg("-r", "--requirement"); ok {
			if include != nil && v != "" {
				include(v, constraint)
			} else {
				in.drop(rel, DropPyIncludeMissing)
			}
			continue
		}
		if v, ok := optArg("-c", "--constraint"); ok {
			if include != nil && v != "" {
				include(v, true)
			} else {
				in.drop(rel, DropPyIncludeMissing)
			}
			continue
		}
		if _, ok := optArg("-e", "--editable"); ok {
			in.drop(rel, DropPyEditable)
			continue
		}
		if strings.HasPrefix(head, "-") {
			// Index, find-links and the other installer options are the
			// operator's (--pip-index-url), never the repository's.
			in.drop(rel, DropPyOption)
			continue
		}
		addRequirement(in, rel, line, constraint)
	}
}

// stripComment removes a # comment (at the start or after whitespace).
func stripComment(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		return ""
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "\t#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// addRequirement validates one requirement (PEP 508 with --hash options)
// against the allowed shapes and adds it, or records why it was left out.
func addRequirement(in *pyInputs, rel, line string, constraint bool) {
	r, reason := parseRequirement(line)
	if reason != "" {
		in.drop(rel, reason)
		return
	}
	list := &in.reqs
	if constraint {
		list = &in.constraints
		r.hashes = nil
	}
	for _, x := range *list {
		if x.line == r.line && slices.Equal(x.hashes, r.hashes) {
			return
		}
	}
	*list = append(*list, r)
}

// parseRequirement is the allowlist of requirement lines: name[extras]
// with a version specifier or an http(s) URL, an optional marker and
// --hash options. Local paths, VCS URLs and anything else are refused with
// the reason.
func parseRequirement(line string) (requirement, string) {
	line = strings.TrimSpace(line)
	var hashes []string
	if i := strings.Index(line, " --"); i >= 0 {
		for _, opt := range strings.Fields(line[i:]) {
			h, ok := strings.CutPrefix(opt, "--hash=")
			if !ok || !pyHash.MatchString(h) {
				// A per-requirement installer option.
				return requirement{}, DropPyOption
			}
			hashes = append(hashes, h)
		}
		line = strings.TrimSpace(line[:i])
	}
	low := strings.ToLower(line)
	switch {
	case strings.HasPrefix(low, "git+"), strings.HasPrefix(low, "hg+"), strings.HasPrefix(low, "svn+"), strings.HasPrefix(low, "bzr+"),
		strings.Contains(low, "@ git+"), strings.Contains(low, "@ hg+"), strings.Contains(low, "@ svn+"), strings.Contains(low, "@ bzr+"):
		return requirement{}, DropPyVCS
	case strings.HasPrefix(low, "file:"), strings.Contains(low, "@ file:"), strings.HasPrefix(line, "."), strings.HasPrefix(line, "/"),
		strings.HasPrefix(line, "~"), strings.HasPrefix(line, `\`), len(line) > 1 && line[1] == ':':
		return requirement{}, DropPyLocal
	case pyURLLine.MatchString(line):
		return requirement{line: line, hashes: hashes}, ""
	}
	m := pyName.FindStringSubmatch(line)
	if m == nil {
		return requirement{}, DropPyNotUnderstood
	}
	rest := line[len(m[0]):]
	spec, marker, hasMarker := strings.Cut(rest, ";")
	spec, marker = strings.TrimSpace(spec), strings.TrimSpace(marker)
	if hasMarker && (marker == "" || !pyMarker.MatchString(marker)) {
		return requirement{}, DropPyNotUnderstood
	}
	extras := strings.ReplaceAll(m[2], " ", "")
	r := requirement{name: normalizeName(m[1]), hashes: hashes}
	if url, isURL := strings.CutPrefix(spec, "@"); isURL {
		url = strings.TrimSpace(url)
		if strings.HasPrefix(strings.ToLower(url), "file:") {
			return requirement{}, DropPyLocal
		}
		if !pyURLLine.MatchString(url) {
			return requirement{}, DropPyNotUnderstood
		}
		r.line = m[1] + extras + " @ " + url
		if hasMarker {
			// A space before ; keeps the marker out of the URL.
			r.line += " ; " + marker
		}
		return r, ""
	}
	if !pySpec.MatchString(spec) || strings.ContainsAny(spec, "\\\"'") {
		return requirement{}, DropPyNotUnderstood
	}
	spec = strings.Trim(strings.TrimSpace(spec), "()")
	r.pinned = pyPin.MatchString(strings.TrimSpace(spec)) && !strings.Contains(spec, "*")
	r.line = m[1] + extras
	if spec != "" {
		r.line += " " + strings.TrimSpace(spec)
	}
	if hasMarker {
		r.line += " ; " + marker
	}
	return r, ""
}

// culprit is a requirement an install failure names.
type culprit struct{ name, reason string }

var (
	uvNoWheels    = regexp.MustCompile(`because ([a-z0-9][a-z0-9._-]*)(?:\[[^\]]*\])?(?:[=<>!~][^ ]*)? has no usable wheels`)
	uvWheelsHint  = regexp.MustCompile("wheels are required for `([a-z0-9][a-z0-9._-]*)`")
	uvNotFound    = regexp.MustCompile(`because ([a-z0-9][a-z0-9._-]*) was not found in the package registry`)
	uvNoVersion   = regexp.MustCompile(`there is no version of ([a-z0-9][a-z0-9._-]*)`)
	uvYouRequire  = regexp.MustCompile(`you require ([a-z0-9][a-z0-9._-]*)`)
	pipNoMatch    = regexp.MustCompile(`no matching distribution found for ([a-z0-9][a-z0-9._-]*)`)
	pipFromParent = regexp.MustCompile(`satisfies the requirement \S+ \(from ([a-z0-9][a-z0-9._-]*)`)
)

// classifyPython maps uv or pip error text to a reason and the requirements
// to leave out before trying again.
func classifyPython(text string) (string, []culprit) {
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
	case has("401 unauthorized", "403 forbidden", "(401)", "(403)", "status code 401", "status code 403", "authentication required"):
		return ReasonPyRegistryAuthRequired, nil
	case has("hash mismatch", "hashes do not match", "do not match the hashes"):
		return ReasonPyHashMismatch, nil
	case has("error sending request", "connection refused", "dns error", "tcp connect error", "network is unreachable", "no route to host",
		"newconnectionerror", "failed to establish a new connection", "max retries exceeded", "temporary failure in name resolution",
		"name or service not known", "could not fetch url", "connecttimeouterror", "read timed out", "operation timed out", "proxyerror"):
		return depdoc.ReasonRegistryUnreachable, nil
	}
	var out []culprit
	seen := map[string]bool{}
	add := func(name, reason string) {
		name = normalizeName(name)
		if !seen[name] {
			seen[name] = true
			out = append(out, culprit{name, reason})
		}
	}
	reason := ""
	for _, m := range uvNoWheels.FindAllStringSubmatch(t, -1) {
		add(m[1], ReasonPyNoBinaryDistribution)
		reason = ReasonPyNoBinaryDistribution
	}
	for _, m := range uvWheelsHint.FindAllStringSubmatch(t, -1) {
		add(m[1], ReasonPyNoBinaryDistribution)
		reason = ReasonPyNoBinaryDistribution
	}
	for _, m := range uvNotFound.FindAllStringSubmatch(t, -1) {
		add(m[1], ReasonPyDependencyNotFound)
		if reason == "" {
			reason = ReasonPyDependencyNotFound
		}
	}
	for _, m := range uvNoVersion.FindAllStringSubmatch(t, -1) {
		add(m[1], ReasonPyDependencyNotFound)
		if reason == "" {
			reason = ReasonPyDependencyNotFound
		}
	}
	for _, m := range pipNoMatch.FindAllStringSubmatch(t, -1) {
		// pip says the same for a project without a wheel and one that does
		// not exist.
		add(m[1], ReasonPyNoMatchingDistibution)
		reason = ReasonPyNoMatchingDistibution
	}
	if reason != "" {
		// A failing transitive dependency is left out through the
		// requirement that pulls it in.
		for _, re := range []*regexp.Regexp{uvYouRequire, pipFromParent} {
			for _, m := range re.FindAllStringSubmatch(t, -1) {
				add(m[1], reason)
			}
		}
		return reason, out
	}
	if has("no solution found", "resolutionimpossible", "conflicting dependencies", "unsatisfiable", "requires a different python") {
		return ReasonPyResolutionConflict, nil
	}
	return depdoc.ReasonFetchFailed, nil
}

// pyDrifted reports whether uv export changed the module's pyproject.toml
// or uv.lock in the workspace copy.
func pyDrifted(job analysis.Job, files map[string]source.Entry, dir string) bool {
	for _, name := range []string{"pyproject.toml", "uv.lock"} {
		rel := govet.JoinRel(dir, name)
		b, err := os.ReadFile(filepath.Join(job.Snapshot.WorkspaceDir, filepath.FromSlash(rel)))
		e, inManifest := files[rel]
		switch {
		case err != nil && !inManifest && errors.Is(err, os.ErrNotExist):
		case err != nil || !inManifest || sha256Hex(b) != e.SHA256:
			return true
		}
	}
	return false
}

var (
	pyVersionRe = regexp.MustCompile(`^(3)\.([0-9]{1,2})\.[0-9]+$`)
	uvVersionRe = regexp.MustCompile(`^uv ([0-9]+\.[0-9]+\.[0-9]+)`)
	pipVersion  = regexp.MustCompile(`^pip ([0-9][0-9A-Za-z.+-]*) from `)
)

// pythonMinor is "3.N" of a "3.N.M" interpreter version (3.8 or later).
func pythonMinor(v string) (string, bool) {
	m := pyVersionRe.FindStringSubmatch(v)
	if m == nil {
		return "", false
	}
	n, _ := strconv.Atoi(m[2])
	return "3." + m[2], n >= 8
}

func parseUVVersion(b []byte) (string, bool) {
	m := uvVersionRe.FindSubmatch(bytes.TrimSpace(b))
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

func parsePipVersion(b []byte) (string, bool) {
	m := pipVersion.FindSubmatch(bytes.TrimSpace(b))
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}
