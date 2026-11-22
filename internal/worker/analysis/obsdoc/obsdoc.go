// Package obsdoc is the generic analyzer observations envelope shared by the
// analyzer packs (docs/ANALYZERS.md). One document describes one analyzer
// task: which tool ran where, how each execution ended, what the parser read
// and which raw tool diagnostics it kept. It is I/O free so the worker, the
// review roles and the report decoder validate the same bytes with the
// same rules. A parser failure is never encoded as "no observations".
package obsdoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Identity of the envelope (role analysis.RoleObservations, media
// analysis.MediaObservations). The go vet v0.1 document keeps its own kind.
const (
	SchemaVersion = "0.1"
	Kind          = "analyzer_observations"
	// MaxBytes bounds one envelope (report.MaxDecodedBytes is larger).
	MaxBytes = 16 << 20
)

// Exit classes (docs/ANALYZERS.md#종료-등급과-결과). not_run: no tool execution was attempted.
const (
	ExitSuccessWithoutFindings = "success_without_findings"
	ExitSuccessWithFindings    = "success_with_findings"
	ExitConfigurationError     = "configuration_error"
	ExitExecutionError         = "execution_error"
	ExitTimeout                = "timeout"
	ExitCancelled              = "cancelled"
	ExitNotRun                 = "not_run"
)

// Location states (the go vet vocabulary). Only resolved observations carry a
// Location and may become code evidence.
const (
	LocationResolved        = "resolved"
	LocationNoPosition      = "unresolved_no_position"
	LocationOutsideSnapshot = "unresolved_outside_snapshot"
	LocationMissingFile     = "unresolved_missing_file"
	LocationLineOutOfRange  = "unresolved_line_out_of_range"
	LocationSourceDrift     = "source_drift"
)

// Rule pack handling of repository configuration and in-source suppressions
// (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제 rule context). A pack that honors repository configuration
// records the files the tool read in RulePack.ConfigFiles; one that cannot
// enumerate them records null, and its vanished findings are never
// resolved. A pack whose tool hides suppressed diagnostics records honored;
// one that keeps them records reported and marks those observations
// Suppressed.
const (
	RepoConfigOverridden = "overridden" // CLI flags replace the repository selection
	RepoConfigHonored    = "honored"    // the tool reads repository configuration
	RepoConfigNone       = "none"       // the tool has no repository configuration
	SuppressHonored      = "honored"    // in-source suppressions hide diagnostics
	SuppressReported     = "reported"   // suppressed diagnostics are kept, marked suppressed
	SuppressDisabled     = "disabled"   // CODEYE turned the tool's in-source suppressions off
	SuppressNone         = "none"       // the tool has no suppression syntax
)

// Ecosystems an analyzer may target; "any" is language independent.
var Ecosystems = []string{"any", "cpp", "go", "java", "javascript", "python", "rust"}

// Categories are the report finding categories an adapter policy may assign.
var Categories = []string{"api_contract", "concurrency", "correctness", "data_integrity", "error_handling", "maintainability", "performance", "resource_lifecycle", "security", "test_gap"}

var (
	locationStates = []string{LocationResolved, LocationNoPosition, LocationOutsideSnapshot, LocationMissingFile, LocationLineOutOfRange, LocationSourceDrift}
	exitClasses    = []string{ExitSuccessWithoutFindings, ExitSuccessWithFindings, ExitConfigurationError, ExitExecutionError, ExitTimeout, ExitCancelled, ExitNotRun}
	columnUnits    = []string{"byte", "utf16", "rune"}
	units          = []string{"crate", "file", "module", "package", "project", "translation_unit"}
	repoConfig     = []string{RepoConfigOverridden, RepoConfigHonored, RepoConfigNone}
	suppressions   = []string{SuppressHonored, SuppressReported, SuppressDisabled, SuppressNone}
	streams        = []string{"stdout", "stderr"}
	// AnalyzerIDPattern: <ecosystem>.<tool>, no hyphen (review record
	// tool codes replace "." with "_" and must stay a code).
	AnalyzerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+$`)
	componentID       = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	observationID     = regexp.MustCompile(`^obs_[0-9a-f]{20}$`)
	code              = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	hex64             = regexp.MustCompile(`^[0-9a-f]{64}$`)
	propertyKey       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	versionText       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._+()/:-]{0,79}$`)
)

