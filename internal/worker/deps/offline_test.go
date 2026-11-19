package deps

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Offline with neither a registry nor an operator module cache: nothing is
// downloaded and the task says why.
func TestOfflineGoWithoutRegistryOrCache(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result {
		t.Fatal("offline without a registry must not download")
		return executor.Result{}
	})}
	res := handler(t, Config{Offline: true}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskSkippedPolicy || res.Reason != depdoc.ReasonOfflineNoRegistry {
		t.Fatalf("result = %s/%s", res.Status, res.Reason)
	}
	if rec := readRecord(t, job, "deps.go"); rec.Reason == nil || *rec.Reason != depdoc.ReasonOfflineNoRegistry || rec.State != depdoc.StateSkipped {
		t.Fatalf("record %+v", rec)
	}
}

// Offline with only the operator's module cache: it is the one proxy, no
// public checksum database is asked, and a module it lacks ends with the
// offline reason (the failed items keep their own).
func TestOfflineGoReadsOnlyTheOperatorCache(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	operator := t.TempDir()
	writeTree(t, operator, map[string]string{"cache/download/example.com/other/@v/v1.0.0.mod": "module example.com/other\n"})
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result {
		return exited(1, `{"Path":"github.com/spf13/pflag","Version":"v1.0.5","Error":"github.com/spf13/pflag@v1.0.5: reading file:///x/github.com/spf13/pflag/@v/v1.0.5.info: no such file or directory"}`,
			"go: github.com/spf13/pflag@v1.0.5: not found: no such file\n")
	})}
	res := handler(t, Config{Offline: true, GoModCache: operator, CABundle: bundle}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskFailed || res.Reason != depdoc.ReasonOfflineNoRegistry {
		t.Fatalf("result = %s/%s", res.Status, res.Reason)
	}
	env := r.specs[2].Env
	if want := fileProxyURL(filepath.Join(operator, "cache", "download")); env["GOPROXY"] != want || env["GOSUMDB"] != "off" {
		t.Fatalf("GOPROXY %q GOSUMDB %q", env["GOPROXY"], env["GOSUMDB"])
	}
	for k, v := range executor.CAEnv(bundle) {
		if env[k] != v {
			t.Fatalf("%s = %q, want the CA bundle", k, env[k])
		}
	}
	rec := readRecord(t, job, "deps.go")
	if len(rec.FailedItems) != 1 || rec.FailedItems[0].Reason != ReasonGoModuleNotOnProxy {
		t.Fatalf("failed items %+v", rec.FailedItems)
	}
}

// The go command stops before listing modules when a go.mod of the graph is
// missing from the operator's cache (what go 1.27 prints): the module it
// names is still a failed item, so the operator knows what to add.
func TestOfflineGoNamesTheModuleMissingBeforeTheList(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	operator := t.TempDir()
	writeTree(t, operator, map[string]string{"cache/download/example.com/other/@v/v1.0.0.mod": "module example.com/other\n"})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result {
		return exited(1, "", "go: github.com/spf13/pflag@v1.0.6: reading file:///x/github.com/spf13/pflag/@v/v1.0.6.mod: no such file or directory\n"+
			"go: /abs/path@v1: ignored\n")
	})}
	res := handler(t, Config{Offline: true, GoModCache: operator}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Status != domain.TaskFailed || res.Reason != depdoc.ReasonOfflineNoRegistry {
		t.Fatalf("result = %s/%s", res.Status, res.Reason)
	}
	rec := readRecord(t, job, "deps.go")
	if len(rec.FailedItems) != 1 || rec.FailedItems[0].Name != "github.com/spf13/pflag@v1.0.6" || rec.FailedItems[0].Reason != ReasonGoModuleNotOnProxy {
		t.Fatalf("failed items %+v", rec.FailedItems)
	}
}

// Offline with an internal proxy: it is used as written with the operator's
// checksum database, and its own failures keep their reason.
func TestOfflineGoUsesTheInternalProxy(t *testing.T) {
	job, _ := jobOf(t, map[string]string{"go.mod": goMod, "go.sum": goSum, "main.go": "package main\n"})
	r := &fakeRunner{fn: goScript(func(context.Context, executor.Spec) executor.Result {
		return exited(1, "", "go: github.com/spf13/pflag@v1.0.5: reading https://mirror.internal/go/github.com/spf13/pflag/@v/v1.0.5.info: 404 Not Found\n")
	})}
	res := handler(t, Config{Offline: true, GoProxy: "https://mirror.internal/go", GoSumDB: "sum.internal"}).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.go"}, r)
	if res.Reason != ReasonGoModuleNotOnProxy {
		t.Fatalf("reason %s", res.Reason)
	}
	if env := r.specs[2].Env; env["GOPROXY"] != "https://mirror.internal/go" || env["GOSUMDB"] != "sum.internal" {
		t.Fatalf("GOPROXY %q GOSUMDB %q", env["GOPROXY"], env["GOSUMDB"])
	}
	if _, ok := r.specs[2].Env["SSL_CERT_FILE"]; ok {
		t.Fatal("no CA bundle configured, none passed")
	}
}

