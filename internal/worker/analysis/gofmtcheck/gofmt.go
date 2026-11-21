// Package gofmtcheck reports files that differ from the configured Go
// toolchain's formatter without modifying them or loading packages.
package gofmtcheck

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	Name       = "gofmt"
	AnalyzerID = "go.gofmt"
	Rule       = "format_mismatch"

	maxFileBytes  = 2 << 20
	maxInputBytes = 64 << 20
	maxArgBytes   = 12 << 10
	maxBatches    = 64
	checkTimeout  = 2 * time.Minute
)

type Analyzer struct {
	tool    *anypack.Tool
	version string
}

// New uses the sibling binary of the configured Go toolchain. Its build
// information identifies both the formatter and the version without PATH
// lookup, invoking a version subprocess, or reading the target repository.
func New(goExecutable string) (*Analyzer, error) {
	resolved, err := nativepath.Resolve(goExecutable)
	if err != nil {
		return nil, errors.New("configured Go toolchain path cannot be resolved")
	}
	name := Name
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	tool, err := anypack.NewTool(filepath.Join(filepath.Dir(resolved), name), nil)
	if err != nil {
		return nil, errors.New("gofmt binary is absent next to the Go toolchain")
	}
	info, err := buildinfo.ReadFile(tool.Resolved)
	if err != nil || info.Path != "cmd/gofmt" || versionValue(info.GoVersion) == "" {
		return nil, errors.New("gofmt binary has no recognized Go build information")
	}
	return &Analyzer{tool: tool, version: info.GoVersion}, nil
}

func versionValue(v string) string {
	version, _, _ := govet.ParseGoVersion([]byte("go version " + v + " " + runtime.GOOS + "/" + runtime.GOARCH))
	return version
}

func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: Name, Tool: "go", Ecosystem: "go", ParseVersion: func(out []byte) (string, bool) {
		version, _, ok := govet.ParseGoVersion(out)
		return version, ok
	}}
}

func eligible(path string) bool { return strings.HasSuffix(path, ".go") }

func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return anypack.PlanTask(job, Descriptor(), "formatting: Go source (gofmt)", anypack.Files(job, eligible, true)), nil
}

func (a *Analyzer) Descriptor() pack.Descriptor { return Descriptor() }

func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return Plan(ctx, job)
}

func inputs(files []source.Entry) ([]source.Entry, [][]string) {
	var selected []source.Entry
	var batches [][]string
	var total int64
	var args []string
	var argBytes int
	for _, file := range files {
		arg := "./" + file.Path
		size := len(arg) + 3
		if file.Size == nil || *file.Size > maxFileBytes || total+*file.Size > maxInputBytes || size > maxArgBytes {
			continue
		}
		if argBytes+size > maxArgBytes {
			batches = append(batches, args)
			if len(batches) == maxBatches {
				return selected, batches
			}
			args, argBytes = nil, 0
		}
		total += *file.Size
		selected = append(selected, file)
		args = append(args, arg)
		argBytes += size
	}
	if len(args) > 0 {
		batches = append(batches, args)
	}
	return selected, batches
}

type batch struct {
	name   string
	args   []string
	result *executor.Result
}

