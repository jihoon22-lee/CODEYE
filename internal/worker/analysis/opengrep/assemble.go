package opengrep

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/sarif"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task reasons of the Opengrep adapter.
const (
	ReasonNotRun          = "opengrep_not_run"
	ReasonFailed          = "opengrep_failed"
	ReasonRulePackLoad    = "rule_pack_load_failed"
	ReasonOutsideSnapshot = "result_outside_snapshot"
	ReasonTooManyFiles    = "too_many_files"
)

// Limitation codes.
const (
	// LimIntraFile: a flow is found only when its source and its sink are in
	// the same file; calls between functions of that file are followed.
	LimIntraFile = "intra_file_flows_only"
	// LimRequestSources: the rules start from request input (HTTP request
	// input; for C and C++ bytes received from a socket and CGI request
	// variables); command line arguments, other environment variables,
	// files and databases are not sources.
	LimRequestSources = "request_input_sources_only"
	// LimSourceText: the recorded SARIF stream quotes the matched source
	// text (snippets and flow step messages); the envelope does not.
	LimSourceText = "tool_output_holds_source_text"
	// LimIncomplete: Opengrep reported a file it parsed only in part or
	// whose rules timed out; it is not claimed processed.
	LimIncomplete    = "files_not_fully_analyzed"
	LimFlowTruncated = "flow_steps_truncated"
	LimFilesTooLarge = "files_over_size_limit_skipped"
	LimNotText       = "non_text_files_skipped"
	LimNotScanned    = "planned_files_not_scanned"
	// LimBadNames: files whose names the SARIF log could not name back
	// (a backslash, a drive-like prefix) were not given to Opengrep.
	LimBadNames = "file_names_not_mappable_skipped"
)

// Coverage unit states (files).
const (
	UnitScanned    = "scanned"
	UnitTooLarge   = "skipped_too_large"
	UnitNotText    = "skipped_not_text"
	UnitNotScanned = "not_scanned"
	UnitIncomplete = "not_fully_analyzed"
	UnitBadName    = "skipped_unmappable_name"
)

// maxRelated is the envelope's bound on related locations of one
// observation.
const maxRelated = 64

// maxCopyBytes bounds the JSON copy read back from the task scratch.
const maxCopyBytes = 64 << 20

// SARIFOptions read Opengrep 2.0 logs: URIs are written as raw paths, the
// primary location's base %SRCROOT% is the scan directory, and columns are
// byte offsets although the log declares no columnKind (measured with a
// Korean and an emoji string before a match: startColumn 25 is the byte
// column; the code point column is 18, the UTF-16 one 19).
var SARIFOptions = sarif.Options{URIForm: sarif.URIRawPath, BaseIDs: []string{"%SRCROOT%"}, ColumnKind: sarif.ColumnBytes}

// jsonCopy is the part of Opengrep's JSON output (written with
// --json-output by the same run) the adapter reads.
type jsonCopy struct {
	Results *[]struct {
		CheckID string `json:"check_id"`
		Path    string `json:"path"`
		Start   struct {
			Line int `json:"line"`
		} `json:"start"`
	} `json:"results"`
	Errors *[]struct {
		Level  string          `json:"level"`
		Type   json.RawMessage `json:"type"`
		Path   *string         `json:"path"`
		RuleID *string         `json:"rule_id"`
	} `json:"errors"`
	Paths *struct {
		Scanned *[]string `json:"scanned"`
	} `json:"paths"`
	SkippedRules *[]json.RawMessage `json:"skipped_rules"`
}

func readCopy(p string) (*jsonCopy, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxCopyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxCopyBytes {
		return nil, errors.New("json copy too large")
	}
	var c jsonCopy
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Results == nil || c.Errors == nil || c.Paths == nil || c.Paths.Scanned == nil || c.SkippedRules == nil {
		return nil, errors.New("json copy lacks results, errors, paths.scanned or skipped_rules")
	}
	return &c, nil
}

// errorType is the name of a JSON error type. Opengrep 2.0.0 writes a
// string ("Syntax error" for a file it could not parse at all, "Timeout")
// or a list whose first member names it (["PartialParsing", [spans]])
// (captured in testdata/errors.json). Any other shape is "unknown": the
// type only labels the error; that a file has one is recorded apart.
func errorType(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return s
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 && json.Unmarshal(list[0], &s) == nil && s != "" {
		return s
	}
	return "unknown"
}

