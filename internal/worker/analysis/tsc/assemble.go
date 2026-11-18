package tsc

import (
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/jsts"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
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
	ReasonNotRun               = "tsc_not_run"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonConfigOutside        = jsts.ReasonConfigOutside
	ReasonWriteOption          = jsts.ReasonWriteOption
	ReasonForeignNodeModules   = "node_modules_outside_snapshot"
	ReasonFilesOutside         = "program_files_outside_snapshot"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonFailed               = "tsc_failed"
	ReasonExitMismatch         = "exit_status_mismatch"
	ReasonConfig               = "tsconfig_invalid"
	ReasonExtendsUnresolved    = "tsconfig_extends_unresolved"
	ReasonDependency           = "dependency_unavailable"
	ReasonSyntax               = "syntax_errors"
	ReasonNoInputs             = "no_input_files"
)

// File (unit) states.
const (
	FileChecked     = "checked"
	FileNotVerified = "not_verified"
	FileGap         = "dependency_gap"
	// FileOutsideProgram is a module JS/TS file that is no root file of the
	// tsconfig program.
	FileOutsideProgram = "outside_tsconfig_program"
)

type assembleInput struct {
	TaskKey, ManifestDigest string
	Module                  jsts.Module
	Files                   map[string]source.Entry
	NodeVersion             string
	ModuleName              string
	Chains                  []jsts.ConfigChain
	Version                 *executor.Result
	Projects                []project
	Solution                bool
	ForeignReferences       int
	ReferencesDropped       int
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

// category is the codeye-ts-tsc v1 policy: type errors are correctness;
// unused-code and implicit-any families are maintainability.
func category(code int) string {
	switch {
	case code == 2578, code == 7027, code == 7028, code >= 6133 && code <= 6205 && code != 6305:
		return "maintainability"
	case code >= 7005 && code <= 7053:
		return "maintainability"
	}
	return "correctness"
}

func isConfigFile(p string) bool { return strings.HasSuffix(strings.ToLower(p), ".json") }

func assemble(in assembleInput) obsdoc.Document {
	mask := in.Mask
	if mask == nil {
		mask = func(s string) string { return s }
	}
	doc := obsdoc.New(obsdoc.Analyzer{ID: AnalyzerID, Ecosystem: jsts.Ecosystem, Required: false}, obsdoc.Component{ID: Name, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = in.Module.Dir
	doc.Patterns = []string{"tsconfig.json"}
	doc.Tool.Name = Tool
	// tsc honors the repository's tsconfig chain (read as data, bound to the
	// manifest); @ts-ignore, @ts-expect-error and @ts-nocheck hide
	// diagnostics and tsc cannot report what they hid (honored).
	var cfgs [][]obsdoc.ConfigFile
	enumerated := len(in.Chains) > 0
	for _, c := range in.Chains {
		cfgs = append(cfgs, c.Files)
		if c.Unreadable || len(c.Packages) > 0 || c.Outside {
			enumerated = false
		}
	}
	doc.RulePack = obsdoc.RulePack{ID: RulePackID, Version: RulePackVersion, Selection: append([]string{}, Selection...), RepositoryConfig: obsdoc.RepoConfigHonored,
		SourceSuppressions: obsdoc.SuppressHonored, UnreportedSuppressionRules: []string{}}
	if enumerated {
		doc.RulePack.ConfigFiles = jsts.MergeConfigFiles(cfgs...)
	}
	doc.Module.Name = in.ModuleName
	doc.Target.Properties = map[string]string{"node": in.NodeVersion, "tsconfig": "tsconfig.json"}
	doc.Coverage.Unit = "file"
	limitations := []string{"type_check_without_dependencies"}
	if in.Module.Implicit {
		limitations = append(limitations, "no_package_json_module")
	}
	for _, c := range in.Chains {
		if len(c.Packages) > 0 {
			limitations = append(limitations, "tsconfig_extends_package")
		}
	}
	for _, e := range in.Executions {
		doc.Executions = append(doc.Executions, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	notRun := func(status domain.TaskStatus, reason string) obsdoc.Document {
		doc.Outcome = obsdoc.Outcome{TaskStatus: status, Reason: ptr(reason), ExitClass: obsdoc.ExitNotRun}
		doc.Coverage.ProcessedPaths = []string{}
		doc.Limitations = append(append(limitations, reason), jsts.OptionLimitations(in.PolicyOptions)...)
		return doc
	}
	if in.Integrity {
		return notRun(domain.TaskFailed, ReasonSnapshotIntegrity)
	}
	if in.Policy != "" {
		return notRun(domain.TaskSkippedPolicy, in.Policy)
	}
	for p := range in.Drift {
		doc.WorkspaceDrift = append(doc.WorkspaceDrift, p)
	}
	sort.Strings(doc.WorkspaceDrift)
	if len(doc.WorkspaceDrift) > 0 {
		limitations = append(limitations, "workspace_drift")
	}
	toolOK := false
	if in.Version != nil && jsts.ExitedZero(in.Version) {
		if v, ok := ParseVersion(in.Version.Stdout.Data); ok {
			toolOK = true
			doc.Tool.Version, doc.Tool.VersionTested = ptr(v), VersionTested(v)
			if !doc.Tool.VersionTested {
				limitations = append(limitations, "tool_version_untested")
			}
		}
	}
	if in.Solution {
		limitations = append(limitations, "solution_config_references_checked")
	}
	if in.ForeignReferences > 0 {
		limitations = append(limitations, "references_outside_module")
	}
	if in.ReferencesDropped > 0 {
		limitations = append(limitations, "references_not_checked")
	}

	// Configuration (showConfig) and check outputs.
	var all []*executor.Result
	all = append(all, in.Version)
	parsedAll, truncated, configFailed, mismatch, cliFailed := true, false, false, false, false
	var gapFiles = map[string]bool{}
	var cands []obsdoc.Observation
	var withheld, syntax []obsdoc.ToolError
	gap, config, extends, noInputs, nonSyntax, outsideFiles := false, false, false, false, false, 0
	records, unparsed, shape := 0, int64(0), 0
	roots := map[string]bool{}
	checks := 0
	ordinals := map[string]int{}
	moduleOwned := func(rel string) bool { return in.Module.Owns(rel) && jsts.IsSource(rel) }
	ownFiles := map[string]bool{}
	for _, f := range in.Module.Files {
		ownFiles[f] = true
	}
	toolError := func(te obsdoc.ToolError) {
		if len(doc.ToolErrors) < 200 {
			doc.ToolErrors = append(doc.ToolErrors, te)
		} else {
			limitations = append(limitations, "tool_errors_truncated")
		}
	}
	read := func(exec string, r *executor.Result) {
		if r == nil {
			return
		}
		all = append(all, r)
		if !jsts.StreamsComplete(r) {
			truncated = true
		}
		st := parseDiagnostics(r.Stdout.Data)
		records += len(st.Records)
		unparsed += st.UnparsedBytes
		shape += st.ShapeErrors
		if st.UnparsedBytes > 0 || st.ShapeErrors > 0 {
			parsedAll = false
		}
		exit := -1
		if r.Status == executor.StatusExited && r.ExitCode != nil {
			exit = *r.ExitCode
		}
		switch {
		case exit == 1:
			// Command-line errors (unknown flag) exit 1.
			cliFailed = true
		case exit != 0 && exit != 2 && r.Status == executor.StatusExited:
			cliFailed = true
		case (exit == 0) != (len(st.Records) == 0) && r.Status == executor.StatusExited:
			mismatch = true
		}
		sha := obsdoc.SHA256Hex(r.Stdout.Data)
		for _, rec := range st.Records {
			// No host path is quoted: masked roots, then any quoted
			// absolute path left; a config file outside the workspace is
			// never echoed (docs/43 §3.2).
			text := jsts.WithholdHostPaths(mask(rec.Text))
			if rec.File == "" || isConfigFile(rec.File) {
				if rec.File != "" {
					if _, ok := jsts.StripWorkspace(rec.File, in.Module.Dir, in.Roots); !ok {
						text = "diagnostic in a configuration file outside the workspace (text withheld)"
						outsideFiles++
					}
				}
				te := obsdoc.ToolError{Execution: exec, Kind: "config", Unit: "program", Message: "TS" + strconv.Itoa(rec.Code) + ": " + text}
				switch {
				case jsts.IsGap(rec.Code):
					gap, te.Kind = true, "dependency"
				case rec.Code == 18003:
					noInputs = true
				case rec.Code == 6053:
					extends, config = true, true
				default:
					config = true
				}
				toolError(te)
				continue
			}
			rel, ok := jsts.StripWorkspace(rec.File, in.Module.Dir, in.Roots)
			if !ok || rel == "." {
				outsideFiles++
				continue
			}
			if jsts.IsGap(rec.Code) {
				gap = true
				gapFiles[rel] = true
				toolError(obsdoc.ToolError{Execution: exec, Kind: "dependency", Unit: rel, Message: "TS" + strconv.Itoa(rec.Code) + ": " + text, Path: ptr(rel), Line: rec.Line, Column: rec.Col})
				continue
			}
			te := obsdoc.ToolError{Execution: exec, Kind: "withheld", Unit: rel, Message: "TS" + strconv.Itoa(rec.Code) + ": " + text, Path: ptr(rel), Line: rec.Line, Column: rec.Col}
			if rec.Code >= 1000 && rec.Code < 2000 {
				te.Kind = "syntax"
				syntax = append(syntax, te)
			} else {
				nonSyntax = true
			}
			withheld = append(withheld, te)
			if !moduleOwned(rel) || !ownFiles[rel] {
				limitations = append(limitations, "diagnostics_outside_module")
				continue
			}
			rule := "TS" + strconv.Itoa(rec.Code)
			pathPtr, loc, lstate := in.Resolver.Resolve(rec.File, rec.Line, rec.Col, 0, 0)
			pos := mask(rel + ":" + strconv.Itoa(rec.Line) + ":" + strconv.Itoa(rec.Col))
			tuple := strings.Join([]string{rel, rule, pos, text}, "\x00")
			ordinal := ordinals[tuple]
			ordinals[tuple]++
			o := obsdoc.Observation{ID: obsdoc.ObservationID(AnalyzerID, in.Module.Dir, rel, rule, pos, pos, text, ordinal), RuleID: rule, Category: category(rec.Code),
				RawSeverity: ptr(rec.Category), RawMessage: text, Path: pathPtr, Location: loc, LocationState: lstate, Related: []obsdoc.Related{},
				RawRef: obsdoc.RawRef{Execution: exec, Stream: "stdout", StreamSHA256: sha, RecordIndex: rec.Index, ByteStart: rec.ByteStart, ByteEnd: rec.ByteEnd}, Unit: rel}
			if o.RawMessage == "" {
				o.RawMessage = "(empty message)"
			}
			cands = append(cands, o)
		}
	}
	for _, p := range in.Projects {
		suffix := ""
		if p.Ref > 0 {
			suffix = ".r" + pad2(p.Ref)
		}
		if p.ConfigRun != nil {
			if jsts.ExitedZero(p.ConfigRun) {
				all = append(all, p.ConfigRun)
				if !jsts.StreamsComplete(p.ConfigRun) {
					truncated = true
				}
				if _, ok := parseShowConfig(p.ConfigRun.Stdout.Data); !ok {
					parsedAll = false
				}
			} else {
				configFailed = true
				read(execConfig+suffix, p.ConfigRun)
			}
		}
		if p.CheckRun != nil {
			checks++
			read(execCheck+suffix, p.CheckRun)
			files, outside := p.rootFiles()
			outsideFiles += outside
			for _, f := range files {
				if strings.HasPrefix(f, analysis.RedactedMarker) {
					if r, ok := jsts.StripWorkspace(f, "", in.Roots); ok {
						f = r
					}
				}
				roots[f] = true
			}
			if checks == 1 {
				for k, v := range p.Shown.Options {
					if obsdoc.SafeText(v, 64) && !strings.ContainsAny(v, "/\\\n") {
						doc.Target.Properties[k] = v
					}
				}
			}
		}
	}
	doc.Parse.Records, doc.Parse.UnparsedBytes, doc.Parse.ShapeErrors = records, unparsed, shape
	syntaxOnly := len(syntax) > 0 && !nonSyntax && !gap && !config
	// The program's own files that tsc took as roots and the module owns.
	var processed []string
	for f := range roots {
		if _, inManifest := in.Files[f]; inManifest && ownFiles[f] {
			processed = append(processed, f)
		}
	}
	sort.Strings(processed)
	if outsideFiles > 0 {
		limitations = append(limitations, "program_files_outside_snapshot")
	}
	switch {
	case gap || config || syntaxOnly:
		// Withhold: with a dependency gap the program's types are
		// incomplete and any type error may follow from that alone
		// (measured on the CODEYE web sources without node_modules: every
		// TS2322 there vanished with the dependencies present); with a
		// configuration error the check ran under options tsc did not load;
		// syntax errors stop tsc before type-checking.
		if gap || config {
			limitations = append(limitations, "diagnostics_withheld_incomplete_program")
			for _, te := range withheld {
				toolError(te)
			}
		} else {
			for _, te := range syntax {
				toolError(te)
			}
		}
		cands = nil
	}
	doc.Observations = append(doc.Observations, cands...)
	for _, o := range doc.Observations {
		if o.LocationState != obsdoc.LocationResolved {
			limitations = append(limitations, "unresolved_observation_location")
			break
		}
	}

	status, reason, class := domain.TaskSucceeded, "", ""
	fail := func(s domain.TaskStatus, r, c string) {
		if reason == "" {
			status, reason, class = s, r, c
		}
	}
	for _, r := range all {
		if r != nil && r.Cleanup == executor.CleanupUncertain {
			fail(domain.TaskFailed, ReasonCleanupUncertain, obsdoc.ExitExecutionError)
		}
	}
	for _, r := range all {
		if r != nil && r.Status == executor.StatusCancelled {
			fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
		}
	}
	if in.Cancelled {
		fail(domain.TaskCancelled, ReasonCancelled, obsdoc.ExitCancelled)
	}
	for _, r := range all {
		if r != nil && r.Status == executor.StatusTimedOut {
			fail(domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout)
		}
	}
	for _, r := range all {
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
	if in.Version == nil || len(in.Projects) == 0 || (!configFailed && checks == 0 && !(in.Solution && len(in.Projects) == 1)) {
		fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
	}
	if len(doc.WorkspaceDrift) > 0 {
		fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	if truncated {
		fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
	}
	if !parsedAll {
		fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	if outsideFiles > 0 {
		fail(domain.TaskSkippedPolicy, ReasonFilesOutside, obsdoc.ExitConfigurationError)
	}
	switch {
	case noInputs && !gap && !config:
		fail(domain.TaskUnsupported, ReasonNoInputs, obsdoc.ExitConfigurationError)
	case extends:
		fail(domain.TaskFailed, ReasonExtendsUnresolved, obsdoc.ExitConfigurationError)
	case config || configFailed && !gap:
		fail(domain.TaskFailed, ReasonConfig, obsdoc.ExitConfigurationError)
	case gap:
		fail(domain.TaskFailed, ReasonDependency, obsdoc.ExitConfigurationError)
	}
	if cliFailed {
		fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
	}
	if mismatch {
		fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
	}
	if syntaxOnly {
		fail(domain.TaskFailed, ReasonSyntax, obsdoc.ExitExecutionError)
	}
	if len(processed) == 0 {
		// A solution config with no local project, or a tsconfig that
		// selects none of this module's files: tsc checked nothing here.
		fail(domain.TaskUnsupported, ReasonNoInputs, obsdoc.ExitConfigurationError)
	}
	// Units: the module's root files, checked or not verified.
	for _, f := range processed {
		switch {
		case gapFiles[f]:
			doc.Coverage.Units[FileGap]++
		case reason == "":
			doc.Coverage.Units[FileChecked]++
		default:
			doc.Coverage.Units[FileNotVerified]++
		}
	}
	partial := false
	if reason == "" {
		class = obsdoc.ExitSuccessWithoutFindings
		if doc.Active() > 0 {
			class = obsdoc.ExitSuccessWithFindings
		}
	} else {
		partial = records > 0 || unparsed > 0
	}
	switch {
	case class == obsdoc.ExitNotRun:
		doc.Observations, partial = []obsdoc.Observation{}, false
		doc.Coverage.ProcessedPaths = []string{}
	case reason != "" && status != domain.TaskUnsupported:
		doc.Coverage.ProcessedPaths = nil
	case reason != "":
		doc.Coverage.ProcessedPaths = []string{}
	default:
		doc.Coverage.ProcessedPaths = processed
	}
	// Once a configuration was shown and checked, the module's JS/TS files
	// are in scope; those that are not root files of the program (outside
	// include, or JavaScript without allowJs) are skipped, never claimed.
	if checks > 0 {
		root := map[string]bool{}
		for _, f := range processed {
			root[f] = true
		}
		skipped := 0
		for _, f := range in.Module.Files {
			if !root[f] {
				skipped++
			}
		}
		doc.Coverage.FilesInScope = ptr(len(in.Module.Files))
		doc.Coverage.FilesSkipped = skipped
		if skipped > 0 {
			doc.Coverage.Units[FileOutsideProgram] += skipped
			limitations = append(limitations, "files_outside_tsconfig_program")
		}
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: status, ExitClass: class, PartialParse: partial}
	if reason != "" {
		doc.Outcome.Reason = ptr(reason)
	}
	doc.Parse.OK = reason == "" && parsedAll
	doc.Limitations = limitations
	return doc
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
