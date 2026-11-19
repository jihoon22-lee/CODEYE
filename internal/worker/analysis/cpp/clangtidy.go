package cpp

import (
	"context"
	"path/filepath"
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

// clang-tidy identity: the extra-depth C/C++ analyzer (Required=false). It
// needs a compile DB (a translation unit is compiled by the clang frontend
// with the entry's flags), and it carries the Clang Static Analyzer as its
// clang-analyzer-* checks; `clang --analyze` is the same engine and is never
// run (docs/ANALYZERS.md#cc-엔진-중복).
const (
	ClangTidyID             = "cpp.clang_tidy"
	ClangTidyName           = "clang-tidy"
	ClangTidyTool           = "clang-tidy"
	ClangTidyAdapterVersion = "1"
	ClangTidyRulePackID     = "codeye-cpp-clang-tidy"
	// ClangTidyRulePackVersion names ClangTidyChecks and ClangTidyOptions.
	ClangTidyRulePackVersion = "1"
	ReasonClangTidyFailed    = "clang_tidy_failed"
	// DefaultBatchSize bounds the translation units of one execution: a
	// failing or slow unit costs at most its batch, and argv stays short.
	DefaultBatchSize     = 16
	execClangTidyVersion = "clang-tidy.version"
	execClangTidyCheck   = "clang-tidy.check"
)

// ClangTidyChecks is the check selection of codeye-cpp-clang-tidy v1, in
// clang-tidy glob order. Excluded: platform families that do not apply to
// the main targets (osx, webkit, fuchsia), opt-in Analyzer families (taint,
// MPI, padding, localizability), checks that report every use of a standard
// function (DeprecatedOrUnsafeBufferHandling, mt-unsafe), bugprone checks
// about naming, style or conversions that fire on most C code without a
// defect, and the duplicates measured on fixtures/cpp/overlap
// (TestRealEngineOverlap, docs/ANALYZERS.md#cc-엔진-중복): checks that reported only what the
// baseline cppcheck also reported on the same lines (assert-side-effect,
// suspicious-memset-usage, core.BitwiseShift, core.StackAddressEscape,
// cplusplus.InnerPointer) and the Analyzer checks that reported only what a
// bugprone check reported (security.FloatLoopCounter, cplusplus.Move).
// Aliases (cert-*, hicpp-*, cppcoreguidelines-*) are not selected:
// clang-tidy would report the same diagnostic under two names.
var ClangTidyChecks = []string{
	"-*",
	"clang-analyzer-*",
	"-clang-analyzer-osx.*",
	"-clang-analyzer-optin.*",
	"-clang-analyzer-webkit.*",
	"-clang-analyzer-fuchsia.*",
	"-clang-analyzer-security.insecureAPI.DeprecatedOrUnsafeBufferHandling",
	"-clang-analyzer-security.FloatLoopCounter",
	"-clang-analyzer-core.BitwiseShift",
	"-clang-analyzer-core.StackAddressEscape",
	"-clang-analyzer-cplusplus.InnerPointer",
	"-clang-analyzer-cplusplus.Move",
	"bugprone-*",
	"-bugprone-easily-swappable-parameters",
	"-bugprone-narrowing-conversions",
	"-bugprone-implicit-widening-of-multiplication-result",
	"-bugprone-reserved-identifier",
	"-bugprone-signed-bitwise",
	"-bugprone-switch-missing-default-case",
	"-bugprone-assignment-in-if-condition",
	"-bugprone-casting-through-void",
	"-bugprone-multi-level-implicit-pointer-conversion",
	"-bugprone-macro-parentheses",
	"-bugprone-assert-side-effect",
	"-bugprone-suspicious-memset-usage",
	"performance-*",
	"-performance-avoid-endl",
	"-performance-enum-size",
	"concurrency-thread-canceltype-asynchronous",
	"misc-redundant-expression",
	"misc-unconventional-assign-operator",
	"misc-throw-by-value-catch-by-reference",
}

// ClangTidyOptions are the pinned configuration keys besides Checks. Every
// key clang-tidy would otherwise read from a repository .clang-tidy is set
// here: --config replaces the file lookup (measured: a repository
// .clang-tidy with Checks, WarningsAsErrors, HeaderFilterRegex, ExtraArgs
// and CheckOptions changes nothing).
var ClangTidyOptions = []string{`WarningsAsErrors: ""`, `HeaderFilterRegex: ".*"`, `ExcludeHeaderFilterRegex: ""`, `SystemHeaders: false`, `FormatStyle: none`, `CheckOptions: {}`}

// ClangTidyConfig is the inline --config value.
func ClangTidyConfig() string {
	return `{Checks: "` + strings.Join(ClangTidyChecks, ",") + `", ` + strings.Join(ClangTidyOptions, ", ") + `}`
}

// ClangTidySelection is the recorded rule selection: the check globs, then
// the pinned options.
func ClangTidySelection() []string {
	out := append([]string{}, ClangTidyChecks...)
	for _, o := range ClangTidyOptions {
		out = append(out, strings.ReplaceAll(o, `"`, ""))
	}
	return out
}

// ClangTidyArgs is the argv of one batch after the executable. cdbDir is
// the task-scratch directory of the CODEYE compile DB; files are absolute
// workspace paths of its units. --extra-arg-before=--no-default-config stops
// the clang driver from loading <triple>-<driver>.cfg files from the
// directory of the driver path (a repository-relative driver could load a
// repository file, measured), and the driver is a basename anyway.
func ClangTidyArgs(cdbDir string, files []string) []string {
	return append([]string{"-p", cdbDir, "--config=" + ClangTidyConfig(), "--extra-arg-before=--no-default-config", "--use-color=false"}, files...)
}

// ClangTidyDescriptor is clang-tidy's pack identity.
func ClangTidyDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: ClangTidyID, Name: ClangTidyName, Ecosystem: Ecosystem, Required: false, Tool: ClangTidyTool,
		VersionArgs: []string{"--version"}, ParseVersion: ParseClangTidyVersion}
}

