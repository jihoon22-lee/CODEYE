package cpp

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

const (
	GCCAnalyzerID              = "cpp.gcc_analyzer"
	GCCAnalyzerName            = "gcc-analyzer"
	GCCAnalyzerTool            = "gcc"
	GCCAnalyzerRulePackID      = "codeye-c-gcc-file-descriptors"
	GCCAnalyzerRulePackVersion = "1"
	GCCAnalyzerTestedRelease   = "13.4,15.2"
	reasonGCCCompile           = "gcc_analyzer_failed"
	reasonGCCUnavailable       = "gcc_analyzer_unavailable"
	reasonGCCLanguage          = "gcc_analyzer_c_only"
	reasonGCCTooComplex        = "gcc_analyzer_too_complex"
	gccVersionExecution        = "gcc-analyzer.version"
	gccTaskTimeout             = 5 * time.Minute
	gccUnitTimeout             = 45 * time.Second
)

// The C descriptor-state checks complement the default Clang and Cppcheck
// selections. GCC still analyzes each translation unit, but diagnostics
// outside this selection are neither published nor counted as findings.
var GCCAnalyzerSelection = []string{
	"-Wanalyzer-fd-access-mode-mismatch",
	"-Wanalyzer-fd-double-close",
	"-Wanalyzer-fd-leak",
	"-Wanalyzer-fd-phase-mismatch",
	"-Wanalyzer-fd-type-mismatch",
	"-Wanalyzer-fd-use-after-close",
	"-Wanalyzer-fd-use-without-check",
}

var gccAnalyzerArgs = []string{
	"-fanalyzer", "-Wanalyzer-too-complex", "-fdiagnostics-format=json", "-fdiagnostics-column-unit=byte",
	"-fdiagnostics-color=never", "-fdiagnostics-show-option", "-S", "-o",
}

var gccVersionHeader = regexp.MustCompile(`^(?:[A-Za-z0-9_.]+-)*gcc(?:-[0-9]+)?[ \t].*[ \t]([0-9]+\.[0-9]+\.[0-9]+)[ \t]*$`)

func ParseGCCAnalyzerVersion(raw []byte) (string, bool) {
	first, _, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
	m := gccVersionHeader.FindStringSubmatch(first)
	if len(m) != 2 {
		return "", false
	}
	majorText, _, _ := strings.Cut(m[1], ".")
	major, _ := strconv.Atoi(majorText)
	return m[1], major >= 13
}

func GCCAnalyzerVersionTested(version string) bool {
	return strings.HasPrefix(version, "13.4.") || strings.HasPrefix(version, "15.2.")
}

func GCCAnalyzerDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: GCCAnalyzerID, Name: GCCAnalyzerName, Tool: GCCAnalyzerTool, Ecosystem: Ecosystem,
		VersionArgs: []string{"--version"}, ParseVersion: ParseGCCAnalyzerVersion}
}

func PlanGCCAnalyzer(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l, err := layoutOf(job)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, p := range l.Sources {
		if path.Ext(p) == ".c" {
			n++
		}
	}
	if n == 0 {
		return nil, nil
	}
	return []pack.Task{{Spec: analysis.TaskSpec{Key: GCCAnalyzerName, Title: "GCC C file descriptor analysis", Stage: domain.StageStaticAnalysis},
		Module: ".", Patterns: patterns(job.Scope), FilesInScope: &n}}, nil
}

type GCCAnalyzer struct{ t tool }

var _ pack.Analyzer = (*GCCAnalyzer)(nil)

func NewGCCAnalyzer(cfg Config) (*GCCAnalyzer, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = gccTaskTimeout
	}
	if cfg.StdoutLimit <= 0 {
		cfg.StdoutLimit = 64 << 10
	}
	if cfg.StderrLimit <= 0 {
		cfg.StderrLimit = 2 << 20
	}
	t, err := newTool(GCCAnalyzerTool, cfg)
	if err != nil {
		return nil, err
	}
	return &GCCAnalyzer{t: t}, nil
}

func (a *GCCAnalyzer) Descriptor() pack.Descriptor { return GCCAnalyzerDescriptor() }
func (a *GCCAnalyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return PlanGCCAnalyzer(ctx, job)
}
func (a *GCCAnalyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	return a.t.probe(ctx, runner, dir, []string{"--version"}, ParseGCCAnalyzerVersion)
}

