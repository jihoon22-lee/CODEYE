// Package errcheck checks discarded errors in the selected Go packages.
package errcheck

import (
	"bytes"
	"context"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const Name = "errcheck"
const rule = "unchecked_error"

var versionPattern = regexp.MustCompile(`^errcheck (v[0-9]+\.[0-9]+\.[0-9]+)$`)

func parseVersion(body []byte) (string, bool) {
	parts := versionPattern.FindStringSubmatch(strings.TrimSpace(string(body)))
	if parts == nil {
		return "", false
	}
	return parts[1], true
}

func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: "go.errcheck", Name: Name, Tool: Name, Ecosystem: "go", VersionArgs: []string{"-version"}, ParseVersion: parseVersion}
}

func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	modules, err := govet.PlanModules(job.Snapshot.Manifest, job.Scope, Name, "unchecked Go error returns")
	if err != nil {
		return nil, err
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	dirs := govet.ModuleDirs(files)
	tasks := make([]pack.Task, 0, len(modules))
	for _, m := range modules {
		count := len(govet.GoFilesInScope(files, dirs, m.ModuleDir, m.Patterns))
		tasks = append(tasks, pack.Task{Spec: analysis.TaskSpec{Key: m.Key, Title: m.Title, Stage: domain.StageStaticAnalysis}, Module: m.ModuleDir, Patterns: m.Patterns, FilesInScope: &count})
	}
	return tasks, nil
}

type Analyzer struct {
	tool    *anypack.Tool
	goTool  *govet.Handler
	version string
}

