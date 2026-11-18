package report

import (
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// EnvironmentDecoder validates the W15 discovery artifacts of env.discover and
// docs.discover against the attempt and the snapshot manifest. They add no
// observation, finding or tool coverage to the report. Without a decoder the
// builder would count those succeeded required tasks as missing their
// artifact (artifact_missing, incomplete result) and flag the media type as
// unrecognized, downgrading every W15 report.
type EnvironmentDecoder struct{}

func (EnvironmentDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: envdoc.MediaType, SchemaVersion: envdoc.SchemaVersion}
}

func (EnvironmentDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	kind := map[string]string{envdoc.TaskEnvironment: envdoc.KindEnvironment, envdoc.TaskDocuments: envdoc.KindDocuments}[ctx.Task.Key]
	if kind == "" || art.Role != "other" {
		return Contribution{}, invalidf("environment document attached to unexpected task or role")
	}
	d, err := envdoc.Decode(body, envdoc.Expect{Snapshot: ctx.Snapshot.ManifestDigest, RunID: ctx.RunID, AttemptID: ctx.AttemptID, Kind: kind, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	if d.Environment != nil {
		// A node observation must be backed by the probe execution the worker
		// recorded for this task; an observed version needs a clean exit.
		for _, o := range d.Environment.Observations {
			if o.Execution == nil {
				if o.Status == envdoc.ObsObserved {
					return Contribution{}, invalidf("observed version without a recorded probe")
				}
				continue
			}
			e, ok := ctx.Executions[*o.Execution]
			if !ok {
				return Contribution{}, invalidf("observation names an unrecorded execution")
			}
			if o.Status == envdoc.ObsObserved && (e.Manifest.Status != executor.StatusExited || e.Manifest.ExitCode == nil || *e.Manifest.ExitCode != 0) {
				return Contribution{}, invalidf("observed version from an unclean probe")
			}
		}
	}
	return Contribution{}, nil
}
