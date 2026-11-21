package staticcheck

import (
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task failure reasons (TaskResult.Reason and outcome.reason).
const (
	ReasonCleanupUncertain     = pack.ReasonCleanupUncertain
	ReasonCancelled            = pack.ReasonCancelled
	ReasonTimeout              = pack.ReasonTimeout
	ReasonExecutionUnsupported = pack.ReasonExecutionUnsupported
	ReasonToolStartFailed      = pack.ReasonToolStartFailed
	ReasonExecutionRejected    = pack.ReasonExecutionRejected
	ReasonToolVersion          = "tool_version_unrecognized"
	ReasonGoModUnreadable      = "go_mod_unreadable"
	ReasonReplaceOutside       = "replace_outside_snapshot"
	ReasonSnapshotIntegrity    = "snapshot_integrity_failed"
	ReasonNotRun               = "staticcheck_not_run"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonToolchain            = "toolchain_unavailable"
	ReasonModuleMissing        = "go_module_missing"
	ReasonDependency           = "dependency_unavailable"
	ReasonNoPackages           = "no_analyzable_packages"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonPackageErrors        = "package_errors"
	ReasonExitMismatch         = "exit_status_mismatch"
	ReasonFailed               = "staticcheck_failed"
	ReasonPackageLoad          = "package_load_error"
)

// Package (unit) states, the go vet vocabulary.
const (
	PackageAnalyzedClean         = "analyzed_clean"
	PackageAnalyzedWithFindings  = "analyzed_with_findings"
	PackageFailedTypecheck       = "failed_typecheck"
	PackageDependencyUnavailable = "dependency_unavailable"
	PackageLoadError             = "load_error"
	PackageNotAnalyzed           = "not_analyzed"
	PackageUnknown               = "unknown"
)

// assembleInput is everything the pure document builder needs. Results are
// nil for executions that were not run.
type assembleInput struct {
	TaskKey, ManifestDigest, ModuleDir string
	Patterns                           []string
	Files                              map[string]source.Entry
	ScopeGoFiles                       []string
	Lines                              func(rel string) (lines int, sha string, ok bool)
	Drift                              map[string]bool
	GoVersion, Version, ModEdit, List  *executor.Result
	Check                              *executor.Result
	Executions                         []analysis.Execution
	Mask                               func(string) string
	Cancelled, Integrity               bool
	ExtraLimitations                   []string
	WorkspaceRoots                     []string
}

func ptr[T any](v T) *T { return &v }

func replaceAll(s, old, repl string) string {
	if old == "" {
		return s
	}
	return strings.ReplaceAll(s, old, repl)
}

// category is the policy codeye-go-staticcheck v1: check family → report
// category. It describes what the check is about, not its impact.
func category(code string) string {
	switch {
	case code == "SA1019" || code == "U1000" || strings.HasPrefix(code, "S1") || strings.HasPrefix(code, "ST"):
		return "maintainability"
	case strings.HasPrefix(code, "SA2"):
		return "concurrency"
	case strings.HasPrefix(code, "SA3"):
		return "test_gap"
	case strings.HasPrefix(code, "SA6"):
		return "performance"
	}
	return "correctness"
}

type pkgState struct {
	ImportPath, Dir string
	Files, Ignored  []string
	State           string
}

type resolver struct {
	in    assembleInput
	cache map[string][3]any
}

func (rs *resolver) lines(rel string) (int, string, bool) {
	if v, ok := rs.cache[rel]; ok {
		return v[0].(int), v[1].(string), v[2].(bool)
	}
	n, sha, ok := 0, "", false
	if rs.in.Lines != nil {
		n, sha, ok = rs.in.Lines(rel)
	}
	rs.cache[rel] = [3]any{n, sha, ok}
	return n, sha, ok
}

// resolve maps a staticcheck position (absolute workspace file, redacted, or
// module-relative) to a snapshot location.
func (rs *resolver) resolve(start, end pos) (*string, *obsdoc.Location, string) {
	if start.File == "" || start.Line < 1 {
		return nil, nil, obsdoc.LocationNoPosition
	}
	rel, ok := govet.StripWorkspace(start.File, rs.in.ModuleDir, rs.in.WorkspaceRoots, true)
	if !ok || rel == "." {
		return nil, nil, obsdoc.LocationOutsideSnapshot
	}
	relp := rel
	entry, ok := rs.in.Files[rel]
	if !ok {
		return &relp, nil, obsdoc.LocationMissingFile
	}
	if rs.in.Drift[rel] {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	n, sha, ok := rs.lines(rel)
	if !ok || sha != entry.SHA256 {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	endLine, endCol := start.Line, start.Col
	if end.File != "" && end.Line >= 1 {
		if erel, eok := govet.StripWorkspace(end.File, rs.in.ModuleDir, rs.in.WorkspaceRoots, true); eok && erel == rel {
			endLine, endCol = end.Line, end.Col
		}
	}
	maxLine := max(n, 1)
	if start.Line > maxLine || endLine < start.Line || endLine > maxLine || (endLine == start.Line && endCol < start.Col) {
		return &relp, nil, obsdoc.LocationLineOutOfRange
	}
	return &relp, &obsdoc.Location{Path: rel, StartLine: start.Line, StartCol: start.Col, EndLine: endLine, EndCol: endCol, ColumnUnit: "byte", BlobSHA256: entry.SHA256}, obsdoc.LocationResolved
}

func posText(p pos) string { return p.File + ":" + strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Col) }

// assemble builds the envelope and decides the task outcome. Success is one
// explicit conjunction; everything else keeps what was parsed.
func assemble(in assembleInput) obsdoc.Document {
	mask := in.Mask
	if mask == nil {
		mask = func(s string) string { return s }
	}
	doc := obsdoc.New(obsdoc.Analyzer{ID: AnalyzerID, Ecosystem: "go", Required: false}, obsdoc.Component{ID: Name, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = in.ModuleDir
	doc.Patterns = append([]string{}, in.Patterns...)
	doc.Tool.Name = Tool
	// -checks replaces the repository's staticcheck.conf checks (overridden;
	// no other conf option applies to SA*/U1000), and -show-ignored keeps
	// the diagnostics //lint:ignore hides (reported), except U1000's.
	doc.RulePack = obsdoc.RulePack{ID: RulePackID, Version: RulePackVersion, Selection: append([]string{}, Selection...),
		RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{},
		UnreportedSuppressionRules: append([]string{}, UnreportedSuppressionRules...)}
	doc.Target.Properties = map[string]string{"cgo_enabled": "0", "build_tags": "", "go_flags": "", "tests": "true", "go_version_target": "module"}
	doc.Coverage.Unit = "package"
	doc.Coverage.FilesInScope = ptr(len(in.ScopeGoFiles))
	limitations := append([]string{"default_build_context_only", "cgo_disabled", "go_vet_overlap_checks_excluded"}, in.ExtraLimitations...)
	for _, e := range in.Executions {
		doc.Executions = append(doc.Executions, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	if in.Integrity {
		doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskFailed, Reason: ptr(ReasonSnapshotIntegrity), ExitClass: obsdoc.ExitNotRun}
		doc.Coverage.ProcessedPaths = []string{}
		doc.Limitations = limitations
		return doc
	}
	for p := range in.Drift {
		doc.WorkspaceDrift = append(doc.WorkspaceDrift, p)
	}
	sort.Strings(doc.WorkspaceDrift)
	if len(doc.WorkspaceDrift) > 0 {
		limitations = append(limitations, "workspace_drift")
	}

	goOK, toolOK := false, false
	if in.GoVersion != nil && pack.ExitedZero(in.GoVersion) {
		if v, platform, ok := govet.ParseGoVersion(in.GoVersion.Stdout.Data); ok {
			goOK = true
			doc.Target.Platform = ptr(platform)
			doc.Target.Properties["toolchain"] = v
			if goos, goarch, ok := strings.Cut(platform, "/"); ok {
				doc.Target.Properties["goos"], doc.Target.Properties["goarch"] = goos, goarch
			}
			if !govet.GoVersionTested(v) {
				limitations = append(limitations, "go_version_untested")
			}
		}
	}
	if in.Version != nil && pack.ExitedZero(in.Version) {
		if v, ok := ParseVersion(in.Version.Stdout.Data); ok {
			toolOK = true
			doc.Tool.Version, doc.Tool.VersionTested = ptr(v), VersionTested(v)
			if !doc.Tool.VersionTested {
				limitations = append(limitations, "tool_version_untested")
			}
		}
	}
	modOK := false
	var escapes []string
	if in.ModEdit != nil && pack.ExitedZero(in.ModEdit) && pack.StreamsComplete(in.ModEdit) {
		if modPath, targets, ok := govet.ParseModEdit(in.ModEdit.Stdout.Data); ok {
			modOK = true
			doc.Module.Name = modPath
			escapes = govet.EscapingReplaces(targets, in.ModuleDir)
		}
	}
	if len(escapes) > 0 {
		limitations = append(limitations, "local_replace_outside_snapshot")
	}

	// go list inventory: the packages staticcheck loads for the same patterns
	// and build context.
	toolchain, moduleMissing, dependency, noPackages := false, false, false, false
	classify := func(text string) string {
		switch govet.GoErrorKind(text) {
		case "toolchain":
			toolchain = true
			return "toolchain"
		case "module_missing":
			moduleMissing = true
			return "module_missing"
		case "dependency":
			dependency = true
			return "dependency"
		}
		return ""
	}
	listOK := false
	var pkgs []*pkgState
	byImport := map[string]*pkgState{}
	byFile := map[string]*pkgState{}
	byDir := map[string]*pkgState{}
	listed := map[string]bool{}
	if in.List != nil {
		packages, unparsed, _ := govet.ParseList(in.List.Stdout.Data)
		listOK = pack.ExitedZero(in.List) && pack.StreamsComplete(in.List) && unparsed == 0
		for _, p := range packages {
			ps := &pkgState{ImportPath: p.ImportPath}
			if dir, ok := govet.StripWorkspace(p.Dir, in.ModuleDir, in.WorkspaceRoots, false); ok && p.Dir != "" {
				ps.Dir = dir
			}
			for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.TestGoFiles, p.XTestGoFiles} {
				for _, f := range group {
					rel := govet.JoinRel(ps.Dir, f)
					ps.Files = append(ps.Files, rel)
					listed[rel] = true
					byFile[rel] = ps
				}
			}
			for _, f := range p.IgnoredGoFiles {
				rel := govet.JoinRel(ps.Dir, f)
				ps.Ignored = append(ps.Ignored, rel)
				listed[rel] = true
			}
			errs := []string{}
			if p.Error != nil {
				errs = append(errs, p.Error.Err)
			}
			for _, e := range p.DepsErrors {
				if e != nil {
					errs = append(errs, e.Err)
				}
			}
			for i, e := range errs {
				kind := classify(e)
				switch {
				case kind == "dependency":
					ps.State = PackageDependencyUnavailable
				case ps.State == "":
					ps.State = PackageLoadError
				}
				if kind == "" {
					kind = "load"
				}
				if i < 20 && len(doc.ToolErrors) < 200 {
					doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: execList, Kind: kind, Unit: p.ImportPath, Message: mask(e)})
				}
			}
			if p.Incomplete && ps.State == "" {
				ps.State = PackageLoadError
			}
			if _, dup := byImport[p.ImportPath]; !dup {
				byImport[p.ImportPath] = ps
			}
			if ps.Dir != "" {
				byDir[ps.Dir] = ps
			}
			pkgs = append(pkgs, ps)
		}
		for _, l := range strings.Split(string(in.List.Stderr.Data), "\n") {
			switch {
			case strings.Contains(l, "matched no packages"):
				noPackages = true
			case strings.TrimSpace(l) != "":
				if kind := classify(l); kind != "" && len(doc.ToolErrors) < 200 {
					doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: execList, Kind: kind, Message: mask(strings.TrimSpace(l))})
				}
			}
		}
	}

	// staticcheck stdout (records) and stderr.
	var st stream
	parsed := false
	stdoutSHA := ""
	fatal := false
	if in.Check != nil {
		st = parseStream(in.Check.Stdout.Data)
		parsed = st.UnparsedBytes == 0 && st.ShapeErrors == 0 && st.DecodeError == ""
		stdoutSHA = obsdoc.SHA256Hex(in.Check.Stdout.Data)
		lines, dropped := parseStderr(in.Check.Stderr.Data)
		if dropped > 0 {
			limitations = append(limitations, "tool_stderr_lines_dropped")
		}
		for _, l := range lines {
			switch l.Kind {
			case "no_packages":
				noPackages = true
			case "fatal":
				fatal = true
				kind := classify(l.Message)
				if kind == "" {
					kind = "fatal"
				}
				doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: execCheck, Kind: kind, Message: mask(l.Message)})
			default:
				limitations = append(limitations, "tool_stderr_output")
			}
		}
	}
	doc.Parse.Records, doc.Parse.UnparsedBytes, doc.Parse.ShapeErrors, doc.Parse.DecodeError = st.Values, st.UnparsedBytes, st.ShapeErrors, st.DecodeError

	pkgOf := func(rel string) *pkgState {
		if p := byFile[rel]; p != nil {
			return p
		}
		return byDir[pack.DirOf(rel)]
	}
	rs := &resolver{in: in, cache: map[string][3]any{}}
	withFindings := map[*pkgState]bool{}
	compile, suppressed := false, false
	ordinals := map[string]int{}
	for _, r := range st.Records {
		msg := mask(r.Message)
		if r.Code == "compile" {
			// A package the tool could not load or type-check: a tool
			// error, never an observation of the code.
			compile = true
			te := obsdoc.ToolError{Execution: execCheck, Kind: classify(r.Message), Unit: compileUnit(r.Message), Message: msg}
			file, line, col := r.Start.File, r.Start.Line, r.Start.Col
			if file == "" {
				if m := compilePos.FindStringSubmatch(r.Message); m != nil {
					file = m[1]
					line, _ = strconv.Atoi(m[2])
					col, _ = strconv.Atoi(m[3])
				}
			}
			if file != "" {
				if rel, ok := govet.StripWorkspace(file, in.ModuleDir, in.WorkspaceRoots, true); ok && rel != "." {
					te.Path, te.Line, te.Column = ptr(rel), line, col
				}
			}
			var p *pkgState
			if te.Unit != "" {
				p = byImport[te.Unit]
			} else if te.Path != nil {
				p = pkgOf(*te.Path)
				if p != nil {
					te.Unit = p.ImportPath
				}
			}
			if te.Kind == "" {
				te.Kind = "typecheck"
			}
			if p != nil {
				if te.Kind == "dependency" {
					p.State = PackageDependencyUnavailable
				} else if p.State == "" || p.State == PackageUnknown {
					p.State = PackageFailedTypecheck
				}
			}
			if len(doc.ToolErrors) < 200 {
				doc.ToolErrors = append(doc.ToolErrors, te)
			}
			continue
		}
		pathPtr, loc, state := rs.resolve(r.Start, r.End)
		unit := ""
		var owner *pkgState
		if pathPtr != nil {
			if owner = pkgOf(*pathPtr); owner != nil {
				unit = owner.ImportPath
			}
		}
		normStart, normEnd := mask(posText(r.Start)), mask(posText(r.End))
		tuple := strings.Join([]string{unit, r.Code, normStart, normEnd, msg}, "\x00")
		ordinal := ordinals[tuple]
		ordinals[tuple]++
		o := obsdoc.Observation{ID: obsdoc.ObservationID(AnalyzerID, in.ModuleDir, unit, r.Code, normStart, normEnd, msg, ordinal), RuleID: r.Code, Category: category(r.Code),
			RawSeverity: ptr(r.Severity), RawMessage: msg, Path: pathPtr, Location: loc, LocationState: state, Related: []obsdoc.Related{},
			RawRef: obsdoc.RawRef{Execution: execCheck, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: r.Index, ByteStart: r.ByteStart, ByteEnd: r.ByteEnd}, Unit: unit,
			// -show-ignored marks a problem hidden by //lint:ignore or
			// //lint:file-ignore with severity "ignored".
			Suppressed: r.Severity == severityIgnored}
		if o.RawMessage == "" {
			o.RawMessage = "(empty message)"
		}
		for _, rel := range r.Related {
			rp, rl, rst := rs.resolve(rel.Start, rel.End)
			o.Related = append(o.Related, obsdoc.Related{RawMessage: mask(rel.Message), Path: rp, Location: rl, LocationState: rst})
		}
		doc.Observations = append(doc.Observations, o)
		if o.Suppressed {
			suppressed = true
		}
		if owner != nil {
			if !o.Suppressed {
				withFindings[owner] = true
			}
		} else {
			limitations = append(limitations, "unattributed_observation_unit")
		}
	}

	exit := -1
	if in.Check != nil && in.Check.Status == executor.StatusExited && in.Check.ExitCode != nil {
		exit = *in.Check.ExitCode
	}
	// Complete: the exit status agrees with the records (-fail=all: exit 1
	// iff a problem was reported), streams are whole and every record parsed.
	consistent := (exit == 0 && len(st.Records) == 0) || (exit == 1 && len(st.Records) > 0)
	checkComplete := in.Check != nil && consistent && pack.StreamsComplete(in.Check) && parsed && !fatal && !compile
	for _, p := range pkgs {
		if p.State != "" {
			continue
		}
		switch {
		case withFindings[p]:
			p.State = PackageAnalyzedWithFindings
		case checkComplete && listOK:
			p.State = PackageAnalyzedClean
		default:
			p.State = PackageUnknown
		}
	}
	unlisted := map[string]bool{}
	if listOK {
		for _, f := range in.ScopeGoFiles {
			if !listed[f] {
				unlisted[pack.DirOf(f)] = true
				doc.Coverage.FilesUnlisted++
			}
		}
	}
	if doc.Coverage.FilesUnlisted > 0 {
		limitations = append(limitations, "unlisted_go_files")
		doc.Coverage.Units[PackageNotAnalyzed] += len(unlisted)
	}
	processed := []string{}
	ignored := map[string]bool{}
	anyUnknown := !listOK
	for _, p := range pkgs {
		doc.Coverage.Units[p.State]++
		for _, f := range p.Ignored {
			ignored[f] = true
		}
		switch p.State {
		case PackageAnalyzedClean, PackageAnalyzedWithFindings:
			processed = append(processed, p.Files...)
		case PackageUnknown:
			anyUnknown = true
		}
	}
	doc.Coverage.FilesSkipped = len(ignored)
	if len(ignored) > 0 {
		limitations = append(limitations, "build_constraint_ignored_files")
	}
	if !anyUnknown {
		doc.Coverage.ProcessedPaths = processed
	}

	// Outcome: precedence from cleanup to parser state (as go vet).
	results := []*executor.Result{in.GoVersion, in.Version, in.ModEdit, in.List, in.Check}
	status, reason, class := pack.ExecutionOutcome(results, in.Cancelled)
	fail := func(s domain.TaskStatus, r, c string) {
		if reason == "" {
			status, reason, class = s, r, c
		}
	}
	if (in.GoVersion != nil && !goOK) || (in.Version != nil && !toolOK) {
		fail(domain.TaskFailed, ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	if in.ModEdit != nil && !modOK {
		fail(domain.TaskFailed, ReasonGoModUnreadable, obsdoc.ExitConfigurationError)
	}
	if len(escapes) > 0 {
		fail(domain.TaskSkippedPolicy, ReasonReplaceOutside, obsdoc.ExitNotRun)
	}
	if in.GoVersion == nil || in.Version == nil || in.ModEdit == nil || in.List == nil || in.Check == nil {
		fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
	}
	if len(doc.WorkspaceDrift) > 0 {
		fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	switch {
	case toolchain:
		fail(domain.TaskFailed, ReasonToolchain, obsdoc.ExitConfigurationError)
	case moduleMissing:
		fail(domain.TaskFailed, ReasonModuleMissing, obsdoc.ExitConfigurationError)
	case dependency || doc.Coverage.Units[PackageDependencyUnavailable] > 0:
		fail(domain.TaskFailed, ReasonDependency, obsdoc.ExitConfigurationError)
	}
	analyzed := doc.Coverage.Units[PackageAnalyzedClean] + doc.Coverage.Units[PackageAnalyzedWithFindings]
	if noPackages && analyzed == 0 && listOK {
		// Every package was excluded by build constraints or the scope
		// matched none: staticcheck ran on nothing, which is not clean.
		fail(domain.TaskUnsupported, ReasonNoPackages, obsdoc.ExitConfigurationError)
	}
	if in.Check != nil && !pack.StreamsComplete(in.Check) || in.List != nil && !pack.StreamsComplete(in.List) {
		fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
	}
	if in.Check != nil && !parsed {
		fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	if in.List != nil && !listOK && pack.ExitedZero(in.List) {
		fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	if compile || doc.Coverage.Units[PackageFailedTypecheck] > 0 {
		fail(domain.TaskFailed, ReasonPackageErrors, obsdoc.ExitExecutionError)
	}
	switch {
	case in.Check == nil:
	case exit != 0 && exit != 1, fatal:
		// Usage errors and crashes exit 2; a fatal "err:" is printed with 1.
		fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
	case exit == 1 && len(doc.Observations) == 0:
		// Exit 1 is "problems reported": with no problem record it is a
		// failure the tool explained only on stderr.
		fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
	case exit == 0 && len(doc.Observations) > 0:
		// With the default -fail=all every reported problem exits 1.
		fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
	}
	if !listOK || doc.Coverage.Units[PackageLoadError] > 0 {
		fail(domain.TaskFailed, ReasonPackageLoad, obsdoc.ExitExecutionError)
	}
	if analyzed == 0 {
		fail(domain.TaskUnsupported, ReasonNoPackages, obsdoc.ExitConfigurationError)
	}
	if suppressed {
		limitations = append(limitations, "source_suppressed_observations")
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
