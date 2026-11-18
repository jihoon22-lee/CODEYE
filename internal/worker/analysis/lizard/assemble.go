package lizard

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task reasons of the lizard adapter (besides the anypack ones).
const (
	ReasonNotRun = "lizard_not_run"
	ReasonFailed = "lizard_failed"
)

// Limitation codes.
const (
	LimMetricsNotDefects   = "metrics_not_defects"
	LimEndLinesUnknown     = "function_end_lines_not_recorded"
	LimFilesNotAnalyzed    = "source_files_not_analyzed"
	LimSuppressionsHonored = "in_source_suppressions_honored"
)

// Coverage unit states (files).
const (
	UnitAnalyzed    = "analyzed"
	UnitNotAnalyzed = "not_analyzed"
)

// item is one <item> of a measure: its name attribute, values and byte
// range in stdout.
type item struct {
	Name       string
	Values     []int
	Start, End int64
}

type measure struct {
	Type   string
	Labels []string
	Items  []item
}

// parsed is what the XML parser read.
type parsed struct {
	Functions, Files []item
	Records          int
	ShapeErrors      int
	DecodeError      string
}

var (
	functionLabels = []string{"Nr.", "NCSS", "CCN"}
	fileLabels     = []string{"Nr.", "NCSS", "CCN", "Functions"}
)

