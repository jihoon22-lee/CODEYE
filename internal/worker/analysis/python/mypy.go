package python

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
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
		if strings.HasPrefix(pack.RelTo(f, moduleDir), "src/") {
			return true
		}
	}
	return false
}

// MypyArgs is the mypy argv after the executable for a target minor; cache,
// report and the file list live in task scratch. skipConfig, when set, is
// the scratch configuration of a rerun that skips the unparsable modules.
func MypyArgs(minor int, cacheDir, reportDir, filesList, skipConfig string) []string {
	args := append([]string{}, MypySelection...)
	args = append(args, "--python-version", "3."+strconv.Itoa(minor), "--platform", mypyPlatform(), "--cache-dir", cacheDir,
		"-O", "json", "--lineprecision-report", reportDir)
	if skipConfig != "" {
		args = append(args, "--config-file", skipConfig)
	}
	return append(args, "@"+filesList)
}

// mypySkipModule is a module name a configuration section header can hold.
var mypySkipModule = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// mypySkipConfig is the configuration of a rerun: only per-module sections
// that make mypy skip the unparsable modules a remaining file imports
// (follow_imports = skip; measured with 2.4.1, an import of a left-out
// module otherwise stops the rerun at the same syntax error). The global
// options stay the command line's.
func mypySkipConfig(modules []string) []string {
	lines := []string{"[mypy]"}
	for _, mod := range modules {
		lines = append(lines, "", "[mypy-"+mod+"]", "follow_imports = skip")
	}
	return lines
}

// MypyMaxReruns bounds the reruns without unparsable files: mypy stops at
// the first file it cannot parse (exit 2, one syntax record, measured with
// 2.4.1), so each rerun leaves out the files reported so far.
const MypyMaxReruns = 32

// mypyCheckName is the execution of check run i (0: the first run).
func mypyCheckName(i int) string {
	if i == 0 {
		return execMypyCheck
	}
	return fmt.Sprintf("%s.r%02d", execMypyCheck, i)
}

