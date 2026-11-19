package python

import (
	"bufio"
	"bytes"
	"context"
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

// mypy identity: an extra-depth type checker, the alternative of pyright
// (docs/ANALYZERS.md#python-팩).
const (
	MypyID             = "python.mypy"
	MypyName           = "mypy"
	MypyTool           = "mypy"
	MypyAdapterVersion = "1"
	MypyParserID       = "mypy-json-lines-v1"
	MypyParserVersion  = "1"
	MypyRulePackID     = "codeye-python-mypy"
	MypyRulePackVer    = "1"
	MypyTestedSeries   = "2.4"
	// mypy 2.4 rejects target versions below 3.10 (measured).
	mypyMinMinor = 10
	mypyMaxMinor = 15
)

// MypyDisabledCodes are the error codes that report the same construct as
// a selected ruff rule on the same line (measured with mypy 2.4.1 and ruff
// 0.16.3 on one fixture, docs/ANALYZERS.md#python-엔진-중복): name-defined F821, no-redef F811,
// str-format F50x/F52x, await-not-async PLE1142.
var MypyDisabledCodes = []string{"await-not-async", "name-defined", "no-redef", "str-format"}

// MypySelection is the rule-pack part of the mypy argv. --config-file= (an
// empty name) disables every configuration file, so repository plugins are
// never imported (measured with a canary plugin) and no repository option
// applies. --no-site-packages: no interpreter is asked for installed
// packages. --no-native-parser: the legacy parser's columns are UTF-8 bytes
// (the native parser's are not, measured).
var MypySelection = func() []string {
	s := []string{"--config-file=", "--no-site-packages", "--explicit-package-bases", "--follow-imports=silent", "--check-untyped-defs", "--no-native-parser"}
	for _, c := range MypyDisabledCodes {
		s = append(s, "--disable-error-code="+c)
	}
	return s
}()

// mypy execution names, in run order.
const (
	execMypyVersion = "mypy.version"
	execMypyCheck   = "mypy.check"
)

// MypyDescriptor is mypy's pack identity (extra depth, not required).
func MypyDescriptor() pack.Descriptor {
	return pack.Descriptor{ID: MypyID, Name: MypyName, Ecosystem: Ecosystem, Required: false, Tool: MypyTool, VersionArgs: []string{"--version"}, ParseVersion: ParseMypyVersion}
}

// MypyPlan is mypy's tool-independent planner.
var MypyPlan = planFor(MypyDescriptor())

var mypyVersion = regexp.MustCompile(`^mypy ([0-9]+\.[0-9]+\.[0-9]+) \(compiled: (?:yes|no)\)$`)

// ParseMypyVersion reads `mypy --version` ("mypy 2.4.1 (compiled: yes)").
// Development builds ("+dev") are not recognized.
func ParseMypyVersion(stdout []byte) (string, bool) {
	m := mypyVersion.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Mypy is the configured mypy adapter.
type Mypy struct{ base }

var _ pack.Analyzer = (*Mypy)(nil)

// NewMypy validates the configuration without running anything.
func NewMypy(cfg Config) (*Mypy, error) {
	b, err := newBase(MypyDescriptor(), cfg)
	if err != nil {
		return nil, err
	}
	return &Mypy{base: b}, nil
}

// Run implements pack.Analyzer.
func (a *Mypy) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	return a.run(ctx, job, task, runner, mypyRun{})
}

type mypyRun struct{}

// mypyPlatform is the --platform value of the node's operating system.
func mypyPlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	}
	return "linux"
}

// usesSrc reports a src layout: module files under src/ get their module
// names relative to src (MYPYPATH), as pyright resolves them.
func usesSrc(moduleDir string, files []string) bool {
	for _, f := range files {
		if strings.HasPrefix(relTo(f, moduleDir), "src/") {
			return true
		}
	}
	return false
}

