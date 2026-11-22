// Package architecture checks statically declared module dependencies and
// contracts without running project configuration or project code.
package architecture

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	DependencyCruiser = "dependency-cruiser"
	ImportLinter      = "import-linter"
	checkTimeout      = 2 * time.Minute
	checkOutput       = 8 << 20
	maxSourceFile     = 2 << 20
	maxSourceTotal    = 64 << 20
	maxSourceFiles    = 4096
	maxConfiguration  = 256 << 10
)

var (
	nodeVersion         = regexp.MustCompile(`^([1-9][0-9]*\.[0-9]+\.[0-9]+)\s*$`)
	importLinterVersion = regexp.MustCompile(`^import-linter ([1-9][0-9]*\.[0-9]+(?:\.[0-9]+)?)\s*$`)
	ruleName            = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
)

func Descriptor(name string) pack.Descriptor {
	d := pack.Descriptor{Name: name, Tool: name, VersionArgs: []string{"--version"}}
	pattern := nodeVersion
	if name == DependencyCruiser {
		d.ID, d.Ecosystem = "javascript.dependency_cruiser", "javascript"
		d.Toolchains, d.Interpreter = []string{"node"}, "node"
	} else {
		d.ID, d.Ecosystem = "python.import_linter", "python"
		pattern = importLinterVersion
	}
	d.ParseVersion = func(out []byte) (string, bool) {
		m := pattern.FindSubmatch(out)
		if m == nil {
			return "", false
		}
		return string(m[1]), true
	}
	return d
}

func PlanDependencyCruiser(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return plan(ctx, job, DependencyCruiser)
}

func PlanImportLinter(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return plan(ctx, job, ImportLinter)
}

func plan(ctx context.Context, job analysis.Job, name string) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// An ordinary Python package has no import contract.
	if name == ImportLinter && !hasImportConfiguration(job) {
		return nil, nil
	}
	d := Descriptor(name)
	files := inputs(job, name)
	if len(files) == 0 && !slices.Contains(pack.InScope(job.Snapshot.Manifest, job.Scope), d.Ecosystem) {
		return nil, nil
	}
	return anypack.PlanTask(job, d, name+" dependency rules", files), nil
}

func inputs(job analysis.Job, name string) []source.Entry {
	if name == DependencyCruiser {
		files, _ := anypack.StructureFiles(job, jsts.IsSource)
		return files
	}
	files, _ := anypack.StructureFiles(job, func(p string) bool {
		return strings.HasSuffix(p, ".py") && !slices.ContainsFunc(strings.Split(p, "/"), func(seg string) bool {
			return seg == "venv" || seg == "site-packages" || seg == "__pycache__"
		})
	})
	return files
}

type Analyzer struct {
	tool    *anypack.Tool
	desc    pack.Descriptor
	python  *anypack.Tool
	version string
}

func New(name, executable string, interpreter *pack.Toolchain) (*Analyzer, error) {
	t, err := anypack.NewTool(executable, interpreter)
	if err != nil {
		return nil, err
	}
	a := &Analyzer{tool: t, desc: Descriptor(name)}
	if name == DependencyCruiser {
		base := filepath.Base(t.Resolved)
		if base != "dependency-cruiser.mjs" || interpreter == nil {
			return nil, errors.New("dependency-cruiser needs its installed Node entry and a configured Node")
		}
	} else {
		python, err := importLinterPython(t.Resolved)
		if err != nil {
			return nil, err
		}
		a.python = python
	}
	return a, nil
}

func importLinterPython(entry string) (*anypack.Tool, error) {
	f, err := os.Open(entry)
	if err != nil {
		return nil, errors.New("cannot read installed Import Linter launcher")
	}
	defer f.Close()
	br := bufio.NewReader(io.LimitReader(f, 4096))
	line, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "#!") {
		return nil, errors.New("Import Linter needs a launcher from an installed Python environment")
	}
	python := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	real, err := nativepath.Resolve(python)
	if err != nil || !strings.HasPrefix(strings.ToLower(filepath.Base(real)), "python") {
		return nil, errors.New("Import Linter launcher has no absolute Python interpreter")
	}
	return anypack.NewTool(python, nil)
}

func (a *Analyzer) Descriptor() pack.Descriptor { return a.desc }
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return plan(ctx, job, a.desc.Name)
}
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	version, err := a.tool.Probe(ctx, runner, dir, a.desc.VersionArgs, a.desc.ParseVersion)
	if err == nil {
		a.version = version
	}
	return version, err
}

func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	run, failure := anypack.Start(ctx, job, task.Key, a.Plan)
	if failure != nil {
		return *failure
	}
	doc := a.document(run)
	var outcome anypack.Outcome
	files := inputs(job, a.desc.Name)
	if len(files) == 0 {
		outcome.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitNotRun)
	} else if job.Scope.Kind == "paths" && !slices.Contains(anypack.Roots(job.Scope), ".") {
		outcome.Fail(domain.TaskUnsupported, "dependency_graph_scope_requires_full", obsdoc.ExitNotRun)
	} else {
		stage, err := run.Mkdir("project")
		if err != nil {
			outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitNotRun)
		} else if err := stageSources(job.Snapshot.SnapshotDir, stage, files); err != nil {
			outcome.Fail(domain.TaskFailed, err.Error(), obsdoc.ExitNotRun)
		} else if a.desc.Name == DependencyCruiser {
			a.runCruiser(ctx, run, runner, stage, files, &doc, &outcome)
		} else {
			a.runImportLinter(ctx, run, runner, stage, files, &doc, &outcome)
		}
	}
	doc.Executions = anypack.ExecutionsOf(run.Executions)
	doc.Parse.OK = !outcome.Failed()
	outcome.Apply(&doc, doc.Parse.Records > 0)
	return run.Finish(doc)
}