// Document is the observations envelope of one analyzer task. It holds no
// node absolute path and no environment value.
type Document struct {
	SchemaVersion  string        `json:"schema_version"`
	Kind           string        `json:"kind"`
	Analyzer       Analyzer      `json:"analyzer"`
	Adapter        Component     `json:"adapter"`
	Parser         Component     `json:"parser"`
	TaskKey        string        `json:"task_key"`
	ManifestDigest string        `json:"snapshot_manifest_digest"`
	Module         Module        `json:"module"`
	Patterns       []string      `json:"patterns"`
	Tool           Tool          `json:"tool"`
	Target         Target        `json:"target"`
	RulePack       RulePack      `json:"rule_pack"`
	Outcome        Outcome       `json:"outcome"`
	Executions     []Execution   `json:"executions"`
	Coverage       Coverage      `json:"coverage"`
	Observations   []Observation `json:"observations"`
	ToolErrors     []ToolError   `json:"tool_errors"`
	Parse          Parse         `json:"parse"`
	WorkspaceDrift []string      `json:"workspace_drift"`
	Limitations    []string      `json:"limitations"`
}

// Analyzer identifies the analyzer (pack descriptor) that owns the task.
type Analyzer struct {
	ID        string `json:"id"`
	Ecosystem string `json:"ecosystem"`
	// Required is the descriptor rule: baseline analyzers are
	// required_for_verdict, extra-depth analyzers are not.
	Required bool `json:"required"`
	// Purpose is what the analyzer's observations are
	// (docs/ANALYZERS.md#분석기-목적): PurposeMetrics (structure measurements, every
	// observation carries a metric) or PurposeSecrets (credential locations,
	// no metric). Absent means diagnostics, so the envelopes of the other
	// analyzers keep their bytes. Consumers read it instead of naming tools.
	Purpose string `json:"purpose,omitempty"`
}

// Analyzer purposes. Diagnostics, the default, is written as an absent field.
const (
	PurposeMetrics = "metrics"
	PurposeSecrets = "secrets"
)

// IsSecrets reports an analyzer whose observations locate credentials. No
// consumer attaches a code excerpt of such a location, and its messages are
// fixed templates without secret material (docs/ANALYZERS.md).
func (a Analyzer) IsSecrets() bool { return a.Purpose == PurposeSecrets }

// Component is a versioned adapter or parser implementation.
type Component struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Module is the analyzed module: snapshot-relative directory and the
// ecosystem identity (Go module path, package name, crate) if known.
type Module struct {
	Dir  string `json:"dir"`
	Name string `json:"name"`
}

// Tool is the operator-installed executable as observed by this task.
type Tool struct {
	Name          string  `json:"name"`
	Version       *string `json:"version"`
	VersionTested bool    `json:"version_tested"`
}

// Target is the build context the tool analyzed. Properties are ecosystem
// specific (Go: goos, goarch, cgo_enabled, build_tags, go_flags, toolchain).
type Target struct {
	Platform   *string           `json:"platform"`
	Properties map[string]string `json:"properties"`
}

// RulePack is the CODEYE selection passed to the tool and how repository
// configuration and in-source suppressions were treated. Together they are
// the rule context a comparison must find equal before it may call a
// vanished finding resolved.
type RulePack struct {
	ID                 string   `json:"id"`
	Version            string   `json:"version"`
	Selection          []string `json:"selection"`
	RepositoryConfig   string   `json:"repository_config"`
	SourceSuppressions string   `json:"source_suppressions"`
	// ConfigFiles are the repository configuration files the tool read,
	// sorted by path, each bound to its manifest SHA-256. [] unless
	// RepositoryConfig is honored; with honored, [] means the tool read no
	// file (its defaults) and null means the pack could not enumerate them.
	ConfigFiles []ConfigFile `json:"config_files"`
	// UnreportedSuppressionRules are the selected rules whose in-source
	// suppressions the tool applies without reporting them although the pack
	// records SourceSuppressions reported (measured per rule; staticcheck
	// U1000). Sorted unique rule IDs; [] unless SourceSuppressions is
	// reported. A comparison never resolves a vanished finding of these rules.
	UnreportedSuppressionRules []string `json:"unreported_suppression_rules"`
}

// ConfigFile is one repository configuration file a tool read.
type ConfigFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Outcome is the adapter's task outcome; the DB task row stays the authority.
type Outcome struct {
	TaskStatus   domain.TaskStatus `json:"task_status"`
	Reason       *string           `json:"reason"`
	ExitClass    string            `json:"exit_class"`
	PartialParse bool              `json:"partial_parse"`
}

