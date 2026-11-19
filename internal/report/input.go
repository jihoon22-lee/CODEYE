package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// ErrInvalidInput wraps every input the builder refuses. It is deterministic:
// the same input is refused again, so callers must not retry it.
var ErrInvalidInput = errors.New("report_input_invalid")

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

// RunFacts are the run row facts. FinalStatus is empty while the run is
// finalizing (the builder decides), or the terminal run status (failed,
// cancelled) for a report-only publication that cannot change the run.
type RunFacts struct {
	ID, ProjectID, Profile, ScopeKind string
	FinalStatus                       string
}

// SnapshotFacts are the run's fixed snapshot.
type SnapshotFacts struct {
	ID, ManifestDigest, Consistency string
	SourceSHA                       *string
}

// AttemptFacts are the attempt row whose accepted completion is reported.
type AttemptFacts struct {
	ID               string
	Number, Epoch    int64
	CompletionStatus string
	Cleanup          string
	Reason           *string
}

// TaskFacts are a DB task row (the authority for status) plus the ordinal of
// its execution row in this attempt (0 when the task never had one).
type TaskFacts struct {
	ID, Key, Status  string
	Required         bool
	Reason           *string
	ExecutionOrdinal int64
}

// ArtifactRef is a finalized artifact row of the attempt.
type ArtifactRef struct {
	ID, RunID, AttemptID, Role, MediaType, SHA256 string
	SizeBytes                                     int64
}

// Input is everything Build may read. The publisher fills Bodies only with
// bytes it read from the artifact store and re-hashed against the row.
type Input struct {
	ReportID string
	Revision int
	Now      time.Time
	Run      RunFacts
	Snapshot SnapshotFacts
	Attempt  AttemptFacts
	Tasks    []TaskFacts
	// SourceManifest are the W04 manifest bytes (sha256 == Snapshot.ManifestDigest).
	SourceManifest []byte
	// ResultManifest is the attempt's result_manifest artifact, nil if none.
	ResultManifest *ArtifactRef
	Artifacts      []ArtifactRef
	Bodies         map[string][]byte
	// Reviews are the project's latest human dispositions per fingerprint
	// (W23). Build stamps those in effect at Now on matching findings.
	Reviews []ReviewFact
}

// MaxDecodedBytes bounds the artifact bytes the publisher loads for decoding.
const MaxDecodedBytes = 32 << 20

// TaskExecutionID is the report form of the server task_executions key.
func TaskExecutionID(attempt, task string, ordinal int64) string {
	return attempt + "/" + task + "/" + strconv.FormatInt(ordinal, 10)
}

// InputDigest identifies the builder input for idempotent publication. It
// excludes ReportID, Revision and Now, so a reconcile pass after a crash or a
// second controller computes the same value.
func InputDigest(in Input) string {
	var b strings.Builder
	w := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
			b.WriteByte(0)
		}
		b.WriteByte('\n')
	}
	w(BuilderID, BuilderVersion, SchemaVersion)
	w("run", in.Run.ID, in.Run.Profile, in.Run.ScopeKind, in.Run.FinalStatus)
	w("snapshot", in.Snapshot.ID, in.Snapshot.ManifestDigest, in.Snapshot.Consistency, deref(in.Snapshot.SourceSHA))
	w("attempt", in.Attempt.ID, strconv.FormatInt(in.Attempt.Number, 10), strconv.FormatInt(in.Attempt.Epoch, 10), in.Attempt.CompletionStatus, in.Attempt.Cleanup, deref(in.Attempt.Reason))
	tasks := append([]TaskFacts(nil), in.Tasks...)
	sort.Slice(tasks, func(a, c int) bool { return tasks[a].ID < tasks[c].ID })
	for _, t := range tasks {
		w("task", t.ID, t.Key, t.Status, strconv.FormatBool(t.Required), deref(t.Reason), strconv.FormatInt(t.ExecutionOrdinal, 10))
	}
	if in.ResultManifest != nil {
		w("result_manifest", in.ResultManifest.ID, in.ResultManifest.SHA256)
	}
	arts := append([]ArtifactRef(nil), in.Artifacts...)
	sort.Slice(arts, func(a, c int) bool { return arts[a].ID < arts[c].ID })
	for _, a := range arts {
		w("artifact", a.ID, a.Role, a.MediaType, a.SHA256, strconv.FormatInt(a.SizeBytes, 10))
	}
	writeReviews(w, in.Reviews)
	return domain.Digest([]byte(b.String()))
}