// MypyArgs is the mypy argv after the executable for a target minor; cache,
// report and the file list live in task scratch.
func MypyArgs(minor int, cacheDir, reportDir, filesList string) []string {
	args := append([]string{}, MypySelection...)
	args = append(args, "--python-version", "3."+strconv.Itoa(minor), "--platform", mypyPlatform(), "--cache-dir", cacheDir,
		"-O", "json", "--lineprecision-report", reportDir)
	return append(args, "@"+filesList)
}

func (mypyRun) execute(rc *runCtx) {
	m := rc.p.Module
	minor, _ := clampMinor(rc.target.Minor, mypyMinMinor, mypyMaxMinor)
	list := filepath.Join(rc.dirs["cfg"], "files.txt")
	var rels []string
	for _, f := range m.Files {
		// Every line of the @file list is an argument: "./" keeps a name
		// like -x.py from reading as an option.
		rels = append(rels, "./"+relTo(f, m.Dir))
	}
	if err := writeLines(list, rels); err != nil {
		rc.writeFail = true
		return
	}
	if usesSrc(m.Dir, m.Files) {
		rc.extraEnv["MYPYPATH"] = filepath.Join(rc.moduleAbs, "src")
	}
	rc.exec(execMypyCheck, MypyArgs(minor, rc.dirs["cache"], rc.dirs["report"], list), nil)
}

// mypyRecord is one -O json record of mypy 2.4.
type mypyRecord struct {
	File      *string `json:"file"`
	Line      *int    `json:"line"`
	Column    *int    `json:"column"`
	EndLine   *int    `json:"end_line"`
	EndColumn *int    `json:"end_column"`
	Message   *string `json:"message"`
	Hint      *string `json:"hint"`
	Code      *string `json:"code"`
	Severity  *string `json:"severity"`
}

// mypyCategory is the policy codeye-python-mypy v1.
func mypyCategory(code, severity string) string {
	switch {
	case severity == "note", strings.HasPrefix(code, "unused-"), strings.HasPrefix(code, "redundant-"), code == "deprecated", code == "annotation-unchecked":
		return "maintainability"
	}
	return "correctness"
}

// mypyModuleName is the module name mypy gives a module-relative file with
// --explicit-package-bases (base: the module directory, or src).
func mypyModuleName(rel string, src bool) string {
	if src && strings.HasPrefix(rel, "src/") {
		rel = rel[len("src/"):]
	}
	rel = strings.TrimSuffix(strings.TrimSuffix(rel, ".pyi"), ".py")
	name := strings.ReplaceAll(rel, "/", ".")
	return strings.TrimSuffix(name, ".__init__")
}

// readLinePrecision reads the module names of lineprecision.txt, the
// report of the modules mypy analyzed.
func readLinePrecision(p string) (map[string]bool, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	out := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	header := 0
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 0:
			continue
		case header == 0:
			if len(f) != 7 || f[0] != "Name" {
				return nil, false
			}
			header++
			continue
		case header == 1:
			if strings.Trim(f[0], "-") != "" {
				return nil, false
			}
			header++
			continue
		case len(f) != 7:
			return nil, false
		}
		for _, n := range f[1:] {
			if _, err := strconv.Atoi(n); err != nil {
				return nil, false
			}
		}
		out[f[0]] = true
	}
	return out, sc.Err() == nil && header == 2
}

