package deps

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Go reasons (docs/ANALYZERS.md#go-의존성-받기).
const (
	ReasonGoModuleNotOnProxy = "module_not_on_proxy"
	ReasonGoChecksumMismatch = "checksum_mismatch"
	ReasonGoSumIncomplete    = "go_sum_incomplete"
	ReasonGoModUnreadable    = "go_mod_unreadable"
	ReasonGoReplaceOutside   = "replace_outside_snapshot"
	ReasonSnapshotIntegrity  = "snapshot_integrity_failed"
	ReasonWorkspaceDrift     = "workspace_drift"
)

// GoCacheKey is the Go module cache under the dependency cache root. The go
// command verifies every module against go.sum and the checksum database,
// so one cache serves every project of the node.
const GoCacheKey = "go/mod"

// GoFetcher runs `go mod download` for every module go vet plans.
type GoFetcher struct{}

// Ecosystem implements Fetcher.
func (GoFetcher) Ecosystem() string { return "go" }

// Plan uses go vet's module planner: one module per go.mod the go command
// treats as a module, keyed with go vet's suffix.
func (GoFetcher) Plan(job analysis.Job) ([]Module, error) {
	mods, err := govet.PlanModules(job.Snapshot.Manifest, job.Scope, "go-vet", "go vet")
	if err != nil {
		return nil, err
	}
	out := make([]Module, 0, len(mods))
	for _, m := range mods {
		out = append(out, Module{Suffix: strings.TrimPrefix(m.Key, govet.HandlerName), Dir: m.ModuleDir, Title: "의존성 받기 (Go " + m.ModuleDir + ")"})
	}
	return out, nil
}

// goModJSON is the part of `go mod edit -json` the fetch reads.
type goModJSON struct {
	Module  struct{ Path string }
	Require []struct {
		Path, Version string
	}
	Replace []struct {
		New struct{ Path, Version string }
	}
}

// goDownload is one `go mod download -json` object.
type goDownload struct {
	Path, Version, Error string
}

// fileProxyURL is the GOPROXY form of a local download cache.
func fileProxyURL(dir string) string {
	p := filepath.ToSlash(dir)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x -> /C:/x
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Fetch implements Fetcher.
func (GoFetcher) Fetch(ctx context.Context, env *Env, m Module) Outcome {
	out := Outcome{Manager: depdoc.Manager{Name: "go"}, Pinned: true}
	fail := func(status domain.TaskStatus, state, reason string) Outcome {
		out.Status, out.State, out.Reason = status, state, reason
		return out
	}
	job := env.Job
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	if _, ok := files[govet.JoinRel(m.Dir, "vendor/modules.txt")]; ok {
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonVendoredDependencies)
	}
	if env.Config.GoExecutable == "" {
		return fail(domain.TaskUnsupported, depdoc.StateSkipped, depdoc.ReasonPackageManagerNotConfigured)
	}
	// The module files come from the read-only snapshot and must match the
	// manifest; the fetch reads and writes copies of them only.
	read := func(name string) ([]byte, bool, bool) {
		e, ok := files[govet.JoinRel(m.Dir, name)]
		if !ok {
			return nil, false, true
		}
		b, err := govet.ReadSnapshotFile(job.Snapshot.SnapshotDir, e.Path)
		if err != nil || sha256Hex(b) != e.SHA256 {
			return nil, true, false
		}
		return b, true, true
	}
	gomod, _, ok := read("go.mod")
	if !ok || gomod == nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
	}
	gosum, hasSum, ok := read("go.sum")
	if !ok {
		return fail(domain.TaskFailed, depdoc.StateFailed, ReasonSnapshotIntegrity)
	}
	if hasSum {
		p := govet.JoinRel(m.Dir, "go.sum")
		out.Lockfile = &depdoc.Lockfile{Path: p, SHA256: files[p].SHA256}
	}
	modDir := filepath.Join(env.TaskDir, "modfile")
	if err := os.Mkdir(modDir, 0o700); err != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	modCopy := filepath.Join(modDir, "go.mod")
	sumCopy := filepath.Join(modDir, "go.sum")
	if os.WriteFile(modCopy, gomod, 0o600) != nil || (hasSum && os.WriteFile(sumCopy, gosum, 0o600) != nil) {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	cache := env.CacheDir(GoCacheKey)
	work := map[string]string{}
	for _, d := range []string{"gocache", "gopath", "config"} {
		work[d] = filepath.Join(env.TaskDir, d)
		if os.Mkdir(work[d], 0o700) != nil {
			return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
		}
	}
	if os.MkdirAll(cache, 0o700) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonCacheQuotaExceeded)
	}
	proxy := env.Config.GoProxy
	if mc := env.Config.GoModCache; mc != "" {
		// The operator's module cache is read first, as a file proxy; the
		// go command never writes through a proxy.
		if st, err := os.Stat(filepath.Join(mc, "cache", "download")); err == nil && st.IsDir() {
			proxy = fileProxyURL(filepath.Join(mc, "cache", "download")) + "," + proxy
			out.Limitations = append(out.Limitations, depdoc.LimSeededFromOperatorCache)
		}
	}
	goExe := env.Config.GoExecutable
	vars := map[string]string{
		"PATH": filepath.Dir(goExe), "LANG": "C.UTF-8",
		"GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local",
		// The module cache stays writable so the node can remove it.
		"GOFLAGS": "-mod=readonly -modcacherw", "CGO_ENABLED": "0",
		"GOPROXY": proxy, "GOSUMDB": "sum.golang.org", "GOVCS": "*:off",
		"GOMODCACHE": cache, "GOCACHE": work["gocache"], "GOPATH": work["gopath"],
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("SystemRoot"); v != "" {
			vars["SystemRoot"] = v
		}
	}
	if govet.DisableTelemetry(vars, work["config"]) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, analysis.ReasonHandlerError)
	}
	workspace := job.Snapshot.WorkspaceDir
	spec := func(args ...string) executor.Spec {
		return executor.Spec{Executable: goExe, Args: args, WorkspaceRoot: workspace, Dir: filepath.Join(workspace, filepath.FromSlash(m.Dir)),
			ProtectedRoots: protectedRoots(job, env.Config.GoModCache), Env: vars, Redact: []string{env.Config.CacheRoot}}
	}
	stopped := func() Outcome {
		status, reason := StopOutcome(env.Stop())
		return fail(status, depdoc.StateFailed, reason)
	}

	res, ok := env.Exec(ctx, "go.version", spec("version"), "")
	if !ok {
		return stopped()
	}
	if v, _, vok := govet.ParseGoVersion(res.Stdout.Data); vok && exitedZero(res) {
		out.Manager.Version = &v
	} else {
		return fail(domain.TaskFailed, depdoc.StateFailed, depdoc.ReasonToolchainUnavailable)
	}
	res, ok = env.Exec(ctx, "go.modedit", spec("mod", "edit", "-json", modCopy), "")
	if !ok {
		return stopped()
	}
	var mod goModJSON
	if !exitedZero(res) || json.Unmarshal(res.Stdout.Data, &mod) != nil {
		return fail(domain.TaskFailed, depdoc.StateFailed, ReasonGoModUnreadable)
	}
	var dirs []string
	for _, r := range mod.Replace {
		if r.New.Version == "" {
			dirs = append(dirs, r.New.Path)
		}
	}
	if len(govet.EscapingReplaces(dirs, m.Dir)) > 0 {
		// go vet refuses the module for the same reason: the go command
		// would read a directory outside the snapshot.
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, ReasonGoReplaceOutside)
	}
	if len(mod.Require) == 0 {
		zero := 0
		out.Counts.Declared = &zero
		return fail(domain.TaskSkippedPolicy, depdoc.StateSkipped, depdoc.ReasonNoDependencies)
	}
	out.CacheKind, out.CacheKey = depdoc.CacheShared, GoCacheKey
	res, ok = env.Exec(ctx, "go.download", spec("mod", "download", "-json", "-modfile="+modCopy), filepath.Join(cache, "cache", "download"))
	parsed, items, failed := parseDownload(res.Stdout.Data)
	if parsed {
		declared := len(items)
		out.Counts = depdoc.Counts{Declared: &declared, Prepared: declared - len(failed), Failed: len(failed)}
		out.FailedItems = failed
	}
	if !ok {
		o := stopped()
		if out.Counts.Prepared > 0 {
			o.State = depdoc.StatePartial
		}
		return o
	}
	if drifted(job, files, m.Dir) {
		return fail(domain.TaskFailed, depdoc.StateFailed, ReasonWorkspaceDrift)
	}
	if !exitedZero(res) {
		reason := classifyGo(string(res.Stderr.Data) + "\n" + failedText(failed))
		for i := range out.FailedItems {
			out.FailedItems[i].Reason = classifyGo(out.FailedItems[i].Reason)
		}
		state := depdoc.StateFailed
		if out.Counts.Prepared > 0 {
			state = depdoc.StatePartial
		}
		return fail(domain.TaskFailed, state, reason)
	}
	if !parsed {
		return fail(domain.TaskFailed, depdoc.StateFailed, "output_unparsed")
	}
	// The analyzers read the workspace go.sum, never this copy. A sum the
	// download had to add is one go vet and staticcheck would miss.
	after, err := os.ReadFile(sumCopy)
	if (err == nil && (!hasSum || !bytes.Equal(after, gosum))) || (err != nil && hasSum) {
		return fail(domain.TaskFailed, depdoc.StatePartial, ReasonGoSumIncomplete)
	}
	out.Status, out.State = domain.TaskSucceeded, depdoc.StatePrepared
	return out
}