func New(exe string, goTool *govet.Handler) (*Analyzer, error) {
	if goTool == nil {
		return nil, errors.New("errcheck needs the Go toolchain")
	}
	tool, err := anypack.NewTool(exe, nil)
	if err != nil {
		return nil, err
	}
	return &Analyzer{tool: tool, goTool: goTool}, nil
}
func (a *Analyzer) Descriptor() pack.Descriptor { return Descriptor() }
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return Plan(ctx, job)
}
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	v, err := a.tool.Probe(ctx, runner, dir, Descriptor().VersionArgs, parseVersion)
	a.version = v
	return v, err
}
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	run, failure := anypack.Start(ctx, job, task.Key, Plan)
	if failure != nil {
		return *failure
	}
	desc := Descriptor()
	component := obsdoc.Component{ID: Name, Version: "1"}
	doc := obsdoc.New(desc.Analyzer(), component, component, task.Key, job.Snapshot.Digest)
	module := run.Task.Module
	doc.Module.Dir = module
	doc.Patterns = run.Task.Patterns
	doc.Tool.Name, doc.Tool.Version, doc.Tool.VersionTested = Name, anypack.Ptr(a.version), a.version == "v1.30.0"
	doc.RulePack = obsdoc.RulePack{ID: "codeye-go-errcheck", Version: "1", Selection: []string{"-mod=readonly"}, RepositoryConfig: obsdoc.RepoConfigNone, SourceSuppressions: obsdoc.SuppressNone, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Coverage.FilesInScope = run.Task.FilesInScope
	doc.Limitations = []string{"active_go_build_context"}
	cache, err := run.Mkdir("gocache")
	if err != nil {
		return pack.Failed("scratch_unavailable")
	}
	gopath, err := run.Mkdir("gopath")
	if err != nil {
		return pack.Failed("scratch_unavailable")
	}
	modcache, _, lim := a.goTool.ModuleCache(job, task.Key, Name, module)
	if modcache == "" {
		modcache = filepath.Join(gopath, "pkg", "mod")
	}
	if lim != "" {
		doc.Limitations = append(doc.Limitations, lim)
	}
	env := a.goTool.Env(cache, gopath, modcache)
	if err := govet.DisableTelemetry(env, run.Dir); err != nil {
		return pack.Failed("scratch_unavailable")
	}
	spec := run.Spec(a.tool, env, anypack.DefaultTimeout, 8<<20, 1<<20)
	spec.Dir = filepath.Join(run.Workspace, filepath.FromSlash(module))
	spec.Redact = append(spec.Redact, govet.RedactForms([]string{a.goTool.GoRoot(), a.goTool.Executable(), modcache})...)
	var outcome anypack.Outcome
	var results []*executor.Result
	// go mod edit reads the manifest without loading packages. Inspect local
	// replacements before allowing the analyzer's go/packages loader to run.
	modSpec := spec
	modSpec.Executable = a.goTool.Executable()
	modSpec.Args = []string{"mod", "edit", "-json"}
	mod := run.Exec(ctx, runner, "go.modedit", modSpec)
	results = append(results, mod)
	var list, check *executor.Result
	if pack.ExitedZero(mod) && pack.StreamsComplete(mod) {
		_, local, ok := govet.ParseModEdit(mod.Stdout.Data)
		if !ok || len(govet.EscapingReplaces(local, module)) > 0 {
			outcome.Fail(domain.TaskUnsupported, "go_module_unavailable", obsdoc.ExitNotRun)
		} else if !anypack.LinkFreeTree(run.Workspace, []string{"."}) {
			outcome.Fail(domain.TaskUnsupported, anypack.ReasonWorkspaceLink, obsdoc.ExitNotRun)
		} else {
			listSpec := spec
			listSpec.Executable = a.goTool.Executable()
			listSpec.Args = append([]string{"list", "-mod=readonly", "-json=" + govet.ListFields}, run.Task.Patterns...)
			list = run.Exec(ctx, runner, "go.list", listSpec)
			results = append(results, list)
			spec.Args = append([]string{"-mod=readonly"}, run.Task.Patterns...)
			if pack.ExitedZero(list) && pack.StreamsComplete(list) {
				check = run.Exec(ctx, runner, "errcheck.check", spec)
				results = append(results, check)
			}
		}
	}
	outcome.Executions(run.Cancelled, results...)
	if !outcome.Failed() && (check == nil || !pack.StreamsComplete(check) || pack.ExitCode(check) > 1 || len(check.Stderr.Data) > 0) {
		outcome.Fail(domain.TaskFailed, "errcheck_packages_unavailable", obsdoc.ExitExecutionError)
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	dirs := govet.ModuleDirs(files)
	var selected []source.Entry
	for _, p := range govet.GoFilesInScope(files, dirs, module, run.Task.Patterns) {
		selected = append(selected, files[p])
	}
	if check != nil {
		doc.Coverage.ProcessedPaths = checkedFiles(list.Stdout.Data, module, run.Roots, selected)
		if len(doc.Coverage.ProcessedPaths) == 0 {
			outcome.Fail(domain.TaskUnsupported, "go_packages_unavailable", obsdoc.ExitConfigurationError)
		}
	}
	if check != nil {
		doc.WorkspaceDrift = anypack.Drift(run.Workspace, selected)
		if len(doc.WorkspaceDrift) > 0 {
			outcome.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
		}
		if pack.ExitCode(check) <= 1 && pack.StreamsComplete(check) {
			parseDiagnostics(&doc, check.Stdout.Data, module, run.Roots, selected, anypack.NewResolver(job.Snapshot.SnapshotDir, selected, doc.WorkspaceDrift))
			if doc.Parse.ShapeErrors > 0 || (pack.ExitCode(check) == 1) != (doc.Parse.Records > 0) {
				outcome.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
			}
		}
	}
	doc.Executions = anypack.ExecutionsOf(run.Executions)
	doc.Parse.OK = !outcome.Failed()
	outcome.Apply(&doc, doc.Parse.Records > 0)
	return run.Finish(doc)
}

// The loader uses the same toolchain, package patterns, working directory, cache and build flags.
func checkedFiles(body []byte, module string, roots []string, selected []source.Entry) []string {
	pkgs, unparsed, failure := govet.ParseList(body)
	if unparsed != 0 || failure != "" {
		return nil
	}
	allowed := map[string]bool{}
	for _, file := range selected {
		allowed[file.Path] = true
	}
	var result []string
	for _, pkg := range pkgs {
		if pkg.Error != nil || pkg.Incomplete || len(pkg.DepsErrors) > 0 {
			return nil
		}
		dir, ok := govet.StripWorkspace(pkg.Dir, module, roots, true)
		if !ok {
			continue
		}
		for _, srcs := range [][]string{pkg.GoFiles, pkg.TestGoFiles, pkg.XTestGoFiles} {
			for _, base := range srcs {
				file := path.Join(dir, base)
				if allowed[file] {
					result = append(result, file)
				}
			}
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func parseDiagnostics(doc *obsdoc.Document, data []byte, module string, roots []string, files []source.Entry, resolver *anypack.Resolver) {
	scope := map[string]bool{}
	for _, f := range files {
		scope[f.Path] = true
	}
	sha := obsdoc.SHA256Hex(data)
	for start, i := 0, 0; start < len(data); i++ {
		n := bytes.IndexByte(data[start:], '\n')
		end := len(data)
		if n >= 0 {
			end = start + n + 1
		}
		pos, _, ok := strings.Cut(string(data[start:end]), ":\t")
		p, line, col, valid := govet.SplitPosition(pos)
		rel, under := govet.StripWorkspace(p, module, roots, true)
		if !ok || !valid || !under || line <= 0 {
			doc.Parse.ShapeErrors++
		} else {
			doc.Parse.Records++
			if scope[rel] {
				name, loc, state := resolver.Resolve(rel, line, line, col, col)
				message := "returned error is discarded"
				doc.Observations = append(doc.Observations, obsdoc.Observation{ID: obsdoc.ObservationID("go.errcheck", module, rel, rule, rel, strconv.Itoa(line)+":"+strconv.Itoa(col), message, 0), RuleID: rule, Category: "error_handling", RawMessage: message, Path: name, Location: loc, LocationState: state, Unit: rel, Related: []obsdoc.Related{}, RawRef: obsdoc.RawRef{Execution: "errcheck.check", Stream: "stdout", StreamSHA256: sha, RecordIndex: i, ByteStart: int64(start), ByteEnd: int64(end)}})
			}
		}
		start = end
	}
}
