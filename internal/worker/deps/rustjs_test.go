package deps

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/rustpack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// ---- Rust -------------------------------------------------------------------

const cargoToml = "[package]\nname = \"c\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[dependencies]\nitoa = \"1\"\n"
const cargoLock = "version = 4\n\n[[package]]\nname = \"c\"\nversion = \"0.1.0\"\ndependencies = [\n \"itoa\",\n]\n\n[[package]]\nname = \"itoa\"\nversion = \"1.0.15\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\nchecksum = \"4a5f13b858c8d314ee3e8f639011f7ccefe71f97f96e50151fb991f267928e2c\"\n"
const rustcVV = "rustc 1.99.1 (58cf8f9c3 2026-10-13)\nbinary: rustc\ncommit-hash: 58cf8f9c3\nhost: x86_64-unknown-linux-gnu\nrelease: 1.99.1\nLLVM version: 21.1.2\n"

// fakeCargo is a toolchain bin directory rustpack accepts; the fake runner
// answers for its programs, which never run.
func fakeCargo(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("toolchain files use Unix names and modes")
	}
	dir := filepath.Join(t.TempDir(), "toolchain", "bin")
	writeTree(t, dir, map[string]string{"cargo": "", "rustc": ""})
	for _, n := range []string{"cargo", "rustc"} {
		if err := os.Chmod(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "cargo")
}

// cargoScript answers rustc -vV, cargo --version, generate-lockfile (it
// writes lock into the working directory) and fetch.
func cargoScript(lock string, fetch func(executor.Spec) executor.Result) func(context.Context, executor.Spec) executor.Result {
	return func(_ context.Context, s executor.Spec) executor.Result {
		switch {
		case filepath.Base(s.Executable) == "rustc":
			return exited(0, rustcVV, "")
		case slices.Equal(s.Args, []string{"--version"}):
			return exited(0, "cargo 1.99.1 (5f94df478 2026-08-27)\n", "")
		case slices.Equal(s.Args, []string{"generate-lockfile"}):
			_ = os.WriteFile(filepath.Join(s.Dir, "Cargo.lock"), []byte(lock), 0o600)
			return exited(0, "", "    Updating crates.io index\n")
		case len(s.Args) > 0 && s.Args[0] == "fetch":
			return fetch(s)
		}
		return exited(2, "", "unexpected")
	}
}

func rustJob(t *testing.T, files map[string]string) (analysis.Job, string) {
	job, src := jobOf(t, files)
	job.AllowRepositoryCode = true
	return job, src
}

func TestPlanRustModulesFollowCargoCheckKeys(t *testing.T) {
	job, _ := rustJob(t, map[string]string{"a/Cargo.toml": cargoToml, "a/src/lib.rs": "", "b/Cargo.toml": cargoToml, "b/src/lib.rs": "", "b/x/Cargo.toml": cargoToml})
	mods, err := RustFetcher{}.Plan(job)
	if err != nil {
		t.Fatal(err)
	}
	checks, _ := rustpack.CheckPlan(context.Background(), job)
	if len(mods) != 2 || len(checks) != 2 {
		t.Fatalf("modules %+v, cargo check %+v", mods, checks)
	}
	for i, m := range mods {
		if want := strings.TrimPrefix(checks[i].Spec.Key, rustpack.CheckName); m.Suffix != want || m.Dir != checks[i].Module {
			t.Fatalf("module %+v, cargo check task %+v", m, checks[i])
		}
	}
}

