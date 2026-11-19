package python

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// pyright identity: an extra-depth type checker, the alternative of mypy,
// run by the shared node toolchain (docs/ANALYZERS.md#공유-interpreter-toolchain).
const (
	PyrightID             = "python.pyright"
	PyrightName           = "pyright"
	PyrightTool           = "pyright"
	PyrightAdapterVersion = "1"
	PyrightParserID       = "pyright-outputjson-v1"
	PyrightParserVersion  = "1"
	PyrightRulePackID     = "codeye-python-pyright"
	PyrightRulePackVer    = "1"
	// PyrightTestedVersion is the exact release whose output the captures
	// pin (pyright releases weekly).
	PyrightTestedVersion = "1.1.414"
	// PyrightSyntaxRule is the rule of an observation of a pyright parser
	// error (pyright gives those no rule).
	PyrightSyntaxRule = "syntax"
	pyrightMinMinor   = 3
	pyrightMaxMinor   = 15
	// emptyVenv is the CODEYE-managed empty virtual environment pyright is
	// pointed at: with a venv configured it reads site-packages from the
	// directory and never starts a Python interpreter to ask for search
	// paths (it does without one, measured).
	emptyVenv = "codeye-empty"
)

// PyrightRuleOverrides turn off the rules that report the same construct
// as a selected ruff rule on the same line (measured with pyright 1.1.414
// and ruff 0.16.3, docs/ANALYZERS.md#python-엔진-중복): reportUndefinedVariable F821,
// reportRedeclaration F811, reportAssertAlwaysTrue F631,
// reportUnusedExpression B015/B018, reportUnsupportedDunderAll PLE0604.
var PyrightRuleOverrides = []string{"reportAssertAlwaysTrue", "reportRedeclaration", "reportUndefinedVariable", "reportUnsupportedDunderAll", "reportUnusedExpression"}

// PyrightSelection is the rule-pack selection recorded in the envelope:
// the CODEYE configuration file replaces the repository's
// (pyrightconfig.json, [tool.pyright]; measured), standard mode, and the
// overlap rules off.
var PyrightSelection = func() []string {
	s := []string{"config=codeye-managed", "typeCheckingMode=standard"}
	for _, r := range PyrightRuleOverrides {
		s = append(s, r+"=none")
	}
	return s
}()

// pyright execution names, in run order.
const (
	execPyrightVersion = "pyright.version"
	execPyrightCheck   = "pyright.check"
)

// PyrightDescriptor is pyright's pack identity: a node script.
func PyrightDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: PyrightID, Name: PyrightName, Ecosystem: Ecosystem, Required: false, Tool: PyrightTool, VersionArgs: []string{"--version"},
		ParseVersion: ParsePyrightVersion, Toolchains: []string{"node"}, Interpreter: "node"}
}

// PyrightPlan is pyright's tool-independent planner.
var PyrightPlan = planFor(PyrightDescriptor())

var pyrightVersion = regexp.MustCompile(`^pyright ([0-9]+\.[0-9]+\.[0-9]+)$`)

// ParsePyrightVersion reads `pyright --version` ("pyright 1.1.414").
func ParsePyrightVersion(stdout []byte) (string, bool) {
	m := pyrightVersion.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Pyright is the configured pyright adapter.
type Pyright struct{ base }

var _ pack.Analyzer = (*Pyright)(nil)

// NewPyright validates the configuration without running anything.
func NewPyright(cfg Config) (*Pyright, error) {
	b, err := newBase(PyrightDescriptor(), cfg)
	if err != nil {
		return nil, err
	}
	return &Pyright{base: b}, nil
}

// Run implements pack.Analyzer.
func (a *Pyright) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	return a.run(ctx, job, task, runner, pyrightRun{})
}

type pyrightRun struct{}

func pyrightPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "Darwin"
	}
	return "Linux"
}

// PyrightConfig is the CODEYE configuration file of one task. Paths are
// relative to the file's directory (pyright ignores absolute include paths,
// measured); root is the module directory.
func PyrightConfig(minor int, root string) []byte {
	cfg := map[string]any{"pythonVersion": "3." + strconv.Itoa(minor), "pythonPlatform": pyrightPlatform(), "typeCheckingMode": "standard",
		"venvPath": "venvs", "venv": emptyVenv, "executionEnvironments": []any{map[string]any{"root": root}}}
	for _, r := range PyrightRuleOverrides {
		cfg[r] = "none"
	}
	b, _ := json.Marshal(cfg)
	return append(b, '\n')
}

