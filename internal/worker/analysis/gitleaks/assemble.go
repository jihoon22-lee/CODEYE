package gitleaks

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"runtime"
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

// Task reasons of the gitleaks adapter.
const (
	ReasonNotRun         = "gitleaks_not_run"
	ReasonFailed         = "gitleaks_failed"
	ReasonExitMismatch   = "exit_status_mismatch"
	ReasonScanIncomplete = "scan_incomplete"
)

// Limitation codes.
const (
	LimHistoryNotScanned    = "history_not_scanned"
	LimSecretValuesWithheld = "secret_values_withheld"
	LimIgnoreFileHonored    = "repository_ignore_file_honored"
	LimBinaryNotClaimed     = "binary_files_not_claimed_processed"
)

// Coverage unit states (files).
const (
	UnitScannedText = "scanned_text"
	UnitEmpty       = "skipped_empty"
	UnitAllowlisted = "skipped_default_allowlist"
	UnitNotText     = "not_text"
)

// RuleIDPattern bounds rule IDs of the CODEYE configuration (gitleaks'
// built-in rule names).
var RuleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// Message is the fixed observation text of a rule: it names the rule and
// carries no secret material, no entropy and no position, so the
// fingerprint (analyzer, rule, path, message) never depends on the secret.
func Message(rule string) string {
	return "secret detected by gitleaks rule " + rule + " (value withheld)"
}

// record is one template record.
type record struct {
	Rule        *string  `json:"rule"`
	File        *string  `json:"file"`
	Symlink     *string  `json:"symlink"`
	StartLine   *int     `json:"start_line"`
	EndLine     *int     `json:"end_line"`
	StartColumn *int     `json:"start_column"`
	EndColumn   *int     `json:"end_column"`
	Entropy     *float64 `json:"entropy"`
}

type trailer struct {
	End      *bool `json:"codeye_end"`
	Findings *int  `json:"findings"`
}

type finding struct {
	Rule, File                                 string
	StartLine, EndLine, StartColumn, EndColumn int
	Index                                      int
	ByteStart, ByteEnd                         int64
}

type stream struct {
	Findings      []finding
	Trailer       int // -1 when missing
	Records       int
	UnparsedBytes int64
	ShapeErrors   int
}

// parseStdout reads the CODEYE template output strictly: one object per
// line, known fields only, the trailer last.
func parseStdout(b []byte) stream {
	st := stream{Trailer: -1}
	var off int64
	for _, raw := range strings.SplitAfter(string(b), "\n") {
		start := off
		off += int64(len(raw))
		line := strings.TrimRight(raw, "\r\n")
		if line == "" {
			continue
		}
		if !strings.HasSuffix(raw, "\n") || st.Trailer >= 0 {
			st.UnparsedBytes += int64(len(raw))
			continue
		}
		if strings.HasPrefix(line, `{"codeye_end"`) {
			var t trailer
			if strictDecode(line, &t) != nil || t.End == nil || !*t.End || t.Findings == nil || *t.Findings < 0 {
				st.ShapeErrors++
				continue
			}
			st.Trailer = *t.Findings
			continue
		}
		var rec record
		if err := strictDecode(line, &rec); err != nil {
			st.UnparsedBytes += int64(len(raw))
			continue
		}
		st.Records++
		if rec.Rule == nil || rec.File == nil || rec.Symlink == nil || rec.StartLine == nil || rec.EndLine == nil || rec.StartColumn == nil || rec.EndColumn == nil || rec.Entropy == nil ||
			!RuleIDPattern.MatchString(*rec.Rule) || *rec.Symlink != "" || *rec.StartLine < 1 || *rec.EndLine < *rec.StartLine || *rec.StartColumn < 0 || *rec.EndColumn < 0 {
			st.ShapeErrors++
			continue
		}
		st.Findings = append(st.Findings, finding{Rule: *rec.Rule, File: *rec.File, StartLine: *rec.StartLine, EndLine: *rec.EndLine,
			StartColumn: *rec.StartColumn, EndColumn: *rec.EndColumn, Index: st.Records - 1, ByteStart: start, ByteEnd: start + int64(len(line))})
	}
	return st
}

func strictDecode(line string, v any) error {
	d := json.NewDecoder(strings.NewReader(line))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return errTrailing
	}
	return nil
}

var errTrailing = errors.New("trailing data after the record")

