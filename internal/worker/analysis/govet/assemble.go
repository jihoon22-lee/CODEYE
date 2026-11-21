package govet

import (
	"path"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task failure reasons (stable codes recorded in TaskResult.Reason and Outcome.Reason).
const (
	ReasonCleanupUncertain     = pack.ReasonCleanupUncertain
	ReasonCancelled            = pack.ReasonCancelled
	ReasonTimeout              = pack.ReasonTimeout
	ReasonExecutionUnsupported = pack.ReasonExecutionUnsupported
	ReasonToolStartFailed      = pack.ReasonToolStartFailed
	ReasonExecutionRejected    = pack.ReasonExecutionRejected
	ReasonToolVersion          = "tool_version_unrecognized"
	ReasonToolchain            = "toolchain_unavailable"
	ReasonModuleMissing        = "go_module_missing"
	ReasonDependency           = "dependency_unavailable"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonPackageErrors        = "package_errors"
	ReasonVetFailed            = "vet_failed"
	ReasonPackageLoad          = "package_load_error"
	ReasonAnalyzerError        = "analyzer_error"
	ReasonSnapshotIntegrity    = "snapshot_integrity_failed"
	ReasonReplaceOutside       = "replace_outside_snapshot"
	ReasonGoModUnreadable      = "go_mod_unreadable"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonNotRun               = "vet_not_run"
	ReasonNoPackages           = "no_analyzable_packages"
)

// assembleInput is everything the pure document builder needs. Results are
// nil for executions that were not run.
type assembleInput struct {
	TaskKey        string
	ManifestDigest string
	Module         ModuleRef
	Patterns       []string
	Files          map[string]source.Entry
	ScopeGoFiles   []string
	// Lines returns the line count and sha256 of a snapshot file.
	Lines                       func(rel string) (lines int, sha string, ok bool)
	Drift                       map[string]bool
	Version, ModEdit, List, Vet *executor.Result
	Mask                        func(string) string
	Cancelled                   bool
	ExtraLimitations            []string
	// WorkspaceRoots are the absolute workspace root forms (as given and
	// symlink-resolved). Tool JSON escapes backslashes and HTML characters,
	// so the executor's byte redaction can miss them; decoded paths are stripped here.
	WorkspaceRoots []string
	// Windows selects drive/backslash/case-insensitive path rules.
	Windows bool
}

func execSummary(name string, r *executor.Result) ExecSummary {
	return ExecSummary{Name: name, Status: r.Status, Reason: r.Reason, ExitCode: r.ExitCode, Cleanup: r.Cleanup,
		Command: analysis.CommandFor(r.Command), Stdout: streamRef(r.Stdout), Stderr: streamRef(r.Stderr)}
}

// stripWorkspace maps a tool path to a snapshot-relative path. Workspace
// paths arrive as "[REDACTED]/rel" (executor Redact hit) or as the decoded
// absolute workspace root (Redact missed a JSON-escaped form). A relative
// path is relative to the module directory and accepted only if allowRelative.
func stripWorkspace(p, moduleDir string, roots []string, windows, allowRelative bool) (string, bool) {
	norm := func(x string) string {
		if windows {
			x = strings.ReplaceAll(x, `\`, "/")
		}
		return x
	}
	p = norm(p)
	var rel string
	matched := false
	switch {
	case strings.HasPrefix(p, analysis.RedactedMarker+"/"):
		rel, matched = strings.TrimPrefix(p, analysis.RedactedMarker+"/"), true
	case p == analysis.RedactedMarker:
		return ".", true
	}
	for _, root := range roots {
		if matched || root == "" {
			break
		}
		r := strings.TrimRight(norm(root), "/")
		hp, hr := p, r
		if windows {
			hp, hr = strings.ToLower(p), strings.ToLower(r)
		}
		switch {
		case hp == hr:
			return ".", true
		case strings.HasPrefix(hp, hr+"/"):
			rel, matched = p[len(r)+1:], true
		}
	}
	if !matched {
		if !allowRelative || strings.HasPrefix(p, "/") || strings.Contains(p, ":") || strings.Contains(p, analysis.RedactedMarker) {
			return "", false
		}
		rel = joinRel(moduleDir, p)
	}
	rel = path.Clean(rel)
	if rel == "." {
		return ".", true
	}
	if domain.ValidateEvidencePath(rel) != nil {
		return "", false
	}
	return rel, true
}

func (in assembleInput) strip(p string, allowRelative bool) (string, bool) {
	return stripWorkspace(p, in.Module.Dir, in.WorkspaceRoots, in.Windows, allowRelative)
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

func (rs *resolver) resolve(posn, end string) (*string, *ObsLocation, string) {
	p, line, col, ok := splitPosition(posn)
	if !ok {
		return nil, nil, LocationNoPosition
	}
	rel, ok := rs.in.strip(p, false)
	if !ok || rel == "." {
		return nil, nil, LocationOutsideSnapshot
	}
	relp := rel
	entry, ok := rs.in.Files[rel]
	if !ok {
		return &relp, nil, LocationMissingFile
	}
	if rs.in.Drift[rel] {
		return &relp, nil, LocationSourceDrift
	}
	n, sha, ok := rs.lines(rel)
	if !ok || sha != entry.SHA256 {
		return &relp, nil, LocationSourceDrift
	}
	endLine, endCol := line, col
	if ep, el, ec, eok := splitPosition(end); eok {
		if erel, rok := rs.in.strip(ep, false); rok && erel == rel {
			endLine, endCol = el, ec
		}
	}
	maxLine := max(n, 1)
	if line < 1 || line > maxLine || endLine < line || endLine > maxLine || (endLine == line && endCol < col) {
		return &relp, nil, LocationLineOutOfRange
	}
	return &relp, &ObsLocation{Path: rel, StartLine: line, StartCol: col, EndLine: endLine, EndCol: endCol, ColumnUnit: "byte", BlobSHA256: entry.SHA256}, LocationResolved
}

// packageKeyImportPath maps a vet JSON / stderr package key to an import
// path candidate: "p [p.test]" → "p"; "p_test" stays (resolved against
// list results by the caller).
func packageKeyImportPath(key string) string {
	if i := strings.Index(key, " ["); i > 0 {
		key = key[:i]
	}
	return strings.TrimSpace(key)
}

func ptr[T any](v T) *T { return &v }

// assemble builds the observations document and decides the task outcome.
// Success is one explicit conjunction; everything else is failed, timed_out,
// cancelled or unsupported, and keeps whatever was parsed.
func assemble(in assembleInput) Document {
	mask := in.Mask
	if mask == nil {
		mask = func(s string) string { return s }
	}
	doc := Document{
		SchemaVersion: DocumentSchemaVersion, Kind: DocumentKind, AnalyzerID: AnalyzerID, AdapterVersion: AdapterVersion,
		ParserID: ParserID, ParserVersion: ParserVersion, TaskKey: in.TaskKey, ManifestDigest: in.ManifestDigest,
		Module: in.Module, Patterns: append([]string{}, in.Patterns...),
		Executions: []ExecSummary{}, Inventory: []PackageScope{}, Observations: []Observation{},
		AnalyzerErrors: []AnalyzerError{}, StderrFailures: []StderrFailure{}, WorkspaceDrift: []string{},
		Coverage: Coverage{GoFilesInScope: len(in.ScopeGoFiles), Packages: map[string]int{}},
	}
	limitations := append([]string{"default_build_context_only", "cgo_disabled"}, in.ExtraLimitations...)
	for p := range in.Drift {
		doc.WorkspaceDrift = append(doc.WorkspaceDrift, p)
	}
	sort.Strings(doc.WorkspaceDrift)
	if len(doc.WorkspaceDrift) > 0 {
		limitations = append(limitations, "workspace_drift")
	}

	var toolVersion *string
	versionOK := false
	if in.Version != nil {
		doc.Executions = append(doc.Executions, execSummary(execVersion, in.Version))
		if pack.ExitedZero(in.Version) {
			if v, platform, ok := parseVersion(in.Version.Stdout.Data); ok {
				toolVersion, doc.ToolVersion, doc.Platform, versionOK = ptr(v), ptr(v), ptr(platform), true
				if !versionTested(v) {
					limitations = append(limitations, "tool_version_untested")
				}
			}
		}
	}

	// go mod edit -json: the go command's own go.mod parse (module path and
	// replace targets); a filesystem replace leaving the snapshot is refused.
	modOK := false
	var escapes []string
	if in.ModEdit != nil {
		doc.Executions = append(doc.Executions, execSummary(execModEdit, in.ModEdit))
		if pack.ExitedZero(in.ModEdit) && pack.StreamsComplete(in.ModEdit) {
			if modPath, targets, ok := parseModEdit(in.ModEdit.Stdout.Data); ok {
				modOK = true
				doc.Module.Path = modPath
				escapes = escapingReplaces(targets, in.Module.Dir, in.Windows)
			}
		}
	}
	if len(escapes) > 0 {
		limitations = append(limitations, "local_replace_outside_snapshot")
	}

	// go list inventory.
	listOK := false
	var stream listStream
	packages := map[string]*PackageScope{} // by import path
	var order []*PackageScope
	listed := map[string]bool{}
	if in.List != nil {
		doc.Executions = append(doc.Executions, execSummary(execList, in.List))
		stream = parseListStream(in.List.Stdout.Data)
		doc.Parse.ListUnparsedBytes, doc.Parse.ListDecodeError = stream.UnparsedBytes, stream.DecodeError
		listOK = pack.ExitedZero(in.List) && pack.StreamsComplete(in.List) && stream.UnparsedBytes == 0
		for _, p := range stream.Packages {
			ps := &PackageScope{ImportPath: p.ImportPath, GoFiles: []string{}, IgnoredGoFiles: []string{}, Errors: []string{}}
			if dir, ok := in.strip(p.Dir, false); ok && p.Dir != "" {
				ps.Dir = dir
			}
			for _, group := range [][]string{p.GoFiles, p.CgoFiles, p.TestGoFiles, p.XTestGoFiles} {
				for _, f := range group {
					rel := joinRel(ps.Dir, f)
					ps.GoFiles = append(ps.GoFiles, rel)
					listed[rel] = true
				}
			}
			for _, f := range p.IgnoredGoFiles {
				rel := joinRel(ps.Dir, f)
				ps.IgnoredGoFiles = append(ps.IgnoredGoFiles, rel)
				listed[rel] = true
			}
			ps.GoFiles, ps.IgnoredGoFiles = uniqueSorted(ps.GoFiles), uniqueSorted(ps.IgnoredGoFiles)
			dependency := false
			if p.Error != nil {
				ps.Errors = append(ps.Errors, mask(p.Error.Err))
				dependency = dependency || isDependencyError(p.Error.Err)
			}
			for i, e := range p.DepsErrors {
				if e == nil {
					continue
				}
				dependency = dependency || isDependencyError(e.Err)
				if i < 20 {
					ps.Errors = append(ps.Errors, mask(e.Err))
				}
			}
			switch {
			case dependency:
				ps.State = PackageDependencyUnavailable
			case p.Error != nil || len(p.DepsErrors) > 0 || p.Incomplete:
				ps.State = PackageLoadError
			}
			if _, dup := packages[p.ImportPath]; !dup {
				packages[p.ImportPath] = ps
			}
			order = append(order, ps)
		}
	}

	// go vet stdout and stderr.
	var vs vetStream
	var vetLines []stderrLine
	vetParsed := false
	if in.Vet != nil {
		doc.Executions = append(doc.Executions, execSummary(execVet, in.Vet))
		vs = parseVetStream(in.Vet.Stdout.Data)
		vetLines, doc.Parse.StderrLinesDropped = parseStderr(in.Vet.Stderr.Data)
		vetParsed = vs.UnparsedBytes == 0 && vs.ShapeErrors == 0 && vs.DecodeError == ""
	}
	doc.Parse.StdoutValues, doc.Parse.EmptyValues = vs.Values, vs.EmptyValues
	doc.Parse.UnparsedBytes, doc.Parse.ShapeErrors, doc.Parse.DecodeError = vs.UnparsedBytes, vs.ShapeErrors, vs.DecodeError
	stdoutSHA := ""
	if in.Vet != nil {
		stdoutSHA = sha256Hex(in.Vet.Stdout.Data)
	}

	lookup := func(key string) *PackageScope {
		ip := packageKeyImportPath(key)
		if p, ok := packages[ip]; ok {
			return p
		}
		if strings.HasSuffix(ip, "_test") {
			if p, ok := packages[strings.TrimSuffix(ip, "_test")]; ok {
				return p
			}
		}
		return nil
	}

	rs := &resolver{in: in, cache: map[string][3]any{}}
	withFindings := map[*PackageScope]bool{}
	unattributed := false
	ordinals := map[string]int{}
	for _, d := range vs.Diagnostics {
		pathPtr, loc, state := rs.resolve(d.Posn, d.End)
		normPosn, normEnd := mask(d.Posn), mask(d.End)
		tuple := strings.Join([]string{d.PackageKey, d.Analyzer, normPosn, normEnd, d.Message}, "\x00")
		ordinal := ordinals[tuple]
		ordinals[tuple]++
		obs := Observation{
			ID: observationID(d.PackageKey, d.Analyzer, normPosn, normEnd, mask(d.Message), ordinal), Tool: AnalyzerID, ToolVersion: toolVersion,
			RuleID: d.Analyzer, RawMessage: mask(d.Message), Path: pathPtr, Location: loc, LocationState: state,
			RawRef:     RawRef{Execution: execVet, Stream: "stdout", StreamSHA256: stdoutSHA, StreamIndex: d.StreamIndex, JSONPointer: d.JSONPointer, ByteStart: d.ByteStart, ByteEnd: d.ByteEnd},
			PackageKey: d.PackageKey, SuggestedFixes: d.SuggestedFixes, RelatedCount: d.Related,
		}
		if state != LocationResolved {
			doc.Parse.UnresolvedObservations++
		}
		doc.Observations = append(doc.Observations, obs)
		if p := lookup(d.PackageKey); p != nil {
			withFindings[p] = true
		} else {
			unattributed = true
		}
	}
	sortObservations(doc.Observations)
	doc.Parse.Observations = len(doc.Observations)
	for _, e := range vs.AnalyzerErrors {
		e.Message = mask(e.Message)
		e.RawRef.StreamSHA256 = stdoutSHA
		doc.AnalyzerErrors = append(doc.AnalyzerErrors, e)
		if p := lookup(e.PackageKey); p != nil && p.State == "" {
			p.State = PackageUnknown
		}
	}
	if unattributed {
		limitations = append(limitations, "unattributed_vet_package_key")
	}

	// stderr failures (vet, then list).
	typecheck, depFailure, toolchain, moduleMissing, noPackages := false, false, false, false, false
	addFailures := func(exec string, lines []stderrLine) {
		for _, l := range lines {
			if l.Kind == "header" || l.Kind == "continuation" {
				continue
			}
			f := StderrFailure{Execution: exec, Kind: l.Kind, Package: l.Package, Message: mask(l.Message), ByteOffset: l.ByteOffset}
			if p, line, col, ok := splitPosition(l.Pos); ok {
				if rel, ok := in.strip(p, true); ok && rel != "." {
					f.Path = ptr(rel)
				}
				f.Line, f.Column = line, col
			}
			text := l.Message
			switch {
			case strings.Contains(text, "matched no packages") || strings.Contains(text, "no packages to vet"):
				noPackages = true
			case isToolchainError(text):
				toolchain = true
			case isModuleMissing(text):
				moduleMissing = true
			case isDependencyError(text):
				depFailure = true
			case l.Kind == "typecheck" || l.Package != "":
				typecheck = true
			}
			if l.Package != "" {
				if p := lookup(l.Package); p != nil {
					switch {
					case isDependencyError(text):
						p.State = PackageDependencyUnavailable
					case p.State == "" || p.State == PackageUnknown:
						p.State = PackageFailedTypecheck
					}
				}
			}
			if len(doc.StderrFailures) < 200 {
				doc.StderrFailures = append(doc.StderrFailures, f)
			}
		}
	}
	addFailures(execVet, vetLines)
	if in.List != nil {
		listLines, dropped := parseStderr(in.List.Stderr.Data)
		doc.Parse.StderrLinesDropped += dropped
		addFailures(execList, listLines)
	}
	for _, p := range order {
		for _, e := range p.Errors {
			switch {
			case isToolchainError(e):
				toolchain = true
			case isModuleMissing(e):
				moduleMissing = true
			}
		}
	}

	vetComplete := pack.ExitedZero(in.Vet) && pack.StreamsComplete(in.Vet) && vetParsed && len(vs.AnalyzerErrors) == 0
	for _, p := range order {
		if p.State != "" {
			continue
		}
		switch {
		case withFindings[p]:
			p.State = PackageAnalyzedWithFindings
		case vetComplete && listOK:
			p.State = PackageAnalyzedClean
		default:
			p.State = PackageUnknown
		}
	}

	// Files in scope that go list did not report (whole-package build
	// constraints, cgo-only packages, ignored names).
	unlisted := map[string][]string{}
	if listOK {
		for _, f := range in.ScopeGoFiles {
			if !listed[f] {
				unlisted[pack.DirOf(f)] = append(unlisted[pack.DirOf(f)], f)
				doc.Coverage.UnlistedGoFiles++
			}
		}
	}
	for _, p := range order {
		doc.Inventory = append(doc.Inventory, *p)
	}
	for _, dir := range sortedKeys(unlisted) {
		doc.Inventory = append(doc.Inventory, PackageScope{Dir: dir, State: PackageNotAnalyzed, GoFiles: []string{}, IgnoredGoFiles: uniqueSorted(unlisted[dir]), Errors: []string{"not_listed_by_go_command"}})
	}
	sort.SliceStable(doc.Inventory, func(a, b int) bool {
		if doc.Inventory[a].Dir != doc.Inventory[b].Dir {
			return doc.Inventory[a].Dir < doc.Inventory[b].Dir
		}
		return doc.Inventory[a].ImportPath < doc.Inventory[b].ImportPath
	})
	if doc.Coverage.UnlistedGoFiles > 0 {
		limitations = append(limitations, "unlisted_go_files")
	}

	// Coverage.
	processed := map[string]bool{}
	ignored := map[string]bool{}
	anyUnknown := !listOK
	for _, p := range doc.Inventory {
		doc.Coverage.Packages[p.State]++
		for _, f := range p.IgnoredGoFiles {
			ignored[f] = true
		}
		switch p.State {
		case PackageAnalyzedClean, PackageAnalyzedWithFindings:
			for _, f := range p.GoFiles {
				processed[f] = true
			}
		case PackageUnknown:
			anyUnknown = true
		}
	}
	doc.Coverage.IgnoredGoFiles = len(ignored)
	if len(ignored) > 0 {
		limitations = append(limitations, "build_constraint_ignored_files")
	}
	if !anyUnknown {
		doc.Coverage.ProcessedGoFiles = ptr(len(processed))
	}

	// Outcome: precedence from cleanup to parser state.
	results := []*executor.Result{in.Version, in.ModEdit, in.List, in.Vet}
	status, reason, class := pack.ExecutionOutcome(results, in.Cancelled)
	partial := false
	fail := func(s domain.TaskStatus, r, c string) {
		if reason == "" {
			status, reason, class = s, r, c
		}
	}
	if in.Version != nil && !versionOK {
		fail(domain.TaskFailed, ReasonToolVersion, ExitConfigurationError)
	}
	if in.ModEdit != nil && !modOK {
		fail(domain.TaskFailed, ReasonGoModUnreadable, ExitConfigurationError)
	}
	if len(escapes) > 0 {
		fail(domain.TaskSkippedPolicy, ReasonReplaceOutside, ExitNotRun)
	}
	if in.Version == nil || in.ModEdit == nil || in.List == nil || in.Vet == nil {
		fail(domain.TaskFailed, ReasonNotRun, ExitNotRun)
	}
	if len(doc.WorkspaceDrift) > 0 {
		// The tool read bytes that are not the snapshot: no coverage claim holds.
		fail(domain.TaskFailed, ReasonWorkspaceDrift, ExitExecutionError)
	}
	switch {
	case toolchain:
		fail(domain.TaskFailed, ReasonToolchain, ExitConfigurationError)
	case moduleMissing:
		fail(domain.TaskFailed, ReasonModuleMissing, ExitConfigurationError)
	case depFailure || doc.Coverage.Packages[PackageDependencyUnavailable] > 0:
		fail(domain.TaskFailed, ReasonDependency, ExitConfigurationError)
	}
	analyzed := doc.Coverage.Packages[PackageAnalyzedClean] + doc.Coverage.Packages[PackageAnalyzedWithFindings]
	if noPackages && analyzed == 0 && listOK {
		// Every package was excluded (build constraints, cgo) or the scope
		// matched none: vet ran on nothing, which is not a clean result.
		fail(domain.TaskUnsupported, ReasonNoPackages, ExitConfigurationError)
	}
	if in.Vet != nil && !pack.StreamsComplete(in.Vet) || in.List != nil && !pack.StreamsComplete(in.List) {
		fail(domain.TaskFailed, ReasonOutputTruncated, ExitExecutionError)
	}
	if in.Vet != nil && !vetParsed || in.List != nil && stream.UnparsedBytes > 0 {
		fail(domain.TaskFailed, ReasonOutputUnparsed, ExitExecutionError)
	}
	if typecheck || doc.Coverage.Packages[PackageFailedTypecheck] > 0 {
		fail(domain.TaskFailed, ReasonPackageErrors, ExitExecutionError)
	}
	if in.Vet != nil && !pack.ExitedZero(in.Vet) {
		fail(domain.TaskFailed, ReasonVetFailed, ExitExecutionError)
	}
	if !listOK || doc.Coverage.Packages[PackageLoadError] > 0 {
		fail(domain.TaskFailed, ReasonPackageLoad, ExitExecutionError)
	}
	if len(vs.AnalyzerErrors) > 0 {
		fail(domain.TaskFailed, ReasonAnalyzerError, ExitExecutionError)
	}
	if analyzed == 0 {
		// vet exited 0 having vetted nothing (all packages excluded by build
		// constraints/cgo, or an empty scope): not a clean result.
		fail(domain.TaskUnsupported, ReasonNoPackages, ExitConfigurationError)
	}
	if reason == "" {
		class = ExitSuccessWithoutFindings
		if len(doc.Observations) > 0 {
			class = ExitSuccessWithFindings
		}
	} else {
		// Some vet output was read but the task failed: what was kept is partial.
		partial = in.Vet != nil && (vs.Values > 0 || vs.UnparsedBytes > 0 || in.Vet.Stdout.Truncated || in.Vet.Stdout.Incomplete)
	}
	doc.Outcome = Outcome{TaskStatus: status, ExitClass: class, PartialParse: partial}
	if reason != "" {
		doc.Outcome.Reason = ptr(reason)
	}
	doc.Parse.OK = reason == "" && vetParsed
	doc.Limitations = uniqueSorted(limitations)
	return doc
}
