// Package opengrep is the pattern and taint adapter (docs/ANALYZERS.md#opengrep-패턴taint-팩).
// It runs the operator-installed Opengrep natively through the executor
// runner over the attempt workspace copy with the CODEYE rule pack
// codeye-taint only: intra-file taint rules that follow HTTP request input
// to a dangerous use (command, file path, SQL text, outgoing request
// address, deserialization, code evaluation, HTML output) within one file.
//
// The SARIF log on stdout is the raw stream every observation points into
// (internal/worker/analysis/sarif); a JSON copy of the same run in the task
// scratch gives the files Opengrep scanned and the files it could not fully
// analyze. Nothing reaches the network: the rule files are local, no
// registry or "auto" configuration is named, the environment is explicit and
// Opengrep 2.0 has no metrics or version-check switch (measured with a
// system call trace, docs/ANALYZERS.md).
package opengrep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/sarif"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Identity.
const (
	AnalyzerID      = "any.opengrep"
	Name            = "opengrep"
	Tool            = "opengrep"
	AdapterVersion  = "1"
	ParserID        = "opengrep-sarif"
	ParserVersion   = "1"
	RulePackID      = "codeye-taint"
	RulePackVersion = "2"
	TestedSeries    = "2.0"
)

// Limits of the files given to Opengrep.
const (
	// MaxFileBytes: larger files are not given to Opengrep (its own default
	// --max-target-bytes; named targets bypass that filter, measured).
	MaxFileBytes = 1_000_000
	// MaxScans bounds the scan executions of one task.
	MaxScans = 64
	// RuleTimeoutSeconds bounds one rule on one file.
	RuleTimeoutSeconds = 30
	// Jobs is the number of parallel Opengrep workers (threads of one
	// process).
	Jobs = 4
)

// MaxArgBytes bounds the path arguments of one scan execution: 96 KiB, and
// 24 KiB on Windows, whose command line holds at most 32767 UTF-16 code
// units (a UTF-8 path has at least as many bytes as UTF-16 units; the rest
// of the argv is under 2 KiB).
var MaxArgBytes = maxArgBytes(runtime.GOOS)

func maxArgBytes(goos string) int {
	if goos == "windows" {
		return 24 << 10
	}
	return 96 << 10
}

// Execution names.
const (
	execVersion = "opengrep.version"
	execScan    = "opengrep.scan"
)

var versionPattern = regexp.MustCompile(`^([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4})$`)

// ParseVersion reads `opengrep --version` stdout ("2.0.0\n").
func ParseVersion(stdout []byte) (string, bool) {
	s := string(stdout)
	if strings.Count(s, "\n") > 1 {
		return "", false
	}
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// VersionTested reports the pinned series.
func VersionTested(v string) bool { return pack.SeriesTested(v, TestedSeries) }

// Descriptor is Opengrep's pack identity: an extra-depth analyzer of the
// languages the rule pack covers (ecosystem any).
func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: Name, Ecosystem: "any", Required: false, Tool: Tool, VersionArgs: []string{"--version"}, ParseVersion: ParseVersion}
}

// Files are the snapshot files of the run scope a rule file applies to,
// without data directories (hidden, testdata, vendor, node_modules) and
// third-party directories, and the number of third-party files left out.
func Files(job analysis.Job) ([]source.Entry, int) {
	langs := Languages()
	return anypack.StructureFiles(job, func(p string) bool { return LanguageOf(langs, p) != "" })
}

// Plan is the tool-independent planner: one task "opengrep" over the
// selected files when supported language source is in scope.
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, _ := Files(job)
	return anypack.Plan(job, Descriptor(), "patterns: intra-file taint rules (opengrep)", files), nil
}

// Settings selects the operator-installed Opengrep.
type Settings struct {
	Executable  string
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// Analyzer is the configured Opengrep adapter.
type Analyzer struct {
	cfg  Settings
	tool *anypack.Tool
}

var _ pack.Analyzer = (*Analyzer)(nil)

// New validates the configuration without running anything.
func New(cfg Settings) (*Analyzer, error) {
	t, err := anypack.NewTool(cfg.Executable, nil)
	if err != nil {
		return nil, errors.New("opengrep: " + err.Error())
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = anypack.DefaultTimeout
	}
	if cfg.StdoutLimit <= 0 {
		cfg.StdoutLimit = anypack.DefaultStdoutLimit
	}
	if cfg.StderrLimit <= 0 {
		cfg.StderrLimit = anypack.DefaultStderrLimit
	}
	return &Analyzer{cfg: cfg, tool: t}, nil
}

// Descriptor implements pack.Analyzer.
func (a *Analyzer) Descriptor() pack.Descriptor { return Descriptor() }

// Plan implements pack.Analyzer.
func (a *Analyzer) Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	return Plan(ctx, job)
}

// Probe runs `opengrep --version` outside any source tree.
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	return a.tool.Probe(ctx, runner, dir, []string{"--version"}, ParseVersion)
}

// ScanArgs is the argv of one scan: the CODEYE rule files only, SARIF with
// the taint traces on stdout, a JSON copy for the scanned file list,
// intra-file taint, the work copy as project root (no version control
// lookup above it) and the files after "--" as "./<path>" so a name like an
// option stays a path. Named targets are scanned whatever a repository
// .semgrepignore or .gitignore says (measured with 2.0.0).
func ScanArgs(ruleFiles []string, jsonCopy string, files []string) []string {
	args := []string{"scan"}
	for _, f := range ruleFiles {
		args = append(args, "--config", f)
	}
	args = append(args, "--no-rewrite-rule-ids", "--sarif", "--dataflow-traces", "--taint-intrafile", "--json-output", jsonCopy,
		"--project-root", ".", "--quiet", "--jobs", strconv.Itoa(Jobs), "--timeout", strconv.Itoa(RuleTimeoutSeconds), "--timeout-threshold", "3",
		"--max-target-bytes", strconv.Itoa(MaxFileBytes), "--")
	for _, f := range files {
		args = append(args, "./"+f)
	}
	return args
}

