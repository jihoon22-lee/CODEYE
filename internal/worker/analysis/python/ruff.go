package python

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Ruff identity: the required baseline of the Python ecosystem (docs/ANALYZERS.md#python-팩).
const (
	RuffID             = "python.ruff"
	RuffName           = "ruff"
	RuffTool           = "ruff"
	RuffAdapterVersion = "1"
	RuffParserID       = "ruff-json-lines-v1"
	RuffParserVersion  = "1"
	RuffRulePackID     = "codeye-python-ruff"
	RuffRulePackVer    = "1"
	// RuffTestedSeries is the ruff minor series the captured outputs pin.
	RuffTestedSeries = "0.16"
)

// RuffRules is the CODEYE rule selection (--select): correctness and error
// flow, no style. E711-E714/E721 comparisons, E722 bare except, E9 IO and
// syntax, F pyflakes, B bugbear, BLE blind except, PLE pylint errors, ASYNC.
// S (flake8-bandit) is not selected: bandit is the security engine of the
// pack and ruff's S rules re-implement its checks (docs/ANALYZERS.md#python-엔진-중복).
const RuffRules = "E711,E712,E713,E714,E721,E722,E9,F,B,BLE,PLE,ASYNC"

// RuffIgnore drops the bugbear rules that only demand an explicit strict=
// argument (a habit, not a defect).
const RuffIgnore = "B905,B911,B912"

// RuffSelection is the rule-pack part of the ruff argv. --isolated ignores
// every configuration file (repository, user), --no-respect-gitignore keeps
// the file set independent of the repository's ignore files, --unfixable ALL
// keeps fix edits (rewritten source text) out of the captured stdout.
var RuffSelection = []string{"--isolated", "--no-respect-gitignore", "--select", RuffRules, "--ignore", RuffIgnore, "--unfixable", "ALL"}

// Ruff execution names, in run order.
const (
	execRuffVersion = "ruff.version"
	execRuffFiles   = "ruff.files"
	execRuffCheck   = "ruff.check"
	execRuffAll     = "ruff.check-ignore-noqa"
)

// ruffExcludes are the --extend-exclude patterns: the pack's skipped
// directories and hidden directories (bare names match at any depth),
// notebooks (cells are not file lines) and the module's nested modules
// ("./" anchors a pattern at the module directory; measured with 0.16.3).
func ruffExcludes(nested []string) string {
	pats := append([]string{".*", "*.ipynb"}, ExcludedDirs...)
	for _, n := range nested {
		pats = append(pats, "./"+n)
	}
	return strings.Join(pats, ",")
}

// RuffDescriptor is ruff's pack identity: the Python baseline (required).
func RuffDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: RuffID, Name: RuffName, Ecosystem: Ecosystem, Required: true, Tool: RuffTool, VersionArgs: []string{"--version"}, ParseVersion: ParseRuffVersion}
}

// RuffPlan is ruff's tool-independent planner.
var RuffPlan = planFor(RuffDescriptor())

var ruffVersion = regexp.MustCompile(`^ruff ([0-9]+\.[0-9]+\.[0-9]+)$`)

// ParseRuffVersion reads `ruff --version` ("ruff 0.16.3").
func ParseRuffVersion(stdout []byte) (string, bool) {
	m := ruffVersion.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

func seriesTested(series string) func(string) bool {
	return func(v string) bool { return v == series || strings.HasPrefix(v, series+".") }
}

// Ruff is the configured ruff adapter.
type Ruff struct{ base }

var _ pack.Analyzer = (*Ruff)(nil)

// NewRuff validates the configuration without running anything.
func NewRuff(cfg Config) (*Ruff, error) {
	b, err := newBase(RuffDescriptor(), cfg)
	if err != nil {
		return nil, err
	}
	return &Ruff{base: b}, nil
}

// Run implements pack.Analyzer.
func (a *Ruff) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	return a.run(ctx, job, task, runner, ruffRun{})
}

type ruffRun struct{}

// RuffArgs is the ruff check argv of a module (after the executable) for a
// Python 3 minor target: the selection, the file exclusions and the targets.
func RuffArgs(minor int, nested, targets []string, extra ...string) []string {
	args := append([]string{"check"}, RuffSelection...)
	args = append(args, "--no-cache", "--force-exclude", "--extend-exclude", ruffExcludes(nested), "--target-version", "py3"+strconv.Itoa(minor))
	args = append(args, extra...)
	return append(append(args, "--"), dotTargets(targets)...)
}