// stderr of gitleaks 8.30 with --no-color at log level info.
var (
	logLine     = regexp.MustCompile(`^[0-9]{1,2}:[0-9]{2}(?:AM|PM) (TRC|DBG|INF|WRN|ERR|FTL) (.*)$`)
	scannedLine = regexp.MustCompile(`^scanned ~([0-9]+) bytes \(.*\) in .+$`)
	leaksLine   = regexp.MustCompile(`^leaks found: ([0-9]+)$`)
)

type logs struct {
	Scanned bool
	NoLeaks bool
	Leaks   int // -1 when not reported
	Other   int // any other line (skipped files, partial scan, fatal errors)
}

func parseStderr(b []byte) logs {
	l := logs{Leaks: -1}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := logLine.FindStringSubmatch(line)
		switch {
		case m == nil:
			l.Other++
		case m[1] == "INF" && scannedLine.MatchString(m[2]):
			l.Scanned = true
		case m[1] == "INF" && m[2] == "no leaks found":
			l.NoLeaks = true
		case m[1] == "WRN" && leaksLine.MatchString(m[2]):
			l.Leaks, _ = strconv.Atoi(leaksLine.FindStringSubmatch(m[2])[1])
		default:
			l.Other++
		}
	}
	return l
}

type scan struct {
	Root, Exec string
	Result     *executor.Result
}

type assembleInput struct {
	TaskKey, ManifestDigest string
	Patterns                []string
	Files                   []source.Entry
	Roots                   []string
	Text                    map[string]anypack.TextState
	Version                 *executor.Result
	Scans                   []scan
	Executions              []analysis.Execution
	Drift                   []string
	Resolver                *anypack.Resolver
	Cancelled               bool
	ScratchFailed           bool
	// Integrity: a selected snapshot file could not be read or does not
	// match its manifest SHA-256; nothing was started.
	Integrity bool
}