func TestRustFetchArgvEnvAndRecord(t *testing.T) {
	job, src := rustJob(t, map[string]string{"Cargo.toml": cargoToml, "Cargo.lock": cargoLock, "src/main.rs": "fn main() {}\n"})
	h := handler(t, Config{CargoExecutable: fakeCargo(t), RustConsumer: true})
	r := &fakeRunner{fn: cargoScript("", func(executor.Spec) executor.Result { return exited(0, "", "  Downloaded itoa v1.0.15\n") })}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.rust"}, r)
	if res.Status != domain.TaskSucceeded || len(res.Artifacts) != 1 {
		t.Fatalf("result %+v", res)
	}
	fetch := r.specs[len(r.specs)-1]
	if want := []string{"fetch", "--locked", "--target", "x86_64-unknown-linux-gnu"}; !slices.Equal(fetch.Args, want) {
		t.Fatalf("argv %v, want %v", fetch.Args, want)
	}
	home := filepath.Join(h.cfg.CacheRoot, "rust", "cargo")
	for _, s := range r.specs {
		if s.Dir != job.Snapshot.WorkspaceDir || strings.HasPrefix(s.Dir, src) || !slices.Contains(s.ProtectedRoots, job.Snapshot.SnapshotDir) {
			t.Fatalf("cwd %s must be the workspace module dir", s.Dir)
		}
		if _, offline := s.Env["CARGO_NET_OFFLINE"]; offline || s.Env["CARGO_HOME"] != home || s.Env["RUSTC_WRAPPER"] != "" {
			t.Fatalf("env %v", s.Env)
		}
	}
	if fetch.Env["CARGO_BUILD_TARGET"] != "x86_64-unknown-linux-gnu" {
		t.Fatalf("target %q", fetch.Env["CARGO_BUILD_TARGET"])
	}
	rec := readRecord(t, job, "deps.rust")
	if rec.State != depdoc.StatePrepared || !rec.Pinned || rec.Lockfile == nil || rec.Lockfile.Path != "Cargo.lock" || *rec.Cache.Key != RustCacheKey ||
		*rec.Counts.Declared != 1 || rec.Counts.Prepared != 1 || rec.Manager.Name != "cargo" || *rec.Manager.Version != "1.99.1" {
		t.Fatalf("record %+v", rec)
	}
}

// Without Cargo.lock the generated one is kept in the task directory for
// the cargo tasks, and the workspace copy is left as the snapshot.
func TestRustFetchWithoutLockfileKeepsTheGeneratedOne(t *testing.T) {
	job, _ := rustJob(t, map[string]string{"Cargo.toml": cargoToml, "src/main.rs": "fn main() {}\n"})
	h := handler(t, Config{CargoExecutable: fakeCargo(t), RustConsumer: true})
	r := &fakeRunner{fn: cargoScript(cargoLock, func(s executor.Spec) executor.Result {
		if _, err := os.Stat(filepath.Join(s.Dir, "Cargo.lock")); err != nil {
			t.Fatalf("fetch without the generated lockfile: %v", err)
		}
		return exited(0, "", "")
	})}
	res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.rust"}, r)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("result %+v", res)
	}
	if b, err := os.ReadFile(filepath.Join(job.Scratch, "deps", "deps.rust", rustpack.GeneratedLockfile)); err != nil || string(b) != cargoLock {
		t.Fatalf("generated lockfile %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(job.Snapshot.WorkspaceDir, "Cargo.lock")); err == nil {
		t.Fatal("generated lockfile left in the workspace copy")
	}
	rec := readRecord(t, job, "deps.rust")
	if rec.Pinned || rec.Lockfile != nil || !slices.Contains(rec.Limitations, depdoc.LimVersionsNotPinned) {
		t.Fatalf("record %+v", rec)
	}
}

func TestRustFetchSkipsAndRefusals(t *testing.T) {
	vendored := map[string]string{"Cargo.toml": cargoToml, "Cargo.lock": cargoLock, "src/main.rs": "", ".cargo/config.toml": "[source.crates-io]\nreplace-with = \"vendored\"\n\n[source.vendored]\ndirectory = \"vendor\"\n"}
	pathOnly := map[string]string{"Cargo.toml": "[package]\nname = \"c\"\nversion = \"0.1.0\"\n", "Cargo.lock": "version = 4\n\n[[package]]\nname = \"c\"\nversion = \"0.1.0\"\n", "src/main.rs": ""}
	plain := map[string]string{"Cargo.toml": cargoToml, "Cargo.lock": cargoLock, "src/main.rs": ""}
	cargo := fakeCargo(t)
	for name, tc := range map[string]struct {
		files   map[string]string
		allowed bool
		cfg     Config
		status  domain.TaskStatus
		reason  string
	}{
		"repository code not allowed": {plain, false, Config{CargoExecutable: cargo, RustConsumer: true}, domain.TaskSkippedPolicy, depdoc.ReasonDependencyConsumersNotAllowed},
		"no cargo":                    {plain, true, Config{RustConsumer: true}, domain.TaskUnsupported, depdoc.ReasonPackageManagerNotConfigured},
		"cargo check unusable":        {plain, true, Config{CargoExecutable: cargo}, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencyConsumer},
		"vendored":                    {vendored, true, Config{CargoExecutable: cargo, RustConsumer: true}, domain.TaskSkippedPolicy, depdoc.ReasonVendoredDependencies},
		"path dependencies only":      {pathOnly, true, Config{CargoExecutable: cargo, RustConsumer: true}, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencies},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := rustJob(t, tc.files)
			job.AllowRepositoryCode = tc.allowed
			r := &fakeRunner{fn: func(context.Context, executor.Spec) executor.Result {
				t.Fatal("nothing may run")
				return executor.Result{}
			}}
			res := handler(t, tc.cfg).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.rust"}, r)
			if res.Status != tc.status || res.Reason != tc.reason {
				t.Fatalf("result %s/%s, want %s/%s", res.Status, res.Reason, tc.status, tc.reason)
			}
			if rec := readRecord(t, job, "deps.rust"); rec.State != depdoc.StateSkipped {
				t.Fatalf("record %+v", rec)
			}
		})
	}
}

func TestRustFetchFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		stderr string
		reason string
	}{
		"registry unreachable": {"error: failed to download from `https://index.crates.io/config.json`\n\nCaused by:\n  [6] Couldn't resolve host name (Could not resolve host: index.crates.io)", depdoc.ReasonRegistryUnreachable},
		"lockfile outdated":    {"error: the lock file /w/Cargo.lock needs to be updated but --locked was passed to prevent this", ReasonLockfileOutdated},
		"not found":            {"error: no matching package named `itoa2` found\nlocation searched: crates.io index", ReasonDependencyNotFound},
		"manifest invalid":     {"error: failed to parse manifest at `/w/Cargo.toml`", ReasonManifestInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := rustJob(t, map[string]string{"Cargo.toml": cargoToml, "Cargo.lock": cargoLock, "src/main.rs": ""})
			r := &fakeRunner{fn: cargoScript("", func(executor.Spec) executor.Result { return exited(101, "", tc.stderr) })}
			res := handler(t, Config{CargoExecutable: fakeCargo(t), RustConsumer: true}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.rust"}, r)
			if res.Status != domain.TaskFailed || res.Reason != tc.reason {
				t.Fatalf("result %s/%s, want failed/%s", res.Status, res.Reason, tc.reason)
			}
			if rec := readRecord(t, job, "deps.rust"); rec.State != depdoc.StateFailed || rec.Usable() {
				t.Fatalf("record %+v", rec)
			}
		})
	}
}

// ---- JS/TS ------------------------------------------------------------------

// jsTools writes a node and the three package managers' entry scripts the
// way they are installed (each inside its npm package); the fake runner
// answers for them.
func jsTools(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{"bin/node": ""}
	for _, p := range []struct{ name, entry string }{{"npm", "bin/npm-cli.js"}, {"pnpm", "bin/pnpm.cjs"}, {"yarn", "bin/yarn.js"}} {
		files["nm/"+p.name+"/package.json"] = `{"name":"` + p.name + `","version":"1.0.0"}`
		files["nm/"+p.name+"/"+p.entry] = "// entry\n"
	}
	writeTree(t, dir, files)
	j := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	return Config{NodeExecutable: j("bin/node"), NpmScript: j("nm/npm/bin/npm-cli.js"), PnpmScript: j("nm/pnpm/bin/pnpm.cjs"), YarnScript: j("nm/yarn/bin/yarn.js"), JSConsumer: true}
}

const pkgJSON = `{"name":"app","version":"1.0.0","devDependencies":{"typescript":"6.0.3"},"dependencies":{"is-number":"7.0.0"}}`
const npmLock = `{"name":"app","lockfileVersion":3,"packages":{"":{"name":"app"},"node_modules/is-number":{"version":"7.0.0"},"node_modules/typescript":{"version":"6.0.3"}}}`
const pnpmLock = "lockfileVersion: '9.0'\n\nimporters:\n\n  .:\n    dependencies:\n      is-number:\n        specifier: 7.0.0\n        version: 7.0.0\n\npackages:\n\n  is-number@7.0.0:\n    resolution: {integrity: sha512-x}\n\n  typescript@6.0.3:\n    resolution: {integrity: sha512-y}\n\nsnapshots:\n\n  is-number@7.0.0: {}\n"
const yarnLock = "# THIS IS AN AUTOGENERATED FILE. DO NOT EDIT THIS FILE DIRECTLY.\n# yarn lockfile v1\n\n\nis-number@7.0.0:\n  version \"7.0.0\"\n\ntypescript@6.0.3:\n  version \"6.0.3\"\n"

