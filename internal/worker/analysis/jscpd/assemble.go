package jscpd

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task reasons of the jscpd adapter.
const (
	ReasonNotRun = "jscpd_not_run"
	ReasonFailed = "jscpd_failed"
)

// Limitation codes.
const (
	LimMetricsNotDefects   = "metrics_not_defects"
	LimSuppressionsHonored = "in_source_suppressions_honored"
	LimColumnsNotRecorded  = "clone_columns_not_recorded"
	LimFilesSkipped        = "source_files_not_compared"
	LimProcessedFromInput  = "processed_paths_from_input_list"
	LimNoFileReachedLimits = "no_file_reached_clone_thresholds"
)

// UnitCompared is the coverage state of a file handed to jscpd.
const UnitCompared = "compared"

// clone is one xcode record: the first place, the duplicated place and the
// record's byte range in stdout.
type clone struct {
	File, Other          string
	Start, End           int
	OtherStart, OtherEnd int
	// Lines is jscpd's own line count of the clone ("Found N lines"), the
	// value its minLines threshold is applied to (end - start, measured).
	Lines              int
	Message            string
	Index              int
	ByteStart, ByteEnd int64
}

// stream is what the stdout parser read.
type stream struct {
	Clones        []clone
	Summary       int // "Found N clones." (-1 when missing)
	Records       int
	UnparsedBytes int64
	ShapeErrors   int
}

var (
	cloneLine   = regexp.MustCompile(`^(.+):([0-9]+):([0-9]+): warning: Found ([0-9]+) lines \(([0-9]+)-([0-9]+)\) duplicated on file (.+) \(([0-9]+)-([0-9]+)\)$`)
	summaryLine = regexp.MustCompile(`^Found ([0-9]+) clones?\.$`)
	timeLine    = regexp.MustCompile(`^time: [0-9.]+(?:ms|s|µs|us|ns|m)$`)
)

// parseStdout reads the xcode reporter of jscpd 5.4 with absolute paths
// masked to "[REDACTED]/<rel>": one record per clone, the clone summary and
// the timing line. Both paths of a record must be files CODEYE listed.
func parseStdout(b []byte, listed map[string]bool) stream {
	st := stream{Summary: -1}
	var off int64
	for _, raw := range strings.SplitAfter(string(b), "\n") {
		start := off
		off += int64(len(raw))
		line := strings.TrimRight(raw, "\r\n")
		if line == "" {
			continue
		}
		if !strings.HasSuffix(raw, "\n") {
			// A record without its newline was cut.
			st.UnparsedBytes += int64(len(raw))
			continue
		}
		switch {
		case summaryLine.MatchString(line):
			n, _ := strconv.Atoi(summaryLine.FindStringSubmatch(line)[1])
			if st.Summary >= 0 {
				st.ShapeErrors++
			}
			st.Summary = n
			continue
		case timeLine.MatchString(line):
			continue
		}
		m := cloneLine.FindStringSubmatch(line)
		if m == nil {
			st.UnparsedBytes += int64(len(raw))
			continue
		}
		st.Records++
		file, ok1 := rel(m[1], listed)
		other, ok2 := rel(m[7], listed)
		nums := make([]int, 0, 5)
		for _, s := range []string{m[5], m[6], m[8], m[9], m[4]} {
			n, err := strconv.Atoi(s)
			if err != nil {
				n = -1
			}
			nums = append(nums, n)
		}
		if !ok1 || !ok2 || nums[0] < 1 || nums[1] < nums[0] || nums[2] < 1 || nums[3] < nums[2] || nums[4] < MinLines {
			st.ShapeErrors++
			continue
		}
		msg := strings.ReplaceAll(line[strings.Index(line, ": warning: ")+len(": warning: "):], analysis.RedactedMarker+"/", "")
		st.Clones = append(st.Clones, clone{File: file, Other: other, Start: nums[0], End: nums[1], OtherStart: nums[2], OtherEnd: nums[3], Lines: nums[4], Message: msg,
			Index: st.Records - 1, ByteStart: start, ByteEnd: start + int64(len(strings.TrimRight(raw, "\n")))})
	}
	return st
}

// rel maps a masked absolute path to a listed snapshot path.
func rel(p string, listed map[string]bool) (string, bool) {
	if !strings.HasPrefix(p, analysis.RedactedMarker+"/") {
		return "", false
	}
	r := strings.TrimPrefix(p, analysis.RedactedMarker+"/")
	return r, listed[r]
}

var (
	configLine  = regexp.MustCompile(`^Using config from .+$`)
	noFilesLine = regexp.MustCompile(`^Warning: jscpd analyzed no files: .*$`)
)

// parseStderr accepts jscpd's config notice and the "analyzed no files"
// warning; anything else (unknown config keys, errors) is reported.
func parseStderr(b []byte) (noFiles, other bool) {
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimRight(l, "\r")
		switch {
		case strings.TrimSpace(l) == "", configLine.MatchString(l):
		case noFilesLine.MatchString(l):
			noFiles = true
		default:
			other = true
		}
	}
	return noFiles, other
}

type assembleInput struct {
	TaskKey, ManifestDigest string
	Patterns                []string
	Files                   []source.Entry
	// Listed are the files handed to jscpd; Skipped the others with the
	// reason CODEYE did not hand them over.
	Listed        []string
	Skipped       map[string]string
	Version, Run  *executor.Result
	Executions    []analysis.Execution
	Drift         []string
	Resolver      *anypack.Resolver
	Cancelled     bool
	ScratchFailed bool
	Integrity     bool
	// Excluded: eligible files below a data directory, not compared.
	Excluded int
}