// assemble builds the envelope and decides the task outcome.
func assemble(in assembleInput) obsdoc.Document {
	doc := obsdoc.New(obsdoc.Analyzer{ID: AnalyzerID, Ecosystem: "any", Required: false}, obsdoc.Component{ID: Name, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = anypack.ModuleDir
	doc.Patterns = append([]string{}, in.Patterns...)
	doc.Tool.Name = Tool
	byPath := map[string]source.Entry{}
	for _, f := range in.Files {
		byPath[f.Path] = f
	}
	// --config replaces a repository .gitleaks.toml and --ignore-gitleaks-allow
	// turns inline gitleaks:allow off, but gitleaks always reads the
	// .gitleaksignore of a scan root (measured): repository configuration is
	// honored and bound to that file's manifest digest.
	configFiles := []obsdoc.ConfigFile{}
	for _, root := range in.Roots {
		if e, ok := byPath[IgnoreFile(root)]; ok {
			configFiles = append(configFiles, obsdoc.ConfigFile{Path: e.Path, SHA256: e.SHA256})
		}
	}
	sort.Slice(configFiles, func(a, b int) bool { return configFiles[a].Path < configFiles[b].Path })
	doc.RulePack = obsdoc.RulePack{ID: RulePackID, Version: RulePackVersion, Selection: append([]string{}, Selection...),
		RepositoryConfig: obsdoc.RepoConfigHonored, SourceSuppressions: obsdoc.SuppressDisabled, ConfigFiles: configFiles, UnreportedSuppressionRules: []string{}}
	doc.Target.Properties = map[string]string{"source": "workspace_directory", "history": "not_scanned", "archive_depth": "0", "decode_depth": "5"}
	doc.Coverage.Unit = "file"
	doc.Coverage.FilesInScope = anypack.Ptr(len(in.Files))
	doc.Executions = anypack.ExecutionsOf(in.Executions)
	lims := []string{LimHistoryNotScanned, LimSecretValuesWithheld}
	if len(configFiles) > 0 {
		lims = append(lims, LimIgnoreFileHonored)
	}
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
	case in.ScratchFailed:
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
	if len(in.Scans) != len(in.Roots) || len(in.Roots) == 0 {
		o.Fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
	}
	if len(in.Drift) > 0 {
		o.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	records := 0
	ordinals := map[string]int{}
	for _, s := range in.Scans {
		res := s.Result
		if res == nil {
			continue
		}
		st := parseStdout(res.Stdout.Data)
		lg := parseStderr(res.Stderr.Data)
		records += st.Records
		doc.Parse.Records += st.Records
		doc.Parse.UnparsedBytes += st.UnparsedBytes
		doc.Parse.ShapeErrors += st.ShapeErrors
		parsed := st.UnparsedBytes == 0 && st.ShapeErrors == 0 && st.Trailer == len(st.Findings)
		if st.Trailer != len(st.Findings) {
			doc.Parse.DecodeError = "report trailer does not match the records"
		}
		if !anypack.StreamsComplete(res) {
			o.Fail(domain.TaskFailed, anypack.ReasonOutputTruncated, obsdoc.ExitExecutionError)
		}
		code := anypack.ExitCode(res)
		switch {
		case code == 1, lg.Other > 0:
			// A fatal error, a skipped file or directory, or a partial scan
			// (all exit 1 or log a warning): the scan did not cover the root.
			doc.ToolErrors = append(doc.ToolErrors, obsdoc.ToolError{Execution: s.Exec, Kind: "scan_incomplete", Unit: s.Root, Message: "gitleaks reported an error or a skipped path for this scan root"})
			o.Fail(domain.TaskFailed, ReasonScanIncomplete, obsdoc.ExitExecutionError)
		case code != 0 && code != ExitCodeLeaks:
			o.Fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
		case !parsed:
			o.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
		case !lg.Scanned,
			code == 0 && (!lg.NoLeaks || len(st.Findings) != 0),
			code == ExitCodeLeaks && (lg.Leaks != len(st.Findings) || len(st.Findings) == 0):
			// The exit status, the log summary and the records must agree.
			o.Fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
		}
		stdoutSHA := obsdoc.SHA256Hex(res.Stdout.Data)
		for _, f := range st.Findings {
			file := f.File
			if runtime.GOOS == "windows" {
				file = filepath.ToSlash(file)
			}
			file = strings.TrimPrefix(file, "./")
			msg := Message(f.Rule)
			pos := file + ":" + strconv.Itoa(f.StartLine) + ":" + strconv.Itoa(f.StartColumn)
			end := file + ":" + strconv.Itoa(f.EndLine) + ":" + strconv.Itoa(f.EndColumn)
			tuple := strings.Join([]string{file, f.Rule, pos, end, msg}, "\x00")
			ord := ordinals[tuple]
			ordinals[tuple]++
			var pathPtr *string
			var loc *obsdoc.Location
			state := obsdoc.LocationOutsideSnapshot
			if domain.ValidateEvidencePath(file) == nil {
				pathPtr, loc, state = in.Resolver.Resolve(file, f.StartLine, f.EndLine, f.StartColumn, f.EndColumn)
			}
			if state == obsdoc.LocationOutsideSnapshot {
				pathPtr = nil
			}
			doc.Observations = append(doc.Observations, obsdoc.Observation{ID: obsdoc.ObservationID(AnalyzerID, anypack.ModuleDir, file, f.Rule, pos, end, msg, ord),
				RuleID: f.Rule, Category: "security", RawMessage: msg, Path: pathPtr, Location: loc, LocationState: state, Related: []obsdoc.Related{},
				RawRef: obsdoc.RawRef{Execution: s.Exec, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: f.Index, ByteStart: f.ByteStart, ByteEnd: f.ByteEnd},
				Unit:   file})
		}
	}
	// processed_paths are claimed fail-closed: only non-empty valid UTF-8
	// text without NUL bytes outside the default allowlist, which gitleaks
	// reads and scans. Binary content it may skip by type is not claimed.
	units := map[string]int{}
	processed := []string{}
	for _, f := range in.Files {
		switch {
		case f.Size != nil && *f.Size == 0:
			units[UnitEmpty]++
		case Allowlisted(f.Path):
			units[UnitAllowlisted]++
		case in.Text[f.Path] != anypack.TextUTF8:
			units[UnitNotText]++
		default:
			units[UnitScannedText]++
			processed = append(processed, f.Path)
		}
	}
	if units[UnitNotText] > 0 {
		lims = append(lims, LimBinaryNotClaimed)
	}
	doc.Coverage.Units = units
	doc.Coverage.FilesSkipped = units[UnitEmpty] + units[UnitAllowlisted]
	doc.Coverage.ProcessedPaths = processed
	if !o.Failed() && len(processed) == 0 {
		o.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitConfigurationError)
	}
	doc.Parse.OK = !o.Failed()
	o.Apply(&doc, records > 0)
	if doc.Outcome.ExitClass == obsdoc.ExitNotRun {
		doc.Coverage.Units = map[string]int{}
		doc.Coverage.FilesSkipped = 0
	}
	doc.Limitations = lims
	return doc
}