type scan struct {
	Exec    string
	Files   []string
	Result  *executor.Result
	Copy    *jsonCopy
	CopyErr error
}

type assembleInput struct {
	TaskKey, ManifestDigest string
	Patterns                []string
	Selected                []source.Entry
	// Files are the selected files given to Opengrep (UTF-8 text within the
	// size limit).
	Files            []source.Entry
	ThirdParty       int
	ExcludedDataDirs int
	Languages        []Language
	Loaded           []Language
	Units            map[string]int
	Version          *executor.Result
	Scans            []scan
	Groups           int
	Executions       []analysis.Execution
	Drift            []string
	Resolver         *anypack.Resolver
	Cancelled        bool
	ScratchFailed    bool
	Integrity        bool
	Linked           bool
	TooManyScans     bool
}

// StepMessage is the fixed text of a flow step in the envelope: its place
// in the tool's flow and its role, never the quoted source text.
func StepMessage(s sarif.Step) string {
	role := "propagation"
	switch {
	case s.Position == 1:
		role = "source"
	case s.Position == s.Of:
		role = "sink"
	case slices.Contains(s.Kinds, "call"):
		role = "call"
	case slices.Contains(s.Kinds, "enter"):
		role = "callee parameter"
	case slices.Contains(s.Kinds, "exit"):
		role = "callee return"
	}
	prefix := "taint flow"
	if s.Flow > 1 || s.Thread > 1 {
		prefix += " " + strconv.Itoa(s.Flow) + "." + strconv.Itoa(s.Thread)
	}
	return fmt.Sprintf("%s step %d of %d: %s", prefix, s.Position, s.Of, role)
}

func relatedKind(s sarif.Step) string {
	switch s.Kind {
	case sarif.StepFlow:
		return obsdoc.RelatedFlowStep
	case sarif.StepRelated:
		return obsdoc.RelatedNote
	}
	return obsdoc.RelatedPosition
}

// keepSteps bounds the related locations: the first steps and the sink.
func keepSteps(steps []sarif.Step) ([]sarif.Step, bool) {
	if len(steps) <= maxRelated {
		return steps, false
	}
	return append(append([]sarif.Step{}, steps[:maxRelated-1]...), steps[len(steps)-1]), true
}

type tally map[string]int

func key(rule, path string, line int) string {
	return rule + "\x00" + path + "\x00" + strconv.Itoa(line)
}