func (a *Analyzer) document(run *anypack.Run) obsdoc.Document {
	d := a.desc
	c := obsdoc.Component{ID: d.Name, Version: "1"}
	doc := obsdoc.New(d.Analyzer(), c, c, run.Task.Spec.Key, run.Job.Snapshot.Digest)
	doc.Module.Dir, doc.Patterns, doc.Coverage.FilesInScope = run.Task.Module, run.Task.Patterns, run.Task.FilesInScope
	doc.Tool.Name, doc.Tool.Version, doc.Tool.VersionTested = d.Name, anypack.Ptr(a.version), VersionTested(d.Name, a.version)
	doc.RulePack = obsdoc.RulePack{ID: "codeye-" + d.Name, Version: "1", RepositoryConfig: obsdoc.RepoConfigNone,
		SourceSuppressions: obsdoc.SuppressNone, Selection: []string{},
		ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	if !doc.Tool.VersionTested {
		doc.Limitations = append(doc.Limitations, "tool_version_untested")
	}
	return doc
}

func VersionTested(name, version string) bool {
	if name == DependencyCruiser {
		return strings.HasPrefix(version, "18.8.")
	}
	return version == "2.17" || strings.HasPrefix(version, "2.17.")
}

func stageSources(snapshot, stage string, files []source.Entry) error {
	root, err := os.OpenRoot(snapshot)
	if err != nil {
		return errors.New("snapshot_integrity_failed")
	}
	defer root.Close()
	var total int64
	for i, f := range files {
		if i >= maxSourceFiles || f.Size == nil || *f.Size > maxSourceFile || total+*f.Size > maxSourceTotal {
			return errors.New("dependency_graph_input_limit")
		}
		body, err := readEntry(root, f, maxSourceFile)
		if err != nil {
			return err
		}
		if err := writeStage(stage, f.Path, body); err != nil {
			return errors.New("scratch_unavailable")
		}
		total += *f.Size
	}
	return nil
}

func readEntry(root *os.Root, f source.Entry, limit int64) ([]byte, error) {
	fd, err := root.Open(f.Path)
	if err != nil {
		return nil, errors.New("snapshot_integrity_failed")
	}
	defer fd.Close()
	body, err := io.ReadAll(io.LimitReader(fd, limit+1))
	if err != nil || f.Size == nil || int64(len(body)) != *f.Size || obsdoc.SHA256Hex(body) != f.SHA256 {
		return nil, errors.New("snapshot_integrity_failed")
	}
	return body, nil
}

func writeStage(stage, rel string, body []byte) error {
	if err := domain.ValidateEvidencePath(rel); err != nil {
		return err
	}
	p := filepath.Join(stage, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, body, 0o600)
}

func configuration(job analysis.Job, paths ...string) (source.Entry, []byte, error) {
	root, err := os.OpenRoot(job.Snapshot.SnapshotDir)
	if err != nil {
		return source.Entry{}, nil, err
	}
	defer root.Close()
	for _, path := range paths {
		for _, entry := range job.Snapshot.Manifest.Entries {
			if entry.Path != path || entry.Kind != "file" || entry.ExcludedReason != "" {
				continue
			}
			if entry.Size == nil || *entry.Size > maxConfiguration {
				return entry, nil, errors.New("dependency_graph_configuration_too_large")
			}
			body, err := readEntry(root, entry, maxConfiguration)
			return entry, body, err
		}
	}
	return source.Entry{}, nil, nil
}

func runCommand(ctx context.Context, runner executor.Runner, run *anypack.Run, name, stage string, spec executor.Spec, outcome *anypack.Outcome) *executor.Result {
	spec.Dir, spec.WorkspaceRoot = stage, stage
	res := run.Exec(ctx, runner, name, spec)
	outcome.Executions(run.Cancelled, res)
	if res == nil || outcome.Failed() {
		return nil
	}
	if !pack.StreamsComplete(res) {
		outcome.Fail(domain.TaskFailed, anypack.ReasonOutputTruncated, obsdoc.ExitExecutionError)
		return nil
	}
	return res
}

func parseFailure(doc *obsdoc.Document, outcome *anypack.Outcome) {
	doc.Parse.ShapeErrors++
	outcome.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
}

func observation(doc *obsdoc.Document, resolver *anypack.Resolver, exec, digest, file, rule string, line int, start, end int64) {
	path, location, state := resolver.Resolve(file, line, line, 0, 0)
	message := "Dependency rule violated: " + rule
	pos := fmt.Sprint(line)
	index := doc.Parse.Records
	doc.Observations = append(doc.Observations, obsdoc.Observation{
		ID:     obsdoc.ObservationID(doc.Analyzer.ID, doc.Module.Dir, file, rule, pos, pos, message, index),
		RuleID: rule, Category: "maintainability", RawMessage: message, Path: path, Location: location, LocationState: state, Unit: file,
		Related: []obsdoc.Related{}, RawRef: obsdoc.RawRef{Execution: exec, Stream: "stdout", StreamSHA256: digest, RecordIndex: index, ByteStart: start, ByteEnd: end}})
	doc.Parse.Records++
}

type jsonRecords struct {
	body   []byte
	offset int
}

func (r *jsonRecords) offsets(raw json.RawMessage) (int64, int64, bool) {
	at := strings.Index(string(r.body[r.offset:]), string(raw))
	if at < 0 {
		return 0, 0, false
	}
	start := r.offset + at
	r.offset = start + len(raw)
	return int64(start), int64(r.offset), true
}