// PlanClangTidy plans one task when C/C++ files are in scope, also without
// a compile DB: the gap is then visible as unsupported (compile_db_missing).
func PlanClangTidy(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return planOne(job, ClangTidyName, false, func(l layout) string {
		if len(l.DBs) > 0 {
			return "clang-tidy and Clang Static Analyzer with " + strconv.Itoa(len(l.DBs)) + " compile DB(s)"
		}
		return "clang-tidy and Clang Static Analyzer (needs a compile DB)"
	})
}

// ClangTidy is the configured clang-tidy adapter.
type ClangTidy struct{ t tool }

var _ pack.Analyzer = (*ClangTidy)(nil)

// NewClangTidy validates the configuration without running anything.
func NewClangTidy(cfg Config) (*ClangTidy, error) {
	t, err := newTool("clang-tidy", cfg)
	if err != nil {
		return nil, err
	}
	return &ClangTidy{t: t}, nil
}

// Descriptor implements pack.Analyzer.
func (a *ClangTidy) Descriptor() pack.Descriptor { return ClangTidyDescriptor() }

// Plan implements pack.Analyzer.
func (a *ClangTidy) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return PlanClangTidy(ctx, job)
}

// Probe runs `clang-tidy --version` in a fresh directory under dir.
func (a *ClangTidy) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	return a.t.probe(ctx, runner, dir, []string{"--version"}, ParseClangTidyVersion)
}