// jsScript answers --version and builds node_modules in the working
// directory for install (and in each member listed in members).
func jsScript(install func(executor.Spec) executor.Result, members ...string) func(context.Context, executor.Spec) executor.Result {
	return func(_ context.Context, s executor.Spec) executor.Result {
		if s.Args[len(s.Args)-1] == "--version" {
			return exited(0, "11.16.0\n", "")
		}
		for _, d := range append([]string{"."}, members...) {
			_ = os.MkdirAll(filepath.Join(s.Dir, filepath.FromSlash(d), "node_modules", "is-number"), 0o700)
		}
		return install(s)
	}
}

func jsJob(t *testing.T, files map[string]string) (analysis.Job, string) {
	return jobOf(t, files)
}

func TestPlanJSInstallRoots(t *testing.T) {
	job, _ := jsJob(t, map[string]string{
		"package.json": `{"name":"mono","workspaces":["packages/*"]}`, "package-lock.json": npmLock,
		"packages/a/package.json": `{"name":"a"}`, "packages/a/src/a.ts": "export const a = 1\n", "packages/a/tsconfig.json": "{}",
		"packages/b/package.json": `{"name":"b"}`, "packages/b/b.js": "",
		"tools/x/package.json": `{"name":"x"}`, "tools/x/pnpm-lock.yaml": pnpmLock, "tools/x/x.ts": "",
		"solo/package.json": `{"name":"solo"}`, "solo/s.ts": "",
	})

	mods, err := JSFetcher{}.Plan(job)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range mods {
		got = append(got, "deps.javascript"+m.Suffix+"="+m.Dir)
	}
	// The packages install at the nearest lockfile above them (the root,
	// which has no source of its own and so is no module); a nested
	// lockfile is its own root.
	if want := []string{"deps.javascript=.", "deps.javascript.m01=tools/x"}; !slices.Equal(got, want) {
		t.Fatalf("plan %v, want %v", got, want)
	}
}

// Without any lockfile above it, a package installs at the outermost
// package.json directory.
func TestPlanJSWithoutLockfile(t *testing.T) {
	job, _ := jsJob(t, map[string]string{"web/package.json": `{"name":"w"}`, "web/a.ts": "", "web/sub/package.json": `{"name":"s"}`, "web/sub/s.ts": ""})
	mods, err := JSFetcher{}.Plan(job)
	if err != nil || len(mods) != 1 || mods[0].Dir != "web" || mods[0].Suffix != ".m01" {
		t.Fatalf("plan %+v, %v", mods, err)
	}
}

func TestJSFetchArgvEnvAndRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		lockName, lock, manager string
		wantArgs                []string
		pinned                  bool
	}{
		"npm":         {"package-lock.json", npmLock, "npm", []string{"ci", "--ignore-scripts", "--no-bin-links"}, true},
		"pnpm":        {"pnpm-lock.yaml", pnpmLock, "pnpm", []string{"install", "--frozen-lockfile", "--ignore-scripts", "--ignore-pnpmfile"}, true},
		"yarn":        {"yarn.lock", yarnLock, "yarn", []string{"install", "--frozen-lockfile", "--ignore-scripts", "--no-bin-links"}, true},
		"no lockfile": {"", "", "npm", []string{"install", "--no-package-lock", "--no-save", "--ignore-scripts"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"package.json": pkgJSON, "tsconfig.json": "{}", "src/a.ts": "export const a = 1\n"}
			if tc.lockName != "" {
				files[tc.lockName] = tc.lock
			}
			job, src := jsJob(t, files)
			cfg := jsTools(t)
			cfg.NpmRegistry = "https://registry.example.test/"
			h := handler(t, cfg)
			r := &fakeRunner{fn: jsScript(func(executor.Spec) executor.Result { return exited(0, "added 2 packages\n", "") })}
			res := h.Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r)
			if res.Status != domain.TaskSucceeded {
				t.Fatalf("result %+v", res)
			}
			install := r.specs[len(r.specs)-1]
			if install.Executable != cfg.NodeExecutable || install.Args[0] != cfg.jsScript(tc.manager) {
				t.Fatalf("install runs %s %v", install.Executable, install.Args)
			}
			args := install.Args[1:]
			if !slices.Equal(args[:len(tc.wantArgs)], tc.wantArgs) || !slices.Contains(args, "--ignore-scripts") ||
				!(slices.Contains(args, "https://registry.example.test/") || slices.Contains(args, "--config.registry=https://registry.example.test/")) {
				t.Fatalf("argv %v", args)
			}
			// The locations a repository .npmrc or .yarnrc could move are pinned.
			ws := job.Snapshot.WorkspaceDir
			pinned := map[string][]string{
				"npm":  {"--prefix", ws, "--logs-dir", "--proxy=null", "--https-proxy=null"},
				"pnpm": {"--dir", ws, "--modules-dir", "--virtual-store-dir", "--config.lockfile-dir=" + ws, "--config.proxy=false"},
				"yarn": {"--modules-folder", filepath.Join(ws, "node_modules"), "--cache-folder"},
			}[tc.manager]
			for _, a := range pinned {
				if !slices.Contains(args, a) {
					t.Fatalf("argv %v lacks %s", args, a)
				}
			}
			cache := filepath.Join(h.cfg.CacheRoot, "javascript", tc.manager)
			if !slices.Contains(args, cache) {
				t.Fatalf("argv %v lacks the shared cache %s", args, cache)
			}
			if install.Dir != job.Snapshot.WorkspaceDir || strings.HasPrefix(install.Dir, src) || install.Env["PATH"] != filepath.Dir(cfg.NodeExecutable) {
				t.Fatalf("cwd %s env %v", install.Dir, install.Env)
			}
			if (install.Env["YARN_IGNORE_PATH"] == "1") != (tc.manager == "yarn") {
				t.Fatalf("YARN_IGNORE_PATH %v", install.Env)
			}
			if tc.manager == "npm" && (!slices.Contains(args, "--userconfig") || !slices.Contains(args, "--globalconfig") || !slices.Contains(args, "--git=git")) {
				t.Fatalf("npm argv %v", args)
			}
			if tc.manager == "pnpm" {
				// pnpm would switch to the packageManager release on any command.
				for _, sp := range r.specs {
					if !slices.Contains(sp.Args, "--config.pm-on-fail=ignore") || sp.Env["pnpm_config_pm_on_fail"] != "ignore" || sp.Env["npm_config_manage_package_manager_versions"] != "false" {
						t.Fatalf("pnpm may switch to another release: %v %v", sp.Args, sp.Env)
					}
				}
			}
			rec := readRecord(t, job, "deps.javascript")
			if rec.State != depdoc.StatePrepared || rec.Manager.Name != tc.manager || rec.Pinned != tc.pinned || rec.Cache.Kind != depdoc.CacheAttempt ||
				!slices.Equal(rec.InstalledDirs, []string{"node_modules"}) || !rec.TreeUsable() || *rec.Counts.Declared != 2 {
				t.Fatalf("record %+v", rec)
			}
			if tc.pinned != (rec.Lockfile != nil) || slices.Contains(rec.Limitations, depdoc.LimVersionsNotPinned) == tc.pinned {
				t.Fatalf("record %+v", rec)
			}
			if _, err := os.Stat(filepath.Join(src, "node_modules")); err == nil {
				t.Fatal("node_modules written into the source")
			}
		})
	}
}