func (pyrightRun) execute(rc *runCtx) {
	m := rc.p.Module
	minor, _ := clampMinor(rc.target.Minor, pyrightMinMinor, pyrightMaxMinor)
	cfgDir := rc.dirs["cfg"]
	root, err := filepath.Rel(cfgDir, rc.moduleAbs)
	if err != nil {
		// No relative path from scratch to the workspace (another volume).
		rc.refused = ReasonConfigUnavailable
		return
	}
	site := filepath.Join(cfgDir, "venvs", emptyVenv, "lib", "python3."+strconv.Itoa(minor), "site-packages")
	if os.MkdirAll(site, 0o700) != nil || analysis.WriteFileExclusive(filepath.Join(cfgDir, "pyrightconfig.json"), PyrightConfig(minor, filepath.ToSlash(root))) != nil {
		rc.writeFail = true
		return
	}
	var list bytes.Buffer
	for _, f := range m.Files {
		// "./" keeps a name like -x.py from reading as an option.
		list.WriteString("./" + pack.RelTo(f, m.Dir))
		list.WriteByte('\n')
	}
	// The files are read from stdin ("-"): no argv length limit, and only
	// these files are checked.
	rc.exec(execPyrightCheck, []string{"--outputjson", "-p", cfgDir, "-"}, list.Bytes())
}

type pyrightPos struct {
	Line      *int `json:"line"`
	Character *int `json:"character"`
}

type pyrightRange struct {
	Start *pyrightPos `json:"start"`
	End   *pyrightPos `json:"end"`
}

// pyrightDiag is one generalDiagnostics element of pyright 1.1.414.
type pyrightDiag struct {
	File     *string       `json:"file"`
	Severity *string       `json:"severity"`
	Message  *string       `json:"message"`
	Range    *pyrightRange `json:"range"`
	Rule     *string       `json:"rule"`
}

type pyrightSummary struct {
	FilesAnalyzed    *int     `json:"filesAnalyzed"`
	ErrorCount       *int     `json:"errorCount"`
	WarningCount     *int     `json:"warningCount"`
	InformationCount *int     `json:"informationCount"`
	TimeInSec        *float64 `json:"timeInSec"`
}

func (p *pyrightPos) ok() bool {
	return p != nil && p.Line != nil && p.Character != nil && *p.Line >= 0 && *p.Character >= 0
}

// pyrightCategory is the policy codeye-python-pyright v1.
func pyrightCategory(rule string) string {
	switch {
	case strings.HasPrefix(rule, "reportUnused"), rule == "reportDeprecated", strings.HasPrefix(rule, "reportUnnecessary"):
		return "maintainability"
	}
	return "correctness"
}