// assemble builds the envelope and decides the task outcome.
func assemble(in assembleInput) obsdoc.Document {
	doc := obsdoc.New(obsdoc.Analyzer{ID: AnalyzerID, Ecosystem: "any", Required: false}, obsdoc.Component{ID: Name, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = anypack.ModuleDir
	doc.Patterns = append([]string{}, in.Patterns...)
	doc.Tool.Name = Tool
	// --config replaces every repository jscpd configuration; the
	// "jscpd:ignore-start/-end" markers still hide blocks without a record
	// (measured), so in-source suppressions are honored.
	doc.RulePack = obsdoc.RulePack{ID: RulePackID, Version: RulePackVersion, Selection: append([]string{}, Selection...),
		RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	doc.Target.Properties = map[string]string{"metric": MetricDuplicate, "min_lines": strconv.Itoa(MinLines), "min_tokens": strconv.Itoa(MinTokens), "mode": Mode}
	doc.Coverage.Unit = "file"
	doc.Coverage.FilesInScope = anypack.Ptr(len(in.Files))
	doc.Executions = anypack.ExecutionsOf(in.Executions)
	lims := []string{LimMetricsNotDefects, LimSuppressionsHonored, LimColumnsNotRecorded, LimProcessedFromInput}
	doc.WorkspaceDrift = append([]string{}, in.Drift...)
	if len(in.Drift) > 0 {
		lims = append(lims, anypack.ReasonWorkspaceDrift)
	}
	units := map[string]int{}
	for _, why := range in.Skipped {
		units[why]++
	}
	if len(in.Skipped) > 0 {
		lims = append(lims, LimFilesSkipped)
	}
	doc.Coverage.FilesSkipped = len(in.Skipped)
	var o anypack.Outcome
	o.Executions(in.Cancelled, in.Version, in.Run)
	switch {
	case in.Integrity:
		o.Fail(domain.TaskFailed, anypack.ReasonSnapshotIntegrity, obsdoc.ExitNotRun)
	case in.ScratchFailed:
		o.Fail(domain.TaskFailed, analysis.ReasonHandlerError, obsdoc.ExitNotRun)
	case len(in.Listed) == 0:
		// Every file was too large or not UTF-8: nothing could be compared.
		o.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitNotRun)
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
	listed := map[string]bool{}
	for _, f := range in.Listed {
		listed[f] = true
	}
	var st stream
	parsed := false
	stdoutSHA := ""
	if in.Run != nil {
		st = parseStdout(in.Run.Stdout.Data, listed)
		stdoutSHA = obsdoc.SHA256Hex(in.Run.Stdout.Data)
		parsed = st.UnparsedBytes == 0 && st.ShapeErrors == 0 && st.Summary == len(st.Clones)
		doc.Parse.Records, doc.Parse.UnparsedBytes, doc.Parse.ShapeErrors = st.Records, st.UnparsedBytes, st.ShapeErrors
		if st.Summary != len(st.Clones) {
			doc.Parse.DecodeError = "clone summary does not match the records"
		}
		noFiles, other := parseStderr(in.Run.Stderr.Data)
		if noFiles {
			lims = append(lims, LimNoFileReachedLimits)
		}
		if !anypack.StreamsComplete(in.Run) {
			o.Fail(domain.TaskFailed, anypack.ReasonOutputTruncated, obsdoc.ExitExecutionError)
		}
		if anypack.ExitCode(in.Run) != 0 {
			// jscpd exits 0 with or without clones (no --exit-code and no
			// --threshold); 1 is an error such as a missing path.
			o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
		}
		if !parsed {
			o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
		}
		if other {
			doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: execRun, Kind: "tool_stderr", Message: "jscpd wrote unrecognized output to stderr"})
			o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
		}
	}
	ordinals := map[string]int{}
	for _, c := range st.Clones {
		pos, end := c.File+":"+strconv.Itoa(c.Start), c.File+":"+strconv.Itoa(c.End)
		tuple := strings.Join([]string{c.File, RuleDuplicate, pos, end, c.Message}, "\x00")
		ord := ordinals[tuple]
		ordinals[tuple]++
		pathPtr, loc, state := in.Resolver.Resolve(c.File, c.Start, c.End, 0, 0)
		rp, rl, rs := in.Resolver.Resolve(c.Other, c.OtherStart, c.OtherEnd, 0, 0)
		doc.Observations = append(doc.Observations, obsdoc.Observation{ID: obsdoc.ObservationID(AnalyzerID, anypack.ModuleDir, c.File, RuleDuplicate, pos, end, c.Message, ord),
			RuleID: RuleDuplicate, Category: "maintainability", RawMessage: c.Message, Path: pathPtr, Location: loc, LocationState: state,
			Related: []obsdoc.Related{{RawMessage: "duplicate of this block", Path: rp, Location: rl, LocationState: rs}},
			RawRef:  obsdoc.RawRef{Execution: execRun, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: c.Index, ByteStart: c.ByteStart, ByteEnd: c.ByteEnd},
			Unit:    c.File, Metric: &obsdoc.Metric{Name: MetricDuplicate, Value: c.Lines, Threshold: MinLines, Unit: "lines"}})
	}
	// jscpd enumerates no analyzed files: the processed files are the ones
	// CODEYE handed over after removing what it silently skips (over 1 MiB,
	// not UTF-8).
	processed := append([]string{}, in.Listed...)
	sort.Strings(processed)
	doc.Coverage.ProcessedPaths = processed
	if len(processed) > 0 {
		units[UnitCompared] = len(processed)
	}
	doc.Coverage.Units = units
	lims = append(lims, anypack.RecordExclusions(&doc, in.Excluded)...)
	doc.Parse.OK = !o.Failed() && parsed
	o.Apply(&doc, st.Records > 0)
	doc.Limitations = lims
	return doc
}