// A failed or stopped install leaves no node_modules: the analyzers then see
// the workspace as without a fetch.
func TestJSFetchFailureRemovesTheTree(t *testing.T) {
	for name, tc := range map[string]struct {
		result executor.Result
		status domain.TaskStatus
		reason string
	}{
		"registry unreachable": {exited(1, "", "npm error code ENOTFOUND\nnpm error network request to https://registry.npmjs.org/is-number failed, reason: getaddrinfo ENOTFOUND registry.npmjs.org"), domain.TaskFailed, depdoc.ReasonRegistryUnreachable},
		"not found":            {exited(1, "", "npm error code E404\nnpm error 404 Not Found - GET https://registry.npmjs.org/no-such-pkg-x - Not found"), domain.TaskFailed, ReasonDependencyNotFound},
		"auth":                 {exited(1, "", "npm error code E401\nnpm error Unable to authenticate"), domain.TaskFailed, ReasonRegistryAuth},
		"out of sync":          {exited(1, "", "npm error `npm ci` can only install packages when your package.json and package-lock.json or npm-shrinkwrap.json are in sync."), domain.TaskFailed, ReasonLockfileOutdated},
		"timeout":              {executor.Result{Status: executor.StatusTimedOut, Cleanup: executor.CleanupConfirmed}, domain.TaskTimedOut, depdoc.ReasonTimeout},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := jsJob(t, map[string]string{"package.json": pkgJSON, "package-lock.json": npmLock, "tsconfig.json": "{}", "a.ts": ""})
			r := &fakeRunner{fn: jsScript(func(executor.Spec) executor.Result { return tc.result })}
			res := handler(t, jsTools(t)).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r)
			if res.Status != tc.status || res.Reason != tc.reason {
				t.Fatalf("result %s/%s, want %s/%s", res.Status, res.Reason, tc.status, tc.reason)
			}
			if _, err := os.Stat(filepath.Join(job.Snapshot.WorkspaceDir, "node_modules")); err == nil {
				t.Fatal("a failed install left node_modules")
			}
			rec := readRecord(t, job, "deps.javascript")
			if rec.TreeUsable() || rec.InstalledDirs != nil {
				t.Fatalf("record %+v", rec)
			}
			if name == "not found" && (len(rec.FailedItems) != 1 || rec.FailedItems[0].Name != "no-such-pkg-x") {
				t.Fatalf("failed items %+v", rec.FailedItems)
			}
		})
	}
}

func TestJSFetchSkipsAndRefusals(t *testing.T) {
	tsFiles := func(extra map[string]string) map[string]string {
		out := map[string]string{"package.json": pkgJSON, "tsconfig.json": "{}", "a.ts": ""}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	tools := jsTools(t)
	noConsumer := tools
	noConsumer.JSConsumer = false
	noPnpm := tools
	noPnpm.PnpmScript = ""
	for name, tc := range map[string]struct {
		files  map[string]string
		cfg    Config
		status domain.TaskStatus
		reason string
	}{
		"no tsconfig":           {map[string]string{"package.json": pkgJSON, "a.js": ""}, tools, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencyConsumer},
		"no consumer usable":    {tsFiles(nil), noConsumer, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencyConsumer},
		"no dependencies":       {map[string]string{"package.json": `{"name":"a"}`, "tsconfig.json": "{}", "a.ts": ""}, tools, domain.TaskSkippedPolicy, depdoc.ReasonNoDependencies},
		"yarn berry":            {tsFiles(map[string]string{"yarn.lock": "__metadata:\n  version: 8\n"}), tools, domain.TaskUnsupported, ReasonLockfileFormatUnsupported},
		"bun":                   {tsFiles(map[string]string{"bun.lock": "{}"}), tools, domain.TaskUnsupported, ReasonLockfileFormatUnsupported},
		"pnpm not configured":   {tsFiles(map[string]string{"pnpm-lock.yaml": pnpmLock}), noPnpm, domain.TaskUnsupported, depdoc.ReasonPackageManagerNotConfigured},
		"corepack shim refused": {tsFiles(map[string]string{"pnpm-lock.yaml": pnpmLock}), func() Config { c := tools; c.PnpmScript = c.NodeExecutable; return c }(), domain.TaskUnsupported, depdoc.ReasonPackageManagerNotConfigured},
	} {
		t.Run(name, func(t *testing.T) {
			job, _ := jsJob(t, tc.files)
			r := &fakeRunner{fn: func(context.Context, executor.Spec) executor.Result {
				t.Fatal("nothing may run")
				return executor.Result{}
			}}
			res := handler(t, tc.cfg).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r)
			if res.Status != tc.status || res.Reason != tc.reason {
				t.Fatalf("result %s/%s, want %s/%s", res.Status, res.Reason, tc.status, tc.reason)
			}
		})
	}
}