func ruffMinor(t pyTarget) int { n, _ := clampMinor(t.Minor, 7, 15); return n }

func (ruffRun) execute(rc *runCtx) {
	m := rc.p.Module
	minor := ruffMinor(rc.target)
	// The cache stays in task scratch; --no-cache disables reads and the
	// measured run writes nothing, the variable bounds any write.
	rc.extraEnv["RUFF_CACHE_DIR"] = rc.dirs["cache"]
	if !safeNested(m.Nested) {
		rc.refused = ReasonUnsafeModuleName
		return
	}
	if r := rc.exec(execRuffFiles, RuffArgs(minor, m.Nested, m.Targets, "--show-files"), nil); r == nil || !exitedZero(r) {
		return
	}
	// Two runs: the honored run gives the active diagnostics, the
	// --ignore-noqa run every diagnostic; the difference is what the
	// project suppressed in source (docs/ANALYZERS.md#python-소스-안-억제).
	rc.exec(execRuffCheck, RuffArgs(minor, m.Nested, m.Targets, "--output-format", "json-lines"), nil)
	rc.exec(execRuffAll, RuffArgs(minor, m.Nested, m.Targets, "--output-format", "json-lines", "--ignore-noqa"), nil)
}

// ruffPos is a ruff {"row","column"} (1-based row, 1-based code point column).
type ruffPos struct {
	Row    *int `json:"row"`
	Column *int `json:"column"`
}

// ruffRecord is one json-lines record of ruff 0.16. Unknown fields are a
// shape error.
type ruffRecord struct {
	Cell        json.RawMessage `json:"cell"`
	Code        *string         `json:"code"`
	EndLocation *ruffPos        `json:"end_location"`
	Filename    *string         `json:"filename"`
	Fix         json.RawMessage `json:"fix"`
	Location    *ruffPos        `json:"location"`
	Message     *string         `json:"message"`
	Name        *string         `json:"name"`
	NoqaRow     json.RawMessage `json:"noqa_row"`
	Severity    *string         `json:"severity"`
	URL         json.RawMessage `json:"url"`
}

type ruffDiag struct {
	el                   element
	code, file, msg, sev string
	row, col, erow, ecol int
	notebook             bool
}

func (d ruffDiag) key() string {
	return strings.Join([]string{d.file, d.code, strconv.Itoa(d.row), strconv.Itoa(d.col), strconv.Itoa(d.erow), strconv.Itoa(d.ecol), d.msg}, "\x00")
}

func (p *ruffPos) ok() bool {
	return p != nil && p.Row != nil && p.Column != nil && *p.Row >= 0 && *p.Column >= 0
}

// parseRuff reads a json-lines stream into diagnostics; shape errors count
// values that are not ruff records.
func parseRuff(stdout []byte) (diags []ruffDiag, values, shape int, unparsed int64, decodeErr string) {
	ls := parseJSONLines(stdout)
	for _, el := range ls.Values {
		var r ruffRecord
		if err := decodeStrict(el.Raw, &r); err != nil || r.Code == nil || *r.Code == "" || r.Filename == nil || r.Message == nil || !r.Location.ok() || !r.EndLocation.ok() {
			shape++
			continue
		}
		d := ruffDiag{el: el, code: *r.Code, file: *r.Filename, msg: *r.Message, row: *r.Location.Row, col: *r.Location.Column, erow: *r.EndLocation.Row, ecol: *r.EndLocation.Column,
			notebook: len(r.Cell) > 0 && string(r.Cell) != "null"}
		if r.Severity != nil {
			d.sev = *r.Severity
		}
		diags = append(diags, d)
	}
	return diags, len(ls.Values), shape, ls.UnparsedBytes, ls.DecodeError
}

// ruffCategory is the policy codeye-python-ruff v1: rule family → report
// category. It describes what the rule is about, not its impact.
func ruffCategory(code string) string {
	switch {
	case code == "E722" || strings.HasPrefix(code, "BLE") || code == "B012" || code == "B014" || code == "B025" || code == "B029" || code == "B030" || code == "B904":
		return "error_handling"
	case strings.HasPrefix(code, "ASYNC"):
		return "concurrency"
	case code == "F401" || code == "F811" || code == "F841" || code == "F842" || code == "B007" || code == "B018":
		return "maintainability"
	}
	return "correctness"
}

// ruffSyntax reports a ruff record that is a parse failure of the file.
func ruffSyntax(code string) bool {
	return code == "invalid-syntax" || code == "E999" || code == "E902"
}

