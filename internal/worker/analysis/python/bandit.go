package python

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// bandit identity: the security engine of the pack (extra depth).
const (
	BanditID             = "python.bandit"
	BanditName           = "bandit"
	BanditTool           = "bandit"
	BanditAdapterVersion = "1"
	BanditParserID       = "bandit-csv-v1"
	BanditParserVersion  = "1"
	BanditRulePackID     = "codeye-python-bandit"
	BanditRulePackVer    = "1"
	BanditTestedSeries   = "1.9"
)

// BanditSkips are the tests CODEYE does not run (docs/42 §7): B101
// assert_used (flags every pytest assertion); B105-B107 hardcoded
// passwords (the message carries the literal value into report text;
// secrets belong to the redacting secret pack); B613 trojansource (ruff
// PLE2502 reports the same bidirectional control characters, measured).
const BanditSkips = "B101,B105,B106,B107,B613"

// BanditSelection is the rule-pack part of the bandit argv. The CODEYE
// --ini file replaces a project .bandit file (bandit reads it from a "."
// target otherwise, measured); no -c, so no YAML or pyproject
// configuration is read.
var BanditSelection = []string{"--ini=codeye-managed", "--skip", BanditSkips}

// bandit execution names, in run order.
const (
	execBanditVersion = "bandit.version"
	execBanditCheck   = "bandit.check"
	execBanditAll     = "bandit.check-ignore-nosec"
)

// BanditDescriptor is bandit's pack identity (extra depth, not required).
func BanditDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: BanditID, Name: BanditName, Ecosystem: Ecosystem, Required: false, Tool: BanditTool, VersionArgs: []string{"--version"}, ParseVersion: ParseBanditVersion}
}

// BanditPlan is bandit's tool-independent planner.
var BanditPlan = planFor(BanditDescriptor())

var (
	banditVersion = regexp.MustCompile(`^bandit ([0-9]+\.[0-9]+\.[0-9]+)$`)
	banditPython  = regexp.MustCompile(`^python version = ([0-9]+\.[0-9]+)\.[0-9]+`)
)

// ParseBanditVersion reads the first line of `bandit --version`
// ("bandit 1.9.4"; the next line names bandit's own Python).
func ParseBanditVersion(stdout []byte) (string, bool) {
	first, _, _ := strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	m := banditVersion.FindStringSubmatch(strings.TrimSpace(first))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// banditParserPython is the Python version whose ast bandit parses with.
func banditParserPython(stdout []byte) string {
	for _, l := range strings.Split(string(stdout), "\n") {
		if m := banditPython.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			return m[1]
		}
	}
	return ""
}

// Bandit is the configured bandit adapter.
type Bandit struct{ base }

var _ pack.Analyzer = (*Bandit)(nil)

// NewBandit validates the configuration without running anything.
func NewBandit(cfg Config) (*Bandit, error) {
	b, err := newBase(BanditDescriptor(), cfg)
	if err != nil {
		return nil, err
	}
	return &Bandit{base: b}, nil
}

// Run implements pack.Analyzer.
func (a *Bandit) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	return a.run(ctx, job, task, runner, banditRun{})
}

type banditRun struct{}

// banditExcludes are the -x patterns. bandit excludes a path that matches a
// glob or contains a pattern as a substring, and turns a pattern naming an
// existing directory into <dir>/*; the "*/<name>/*" globs match path
// segments only and never name a directory.
func banditExcludes(nested []string) string {
	pats := []string{"*/.*"}
	for _, d := range ExcludedDirs {
		pats = append(pats, "*/"+d+"/*")
	}
	for _, n := range nested {
		pats = append(pats, "./"+n+"/*")
	}
	return strings.Join(pats, ",")
}

// BanditArgs is the bandit argv after the executable. format is "csv"
// (results on stdout) or "json" with -o in extra.
func BanditArgs(ini, format string, nested, targets []string, extra ...string) []string {
	args := []string{"-r", "-f", format, "-q", "--ini", ini, "--skip", BanditSkips, "-x", banditExcludes(nested)}
	args = append(args, extra...)
	return append(append(args, "--"), dotTargets(targets)...)
}

func (banditRun) execute(rc *runCtx) {
	m := rc.p.Module
	if !safeNested(m.Nested) {
		rc.refused = ReasonUnsafeModuleName
		return
	}
	ini := filepath.Join(rc.dirs["cfg"], "bandit.ini")
	if err := analysis.WriteFileExclusive(ini, []byte("[bandit]\n")); err != nil {
		rc.writeFail = true
		return
	}
	// Two runs, as for ruff: the honored run's results are active, the rest
	// of the --ignore-nosec run is what # nosec hides (docs/42 §8). bandit's
	// JSON carries the flagged source lines ("code", even with -n 0), so it
	// goes to a scratch file (files, errors, honored results) and never into
	// a captured stream; the stream observations come from the code-free
	// CSV of the --ignore-nosec run.
	rc.exec(execBanditCheck, BanditArgs(ini, "json", m.Nested, m.Targets, "-o", filepath.Join(rc.dirs["report"], "bandit.json")), nil)
	rc.exec(execBanditAll, BanditArgs(ini, "csv", m.Nested, m.Targets, "--ignore-nosec"), nil)
}