// A workspace root's install records every node_modules the package
// manager built (root and members), each once.
func TestJSFetchRecordsMemberTrees(t *testing.T) {
	job, _ := jsJob(t, map[string]string{
		"package.json": `{"name":"mono","private":true,"devDependencies":{"typescript":"6.0.3"}}`, "pnpm-lock.yaml": pnpmLock, "pnpm-workspace.yaml": "packages: [packages/*]\n",
		"src/r.ts": "", "tsconfig.json": "{}",
		"packages/a/package.json": `{"name":"a"}`, "packages/a/a.ts": "", "packages/a/tsconfig.json": "{}",
	})
	r := &fakeRunner{fn: jsScript(func(executor.Spec) executor.Result { return exited(0, "", "") }, "packages/a")}
	res := handler(t, jsTools(t)).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("result %+v", res)
	}
	if rec := readRecord(t, job, "deps.javascript"); !slices.Equal(rec.InstalledDirs, []string{"node_modules", "packages/a/node_modules"}) {
		t.Fatalf("installed %v", rec.InstalledDirs)
	}
}

func TestClassifyJSAndVersion(t *testing.T) {
	for text, want := range map[string]string{
		" ERR_PNPM_OUTDATED_LOCKFILE  Cannot install with \"frozen-lockfile\"":                                                  ReasonLockfileOutdated,
		"error Your lockfile needs to be updated, but yarn was run with `--frozen-lockfile`.":                                   ReasonLockfileOutdated,
		" ERR_PNPM_FETCH_404  GET https://registry.npmjs.org/nope: Not Found - 404":                                             ReasonDependencyNotFound,
		"error An unexpected error occurred: \"https://registry.yarnpkg.com/nope: getaddrinfo ENOTFOUND registry.yarnpkg.com\"": depdoc.ReasonRegistryUnreachable,
		"something else": depdoc.ReasonFetchFailed,
	} {
		if got := classifyJS("", text); got != want {
			t.Errorf("classify %q = %s, want %s", text, got, want)
		}
	}
	for in, want := range map[string]string{"11.16.0\n": "11.16.0", "v1.22.22": "1.22.22", "Update available\n": ""} {
		if got, ok := parseManagerVersion([]byte(in)); got != want || ok != (want != "") {
			t.Errorf("version %q = %q %v", in, got, ok)
		}
	}
}

// ---- installed_dirs ---------------------------------------------------------

func jsRecord(key string) depdoc.Record {
	return depdoc.Record{SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind, Snapshot: "s", RunID: "r", AttemptID: "a", TaskKey: key,
		Module: depdoc.Module{ID: "javascript:web", Ecosystem: "javascript", Path: "web"}, Manager: depdoc.Manager{Name: "npm"}, Pinned: true,
		State: depdoc.StatePrepared, TaskStatus: "succeeded", Cache: depdoc.Cache{Kind: depdoc.CacheAttempt},
		InstalledDirs: []string{"web/node_modules", "web/pkg/a/node_modules"}, DroppedInputs: []depdoc.Dropped{}, FailedItems: []depdoc.Failed{}, Limitations: []string{}}
}

