package deps

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// JS/TS reasons (docs/ANALYZERS.md#jsts-의존성-받기).
const (
	ReasonLockfileFormatUnsupported  = "lockfile_format_unsupported"
	ReasonLockfileVersionUnsupported = "lockfile_version_unsupported"
	// LimMultipleLockfiles: the install root holds lockfiles of several
	// package managers; the one package.json names (packageManager), else
	// the first of jsts.Lockfiles, was used.
	LimMultipleLockfiles = "multiple_lockfiles"
	// LimLinksOutsideRemoved: links in the installed trees that pointed
	// outside the workspace copy and the package cache (file: and link:
	// dependencies) were removed; dropped_inputs lists them (at most
	// maxDroppedLinks).
	LimLinksOutsideRemoved = "links_outside_workspace_removed"
	// ReasonLinkOutsideWorkspace is the dropped_inputs reason of such a link.
	ReasonLinkOutsideWorkspace = "link_outside_workspace"
)

// maxDroppedLinks bounds the removed links one record lists.
const maxDroppedLinks = 64

// JS/TS package managers and their shared download caches under the
// dependency cache root. The node_modules trees themselves are built in
// the attempt's workspace copy and go with it.
const (
	ManagerNpm  = "npm"
	ManagerPnpm = "pnpm"
	ManagerYarn = "yarn"
)

var jsCacheKeys = map[string]string{ManagerNpm: "javascript/npm", ManagerPnpm: "javascript/pnpm", ManagerYarn: "javascript/yarn"}

// maxInstalled bounds the node_modules directories one install records.
const maxInstalled = depdoc.MaxInstalledDirs

// JSFetcher installs the dependencies of every JS/TS install root with the
// package manager its lockfile names, lifecycle scripts off.
type JSFetcher struct{}

// Ecosystem implements Fetcher.
func (JSFetcher) Ecosystem() string { return "javascript" }

