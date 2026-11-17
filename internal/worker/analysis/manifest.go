package analysis

import (
	"encoding/json"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// ResultManifestSchemaVersion is the result_manifest artifact version (W06 §8).
const ResultManifestSchemaVersion = "0.1"

// ResultManifest is the canonical JSON the worker uploads with role
// result_manifest. The worker fills server artifact IDs; W09 consumes it.
// No field may carry a node absolute path or an environment value.
type ResultManifest struct {
	SchemaVersion string            `json:"schema_version"`
	RunID         string            `json:"run_id"`
	AttemptID     string            `json:"attempt_id"`
	LeaseEpoch    int64             `json:"lease_epoch"`
	Snapshot      ManifestSnapshot  `json:"snapshot"`
	Handlers      []ManifestHandler `json:"handlers"`
	Tasks         []ManifestTask    `json:"tasks"`
	CleanupState  string            `json:"cleanup_state"` // confirmed | uncertain
	Limitations   []string          `json:"limitations"`
}

type ManifestSnapshot struct {
	ID             string `json:"id"`
	ManifestDigest string `json:"manifest_digest"`
}

type ManifestHandler struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Capability string `json:"capability"`
}

type ManifestTask struct {
	TaskID      string              `json:"task_id"`
	Key         string              `json:"key"`
	Required    bool                `json:"required"`
	Status      domain.TaskStatus   `json:"status"`
	Reason      *string             `json:"reason"`
	Executions  []ManifestExecution `json:"executions"`
	Artifacts   []ManifestArtifact  `json:"artifacts"`
	Limitations []string            `json:"limitations"`
}

type ManifestExecution struct {
	// Name is Execution.Name, or "unreported" for a run the worker recorded
	// but the handler did not return.
	Name             string           `json:"name"`
	Status           executor.Status  `json:"status"`
	ExitCode         *int             `json:"exit_code"`
	Reason           string           `json:"reason,omitempty"`
	Cleanup          executor.Cleanup `json:"cleanup"`
	Command          ManifestCommand  `json:"command"`
	StdoutArtifactID *string          `json:"stdout_artifact_id"`
	StderrArtifactID *string          `json:"stderr_artifact_id"`
	StdoutTruncated  bool             `json:"stdout_truncated"`
	StderrTruncated  bool             `json:"stderr_truncated"`
	StartedAt        string           `json:"started_at"`
	EndedAt          string           `json:"ended_at"`
}

// ManifestCommand is a path-free command record: argv[0] is a base name, Dir
// is workspace-relative ("." = workspace root) or "[REDACTED]".
type ManifestCommand struct {
	Argv    []string `json:"argv"`
	Dir     string   `json:"dir"`
	EnvKeys []string `json:"env_keys"`
}

type ManifestArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Role       string `json:"role"`
	MediaType  string `json:"media_type"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
}

// RedactedMarker is the W05 executor redaction marker. Handlers pass the
// workspace root as Spec.Redact, so recorded dirs and streams read
// "[REDACTED]/rel".
const RedactedMarker = "[REDACTED]"

// CommandFor converts an executor command record into a path-free manifest
// command: argv[0] becomes its base name, a dir under the redacted workspace
// becomes workspace-relative and any other absolute value becomes RedactedMarker.
func CommandFor(c executor.CommandRecord) ManifestCommand {
	argv := make([]string, len(c.Argv))
	for i, a := range c.Argv {
		switch {
		case i == 0:
			argv[i] = baseName(a)
		case looksAbsolute(a):
			argv[i] = RedactedMarker
		default:
			argv[i] = a
		}
	}
	keys := append([]string{}, c.EnvKeys...)
	return ManifestCommand{Argv: argv, Dir: RelativeDir(c.Dir), EnvKeys: keys}
}

// RelativeDir maps "[REDACTED]" → ".", "[REDACTED]/a/b" → "a/b" and any
// other absolute path → RedactedMarker.
func RelativeDir(dir string) string {
	d := filepath.ToSlash(dir)
	switch {
	case d == RedactedMarker:
		return "."
	case strings.HasPrefix(d, RedactedMarker+"/"):
		rel := path.Clean(strings.TrimPrefix(d, RedactedMarker+"/"))
		if rel == "." || domain.ValidateEvidencePath(rel) == nil {
			return rel
		}
		return RedactedMarker
	case d == "" || looksAbsolute(d) || strings.Contains(d, RedactedMarker):
		return RedactedMarker
	default:
		if domain.ValidateEvidencePath(d) == nil {
			return d
		}
		return RedactedMarker
	}
}

func baseName(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

var drivePattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func looksAbsolute(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") || drivePattern.MatchString(p)
}

// FormatTime renders execution timestamps in the manifest (UTC, RFC 3339 nanoseconds).
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate enforces the invariants W09 relies on.
func (m ResultManifest) Validate() error {
	if m.SchemaVersion != ResultManifestSchemaVersion {
		return invalidf("schema_version must be %s", ResultManifestSchemaVersion)
	}
	if m.RunID == "" || m.AttemptID == "" || m.LeaseEpoch <= 0 {
		return invalidf("run_id, attempt_id and lease_epoch are required")
	}
	if m.Snapshot.ID == "" || !hex64.MatchString(m.Snapshot.ManifestDigest) {
		return invalidf("snapshot id and 64-hex manifest_digest are required")
	}
	if m.CleanupState != "confirmed" && m.CleanupState != "uncertain" {
		return invalidf("cleanup_state must be confirmed or uncertain")
	}
	if m.Handlers == nil || m.Tasks == nil || m.Limitations == nil {
		return invalidf("handlers, tasks and limitations must be arrays")
	}
	uncertain := false
	keys := map[string]bool{}
	for _, t := range m.Tasks {
		if !ValidTaskKey(t.Key) || keys[t.Key] || t.TaskID == "" {
			return invalidf("task key %q is invalid or duplicated", t.Key)
		}
		keys[t.Key] = true
		if !t.Status.Valid() || !t.Status.Terminal() {
			return invalidf("task %s status %q is not terminal", t.Key, t.Status)
		}
		if t.Executions == nil || t.Artifacts == nil || t.Limitations == nil {
			return invalidf("task %s arrays must not be null", t.Key)
		}
		for _, e := range t.Executions {
			if e.Name != "unreported" && !ValidTaskKey(e.Name) {
				return invalidf("execution name %q is invalid", e.Name)
			}
			switch e.Cleanup {
			case executor.CleanupConfirmed, executor.CleanupNotStarted:
			case executor.CleanupUncertain:
				uncertain = true
			default:
				return invalidf("execution cleanup %q is invalid", e.Cleanup)
			}
			if e.Cleanup == executor.CleanupUncertain && t.Status == domain.TaskSucceeded {
				return invalidf("task %s succeeded with uncertain cleanup", t.Key)
			}
			if t.Status == domain.TaskSucceeded && (e.Status != executor.StatusExited || e.StdoutTruncated) {
				return invalidf("task %s succeeded with an execution that did not exit or was truncated", t.Key)
			}
			if e.Command.Dir != "." && e.Command.Dir != RedactedMarker && domain.ValidateEvidencePath(e.Command.Dir) != nil {
				return invalidf("execution dir must be workspace-relative")
			}
			for i, a := range e.Command.Argv {
				if looksAbsolute(a) || (i == 0 && strings.ContainsAny(a, "/\\")) {
					return invalidf("execution argv must not carry absolute paths")
				}
			}
			for _, k := range e.Command.EnvKeys {
				if strings.Contains(k, "=") {
					return invalidf("env_keys must be names only")
				}
			}
		}
		for _, a := range t.Artifacts {
			if a.ArtifactID == "" || a.Role == "" || a.MediaType == "" || !hex64.MatchString(a.SHA256) || a.SizeBytes < 0 {
				return invalidf("task %s artifact is incomplete", t.Key)
			}
		}
	}
	if uncertain && m.CleanupState != "uncertain" {
		return invalidf("an uncertain execution requires cleanup_state uncertain")
	}
	return nil
}

// MarshalCanonical returns the canonical JSON of a validated manifest.
func (m ResultManifest) MarshalCanonical() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return domain.CanonicalJSON(raw)
}
