package python

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func ptr[T any](v T) *T { return &v }

// builder assembles one envelope and decides the task outcome. The first
// failure recorded wins; success is what remains when none was.
type builder struct {
	rc       *runCtx
	doc      obsdoc.Document
	lims     []string
	status   domain.TaskStatus
	reason   string
	class    string
	ordinals map[string]int
	// output: the tool printed records the parser read (partial parse on
	// failure).
	output bool
	parsed bool
	// outside counts dropped diagnostics and errors of files outside the
	// attempt workspace.
	outside int
}

func newBuilder(rc *runCtx, adapter, parser obsdoc.Component, rp obsdoc.RulePack) *builder {
	d := rc.b.desc
	doc := obsdoc.New(obsdoc.Analyzer{ID: d.ID, Ecosystem: d.Ecosystem, Required: d.Required}, adapter, parser, rc.p.Task.Spec.Key, rc.job.Snapshot.Digest)
	doc.Module.Dir = rc.p.Module.Dir
	doc.Patterns = append([]string{}, rc.p.Task.Patterns...)
	doc.Tool.Name = d.Tool
	doc.RulePack = rp
	doc.Coverage.Unit = "file"
	doc.Coverage.FilesInScope = ptr(len(toolFiles(d, rc.p.Module.Files)))
	doc.Target.Properties["python_version_source"] = rc.target.Source
	if rc.target.Declared != "" {
		doc.Target.Properties["python_version_declared"] = rc.target.Declared
	}
	for _, e := range rc.executions {
		doc.Executions = append(doc.Executions, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	bd := &builder{rc: rc, doc: doc, status: domain.TaskSucceeded, ordinals: map[string]int{}, parsed: true}
	bd.lims = append(bd.lims, rc.target.Limitations...)
	if rc.b.cfg.BothTypeCheckers {
		bd.lims = append(bd.lims, LimBothTypeCheckers)
	}
	return bd
}

func (bd *builder) fail(s domain.TaskStatus, r, c string) {
	if bd.reason == "" {
		bd.status, bd.reason, bd.class = s, r, c
	}
}

func (bd *builder) limit(codes ...string) { bd.lims = append(bd.lims, codes...) }

// targetVersion records the Python version passed to the tool after its
// supported range is applied.
func (bd *builder) targetVersion(lo, hi int) int {
	n, lim := clampMinor(bd.rc.target.Minor, lo, hi)
	if lim != "" {
		bd.limit(lim)
	}
	bd.doc.Target.Properties["python_version"] = "3." + strconv.Itoa(n)
	return n
}

// identify reads the version execution.
func (bd *builder) identify(tested func(string) bool) {
	r := bd.rc.results[bd.rc.b.desc.Name+".version"]
	if r == nil || !pack.ExitedZero(r) {
		return
	}
	if v, ok := bd.rc.b.desc.ParseVersion(r.Stdout.Data); ok {
		bd.doc.Tool.Version, bd.doc.Tool.VersionTested = ptr(v), tested(v)
		if !bd.doc.Tool.VersionTested {
			bd.limit(LimToolVersionUntested)
		}
	}
}

// executions applies the execution-level outcomes in precedence order
// (cleanup, cancellation, timeout, start, identification, completeness,
// drift, truncation) over the task's planned executions.
func (bd *builder) executions(names ...string) {
	rc := bd.rc
	results := make([]*executor.Result, len(names))
	for i, n := range names {
		results[i] = rc.results[n]
	}
	for _, r := range results {
		if r != nil && r.Cleanup == executor.CleanupUncertain {
			bd.fail(domain.TaskFailed, ReasonCleanupUncertain, obsdoc.ExitExecutionError)
		}
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusCancelled {
			bd.fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
		}
	}
	if rc.cancelled {
		bd.fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusTimedOut {
			bd.fail(domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout)
		}
	}
	for _, r := range results {
		if r == nil {
			continue
		}
		switch r.Status {
		case executor.StatusUnsupported:
			bd.fail(domain.TaskUnsupported, ReasonExecutionUnsupported, obsdoc.ExitNotRun)
		case executor.StatusStartFailed:
			bd.fail(domain.TaskFailed, ReasonToolStartFailed, obsdoc.ExitNotRun)
		case executor.StatusRejected:
			bd.fail(domain.TaskFailed, ReasonExecutionRejected, obsdoc.ExitNotRun)
		}
	}
	if results[0] != nil && bd.doc.Tool.Version == nil {
		bd.fail(domain.TaskFailed, ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	if rc.refused != "" {
		bd.fail(domain.TaskFailed, rc.refused, obsdoc.ExitNotRun)
	}
	for _, r := range results {
		if r == nil {
			bd.fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
		}
	}
	for p := range rc.drift {
		bd.doc.WorkspaceDrift = append(bd.doc.WorkspaceDrift, p)
	}
	sort.Strings(bd.doc.WorkspaceDrift)
	if len(bd.doc.WorkspaceDrift) > 0 {
		bd.limit(ReasonWorkspaceDrift)
		bd.fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	for _, r := range results {
		if r != nil && !pack.StreamsComplete(r) {
			bd.fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
		}
	}
}

// unparsed records a parse failure of tool output.
func (bd *builder) unparsed() {
	bd.parsed = false
	bd.fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
}

// toolPath maps a tool-reported file to a snapshot-relative path.
func (bd *builder) toolPath(file string) (string, bool) {
	if file == "" {
		return "", false
	}
	return govet.StripWorkspace(file, bd.rc.p.Module.Dir, bd.rc.roots, true)
}

// observe adds one observation of a tool diagnostic.
func (bd *builder) observe(rule, category, severity, message, file string, pl place, ref obsdoc.RawRef, suppressed bool) {
	rc := bd.rc
	rel, ok := bd.toolPath(file)
	if file != "" && !ok {
		// A file outside the attempt workspace is not the project's code:
		// neither the diagnostic nor its text is kept.
		bd.outside++
		return
	}
	pathPtr, loc, state := locate(rel, ok, pl, rc.files, rc.drift, rc.texts)
	msg := rc.mask(message)
	if msg == "" {
		msg = "(empty message)"
	}
	unit := ""
	if pathPtr != nil {
		unit = *pathPtr
	} else {
		bd.limit(LimUnattributed)
	}
	start := rc.mask(file + ":" + strconv.Itoa(pl.StartLine) + ":" + strconv.Itoa(pl.StartCol))
	end := rc.mask(file + ":" + strconv.Itoa(pl.EndLine) + ":" + strconv.Itoa(pl.EndCol))
	tuple := strings.Join([]string{unit, rule, start, end, msg}, "\x00")
	ordinal := bd.ordinals[tuple]
	bd.ordinals[tuple]++
	o := obsdoc.Observation{ID: obsdoc.ObservationID(rc.b.desc.ID, rc.p.Module.Dir, unit, rule, start, end, msg, ordinal), RuleID: rule, Category: category,
		RawMessage: msg, Path: pathPtr, Location: loc, LocationState: state, Related: []obsdoc.Related{}, RawRef: ref, Unit: unit, Suppressed: suppressed}
	if severity != "" {
		o.RawSeverity = ptr(severity)
	}
	bd.doc.Observations = append(bd.doc.Observations, o)
	if suppressed {
		bd.limit(LimSuppressed)
	}
}

// toolError records a unit the tool could not analyze (a syntax error, an
// unreadable file). It is never an observation of the code.
func (bd *builder) toolError(execution, kind, file string, line, col int, message string) {
	te := obsdoc.ToolError{Execution: execution, Kind: kind, Message: bd.rc.mask(message), Line: max(line, 0), Column: max(col, 0)}
	rel, ok := bd.toolPath(file)
	if file != "" && !ok {
		bd.outside++
		return
	}
	if ok && rel != "." {
		te.Path, te.Unit = ptr(rel), rel
	}
	if len(bd.doc.ToolErrors) < 200 {
		bd.doc.ToolErrors = append(bd.doc.ToolErrors, te)
	}
}

// finish decides the outcome. processed are the snapshot files the tool
// really processed (nil = unknown); they count only for a success.
func (bd *builder) finish(processed []string, syntaxFiles map[string]bool) obsdoc.Document {
	doc := &bd.doc
	planned := map[string]bool{}
	for _, f := range toolFiles(bd.rc.b.desc, bd.rc.p.Module.Files) {
		planned[f] = true
	}
	var inManifest []string
	if processed != nil {
		inManifest = []string{}
		for _, p := range processed {
			if _, ok := bd.rc.files[p]; ok && !syntaxFiles[p] {
				inManifest = append(inManifest, p)
			}
		}
		sort.Strings(inManifest)
		inManifest = slices.Compact(inManifest)
		seen := map[string]bool{}
		for _, p := range inManifest {
			seen[p] = true
		}
		for f := range planned {
			if !seen[f] && !syntaxFiles[f] {
				doc.Coverage.FilesUnlisted++
			}
		}
		if doc.Coverage.FilesUnlisted > 0 {
			bd.limit(LimFilesNotProcessed)
			doc.Coverage.Units[UnitNotAnalyzed] += doc.Coverage.FilesUnlisted
		}
		withFindings := map[string]bool{}
		for _, o := range doc.Observations {
			if !o.Suppressed && o.Path != nil {
				withFindings[*o.Path] = true
			}
		}
		for _, p := range inManifest {
			if withFindings[p] {
				doc.Coverage.Units[UnitAnalyzedWithFindings]++
			} else {
				doc.Coverage.Units[UnitAnalyzedClean]++
			}
		}
	}
	if len(syntaxFiles) > 0 {
		doc.Coverage.Units[UnitSyntaxError] += len(syntaxFiles)
		bd.fail(domain.TaskFailed, ReasonSyntaxErrors, obsdoc.ExitExecutionError)
	}
	if bd.reason == "" && processed == nil {
		bd.fail(domain.TaskFailed, ReasonProcessedMismatch, obsdoc.ExitExecutionError)
	}
	if bd.reason == "" && len(inManifest) == 0 {
		bd.fail(domain.TaskFailed, ReasonNoFilesProcessed, obsdoc.ExitExecutionError)
	}
	partial := false
	if bd.reason == "" {
		bd.class = obsdoc.ExitSuccessWithoutFindings
		if doc.Active() > 0 {
			bd.class = obsdoc.ExitSuccessWithFindings
		}
		doc.Coverage.ProcessedPaths = inManifest
	} else {
		partial = bd.output || !bd.parsed
	}
	switch {
	case bd.class == obsdoc.ExitNotRun:
		doc.Observations, partial = []obsdoc.Observation{}, false
		doc.ToolErrors = []obsdoc.ToolError{}
		doc.Coverage.ProcessedPaths = []string{}
	case bd.reason != "":
		// A failed run never claims files as processed.
		doc.Coverage.ProcessedPaths = nil
	}
	if bd.outside > 0 {
		bd.limit(LimOutsideDropped)
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: bd.status, ExitClass: bd.class, PartialParse: partial}
	if bd.reason != "" {
		doc.Outcome.Reason = ptr(bd.reason)
	}
	doc.Parse.OK = bd.reason == "" && bd.parsed
	doc.Limitations = bd.lims
	return *doc
}
