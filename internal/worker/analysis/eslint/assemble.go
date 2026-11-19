package eslint

import (
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task reasons (TaskResult.Reason and outcome.reason).
const (
	ReasonCleanupUncertain     = "cleanup_uncertain"
	ReasonCancelled            = "cancelled"
	ReasonTimeout              = "timeout"
	ReasonExecutionUnsupported = "execution_unsupported"
	ReasonToolStartFailed      = "tool_start_failed"
	ReasonExecutionRejected    = "execution_rejected"
	ReasonToolVersion          = "tool_version_unrecognized"
	ReasonSnapshotIntegrity    = "snapshot_integrity_failed"
	ReasonNotRun               = "eslint_not_run"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonConfigOutside        = jsts.ReasonConfigOutside
	ReasonWriteOption          = jsts.ReasonWriteOption
	ReasonForeignNodeModules   = "node_modules_outside_snapshot"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonFailed               = "eslint_failed"
	ReasonExitMismatch         = "exit_status_mismatch"
	ReasonSuppressionsActive   = "source_suppressions_not_disabled"
	ReasonConfig               = "tsconfig_invalid"
	ReasonDependency           = "dependency_unavailable"
	ReasonParseErrors          = "parse_errors"
	ReasonNoFiles              = "no_analyzable_files"
)

// File (unit) states.
const (
	FileClean           = "linted_clean"
	FileWithFindings    = "linted_with_findings"
	FileParseError      = "parse_error"
	FileOutsideTSConfig = "outside_tsconfig_program"
	FileNotLinted       = "not_linted"
	FileUnknown         = "unknown"
)

// outsideProgram is the fatal parser message for a TS file the module's
// tsconfig does not include (typescript-eslint 8.73, parserOptions.project).
const outsideProgram = "was not found in any of the provided project(s)"

// tsconfigUnparsed is typescript-eslint's fatal message for a tsconfig that
// does not load.
const tsconfigUnparsed = "Unable to parse the specified 'tsconfig' file"

type assembleInput struct {
	Typed                   bool
	TaskKey, ManifestDigest string
	Module                  jsts.Module
	Patterns                []string
	Files                   map[string]source.Entry
	Expected                []string
	Selection               []string
	Plugins                 plugins
	NodeVersion             string
	ModuleName              string
	Chain                   jsts.ConfigChain
	Version, Check          *executor.Result
	Executions              []analysis.Execution
	Drift                   map[string]bool
	Resolver                *jsts.Resolver
	Roots                   []string
	Mask                    func(string) string
	Cancelled, Integrity    bool
	Policy                  string
	PolicyOptions           []string
}

func ptr[T any](v T) *T { return &v }

// maintainability are the baseline rules about code hygiene rather than
// behavior.
var maintainability = map[string]bool{
	"no-unused-vars": true, "@typescript-eslint/no-unused-vars": true, "no-empty": true, "no-empty-pattern": true, "no-empty-static-block": true,
	"no-extra-boolean-cast": true, "no-useless-escape": true, "no-useless-catch": true, "no-useless-assignment": true, "no-useless-backreference": true,
	"no-unused-labels": true, "no-unused-private-class-members": true, "no-regex-spaces": true, "no-irregular-whitespace": true, "no-var": true,
	"prefer-const": true, "prefer-rest-params": true, "prefer-spread": true, "no-case-declarations": true, "no-octal": true, "no-nonoctal-decimal-escape": true,
	"@typescript-eslint/no-explicit-any": true, "@typescript-eslint/ban-ts-comment": true, "@typescript-eslint/no-empty-object-type": true,
	"@typescript-eslint/no-unused-expressions": true, "@typescript-eslint/no-require-imports": true, "@typescript-eslint/triple-slash-reference": true,
	"@typescript-eslint/no-namespace": true, "@typescript-eslint/prefer-namespace-keyword": true, "@typescript-eslint/no-unnecessary-type-constraint": true,
	"@typescript-eslint/no-wrapper-object-types": true, "@typescript-eslint/no-unsafe-function-type": true, "@typescript-eslint/prefer-as-const": true,
	"@typescript-eslint/no-array-constructor": true, "@typescript-eslint/no-this-alias": true, "@typescript-eslint/no-unnecessary-type-assertion": true,
	"@typescript-eslint/no-duplicate-type-constituents": true, "@typescript-eslint/no-redundant-type-constituents": true,
}

// category is the policy of codeye-js-eslint / codeye-ts-eslint-typed v1:
// what a rule is about, not its impact.
func category(rule string) string {
	switch rule {
	case "@typescript-eslint/no-floating-promises", "@typescript-eslint/no-misused-promises", "@typescript-eslint/await-thenable", "@typescript-eslint/require-await",
		"@typescript-eslint/only-throw-error", "@typescript-eslint/prefer-promise-reject-errors", "no-async-promise-executor", "no-unsafe-finally", "preserve-caught-error":
		return "error_handling"
	case "@typescript-eslint/no-implied-eval":
		return "security"
	}
	if strings.HasPrefix(rule, "@typescript-eslint/no-unsafe-") {
		return "api_contract"
	}
	if maintainability[rule] {
		return "maintainability"
	}
	return "correctness"
}

func addLim(l *[]string, codes ...string) { *l = append(*l, codes...) }

// assemble builds the envelope and decides the outcome. Success is one
// explicit conjunction; every other case keeps what was parsed.
func assemble(in assembleInput) obsdoc.Document {
	mask := in.Mask
	if mask == nil {
		mask = func(s string) string { return s }
	}
	id, name, rp, rpv := AnalyzerID, Name, RulePackID, RulePackVersion
	if in.Typed {
		id, name, rp, rpv = TypedAnalyzerID, TypedName, TypedRulePackID, TypedRulePackVersion
	}
	doc := obsdoc.New(obsdoc.Analyzer{ID: id, Ecosystem: jsts.Ecosystem, Required: !in.Typed}, obsdoc.Component{ID: name, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = in.Module.Dir
	doc.Module.Name = in.ModuleName
	doc.Patterns = append([]string{}, in.Patterns...)
	doc.Tool.Name = Tool
	// The managed configuration replaces the repository's ESLint
	// configuration (overridden); --no-inline-config and an empty
	// suppressions file turn off every in-source and bulk suppression
	// (disabled). The type-aware task also honors the module's tsconfig.
	doc.RulePack = obsdoc.RulePack{ID: rp, Version: rpv, Selection: append([]string{}, in.Selection...), RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressDisabled, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	if in.Typed {
		doc.RulePack.RepositoryConfig = obsdoc.RepoConfigHonored
		doc.RulePack.ConfigFiles = nil
		if !in.Chain.Unreadable && len(in.Chain.Packages) == 0 && !in.Chain.Outside && len(in.Chain.Files) > 0 {
			doc.RulePack.ConfigFiles = append([]obsdoc.ConfigFile{}, in.Chain.Files...)
		}
	}
	props := map[string]string{"node": in.NodeVersion, "parser_typescript": in.Plugins.typescript.Version, "typescript_eslint": in.Plugins.tseslint.Version,
		"tsconfig": "not_used"}
	if !in.Typed {
		props["eslint_js"] = in.Plugins.eslintJS.Version
	} else {
		props["tsconfig"] = "tsconfig.json"
	}
	for k, v := range props {
		if v == "" || !obsdoc.SafeText(v, 64) || strings.ContainsAny(v, "/\\\n") {
			delete(props, k)
		}
	}
	doc.Target.Properties = props
	doc.Coverage.Unit = "file"
	doc.Coverage.FilesInScope = ptr(len(in.Expected))
	limitations := []string{"repository_eslint_config_not_loaded"}
	if in.Module.Implicit {
		addLim(&limitations, "no_package_json_module")
	}
	if in.Typed {
		addLim(&limitations, "javascript_files_not_type_checked")
	}
	for pkg, series := range testedPlugins {
		v := ""
		switch pkg {
		case pkgESLintJS:
			v = in.Plugins.eslintJS.Version
		case pkgTSESLint:
			v = in.Plugins.tseslint.Version
		case pkgTypeScript:
			v = in.Plugins.typescript.Version
		}
		if !strings.HasPrefix(v, series) {
			addLim(&limitations, "plugin_version_untested")
		}
	}
	for _, e := range in.Executions {
		doc.Executions = append(doc.Executions, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	notRun := func(status domain.TaskStatus, reason, class string) obsdoc.Document {
		doc.Outcome = obsdoc.Outcome{TaskStatus: status, Reason: ptr(reason), ExitClass: class}
		doc.Coverage.ProcessedPaths = []string{}
		doc.Limitations = append(append(limitations, reason), jsts.OptionLimitations(in.PolicyOptions)...)
		return doc
	}
	if in.Integrity {
		return notRun(domain.TaskFailed, ReasonSnapshotIntegrity, obsdoc.ExitNotRun)
	}
	if in.Policy != "" {
		return notRun(domain.TaskSkippedPolicy, in.Policy, obsdoc.ExitNotRun)
	}
	for p := range in.Drift {
		doc.WorkspaceDrift = append(doc.WorkspaceDrift, p)
	}
	sort.Strings(doc.WorkspaceDrift)
	if len(doc.WorkspaceDrift) > 0 {
		addLim(&limitations, "workspace_drift")
	}

	toolOK := false
	if in.Version != nil && pack.ExitedZero(in.Version) {
		if v, ok := ParseVersion(in.Version.Stdout.Data); ok {
			toolOK = true
			doc.Tool.Version, doc.Tool.VersionTested = ptr(v), VersionTested(v)
			if !doc.Tool.VersionTested {
				addLim(&limitations, "tool_version_untested")
			}
		}
	}

	var st stream
	parsed, crash := false, false
	stdoutSHA := ""
	if in.Check != nil {
		st = parseStream(in.Check.Stdout.Data)
		parsed = st.UnparsedBytes == 0 && st.ShapeErrors == 0 && st.DecodeError == ""
		stdoutSHA = obsdoc.SHA256Hex(in.Check.Stdout.Data)
		se := parseStderr(in.Check.Stderr.Data)
		crash = se.Crash
		for _, w := range se.Warnings {
			if w == "ESLintIgnoreWarning" {
				addLim(&limitations, "eslintignore_unsupported")
			} else {
				addLim(&limitations, "tool_stderr_warning")
			}
		}
		if se.Other > 0 {
			addLim(&limitations, "tool_stderr_output")
		}
		if se.Dropped > 0 {
			addLim(&limitations, "tool_stderr_lines_dropped")
		}
	}
	doc.Parse.Records, doc.Parse.UnparsedBytes, doc.Parse.ShapeErrors, doc.Parse.DecodeError = st.Values, st.UnparsedBytes, st.ShapeErrors, st.DecodeError

	expected := map[string]bool{}
	for _, f := range in.Expected {
		expected[f] = true
	}
	var cands []obsdoc.Observation
	var withheld []obsdoc.ToolError
	states := map[string]string{}
	suppressedActive, gap, config, parseErr, configReported := false, false, false, false, false
	errorsN := 0
	ordinals := map[string]int{}
	toolError := func(te obsdoc.ToolError) {
		if len(doc.ToolErrors) < 200 {
			doc.ToolErrors = append(doc.ToolErrors, te)
		} else {
			addLim(&limitations, "tool_errors_truncated")
		}
	}
	for _, r := range st.Results {
		for _, m := range r.Messages {
			if m.Fatal || m.Severity == 2 {
				errorsN++
			}
		}
		rel, ok := jsts.StripWorkspace(r.FilePath, in.Module.Dir, in.Roots)
		if _, known := in.Files[rel]; !ok || !known {
			addLim(&limitations, "results_outside_snapshot")
			continue
		}
		if !expected[rel] {
			addLim(&limitations, "results_outside_module")
			continue
		}
		if r.Suppressed > 0 {
			suppressedActive = true
		}
		state := FileClean
		for _, m := range r.Messages {
			text := jsts.WithholdHostPaths(mask(m.Text))
			switch {
			case m.Fatal && in.Typed && strings.Contains(m.Text, outsideProgram):
				state = FileOutsideTSConfig
				continue
			case m.Fatal && in.Typed && strings.Contains(m.Text, tsconfigUnparsed):
				// The tsconfig did not load (measured: an unresolved
				// package extends, TS6053): a configuration error of the
				// program, not a parse error of the file.
				config = true
				state = FileNotLinted
				if !configReported {
					configReported = true
					toolError(obsdoc.ToolError{Execution: execCheck, Kind: "config", Unit: "program", Message: text})
				}
				continue
			case m.Fatal:
				parseErr = true
				state = FileParseError
				toolError(obsdoc.ToolError{Execution: execCheck, Kind: "parse", Unit: rel, Message: text, Path: ptr(rel), Line: m.Line, Column: m.Col})
				continue
			case m.RuleID == "":
				addLim(&limitations, "tool_notices")
				continue
			case m.RuleID == "codeye/program-diagnostics":
				code, scope, body, ok := parseProgramDiagnostic(m.Text)
				te := obsdoc.ToolError{Execution: execCheck, Kind: "config", Unit: "program", Message: jsts.WithholdHostPaths(mask(body))}
				if scope == "file" {
					te.Unit, te.Path, te.Line, te.Column = rel, ptr(rel), m.Line, m.Col
				}
				switch {
				case !ok:
					addLim(&limitations, "tool_notices")
					continue
				case jsts.IsGap(code):
					gap = true
					te.Kind = "dependency"
				case jsts.IsDeprecation(code):
					addLim(&limitations, "typescript_deprecations_ignored")
					continue
				default:
					config = true
				}
				te.Message = "TS" + strconv.Itoa(code) + ": " + te.Message
				toolError(te)
				continue
			case strings.HasPrefix(m.RuleID, "codeye/"):
				addLim(&limitations, "tool_notices")
				continue
			}
			pathPtr, loc, lstate := in.Resolver.Resolve(r.FilePath, m.Line, m.Col, m.EndLine, m.EndCol)
			startText := mask(rel + ":" + strconv.Itoa(m.Line) + ":" + strconv.Itoa(m.Col))
			endText := mask(rel + ":" + strconv.Itoa(m.EndLine) + ":" + strconv.Itoa(m.EndCol))
			tuple := strings.Join([]string{rel, m.RuleID, startText, endText, text}, "\x00")
			ordinal := ordinals[tuple]
			ordinals[tuple]++
			sev := "error"
			if m.Severity == 1 {
				sev = "warning"
			}
			o := obsdoc.Observation{ID: obsdoc.ObservationID(id, in.Module.Dir, rel, m.RuleID, startText, endText, text, ordinal), RuleID: m.RuleID,
				Category: category(m.RuleID), RawSeverity: ptr(sev), RawMessage: text, Path: pathPtr, Location: loc, LocationState: lstate, Related: []obsdoc.Related{},
				RawRef: obsdoc.RawRef{Execution: execCheck, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: m.Index, JSONPointer: m.Pointer, ByteStart: m.ByteStart, ByteEnd: m.ByteEnd},
				Unit:   rel, SuggestedFixes: m.Fixes}
			if o.RawMessage == "" {
				o.RawMessage = "(empty message)"
			}
			cands = append(cands, o)
			withheld = append(withheld, obsdoc.ToolError{Execution: execCheck, Kind: "withheld", Unit: rel, Message: m.RuleID + ": " + text, Path: ptr(rel), Line: m.Line, Column: m.Col})
			if state == FileClean {
				state = FileWithFindings
			}
		}
		if prev, dup := states[rel]; !dup || prev == FileClean {
			states[rel] = state
		}
	}
	// A type-aware run whose program misses a dependency, or whose
	// configuration did not load, has incomplete types: every type-aware
	// diagnostic may follow from that (measured, docs/ANALYZERS.md#의존성-공백), so none is an
	// observation. They stay in stdout and are listed as withheld.
	if in.Typed && (gap || config) {
		addLim(&limitations, "diagnostics_withheld_incomplete_types")
		for _, te := range withheld {
			toolError(te)
		}
		cands = nil
	}
	doc.Observations = append(doc.Observations, cands...)
	for _, o := range doc.Observations {
		if o.LocationState != obsdoc.LocationResolved {
			addLim(&limitations, "unresolved_observation_location")
			break
		}
	}

	exit := -1
	if in.Check != nil && in.Check.Status == executor.StatusExited && in.Check.ExitCode != nil {
		exit = *in.Check.ExitCode
	}
	// Every selected rule is an error: exit 1 iff an error-severity message
	// (fatal parse errors included), exit 0 iff none; 2 is a crash or a
	// configuration error.
	consistent := (exit == 0 && errorsN == 0) || (exit == 1 && errorsN > 0)
	processed := []string{}
	skipped := 0
	if parsed {
		for _, f := range in.Expected {
			s, ok := states[f]
			if !ok {
				s = FileNotLinted
				doc.Coverage.FilesUnlisted++
			}
			doc.Coverage.Units[s]++
			switch s {
			case FileClean, FileWithFindings:
				processed = append(processed, f)
			case FileOutsideTSConfig:
				skipped++
			}
		}
	} else if in.Check != nil {
		doc.Coverage.Units[FileUnknown] = len(in.Expected)
	}
	doc.Coverage.FilesSkipped = skipped
	if skipped > 0 {
		addLim(&limitations, "files_outside_tsconfig_program")
	}
	if doc.Coverage.FilesUnlisted > 0 {
		addLim(&limitations, "unlisted_files")
	}
	doc.Coverage.ProcessedPaths = processed

	results := []*executor.Result{in.Version, in.Check}
	status, reason, class := domain.TaskSucceeded, "", ""
	fail := func(s domain.TaskStatus, r, c string) {
		if reason == "" {
			status, reason, class = s, r, c
		}
	}
	for _, r := range results {
		if r != nil && r.Cleanup == executor.CleanupUncertain {
			fail(domain.TaskFailed, ReasonCleanupUncertain, obsdoc.ExitExecutionError)
		}
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusCancelled {
			fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
		}
	}
	if in.Cancelled {
		fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
	}
	for _, r := range results {
		if r != nil && r.Status == executor.StatusTimedOut {
			fail(domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout)
		}
	}
	for _, r := range results {
		if r == nil {
			continue
		}
		switch r.Status {
		case executor.StatusUnsupported:
			fail(domain.TaskUnsupported, ReasonExecutionUnsupported, obsdoc.ExitNotRun)
		case executor.StatusStartFailed:
			fail(domain.TaskFailed, ReasonToolStartFailed, obsdoc.ExitNotRun)
		case executor.StatusRejected:
			fail(domain.TaskFailed, ReasonExecutionRejected, obsdoc.ExitNotRun)
		}
	}
	if in.Version != nil && !toolOK {
		fail(domain.TaskFailed, ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	if in.Version == nil || in.Check == nil {
		fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
	}
	if len(doc.WorkspaceDrift) > 0 {
		fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	if in.Check != nil && !pack.StreamsComplete(in.Check) {
		fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
	}
	switch {
	case in.Check == nil:
	case crash || (exit != 0 && exit != 1):
		fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
	case !parsed:
		fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	case !consistent:
		fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
	}
	if suppressedActive {
		// --no-inline-config and the empty suppressions file must leave
		// nothing suppressed; a suppressed message means they did not hold.
		fail(domain.TaskFailed, ReasonSuppressionsActive, obsdoc.ExitExecutionError)
	}
	if config {
		fail(domain.TaskFailed, ReasonConfig, obsdoc.ExitConfigurationError)
	}
	if gap {
		fail(domain.TaskFailed, ReasonDependency, obsdoc.ExitConfigurationError)
	}
	if parseErr {
		fail(domain.TaskFailed, ReasonParseErrors, obsdoc.ExitExecutionError)
	}
	if in.Check != nil && parsed && len(processed) == 0 {
		// Nothing was linted (every file outside the program, or no file
		// matched): never a clean result.
		fail(domain.TaskUnsupported, ReasonNoFiles, obsdoc.ExitConfigurationError)
	}
	partial := false
	if reason == "" {
		class = obsdoc.ExitSuccessWithoutFindings
		if doc.Active() > 0 {
			class = obsdoc.ExitSuccessWithFindings
		}
	} else {
		partial = in.Check != nil && (st.Values > 0 || st.UnparsedBytes > 0 || in.Check.Stdout.Truncated || in.Check.Stdout.Incomplete)
	}
	switch {
	case class == obsdoc.ExitNotRun:
		doc.Observations, partial = []obsdoc.Observation{}, false
		doc.Coverage.ProcessedPaths = []string{}
	case reason != "" && status != domain.TaskUnsupported:
		// A failed run never claims files as processed.
		doc.Coverage.ProcessedPaths = nil
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: status, ExitClass: class, PartialParse: partial}
	if reason != "" {
		doc.Outcome.Reason = ptr(reason)
	}
	doc.Parse.OK = reason == "" && parsed
	doc.Limitations = limitations
	return doc
}
