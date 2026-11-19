package report

import (
	"bytes"
	"encoding/json"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
)

// acceptResultManifest decodes and cross-checks the result manifest. A
// non-empty reason rejects it; the report then shows DB task rows only.
func acceptResultManifest(in Input, arts map[string]ArtifactRef, states map[string]*taskState) (analysis.ResultManifest, string) {
	var m analysis.ResultManifest
	a := arts[in.ResultManifest.ID]
	body, ok := in.Bodies[a.ID]
	if !ok || a.Role != "result_manifest" || a.MediaType != analysis.MediaResultManifest {
		return m, "unreadable"
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.More() || m.Validate() != nil {
		return m, "invalid"
	}
	if canon, err := domain.CanonicalJSON(body); err != nil || !bytes.Equal(canon, body) {
		return m, "not_canonical"
	}
	if m.RunID != in.Run.ID || m.AttemptID != in.Attempt.ID || m.LeaseEpoch != in.Attempt.Epoch {
		return m, "attempt_mismatch"
	}
	if m.Snapshot.ID != in.Snapshot.ID || m.Snapshot.ManifestDigest != in.Snapshot.ManifestDigest {
		return m, "snapshot_mismatch"
	}
	if m.CleanupState == "confirmed" && in.Attempt.Cleanup != "confirmed" {
		return m, "cleanup_mismatch"
	}
	for _, t := range m.Tasks {
		st, ok := states[t.Key]
		if !ok || st.facts.ID != t.TaskID || st.facts.Required != t.Required {
			return m, "task_mismatch"
		}
		// The accepted DB status is the authority; an artifact may never claim
		// success for a task the controller recorded otherwise.
		if t.Status == domain.TaskSucceeded && st.facts.Status != string(domain.TaskSucceeded) {
			return m, "task_status_upgrade"
		}
		if st.facts.ExecutionOrdinal < 1 && (len(t.Executions) > 0 || len(t.Artifacts) > 0) {
			return m, "task_execution_missing"
		}
		for _, e := range t.Executions {
			for role, id := range map[string]*string{"stdout": e.StdoutArtifactID, "stderr": e.StderrArtifactID} {
				if id == nil {
					continue
				}
				if x, ok := arts[*id]; !ok || x.Role != role {
					return m, "execution_artifact_unbound"
				}
			}
		}
		for _, ma := range t.Artifacts {
			x, ok := arts[ma.ArtifactID]
			if !ok || x.SHA256 != ma.SHA256 || x.SizeBytes != ma.SizeBytes || x.Role != ma.Role || x.MediaType != ma.MediaType {
				return m, "task_artifact_unbound"
			}
		}
	}
	return m, ""
}

func executionOf(ref ExecutionRef, key, texec string) Execution {
	e := ref.Manifest
	x := Execution{ID: ref.ID, TaskKey: key, TaskExecutionID: texec, Name: e.Name, Status: string(e.Status), ExitCode: e.ExitCode, Cleanup: string(e.Cleanup),
		Argv: append([]string{}, e.Command.Argv...), Cwd: e.Command.Dir, EnvKeys: append([]string{}, e.Command.EnvKeys...),
		StdoutArtifactID: e.StdoutArtifactID, StderrArtifactID: e.StderrArtifactID, StdoutTruncated: e.StdoutTruncated, StderrTruncated: e.StderrTruncated}
	if e.Reason != "" {
		reason := e.Reason
		x.Reason = &reason
	}
	if e.StartedAt != "" {
		s := e.StartedAt
		x.StartedAt = &s
	}
	if e.EndedAt != "" {
		s := e.EndedAt
		x.EndedAt = &s
	}
	return x
}

// checkContribution enforces the evidence rules on decoder output before it
// may enter the report.
func checkContribution(c Contribution, obsSeen, findSeen map[string]bool, arts map[string]ArtifactRef, index *SourceIndex, snapshot string, ctx DecodeContext) error {
	local := map[string]bool{}
	for _, o := range c.Observations {
		if o.ID == "" || obsSeen[o.ID] || local[o.ID] || o.TaskKey != ctx.Task.Key {
			return invalidf("observation id %q is empty or duplicated", o.ID)
		}
		if _, ok := arts[o.RawRef.ArtifactID]; !ok {
			return invalidf("observation raw_ref is not an attempt artifact")
		}
		if o.Location != nil && o.Location.SnapshotID != snapshot {
			return invalidf("observation location snapshot mismatch")
		}
		local[o.ID] = true
	}
	artIDs := map[string]bool{}
	for id := range arts {
		artIDs[id] = true
	}
	localFind := map[string]bool{}
	for _, f := range c.Findings {
		if f.ID == "" || findSeen[f.ID] || localFind[f.ID] {
			return invalidf("finding id %q is empty or duplicated", f.ID)
		}
		localFind[f.ID] = true
		if err := validateFinding(f, snapshot, local, artIDs); err != nil {
			return err
		}
		// Only a review decoder may produce anything but unreviewed findings.
		if c.Review == nil && f.Validation != "unreviewed" {
			return invalidf("tool-only finding must be unreviewed")
		}
		for _, id := range f.ObservationIDs {
			if !local[id] {
				return invalidf("finding references an unknown observation")
			}
		}
		for _, e := range f.Evidence {
			switch e.Kind {
			case "code", "document":
				if e.Location == nil || e.Location.SnapshotID != snapshot {
					return invalidf("code evidence without a snapshot location")
				}
				if sha, ok := index.Lookup(e.Location.Path); !ok || sha != e.BlobSHA256 {
					return invalidf("code evidence path/hash is not in the source manifest")
				}
			case "tool", "execution":
				if _, ok := arts[e.ArtifactID]; !ok || e.TaskExecutionID != ctx.TaskExecutionID {
					return invalidf("tool evidence is not bound to an attempt artifact")
				}
			}
		}
		for _, l := range f.Locations {
			if l.SnapshotID != snapshot {
				return invalidf("finding location snapshot mismatch")
			}
			if _, ok := index.Lookup(l.Path); !ok {
				return invalidf("finding location is not in the source manifest")
			}
		}
	}
	for _, l := range c.Limitations {
		if !codePattern.MatchString(l) {
			return invalidf("limitation must be a code")
		}
	}
	return nil
}