func (pyrightRun) assemble(rc *runCtx) obsdoc.Document {
	rp := obsdoc.RulePack{ID: PyrightRulePackID, Version: PyrightRulePackVer, Selection: append([]string{}, PyrightSelection...), RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	bd := newBuilder(rc, obsdoc.Component{ID: PyrightName, Version: PyrightAdapterVersion}, obsdoc.Component{ID: PyrightParserID, Version: PyrightParserVersion}, rp)
	bd.identify(func(v string) bool { return v == PyrightTestedVersion })
	bd.targetVersion(pyrightMinMinor, pyrightMaxMinor)
	bd.doc.Target.Properties["platform"] = pyrightPlatform()
	if rc.b.cfg.Node != nil {
		bd.doc.Target.Properties["node"] = rc.b.cfg.Node.Version
	}
	bd.limit(LimNoThirdParty, LimDefaultContext)
	bd.executions(execPyrightVersion, execPyrightCheck)

	r := rc.results[execPyrightCheck]
	syntax := map[string]bool{}
	var processed []string
	if r != nil {
		doc := parseDocument(r.Stdout.Data, "generalDiagnostics")
		diags := doc.Arrays["generalDiagnostics"]
		bd.doc.Parse.Records, bd.doc.Parse.UnparsedBytes, bd.doc.Parse.DecodeError = len(diags), doc.UnparsedBytes, doc.DecodeError
		if len(diags) > 0 || doc.UnparsedBytes > 0 {
			bd.output = true
		}
		var summary pyrightSummary
		var version, timeText string
		shapeOK := doc.DecodeError == "" && len(doc.Fields) == 4 && decodeStrict(doc.Fields["summary"], &summary) == nil &&
			json.Unmarshal(doc.Fields["version"], &version) == nil && json.Unmarshal(doc.Fields["time"], &timeText) == nil &&
			summary.FilesAnalyzed != nil && summary.ErrorCount != nil && summary.WarningCount != nil && summary.InformationCount != nil
		if doc.DecodeError == "" && !shapeOK {
			bd.doc.Parse.ShapeErrors++
		}
		sha := obsdoc.SHA256Hex(r.Stdout.Data)
		counts := map[string]int{}
		for _, el := range diags {
			var d pyrightDiag
			if err := decodeStrict(el.Raw, &d); err != nil || d.File == nil || d.Severity == nil || d.Message == nil || d.Range == nil || !d.Range.Start.ok() || !d.Range.End.ok() {
				bd.doc.Parse.ShapeErrors++
				continue
			}
			counts[*d.Severity]++
			start, end := d.Range.Start, d.Range.End
			rule := ""
			if d.Rule != nil {
				rule = *d.Rule
			}
			switch rule {
			case "":
				// Diagnostics without a rule are the parser's (syntax
				// errors): the file is left out and its first one is the
				// file's observation.
				ref := obsdoc.RawRef{Execution: execPyrightCheck, Stream: "stdout", StreamSHA256: sha, RecordIndex: el.Index, JSONPointer: el.Pointer, ByteStart: el.Start, ByteEnd: el.End}
				bd.unparsable(syntax, PyrightSyntaxRule, *d.Severity, *d.Message, *d.File, &place{StartLine: *start.Line + 1, StartCol: *start.Character + 1, EndLine: *end.Line + 1, EndCol: *end.Character + 1, Unit: unitUTF16}, ref)
				continue
			case "reportMissingImports":
				bd.doc.Coverage.Units[UnitImportUnresolved]++
				bd.limit(LimImportsUnresolved)
				continue
			case "reportMissingModuleSource":
				// A bundled stub resolved the import; the package itself is
				// not installed.
				bd.doc.Coverage.Units[UnitImportSourceMissing]++
				bd.limit(LimImportSourcesMissing)
				continue
			case "reportMissingTypeStubs":
				bd.doc.Coverage.Units[UnitStubMissing]++
				bd.limit(LimStubsMissing)
				continue
			}
			pl := place{StartLine: *start.Line + 1, StartCol: *start.Character + 1, EndLine: *end.Line + 1, EndCol: *end.Character + 1, Unit: unitUTF16}
			ref := obsdoc.RawRef{Execution: execPyrightCheck, Stream: "stdout", StreamSHA256: sha, RecordIndex: el.Index, JSONPointer: el.Pointer, ByteStart: el.Start, ByteEnd: el.End}
			bd.observe(rule, pyrightCategory(rule), *d.Severity, *d.Message, *d.File, pl, ref, false)
		}
		if bd.doc.Parse.ShapeErrors > 0 || doc.UnparsedBytes > 0 || doc.DecodeError != "" {
			bd.unparsed()
		}
		// The summary must agree with the diagnostics; pyright exits 1
		// iff it reported an error (warnings exit 0).
		consistent := shapeOK && *summary.ErrorCount == counts["error"] && *summary.WarningCount == counts["warning"] && *summary.InformationCount == counts["information"]
		switch code := pack.ExitCode(r); {
		case r.Status != executor.StatusExited:
		case code == 0 || code == 1:
			if !consistent || (code == 1) != (counts["error"] > 0) {
				bd.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
			}
			// filesAnalyzed must be the number of files handed over.
			if files := rc.p.Module.Files; consistent && *summary.FilesAnalyzed == len(files) {
				processed = append([]string{}, files...)
			}
		default:
			// 2 fatal, 3 configuration file, 4 command line (pyright docs).
			bd.fail(domain.TaskFailed, "pyright_failed", obsdoc.ExitConfigurationError)
		}
		if len(bytes.TrimSpace(r.Stderr.Data)) > 0 {
			bd.limit(LimStderrOutput)
		}
	}
	return bd.finish(processed, syntax)
}