func clangTidyBase(job analysis.Job, key string, l layout) obsdoc.Document {
	doc := obsdoc.New(obsdoc.Analyzer{ID: ClangTidyID, Ecosystem: Ecosystem, Required: false}, obsdoc.Component{ID: ClangTidyName, Version: ClangTidyAdapterVersion},
		obsdoc.Component{ID: ClangTidyParserID, Version: ClangTidyParserVersion}, key, job.Snapshot.Digest)
	doc.Module.Dir = "."
	doc.Patterns = patterns(job.Scope)
	doc.Tool.Name = ClangTidyTool
	doc.RulePack = obsdoc.RulePack{ID: ClangTidyRulePackID, Version: ClangTidyRulePackVersion, Selection: ClangTidySelection(),
		RepositoryConfig: obsdoc.RepoConfigNone, SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Target.Platform = ptr(runtime.GOOS + "/" + runtime.GOARCH)
	doc.Target.Properties = map[string]string{"header_filter": ".*", "system_headers": "false", "repository_clang_tidy_config": "overridden", "source_snippets": "removed"}
	doc.Coverage.Unit = "translation_unit"
	doc.Coverage.FilesInScope = ptr(len(l.Sources))
	return doc
}

// Run executes the clang-tidy task.
func (a *ClangTidy) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	tasks, err := PlanClangTidy(ctx, job)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError, "plan_failed")
	}
	if !slices.ContainsFunc(tasks, func(t pack.Task) bool { return t.Spec.Key == task.Key }) {
		return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	l, _ := layoutOf(job)
	s, bad := newSession(job, task.Key, a.t, "$CLANG_TIDY")
	if bad != nil {
		return *bad
	}
	doc := clangTidyBase(job, task.Key, l)
	if len(l.DBs) == 0 {
		// Without build flags the clang frontend cannot compile a unit; a
		// configure step that would write the DB executes repository code
		// and is not implemented.
		doc.Limitations = append(doc.Limitations, "compile_db_generation_not_implemented")
		return notRun(s, doc, domain.TaskUnsupported, ReasonCompileDBMissing, obsdoc.ExitNotRun)
	}
	data, cfgFiles, ok := readDBs(job, l)
	if !ok {
		return notRun(s, doc, domain.TaskFailed, ReasonSnapshotIntegrity, obsdoc.ExitNotRun)
	}
	m, err := MapDBs(l.DBs, data, l.Files, func(rel string) bool { return pack.PathInScope(rel, job.Scope) })
	if err != nil {
		return notRun(s, doc, domain.TaskFailed, ReasonCompileDBUnreadable, obsdoc.ExitNotRun)
	}
	in := ctInput{Key: task.Key, Layout: l, Mapping: &m, ConfigFiles: cfgFiles}
	applyClangTidyMapping(&doc, in)
	if len(m.Units) == 0 {
		return notRun(s, doc, domain.TaskUnsupported, ReasonNoTranslationUnits, obsdoc.ExitNotRun)
	}
	b, err := RenderDB(s.workspace, m.Units)
	cdbDir := s.workDir
	if err != nil || analysis.WriteFileExclusive(filepath.Join(cdbDir, CompileDBName), b) != nil {
		return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
	}
	for i := 0; i < len(m.Units); i += a.t.cfg.BatchSize {
		end := min(i+a.t.cfg.BatchSize, len(m.Units))
		var batch []string
		for _, u := range m.Units[i:end] {
			batch = append(batch, u.Rel)
		}
		in.Batches = append(in.Batches, batch)
	}
	in.Checks = make([]*executor.Result, len(in.Batches))
	in.Dropped = make([]ctDropped, len(in.Batches))
	res, e, err := s.run(ctx, runner, execClangTidyVersion, s.spec([]string{"--version"}, probeTimeout), nil)
	in.Version = res
	if err != nil {
		return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Limitations: []string{"execution_write_failed"}}
	}
	in.Executions = append(in.Executions, e)
	if _, known := ParseClangTidyVersion(res.Stdout.Data); known && exitedWith(res, 0) && !pack.Stoppable(res) {
		for bi, batch := range in.Batches {
			if ctx.Err() != nil {
				in.Cancelled = true
				break
			}
			files := make([]string, len(batch))
			for j, rel := range batch {
				files[j] = filepath.Join(s.workspace, filepath.FromSlash(rel))
			}
			res, e, err := s.run(ctx, runner, checkExecName(bi), s.spec(ClangTidyArgs(cdbDir, files), a.t.cfg.Timeout), func(r *executor.Result) {
				in.Dropped[bi] = sanitizeClangTidy(r, func(p string) bool { return insideOrPseudo(p, s.inside) })
			})
			in.Checks[bi] = res
			if err != nil {
				return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: in.Executions, Limitations: []string{"execution_write_failed"}}
			}
			in.Executions = append(in.Executions, e)
			if pack.Stoppable(res) {
				break
			}
		}
	}
	in.Drift = drift(s.workspace, l.Files)
	in.Roots, in.Mask, in.SnapshotDir = s.roots, s.mask, job.Snapshot.SnapshotDir
	return s.write(assembleClangTidy(doc, in), in.Executions)
}