func deref(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

// --- decoder registry (W12/W13 extension seam) -------------------------------

// DecoderKey selects the artifacts a decoder owns: media type and the
// document's top-level schema_version, and its "kind" when Kind is not empty.
// The artifact role is deliberately not part of the key (review documents
// arrive with role "other").
type DecoderKey struct {
	MediaType, SchemaVersion, Kind string
}

func (k DecoderKey) String() string {
	s := k.MediaType + ";schema_version=" + k.SchemaVersion
	if k.Kind != "" {
		s += ";kind=" + k.Kind
	}
	return s
}

// ExecutionRef is one execution of the task as recorded by the worker in the
// result manifest, with its stream artifacts resolved to attempt artifacts.
type ExecutionRef struct {
	ID             string // report execution ID "<task_key>/<name>"
	Manifest       analysis.ManifestExecution
	Stdout, Stderr *ArtifactRef
}

// SourceIndex looks up included file entries of the snapshot manifest.
type SourceIndex struct {
	entries map[string]string // path -> sha256 of included file/lfs_pointer entries
	files   int
}

// Lookup returns the manifest sha256 of an included file entry.
func (s *SourceIndex) Lookup(path string) (string, bool) {
	sha, ok := s.entries[path]
	return sha, ok
}

// DecodeContext is what a decoder may read. It never offers I/O.
type DecodeContext struct {
	RunID, AttemptID string
	Snapshot         SnapshotFacts
	Task             TaskFacts
	TaskExecutionID  string
	Executions       map[string]ExecutionRef // by execution name
	Source           *SourceIndex
}

// Contribution is what one accepted artifact adds to the report. Findings must
// reference only their own evidence, observations in this contribution and
// artifacts of the attempt; the builder re-validates everything.
type Contribution struct {
	Observations []Observation
	Findings     []Finding
	Tool         *ToolCoverage
	// Review is set only by review decoders (W12/W13). Accepted means the
	// decoder validated the review result bytes against the evidence catalog.
	Review      *ReviewResult
	Limitations []string
}

// ReviewResult is a decoded model review artifact of one task.
type ReviewResult struct {
	Accepted bool
	Record   *record.Record
	// R1 is set instead of Record for review-r1 (schema 0.2) records.
	R1 *record.R1Record
}

// ArtifactDecoder turns one verified artifact into a contribution. Decode must
// be deterministic and must not perform I/O. Any error rejects the artifact:
// its task's results are excluded and the report records the rejection.
type ArtifactDecoder interface {
	Key() DecoderKey
	Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error)
}

// Registry is an explicit set of decoders.
type Registry struct {
	byKey map[DecoderKey]ArtifactDecoder
}

// NewRegistry validates keys; duplicates are an error.
func NewRegistry(ds ...ArtifactDecoder) (*Registry, error) {
	r := &Registry{byKey: map[DecoderKey]ArtifactDecoder{}}
	for _, d := range ds {
		if d == nil {
			return nil, errors.New("report: nil decoder")
		}
		k := d.Key()
		if k.MediaType == "" || k.SchemaVersion == "" {
			return nil, errors.New("report: decoder key needs media type and schema version")
		}
		if _, dup := r.byKey[k]; dup {
			return nil, fmt.Errorf("report: duplicate decoder %s", k)
		}
		r.byKey[k] = d
	}
	return r, nil
}

// DefaultRegistry handles tool observations (go vet v0.1 and the generic
// analyzer envelope, docs/ANALYZERS.md), snapshot-bound review records and W15
// discovery documents.
func DefaultRegistry() *Registry {
	r, err := NewRegistry(GoVetDecoder{}, AnalyzerDecoder{}, ReviewDecoder{}, R1ReviewDecoder{}, EnvironmentDecoder{})
	if err != nil {
		panic(err)
	}
	return r
}

// lookup finds the decoder for an artifact by peeking at the document header.
func (r *Registry) lookup(art ArtifactRef, body []byte) (ArtifactDecoder, bool) {
	if r == nil || body == nil || !strings.HasSuffix(art.MediaType, "+json") {
		return nil, false
	}
	var head struct {
		SchemaVersion string `json:"schema_version"`
		Kind          string `json:"kind"`
	}
	if json.NewDecoder(bytes.NewReader(body)).Decode(&head) != nil || head.SchemaVersion == "" {
		return nil, false
	}
	if d, ok := r.byKey[DecoderKey{art.MediaType, head.SchemaVersion, head.Kind}]; ok {
		return d, true
	}
	d, ok := r.byKey[DecoderKey{art.MediaType, head.SchemaVersion, ""}]
	return d, ok
}

// Wants reports whether some decoder could own artifacts of this media type,
// so the publisher loads only bytes a decoder may read.
func (r *Registry) Wants(mediaType string) bool {
	if r == nil {
		return false
	}
	for k := range r.byKey {
		if k.MediaType == mediaType {
			return true
		}
	}
	return false
}