func TestInstalledDirsValidation(t *testing.T) {
	want := depdoc.Expect{Snapshot: "s", RunID: "r", AttemptID: "a", TaskKey: "deps.javascript"}
	if _, err := depdoc.Marshal(jsRecord("deps.javascript"), want); err != nil {
		t.Fatalf("good record: %v", err)
	}
	for name, edit := range map[string]func(*depdoc.Record){
		"empty but present": func(r *depdoc.Record) { r.InstalledDirs = []string{} },
		"shared cache": func(r *depdoc.Record) {
			k := jsCacheKeys[ManagerNpm]
			r.Cache = depdoc.Cache{Kind: depdoc.CacheShared, Key: &k}
		},
		"other ecosystem":      func(r *depdoc.Record) { r.Module = depdoc.Module{ID: "rust:web", Ecosystem: "rust", Path: "web"} },
		"not node_modules":     func(r *depdoc.Record) { r.InstalledDirs = []string{"web/vendor"} },
		"outside the module":   func(r *depdoc.Record) { r.InstalledDirs = []string{"other/node_modules"} },
		"nested in a tree":     func(r *depdoc.Record) { r.InstalledDirs = []string{"web/node_modules/x/node_modules"} },
		"unsorted":             func(r *depdoc.Record) { r.InstalledDirs = []string{"web/pkg/a/node_modules", "web/node_modules"} },
		"duplicate":            func(r *depdoc.Record) { r.InstalledDirs = []string{"web/node_modules", "web/node_modules"} },
		"escapes the snapshot": func(r *depdoc.Record) { r.InstalledDirs = []string{"web/../node_modules"} },
	} {
		r := jsRecord("deps.javascript")
		edit(&r)
		if _, err := depdoc.Marshal(r, want); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// InstalledTrees lists the trees of usable JS/TS records of this attempt.
func TestInstalledTreesReadsUsableRecords(t *testing.T) {
	scratch := t.TempDir()
	want := depdoc.Expect{Snapshot: "s", RunID: "r", AttemptID: "a"}
	write := func(r depdoc.Record) {
		w := want
		w.TaskKey = r.TaskKey
		b, err := depdoc.Marshal(r, w)
		if err != nil {
			t.Fatal(err)
		}
		p := depdoc.Path(scratch, r.TaskKey)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(jsRecord("deps.javascript"))
	member := jsRecord("deps.javascript.m01")
	member.Module = depdoc.Module{ID: "javascript:tools", Ecosystem: "javascript", Path: "tools"}
	member.InstalledDirs = []string{"tools/node_modules"}
	write(member)
	failed := jsRecord("deps.javascript.m02")
	failed.Module = depdoc.Module{ID: "javascript:docs", Ecosystem: "javascript", Path: "docs"}
	reason := depdoc.ReasonRegistryUnreachable
	failed.InstalledDirs, failed.State, failed.TaskStatus, failed.Reason = nil, depdoc.StateFailed, "failed", &reason
	write(failed)
	if got := depdoc.InstalledTrees(scratch, want); !slices.Equal(got, []string{"tools/node_modules", "web/node_modules", "web/pkg/a/node_modules"}) {
		t.Fatalf("trees = %v", got)
	}
	other := want
	other.AttemptID = "b"
	if got := depdoc.InstalledTrees(scratch, other); len(got) != 0 {
		t.Fatalf("another attempt's trees = %v", got)
	}
}

// A link of the installed tree that leaves the workspace copy (a file: or
// link: dependency) is removed and recorded; the install stays usable.
func TestJSFetchRemovesLinksOutsideTheWorkspace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory links need privileges on Windows")
	}
	job, _ := jsJob(t, map[string]string{"package.json": pkgJSON, "package-lock.json": npmLock, "tsconfig.json": "{}", "a.ts": ""})
	outside := t.TempDir()
	r := &fakeRunner{fn: jsScript(func(s executor.Spec) executor.Result {
		nm := filepath.Join(s.Dir, "node_modules")
		_ = os.Symlink(outside, filepath.Join(nm, "host"))
		_ = os.Symlink("is-number", filepath.Join(nm, "alias"))
		_ = os.Symlink("../../..", filepath.Join(nm, "is-number", "up"))
		return exited(0, "", "")
	})}
	res := handler(t, jsTools(t)).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r)
	if res.Status != domain.TaskSucceeded {
		t.Fatalf("result %+v", res)
	}
	rec := readRecord(t, job, "deps.javascript")
	var dropped []string
	for _, d := range rec.DroppedInputs {
		if d.Reason == ReasonLinkOutsideWorkspace {
			dropped = append(dropped, d.Path)
		}
	}
	if !slices.Equal(dropped, []string{"node_modules/host", "node_modules/is-number/up"}) || !slices.Contains(rec.Limitations, LimLinksOutsideRemoved) || !rec.TreeUsable() {
		t.Fatalf("record %+v", rec)
	}
	nm := filepath.Join(job.Snapshot.WorkspaceDir, "node_modules")
	for name, want := range map[string]bool{"host": false, "is-number/up": false, "alias": true} {
		if _, err := os.Lstat(filepath.Join(nm, filepath.FromSlash(name))); (err == nil) != want {
			t.Errorf("%s present=%v", name, err == nil)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("the link target was removed")
	}
}