func gccCUnit(u Unit) bool {
	if path.Ext(u.Rel) != ".c" || u.RenderedDriver() != "clang" {
		return false
	}
	return !slices.ContainsFunc(u.Args, func(a Arg) bool { return a.Flag == "-x" && a.Value != "c" })
}

func gccUnitArgs(u Unit, workspace string) []string {
	if !u.safe() || !gccCUnit(u) {
		return nil
	}
	compiler := u.Render(workspace)
	args := append([]string{}, compiler[1:len(compiler)-1]...)
	args = append(args, gccAnalyzerArgs...)
	return append(args, os.DevNull, compiler[len(compiler)-1])
}

func gccBase(job analysis.Job, task pack.Task) obsdoc.Document {
	doc := obsdoc.New(obsdoc.Analyzer{ID: GCCAnalyzerID, Ecosystem: Ecosystem}, obsdoc.Component{ID: GCCAnalyzerName, Version: "1"},
		obsdoc.Component{ID: "gcc-diagnostics-json", Version: "1"}, task.Spec.Key, job.Snapshot.Digest)
	doc.Module.Dir = "."
	doc.Patterns = task.Patterns
	doc.Tool.Name = GCCAnalyzerTool
	doc.RulePack = obsdoc.RulePack{ID: GCCAnalyzerRulePackID, Version: GCCAnalyzerRulePackVersion, Selection: GCCAnalyzerSelection,
		RepositoryConfig: obsdoc.RepoConfigNone, SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Target.Platform = ptr(runtime.GOOS + "/" + runtime.GOARCH)
	doc.Target.Properties = map[string]string{"language": "C", "mode": modeCompileDB, "source_snippets": "removed"}
	doc.Coverage.Unit, doc.Coverage.FilesInScope = "translation_unit", task.FilesInScope
	return doc
}

func (a *GCCAnalyzer) Run(ctx context.Context, job analysis.Job, spec analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	tasks, err := a.Plan(ctx, job)
	if ctx.Err() != nil {
		return analysis.TaskResult{Status: domain.TaskCancelled, Reason: ReasonCancelled}
	}
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "plan_failed")
	}
	if len(tasks) != 1 || spec.Key != GCCAnalyzerName {
		return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	s, bad := newSession(job, spec.Key, a.t, "$GCC")
	if bad != nil {
		return *bad
	}
	doc := gccBase(job, tasks[0])
	l, _ := layoutOf(job)
	src, broken := compileDBs(job, l)
	if broken != "" {
		return notRun(s, doc, domain.TaskFailed, broken, obsdoc.ExitNotRun)
	}
	if len(src.DBs) == 0 {
		doc.Limitations = append(doc.Limitations, src.Limitations...)
		return notRun(s, doc, domain.TaskUnsupported, ReasonCompileDBMissing, obsdoc.ExitNotRun)
	}
	l.DBs = src.DBs
	m, err := MapDBs(src.DBs, src.Data, l.Files, func(rel string) bool { return path.Ext(rel) == ".c" && pack.PathInScope(rel, job.Scope) })
	if err != nil {
		return notRun(s, doc, domain.TaskFailed, ReasonCompileDBUnreadable, obsdoc.ExitNotRun)
	}
	l.Sources = slices.DeleteFunc(l.Sources, func(p string) bool { return path.Ext(p) != ".c" })
	languageExcluded := 0
	for _, u := range m.Units {
		if !gccCUnit(u) {
			m.ExcludedSources[u.Rel] = reasonGCCLanguage
			languageExcluded++
		}
	}
	m.Units = slices.DeleteFunc(m.Units, func(u Unit) bool { return !gccCUnit(u) })
	applyCompileDB(&doc, &m, l, src.ConfigFiles)
	src.apply(&doc)
	if languageExcluded > 0 {
		doc.Limitations = append(doc.Limitations, reasonGCCLanguage)
		doc.Target.Properties["c_entries_in_other_languages"] = strconv.Itoa(languageExcluded)
	}
	if len(m.Units) == 0 {
		return notRun(s, doc, domain.TaskUnsupported, ReasonNoTranslationUnits, obsdoc.ExitNotRun)
	}
	version, e, err := s.run(ctx, runner, gccVersionExecution, s.spec([]string{"--version"}, probeTimeout), nil)
	if err != nil {
		return gccWriteFailure(nil)
	}
	allowedPosition := func(p string) bool {
		if !filepath.IsAbs(p) && !strings.HasPrefix(p, "[REDACTED]/") {
			return false
		}
		rel, ok := govet.StripWorkspace(p, ".", s.roots, true)
		return ok && l.Files[rel].SHA256 != ""
	}
	execs := []analysis.Execution{e}
	checks := make([]gccUnitResult, 0, len(m.Units))
	if toolVersion, known := ParseGCCAnalyzerVersion(version.Stdout.Data); known && exitedWith(version, 0) && pack.StreamsComplete(version) && !pack.Stoppable(version) {
		doc.Tool.Version, doc.Tool.VersionTested = ptr(toolVersion), GCCAnalyzerVersionTested(toolVersion)
		if !doc.Tool.VersionTested {
			doc.Limitations = append(doc.Limitations, "tool_version_untested")
		}
		deadline := time.Now().Add(a.t.cfg.Timeout)
		for i, u := range m.Units {
			remaining := time.Until(deadline)
			if ctx.Err() != nil || remaining <= 0 {
				break
			}
			args := gccUnitArgs(u, s.workspace)
			if args == nil {
				return gccWriteFailure(execs)
			}
			exec := "gcc-analyzer.check." + strconv.Itoa(i+1)
			cp := gccUnitResult{unit: u.Rel, exec: exec}
			cp.result, e, err = s.run(ctx, runner, exec, s.spec(args, min(remaining, gccUnitTimeout)), func(r *executor.Result) {
				cp.parsed = sanitizeGCCAnalyzer(r, allowedPosition, s.mask)
			})
			if err != nil {
				return gccWriteFailure(execs)
			}
			checks, execs = append(checks, cp), append(execs, e)
			if pack.Stoppable(cp.result) {
				break
			}
		}
	}
	return s.write(assembleGCCAnalyzer(doc, s, m, l, version, checks, execs, ctx.Err() != nil), execs)
}