// parseXML reads lizard 1.26 cppncss XML strictly: the root, the Function
// and File measures in that order, their labels, integer values and nothing
// else. A shape the parser does not know is a shape error, never "no
// functions".
func parseXML(b []byte) parsed {
	var out parsed
	if len(bytes.TrimSpace(b)) == 0 {
		out.DecodeError = "empty output"
		return out
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	var measures []measure
	var stack []string
	var cur *measure
	var it *item
	var text strings.Builder
	shape := func() { out.ShapeErrors++ }
	for {
		off := d.InputOffset()
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			out.DecodeError = "xml: malformed document"
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			name := t.Name.Local
			stack = append(stack, name)
			text.Reset()
			switch {
			case parent == "" && name == "cppncss":
			case parent == "cppncss" && name == "measure":
				measures = append(measures, measure{Type: attr(t, "type")})
				cur = &measures[len(measures)-1]
			case parent == "measure" && name == "labels":
			case parent == "labels" && name == "label":
			case parent == "measure" && name == "item":
				cur.Items = append(cur.Items, item{Name: attr(t, "name"), Start: off})
				it = &cur.Items[len(cur.Items)-1]
			case parent == "item" && name == "value":
			case parent == "measure" && (name == "average" || name == "sum"):
			default:
				shape()
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			name := t.Name.Local
			stack = stack[:len(stack)-1]
			switch name {
			case "label":
				if cur != nil {
					cur.Labels = append(cur.Labels, strings.TrimSpace(text.String()))
				}
			case "value":
				v, err := strconv.Atoi(strings.TrimSpace(text.String()))
				if err != nil || it == nil {
					shape()
				} else {
					it.Values = append(it.Values, v)
				}
			case "item":
				if it != nil {
					it.End = d.InputOffset()
				}
				it = nil
			case "measure":
				cur = nil
			}
			text.Reset()
		case xml.ProcInst, xml.Comment, xml.Directive:
		}
	}
	if len(stack) != 0 {
		out.DecodeError = "xml: unexpected end"
		return out
	}
	if len(measures) != 2 || measures[0].Type != "Function" || measures[1].Type != "File" ||
		!slices.Equal(measures[0].Labels, functionLabels) || !slices.Equal(measures[1].Labels, fileLabels) {
		shape()
		return out
	}
	for _, m := range []struct {
		items []item
		n     int
		dst   *[]item
	}{{measures[0].Items, 3, &out.Functions}, {measures[1].Items, 4, &out.Files}} {
		for _, i := range m.items {
			if len(i.Values) != m.n || i.Name == "" || i.End <= i.Start {
				shape()
				continue
			}
			*m.dst = append(*m.dst, i)
		}
	}
	out.Records = len(out.Functions) + len(out.Files)
	return out
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// functionAt splits "<name>(...) at <file>:<line>" against the analyzed
// file names (longest match), so a name or path with " at " stays exact.
func functionAt(name string, files map[string]bool) (fn, file string, line int, ok bool) {
	best := ""
	for f := range files {
		marker := " at " + f + ":"
		i := strings.LastIndex(name, marker)
		if i < 0 {
			continue
		}
		n, err := strconv.Atoi(name[i+len(marker):])
		if err != nil || n < 1 {
			continue
		}
		if len(f) > len(best) {
			best, fn, file, line, ok = f, name[:i], f, n, true
		}
	}
	return fn, file, line, ok
}

// stderr lines lizard prints for a file it could not analyze (it still
// lists the file with zero values).
var skippedLine = regexp.MustCompile(`^(?:Error: doesn't support none utf encoding '(.*)'|Error: Fail to read source file '(.*)'|\[skip\] fail to process '(.*)' with RecursionError.*)$`)

// parseStderr returns the files lizard reported it could not analyze and
// whether other output was present.
func parseStderr(b []byte) (skipped []string, other bool) {
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		m := skippedLine.FindStringSubmatch(l)
		if m == nil {
			other = true
			continue
		}
		for _, g := range m[1:] {
			if g != "" {
				skipped = append(skipped, g)
			}
		}
	}
	return skipped, other
}

type assembleInput struct {
	TaskKey, ManifestDigest string
	Patterns                []string
	Files                   []source.Entry
	Version, Run            *executor.Result
	Executions              []analysis.Execution
	Drift                   []string
	Resolver                *anypack.Resolver
	Cancelled               bool
	ScratchFailed           bool
	// Excluded: eligible files below a data directory, not measured.
	Excluded int
}

// assemble builds the envelope and decides the task outcome.
func assemble(in assembleInput) obsdoc.Document {
	doc := obsdoc.New(obsdoc.Analyzer{ID: AnalyzerID, Ecosystem: "any", Required: false}, obsdoc.Component{ID: Name, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = anypack.ModuleDir
	doc.Patterns = append([]string{}, in.Patterns...)
	doc.Tool.Name = Tool
	// CODEYE names the files and applies the threshold; lizard's in-source
	// "#lizard forgive" comments and the "GENERATED CODE" marker hide
	// functions from the output without a record (measured), so they are
	// honored and a vanished metric is never read as an improvement.
	doc.RulePack = obsdoc.RulePack{ID: RulePackID, Version: RulePackVersion, Selection: append([]string{}, Selection...),
		RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Target.Properties = map[string]string{"metric": MetricCCN, "threshold": strconv.Itoa(CCNThreshold)}
	doc.Coverage.Unit = "file"
	doc.Coverage.FilesInScope = anypack.Ptr(len(in.Files))
	doc.Executions = anypack.ExecutionsOf(in.Executions)
	lims := []string{LimMetricsNotDefects, LimEndLinesUnknown, LimSuppressionsHonored}
	doc.WorkspaceDrift = append([]string{}, in.Drift...)
	if len(in.Drift) > 0 {
		lims = append(lims, anypack.ReasonWorkspaceDrift)
	}
	var o anypack.Outcome
	o.Executions(in.Cancelled, in.Version, in.Run)
	if in.ScratchFailed {
		o.Fail(domain.TaskFailed, analysis.ReasonHandlerError, obsdoc.ExitNotRun)
	}
	toolOK := false
	if in.Version != nil && anypack.ExitedZero(in.Version) {
		if v, ok := ParseVersion(in.Version.Stdout.Data); ok {
			toolOK = true
			doc.Tool.Version, doc.Tool.VersionTested = anypack.Ptr(v), VersionTested(v)
			if !doc.Tool.VersionTested {
				lims = append(lims, "tool_version_untested")
			}
		}
	}
	if in.Version != nil && !toolOK {
		o.Fail(domain.TaskFailed, anypack.ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	if in.Run == nil {
		o.Fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
	}
	if len(in.Drift) > 0 {
		o.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	var p parsed
	parseOK := false
	if in.Run != nil {
		p = parseXML(in.Run.Stdout.Data)
		parseOK = p.DecodeError == "" && p.ShapeErrors == 0
		doc.Parse.Records, doc.Parse.ShapeErrors, doc.Parse.DecodeError = p.Records, p.ShapeErrors, truncate(p.DecodeError)
		if !parseOK {
			doc.Parse.UnparsedBytes = int64(len(in.Run.Stdout.Data))
		}
		if !anypack.StreamsComplete(in.Run) {
			o.Fail(domain.TaskFailed, anypack.ReasonOutputTruncated, obsdoc.ExitExecutionError)
		}
		if anypack.ExitCode(in.Run) != 0 {
			// XML mode exits 0 on every completed run; anything else is a
			// crash or a usage error.
			o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
		}
		if !parseOK {
			o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
		}
	}
	listed := map[string]bool{}
	for _, f := range in.Files {
		listed[f.Path] = true
	}
	analyzed := map[string]bool{}
	for _, f := range p.Files {
		analyzed[f.Name] = true
	}
	if parseOK && len(analyzed) != len(listed) {
		o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	for f := range analyzed {
		if !listed[f] {
			o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
		}
	}
	skipped := map[string]bool{}
	if in.Run != nil {
		names, other := parseStderr(in.Run.Stderr.Data)
		for _, n := range names {
			if rel, ok := relName(n); ok && listed[rel] {
				skipped[rel] = true
			} else {
				other = true
			}
		}
		if other {
			doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: execRun, Kind: "tool_stderr", Message: "lizard wrote unrecognized output to stderr"})
			o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
		}
	}
	if len(skipped) > 0 {
		lims = append(lims, LimFilesNotAnalyzed)
	}
	doc.Coverage.FilesSkipped = len(skipped)
	// Functions: every record is read; those at or above the threshold are
	// metric observations.
	stdoutSHA := ""
	if in.Run != nil {
		stdoutSHA = obsdoc.SHA256Hex(in.Run.Stdout.Data)
	}
	ordinals := map[string]int{}
	for i, f := range p.Functions {
		fn, file, line, ok := functionAt(f.Name, analyzed)
		if !ok {
			doc.Parse.ShapeErrors++
			o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
			continue
		}
		ncss, ccn := f.Values[1], f.Values[2]
		if ccn < CCNThreshold || skipped[file] {
			continue
		}
		msg := f.Name + ": NCSS " + strconv.Itoa(ncss) + ", CCN " + strconv.Itoa(ccn)
		pos := file + ":" + strconv.Itoa(line)
		tuple := strings.Join([]string{file, RuleCCN, pos, fn, msg}, "\x00")
		ord := ordinals[tuple]
		ordinals[tuple]++
		pathPtr, loc, state := in.Resolver.Resolve(file, line, line, 0, 0)
		doc.Observations = append(doc.Observations, obsdoc.Observation{ID: obsdoc.ObservationID(AnalyzerID, anypack.ModuleDir, file, RuleCCN, pos, pos, msg, ord),
			RuleID: RuleCCN, Category: "maintainability", RawMessage: msg, Path: pathPtr, Location: loc, LocationState: state, Related: []obsdoc.Related{},
			RawRef: obsdoc.RawRef{Execution: execRun, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: i, ByteStart: f.Start, ByteEnd: f.End},
			Unit:   file, Metric: &obsdoc.Metric{Name: MetricCCN, Value: ccn, Threshold: CCNThreshold, Unit: "count"}})
	}
	processed := []string{}
	for f := range analyzed {
		if !skipped[f] {
			processed = append(processed, f)
		}
	}
	doc.Coverage.Units = map[string]int{UnitAnalyzed: len(processed)}
	if len(skipped) > 0 {
		doc.Coverage.Units[UnitNotAnalyzed] = len(skipped)
	}
	doc.Coverage.ProcessedPaths = processed
	lims = append(lims, anypack.RecordExclusions(&doc, in.Excluded)...)
	if !o.Failed() && len(processed) == 0 {
		o.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitConfigurationError)
	}
	doc.Parse.OK = !o.Failed() && parseOK
	o.Apply(&doc, p.Records > 0)
	if doc.Outcome.ExitClass == obsdoc.ExitNotRun {
		doc.Coverage.Units = map[string]int{}
		doc.Coverage.FilesSkipped = 0
	}
	doc.Limitations = lims
	return doc
}

// relName maps a file name lizard printed (the listed relative path, or
// the masked workspace form) to the snapshot path.
func relName(n string) (string, bool) {
	n = strings.TrimPrefix(n, analysis.RedactedMarker+"/")
	if domain.ValidateEvidencePath(n) != nil {
		return "", false
	}
	return n, true
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