// Plan lists the install roots of the JS/TS modules (jsts.InstallRoots),
// the grouping tsc and type-aware ESLint use to find the record.
func (JSFetcher) Plan(job analysis.Job) ([]Module, error) {
	roots, err := jsts.InstallRoots(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	out := make([]Module, 0, len(roots))
	for _, r := range roots {
		out = append(out, Module{Suffix: r.Suffix, Dir: r.Dir, Title: "의존성 받기 (JS/TS " + r.Dir + ")"})
	}
	return out, nil
}

// jsManager is the package manager an install root uses.
type jsManager struct {
	name     string
	script   string // entry script (cfg)
	pkg      string // npm package of the script
	lockfile string // snapshot path, "" without one
}

// chooseManager picks the package manager from the lockfiles in the
// install root; package.json's packageManager breaks a tie. reason is set
// when no supported manager applies.
func chooseManager(root jsts.InstallRoot, packageManager string, yarnLock []byte) (m jsManager, multiple bool, reason string) {
	byName := map[string]string{}
	for _, l := range root.Lockfiles {
		switch path.Base(l) {
		case "pnpm-lock.yaml":
			byName[ManagerPnpm] = l
		case "yarn.lock":
			byName[ManagerYarn] = l
		case "package-lock.json", "npm-shrinkwrap.json":
			if _, seen := byName[ManagerNpm]; !seen {
				byName[ManagerNpm] = l
			}
		case "bun.lock", "bun.lockb":
			byName["bun"] = l
		}
	}
	multiple = len(byName) > 1
	pick := ""
	if want, _, _ := strings.Cut(packageManager, "@"); byName[want] != "" {
		pick = want
	}
	if pick == "" {
		for _, l := range root.Lockfiles {
			for name, p := range byName {
				if p == l && pick == "" {
					pick = name
				}
			}
		}
	}
	switch pick {
	case "":
		return jsManager{name: ManagerNpm, pkg: "npm"}, false, ""
	case "bun":
		return jsManager{name: "bun", lockfile: byName["bun"]}, multiple, ReasonLockfileFormatUnsupported
	case ManagerYarn:
		// Yarn 2 and later (berry) write a YAML lockfile with __metadata;
		// only the classic format is installed.
		if bytes.Contains(yarnLock, []byte("__metadata:")) || !bytes.Contains(yarnLock, []byte("yarn lockfile v1")) {
			return jsManager{name: ManagerYarn, lockfile: byName[ManagerYarn]}, multiple, ReasonLockfileFormatUnsupported
		}
	}
	return jsManager{name: pick, pkg: pick, lockfile: byName[pick]}, multiple, ""
}

func (c Config) jsScript(name string) string {
	switch name {
	case ManagerNpm:
		return c.NpmScript
	case ManagerPnpm:
		return c.PnpmScript
	case ManagerYarn:
		return c.YarnScript
	}
	return ""
}

// Fetch implements Fetcher.
func (JSFetcher) Fetch(ctx context.Context, env *Env, m Module) Outcome {
	out := Outcome{Manager: depdoc.Manager{Name: ManagerNpm}}
	fail := func(status domain.TaskStatus, state, reason string) Outcome {
		out.Status, out.State, out.Reason = status, state, reason
		return out
	}
	job, cfg := env.Job, env.Config
	roots, err := jsts.InstallRoots(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	var root jsts.InstallRoot
	for _, r := range roots {
		if r.Dir == m.Dir {
			root = r
		}
	}
	if root.Dir == "" {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	// Only tsc and type-aware ESLint resolve modules; they run on modules
	// with a tsconfig.json.
	consumer := false
	for _, mod := range root.Modules {
		consumer = consumer || mod.TSConfig
	}
	if !consumer || !cfg.JSConsumer {
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencyConsumer)
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	read := func(rel string) ([]byte, bool) {
		e, ok := files[rel]
		if !ok {
			return nil, false
		}
		b, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, e.Path)
		return b, err == nil && sha256Hex(b) == e.SHA256
	}
	var manifest struct {
		PackageManager string `json:"packageManager"`
	}
	pkgJSON, hasPkg := read(jsts.Join(root.Dir, "package.json"))
	if hasPkg {
		_ = json.Unmarshal(pkgJSON, &manifest)
	}
	var yarnLock []byte
	if l := jsts.Join(root.Dir, "yarn.lock"); files[l].SHA256 != "" {
		b, ok := read(l)
		if !ok {
			return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
		}
		yarnLock = b
	}
	mgr, multiple, reason := chooseManager(root, manifest.PackageManager, yarnLock)
	out.Manager.Name = mgr.name
	if multiple {
		out.Limitations = append(out.Limitations, LimMultipleLockfiles)
	}
	var lock []byte
	if mgr.lockfile != "" {
		b, ok := read(mgr.lockfile)
		if !ok {
			return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
		}
		lock, out.Pinned = b, true
		out.Lockfile = &depdoc.Lockfile{Path: mgr.lockfile, SHA256: files[mgr.lockfile].SHA256}
	}
	if reason != "" {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, reason)
	}
	if !hasPkg {
		return fail(domain.TaskFailed, depdoc.StateFailed, ReasonManifestInvalid)
	}
	declared, counted := countPackages(mgr.name, lock, pkgJSON)
	if counted {
		out.Counts.Declared = &declared
		if declared == 0 {
			return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencies)
		}
	}
	scriptPath := cfg.jsScript(mgr.name)
	if cfg.NodeExecutable == "" || scriptPath == "" {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
	}
	node, ok := nodeToolchain(cfg.NodeExecutable)
	if !ok {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
	}
	script, err := jsts.NewScript(scriptPath, mgr.pkg, node)
	if err != nil {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
	}
	cacheKey := jsCacheKeys[mgr.name]
	cache := env.CacheDir(cacheKey)
	if os.MkdirAll(cache, 0o700) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonCacheQuotaExceeded)
	}
	cfgDir := filepath.Join(env.TaskDir, "config")
	if os.Mkdir(cfgDir, 0o700) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	userRC, globalRC := filepath.Join(cfgDir, "user.npmrc"), filepath.Join(cfgDir, "global.npmrc")
	if os.WriteFile(userRC, nil, 0o600) != nil || os.WriteFile(globalRC, nil, 0o600) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	workspace := job.Snapshot.WorkspaceDir
	rootAbs := filepath.Join(workspace, filepath.FromSlash(root.Dir))
	vars := jsEnv(mgr.name, node.Dir)
	spec := func(args ...string) executor.Spec {
		return executor.Spec{Executable: node.Executable, Args: script.Argv(args...), WorkspaceRoot: workspace, Dir: rootAbs,
			ProtectedRoots: []string{job.Snapshot.SnapshotDir}, Env: vars, Redact: []string{cfg.CacheRoot}}
	}
	stopped := func() Outcome {
		status, reason := StopOutcome(env.Stop())
		return fail(status, depdoc.StateFailed, reason)
	}
	versionArgs := []string{"--version"}
	if mgr.name == ManagerPnpm {
		versionArgs = []string{"--config.pm-on-fail=ignore", "--version"}
	}
	res, ok := env.Exec(ctx, mgr.name+".version", spec(versionArgs...), "")
	if !ok {
		return stopped()
	}
	v, parsed := parseManagerVersion(res.Stdout.Data)
	if !parsed || !exitedZero(res) {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonToolchainUnavailable)
	}
	out.Manager.Version = &v
	if lock == nil {
		out.Limitations = append(out.Limitations, depdoc.LimVersionsNotPinned)
	}
	args := installArgs(mgr.name, lock != nil, installDirs{root: rootAbs, cache: cache, task: env.TaskDir, userRC: userRC, globalRC: globalRC}, cfg.NpmRegistry)
	out.CacheKind = depdoc.CacheAttempt
	res, ok = env.Exec(ctx, mgr.name+".install", spec(args...), cache)
	installed, walked := installedTrees(workspace, root.Dir)
	// A failed or stopped install leaves no tree behind: the analyzers then
	// see the workspace as without a fetch and report the same gap.
	discard := func() {
		for _, d := range installed {
			_ = os.RemoveAll(filepath.Join(workspace, filepath.FromSlash(d)))
		}
	}
	if !ok {
		discard()
		return stopped()
	}
	if drifted := jsDrift(workspace, files, root); drifted {
		discard()
		return fail(domain.TaskFailed, depdoc.StateFailed, ReasonWorkspaceDrift)
	}
	if !exitedZero(res) {
		discard()
		text := string(res.Stderr.Data) + "\n" + string(res.Stdout.Data)
		out.FailedItems = failedPackages(text)
		out.Counts.Failed = len(out.FailedItems)
		return fail(domain.TaskFailed, depdoc.StateFailed, classifyJS(mgr.name, text))
	}
	if !walked {
		discard()
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	// file: and link: dependencies may point outside the workspace copy;
	// the analyzers would then read host files. Such links are removed and
	// recorded; the rest of the install stays usable.
	removed, swept := sweepForeignLinks(workspace, installed, []string{workspace, cache})
	if !swept {
		discard()
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	if len(removed) > 0 {
		out.Limitations = append(out.Limitations, LimLinksOutsideRemoved)
		for i, p := range removed {
			if i == maxDroppedLinks {
				break
			}
			out.DroppedInputs = append(out.DroppedInputs, depdoc.Dropped{Path: p, Reason: ReasonLinkOutsideWorkspace})
		}
	}
	if counted {
		out.Counts.Prepared = declared
	} else {
		n := topLevelPackages(workspace, installed)
		out.Counts.Prepared = n
	}
	out.InstalledDirs = installed
	out.Status, out.State = domain.TaskSucceeded, depdoc.StatePrepared
	return out
}

// nodeToolchain is the node handle of the operator's --node path.
func nodeToolchain(exe string) (pack.Toolchain, bool) {
	if !filepath.IsAbs(exe) {
		return pack.Toolchain{}, false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return pack.Toolchain{}, false
	}
	return pack.Toolchain{Name: "node", Executable: exe, Dir: filepath.Dir(resolved)}, true
}

// jsEnv is the complete environment of a package manager: PATH is the
// node directory only, nothing else is inherited. HOME is the executor's
// per-run directory, so no user configuration (~/.npmrc with tokens) is
// read. YARN_IGNORE_PATH keeps yarn from running the yarn release a
// repository's .yarnrc names (yarn-path, repository code).
func jsEnv(manager, nodeDir string) map[string]string {
	vars := map[string]string{"PATH": nodeDir, "LANG": "C.UTF-8", "NO_UPDATE_NOTIFIER": "1", "CI": "true"}
	switch manager {
	case ManagerYarn:
		vars["YARN_IGNORE_PATH"] = "1"
	case ManagerPnpm:
		// pnpm switches to (and downloads) the release the project's
		// packageManager field names on every command, --version included
		// (measured with pnpm 11): pm-on-fail (pnpm 11) and
		// manage-package-manager-versions (pnpm 9 and 10) keep it on the
		// operator's release.
		vars["pnpm_config_pm_on_fail"] = "ignore"
		vars["npm_config_manage_package_manager_versions"] = "false"
		vars["pnpm_config_manage_package_manager_versions"] = "false"
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			vars["SystemRoot"] = v
		}
	}
	return vars
}

