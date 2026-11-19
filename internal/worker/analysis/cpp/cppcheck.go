package cpp

import (
	"context"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// cppcheck identity: the baseline (required) C/C++ analyzer. It parses
// translation units without build flags (tolerating unknown headers and
// macros, unlike a compiler), so it is the one C/C++ tool a node can run on
// every snapshot (docs/ANALYZERS.md#기준-분석기와-추가-깊이, docs/ANALYZERS.md#cc-팩).
const (
	CppcheckID             = "cpp.cppcheck"
	CppcheckName           = "cppcheck"
	CppcheckTool           = "cppcheck"
	CppcheckAdapterVersion = "1"
	CppcheckRulePackID     = "codeye-cpp-cppcheck"
	// CppcheckRulePackVersion names the selection below.
	CppcheckRulePackVersion = "1"
	ReasonCppcheckFailed    = "cppcheck_failed"
	execCppcheckVersion     = "cppcheck.version"
	execCppcheckCheck       = "cppcheck.check"
)

// CppcheckSelection is the CODEYE rule selection: the error checks (always
// on) plus warning, performance and portability; style is not run (docs/ANALYZERS.md
// §7). information and missingInclude are enabled for coverage only: their
// records (missing headers, configuration limits, the active-checker count)
// never become observations. The check level and platform are pinned so a
// default change of the tool cannot change the rules silently. Inline
// suppressions are not enabled (no --inline-suppr): every diagnostic is
// reported, so source_suppressions is "disabled" (measured: with
// --inline-suppr suppressed diagnostics vanish without any record, docs/ANALYZERS.md
// §6).
var CppcheckSelection = []string{"--enable=warning,performance,portability,information,missingInclude", "--check-level=normal", "--platform=native"}

// CppcheckArgs is the check argv after the executable. input is
// "--project=<CODEYE compile DB>" or "--file-list=<list>" (task scratch).
func CppcheckArgs(input string) []string {
	return append(append([]string{"--output-format=xmlv2"}, CppcheckSelection...), "--error-exitcode=2", input)
}

// CppcheckDescriptor is cppcheck's pack identity.
func CppcheckDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: CppcheckID, Name: CppcheckName, Ecosystem: Ecosystem, Required: true, Tool: CppcheckTool,
		VersionArgs: []string{"--version"}, ParseVersion: ParseCppcheckVersion}
}

// PlanCppcheck plans one task when C/C++ files are in scope: with the
// snapshot's compile DBs when it has any, otherwise the reduced mode
// (sources without build flags).
func PlanCppcheck(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return planOne(job, CppcheckName, true, func(l layout) string {
		if len(l.DBs) > 0 {
			return "cppcheck C/C++ analysis with " + strconv.Itoa(len(l.DBs)) + " compile DB(s)"
		}
		return "cppcheck C/C++ reduced analysis (no compile DB: no build flags)"
	})
}

// Cppcheck is the configured cppcheck adapter.
type Cppcheck struct{ t tool }

var _ pack.Analyzer = (*Cppcheck)(nil)

// NewCppcheck validates the configuration without running anything.
func NewCppcheck(cfg Config) (*Cppcheck, error) {
	t, err := newTool("cppcheck", cfg)
	if err != nil {
		return nil, err
	}
	return &Cppcheck{t: t}, nil
}

// Descriptor implements pack.Analyzer.
func (a *Cppcheck) Descriptor() pack.Descriptor { return CppcheckDescriptor() }

// Plan implements pack.Analyzer.
func (a *Cppcheck) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return PlanCppcheck(ctx, job)
}

// Probe runs `cppcheck --version` in a fresh directory under dir.
func (a *Cppcheck) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	return a.t.probe(ctx, runner, dir, []string{"--version"}, ParseCppcheckVersion)
}