// banditCSVHeader is the bandit 1.9 CSV header.
var banditCSVHeader = []string{"filename", "test_name", "test_id", "issue_severity", "issue_confidence", "issue_cwe", "issue_text", "line_number", "col_offset", "end_col_offset", "line_range", "more_info"}

// banditResult is one results element of the bandit 1.9 JSON report.
type banditResult struct {
	Code            *string         `json:"code"`
	ColOffset       *int            `json:"col_offset"`
	EndColOffset    *int            `json:"end_col_offset"`
	Filename        *string         `json:"filename"`
	IssueConfidence *string         `json:"issue_confidence"`
	IssueCWE        json.RawMessage `json:"issue_cwe"`
	IssueSeverity   *string         `json:"issue_severity"`
	IssueText       *string         `json:"issue_text"`
	LineNumber      *int            `json:"line_number"`
	LineRange       []int           `json:"line_range"`
	MoreInfo        *string         `json:"more_info"`
	TestID          *string         `json:"test_id"`
	TestName        *string         `json:"test_name"`
}

type banditError struct {
	Filename *string `json:"filename"`
	Reason   *string `json:"reason"`
}

// banditFinding is one result: from the JSON report (honored run) or one
// CSV record of stdout (with its byte range).
type banditFinding struct {
	index                    int
	start, end               int64
	file, test, text, sev    string
	line, col, endLine, endC int
}

func (f banditFinding) key() string {
	return strings.Join([]string{f.file, f.test, strconv.Itoa(f.line), strconv.Itoa(f.col), strconv.Itoa(f.endC), f.text}, "\x00")
}

// banditReport is the scratch JSON report of the honored run.
type banditReport struct {
	findings []banditFinding
	errors   []banditError
	metrics  []string
	ok       bool
}

func readBanditReport(p string) banditReport {
	var out banditReport
	b, err := os.ReadFile(p)
	if err != nil || len(b) > DefaultStdoutLimit {
		return out
	}
	var doc struct {
		Errors      []banditError              `json:"errors"`
		GeneratedAt *string                    `json:"generated_at"`
		Metrics     map[string]json.RawMessage `json:"metrics"`
		Results     []banditResult             `json:"results"`
	}
	if decodeStrict(b, &doc) != nil || doc.GeneratedAt == nil || doc.Metrics["_totals"] == nil || doc.Errors == nil || doc.Results == nil {
		return out
	}
	for _, e := range doc.Errors {
		if e.Filename == nil || e.Reason == nil {
			return out
		}
	}
	for k := range doc.Metrics {
		if k != "_totals" {
			out.metrics = append(out.metrics, path.Clean(k))
		}
	}
	for _, r := range doc.Results {
		if r.Filename == nil || r.TestID == nil || r.IssueText == nil || r.LineNumber == nil || r.ColOffset == nil || r.EndColOffset == nil || r.IssueSeverity == nil {
			return out
		}
		out.findings = append(out.findings, banditFinding{file: path.Clean(*r.Filename), test: *r.TestID, text: *r.IssueText, sev: *r.IssueSeverity,
			line: *r.LineNumber, col: *r.ColOffset, endC: *r.EndColOffset})
	}
	out.errors, out.ok = doc.Errors, true
	return out
}

type banditCSV struct {
	findings []banditFinding
	records  int
	shape    int
	unparsed int64
	decode   string
}

// parseBanditCSV reads the CSV stdout: the exact header, then one record
// per result with its byte range (without the line terminator).
func parseBanditCSV(stdout []byte) banditCSV {
	var out banditCSV
	if len(stdout) == 0 {
		out.decode = "empty"
		return out
	}
	r := csv.NewReader(bytes.NewReader(stdout))
	r.FieldsPerRecord = len(banditCSVHeader)
	head, err := r.Read()
	if err != nil || !slices.Equal(head, banditCSVHeader) {
		out.decode, out.unparsed = "csv_header", int64(len(stdout))
		return out
	}
	for {
		start := r.InputOffset()
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			out.decode, out.unparsed = "csv_record", int64(len(stdout))-start
			return out
		}
		end := r.InputOffset()
		for end > start && (stdout[end-1] == '\n' || stdout[end-1] == '\r') {
			end--
		}
		f := banditFinding{index: out.records, start: start, end: end, file: path.Clean(rec[0]), test: rec[2], sev: rec[3], text: rec[6]}
		out.records++
		var lines []int
		var e1, e2, e3 error
		f.line, e1 = strconv.Atoi(rec[7])
		f.col, e2 = strconv.Atoi(rec[8])
		f.endC, e3 = strconv.Atoi(rec[9])
		if e1 != nil || e2 != nil || e3 != nil || json.Unmarshal([]byte(rec[10]), &lines) != nil || f.test == "" || f.file == "" {
			out.shape++
			continue
		}
		f.endLine = f.line
		for _, l := range lines {
			f.endLine = max(f.endLine, l)
		}
		out.findings = append(out.findings, f)
	}
	return out
}