func (mypyRun) execute(rc *runCtx) {
	m := rc.p.Module
	minor, _ := clampMinor(rc.target.Minor, mypyMinMinor, mypyMaxMinor)
	if usesSrc(m.Dir, m.Files) {
		rc.extraEnv["MYPYPATH"] = filepath.Join(rc.moduleAbs, "src")
	}
	files := append([]string{}, m.Files...)
	src := usesSrc(m.Dir, m.Files)
	var skipped []string
	// The first run and its reruns share the task's one time limit: each
	// rerun gets only what is left.
	rc.deadline = time.Now().Add(rc.b.cfg.Timeout)
	for i := 0; i <= MypyMaxReruns; i++ {
		name := "files.txt"
		if i > 0 {
			name = fmt.Sprintf("files.r%02d.txt", i)
		}
		list := filepath.Join(rc.dirs["cfg"], name)
		var rels []string
		for _, f := range files {
			// Every line of the @file list is an argument: "./" keeps a name
			// like -x.py from reading as an option.
			rels = append(rels, "./"+pack.RelTo(f, m.Dir))
		}
		if err := writeLines(list, rels); err != nil {
			rc.writeFail = true
			return
		}
		skipConfig := ""
		if len(skipped) > 0 {
			skipConfig = filepath.Join(rc.dirs["cfg"], fmt.Sprintf("mypy.r%02d.ini", i))
			if err := writeLines(skipConfig, mypySkipConfig(skipped)); err != nil {
				rc.writeFail = true
				return
			}
		}
		exec := mypyCheckName(i)
		r := rc.exec(exec, MypyArgs(minor, rc.dirs["cache"], rc.dirs["report"], list, skipConfig), nil)
		if r == nil {
			return
		}
		rc.checks = append(rc.checks, exec)
		if r.Status != executor.StatusExited || pack.ExitCode(r) != 2 || !pack.StreamsComplete(r) {
			return
		}
		// Exit 2 with syntax records of listed files: run again without
		// them. Anything else that stops mypy is the run's outcome.
		bad := map[string]bool{}
		for _, el := range parseJSONLines(r.Stdout.Data).Values {
			var rec mypyRecord
			if decodeStrict(el.Raw, &rec) != nil || rec.File == nil || rec.Code == nil || *rec.Code != "syntax" {
				continue
			}
			if rel, ok := govet.StripWorkspace(*rec.File, m.Dir, rc.roots, true); ok && slices.Contains(files, rel) {
				bad[rel] = true
			}
		}
		if len(bad) == 0 {
			return
		}
		files = slices.DeleteFunc(files, func(f string) bool { return bad[f] })
		for _, f := range slices.Sorted(maps.Keys(bad)) {
			if mod := mypyModuleName(pack.RelTo(f, m.Dir), src); mypySkipModule.MatchString(mod) && !slices.Contains(skipped, mod) {
				skipped = append(skipped, mod)
			}
		}
		if len(files) == 0 {
			return
		}
	}
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
	checks := rc.checks
	if len(checks) == 0 {
		checks = []string{execMypyCheck}
	}
	bd.executions(append([]string{execMypyVersion}, checks...)...)
	if rc.budgetSpent {
		// The time limit ran out before a rerun without the unparsable
		// files: the syntax errors found so far stay, nothing was
		// type-checked.
		bd.fail(domain.TaskTimedOut, ReasonTimeout, obsdoc.ExitTimeout)
		bd.limit(LimMypyRerunsTimedOut)
	}

	syntax := map[string]bool{}
	var processed []string
	for i, name := range checks {
		r := rc.results[name]
		if r == nil {
			continue
		}
		last := i == len(checks)-1
		ls := parseJSONLines(r.Stdout.Data)
		bd.doc.Parse.Records += len(ls.Values)
		bd.doc.Parse.UnparsedBytes += ls.UnparsedBytes
		if ls.DecodeError != "" {
			bd.doc.Parse.DecodeError = ls.DecodeError
		}
		if len(ls.Values) > 0 || ls.UnparsedBytes > 0 {
			bd.output = true
		}
		sha := obsdoc.SHA256Hex(r.Stdout.Data)
		errors, blocking, runSyntax := 0, false, 0
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
			ref := obsdoc.RawRef{Execution: name, Stream: "stdout", StreamSHA256: sha, RecordIndex: el.Index, ByteStart: el.Start, ByteEnd: el.End}
			switch {
			case code == "syntax":
				// The file is left out of the next run; its syntax error is
				// the file's observation.
				runSyntax++
				bd.unparsable(syntax, code, sev, *rec.Message, *rec.File, &place{StartLine: *rec.Line, StartCol: *rec.Column + 1, EndLine: *rec.EndLine, EndCol: *rec.EndColumn + 1, Unit: unitByte}, ref)
				continue
			case !last:
				// A run that stopped at a syntax error reports nothing else.
				bd.doc.Parse.ShapeErrors++
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
				bd.toolError(name, "blocking", *rec.File, 0, 0, *rec.Message)
				continue
			}
			rule := code
			if rule == "" {
				rule = "mypy-" + sev
			}
			pl := place{StartLine: *rec.Line, StartCol: *rec.Column + 1, EndLine: *rec.EndLine, EndCol: *rec.EndColumn + 1, Unit: unitByte}
			bd.observe(rule, mypyCategory(code, sev), sev, *rec.Message, *rec.File, pl, ref, false)
		}
		if bd.doc.Parse.ShapeErrors > 0 || ls.UnparsedBytes > 0 || ls.DecodeError != "" {
			bd.unparsed()
		}
		switch code := pack.ExitCode(r); {
		case r.Status != executor.StatusExited:
		case code == 0 && errors == 0, code == 1 && errors > 0 && !blocking && runSyntax == 0:
		case code == 2 && runSyntax > 0 && !last:
			// A syntax error stops mypy before type checking (exit 2); the
			// next run leaves the file out.
		case code == 2 && runSyntax > 0:
			// mypy could not be run past it (not a listed file, or the
			// rerun bound): nothing was type-checked.
			bd.fail(domain.TaskFailed, ReasonSyntaxErrors, obsdoc.ExitExecutionError)
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
		if last && (pack.ExitCode(r) == 0 || pack.ExitCode(r) == 1) {
			if names, ok := readLinePrecision(filepath.Join(rc.dirs["report"], "lineprecision.txt")); ok {
				processed = []string{}
				for _, f := range m.Files {
					if names[mypyModuleName(pack.RelTo(f, m.Dir), src)] {
						processed = append(processed, f)
					}
				}
			}
		}
	}
	return bd.finish(processed, syntax)
}
