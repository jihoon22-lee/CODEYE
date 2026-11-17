package govet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// DocumentSchemaVersion / DocumentKind identify the observations artifact
// (role "observations", media analysis.MediaObservations).
const (
	DocumentSchemaVersion = "0.1"
	DocumentKind          = "go_vet_observations"
	AnalyzerID            = "go.vet"
	AdapterVersion        = "1"
)

// Execution names inside one go-vet task.
const (
	execVersion = "go.version"
	execModEdit = "go.modedit"
	execList    = "go.list"
	execVet     = "go.vet"
)

// Location states. Only "resolved" observations carry a Location and may
// become code evidence.
const (
	LocationResolved        = "resolved"
	LocationNoPosition      = "unresolved_no_position"
	LocationOutsideSnapshot = "unresolved_outside_snapshot"
	LocationMissingFile     = "unresolved_missing_file"
	LocationLineOutOfRange  = "unresolved_line_out_of_range"
	LocationSourceDrift     = "source_drift"
)

// Package states.
const (
	PackageAnalyzedClean         = "analyzed_clean"
	PackageAnalyzedWithFindings  = "analyzed_with_findings"
	PackageFailedTypecheck       = "failed_typecheck"
	PackageDependencyUnavailable = "dependency_unavailable"
	PackageLoadError             = "load_error"
	PackageNotAnalyzed           = "not_analyzed"
	PackageUnknown               = "unknown"
)

// Exit classes (docs/06 §6). With -json, go vet exits 0 with or without
// findings, so the class comes from parsed stdout, not from the exit code.
const (
	ExitSuccessWithoutFindings = "success_without_findings"
	ExitSuccessWithFindings    = "success_with_findings"
	ExitConfigurationError     = "configuration_error"
	ExitExecutionError         = "execution_error"
	ExitTimeout                = "timeout"
	ExitCancelled              = "cancelled"
	ExitNotRun                 = "not_run"
)

// Document is the observations artifact of one go-vet task. It contains no
// node absolute path and no environment value; raw bytes are referenced by
// execution name + stream + sha256.
type Document struct {
	SchemaVersion  string          `json:"schema_version"`
	Kind           string          `json:"kind"`
	AnalyzerID     string          `json:"analyzer_id"`
	AdapterVersion string          `json:"adapter_version"`
	ParserID       string          `json:"parser_id"`
	ParserVersion  string          `json:"parser_version"`
	TaskKey        string          `json:"task_key"`
	ManifestDigest string          `json:"snapshot_manifest_digest"`
	Module         ModuleRef       `json:"module"`
	Patterns       []string        `json:"patterns"`
	ToolVersion    *string         `json:"tool_version"`
	Platform       *string         `json:"platform"`
	Outcome        Outcome         `json:"outcome"`
	Executions     []ExecSummary   `json:"executions"`
	Inventory      []PackageScope  `json:"inventory"`
	Coverage       Coverage        `json:"coverage"`
	Observations   []Observation   `json:"observations"`
	AnalyzerErrors []AnalyzerError `json:"analyzer_errors"`
	StderrFailures []StderrFailure `json:"stderr_failures"`
	Parse          ParseReport     `json:"parse"`
	WorkspaceDrift []string        `json:"workspace_drift"`
	Limitations    []string        `json:"limitations"`
}

type ModuleRef struct {
	Dir  string `json:"dir"`  // snapshot-relative, "." = root
	Path string `json:"path"` // module path from go.mod, "" if unknown
}

type Outcome struct {
	TaskStatus   domain.TaskStatus `json:"task_status"`
	Reason       *string           `json:"reason"`
	ExitClass    string            `json:"exit_class"`
	PartialParse bool              `json:"partial_parse"`
}