// StreamRef binds a captured stream by hash and size.
type StreamRef struct {
	SHA256        string `json:"sha256"`
	CapturedBytes int64  `json:"captured_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	Truncated     bool   `json:"truncated"`
	Incomplete    bool   `json:"incomplete"`
}

// Execution is one executor run of the task (its name matches the result
// manifest execution and the stream artifacts).
type Execution struct {
	Name     string                   `json:"name"`
	Status   executor.Status          `json:"status"`
	Reason   string                   `json:"reason,omitempty"`
	ExitCode *int                     `json:"exit_code"`
	Cleanup  executor.Cleanup         `json:"cleanup"`
	Command  analysis.ManifestCommand `json:"command"`
	Stdout   StreamRef                `json:"stdout"`
	Stderr   StreamRef                `json:"stderr"`
}

// Coverage is what the task covered. ProcessedPaths is null when unknown;
// [] means the tool processed nothing (e.g. not_run). FilesInScope is null
// when the adapter cannot enumerate its inputs.
type Coverage struct {
	Unit           string         `json:"unit"`
	FilesInScope   *int           `json:"files_in_scope"`
	ProcessedPaths []string       `json:"processed_paths"`
	FilesSkipped   int            `json:"files_skipped"`
	FilesUnlisted  int            `json:"files_unlisted"`
	Units          map[string]int `json:"units"`
}

// Location is a resolved snapshot location (1-based lines).
type Location struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	StartCol   int    `json:"start_col"`
	EndLine    int    `json:"end_line"`
	EndCol     int    `json:"end_col"`
	ColumnUnit string `json:"column_unit"`
	BlobSHA256 string `json:"blob_sha256"`
}

// RawRef locates the raw bytes of an observation in one execution stream.
// RecordIndex is the 0-based record (JSON value or line) in the stream; it
// is a locator only and never part of an ID.
type RawRef struct {
	Execution    string `json:"execution"`
	Stream       string `json:"stream"`
	StreamSHA256 string `json:"stream_sha256"`
	RecordIndex  int    `json:"record_index"`
	JSONPointer  string `json:"json_pointer"`
	ByteStart    int64  `json:"byte_start"`
	ByteEnd      int64  `json:"byte_end"`
}

const (
	RelatedPosition   = "reported_position"
	RelatedNote       = "tool_note"
	RelatedAssumption = "condition_assumption"
	RelatedBranch     = "assumed_branch"
)

func ValidRelatedKind(kind string) bool {
	switch kind {
	case RelatedPosition, RelatedNote, RelatedAssumption, RelatedBranch:
		return true
	}
	return false
}

// Related is a secondary location the tool attached to a diagnostic.
type Related struct {
	Kind          string    `json:"kind,omitempty"`
	RawSpan       *ByteSpan `json:"raw_span,omitempty"`
	RawMessage    string    `json:"raw_message"`
	Path          *string   `json:"path"`
	Location      *Location `json:"location"`
	LocationState string    `json:"location_state"`
}

// ByteSpan refers to the same execution stream as the primary diagnostic.
type ByteSpan struct {
	Start int64 `json:"byte_start"`
	End   int64 `json:"byte_end"`
}

// Observation is one raw tool diagnostic. RawMessage is the tool text with
// node paths masked; it is never rewritten by a model.
type Observation struct {
	ID             string    `json:"id"`
	RuleID         string    `json:"rule_id"`
	Category       string    `json:"category"`
	RawSeverity    *string   `json:"raw_severity"`
	RawMessage     string    `json:"raw_message"`
	Path           *string   `json:"path"`
	Location       *Location `json:"location"`
	LocationState  string    `json:"location_state"`
	Related        []Related `json:"related"`
	RawRef         RawRef    `json:"raw_ref"`
	Unit           string    `json:"unit"`
	SuggestedFixes int       `json:"suggested_fix_count"`
	// Suppressed marks a diagnostic the tool reports as hidden by an
	// in-source suppression (only with source_suppressions=reported). It is
	// kept so a comparison can tell "suppressed" from "fixed"; it never
	// becomes a finding.
	Suppressed bool `json:"suppressed"`
	// Metric, when set, makes the observation a structure or duplication
	// measurement that reached the rule pack's reporting threshold
	// (docs/ANALYZERS.md), not a diagnostic of a defect: it is never active (Active), a
	// finding, a defect count or model review input. Absent on diagnostics.
	Metric *Metric `json:"metric,omitempty"`
}

// Metric is a measured value at or above its rule pack threshold. A metric
// is maintainability information, never evidence of a defect.
type Metric struct {
	// Name is the measured quantity (a code: cyclomatic_complexity,
	// duplicated_lines).
	Name string `json:"name"`
	// Value is the measured value; Threshold the rule pack's inclusive
	// reporting threshold (Value >= Threshold).
	Value     int `json:"value"`
	Threshold int `json:"threshold"`
	// Unit is what Value counts.
	Unit string `json:"unit"`
}

// MetricUnits are the units a metric may count.
var MetricUnits = []string{"count", "lines", "tokens"}

// SecretTaskNames are the task names of the PurposeSecrets analyzers (pack
// descriptor names). A secrets task screens model inputs and is planned
// whenever model input is in scope, so it never counts as a planned static
// analysis of the code (the report's static_analysis_not_planned).
var SecretTaskNames = []string{"gitleaks"}

// IsSecretTask reports a task key owned by a secrets analyzer (the name or
// name.<suffix>, as pack.Owns).
func IsSecretTask(key string) bool {
	return slices.ContainsFunc(SecretTaskNames, func(n string) bool { return key == n || strings.HasPrefix(key, n+".") })
}

// ToolError is a tool-reported failure to analyze (type error, missing
// dependency, load failure). It is not an observation of the code.
type ToolError struct {
	Execution string  `json:"execution"`
	Kind      string  `json:"kind"`
	Unit      string  `json:"unit"`
	Message   string  `json:"message"`
	Path      *string `json:"path"`
	Line      int     `json:"line"`
	Column    int     `json:"column"`
}

// Parse reports what the parser read.
type Parse struct {
	OK                     bool   `json:"ok"`
	Records                int    `json:"records"`
	UnparsedBytes          int64  `json:"unparsed_bytes"`
	ShapeErrors            int    `json:"shape_errors"`
	DecodeError            string `json:"decode_error,omitempty"`
	Observations           int    `json:"observations"`
	UnresolvedObservations int    `json:"unresolved_observations"`
	ToolErrors             int    `json:"tool_errors"`
}

// Expect is what a consumer already trusts: the task key and status it
// accepted, the snapshot digest and the manifest lookup of included files.
type Expect struct {
	TaskKey  string
	Snapshot string
	Lookup   func(path string) (sha256 string, ok bool)
}

// ErrInvalid wraps every validation error.
var ErrInvalid = errors.New("observations document invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// ArtifactPath is where an analyzer task writes its envelope in attempt
// scratch; later roles of the attempt (review interpret role) read it there
// and validate it again.
func ArtifactPath(scratch, taskKey string) string {
	return filepath.Join(scratch, "analyzers", taskKey, "observations.json")
}

// SHA256Hex is the lowercase hex SHA-256 of b.
func SHA256Hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// StreamOf summarizes a captured stream.
func StreamOf(s executor.Stream) StreamRef {
	return StreamRef{SHA256: SHA256Hex(s.Data), CapturedBytes: int64(len(s.Data)), TotalBytes: s.TotalBytes, Truncated: s.Truncated, Incomplete: s.Incomplete}
}

// ExecutionOf summarizes one executor result under its execution name.
func ExecutionOf(name string, r executor.Result) Execution {
	return Execution{Name: name, Status: r.Status, Reason: r.Reason, ExitCode: r.ExitCode, Cleanup: r.Cleanup,
		Command: analysis.CommandFor(r.Command), Stdout: StreamOf(r.Stdout), Stderr: StreamOf(r.Stderr)}
}

// ObservationID is content derived: analyzer, module, unit, rule, masked
// positions, masked message and the ordinal among identical tuples. Output
// order never changes it.
func ObservationID(analyzerID, moduleDir, unit, rule, position, end, message string, ordinal int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{analyzerID, moduleDir, unit, rule, position, end, message, strconv.Itoa(ordinal)}, "\x00")))
	return "obs_" + hex.EncodeToString(sum[:])[:20]
}

// New returns an empty document of the envelope with every list non-nil.
func New(a Analyzer, adapter, parser Component, taskKey, manifestDigest string) Document {
	return Document{SchemaVersion: SchemaVersion, Kind: Kind, Analyzer: a, Adapter: adapter, Parser: parser, TaskKey: taskKey, ManifestDigest: manifestDigest,
		Patterns: []string{}, Target: Target{Properties: map[string]string{}}, RulePack: RulePack{Selection: []string{}, RepositoryConfig: RepoConfigNone, SourceSuppressions: SuppressNone, ConfigFiles: []ConfigFile{}, UnreportedSuppressionRules: []string{}},
		Executions: []Execution{}, Coverage: Coverage{Unit: "file", Units: map[string]int{}}, Observations: []Observation{}, ToolErrors: []ToolError{},
		WorkspaceDrift: []string{}, Limitations: []string{}}
}

// Finish sorts observations and limitations and fills the parse counters
// from the lists, so a builder cannot make them disagree.
func (d *Document) Finish() {
	sort.SliceStable(d.Observations, func(a, b int) bool { return obsKey(d.Observations[a]) < obsKey(d.Observations[b]) })
	d.Parse.Observations, d.Parse.UnresolvedObservations, d.Parse.ToolErrors = len(d.Observations), 0, len(d.ToolErrors)
	for _, o := range d.Observations {
		if o.LocationState != LocationResolved {
			d.Parse.UnresolvedObservations++
		}
	}
	d.Limitations = uniqueSorted(d.Limitations)
	d.WorkspaceDrift = uniqueSorted(d.WorkspaceDrift)
	if d.Coverage.ProcessedPaths != nil {
		d.Coverage.ProcessedPaths = uniqueSorted(d.Coverage.ProcessedPaths)
	}
}

func obsKey(o Observation) string {
	p, line, col := "\xff", 0, 0
	if o.Path != nil {
		p = *o.Path
	}
	if o.Location != nil {
		line, col = o.Location.StartLine, o.Location.StartCol
	}
	return p + "\x00" + pad(line) + "\x00" + pad(col) + "\x00" + o.RuleID + "\x00" + o.RawMessage + "\x00" + o.ID
}

func pad(n int) string { return strconv.FormatInt(int64(n)+1e12, 10) }

func uniqueSorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return slices.Compact(out)
}

// Marshal validates and renders canonical JSON.
func Marshal(d Document, want Expect) ([]byte, error) {
	if err := Validate(d, want); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		return nil, invalid("encoding: %v", err)
	}
	b, err := domain.CanonicalJSON(bytes.TrimSpace(buf.Bytes()))
	if err != nil || len(b) > MaxBytes {
		return nil, invalid("encoding")
	}
	return b, nil
}

// Decode strictly parses and validates an envelope.
func Decode(body []byte, want Expect) (Document, error) {
	var d Document
	if len(body) > MaxBytes {
		return Document{}, invalid("document too large")
	}
	if err := domain.DecodeStrict(body, &d); err != nil {
		return Document{}, invalid("not strict JSON: %v", err)
	}
	if err := Validate(d, want); err != nil {
		return Document{}, err
	}
	return d, nil
}

func sortedCodes(list []string) bool {
	if list == nil {
		return false
	}
	for i, s := range list {
		if !code.MatchString(s) || (i > 0 && list[i-1] >= s) {
			return false
		}
	}
	return true
}

func sortedPaths(list []string, lookup func(string) (string, bool)) bool {
	for i, p := range list {
		if domain.ValidateEvidencePath(p) != nil || (i > 0 && list[i-1] >= p) {
			return false
		}
		if lookup != nil {
			if _, ok := lookup(p); !ok {
				return false
			}
		}
	}
	return true
}

func validDir(p string) bool { return p == "." || domain.ValidateEvidencePath(p) == nil }

// SafeText reports printable UTF-8 text without control characters other
// than tab and newline, within n bytes.
func SafeText(s string, n int) bool {
	if len(s) > n || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 && r != '\t' && r != '\n' || r == 0x7f {
			return false
		}
	}
	return true
}

// Validate applies Decode's rules to an in-memory document. It checks
// structure and cross-field consistency; it cannot attest that a tool ran
// correctly, only that the document does not claim more than it records.
func Validate(d Document, want Expect) error {
	switch {
	case d.SchemaVersion != SchemaVersion || d.Kind != Kind:
		return invalid("schema_version or kind")
	case !AnalyzerIDPattern.MatchString(d.Analyzer.ID) || len(d.Analyzer.ID) > 64 || !slices.Contains(Ecosystems, d.Analyzer.Ecosystem):
		return invalid("analyzer identity")
	case d.Analyzer.Purpose != "" && d.Analyzer.Purpose != PurposeMetrics && d.Analyzer.Purpose != PurposeSecrets:
		return invalid("analyzer purpose")
	case !componentID.MatchString(d.Adapter.ID) || !componentID.MatchString(d.Parser.ID) || d.Adapter.Version == "" || d.Parser.Version == "" || len(d.Adapter.Version) > 32 || len(d.Parser.Version) > 32:
		return invalid("adapter or parser identity")
	case !analysis.ValidTaskKey(d.TaskKey) || (want.TaskKey != "" && d.TaskKey != want.TaskKey):
		return invalid("task key")
	case !hex64.MatchString(d.ManifestDigest) || (want.Snapshot != "" && d.ManifestDigest != want.Snapshot):
		return invalid("snapshot digest")
	case !validDir(d.Module.Dir) || !SafeText(d.Module.Name, 256) || strings.Contains(d.Module.Name, "\n"):
		return invalid("module")
	case d.Patterns == nil || len(d.Patterns) > 256:
		return invalid("patterns")
	case !code.MatchString(strings.ReplaceAll(d.Tool.Name, "-", "_")) || (d.Tool.Version != nil && !versionText.MatchString(*d.Tool.Version)) || (d.Tool.Version == nil && d.Tool.VersionTested):
		return invalid("tool")
	case d.Target.Properties == nil || (d.Target.Platform != nil && !versionText.MatchString(*d.Target.Platform)):
		return invalid("target")
	case !componentID.MatchString(d.RulePack.ID) || d.RulePack.Version == "" || d.RulePack.Selection == nil || !slices.Contains(repoConfig, d.RulePack.RepositoryConfig) || !slices.Contains(suppressions, d.RulePack.SourceSuppressions):
		return invalid("rule pack")
	case d.Executions == nil || d.Observations == nil || d.ToolErrors == nil || d.Coverage.Units == nil || d.WorkspaceDrift == nil:
		return invalid("lists must be arrays")
	case !sortedCodes(d.Limitations):
		return invalid("limitations must be sorted unique codes")
	case !sortedPaths(d.WorkspaceDrift, nil):
		return invalid("workspace drift")
	}
	for _, p := range d.Patterns {
		if !SafeText(p, 512) || strings.ContainsAny(p, "\n\t") || p == "" {
			return invalid("pattern")
		}
	}
	for k, v := range d.Target.Properties {
		if !propertyKey.MatchString(k) || !SafeText(v, 256) || strings.Contains(v, "\n") || looksAbsolute(v) {
			return invalid("target property %q", k)
		}
	}
	for _, s := range d.RulePack.Selection {
		if s == "" || !SafeText(s, 1024) || strings.ContainsAny(s, "\n\t") || looksAbsolute(s) {
			return invalid("rule selection")
		}
	}
	if err := checkConfigFiles(d.RulePack, want.Lookup); err != nil {
		return err
	}
	if err := checkOutcome(d); err != nil {
		return err
	}
	execs, err := checkExecutions(d)
	if err != nil {
		return err
	}
	if err := checkCoverage(d.Coverage, want.Lookup); err != nil {
		return err
	}
	if err := checkObservations(d, execs, want.Lookup); err != nil {
		return err
	}
	for _, e := range d.ToolErrors {
		if _, ok := execs[e.Execution]; !ok || !code.MatchString(e.Kind) || !SafeText(e.Message, 8192) || !SafeText(e.Unit, 512) || e.Line < 0 || e.Column < 0 ||
			(e.Path != nil && domain.ValidateEvidencePath(*e.Path) != nil) {
			return invalid("tool error")
		}
	}
	p := d.Parse
	if p.Records < 0 || p.UnparsedBytes < 0 || p.ShapeErrors < 0 || p.Observations != len(d.Observations) || p.ToolErrors != len(d.ToolErrors) || !SafeText(p.DecodeError, 256) {
		return invalid("parse counters")
	}
	return nil
}

// checkConfigFiles: config files only when the tool honors repository
// configuration (null = not enumerated), sorted unique manifest paths bound
// to their manifest SHA-256.
func checkConfigFiles(r RulePack, lookup func(string) (string, bool)) error {
	rules := r.UnreportedSuppressionRules
	if rules == nil || len(rules) > 64 || (r.SourceSuppressions != SuppressReported && len(rules) != 0) {
		return invalid("unreported suppression rules")
	}
	for i, id := range rules {
		if id == "" || !SafeText(id, 128) || strings.ContainsAny(id, " \n\t") || (i > 0 && rules[i-1] >= id) {
			return invalid("unreported suppression rules must be sorted unique rule IDs")
		}
	}
	if r.RepositoryConfig != RepoConfigHonored && (r.ConfigFiles == nil || len(r.ConfigFiles) != 0) {
		return invalid("config files without honored repository configuration")
	}
	if len(r.ConfigFiles) > 256 {
		return invalid("config files")
	}
	for i, f := range r.ConfigFiles {
		if domain.ValidateEvidencePath(f.Path) != nil || !hex64.MatchString(f.SHA256) || (i > 0 && r.ConfigFiles[i-1].Path >= f.Path) {
			return invalid("config files must be sorted unique paths with SHA-256")
		}
		if lookup != nil {
			if sha, ok := lookup(f.Path); !ok || sha != f.SHA256 {
				return invalid("config file %s is not the manifest file", f.Path)
			}
		}
	}
	return nil
}

// Active reports the observations that are diagnostics a finding may come
// from: not suppressed in source and not a metric. A task whose only
// observations are metrics succeeds without findings.
func (d Document) Active() int {
	n := 0
	for _, o := range d.Observations {
		if !o.Suppressed && o.Metric == nil {
			n++
		}
	}
	return n
}

func checkOutcome(d Document) error {
	o := d.Outcome
	switch {
	case !o.TaskStatus.Terminal() || !slices.Contains(exitClasses, o.ExitClass):
		return invalid("outcome status or exit class")
	case (o.TaskStatus == domain.TaskSucceeded) != (o.Reason == nil), o.Reason != nil && !code.MatchString(*o.Reason):
		return invalid("outcome reason")
	}
	if o.TaskStatus == domain.TaskSucceeded {
		// Findings are the active observations: diagnostics hidden by an
		// in-source suppression are kept but are not findings.
		want := ExitSuccessWithoutFindings
		if d.Active() > 0 {
			want = ExitSuccessWithFindings
		}
		switch {
		case o.ExitClass != want:
			return invalid("exit class does not follow the parsed observations")
		case o.PartialParse || !d.Parse.OK || d.Parse.UnparsedBytes != 0 || d.Parse.ShapeErrors != 0 || d.Parse.DecodeError != "":
			return invalid("success without a complete parse")
		case len(d.ToolErrors) != 0 || len(d.WorkspaceDrift) != 0 || len(d.Executions) == 0:
			return invalid("success with tool errors, drift or no execution")
		case d.Coverage.ProcessedPaths == nil || len(d.Coverage.ProcessedPaths) == 0:
			return invalid("success without processed files")
		}
	} else if o.ExitClass == ExitSuccessWithFindings || o.ExitClass == ExitSuccessWithoutFindings {
		return invalid("a failed task cannot have a success exit class")
	}
	if o.ExitClass == ExitNotRun && (len(d.Observations) != 0 || o.PartialParse || d.Coverage.ProcessedPaths == nil || len(d.Coverage.ProcessedPaths) != 0) {
		// Nothing ran: nothing was processed, and that is known.
		return invalid("not_run claims processed files or observations")
	}
	if o.PartialParse && o.TaskStatus == domain.TaskSucceeded {
		return invalid("partial parse on success")
	}
	return nil
}

func checkExecutions(d Document) (map[string]Execution, error) {
	execs := map[string]Execution{}
	for _, e := range d.Executions {
		if !analysis.ValidTaskKey(e.Name) {
			return nil, invalid("execution name")
		}
		if _, dup := execs[e.Name]; dup {
			return nil, invalid("duplicate execution %s", e.Name)
		}
		for _, s := range []StreamRef{e.Stdout, e.Stderr} {
			if !hex64.MatchString(s.SHA256) || s.CapturedBytes < 0 || s.TotalBytes < s.CapturedBytes {
				return nil, invalid("execution %s stream", e.Name)
			}
		}
		if len(e.Command.Argv) > 0 && (strings.ContainsAny(e.Command.Argv[0], `/\`) || slices.ContainsFunc(e.Command.Argv[1:], looksAbsolute)) {
			return nil, invalid("execution %s command holds a node path", e.Name)
		}
		if e.Command.Dir != "." && e.Command.Dir != analysis.RedactedMarker && domain.ValidateEvidencePath(e.Command.Dir) != nil {
			return nil, invalid("execution %s dir", e.Name)
		}
		if d.Outcome.TaskStatus == domain.TaskSucceeded && (e.Status != executor.StatusExited || e.ExitCode == nil || e.Cleanup != executor.CleanupConfirmed ||
			e.Stdout.Truncated || e.Stdout.Incomplete || e.Stderr.Truncated || e.Stderr.Incomplete) {
			return nil, invalid("success with an unclean execution %s", e.Name)
		}
		execs[e.Name] = e
	}
	return execs, nil
}

func looksAbsolute(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) || (len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/'))
}

func checkCoverage(c Coverage, lookup func(string) (string, bool)) error {
	switch {
	case !slices.Contains(units, c.Unit):
		return invalid("coverage unit")
	case c.FilesInScope != nil && *c.FilesInScope < 0, c.FilesSkipped < 0, c.FilesUnlisted < 0:
		return invalid("coverage counts")
	case c.ProcessedPaths != nil && !sortedPaths(c.ProcessedPaths, lookup):
		return invalid("processed paths must be sorted unique manifest paths")
	}
	for k, v := range c.Units {
		if !code.MatchString(k) || v < 0 {
			return invalid("coverage unit state %q", k)
		}
	}
	return nil
}

func checkLocation(l *Location, lookup func(string) (string, bool)) bool {
	if l == nil {
		return true
	}
	if domain.ValidateEvidencePath(l.Path) != nil || l.StartLine < 1 || l.EndLine < l.StartLine || l.StartCol < 0 || l.EndCol < 0 ||
		(l.EndLine == l.StartLine && l.EndCol < l.StartCol && l.EndCol != 0) || !slices.Contains(columnUnits, l.ColumnUnit) || !hex64.MatchString(l.BlobSHA256) {
		return false
	}
	if lookup != nil {
		sha, ok := lookup(l.Path)
		return ok && sha == l.BlobSHA256
	}
	return true
}

func checkPlace(path *string, loc *Location, state string, lookup func(string) (string, bool)) bool {
	switch {
	case !slices.Contains(locationStates, state), (state == LocationResolved) != (loc != nil):
		return false
	case loc != nil && (path == nil || *path != loc.Path):
		return false
	case path != nil && domain.ValidateEvidencePath(*path) != nil:
		return false
	case (state == LocationNoPosition || state == LocationOutsideSnapshot) && path != nil:
		return false
	}
	return checkLocation(loc, lookup)
}

func checkObservations(d Document, execs map[string]Execution, lookup func(string) (string, bool)) error {
	seen := map[string]bool{}
	unresolved := 0
	for _, o := range d.Observations {
		switch {
		case !observationID.MatchString(o.ID) || seen[o.ID]:
			return invalid("observation id")
		case o.RuleID == "" || !SafeText(o.RuleID, 128) || strings.ContainsAny(o.RuleID, " \n\t"):
			return invalid("observation %s rule", o.ID)
		case !slices.Contains(Categories, o.Category):
			return invalid("observation %s category", o.ID)
		case o.RawSeverity != nil && (!SafeText(*o.RawSeverity, 32) || *o.RawSeverity == ""):
			return invalid("observation %s raw severity", o.ID)
		case !SafeText(o.RawMessage, 64<<10) || o.RawMessage == "":
			return invalid("observation %s message", o.ID)
		case !checkPlace(o.Path, o.Location, o.LocationState, lookup), o.Related == nil, len(o.Related) > 64:
			return invalid("observation %s location", o.ID)
		case o.SuggestedFixes < 0 || !SafeText(o.Unit, 512):
			return invalid("observation %s metadata", o.ID)
		case o.Location != nil && slices.Contains(d.WorkspaceDrift, o.Location.Path):
			return invalid("observation %s links a drifted file", o.ID)
		case o.Suppressed && d.RulePack.SourceSuppressions != SuppressReported:
			return invalid("observation %s is suppressed without reported source suppressions", o.ID)
		case o.Metric != nil && (!code.MatchString(o.Metric.Name) || !slices.Contains(MetricUnits, o.Metric.Unit) || o.Metric.Threshold < 0 || o.Metric.Value < o.Metric.Threshold || o.Suppressed):
			return invalid("observation %s metric", o.ID)
		case (d.Analyzer.Purpose == PurposeMetrics) != (o.Metric != nil) && (d.Analyzer.Purpose != "" || o.Metric == nil):
			// A metrics analyzer reports only metrics and a secrets analyzer
			// none; an envelope without a purpose may carry metrics (written
			// before the purpose was recorded).
			return invalid("observation %s does not match the analyzer purpose", o.ID)
		}
		for _, r := range o.Related {
			if !SafeText(r.RawMessage, 8192) || !checkPlace(r.Path, r.Location, r.LocationState, lookup) {
				return invalid("observation %s related location", o.ID)
			}
		}
		e, ok := execs[o.RawRef.Execution]
		ref := o.RawRef
		if !ok || !slices.Contains(streams, ref.Stream) || ref.RecordIndex < 0 || ref.ByteStart < 0 || ref.ByteEnd <= ref.ByteStart || !SafeText(ref.JSONPointer, 256) ||
			(ref.JSONPointer != "" && !strings.HasPrefix(ref.JSONPointer, "/")) {
			return invalid("observation %s raw reference", o.ID)
		}
		s := e.Stdout
		if ref.Stream == "stderr" {
			s = e.Stderr
		}
		if ref.StreamSHA256 != s.SHA256 || ref.ByteEnd > s.CapturedBytes {
			return invalid("observation %s raw reference is not bound to its stream", o.ID)
		}
		next := ref.ByteStart
		for _, related := range o.Related {
			if related.RawSpan == nil && related.Kind == "" { // earlier envelopes
				continue
			}
			span := related.RawSpan
			if !ValidRelatedKind(related.Kind) || span == nil || span.Start < next || span.End <= span.Start || span.End > ref.ByteEnd {
				return invalid("observation %s related raw reference", o.ID)
			}
			next = span.End
		}
		if o.LocationState != LocationResolved {
			unresolved++
		}
		seen[o.ID] = true
	}
	if unresolved != d.Parse.UnresolvedObservations {
		return invalid("unresolved observation count")
	}
	return nil
}
