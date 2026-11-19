// Package gitleaks is the secrets adapter (docs/ANALYZERS.md). It runs the
// operator-installed gitleaks natively through the executor runner over the
// attempt workspace copy ("gitleaks dir"; a snapshot has no Git history) and
// records each detected credential location as a security observation whose
// message is a fixed template.
//
// No secret value leaves the gitleaks process: CODEYE passes its own report
// template that prints only the rule, the file, the line and column range
// and the entropy (never Secret, Match or Line), --redact=100 masks what
// gitleaks logs, the log level is pinned to info (the debug and trace sites
// print raw and decoded secrets) and -v is never passed. Captured stdout and
// stderr therefore hold no secret material; the tests plant a generated
// secret and assert it appears nowhere.
package gitleaks

import (
	"context"
	"errors"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Identity.
const (
	AnalyzerID      = "any.gitleaks"
	Name            = "gitleaks"
	Tool            = "gitleaks"
	AdapterVersion  = "1"
	ParserID        = "gitleaks-codeye-template"
	ParserVersion   = "1"
	RulePackID      = "codeye-secrets-gitleaks"
	RulePackVersion = "1"
	TestedSeries    = "8.30"
)

// ExitCodeLeaks is passed as --exit-code so "leaks found" (3) is told apart
// from a fatal error or a partial scan, which gitleaks reports with 1
// (measured with 8.30.1).
const ExitCodeLeaks = 3

// Config is the CODEYE-managed gitleaks configuration: gitleaks' built-in
// default rules and global allowlists. Passing it with --config means a
// repository .gitleaks.toml is never read (measured: a repository
// configuration without [extend] would otherwise replace every rule).
const Config = "# CODEYE rule pack codeye-secrets-gitleaks v1: the built-in gitleaks rules.\n[extend]\nuseDefault = true\n"

// Template is the CODEYE report template: one JSON object per finding
// with only non-secret fields, then a trailer with the finding count so a
// cut stream is detected.
const Template = `{{ range . }}{"rule":{{ toJson .RuleID }},"file":{{ toJson .File }},"symlink":{{ toJson .SymlinkFile }},"start_line":{{ .StartLine }},"end_line":{{ .EndLine }},"start_column":{{ .StartColumn }},"end_column":{{ .EndColumn }},"entropy":{{ printf "%.4f" .Entropy }}}
{{ end }}{"codeye_end":true,"findings":{{ len . }}}
`

// Selection is the rule pack: the configuration, inline gitleaks:allow
// comments disabled (every match is reported), no archive traversal and the
// default decode depth.
var Selection = []string{"config=extend.useDefault", "--ignore-gitleaks-allow", "--max-archive-depth=0", "--max-decode-depth=5"}

// Execution names.
const (
	execVersion = "gitleaks.version"
	execScan    = "gitleaks.scan"
)

var versionPattern = regexp.MustCompile(`^v([0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4})$`)

// ParseVersion reads `gitleaks version` stdout ("v8.30.1\n"). A build
// without version ldflags prints "version is set by build process" and is
// rejected.
func ParseVersion(stdout []byte) (string, bool) {
	if strings.Count(string(stdout), "\n") > 1 {
		return "", false
	}
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// VersionTested reports the pinned series.
func VersionTested(v string) bool { return pack.SeriesTested(v, TestedSeries) }

// Descriptor is gitleaks' pack identity: an extra-depth analyzer of every
// supported language snapshot (ecosystem any).
func Descriptor() pack.Descriptor {
	return pack.Descriptor{ID: AnalyzerID, Name: Name, Ecosystem: "any", Required: false, Tool: Tool, VersionArgs: []string{"version"}, ParseVersion: ParseVersion, Purpose: obsdoc.PurposeSecrets}
}

// Plan is the tool-independent planner: one task "gitleaks" over every
// included regular file in scope (configuration, documents and data
// included; gitleaks' own global allowlist decides what it skips).
func Plan(ctx context.Context, job analysis.Job) ([]pack.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return anypack.Plan(job, Descriptor(), "secrets: credential patterns (gitleaks)", anypack.Files(job, nil, false)), nil
}

// AllowlistPaths are the global allowlist path patterns of the gitleaks
// 8.30.1 default configuration (config/gitleaks.toml). gitleaks skips a
// directory or file whose walk path matches one without reading it; CODEYE
// uses the copy only to leave those files out of processed_paths.
var AllowlistPaths = compile([]string{
	`gitleaks\.toml`,
	`(?i)\.(?:bmp|gif|jpe?g|png|svg|tiff?)$`,
	`(?i)\.(?:eot|[ot]tf|woff2?)$`,
	`(?i)\.(?:docx?|xlsx?|pdf|bin|socket|vsidx|v2|suo|wsuo|.dll|pdb|exe|gltf)$`,
	`go\.(?:mod|sum|work(?:\.sum)?)$`,
	`(?:^|/)vendor/modules\.txt$`,
	`(?:^|/)vendor/(?:github\.com|golang\.org/x|google\.golang\.org|gopkg\.in|istio\.io|k8s\.io|sigs\.k8s\.io)(?:/.*)?$`,
	`(?:^|/)gradlew(?:\.bat)?$`,
	`(?:^|/)gradle\.lockfile$`,
	`(?:^|/)mvnw(?:\.cmd)?$`,
	`(?:^|/)\.mvn/wrapper/MavenWrapperDownloader\.java$`,
	`(?:^|/)node_modules(?:/.*)?$`,
	`(?:^|/)(?:deno\.lock|npm-shrinkwrap\.json|package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$`,
	`(?:^|/)bower_components(?:/.*)?$`,
	`(?:^|/)(?:angular|bootstrap|jquery(?:-?ui)?|plotly|swagger-?ui)[a-zA-Z0-9.-]*(?:\.min)?\.js(?:\.map)?$`,
	`(?:^|/)javascript\.json$`,
	`(?:^|/)(?:Pipfile|poetry)\.lock$`,
	`(?i)(?:^|/)(?:v?env|virtualenv)/lib(?:64)?(?:/.*)?$`,
	`(?i)(?:^|/)(?:lib(?:64)?/python[23](?:\.\d{1,2})+|python/[23](?:\.\d{1,2})+/lib(?:64)?)(?:/.*)?$`,
	`(?i)(?:^|/)[a-z0-9_.]+-[0-9.]+\.dist-info(?:/.+)?$`,
	`(?:^|/)vendor/(?:bundle|ruby)(?:/.*?)?$`,
	`\.gem$`,
	`verification-metadata\.xml`,
	`Database.refactorlog`,
	`(?:^|/)\.git$`,
})

func compile(list []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(list))
	for i, s := range list {
		out[i] = regexp.MustCompile(s)
	}
	return out
}

// Allowlisted reports a snapshot path gitleaks 8.30 skips by its default
// global allowlist: the file or one of its directories matches.
func Allowlisted(p string) bool {
	segs := strings.Split(p, "/")
	for i := 1; i <= len(segs); i++ {
		q := strings.Join(segs[:i], "/")
		for _, re := range AllowlistPaths {
			if re.MatchString(q) {
				return true
			}
		}
	}
	return false
}

// Settings selects the operator-installed gitleaks.
type Settings struct {
	Executable  string
	Timeout     time.Duration
	StdoutLimit int64
	StderrLimit int64
}

// Analyzer is the configured gitleaks adapter.
type Analyzer struct {
	cfg  Settings
	tool *anypack.Tool
}

var _ pack.Analyzer = (*Analyzer)(nil)

// New validates the configuration without running anything.
func New(cfg Settings) (*Analyzer, error) {
	t, err := anypack.NewTool(cfg.Executable, nil)
	if err != nil {
		return nil, errors.New("gitleaks: " + err.Error())
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

// Probe runs `gitleaks version` outside any source tree.
func (a *Analyzer) Probe(ctx context.Context, runner executor.Runner, dir string) (string, error) {
	return a.tool.Probe(ctx, runner, dir, []string{"version"}, ParseVersion)
}

// ScanArgs is the argv of one scan root (workspace-relative, cwd = the
// workspace root, so reported paths are snapshot paths). The root comes
// from the run scope: it is the last argument, after "--", and written
// "./<root>", so a directory named like an option ("--config=x", "-v") is
// always a path (measured with 8.30.1: reported paths stay "<root>/...").
func ScanArgs(root, config, ignoreDir, template string) []string {
	return []string{"dir", "--no-banner", "--no-color", "--log-level", "info", "--redact=100",
		"--config", config, "--gitleaks-ignore-path", ignoreDir, "--ignore-gitleaks-allow", "--max-archive-depth", "0", "--max-decode-depth", "5",
		"--exit-code", strconv.Itoa(ExitCodeLeaks), "--report-format", "template", "--report-template", template, "--report-path", "-", "--", RootArg(root)}
}

// RootArg is the positional form of a scan root: "." or "./<root>".
func RootArg(root string) string {
	if root == "." {
		return root
	}
	return "./" + root
}

// ScanRoot returns the scan root of a scan argv (its last argument).
func ScanRoot(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.TrimPrefix(args[len(args)-1], "./")
}

// scanRoots are the scope roots that hold a selected file.
func scanRoots(scope []string, files []source.Entry) []string {
	var out []string
	for _, r := range scope {
		if r != "." && domain.ValidateEvidencePath(r) != nil {
			continue
		}
		if slices.ContainsFunc(files, func(f source.Entry) bool { return r == "." || f.Path == r || strings.HasPrefix(f.Path, r+"/") }) {
			out = append(out, r)
		}
	}
	return out
}

// execName is the execution of scan root i of n.
func execName(i, n int) string {
	if n == 1 {
		return execScan
	}
	return execScan + ".r" + pad(i+1)
}

func pad(n int) string {
	s := strconv.Itoa(n)
	if len(s) < 2 {
		s = "0" + s
	}
	return s
}

// IgnoreFile is the .gitleaksignore gitleaks always reads at a scan root
// that is a directory (measured: -i does not turn it off).
func IgnoreFile(root string) string {
	if root == "." {
		return ".gitleaksignore"
	}
	return path.Join(root, ".gitleaksignore")
}

// IgnoreFilesSafe checks the repository .gitleaksignore of every scan root
// (read from the snapshot, bound to its manifest SHA-256): gitleaks always
// reads it, so an entry whose path field is absolute, climbs with "..",
// or holds a NUL refuses the task instead of being passed on.
func IgnoreFilesSafe(snapshotDir string, files []source.Entry, roots []string) bool {
	byPath := map[string]source.Entry{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	for _, root := range roots {
		e, ok := byPath[IgnoreFile(root)]
		if !ok {
			continue
		}
		b, err := govet.ReadSnapshotFile(snapshotDir, e.Path)
		if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
			return false
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.ContainsRune(line, 0) {
				return false
			}
			fields := strings.Split(line, ":")
			for _, f := range fields[:len(fields)-1] {
				if strings.HasPrefix(f, "/") || strings.HasPrefix(f, "\\") || slices.Contains(strings.FieldsFunc(f, func(r rune) bool { return r == '/' || r == '\\' }), "..") {
					return false
				}
			}
		}
	}
	return true
}

// Run executes the gitleaks task.
func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	r, failed := anypack.Start(ctx, job, task.Key, Plan)
	if failed != nil {
		return *failed
	}
	files := anypack.Files(job, nil, false)
	in := assembleInput{TaskKey: r.Task.Spec.Key, ManifestDigest: job.Snapshot.Digest, Patterns: r.Task.Patterns, Files: files, Roots: scanRoots(r.Task.Patterns, files),
		Text: map[string]anypack.TextState{}}
	for _, f := range files {
		in.Text[f.Path] = anypack.ClassifyText(job.Snapshot.SnapshotDir, f)
		if in.Text[f.Path] == anypack.TextUnreadable {
			in.Integrity = true
		}
	}
	cfg, err := r.WriteFile("codeye-gitleaks.toml", []byte(Config))
	if err != nil {
		in.ScratchFailed = true
	}
	tmpl, err := r.WriteFile("report.tmpl", []byte(Template))
	if err != nil {
		in.ScratchFailed = true
	}
	ignore, err := r.Mkdir("ignore")
	if err != nil {
		in.ScratchFailed = true
	}
	in.ConfigRejected = !IgnoreFilesSafe(job.Snapshot.SnapshotDir, files, in.Roots)
	in.Linked = !anypack.LinkFreeTree(r.Workspace, in.Roots)
	if !in.ScratchFailed && !in.Integrity && !in.ConfigRejected && !in.Linked {
		env := a.tool.Env(nil)
		in.Version = r.Exec(ctx, runner, execVersion, r.Spec(a.tool, env, anypack.ProbeTimeout, 64<<10, 64<<10, "version"))
		if !pack.Stoppable(in.Version) && pack.ExitedZero(in.Version) {
			if _, ok := ParseVersion(in.Version.Stdout.Data); ok {
				for i, root := range in.Roots {
					res := r.Exec(ctx, runner, execName(i, len(in.Roots)), r.Spec(a.tool, env, a.cfg.Timeout, a.cfg.StdoutLimit, a.cfg.StderrLimit, ScanArgs(root, cfg, ignore, tmpl)...))
					in.Scans = append(in.Scans, scan{Root: root, Exec: execName(i, len(in.Roots)), Result: res})
					if pack.Stoppable(res) {
						break
					}
				}
			}
		}
	}
	in.Cancelled = r.Cancelled
	in.Executions = r.Executions
	in.Drift = anypack.Drift(r.Workspace, files)
	in.Resolver = anypack.NewResolver(job.Snapshot.SnapshotDir, files, in.Drift)
	return r.Finish(assemble(in))
}