func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	run, failure := anypack.Start(ctx, job, task.Key, Plan)
	if failure != nil {
		return *failure
	}
	component := obsdoc.Component{ID: Name, Version: "1"}
	doc := obsdoc.New(Descriptor().Analyzer(), component, component, task.Key, job.Snapshot.Digest)
	doc.Module.Dir = anypack.ModuleDir
	doc.Patterns = run.Task.Patterns
	doc.Tool.Name, doc.Tool.Version, doc.Tool.VersionTested = Name, anypack.Ptr(a.version), govet.GoVersionTested(a.version)
	doc.RulePack = obsdoc.RulePack{ID: "codeye-go-format", Version: "1", Selection: []string{"-l"},
		RepositoryConfig: obsdoc.RepoConfigNone, SourceSuppressions: obsdoc.SuppressNone, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Coverage.FilesInScope = run.Task.FilesInScope
	doc.Limitations = anypack.RecordExclusions(&doc, anypack.ExcludedDataDirFiles(job, eligible))
	if !doc.Tool.VersionTested {
		doc.Limitations = append(doc.Limitations, "tool_version_untested")
	}

	files := anypack.Files(job, eligible, true)
	selected, batches := inputs(files)
	if skipped := len(files) - len(selected); skipped > 0 {
		doc.Coverage.FilesSkipped = skipped
		doc.Coverage.Units["format_input_limit"] = skipped
		doc.Limitations = append(doc.Limitations, "format_input_limit")
	}

	var outcome anypack.Outcome
	var completed []batch
	var results []*executor.Result
	var timedOut bool
	switch {
	case !anypack.LinkFreeFiles(run.Workspace, selected):
		outcome.Fail(domain.TaskFailed, anypack.ReasonWorkspaceLink, obsdoc.ExitNotRun)
	case len(selected) == 0:
		outcome.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitNotRun)
	default:
		deadline := time.Now().Add(checkTimeout)
		for i, paths := range batches {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				timedOut = true
				break
			}
			name := fmt.Sprintf("gofmt.check.b%03d", i+1)
			args := append([]string{"-l", "--"}, paths...)
			result := run.Exec(ctx, runner, name, run.Spec(a.tool, a.tool.Env(nil), remaining, 32<<10, 64<<10, args...))
			completed = append(completed, batch{name: name, args: paths, result: result})
			results = append(results, result)
			if pack.Stoppable(result) {
				break
			}
		}
	}
	outcome.Executions(run.Cancelled, results...)
	if timedOut {
		outcome.Fail(domain.TaskTimedOut, pack.ReasonTimeout, obsdoc.ExitTimeout)
	}

	doc.Executions = anypack.ExecutionsOf(run.Executions)
	if len(doc.Executions) > 0 && anypack.LinkFreeFiles(run.Workspace, selected) {
		doc.WorkspaceDrift = anypack.Drift(run.Workspace, selected)
	} else if len(doc.Executions) > 0 {
		doc.WorkspaceDrift = anypack.Paths(selected)
	}
	if len(doc.WorkspaceDrift) > 0 {
		outcome.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	resolver := anypack.NewResolver(job.Snapshot.SnapshotDir, selected, doc.WorkspaceDrift)
	for _, b := range completed {
		if b.result == nil {
			continue
		}
		if !pack.StreamsComplete(b.result) {
			outcome.Fail(domain.TaskFailed, anypack.ReasonOutputTruncated, obsdoc.ExitExecutionError)
		}
		if pack.ExitCode(b.result) != 0 || len(b.result.Stderr.Data) != 0 {
			outcome.Fail(domain.TaskFailed, "gofmt_failed", obsdoc.ExitExecutionError)
		}
		readMismatches(&doc, b, resolver)
	}
	if doc.Parse.UnparsedBytes > 0 || doc.Parse.ShapeErrors > 0 {
		outcome.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	if !outcome.Failed() {
		doc.Coverage.ProcessedPaths = anypack.Paths(selected)
		doc.Coverage.Units["format_checked"] = len(selected)
	}
	doc.Parse.OK = !outcome.Failed()
	outcome.Apply(&doc, doc.Parse.Records > 0)
	return run.Finish(doc)
}

// gofmt -l writes the same relative filename it received, once per
// nonconforming file. The stream contains no patch or source content.
func readMismatches(doc *obsdoc.Document, b batch, resolver *anypack.Resolver) {
	listed := make(map[string]bool, len(b.args))
	for _, arg := range b.args {
		listed[arg] = true
	}
	out := b.result.Stdout.Data
	if len(out) == 0 {
		return
	}
	sha := obsdoc.SHA256Hex(out)
	var off int64
	for i, raw := range strings.SplitAfter(string(out), "\n") {
		if raw == "" {
			continue
		}
		start := off
		off += int64(len(raw))
		if !strings.HasSuffix(raw, "\n") {
			doc.Parse.UnparsedBytes += int64(len(raw))
			continue
		}
		arg := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if !listed[arg] {
			doc.Parse.ShapeErrors++
			continue
		}
		delete(listed, arg)
		p := strings.TrimPrefix(arg, "./")
		msg := "file differs from gofmt output"
		path, location, state := resolver.Resolve(p, 1, 1, 0, 0)
		doc.Observations = append(doc.Observations, obsdoc.Observation{
			ID:     obsdoc.ObservationID(AnalyzerID, anypack.ModuleDir, p, Rule, p, p, msg, 0),
			RuleID: Rule, Category: "maintainability", RawMessage: msg, Path: path, Location: location, LocationState: state,
			Related: []obsdoc.Related{}, Unit: p,
			RawRef: obsdoc.RawRef{Execution: b.name, Stream: "stdout", StreamSHA256: sha, RecordIndex: i, ByteStart: start, ByteEnd: off},
		})
		doc.Parse.Records++
	}
}