// Default registries when the operator names none (npm and pnpm share npm's).
const (
	defaultNpmRegistry  = "https://registry.npmjs.org/"
	defaultYarnRegistry = "https://registry.yarnpkg.com"
)

// installDirs are the locations an install may write besides the install
// root's node_modules, all pinned on the command line so a repository
// .npmrc or .yarnrc cannot move them.
type installDirs struct {
	root     string // install root in the workspace copy
	cache    string // shared download cache of the manager
	task     string // the task's private directory
	userRC   string // empty npm user configuration
	globalRC string // empty npm global configuration
}

// installArgs is the install argv after the entry script. Lifecycle
// scripts never run; the lockfile is never rewritten; the download cache
// is the shared one; the user's and the global configuration are empty
// files. A repository .npmrc, .yarnrc or .yarnrc.yml never reaches the
// workspace copy (the snapshot leaves them out as secret files), but
// pnpm-workspace.yaml and package.json can carry pnpm settings; command
// -line settings win over both (measured with npm 11, pnpm 11 and yarn
// 1.22), so the project directory, node_modules, the virtual store, the
// caches, logs and state, the registry and (npm, pnpm) the proxy are
// CODEYE's.
func installArgs(manager string, locked bool, d installDirs, registry string) []string {
	var args []string
	switch manager {
	case ManagerPnpm:
		if registry == "" {
			registry = defaultNpmRegistry
		}
		// The flags repeat jsEnv's release pinning; the pnpmfile (the
		// project's and a configured global one) is repository code.
		args = []string{"install", "--frozen-lockfile", "--ignore-scripts", "--ignore-pnpmfile", "--prefer-offline", "--reporter=append-only",
			"--dir", d.root, "--modules-dir", "node_modules", "--virtual-store-dir", "node_modules/.pnpm", "--store-dir", d.cache,
			"--config.lockfile-dir=" + d.root, "--config.cache-dir=" + filepath.Join(d.task, "pnpm-cache"), "--config.state-dir=" + filepath.Join(d.task, "pnpm-state"),
			"--config.global-dir=" + filepath.Join(d.task, "pnpm-global"), "--config.registry=" + registry, "--config.proxy=false", "--config.https-proxy=false",
			"--config.pm-on-fail=ignore", "--config.manage-package-manager-versions=false", "--config.package-manager-strict=false",
			"--config.update-notifier=false", "--config.side-effects-cache=false", "--config.verify-deps-before-run=false"}
	case ManagerYarn:
		if registry == "" {
			registry = defaultYarnRegistry
		}
		// yarn 1 reads its settings from .yarnrc only, which the snapshot
		// leaves out; it has no command-line value that clears a proxy.
		args = []string{"install", "--frozen-lockfile", "--ignore-scripts", "--no-bin-links", "--non-interactive", "--ignore-engines", "--prefer-offline",
			"--no-progress", "--modules-folder", filepath.Join(d.root, "node_modules"), "--cache-folder", d.cache, "--registry", registry}
	default:
		if registry == "" {
			registry = defaultNpmRegistry
		}
		args = []string{"ci"}
		if !locked {
			args = []string{"install", "--no-package-lock", "--no-save"}
		}
		// --install-links=false keeps file: directory dependencies links,
		// which the link sweep removes when they leave the workspace.
		// --git names no program: the PATH holds node only, so a git
		// dependency fails instead of running a git a project .npmrc names.
		args = append(args, "--ignore-scripts", "--no-bin-links", "--no-audit", "--no-fund", "--include=dev", "--include=optional", "--include=peer",
			"--engine-strict=false", "--prefer-offline", "--no-update-notifier", "--no-progress", "--prefix", d.root, "--cache", d.cache,
			"--logs-dir", filepath.Join(d.task, "npm-logs"), "--userconfig", d.userRC, "--globalconfig", d.globalRC,
			"--registry", registry, "--proxy=null", "--https-proxy=null", "--install-links=false", "--git=git")
	}
	return args
}