// Selection is the recorded rule selection: the engine switches and the
// SHA-256 prefix of each rule file, so a changed rule file changes the
// rule context.
func Selection(langs []Language) []string {
	out := []string{"--taint-intrafile", "--dataflow-traces", "--timeout=" + strconv.Itoa(RuleTimeoutSeconds), "--timeout-threshold=3"}
	for _, l := range langs {
		sum := sha256.Sum256(l.File)
		out = append(out, "rules/"+l.Name+".yaml@"+hex.EncodeToString(sum[:])[:12])
	}
	return out
}

// argOverhead is what one path argument adds to the command line beyond its
// bytes: the "./" prefix, the separator, and on Windows the two quotes
// around a name with a space (snapshot names hold no backslash or quote,
// so nothing else is escaped).
const argOverhead = 5

// batches splits the files into scans of at most MaxArgBytes of path
// arguments, in path order.
func batches(files []source.Entry) [][]string {
	var out [][]string
	var cur []string
	size := 0
	for _, f := range files {
		n := len(f.Path) + argOverhead
		if len(cur) > 0 && size+n > MaxArgBytes {
			out = append(out, cur)
			cur, size = nil, 0
		}
		cur = append(cur, f.Path)
		size += n
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func execName(i, n int) string {
	if n == 1 {
		return execScan
	}
	s := strconv.Itoa(i + 1)
	if len(s) < 2 {
		s = "0" + s
	}
	return execScan + ".r" + s
}

// Run executes the Opengrep task.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	r, failed := anypack.Start(ctx, job, task.Key, Plan)
	if failed != nil {
		return *failed
	}
	langs := Languages()
	selected, thirdParty := Files(job)
	in := assembleInput{TaskKey: r.Task.Spec.Key, ManifestDigest: job.Snapshot.Digest, Patterns: r.Task.Patterns, Selected: selected, ThirdParty: thirdParty,
		Languages: langs, Units: map[string]int{}, ExcludedDataDirs: anypack.ExcludedDataDirFiles(job, func(p string) bool { return LanguageOf(langs, p) != "" })}
	for _, f := range selected {
		switch {
		case f.Size != nil && *f.Size > MaxFileBytes:
			in.Units[UnitTooLarge]++
		case !sarif.Maps(f.Path, SARIFOptions):
			// A name the SARIF log could not name back (a backslash, a
			// drive-like prefix) is not given to Opengrep.
			in.Units[UnitBadName]++
		default:
			switch anypack.ClassifyText(job.Snapshot.SnapshotDir, f) {
			case anypack.TextUnreadable:
				in.Integrity = true
			case anypack.TextUTF8:
				in.Files = append(in.Files, f)
			default:
				in.Units[UnitNotText]++
			}
		}
	}
	used := map[string]bool{}
	for _, f := range in.Files {
		used[LanguageOf(langs, f.Path)] = true
	}
	var ruleFiles []string
	if _, err := r.Mkdir("rules"); err != nil {
		in.ScratchFailed = true
	}
	for _, l := range langs {
		if !used[l.Name] {
			continue
		}
		in.Loaded = append(in.Loaded, l)
		p, err := r.WriteFile(filepath.Join("rules", l.Name+".yaml"), l.File)
		if err != nil {
			in.ScratchFailed = true
		}
		ruleFiles = append(ruleFiles, p)
	}
	in.Linked = !anypack.LinkFreeFiles(r.Workspace, in.Files)
	groups := batches(in.Files)
	in.TooManyScans = len(groups) > MaxScans
	if !in.ScratchFailed && !in.Integrity && !in.Linked && !in.TooManyScans && len(in.Files) > 0 {
		env := a.tool.Env(nil)
		in.Version = r.Exec(ctx, runner, execVersion, r.Spec(a.tool, env, anypack.ProbeTimeout, 64<<10, 64<<10, "--version"))
		if !pack.Stoppable(in.Version) && pack.ExitedZero(in.Version) {
			if _, ok := ParseVersion(in.Version.Stdout.Data); ok {
				for i, g := range groups {
					name := execName(i, len(groups))
					copyPath := filepath.Join(r.Dir, name+".json")
					res := r.Exec(ctx, runner, name, r.Spec(a.tool, env, a.cfg.Timeout, a.cfg.StdoutLimit, a.cfg.StderrLimit, ScanArgs(ruleFiles, copyPath, g)...))
					s := scan{Exec: name, Files: g, Result: res}
					if res != nil {
						s.Copy, s.CopyErr = readCopy(copyPath)
					}
					in.Scans = append(in.Scans, s)
					if pack.Stoppable(res) {
						break
					}
				}
			}
		}
	}
	in.Groups = len(groups)
	in.Cancelled = r.Cancelled
	in.Executions = r.Executions
	in.Drift = anypack.Drift(r.Workspace, in.Files)
	in.Resolver = anypack.NewResolver(job.Snapshot.SnapshotDir, in.Files, in.Drift)
	return r.Finish(assemble(in))
}