// protectedRoots are the read-only inputs of a fetch: the snapshot and the
// operator's module cache.
func protectedRoots(job analysis.Job, operatorCache string) []string {
	roots := []string{job.Snapshot.SnapshotDir}
	if operatorCache != "" {
		roots = append(roots, operatorCache)
	}
	return roots
}

func exitedZero(r executor.Result) bool {
	return r.Status == executor.StatusExited && r.ExitCode != nil && *r.ExitCode == 0
}

// parseDownload reads the JSON objects `go mod download -json` prints, one
// per module. failed lists the modules with an Error, the raw error text as
// the reason until it is classified.
func parseDownload(stdout []byte) (bool, []goDownload, []depdoc.Failed) {
	var items []goDownload
	var failed []depdoc.Failed
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		var d goDownload
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || d.Path == "" {
			return false, nil, nil
		}
		items = append(items, d)
		if d.Error != "" {
			failed = append(failed, depdoc.Failed{Name: d.Path + "@" + d.Version, Reason: d.Error})
		}
	}
	return true, items, failed
}

func failedText(failed []depdoc.Failed) string {
	var b strings.Builder
	for _, f := range failed {
		b.WriteString(f.Reason + "\n")
	}
	return b.String()
}

// classifyGo maps go command error text to a reason.
func classifyGo(text string) string {
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
	case has("checksum mismatch", "security error"):
		return ReasonGoChecksumMismatch
	case has("missing go.sum entry"):
		return ReasonGoSumIncomplete
	case has("requires go >=", "gotoolchain=local", "toolchain not available"):
		return depdoc.ReasonToolchainUnavailable
	case has("dial tcp", "connection refused", "no such host", "i/o timeout", "network is unreachable", "tls handshake", "connection reset", "proxyconnect"):
		return depdoc.ReasonRegistryUnreachable
	case has("404 not found", "410 gone", "not found:", "unknown revision", "invalid version", "no matching versions"):
		return ReasonGoModuleNotOnProxy
	}
	return depdoc.ReasonFetchFailed
}

// drifted reports whether the fetch changed the module's go.mod or go.sum in
// the workspace copy.
func drifted(job analysis.Job, files map[string]source.Entry, dir string) bool {
	for _, name := range []string{"go.mod", "go.sum"} {
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
