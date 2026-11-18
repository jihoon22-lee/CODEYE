package govet

import (
	"context"
	"runtime"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// This file exposes the go vet handler as a pack.Analyzer (docs/39) and the
// Go plumbing other Go analyzers share with it (W19 staticcheck). The
// wrappers do not change go vet behavior: Plan and Run are the handler's.

// Descriptor is go vet's pack identity: the baseline (required) Go analyzer.
func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: HandlerName, Ecosystem: "go", Required: true, Tool: "go", VersionArgs: []string{"version"},
		ParseVersion: func(b []byte) (string, bool) { v, _, ok := parseVersion(b); return v, ok }}
}

// Plan is go vet's tool-independent planner (the handler's plan, with scope).
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	planned, err := planTasks(job.Snapshot.Manifest, job.Scope)
	if err != nil {
		return nil, err
	}
	files := includedFiles(job.Snapshot.Manifest)
	modules := moduleDirs(files)
	out := make([]pack.Task, 0, len(planned))
	for _, p := range planned {
		n := 0
		if p.Unsupported == "" {
			n = len(goFilesInScope(files, modules, p.ModuleDir, p.Patterns))
		}
		out = append(out, pack.Task{Spec: analysis.TaskSpec{Key: p.Key, Required: true, Title: p.Title, Stage: domain.StageStaticAnalysis},
			Module: p.ModuleDir, Patterns: append([]string{}, p.Patterns...), FilesInScope: &n})
	}
	return out, nil
}

// member adapts the configured handler to pack.Analyzer.
type member struct{ h *Handler }

// Analyzer returns the configured go vet handler as a composite member.
func (h *Handler) Analyzer() pack.Analyzer { return member{h} }

func (m member) Descriptor() pack.Descriptor { return Descriptor() }
func (m member) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return Plan(ctx, job)
}
func (m member) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	return m.h.Run(ctx, job, task, runner)
}

// GoRoot returns the GOROOT of the resolved go executable.
func (h *Handler) GoRoot() string { return h.goroot }

// ModCache returns the resolved operator module cache ("" if none).
func (h *Handler) ModCache() string { return h.modcache }

// ResolvedGo returns the resolved go executable.
func (h *Handler) ResolvedGo() string { return h.resolvedGo }

// Env is the complete explicit environment go vet uses for every go
// execution. Other Go analyzers start from it so both see one build context.
func (h *Handler) Env(gocache, gopath, modcache string) map[string]string {
	return h.env(gocache, gopath, modcache)
}

// ---- shared Go plumbing ----------------------------------------------------

// ModuleTask is one Go module task derived from the manifest and scope.
type ModuleTask struct {
	Key, Title, ModuleDir string
	Patterns              []string
	// Unsupported is a limitation code when the snapshot or scope has no Go
	// module/package for this analyzer (only the no-module placeholder).
	Unsupported string
}

// PlanModules plans one task per Go module like go vet, keyed name,
// name.m01, ... (name instead of "go-vet"). With no Go module it returns
// nil: only go vet keeps its R0 placeholder task.
func PlanModules(m source.Manifest, scope domain.Scope, name, title string) ([]ModuleTask, error) {
	planned, err := planTasks(m, scope)
	if err != nil {
		return nil, err
	}
	var out []ModuleTask
	for _, p := range planned {
		if p.Unsupported != "" {
			return nil, nil
		}
		key := name
		if p.Key != HandlerName {
			key = name + p.Key[len(HandlerName):]
		}
		out = append(out, ModuleTask{Key: key, Title: title + p.Title[len("go vet"):], ModuleDir: p.ModuleDir, Patterns: append([]string{}, p.Patterns...)})
	}
	return out, nil
}

// IncludedFiles maps included regular file paths of the manifest to entries.
func IncludedFiles(m source.Manifest) map[string]source.Entry { return includedFiles(m) }

// ModuleDirs lists the go.mod directories the go command treats as modules.
func ModuleDirs(files map[string]source.Entry) []string { return moduleDirs(files) }

// GoFilesInScope lists the included .go files `go list <patterns>` could
// report for module mod.
func GoFilesInScope(files map[string]source.Entry, modules []string, mod string, patterns []string) []string {
	return goFilesInScope(files, modules, mod, patterns)
}

// OwningModule returns the deepest module dir containing p.
func OwningModule(p string, modules []string) (string, bool) { return owningModule(p, modules) }

// ReadSnapshotFile reads a snapshot-relative file through os.Root.
func ReadSnapshotFile(snapshotDir, rel string) ([]byte, error) {
	return readSnapshotFile(snapshotDir, rel)
}

// JoinRel joins a module dir and a module-relative name.
func JoinRel(dir, name string) string { return joinRel(dir, name) }

// ParseModEdit reads `go mod edit -json`.
func ParseModEdit(stdout []byte) (modulePath string, dirTargets []string, ok bool) {
	return parseModEdit(stdout)
}

// EscapingReplaces returns directory replace targets that leave the snapshot.
func EscapingReplaces(targets []string, moduleDir string) []string {
	return escapingReplaces(targets, moduleDir, runtime.GOOS == "windows")
}

// RedactForms returns the workspace roots in every form W05 must redact.
func RedactForms(roots []string) []string { return redactForms(roots) }

// Masker replaces node-absolute toolchain, cache, scratch and snapshot
// prefixes with stable placeholders and makes workspace paths relative.
func Masker(workspaceRoots []string, goroot, goexe, modcache, scratch, snapshotRoot string) func(string) string {
	return masker(workspaceRoots, goroot, goexe, modcache, scratch, snapshotRoot)
}

// StripWorkspace maps a tool path to a snapshot-relative path.
func StripWorkspace(p, moduleDir string, roots []string, allowRelative bool) (string, bool) {
	return stripWorkspace(p, moduleDir, roots, runtime.GOOS == "windows", allowRelative)
}

// ParseGoVersion reads `go version` stdout: version and GOOS/GOARCH.
func ParseGoVersion(stdout []byte) (version, platform string, ok bool) { return parseVersion(stdout) }

// GoVersionTested reports whether v is the Go minor the fixtures pin.
func GoVersionTested(v string) bool { return versionTested(v) }

// WorkspaceDrift compares the module's Go inputs in the workspace with the
// manifest after a run.
func WorkspaceDrift(workspace string, files map[string]source.Entry, modules []string, mod string) map[string]bool {
	return workspaceDrift(workspace, files, modules, mod)
}

// ListFields is the go list -json field list.
const ListFields = listFields

// ListPackage is the subset of `go list -e -json` an adapter reads.
type ListPackage = listPackage

// ParseList parses `go list -e -json` stdout.
func ParseList(stdout []byte) (packages []ListPackage, unparsed int64, decodeError string) {
	s := parseListStream(stdout)
	return s.Packages, s.UnparsedBytes, s.DecodeError
}

// GoErrorKind classifies go command error text: toolchain, module_missing,
// dependency or "".
func GoErrorKind(s string) string {
	switch {
	case isToolchainError(s):
		return "toolchain"
	case isModuleMissing(s):
		return "module_missing"
	case isDependencyError(s):
		return "dependency"
	}
	return ""
}

// SplitPosition splits "path:line[:col]" from the right.
func SplitPosition(p string) (path string, line, col int, ok bool) { return splitPosition(p) }

// Executable returns the configured go executable path (as given).
func (h *Handler) Executable() string { return h.cfg.GoExecutable }