type StreamRef struct {
	SHA256        string `json:"sha256"`
	CapturedBytes int64  `json:"captured_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	Truncated     bool   `json:"truncated"`
	Incomplete    bool   `json:"incomplete"`
}

type ExecSummary struct {
	Name     string                   `json:"name"`
	Status   executor.Status          `json:"status"`
	Reason   string                   `json:"reason,omitempty"`
	ExitCode *int                     `json:"exit_code"`
	Cleanup  executor.Cleanup         `json:"cleanup"`
	Command  analysis.ManifestCommand `json:"command"`
	Stdout   StreamRef                `json:"stdout"`
	Stderr   StreamRef                `json:"stderr"`
}

type PackageScope struct {
	ImportPath     string   `json:"import_path"`
	Dir            string   `json:"dir"` // snapshot-relative
	State          string   `json:"state"`
	GoFiles        []string `json:"go_files"`         // snapshot-relative, incl. test files
	IgnoredGoFiles []string `json:"ignored_go_files"` // excluded by build constraints/cgo
	Errors         []string `json:"errors"`           // masked go list / stderr text
}

type Coverage struct {
	GoFilesInScope   int            `json:"go_files_in_scope"`  // included manifest .go files under the planned patterns
	ProcessedGoFiles *int           `json:"processed_go_files"` // files of analyzed_* packages; null if any listed package is unknown
	IgnoredGoFiles   int            `json:"ignored_go_files"`
	UnlistedGoFiles  int            `json:"unlisted_go_files"` // in scope but in no package go list reported
	Packages         map[string]int `json:"packages"`          // state → count
}

type Observation struct {
	ID             string       `json:"id"`
	Tool           string       `json:"tool"`
	ToolVersion    *string      `json:"tool_version"`
	RuleID         string       `json:"rule_id"`
	RawSeverity    *string      `json:"raw_severity"` // go vet has none; never invented
	RawMessage     string       `json:"raw_message"`
	Path           *string      `json:"path"` // snapshot-relative when the tool position maps into the workspace
	Location       *ObsLocation `json:"location"`
	LocationState  string       `json:"location_state"`
	RawRef         RawRef       `json:"raw_ref"`
	PackageKey     string       `json:"package_key"`
	SuggestedFixes int          `json:"suggested_fix_count"` // counted, never applied
	RelatedCount   int          `json:"related_count"`
}

type ObsLocation struct {
	Path       string `json:"path"`
	StartLine  int    `json:"start_line"`
	StartCol   int    `json:"start_col"`
	EndLine    int    `json:"end_line"`
	EndCol     int    `json:"end_col"`
	ColumnUnit string `json:"column_unit"`
	BlobSHA256 string `json:"blob_sha256"` // W04 manifest entry hash
}

// RawRef locates the raw bytes of an observation: the execution's stream
// (bound by sha256), the 0-based top-level JSON value, an RFC 6901 pointer
// inside it and the value's byte range. StreamIndex is a locator only: go vet
// output order is not deterministic, so IDs never depend on it.
type RawRef struct {
	Execution    string `json:"execution"`
	Stream       string `json:"stream"`
	StreamSHA256 string `json:"stream_sha256"`
	StreamIndex  int    `json:"stream_index"`
	JSONPointer  string `json:"json_pointer"`
	ByteStart    int64  `json:"byte_start"`
	ByteEnd      int64  `json:"byte_end"`
}

type StderrFailure struct {
	Execution  string  `json:"execution"`
	Kind       string  `json:"kind"`
	Package    string  `json:"package"`
	Path       *string `json:"path"`
	Line       int     `json:"line"`
	Column     int     `json:"column"`
	Message    string  `json:"message"`
	ByteOffset int64   `json:"byte_offset"`
}

type ParseReport struct {
	OK                     bool   `json:"ok"`
	StdoutValues           int    `json:"stdout_values"`
	EmptyValues            int    `json:"empty_values"`
	UnparsedBytes          int64  `json:"unparsed_bytes"`
	ShapeErrors            int    `json:"shape_errors"`
	DecodeError            string `json:"decode_error,omitempty"`
	ListUnparsedBytes      int64  `json:"list_unparsed_bytes"`
	ListDecodeError        string `json:"list_decode_error,omitempty"`
	StderrLinesDropped     int    `json:"stderr_lines_dropped"`
	Observations           int    `json:"observations"`
	UnresolvedObservations int    `json:"unresolved_observations"`
}

func sha256Hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func streamRef(s executor.Stream) StreamRef {
	return StreamRef{SHA256: sha256Hex(s.Data), CapturedBytes: int64(len(s.Data)), TotalBytes: s.TotalBytes, Truncated: s.Truncated, Incomplete: s.Incomplete}
}

// observationID is content-derived: package key, rule, normalized positions,
// message and an ordinal among identical tuples.
func observationID(pkg, rule, posn, end, message string, ordinal int) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{AnalyzerID, pkg, rule, posn, end, message, strconv.Itoa(ordinal)}, "\x00")))
	return "obs_" + hex.EncodeToString(sum[:])[:20]
}

// marshalDocument renders canonical JSON (sorted keys, no HTML escaping drift).
func marshalDocument(d Document) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return domain.CanonicalJSON(bytes.TrimSpace(buf.Bytes()))
}

func sortObservations(obs []Observation) {
	key := func(o Observation) string {
		p := ""
		if o.Path != nil {
			p = *o.Path
		}
		line, col := 0, 0
		if o.Location != nil {
			line, col = o.Location.StartLine, o.Location.StartCol
		}
		return p + "\x00" + pad(line) + "\x00" + pad(col) + "\x00" + o.RuleID + "\x00" + o.RawMessage + "\x00" + o.ID
	}
	sort.SliceStable(obs, func(a, b int) bool { return key(obs[a]) < key(obs[b]) })
}

func pad(n int) string { return strconv.FormatInt(int64(n)+1e12, 10) }

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	s := append([]string(nil), in...)
	sort.Strings(s)
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func joinRel(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return path.Join(dir, name)
}