// assemble builds the envelope and decides the task outcome.
func assemble(in assembleInput) obsdoc.Document {
	doc := obsdoc.New(Descriptor().Analyzer(), obsdoc.Component{ID: Name, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = anypack.ModuleDir
	doc.Patterns = append([]string{}, in.Patterns...)
	doc.Tool.Name = Tool
	// Only the CODEYE rule files are read; named targets are scanned whatever
	// a repository .semgrepignore or .gitignore says, and Opengrep has no
	// other repository configuration. nosem comments are reported, not
	// hidden: the SARIF result carries an inSource suppression.
	// The selection names the rule files this run loaded, not every file of
	// the pack: runs over different languages are different rule contexts.
	doc.RulePack = obsdoc.RulePack{ID: RulePackID, Version: RulePackVersion, Selection: Selection(in.Loaded),
		RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	names := []string{}
	for _, l := range in.Loaded {
		names = append(names, l.Name)
	}
	doc.Target.Properties = map[string]string{"analysis": "intra_file_taint", "sources": "request_input", "rule_languages": strings.Join(names, ",")}
	if len(names) == 0 {
		doc.Target.Properties["rule_languages"] = "none"
	}
	doc.Coverage.Unit = "file"
	doc.Coverage.FilesInScope = anypack.Ptr(len(in.Selected))
	doc.Executions = anypack.ExecutionsOf(in.Executions)
	lims := []string{LimIntraFile, LimRequestSources, LimSourceText}
	lims = append(lims, anypack.RecordExclusions(&doc, in.ExcludedDataDirs)...)
	anypack.RecordThirdPartyExclusions(&doc, in.ThirdParty)
	lims = append(lims, doc.Limitations...)
	doc.WorkspaceDrift = append([]string{}, in.Drift...)
	if len(in.Drift) > 0 {
		lims = append(lims, anypack.ReasonWorkspaceDrift)
	}
	var o anypack.Outcome
	results := []*executor.Result{in.Version}
	for _, s := range in.Scans {
		results = append(results, s.Result)
	}
	o.Executions(in.Cancelled, results...)
	switch {
	case in.Integrity:
		o.Fail(domain.TaskFailed, anypack.ReasonSnapshotIntegrity, obsdoc.ExitNotRun)
	case in.Linked:
		o.Fail(domain.TaskFailed, anypack.ReasonWorkspaceLink, obsdoc.ExitNotRun)
	case in.ScratchFailed:
		o.Fail(domain.TaskFailed, analysis.ReasonHandlerError, obsdoc.ExitNotRun)
	case in.TooManyScans:
		o.Fail(domain.TaskUnsupported, ReasonTooManyFiles, obsdoc.ExitNotRun)
	case len(in.Files) == 0:
		o.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitNotRun)
	}
	version := ""
	if in.Version != nil && pack.ExitedZero(in.Version) {
		if v, ok := ParseVersion(in.Version.Stdout.Data); ok {
			version = v
			doc.Tool.Version, doc.Tool.VersionTested = anypack.Ptr(v), VersionTested(v)
			if !doc.Tool.VersionTested {
				lims = append(lims, "tool_version_untested")
			}
		}
	}
	if in.Version != nil && version == "" {
		o.Fail(domain.TaskFailed, anypack.ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	if len(in.Scans) != in.Groups {
		o.Fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
	}
	if len(in.Drift) > 0 {
		o.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	var wantRules []string
	for _, l := range in.Loaded {
		wantRules = append(wantRules, l.Rules...)
	}
	sort.Strings(wantRules)
	given := map[string]bool{}
	for _, f := range in.Files {
		given[f.Path] = true
	}
	scanned := map[string]bool{}
	incomplete := map[string]string{}
	ruleInfo := map[string]obsdoc.Rule{}
	ordinals := map[string]int{}
	records := 0
	flowsCut := false
	for _, s := range in.Scans {
		res := s.Result
		if res == nil {
			continue
		}
		if !pack.StreamsComplete(res) {
			o.Fail(domain.TaskFailed, anypack.ReasonOutputTruncated, obsdoc.ExitExecutionError)
		}
		code := pack.ExitCode(res)
		log, err := sarif.Decode(res.Stdout.Data, SARIFOptions)
		if err != nil {
			doc.Parse.ShapeErrors++
			doc.Parse.UnparsedBytes += int64(len(res.Stdout.Data))
			if doc.Parse.DecodeError == "" {
				doc.Parse.DecodeError = "sarif log rejected by the adapter"
			}
			switch code {
			case 0:
				o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
			case 4, 5, 7, 8:
				o.Fail(domain.TaskFailed, ReasonRulePackLoad, obsdoc.ExitConfigurationError)
			default:
				o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
			}
			continue
		}
		run := log.Runs[0]
		ruleErr := false
		for _, n := range run.Notifications {
			if n.Level == "error" {
				ruleErr = true
			}
		}
		var gotRules []string
		for _, r := range run.Rules {
			gotRules = append(gotRules, r.ID)
			short := r.FullDescription
			if short == "" {
				short = r.ShortDescription
			}
			if i := strings.IndexByte(short, '\n'); i >= 0 {
				short = short[:i]
			}
			cwe := append([]string{}, r.CWE...)
			ruleInfo[r.ID] = obsdoc.Rule{ID: r.ID, ShortDescription: short, CWE: cwe}
		}
		sort.Strings(gotRules)
		switch {
		case code == 4 || code == 5 || code == 7 || code == 8, ruleErr, !slices.Equal(gotRules, wantRules):
			// A rule file that does not load is a failed task, never fewer
			// findings.
			o.Fail(domain.TaskFailed, ReasonRulePackLoad, obsdoc.ExitConfigurationError)
		case code != 0, !run.ExecutionSuccessful:
			o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
		case len(log.Runs) != 1 || run.Driver.Name != "Opengrep" || run.Driver.SemanticVersion != version || run.ColumnKind != sarif.ColumnBytes:
			o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
		}
		// The JSON copy of the same run: scanned files and per-file errors,
		// cross-checked against the SARIF log.
		c := s.Copy
		switch {
		case s.CopyErr != nil || c == nil:
			o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
			if doc.Parse.DecodeError == "" {
				doc.Parse.DecodeError = "json copy of the scan unreadable"
			}
		default:
			if len(*c.SkippedRules) > 0 {
				o.Fail(domain.TaskFailed, ReasonRulePackLoad, obsdoc.ExitConfigurationError)
			}
			// The JSON output leaves out results hidden by nosem; the SARIF
			// log keeps them with an inSource suppression (measured, 2.0.0).
			if len(*c.Errors) != len(run.Notifications) {
				o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
				doc.Parse.DecodeError = "json copy and sarif log disagree"
			}
			want := tally{}
			for _, r := range *c.Results {
				want[key(r.CheckID, r.Path, r.Start.Line)]++
			}
			for _, r := range run.Results {
				if r.Primary == nil || r.Suppressed {
					continue
				}
				want[key(r.RuleID, r.Primary.URI, r.Primary.StartLine)]--
			}
			for _, v := range want {
				if v != 0 {
					o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
					doc.Parse.DecodeError = "json copy and sarif log disagree"
				}
			}
			batch := map[string]bool{}
			for _, f := range s.Files {
				batch[f] = true
			}
			for _, p := range *c.Paths.Scanned {
				if !batch[p] {
					o.Fail(domain.TaskFailed, ReasonOutsideSnapshot, obsdoc.ExitExecutionError)
					continue
				}
				scanned[p] = true
			}
			for _, e := range *c.Errors {
				switch {
				case e.Path != nil && batch[*e.Path]:
					// An error about one given file, whatever its level and
					// type (a file parsed in part or not at all, a rule that
					// timed out on it): that file is not fully analyzed and
					// the other files keep their results.
					if _, ok := incomplete[*e.Path]; !ok {
						incomplete[*e.Path] = errorType(e.Type)
					}
				case e.RuleID != nil:
					o.Fail(domain.TaskFailed, ReasonRulePackLoad, obsdoc.ExitConfigurationError)
				case e.Path != nil:
					o.Fail(domain.TaskFailed, ReasonOutsideSnapshot, obsdoc.ExitExecutionError)
				default:
					// An error of the run, about no file and no rule.
					o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
				}
			}
		}
		stdoutSHA := obsdoc.SHA256Hex(res.Stdout.Data)
		dropped := 0
		for _, r := range run.Results {
			records++
			doc.Parse.Records++
			var pathPtr *string
			var loc *obsdoc.Location
			state := obsdoc.LocationNoPosition
			file := ""
			if r.Primary != nil {
				file = r.Primary.Path
				if r.Primary.Outside || !given[file] {
					dropped++
					continue
				}
				pathPtr, loc, state = in.Resolver.Resolve(file, r.Primary.StartLine, r.Primary.EndLine, r.Primary.StartColumn, r.Primary.EndColumn)
			}
			steps, cut := keepSteps(r.Steps)
			flowsCut = flowsCut || cut
			related := []obsdoc.Related{}
			for _, st := range steps {
				rel := obsdoc.Related{Kind: relatedKind(st), RawSpan: &obsdoc.ByteSpan{Start: st.ByteStart, End: st.ByteEnd}, RawMessage: StepMessage(st), LocationState: obsdoc.LocationNoPosition}
				if st.Kind != sarif.StepFlow {
					rel.RawMessage = "related location reported by opengrep"
				}
				if st.Location != nil && st.Location.StartLine > 0 {
					if st.Location.Outside || !given[st.Location.Path] {
						rel.LocationState = obsdoc.LocationOutsideSnapshot
					} else {
						rel.Path, rel.Location, rel.LocationState = in.Resolver.Resolve(st.Location.Path, st.Location.StartLine, st.Location.EndLine, st.Location.StartColumn, st.Location.EndColumn)
					}
				}
				related = append(related, rel)
			}
			pos, end := file+":0:0", file+":0:0"
			if r.Primary != nil {
				pos = file + ":" + strconv.Itoa(r.Primary.StartLine) + ":" + strconv.Itoa(r.Primary.StartColumn)
				end = file + ":" + strconv.Itoa(r.Primary.EndLine) + ":" + strconv.Itoa(r.Primary.EndColumn)
			}
			tuple := strings.Join([]string{file, r.RuleID, pos, end, r.Message}, "\x00")
			ord := ordinals[tuple]
			ordinals[tuple]++
			sev := r.Level
			doc.Observations = append(doc.Observations, obsdoc.Observation{ID: obsdoc.ObservationID(AnalyzerID, anypack.ModuleDir, file, r.RuleID, pos, end, r.Message, ord),
				RuleID: r.RuleID, Category: "security", RawSeverity: &sev, RawMessage: r.Message, Path: pathPtr, Location: loc, LocationState: state, Related: related,
				RawRef: obsdoc.RawRef{Execution: s.Exec, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: r.Index, JSONPointer: r.Pointer, ByteStart: r.ByteStart, ByteEnd: r.ByteEnd},
				Unit:   file, Suppressed: r.Suppressed})
		}
		if dropped > 0 {
			doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: s.Exec, Kind: "result_outside_snapshot", Unit: ".",
				Message: "opengrep reported " + strconv.Itoa(dropped) + " result(s) for files it was not given (paths not recorded)"})
			o.Fail(domain.TaskFailed, ReasonOutsideSnapshot, obsdoc.ExitExecutionError)
		}
	}
	if flowsCut {
		lims = append(lims, LimFlowTruncated)
	}
	if len(ruleInfo) > 0 {
		for _, id := range wantRules {
			if ri, ok := ruleInfo[id]; ok {
				doc.Rules = append(doc.Rules, ri)
			}
		}
		for _, ob := range doc.Observations {
			if _, ok := ruleInfo[ob.RuleID]; !ok || !slices.Contains(wantRules, ob.RuleID) {
				o.Fail(domain.TaskFailed, ReasonRulePackLoad, obsdoc.ExitConfigurationError)
				doc.Rules = nil
			}
		}
	}
	units := map[string]int{}
	for k, v := range in.Units {
		units[k] = v
	}
	for k, v := range doc.Coverage.Units {
		units[k] = v
	}
	processed := []string{}
	var partial []string
	for _, f := range in.Files {
		switch {
		case !scanned[f.Path]:
			units[UnitNotScanned]++
		case hasKey(incomplete, f.Path):
			units[UnitIncomplete]++
			partial = append(partial, f.Path)
		default:
			units[UnitScanned]++
			processed = append(processed, f.Path)
		}
	}
	if len(partial) > 0 {
		lims = append(lims, LimIncomplete)
		doc.Target.Properties["incomplete_files"] = listProperty(partial)
	}
	if units[UnitTooLarge] > 0 {
		lims = append(lims, LimFilesTooLarge)
	}
	if units[UnitNotText] > 0 {
		lims = append(lims, LimNotText)
	}
	if units[UnitBadName] > 0 {
		lims = append(lims, LimBadNames)
	}
	if len(in.Scans) > 0 && units[UnitNotScanned] > 0 {
		lims = append(lims, LimNotScanned)
	}
	doc.Coverage.Units = units
	doc.Coverage.FilesSkipped = units[UnitTooLarge] + units[UnitNotText] + units[UnitBadName]
	doc.Coverage.FilesUnlisted = units[UnitNotScanned]
	doc.Coverage.ProcessedPaths = processed
	if !o.Failed() && len(processed) == 0 {
		o.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitConfigurationError)
	}
	doc.Parse.OK = !o.Failed()
	o.Apply(&doc, records > 0)
	if doc.Outcome.ExitClass == obsdoc.ExitNotRun {
		doc.Rules = nil
		doc.Coverage.Units = map[string]int{}
		doc.Coverage.FilesSkipped, doc.Coverage.FilesUnlisted = 0, 0
	}
	doc.Limitations = lims
	return doc
}

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

// listProperty joins sorted paths into a target property of at most 256
// bytes; the rest is "+N more".
func listProperty(paths []string) string {
	sort.Strings(paths)
	out := ""
	for i, p := range paths {
		next := p
		if out != "" {
			next = out + "," + p
		}
		more := " +" + strconv.Itoa(len(paths)-i) + " more"
		if len(next) > 256-len(more) {
			return out + more
		}
		out = next
	}
	return out
}