func (mypyRun) assemble(rc *runCtx) obsdoc.Document {
	rp := obsdoc.RulePack{ID: MypyRulePackID, Version: MypyRulePackVer, Selection: append([]string{}, MypySelection...), RepositoryConfig: obsdoc.RepoConfigOverridden,
		SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{}, UnreportedSuppressionRules: []string{}}
	bd := newBuilder(rc, obsdoc.Component{ID: MypyName, Version: MypyAdapterVersion}, obsdoc.Component{ID: MypyParserID, Version: MypyParserVersion}, rp)
	bd.identify(seriesTested(MypyTestedSeries))
	bd.targetVersion(mypyMinMinor, mypyMaxMinor)
	m := rc.p.Module
	src := usesSrc(m.Dir, m.Files)
	bd.doc.Target.Properties["platform"] = mypyPlatform()
	if src {
		bd.doc.Target.Properties["mypy_path"] = "src"
	}
	bd.limit(LimNoThirdParty, LimDefaultContext)
	bd.executions(execMypyVersion, execMypyCheck)

	r := rc.results[execMypyCheck]
	syntax := map[string]bool{}
	var processed []string
	if r != nil {
		ls := parseJSONLines(r.Stdout.Data)
		bd.doc.Parse.Records, bd.doc.Parse.UnparsedBytes, bd.doc.Parse.DecodeError = len(ls.Values), ls.UnparsedBytes, ls.DecodeError
		if len(ls.Values) > 0 || ls.UnparsedBytes > 0 {
			bd.output = true
		}
		sha := obsdoc.SHA256Hex(r.Stdout.Data)
		errors, blocking := 0, false
		for _, el := range ls.Values {
			var rec mypyRecord
			if err := decodeStrict(el.Raw, &rec); err != nil || rec.File == nil || rec.Line == nil || rec.Column == nil || rec.EndLine == nil || rec.EndColumn == nil ||
				rec.Message == nil || rec.Severity == nil {
				bd.doc.Parse.ShapeErrors++
				continue
			}
			code, sev := "", *rec.Severity
			if rec.Code != nil {
				code = *rec.Code
			}
			if sev == "error" {
				errors++
			}
			switch {
			case code == "syntax":
				bd.toolError(execMypyCheck, "syntax", *rec.File, *rec.Line, *rec.Column+1, *rec.Message)
				if rel, ok := bd.toolPath(*rec.File); ok {
					syntax[rel] = true
				}
				continue
			case code == "import-not-found":
				// No dependency is installed: an unresolved import is a
				// coverage gap; the module's names become Any (no
				// follow-on errors, measured).
				bd.doc.Coverage.Units[UnitImportUnresolved]++
				bd.limit(LimImportsUnresolved)
				continue
			case code == "import-untyped":
				bd.doc.Coverage.Units[UnitStubMissing]++
				bd.limit(LimStubsMissing)
				continue
			case *rec.Line < 1 && sev == "error":
				// A record without a position (duplicate module, invalid
				// package name) blocks the whole run.
				blocking = true
				bd.toolError(execMypyCheck, "blocking", *rec.File, 0, 0, *rec.Message)
				continue
			}
			rule := code
			if rule == "" {
				rule = "mypy-" + sev
			}
			pl := place{StartLine: *rec.Line, StartCol: *rec.Column + 1, EndLine: *rec.EndLine, EndCol: *rec.EndColumn + 1, Unit: unitByte}
			ref := obsdoc.RawRef{Execution: execMypyCheck, Stream: "stdout", StreamSHA256: sha, RecordIndex: el.Index, ByteStart: el.Start, ByteEnd: el.End}
			bd.observe(rule, mypyCategory(code, sev), sev, *rec.Message, *rec.File, pl, ref, false)
		}
		if bd.doc.Parse.ShapeErrors > 0 || ls.UnparsedBytes > 0 || ls.DecodeError != "" {
			bd.unparsed()
		}
		switch code := exitCode(r); {
		case r.Status != executor.StatusExited:
		case code == 0 && errors == 0, code == 1 && errors > 0 && !blocking && len(syntax) == 0:
		case code == 2 && len(syntax) > 0:
			// Syntax errors stop mypy before type checking (exit 2).
		case code == 2:
			bd.fail(domain.TaskFailed, "mypy_failed", obsdoc.ExitConfigurationError)
		default:
			bd.fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
		}
		if len(bytes.TrimSpace(r.Stderr.Data)) > 0 {
			bd.limit(LimStderrOutput)
		}
		// The line precision report lists every module mypy analyzed; a
		// listed file whose module is not in it was not processed.
		if exitCode(r) == 0 || exitCode(r) == 1 {
			if names, ok := readLinePrecision(filepath.Join(rc.dirs["report"], "lineprecision.txt")); ok {
				processed = []string{}
				for _, f := range m.Files {
					if names[mypyModuleName(relTo(f, m.Dir), src)] {
						processed = append(processed, f)
					}
				}
			}
		}
	}
	return bd.finish(processed, syntax)
}