var managerVersion = regexp.MustCompile(`^v?([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,6}(?:-[0-9A-Za-z.]+)?)$`)

// parseManagerVersion reads `<manager> --version`: a version on the first line.
func parseManagerVersion(stdout []byte) (string, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	m := managerVersion.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// installedTrees lists the node_modules directories below the install root
// of the workspace copy (snapshot-relative), not descending into one. A
// snapshot never holds node_modules, so each is the install's. ok is false
// when the walk failed or found more than the record holds.
func installedTrees(workspace, dir string) ([]string, bool) {
	var out []string
	base := filepath.Join(workspace, filepath.FromSlash(dir))
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == depdoc.InstalledDirName {
			rel, err := filepath.Rel(workspace, p)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			if len(out) > maxInstalled {
				return fs.ErrInvalid
			}
			return filepath.SkipDir
		}
		if p != base && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	return out, err == nil
}

// sweepForeignLinks walks the installed trees (not following links) and
// removes every link (a symlink, or a junction on Windows) whose target,
// written or resolved, lies outside the allowed roots. It returns the
// removed links (snapshot-relative, sorted); ok is false when a walk or a
// removal failed.
func sweepForeignLinks(workspace string, trees, allowed []string) (removed []string, ok bool) {
	var roots []string
	for _, r := range allowed {
		roots = append(roots, filepath.Clean(r))
		if res, err := filepath.EvalSymlinks(r); err == nil {
			roots = append(roots, res)
		}
	}
	inside := func(p string) bool {
		for _, r := range roots {
			if rel, err := filepath.Rel(r, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				return true
			}
		}
		return false
	}
	for _, t := range trees {
		err := filepath.WalkDir(filepath.Join(workspace, filepath.FromSlash(t)), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
				return nil
			}
			keep := false
			if target, err := os.Readlink(p); err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(p), target)
				}
				keep = inside(filepath.Clean(target))
				if res, err := filepath.EvalSymlinks(p); keep && err == nil {
					keep = inside(res)
				}
			}
			if keep {
				return nil
			}
			if err := os.Remove(p); err != nil {
				return err
			}
			rel, err := filepath.Rel(workspace, p)
			if err != nil {
				return err
			}
			removed = append(removed, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return removed, false
		}
	}
	sort.Strings(removed)
	return removed, true
}