func checkExecName(batch int) string { return execClangTidyCheck + "." + strconv.Itoa(batch+1) }

// ctInput is everything the pure clang-tidy document builder needs.
type ctInput struct {
	Key         string
	Layout      layout
	Mapping     *Mapping
	ConfigFiles []obsdoc.ConfigFile
	Batches     [][]string
	Version     *executor.Result
	Checks      []*executor.Result // per batch; nil = not run
	Dropped     []ctDropped        // per batch: what sanitizeClangTidy removed
	Executions  []analysis.Execution
	Drift       map[string]bool
	Roots       []string
	Mask        func(string) string
	SnapshotDir string
	Cancelled   bool
}

// applyClangTidyMapping records the compile DB (honored repository build
// configuration, bound to its manifest digests) and its mapping.
func applyClangTidyMapping(doc *obsdoc.Document, in ctInput) {
	m := in.Mapping
	doc.Target.Properties["mode"] = modeCompileDB
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

// clangTidyCategory is the policy of codeye-cpp-clang-tidy v1.
func clangTidyCategory(check string) string {
	switch {
	case strings.HasPrefix(check, "performance-"):
		return "performance"
	case strings.HasPrefix(check, "clang-analyzer-security.") || strings.HasPrefix(check, "clang-analyzer-unix.Chroot") ||
		slices.Contains([]string{"bugprone-unsafe-functions", "bugprone-command-processor", "bugprone-not-null-terminated-result", "bugprone-random-generator-seed"}, check):
		return "security"
	case slices.Contains([]string{"clang-analyzer-unix.Malloc", "clang-analyzer-unix.MallocSizeof", "clang-analyzer-unix.MismatchedDeallocator", "clang-analyzer-unix.Stream",
		"clang-analyzer-cplusplus.NewDelete", "clang-analyzer-cplusplus.NewDeleteLeaks", "clang-analyzer-cplusplus.ArrayDelete", "clang-analyzer-cplusplus.InnerPointer",
		"bugprone-unique-ptr-array-mismatch", "bugprone-shared-ptr-array-mismatch", "bugprone-dangling-handle", "bugprone-suspicious-realloc-usage"}, check):
		return "resource_lifecycle"
	case strings.HasPrefix(check, "concurrency-") || slices.Contains([]string{"clang-analyzer-unix.BlockInCriticalSection", "bugprone-spuriously-wake-up-functions",
		"bugprone-bad-signal-to-kill-thread", "bugprone-signal-handler"}, check):
		return "concurrency"
	case slices.Contains([]string{"bugprone-exception-escape", "bugprone-empty-catch", "bugprone-unhandled-exception-at-new", "misc-throw-by-value-catch-by-reference",
		"bugprone-throw-keyword-missing", "bugprone-std-exception-baseclass", "bugprone-exception-copy-constructor-throws", "bugprone-unsafe-to-allow-exceptions",
		"bugprone-throwing-static-initialization", "bugprone-unused-return-value", "bugprone-posix-return", "clang-analyzer-unix.Errno",
		"bugprone-unchecked-string-to-number-conversion"}, check):
		return "error_handling"
	case strings.HasPrefix(check, "clang-analyzer-deadcode."):
		return "maintainability"
	}
	return "correctness"
}

// ctBatch is the parse of one batch execution.
type ctBatch struct {
	files   []string
	res     *executor.Result
	out     ctStream
	errs    ctStderr
	exit    int
	sha     string
	failed  map[string]bool // units whose cumulative error count grew
	seen    map[string]bool // units with a stderr block
	skipped map[string]bool
	shape   int // stderr blocks that do not follow the measured format
	// lastErrors is the cumulative error count at the end of the run.
	lastErrors int
	dropped    ctDropped
}

// assembleClangTidy builds the envelope and decides the outcome.
func assembleClangTidy(doc obsdoc.Document, in ctInput) obsdoc.Document {
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
		if v, ok := ParseClangTidyVersion(in.Version.Stdout.Data); ok {
			toolVersion = v
			doc.Tool.Version, doc.Tool.VersionTested = ptr(v), ClangTidyVersionTested(v)
			if !doc.Tool.VersionTested {
				doc.Limitations = append(doc.Limitations, "tool_version_untested")
			}
		}
	}
	rs := &resolver{files: in.Layout.Files, drift: in.Drift, snapshotDir: in.SnapshotDir, roots: in.Roots, cache: map[string]lineInfo{}}
	unitOf := map[string]int{} // TU -> batch
	for bi, b := range in.Batches {
		for _, f := range b {
			unitOf[f] = bi
		}
	}
	batches := make([]ctBatch, len(in.Batches))
	parsed := true
	nolint := 0
	for bi, files := range in.Batches {
		b := ctBatch{files: files, exit: -1, failed: map[string]bool{}, seen: map[string]bool{}, skipped: map[string]bool{}}
		if bi < len(in.Checks) {
			b.res = in.Checks[bi]
		}
		if b.res != nil {
			b.out, b.errs = parseClangTidyStdout(b.res.Stdout.Data), parseClangTidyStderr(b.res.Stderr.Data)
			b.sha = obsdoc.SHA256Hex(b.res.Stdout.Data)
			if b.res.Status == executor.StatusExited && b.res.ExitCode != nil {
				b.exit = *b.res.ExitCode
			}
			member := map[string]bool{}
			for _, f := range files {
				member[f] = true
			}
			relOf := func(name string) (string, bool) {
				rel, ok := rs.rel(name)
				return rel, ok && member[rel]
			}
			for _, n := range b.errs.Skipped {
				if rel, ok := relOf(n); ok {
					b.skipped[rel] = true
				} else {
					b.shape++
				}
			}
			switch {
			case len(files) == 1 && len(b.errs.Units) <= 1:
				// One unit: no Processing line; the error line names it.
				b.seen[files[0]] = true
				if len(b.errs.Units) == 1 {
					u := b.errs.Units[0]
					b.lastErrors = u.Errors
					if u.ErrorLine {
						if rel, ok := relOf(u.File); ok {
							b.failed[rel] = true
						} else {
							b.shape++
						}
					}
					if u.ErrorLine != (u.Errors > 0) || u.Foreign > 0 {
						b.shape++
					}
				}
			default:
				prev := 0
				for i, u := range b.errs.Units {
					rel, ok := relOf(u.File)
					if !ok || u.Index != i+1 || u.Total != len(files) || b.seen[rel] {
						b.shape++
						continue
					}
					b.seen[rel] = true
					if u.Errors > prev {
						b.failed[rel] = true
					}
					// "Error while processing" follows the cumulative count.
					if u.Errors < prev || u.ErrorLine != (u.Errors > 0) || u.Foreign > 0 {
						b.shape++
					}
					prev = u.Errors
				}
				b.lastErrors = prev
			}
			nolint += b.errs.Nolint
			if b.errs.Other > 0 {
				doc.Limitations = append(doc.Limitations, "tool_stderr_unrecognized")
			}
			if b.out.ShapeErrors > 0 || b.out.UnparsedBytes > 0 || b.shape > 0 {
				parsed = false
			}
			doc.Parse.Records += b.out.Values
			doc.Parse.UnparsedBytes += b.out.UnparsedBytes
			doc.Parse.ShapeErrors += b.out.ShapeErrors + b.shape
		}
		batches[bi] = b
	}
	if nolint > 0 {
		doc.Target.Properties["nolint_suppressed"] = strconv.Itoa(nolint)
		doc.Limitations = append(doc.Limitations, "nolint_suppressed_diagnostics")
	}
	// What sanitizeClangTidy removed from the stored streams: errors outside
	// the workspace still count as compile errors, without their text.
	droppedDiags := 0
	for bi := range batches {
		if bi >= len(in.Dropped) || batches[bi].res == nil {
			continue
		}
		d := in.Dropped[bi]
		batches[bi].dropped = d
		droppedDiags += d.Diagnostics + d.Notes
		for i := 0; i < d.Errors && len(doc.ToolErrors) < 200; i++ {
			doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: checkExecName(bi), Kind: "compile_error",
				Message: "compiler error message withheld (location outside the workspace copy)"})
		}
	}
	if droppedDiags > 0 {
		doc.Limitations = append(doc.Limitations, LimOutsideDropped)
		doc.Target.Properties["outside_records_dropped"] = strconv.Itoa(droppedDiags)
	}

	withFindings := map[string]bool{}
	missingIn := map[string]bool{}  // TU whose own line names a missing include
	headerMissing := map[int]bool{} // batch with a missing include in a header
	errorIn := map[string]bool{}
	ordinals := map[string]int{}
	headerSeen := map[string]int{} // header diagnostic tuple -> batch (clang-tidy dedups within one run)
	for bi, b := range batches {
		if b.res == nil {
			continue
		}
		exec := checkExecName(bi)
		for _, d := range b.out.Diags {
			unit := ""
			if rel, ok := rs.rel(d.File); ok {
				if ub, ok := unitOf[rel]; ok && ub == bi {
					unit = rel
				}
			}
			rule := d.Rule()
			if d.Level == "error" || d.Level == "fatal error" {
				kind := "compile_error"
				if strings.HasSuffix(d.Message, "file not found") {
					kind = ccMissingInclude
					if unit != "" {
						missingIn[unit] = true
					} else {
						headerMissing[bi] = true
					}
				} else if unit != "" {
					errorIn[unit] = true
				}
				te := obsdoc.ToolError{Execution: exec, Kind: kind, Unit: unit, Message: mask(d.Message)}
				if rel, ok := rs.rel(d.File); ok {
					te.Path, te.Line, te.Column = ptr(rel), d.Line, d.Col
				}
				if len(doc.ToolErrors) < 200 {
					doc.ToolErrors = append(doc.ToolErrors, te)
				}
				continue
			}
			if d.Level == "remark" {
				doc.Limitations = append(doc.Limitations, "tool_remarks")
				continue
			}
			if strings.HasPrefix(rule, "clang-diagnostic-") {
				// Compiler warnings are not selected (docs/ANALYZERS.md#cppcheck); one that
				// still appears (a #pragma) is not a finding.
				doc.Limitations = append(doc.Limitations, "compiler_warnings_ignored")
				continue
			}
			msg := mask(d.Message)
			if msg == "" {
				msg = "(empty message)"
			}
			pathPtr, loc, state := rs.resolve(d.File, d.Line, d.Col)
			pos := mask(d.File) + ":" + strconv.Itoa(d.Line) + ":" + strconv.Itoa(d.Col)
			if pathPtr != nil {
				pos = *pathPtr + ":" + strconv.Itoa(d.Line) + ":" + strconv.Itoa(d.Col)
			}
			tuple := strings.Join([]string{unit, rule, pos, msg}, "\x00")
			if unit == "" {
				if first, ok := headerSeen[tuple]; ok && first != bi {
					continue
				}
				headerSeen[tuple] = bi
			}
			ordinal := ordinals[tuple]
			ordinals[tuple]++
			o := obsdoc.Observation{ID: obsdoc.ObservationID(ClangTidyID, ".", unit, rule, pos, pos, msg, ordinal), RuleID: rule, Category: clangTidyCategory(rule),
				RawSeverity: ptr(d.Level), RawMessage: msg, Path: pathPtr, Location: loc, LocationState: state, Related: []obsdoc.Related{},
				RawRef: obsdoc.RawRef{Execution: exec, Stream: "stdout", StreamSHA256: b.sha, RecordIndex: d.Index, ByteStart: d.ByteStart, ByteEnd: d.ByteEnd}, Unit: unit}
			for _, n := range d.Notes {
				if len(o.Related) >= 64 {
					doc.Limitations = append(doc.Limitations, "related_locations_truncated")
					break
				}
				rp, rl, rst := rs.resolve(n.File, n.Line, n.Col)
				o.Related = append(o.Related, obsdoc.Related{RawMessage: mask(n.Message), Path: rp, Location: rl, LocationState: rst})
			}
			doc.Observations = append(doc.Observations, o)
			if unit != "" {
				withFindings[unit] = true
			}
		}
	}

	var o outcome
	results := append([]*executor.Result{in.Version}, in.Checks...)
	o.executionFailures(results, in.Cancelled)
	if in.Version != nil && toolVersion == "" {
		o.fail(domain.TaskFailed, ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	if in.Version == nil {
		o.fail(domain.TaskFailed, "clang_tidy_not_run", obsdoc.ExitNotRun)
	}
	if len(doc.WorkspaceDrift) > 0 {
		o.fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	ranAny := false
	for _, b := range batches {
		if b.res == nil {
			continue
		}
		ranAny = true
		if !pack.StreamsComplete(b.res) {
			o.fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
		}
		if (b.exit != 0 && b.exit != 1) || len(b.errs.Fatal) > 0 {
			// A crash, a usage error, or a compile DB that was not loaded
			// ("Running without flags." is a silent no-flags fallback).
			o.fail(domain.TaskFailed, ReasonClangTidyFailed, obsdoc.ExitExecutionError)
		}
	}
	if !parsed {
		o.fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	for _, b := range batches {
		if b.res == nil || (b.exit != 0 && b.exit != 1) || len(b.errs.Fatal) > 0 {
			continue
		}
		errorDiags := b.dropped.Errors > 0
		for _, d := range b.out.Diags {
			errorDiags = errorDiags || d.Level == "error" || d.Level == "fatal error"
		}
		// exit 0 iff no unit had a compiler error (measured); exit 1 when
		// the run's error count is non-zero.
		if b.exit == 0 && (b.lastErrors > 0 || errorDiags) || b.exit == 1 && (b.lastErrors == 0 || !errorDiags) {
			o.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
		}
	}

	processed := []string{}
	notProcessed, compileFailed, includeFailed := false, false, false
	for bi, b := range batches {
		for _, u := range b.files {
			state := ""
			switch {
			case b.res == nil:
				state, notProcessed = TUNotProcessed, true
			case (b.exit != 0 && b.exit != 1) || len(b.errs.Fatal) > 0:
				state = TUUnknown
			case b.failed[u] && (missingIn[u] || headerMissing[bi] && !errorIn[u]):
				state, includeFailed = TUFailedMissingInclude, true
			case b.failed[u]:
				state, compileFailed = TUFailedCompile, true
			case b.skipped[u] || len(b.files) > 1 && !b.seen[u]:
				state, notProcessed = TUNotProcessed, true
			case withFindings[u]:
				state = TUAnalyzedWithFindings
			default:
				state = TUAnalyzedClean
			}
			doc.Coverage.Units[state]++
			if state == TUAnalyzedClean || state == TUAnalyzedWithFindings {
				processed = append(processed, u)
			}
		}
	}
	if compileFailed {
		o.fail(domain.TaskFailed, ReasonTUErrors, obsdoc.ExitExecutionError)
	}
	if includeFailed {
		o.fail(domain.TaskFailed, ReasonMissingInclude, obsdoc.ExitConfigurationError)
	}
	if notProcessed && ranAny {
		o.fail(domain.TaskFailed, ReasonTUNotProcessed, obsdoc.ExitExecutionError)
	}
	// As in the other adapters: a failed task keeps what it parsed and says so.
	partial := false
	for _, b := range batches {
		if b.res != nil && (b.out.Values > 0 || b.out.UnparsedBytes > 0 || b.res.Stdout.Truncated || b.res.Stdout.Incomplete) {
			partial = true
		}
	}
	o.finish(&doc, processed, partial, parsed)
	return doc
}