// cppcheckBase is the envelope skeleton shared by every outcome.
func cppcheckBase(job analysis.Job, key string, l layout) obsdoc.Document {
	doc := obsdoc.New(obsdoc.Analyzer{ID: CppcheckID, Ecosystem: Ecosystem, Required: true}, obsdoc.Component{ID: CppcheckName, Version: CppcheckAdapterVersion},
		obsdoc.Component{ID: CppcheckParserID, Version: CppcheckParserVersion}, key, job.Snapshot.Digest)
	doc.Module.Dir = "."
	doc.Patterns = patterns(job.Scope)
	doc.Tool.Name = CppcheckTool
	doc.RulePack = obsdoc.RulePack{ID: CppcheckRulePackID, Version: CppcheckRulePackVersion, Selection: append([]string{}, CppcheckSelection...),
		RepositoryConfig: obsdoc.RepoConfigNone, SourceSuppressions: obsdoc.SuppressDisabled, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Target.Platform = ptr(runtime.GOOS + "/" + runtime.GOARCH)
	doc.Target.Properties = map[string]string{"platform": "native", "check_level": "normal", "inline_suppressions": "disabled"}
	doc.Coverage.Unit = "translation_unit"
	doc.Coverage.FilesInScope = ptr(len(l.Sources))
	return doc
}

// Run executes the cppcheck task.
func (a *Cppcheck) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	tasks, err := PlanCppcheck(ctx, job)
	if err != nil {
		return failed(analysis.ReasonHandlerError, "plan_failed")
	}
	if !slices.ContainsFunc(tasks, func(t pack.Task) bool { return t.Spec.Key == task.Key }) {
		return failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	l, _ := layoutOf(job)
	s, bad := newSession(job, task.Key, a.t, "$CPPCHECK")
	if bad != nil {
		return *bad
	}
	in := ccInput{Key: task.Key, Layout: l, Mode: modeReduced, Units: l.Sources}
	doc := cppcheckBase(job, task.Key, l)
	if len(l.DBs) > 0 {
		data, cfgFiles, ok := readDBs(job, l)
		if !ok {
			return notRun(s, doc, domain.TaskFailed, ReasonSnapshotIntegrity, obsdoc.ExitNotRun)
		}
		m, err := MapDBs(l.DBs, data, l.Files, func(rel string) bool { return inScope(rel, job.Scope) })
		if err != nil {
			return notRun(s, doc, domain.TaskFailed, ReasonCompileDBUnreadable, obsdoc.ExitNotRun)
		}
		in.Mode, in.Mapping, in.ConfigFiles = modeCompileDB, &m, cfgFiles
		in.Units = nil
		for _, u := range m.Units {
			in.Units = append(in.Units, u.Rel)
		}
	}
	applyCppcheckMode(&doc, in)
	if len(in.Units) == 0 {
		return notRun(s, doc, domain.TaskUnsupported, ReasonNoTranslationUnits, obsdoc.ExitNotRun)
	}
	var input string
	if in.Mode == modeCompileDB {
		b, err := RenderDB(s.workspace, in.Mapping.Units)
		input = filepath.Join(s.workDir, CompileDBName)
		if err != nil || analysis.WriteFileExclusive(input, b) != nil {
			return failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
		input = "--project=" + input
	} else {
		var list strings.Builder
		for _, u := range in.Units {
			list.WriteString(filepath.Join(s.workspace, filepath.FromSlash(u)) + "\n")
		}
		input = filepath.Join(s.workDir, "files.txt")
		if analysis.WriteFileExclusive(input, []byte(list.String())) != nil {
			return failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
		input = "--file-list=" + input
	}
	steps := []struct {
		name string
		args []string
		dst  **executor.Result
	}{
		{execCppcheckVersion, []string{"--version"}, &in.Version},
		{execCppcheckCheck, CppcheckArgs(input), &in.Check},
	}
	for _, st := range steps {
		if ctx.Err() != nil {
			in.Cancelled = true
			break
		}
		var clean func(*executor.Result)
		if st.name == execCppcheckCheck {
			clean = func(r *executor.Result) { in.Dropped = sanitizeCppcheck(r, s.inside) }
		}
		res, e, err := s.run(ctx, runner, st.name, s.spec(st.args, a.t.cfg.Timeout), clean)
		*st.dst = res
		if err != nil {
			return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: in.Executions, Limitations: []string{"execution_write_failed"}}
		}
		in.Executions = append(in.Executions, e)
		if stoppable(res) {
			break
		}
		if st.name == execCppcheckVersion {
			// An unidentified cppcheck is never run on the code.
			if _, ok := ParseCppcheckVersion(res.Stdout.Data); !ok || !exitedWith(res, 0) {
				break
			}
		}
	}
	in.Drift = drift(s.workspace, l.Files)
	in.Roots, in.Mask, in.SnapshotDir = s.roots, s.mask, job.Snapshot.SnapshotDir
	return s.write(assembleCppcheck(doc, in), in.Executions)
}

const (
	modeReduced   = "reduced"
	modeCompileDB = "compile_db"
)

// ccInput is everything the pure cppcheck document builder needs.
type ccInput struct {
	Key         string
	Layout      layout
	Mode        string
	Mapping     *Mapping
	ConfigFiles []obsdoc.ConfigFile
	Units       []string
	Version     *executor.Result
	Check       *executor.Result
	// Dropped are the check records removed from the stored stream because
	// a location is outside the workspace (sanitize.go): id, severity and
	// file0 only.
	Dropped     []ccRecord
	Executions  []analysis.Execution
	Drift       map[string]bool
	Roots       []string
	Mask        func(string) string
	SnapshotDir string
	Cancelled   bool
}

// applyCppcheckMode records the mode in the rule pack, target and
// limitations: a compile DB is repository build configuration the tool
// reads (honored, digested); the reduced mode reads none.
func applyCppcheckMode(doc *obsdoc.Document, in ccInput) {
	doc.Target.Properties["mode"] = in.Mode
	if in.Mode == modeReduced {
		doc.Limitations = append(doc.Limitations, ReasonCompileDBMissing, "reduced_mode_no_build_flags")
		return
	}
	m := in.Mapping
	doc.RulePack.RepositoryConfig = obsdoc.RepoConfigHonored
	doc.RulePack.ConfigFiles = append([]obsdoc.ConfigFile{}, in.ConfigFiles...)
	compileDBProperties(doc, m, len(in.Layout.DBs))
	doc.Limitations = append(doc.Limitations, m.Limitations()...)
	if len(in.Layout.DBs) > 1 {
		doc.Limitations = append(doc.Limitations, LimMultipleDBs)
	}
	listed := map[string]bool{}
	for _, u := range m.Units {
		listed[u.Rel] = true
	}
	for _, src := range in.Layout.Sources {
		if _, excluded := m.ExcludedSources[src]; !listed[src] && !excluded {
			doc.Coverage.FilesUnlisted++
		}
	}
	doc.Coverage.FilesSkipped = len(m.ExcludedSources)
	if doc.Coverage.FilesUnlisted > 0 {
		doc.Limitations = append(doc.Limitations, LimSourcesNotInDB)
		doc.Coverage.Units[TUNotInCompileDB] = doc.Coverage.FilesUnlisted
	}
	if doc.Coverage.FilesSkipped > 0 {
		doc.Coverage.Units[TUCompileDBEntryExcluded] = doc.Coverage.FilesSkipped
	}
}

func compileDBProperties(doc *obsdoc.Document, m *Mapping, dbs int) {
	p := doc.Target.Properties
	p["compile_dbs"] = strconv.Itoa(dbs)
	p["compile_db_entries"] = strconv.Itoa(m.Total)
	p["compile_db_units"] = strconv.Itoa(len(m.Units))
	if v := countText(m.Excluded); v != "" {
		p["entries_excluded"] = v
	}
	if v := m.DroppedText(); v != "" {
		p["flags_dropped"] = v
	}
	if v := strings.Join(m.Drivers, ","); v != "" && len(v) <= 256 {
		p["drivers"] = v
	}
	if v := Std(m.Units); v != "" {
		p["std"] = v
	}
}

// cppcheck record classes. Coverage records describe what the tool could not
// analyze; they are tool errors or limitations, never observations of code.
const (
	ccObservation    = "observation"
	ccMissingInclude = "missing_include"
	ccSystemInclude  = "system_include"
	ccParse          = "parse_error"
	ccLimit          = "analysis_limit"
	ccReport         = "report"
	ccInformation    = "information"
)

// ccCoverage maps cppcheck 2.22 ids (cppcheck --errorlist and the measured
// runtime ids) that report analysis problems, not code.
var ccCoverage = map[string]string{
	"missingInclude": ccMissingInclude, "missingIncludeSystem": ccSystemInclude,
	"syntaxError": ccParse, "preprocessorErrorDirective": ccParse, "unknownMacro": ccParse, "internalAstError": ccParse, "internalError": ccParse,
	"cppcheckError": ccParse, "noValidConfiguration": ccParse, "instantiationError": ccParse, "unhandledChar": ccParse,
	"toomanyconfigs": ccLimit, "purgedConfiguration": ccLimit, "normalCheckLevelMaxBranches": ccLimit, "templateRecursion": ccLimit,
	"checkersReport": ccReport, "unmatchedSuppression": ccReport,
}

func ccKind(r ccRecord) string {
	if k, ok := ccCoverage[r.ID]; ok {
		return k
	}
	switch {
	case strings.HasPrefix(r.ID, "internal"):
		return ccParse
	case r.Severity == "information" || r.Severity == "debug" || r.Severity == "none":
		return ccInformation
	}
	return ccObservation
}

// cppcheckCategory is the policy of codeye-cpp-cppcheck v1: what the check is
// about, not its impact.
func cppcheckCategory(id, severity string) string {
	switch {
	case severity == "performance":
		return "performance"
	case strings.HasPrefix(id, "memleak") || strings.HasPrefix(id, "resourceLeak") || strings.HasPrefix(id, "leak") || id == "deallocuse" ||
		id == "doubleFree" || id == "mismatchAllocDealloc" || strings.HasPrefix(id, "deallocDealloc") || id == "deallocret" || id == "uninitdata":
		return "resource_lifecycle"
	case strings.HasPrefix(id, "except") || strings.HasPrefix(id, "throw") || strings.HasPrefix(id, "rethrow") || strings.HasPrefix(id, "catchException"):
		return "error_handling"
	case severity == "style":
		return "maintainability"
	}
	return "correctness"
}

var activeCheckers = regexp.MustCompile(`^Active checkers: ([0-9]+/[0-9]+)`)

// assembleCppcheck builds the envelope and decides the outcome. Success is
// one explicit conjunction; everything else keeps what was parsed.
func assembleCppcheck(doc obsdoc.Document, in ccInput) obsdoc.Document {
	mask := in.Mask
	if mask == nil {
		mask = func(v string) string { return v }
	}
	for _, e := range in.Executions {
		doc.Executions = append(doc.Executions, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	for p := range in.Drift {
		doc.WorkspaceDrift = append(doc.WorkspaceDrift, p)
	}
	sort.Strings(doc.WorkspaceDrift)
	if len(doc.WorkspaceDrift) > 0 {
		doc.Limitations = append(doc.Limitations, ReasonWorkspaceDrift)
	}
	toolVersion := ""
	if in.Version != nil && exitedWith(in.Version, 0) {
		if v, ok := ParseCppcheckVersion(in.Version.Stdout.Data); ok {
			toolVersion = v
			doc.Tool.Version, doc.Tool.VersionTested = ptr(v), CppcheckVersionTested(v)
			if !doc.Tool.VersionTested {
				doc.Limitations = append(doc.Limitations, "tool_version_untested")
			}
		}
	}
	rs := &resolver{files: in.Layout.Files, drift: in.Drift, snapshotDir: in.SnapshotDir, roots: in.Roots, cache: map[string]lineInfo{}}
	units := map[string]bool{}
	for _, u := range in.Units {
		units[u] = true
	}
	tuOf := func(file string) string {
		if rel, ok := rs.rel(file); ok && units[rel] {
			return rel
		}
		return ""
	}
	var st ccStream
	var out ccStdout
	stderrSHA := ""
	if in.Check != nil {
		st = parseCppcheckXML(in.Check.Stderr.Data)
		out = parseCppcheckStdout(in.Check.Stdout.Data)
		stderrSHA = obsdoc.SHA256Hex(in.Check.Stderr.Data)
		if st.Version != "" && toolVersion != "" && st.Version != toolVersion {
			st.ShapeErrors++
		}
		if out.Other > 0 {
			doc.Limitations = append(doc.Limitations, "tool_stdout_output")
		}
		if out.Configs > 0 {
			doc.Target.Properties["preprocessor_configs_checked"] = strconv.Itoa(out.Configs)
		}
	}
	parsed := in.Check != nil && st.Complete && st.UnparsedBytes == 0 && st.ShapeErrors == 0 && st.DecodeError == ""
	doc.Parse.Records, doc.Parse.UnparsedBytes, doc.Parse.ShapeErrors, doc.Parse.DecodeError = st.Values, st.UnparsedBytes, st.ShapeErrors, st.DecodeError
	failedParse, partialInclude, withFindings := map[string]bool{}, map[string]bool{}, map[string]bool{}
	unattributedParse, headerMissing := false, false
	ordinals := map[string]int{}
	addToolError := func(kind, unit string, r ccRecord) {
		te := obsdoc.ToolError{Execution: execCppcheckCheck, Kind: kind, Unit: unit, Message: mask(r.ID + ": " + r.Msg)}
		if len(r.Locations) > 0 {
			if rel, ok := rs.rel(r.Locations[0].File); ok {
				te.Path, te.Line, te.Column = ptr(rel), r.Locations[0].Line, r.Locations[0].Col
			}
		}
		if len(doc.ToolErrors) < 200 {
			doc.ToolErrors = append(doc.ToolErrors, te)
		}
	}
	for _, r := range st.Records {
		var primary ccLocation
		if len(r.Locations) > 0 {
			primary = r.Locations[0]
		}
		unit := tuOf(r.File0)
		if unit == "" {
			unit = tuOf(primary.File)
		}
		switch ccKind(r) {
		case ccMissingInclude:
			addToolError(ccMissingInclude, unit, r)
			if u := tuOf(primary.File); u != "" {
				partialInclude[u] = true
			} else {
				headerMissing = true
			}
			continue
		case ccSystemInclude:
			doc.Limitations = append(doc.Limitations, "system_includes_not_parsed")
			continue
		case ccParse:
			addToolError(ccParse, unit, r)
			if unit != "" {
				failedParse[unit] = true
			} else {
				unattributedParse = true
			}
			continue
		case ccLimit:
			switch r.ID {
			case "normalCheckLevelMaxBranches":
				doc.Limitations = append(doc.Limitations, "check_level_branches_limited")
			default:
				doc.Limitations = append(doc.Limitations, "preprocessor_configurations_limited")
			}
			continue
		case ccReport:
			if m := activeCheckers.FindStringSubmatch(r.Msg); m != nil && r.ID == "checkersReport" {
				doc.Target.Properties["active_checkers"] = m[1]
			}
			continue
		case ccInformation:
			doc.Limitations = append(doc.Limitations, "tool_information_messages")
			continue
		}
		msg := mask(r.Msg)
		if msg == "" {
			msg = "(empty message)"
		}
		pathPtr, loc, state := rs.resolve(primary.File, primary.Line, primary.Col)
		pos := mask(primary.File) + ":" + strconv.Itoa(primary.Line) + ":" + strconv.Itoa(primary.Col)
		if pathPtr != nil {
			pos = *pathPtr + ":" + strconv.Itoa(primary.Line) + ":" + strconv.Itoa(primary.Col)
		}
		tuple := strings.Join([]string{unit, r.ID, pos, msg}, "\x00")
		ordinal := ordinals[tuple]
		ordinals[tuple]++
		o := obsdoc.Observation{ID: obsdoc.ObservationID(CppcheckID, ".", unit, r.ID, pos, pos, msg, ordinal), RuleID: r.ID, Category: cppcheckCategory(r.ID, r.Severity),
			RawSeverity: ptr(r.Severity), RawMessage: msg, Path: pathPtr, Location: loc, LocationState: state, Related: []obsdoc.Related{},
			RawRef: obsdoc.RawRef{Execution: execCppcheckCheck, Stream: "stderr", StreamSHA256: stderrSHA, RecordIndex: r.Index, ByteStart: r.ByteStart, ByteEnd: r.ByteEnd}, Unit: unit}
		for i, l := range r.Locations {
			if i == 0 {
				continue
			}
			if len(o.Related) >= 64 {
				doc.Limitations = append(doc.Limitations, "related_locations_truncated")
				break
			}
			rp, rl, rst := rs.resolve(l.File, l.Line, l.Col)
			o.Related = append(o.Related, obsdoc.Related{RawMessage: mask(l.Info), Path: rp, Location: rl, LocationState: rst})
		}
		doc.Observations = append(doc.Observations, o)
		if unit != "" {
			withFindings[unit] = true
		} else {
			doc.Limitations = append(doc.Limitations, "unattributed_observation_unit")
		}
	}
	// Records removed from the stored stream (a location outside the
	// workspace copy) still decide the outcome, without their text.
	for _, r := range in.Dropped {
		unit := tuOf(r.File0)
		switch ccKind(r) {
		case ccParse:
			if len(doc.ToolErrors) < 200 {
				doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: execCppcheckCheck, Kind: ccParse, Unit: unit,
					Message: r.ID + ": message withheld (location outside the workspace copy)"})
			}
			if unit != "" {
				failedParse[unit] = true
			} else {
				unattributedParse = true
			}
		case ccMissingInclude:
			headerMissing = true
		}
	}
	if len(in.Dropped) > 0 {
		doc.Limitations = append(doc.Limitations, LimOutsideDropped)
		doc.Target.Properties["outside_records_dropped"] = strconv.Itoa(len(in.Dropped))
	}
	if headerMissing {
		doc.Limitations = append(doc.Limitations, "missing_include_in_header")
	}
	if unattributedParse {
		doc.Limitations = append(doc.Limitations, "unattributed_parse_error")
	}

	exit := -1
	if in.Check != nil && in.Check.Status == executor.StatusExited && in.Check.ExitCode != nil {
		exit = *in.Check.ExitCode
	}
	checked := map[string]bool{}
	for _, f := range out.Checked {
		if rel, ok := rs.rel(f); ok {
			checked[rel] = true
		}
	}
	ran := in.Check != nil && (exit == 0 || exit == 2)
	notProcessed := false
	processed := []string{}
	for _, u := range in.Units {
		state := ""
		switch {
		case !ran:
			state = TUUnknown
		case failedParse[u]:
			state = TUFailedParse
		case partialInclude[u]:
			state = TUPartialMissingInclude
		case !checked[u]:
			state, notProcessed = TUNotProcessed, true
		case headerMissing:
			// cppcheck reports a header's missing include once, without
			// the including unit (measured): no unit can be claimed whole.
			state = TUPossiblyPartial
		case withFindings[u] && in.Mode == modeReduced:
			state = TUReducedWithFindings
		case withFindings[u]:
			state = TUAnalyzedWithFindings
		case in.Mode == modeReduced:
			state = TUReducedClean
		default:
			state = TUAnalyzedClean
		}
		doc.Coverage.Units[state]++
		switch state {
		case TUAnalyzedClean, TUAnalyzedWithFindings, TUReducedClean, TUReducedWithFindings:
			processed = append(processed, u)
		}
	}

	var o outcome
	o.executionFailures([]*executor.Result{in.Version, in.Check}, in.Cancelled)
	if in.Version != nil && toolVersion == "" {
		o.fail(domain.TaskFailed, ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	if in.Version == nil || in.Check == nil {
		o.fail(domain.TaskFailed, "cppcheck_not_run", obsdoc.ExitNotRun)
	}
	if len(doc.WorkspaceDrift) > 0 {
		o.fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	if in.Check != nil && !streamsComplete(in.Check) {
		o.fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
	}
	if in.Check != nil && (exit != 0 && exit != 2 || len(out.Fatal) > 0) {
		// Usage errors and "no input" exit 1 with "cppcheck: error:" on
		// stdout; a crash ends by signal.
		o.fail(domain.TaskFailed, ReasonCppcheckFailed, obsdoc.ExitExecutionError)
	}
	if in.Check != nil && !parsed {
		o.fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	// --error-exitcode=2: exit 2 iff at least one <error> record of any
	// severity other than the checkersReport summary, which cppcheck 2.22
	// writes on every run (measured: a clean unit exits 0 with only that
	// record; a lone missingInclude, missingIncludeSystem or syntaxError
	// exits 2).
	records := st.Values + len(in.Dropped)
	for _, r := range st.Records {
		if r.ID == "checkersReport" {
			records--
		}
	}
	if in.Check != nil && !(exit == 0 && records == 0 || exit == 2 && records > 0) {
		o.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
	}
	if len(failedParse) > 0 || unattributedParse {
		o.fail(domain.TaskFailed, ReasonTUErrors, obsdoc.ExitExecutionError)
	}
	if len(partialInclude) > 0 || headerMissing {
		o.fail(domain.TaskFailed, ReasonMissingInclude, obsdoc.ExitConfigurationError)
	}
	if notProcessed {
		o.fail(domain.TaskFailed, ReasonTUNotProcessed, obsdoc.ExitExecutionError)
	}
	partial := in.Check != nil && (st.Values > 0 || st.UnparsedBytes > 0 || in.Check.Stderr.Truncated || in.Check.Stderr.Incomplete)
	o.finish(&doc, processed, partial, parsed)
	return doc
}