// topLevelPackages counts the package directories directly in the
// installed trees (a scope directory counts its packages).
func topLevelPackages(workspace string, dirs []string) int {
	n := 0
	for _, d := range dirs {
		entries, err := os.ReadDir(filepath.Join(workspace, filepath.FromSlash(d)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			switch {
			case strings.HasPrefix(name, "."):
			case strings.HasPrefix(name, "@"):
				if sub, err := os.ReadDir(filepath.Join(workspace, filepath.FromSlash(d), name)); err == nil {
					n += len(sub)
				}
			default:
				n++
			}
		}
	}
	return n
}

// jsDrift reports a changed package.json or lockfile of the install root
// or of a module it serves.
func jsDrift(workspace string, files map[string]source.Entry, root jsts.InstallRoot) bool {
	paths := append([]string{}, root.Lockfiles...)
	paths = append(paths, jsts.Join(root.Dir, "package.json"))
	for _, m := range root.Modules {
		paths = append(paths, jsts.Join(m.Dir, "package.json"))
	}
	for _, rel := range paths {
		e, listed := files[rel]
		if !listed {
			continue
		}
		b, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
		if err != nil || sha256Hex(b) != e.SHA256 {
			return true
		}
	}
	return false
}

// countPackages counts the packages a lockfile pins (npm: the packages
// entries, pnpm: the packages section, yarn: the entries), or without a
// lockfile the dependencies package.json declares. ok is false when the
// lockfile does not read as expected.
func countPackages(manager string, lock, pkgJSON []byte) (int, bool) {
	if lock == nil {
		var p struct {
			Dependencies, DevDependencies, OptionalDependencies, PeerDependencies map[string]json.RawMessage
			Workspaces                                                            json.RawMessage
		}
		if json.Unmarshal(pkgJSON, &p) != nil || len(p.Workspaces) > 0 {
			return 0, false
		}
		return len(p.Dependencies) + len(p.DevDependencies) + len(p.OptionalDependencies) + len(p.PeerDependencies), true
	}
	switch manager {
	case ManagerNpm:
		var l struct {
			LockfileVersion int                        `json:"lockfileVersion"`
			Packages        map[string]json.RawMessage `json:"packages"`
			Dependencies    map[string]json.RawMessage `json:"dependencies"`
		}
		if json.Unmarshal(lock, &l) != nil {
			return 0, false
		}
		if l.Packages != nil {
			n := 0
			for k, raw := range l.Packages {
				var e struct {
					Link bool `json:"link"`
				}
				_ = json.Unmarshal(raw, &e)
				if strings.Contains(k, "node_modules/") && !e.Link {
					n++
				}
			}
			return n, true
		}
		return len(l.Dependencies), l.Dependencies != nil
	case ManagerPnpm:
		n, in := 0, false
		for _, line := range strings.Split(string(lock), "\n") {
			line = strings.TrimRight(line, "\r")
			switch {
			case line == "packages:":
				in = true
			case in && line != "" && !strings.HasPrefix(line, " "):
				in = false
			case in && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":"):
				n++
			}
		}
		return n, true
	case ManagerYarn:
		n := 0
		for _, line := range strings.Split(string(lock), "\n") {
			if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") && strings.HasSuffix(strings.TrimRight(line, "\r"), ":") {
				n++
			}
		}
		return n, true
	}
	return 0, false
}