func (ruffRun) assemble(rc *runCtx) obsdoc.Document {
	rp := obsdoc.RulePack{ID: RuffRulePackID, Version: RuffRulePackVer, Selection: append([]string{}, RuffSelection...), RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressReported, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	bd := newBuilder(rc, obsdoc.Component{ID: RuffName, Version: RuffAdapterVersion}, obsdoc.Component{ID: RuffParserID, Version: RuffParserVersion}, rp)
	bd.identify(seriesTested(RuffTestedSeries))
	minor := bd.targetVersion(7, 15)
	bd.doc.Target.Properties["target_version"] = "py3" + strconv.Itoa(minor)
	bd.executions(execRuffVersion, execRuffFiles, execRuffCheck, execRuffAll)

	// --show-files: the files ruff analyzes with the same settings.
	var processed []string
	if r := rc.results[execRuffFiles]; r != nil && exitedZero(r) && streamsComplete(r) {
		processed = []string{}
		sc := bufio.NewScanner(bytes.NewReader(r.Stdout.Data))
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			if line == "" {
				continue
			}
			rel, ok := bd.toolPath(line)
			if !ok {
				processed = nil
				bd.unparsed()
				break
			}
			if isSource(rel) {
				processed = append(processed, rel)
			}
		}
		if sc.Err() != nil {
			processed = nil
			bd.unparsed()
		}
	} else if r != nil && r.Status == executor.StatusExited {
		bd.fail(domain.TaskFailed, "ruff_failed", obsdoc.ExitConfigurationError)
	}

	active, all := rc.results[execRuffCheck], rc.results[execRuffAll]
	var honored, every []ruffDiag
	syntax := map[string]bool{}
	for i, r := range []*executor.Result{active, all} {
		if r == nil {
			continue
		}
		diags, values, shape, unparsed, decodeErr := parseRuff(r.Stdout.Data)
		if i == 1 {
			bd.doc.Parse.Records, bd.doc.Parse.UnparsedBytes, bd.doc.Parse.ShapeErrors, bd.doc.Parse.DecodeError = values, unparsed, shape, decodeErr
			every = diags
		} else {
			honored = diags
		}
		if values > 0 || unparsed > 0 {
			bd.output = true
		}
		if shape > 0 || unparsed > 0 || decodeErr != "" {
			bd.unparsed()
		}
		switch code := exitCode(r); {
		case r.Status != executor.StatusExited:
		case code == 0 && len(diags) == 0, code == 1 && len(diags) > 0:
		case code == 0 || code == 1:
			// Exit 1 iff ruff reported a diagnostic.
			bd.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
		default:
			// Exit 2: configuration, usage or internal error.
			bd.fail(domain.TaskFailed, "ruff_failed", obsdoc.ExitConfigurationError)
		}
		if len(bytes.TrimSpace(r.Stderr.Data)) > 0 {
			bd.limit(LimStderrOutput)
		}
	}
	// The honored run's diagnostics must all appear in the --ignore-noqa
	// run; the rest of that run is what in-source suppressions hide.
	count := map[string]int{}
	for _, d := range honored {
		count[d.key()]++
	}
	stdoutSHA := ""
	if all != nil {
		stdoutSHA = obsdoc.SHA256Hex(all.Stdout.Data)
	}
	for _, d := range every {
		suppressed := true
		if count[d.key()] > 0 {
			count[d.key()]--
			suppressed = false
		}
		ref := obsdoc.RawRef{Execution: execRuffAll, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: d.el.Index, ByteStart: d.el.Start, ByteEnd: d.el.End}
		if ruffSyntax(d.code) {
			// A file ruff could not parse is a tool error, not a defect
			// observation; the task cannot succeed (docs/ANALYZERS.md#python-규칙-팩).
			bd.toolError(execRuffAll, "syntax", d.file, d.row, d.col, d.msg)
			if rel, ok := bd.toolPath(d.file); ok {
				syntax[rel] = true
			}
			continue
		}
		if d.notebook {
			bd.limit("notebook_cells_not_mapped")
		}
		bd.observe(d.code, ruffCategory(d.code), d.sev, d.msg, d.file, place{StartLine: d.row, StartCol: d.col, EndLine: d.erow, EndCol: d.ecol, Unit: unitRune}, ref, suppressed)
	}
	for _, n := range count {
		if n > 0 {
			bd.fail(domain.TaskFailed, ReasonSuppressionDiff, obsdoc.ExitExecutionError)
		}
	}
	return bd.finish(processed, syntax)
}