func gccWriteFailure(execs []analysis.Execution) analysis.TaskResult {
	return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: execs, Limitations: []string{"execution_write_failed"}}
}

type gccUnitResult struct {
	unit, exec string
	result     *executor.Result
	parsed     gccDiagnostics
}

func assembleGCCAnalyzer(doc obsdoc.Document, s *session, m Mapping, l layout, version *executor.Result, checks []gccUnitResult,
	execs []analysis.Execution, cancelled bool) obsdoc.Document {
	results := []*executor.Result{version}
	for _, e := range execs {
		doc.Executions = append(doc.Executions, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	for _, b := range checks {
		results = append(results, b.result)
	}
	var o outcome
	o.executionFailures(results, cancelled)
	if doc.Tool.Version == nil {
		o.fail(domain.TaskUnsupported, ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	rs := resolver{files: l.Files, drift: drift(s.workspace, l.Files), snapshotDir: s.job.Snapshot.SnapshotDir, roots: s.roots, cache: map[string]lineInfo{}}
	for p := range rs.drift {
		doc.WorkspaceDrift = append(doc.WorkspaceDrift, p)
	}
	sort.Strings(doc.WorkspaceDrift)
	if len(doc.WorkspaceDrift) > 0 {
		doc.Limitations = append(doc.Limitations, ReasonWorkspaceDrift)
		o.fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	processed := []string{}
	outside, tooComplex := 0, 0
	for _, b := range checks {
		before := len(doc.Observations)
		parse := b.parsed
		outside += parse.outOfScope
		if parse.trimmed {
			doc.Limitations = append(doc.Limitations, "related_locations_truncated")
		}
		if !pack.StreamsComplete(b.result) {
			o.fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
			doc.Coverage.Units[TUUnknown]++
			continue
		}
		if parse.invalid {
			doc.Parse.ShapeErrors++
			o.fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
			doc.Coverage.Units[TUUnknown]++
			continue
		}
		if parse.unavailable {
			o.fail(domain.TaskUnsupported, reasonGCCUnavailable, obsdoc.ExitConfigurationError)
			doc.Coverage.Units[TUUnknown]++
			continue
		}
		doc.Parse.Records += len(parse.records)
		appendGCCRecords(&doc, &rs, b)
		switch {
		case exitedWith(b.result, 0) && parse.compileErrors != 0 || exitedWith(b.result, 1) && parse.compileErrors == 0:
			o.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
			doc.Coverage.Units[TUUnknown]++
		case parse.missing && exitedWith(b.result, 1):
			o.fail(domain.TaskFailed, ReasonMissingInclude, obsdoc.ExitExecutionError)
			doc.Coverage.Units[TUFailedMissingInclude]++
		case !exitedWith(b.result, 0):
			o.fail(domain.TaskFailed, reasonGCCCompile, obsdoc.ExitExecutionError)
			doc.Coverage.Units[TUFailedCompile]++
		case parse.tooComplex:
			// GCC stopped exploring some paths of this unit at its limit; the
			// diagnostics it found stay, the unit is not a processed file
			// and does not fail the units that finished (decision A67).
			tooComplex++
			doc.Coverage.Units[TUPartialTooComplex]++
		default:
			processed = append(processed, b.unit)
			if len(doc.Observations) == before {
				doc.Coverage.Units[TUAnalyzedClean]++
			} else {
				doc.Coverage.Units[TUAnalyzedWithFindings]++
			}
		}
	}
	if outside > 0 {
		doc.Limitations = append(doc.Limitations, LimOutsideDropped)
		doc.Target.Properties["outside_records_dropped"] = strconv.Itoa(outside)
	}
	if missing := len(m.Units) - len(checks); missing > 0 {
		doc.Coverage.Units[TUNotProcessed] += missing
		o.fail(domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout)
	}
	if tooComplex > 0 {
		doc.Limitations = append(doc.Limitations, reasonGCCTooComplex)
		doc.Target.Properties["units_too_complex"] = strconv.Itoa(tooComplex)
		// Success needs a unit analysed to the end: with none, the task
		// would read as clean with nothing analysed.
		if len(processed) == 0 {
			o.fail(domain.TaskFailed, reasonGCCTooComplex, obsdoc.ExitExecutionError)
		}
	}
	sort.Strings(processed)
	o.finish(&doc, processed, len(doc.Observations) > 0, true)
	return doc
}

func appendGCCRecords(doc *obsdoc.Document, rs *resolver, b gccUnitResult) {
	duplicates := map[string]int{}
	for _, rec := range b.parsed.records {
		pos := rec.location()
		if pos == nil {
			continue
		}
		p, loc, state := rs.resolve(pos.File, pos.Line, pos.Col)
		if rec.Kind != "warning" {
			if len(doc.ToolErrors) < 200 {
				doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: b.exec, Unit: b.unit, Kind: "compile_error",
					Message: rec.Message, Path: p, Line: pos.Line, Column: pos.Col})
			}
			continue
		}
		rule := strings.TrimPrefix(rec.Option, "-W")
		position := strconv.Itoa(pos.Line) + ":" + strconv.Itoa(pos.Col)
		if p != nil {
			position = *p + ":" + position
		}
		tuple := rule + "\x00" + position + "\x00" + rec.Message
		n := duplicates[tuple]
		duplicates[tuple]++
		o := obsdoc.Observation{ID: obsdoc.ObservationID(GCCAnalyzerID, ".", b.unit, rule, position, position, rec.Message, n),
			RuleID: rule, Category: "resource_lifecycle", RawSeverity: ptr(rec.Kind), RawMessage: rec.Message,
			Path: p, Location: loc, LocationState: state, Related: []obsdoc.Related{}, Unit: b.unit,
			RawRef: obsdoc.RawRef{Execution: b.exec, Stream: "stderr", StreamSHA256: obsdoc.SHA256Hex(b.result.Stderr.Data),
				RecordIndex: rec.index, JSONPointer: "/" + strconv.Itoa(rec.index), ByteStart: rec.span.Start, ByteEnd: rec.span.End}}
		for i, trace := range rec.Path[:len(rec.events)] {
			p, loc, state := rs.resolve(trace.Location.File, trace.Location.Line, trace.Location.Col)
			o.Related = append(o.Related, obsdoc.Related{Kind: gccRelatedKind(trace.Description), RawSpan: &rec.events[i],
				RawMessage: trace.Description, Path: p, Location: loc, LocationState: state})
		}
		doc.Observations = append(doc.Observations, o)
	}
}