func (banditRun) assemble(rc *runCtx) obsdoc.Document {
	rp := obsdoc.RulePack{ID: BanditRulePackID, Version: BanditRulePackVer, Selection: append([]string{}, BanditSelection...), RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	bd := newBuilder(rc, obsdoc.Component{ID: BanditName, Version: BanditAdapterVersion}, obsdoc.Component{ID: BanditParserID, Version: BanditParserVersion}, rp)
	bd.identify(seriesTested(BanditTestedSeries))
	if v := rc.results[execBanditVersion]; v != nil && exitedZero(v) {
		if py := banditParserPython(v.Stdout.Data); py != "" {
			// bandit parses with its own interpreter; the module's target
			// version is recorded but cannot be applied.
			bd.doc.Target.Properties["parser_python_version"] = py
		}
	}
	bd.doc.Target.Properties["python_version_applied"] = "false"
	bd.executions(execBanditVersion, execBanditCheck, execBanditAll)

	honoredRun, allRun := rc.results[execBanditCheck], rc.results[execBanditAll]
	var report banditReport
	var every banditCSV
	syntax := map[string]bool{}
	var processed []string
	if honoredRun != nil && honoredRun.Status == executor.StatusExited {
		report = readBanditReport(filepath.Join(rc.dirs["report"], "bandit.json"))
		switch code := exitCode(honoredRun); {
		case !report.ok:
			bd.unparsed()
		case len(honoredRun.Stdout.Data) > 0:
			// -o sends the report to the file; anything on stdout is output
			// the parser does not know.
			bd.unparsed()
		case (code == 0) == (len(report.findings) == 0) && (code == 0 || code == 1):
		case code == 0 || code == 1:
			// Exit 1 iff a result was reported (file errors do not change
			// the exit code, measured).
			bd.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
		default:
			bd.fail(domain.TaskFailed, "bandit_failed", obsdoc.ExitConfigurationError)
		}
	}
	if allRun != nil && allRun.Status == executor.StatusExited {
		every = parseBanditCSV(allRun.Stdout.Data)
		bd.doc.Parse.Records, bd.doc.Parse.UnparsedBytes, bd.doc.Parse.ShapeErrors, bd.doc.Parse.DecodeError = every.records, every.unparsed, every.shape, every.decode
		if every.records > 0 || every.unparsed > 0 {
			bd.output = true
		}
		if every.shape > 0 || every.unparsed > 0 || every.decode != "" {
			bd.unparsed()
		}
		switch code := exitCode(allRun); {
		case (code == 0) == (every.records == 0) && (code == 0 || code == 1):
		case code == 0 || code == 1:
			bd.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
		default:
			bd.fail(domain.TaskFailed, "bandit_failed", obsdoc.ExitConfigurationError)
		}
	}
	if report.ok {
		// metrics names every file bandit opened, errors the ones it could
		// not parse or read.
		failedFiles := map[string]bool{}
		for _, e := range report.errors {
			file := path.Clean(*e.Filename)
			failedFiles[file] = true
			if !strings.Contains(*e.Reason, "syntax error") {
				// A file bandit could not read or decode is not a syntax
				// error of the code; the task still fails (a success
				// envelope carries no tool errors).
				bd.toolError(execBanditCheck, "file", file, 0, 0, *e.Reason)
				bd.fail(domain.TaskFailed, ReasonFileErrors, obsdoc.ExitExecutionError)
				continue
			}
			bd.toolError(execBanditCheck, "syntax", file, 0, 0, *e.Reason)
			if rel, ok := bd.toolPath(file); ok {
				syntax[rel] = true
			}
		}
		processed = []string{}
		for _, f := range report.metrics {
			if failedFiles[f] {
				continue
			}
			if rel, ok := bd.toolPath(f); ok {
				processed = append(processed, rel)
			}
		}
	}
	count := map[string]int{}
	for _, f := range report.findings {
		count[f.key()]++
	}
	sha := ""
	if allRun != nil {
		sha = obsdoc.SHA256Hex(allRun.Stdout.Data)
	}
	for _, f := range every.findings {
		suppressed := true
		if count[f.key()] > 0 {
			count[f.key()]--
			suppressed = false
		}
		ref := obsdoc.RawRef{Execution: execBanditAll, Stream: "stdout", StreamSHA256: sha, RecordIndex: f.index, ByteStart: f.start, ByteEnd: f.end}
		pl := place{StartLine: f.line, StartCol: f.col + 1, EndLine: f.endLine, EndCol: f.endC + 1, Unit: unitByte}
		bd.observe(f.test, "security", f.sev, f.text, f.file, pl, ref, suppressed)
	}
	for _, n := range count {
		if n > 0 && report.ok && every.decode == "" {
			bd.fail(domain.TaskFailed, ReasonSuppressionDiff, obsdoc.ExitExecutionError)
		}
	}
	return bd.finish(processed, syntax)
}