var npm404 = regexp.MustCompile(`(?:404 Not Found - GET \S+/|ERR_PNPM_FETCH_404\S* GET \S+/|ERR_PNPM_NO_MATCHING_VERSION\S* No matching version found for |"https?://\S+/)(@?[a-z0-9][a-z0-9._~-]*(?:%2[fF]|/)?[a-z0-9._~-]*)`)

// failedPackages names the packages the registry did not have (bounded).
func failedPackages(text string) []depdoc.Failed {
	seen := map[string]bool{}
	var out []depdoc.Failed
	for _, m := range npm404.FindAllStringSubmatch(text, -1) {
		name := strings.ReplaceAll(strings.ReplaceAll(m[1], "%2f", "/"), "%2F", "/")
		if name == "" || seen[name] || len(out) == depdoc.MaxFailedItems {
			continue
		}
		seen[name] = true
		out = append(out, depdoc.Failed{Name: name, Reason: ReasonDependencyNotFound})
	}
	return out
}

// classifyJS maps package manager error text to a reason.
func classifyJS(manager, text string) string {
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
	case has("err_pnpm_outdated_lockfile", "lockfile needs to be updated", "are in sync", "missing from lock file", "does not satisfy", "err_pnpm_lockfile_config_mismatch"):
		return ReasonLockfileOutdated
	case has("err_pnpm_lockfile_breaking_change", "not compatible lockfile", "unsupported lockfile", "lockfile version", "lockfileversion"):
		return ReasonLockfileVersionUnsupported
	case has("e401", "e403", "eneedauth", "err_pnpm_fetch_401", "err_pnpm_fetch_403", "unable to authenticate", "request failed \"401", "request failed \"403", "authentication required"):
		return ReasonRegistryAuth
	case has("enotfound", "econnrefused", "etimedout", "eai_again", "econnreset", "enetunreach", "ehostunreach", "socket hang up", "network request", "err_pnpm_meta_fetch_fail", "getaddrinfo", "unable to get local issuer", "self-signed certificate"):
		return depdoc.ReasonRegistryUnreachable
	case has("e404", "404 not found", "err_pnpm_fetch_404", "etarget", "no matching version", "err_pnpm_no_matching_version", "not found", "couldn't find any versions", "couldn't find package"):
		return ReasonDependencyNotFound
	case has("ejsonparse", "err_pnpm_json_parse", "unexpected token", "invalid package.json"):
		return ReasonManifestInvalid
	}
	return depdoc.ReasonFetchFailed
}