func TestOfflineJSAndPythonAndRustNeedARegistry(t *testing.T) {
	never := func(executor.Spec) executor.Result {
		t.Fatal("offline without a registry must not install")
		return executor.Result{}
	}
	job, _ := jsJob(t, map[string]string{"package.json": pkgJSON, "package-lock.json": npmLock, "tsconfig.json": "{}", "src/a.ts": "export const a = 1\n"})
	cfg := jsTools(t)
	cfg.Offline = true
	r := &fakeRunner{fn: jsScript(never)}
	if res := handler(t, cfg).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r); res.Status != domain.TaskSkippedPolicy || res.Reason != depdoc.ReasonOfflineNoRegistry {
		t.Fatalf("js result %s/%s", res.Status, res.Reason)
	}

	// With the internal registry the install runs and trusts the CA bundle.
	bundle := filepath.Join(t.TempDir(), "ca.pem")
	cfg.NpmRegistry, cfg.CABundle = "https://npm.internal/", bundle
	job, _ = jsJob(t, map[string]string{"package.json": pkgJSON, "package-lock.json": npmLock, "tsconfig.json": "{}", "src/a.ts": "export const a = 1\n"})
	r = &fakeRunner{fn: jsScript(func(executor.Spec) executor.Result { return exited(0, "added 2 packages\n", "") })}
	if res := handler(t, cfg).Run(context.Background(), job, analysis.TaskSpec{Key: "deps.javascript"}, r); res.Status != domain.TaskSucceeded {
		t.Fatalf("js with a registry %s/%s", res.Status, res.Reason)
	}
	install := r.specs[len(r.specs)-1]
	if i := slices.Index(install.Args, "--registry"); i < 0 || install.Args[i+1] != "https://npm.internal/" || install.Env["NODE_EXTRA_CA_CERTS"] != bundle {
		t.Fatalf("install argv %v env %v", install.Args, install.Env)
	}

	pyJob, _ := jobOf(t, map[string]string{"pyproject.toml": "[project]\nname='a'\n", "a.py": "import idna\n"})
	ps := &pyScript{facts: factsJSON("idna==3.10"), install: func(int, executor.Spec) executor.Result { return never(executor.Spec{}) }}
	if res := pyHandler(t, Config{Offline: true}).Run(context.Background(), pyJob, analysis.TaskSpec{Key: "deps.python"}, &fakeRunner{fn: ps.run}); res.Status != domain.TaskSkippedPolicy || res.Reason != depdoc.ReasonOfflineNoRegistry {
		t.Fatalf("python result %s/%s", res.Status, res.Reason)
	}
	pyJob, _ = jobOf(t, map[string]string{"pyproject.toml": "[project]\nname='a'\n", "a.py": "import idna\n"})
	ps = &pyScript{facts: factsJSON("idna==3.10"), install: func(_ int, s executor.Spec) executor.Result { return installInto(s) }}
	r = &fakeRunner{fn: ps.run}
	if res := pyHandler(t, Config{Offline: true, PipIndexURL: "https://pypi.internal/simple", CABundle: bundle}).Run(context.Background(), pyJob, analysis.TaskSpec{Key: "deps.python"}, r); res.Status != domain.TaskSucceeded {
		t.Fatalf("python with an index %s/%s", res.Status, res.Reason)
	}
	for _, s := range r.specs {
		if slices.Contains(s.Args, "install") && (s.Env["PIP_CERT"] != bundle || !strings.Contains(strings.Join(s.Args, " "), "--index-url https://pypi.internal/simple")) {
			t.Fatalf("pip install argv %v env %v", s.Args, s.Env)
		}
	}

	rustJob, _ := rustJob(t, map[string]string{"Cargo.toml": cargoToml, "Cargo.lock": cargoLock, "src/main.rs": "fn main() {}\n"})
	rustJob.AllowRepositoryCode = true
	r = &fakeRunner{fn: cargoScript("", never)}
	if res := handler(t, Config{Offline: true, CargoExecutable: fakeCargo(t), RustConsumer: true}).Run(context.Background(), rustJob, analysis.TaskSpec{Key: "deps.rust"}, r); res.Status != domain.TaskSkippedPolicy || res.Reason != depdoc.ReasonOfflineNoRegistry {
		t.Fatalf("rust result %s/%s", res.Status, res.Reason)
	}
}
