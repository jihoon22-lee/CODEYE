// Package reportsvc publishes structured reports on the controller. It loads the
// run's structured state, re-hashes every artifact byte it reads from the
// artifact store, builds the report with internal/report and stores it in the
// same transaction that settles the run. It is safe to run repeatedly and from
// several controllers: publication is keyed by the builder input digest and
// fenced by the attempt lease epoch.
package reportsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/artifactstore"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Store is the persistence the publisher needs.
type Store interface {
	PendingReports(ctx context.Context, limit int) ([]string, error)
	LoadPublication(ctx context.Context, runID string) (postgres.Publication, error)
	PublishReport(ctx context.Context, in postgres.ReportPublication) (postgres.ReportRecord, bool, error)
	RejectPublication(ctx context.Context, runID, attemptID string, epoch int64, expect domain.RunStatus, reason string, inputDigest *string) error
}

// Publisher publishes pending reports.
type Publisher struct {
	Store     Store
	Artifacts *artifactstore.Store
	Registry  *report.Registry
	Now       func() time.Time
	Log       *slog.Logger
}

// Outcome of one run's publication attempt.
type Outcome string

const (
	OutcomePublished Outcome = "published"
	OutcomeReplayed  Outcome = "replayed"
	OutcomeRejected  Outcome = "rejected"
	OutcomeSkipped   Outcome = "skipped"
)

// ErrTransient marks failures worth retrying on the next pass (I/O, DB).
var ErrTransient = errors.New("report_publication_transient")

// Reconcile publishes every pending report once. It returns how many runs it
// published and the first transient error (the rest of the batch still runs).
func (p *Publisher) Reconcile(ctx context.Context) (int, error) {
	ids, err := p.Store.PendingReports(ctx, 50)
	if err != nil {
		return 0, err
	}
	published := 0
	var first error
	for _, id := range ids {
		out, err := p.PublishRun(ctx, id)
		if err != nil {
			if first == nil {
				first = err
			}
			p.log().Warn("report publication deferred", "run_id", id, "error_class", errorClass(err))
			continue
		}
		if out == OutcomePublished {
			published++
		}
		if out != OutcomeSkipped {
			p.log().Info("report publication", "run_id", id, "outcome", string(out))
		}
	}
	return published, first
}

func (p *Publisher) log() *slog.Logger {
	if p.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return p.Log
}

// finalStatus maps the observed run status to the builder's FinalStatus.
func finalStatus(s domain.RunStatus) (string, bool) {
	switch s {
	case domain.RunFinalizing:
		return "", true
	case domain.RunCancelRequested, domain.RunCancelling, domain.RunCancelled:
		return report.StatusCancelled, true
	case domain.RunFailed:
		return report.StatusFailed, true
	}
	return "", false
}

// PublishRun builds and stores the report of one run's current attempt.
func (p *Publisher) PublishRun(ctx context.Context, runID string) (Outcome, error) {
	pub, err := p.Store.LoadPublication(ctx, runID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			return OutcomeSkipped, nil
		}
		return "", fmt.Errorf("%w: load: %v", ErrTransient, err)
	}
	final, ok := finalStatus(pub.Run.Status)
	a := pub.Attempt
	if !ok || a.Status != "released" || a.Cleanup != "confirmed" || pub.Snapshot.ManifestDigest == nil {
		return OutcomeSkipped, nil
	}
	reject := func(reason string, digest *string) (Outcome, error) {
		err := p.Store.RejectPublication(ctx, runID, a.ID, a.Epoch, pub.Run.Status, reason, digest)
		switch {
		case err == nil:
			return OutcomeRejected, nil
		case errors.Is(err, domain.ErrStaleAttempt):
			return OutcomeSkipped, nil
		}
		return "", fmt.Errorf("%w: reject: %v", ErrTransient, err)
	}
	if p.Artifacts == nil {
		return "", fmt.Errorf("%w: artifact store not configured", ErrTransient)
	}
	if pub.SourceManifest == nil {
		return reject("source_manifest_unavailable", nil)
	}
	src, ok, err := p.read(*pub.SourceManifest)
	if err != nil {
		return "", err
	}
	if !ok {
		return reject("source_manifest_integrity_failed", nil)
	}
	project := &report.ProjectInfo{Name: pub.ProjectName, Source: pub.SourceLocation}
	if pub.StartedAt != nil {
		project.StartedAt = pub.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if pub.FinishedAt != nil {
		project.FinishedAt = pub.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	in := report.Input{Project: project, ReportID: domain.NewID(), Revision: pub.NextRevision, Now: p.now(),
		Run:      report.RunFacts{ID: pub.Run.ID, ProjectID: pub.Run.ProjectID, Profile: pub.Profile, ScopeKind: pub.ScopeKind, FinalStatus: final, ScopePaths: pub.ScopePaths},
		Snapshot: report.SnapshotFacts{ID: pub.Snapshot.ID, ManifestDigest: *pub.Snapshot.ManifestDigest, Consistency: pub.Snapshot.Consistency, SourceSHA: pub.Snapshot.SourceSHA},
		Attempt: report.AttemptFacts{ID: a.ID, Number: a.Number, Epoch: a.Epoch, Cleanup: a.Cleanup, Reason: a.CompletionReason,
			CompletionStatus: "not_recorded"},
		SourceManifest: src, Bodies: map[string][]byte{}, PullRequest: pub.PullRequest}
	if a.CompletionStatus != nil {
		in.Attempt.CompletionStatus = *a.CompletionStatus
	}
	for _, rv := range pub.Reviews {
		in.Reviews = append(in.Reviews, report.ReviewFact{Fingerprint: rv.Fingerprint, Version: rv.Version, Disposition: rv.Disposition, PathPrefix: rv.PathPrefix, ExpiresAt: rv.ExpiresAt})
	}
	for _, t := range pub.Tasks {
		if t.Key == nil {
			return reject("task_key_missing", nil)
		}
		facts := report.TaskFacts{ID: t.ID, Key: *t.Key, Status: t.Status, Required: t.Required, Reason: t.Reason, ExecutionOrdinal: t.ExecutionOrdinal}
		if t.Stage != nil {
			facts.Stage = *t.Stage
		}
		in.Tasks = append(in.Tasks, facts)
	}
	for _, art := range pub.Artifacts {
		ref := report.ArtifactRef{ID: art.ID, RunID: art.RunID, AttemptID: art.AttemptID, Role: art.Role, MediaType: art.MediaType, SHA256: art.SHA256, SizeBytes: art.SizeBytes}
		in.Artifacts = append(in.Artifacts, ref)
		isManifest := a.ResultManifestID != nil && *a.ResultManifestID == art.ID
		if isManifest {
			r := ref
			in.ResultManifest = &r
		}
		if !isManifest && !p.Registry.Wants(art.MediaType) {
			continue
		}
		body, ok, err := p.read(art)
		if err != nil {
			return "", err
		}
		// Bytes that no longer match their row are left out: the builder then
		// reports the document as missing/rejected instead of trusting it.
		if ok {
			in.Bodies[art.ID] = body
		}
	}
	if a.ResultManifestID != nil && in.ResultManifest == nil {
		return reject("result_manifest_not_attempt_artifact", nil)
	}
	readGoTestFailureBodies(&in, pub.Artifacts, p.read)
	digest := report.InputDigest(in)
	built, err := report.Build(in, p.Registry)
	if err != nil {
		if errors.Is(err, report.ErrInvalidInput) {
			return reject("report_input_invalid", &digest)
		}
		return "", fmt.Errorf("%w: build: %v", ErrTransient, err)
	}
	body, err := report.Marshal(built)
	if err != nil {
		return reject("report_input_invalid", &digest)
	}
	_, created, err := p.Store.PublishReport(ctx, postgres.ReportPublication{ReportID: built.ID, RunID: runID, AttemptID: a.ID, Epoch: a.Epoch, ResultManifestID: a.ResultManifestID,
		InputDigest: digest, Revision: built.Revision, SchemaVersion: report.SchemaVersion, BuilderVersion: report.BuilderVersion,
		Status: built.Status, QualityVerdict: built.QualityVerdict, Body: body, ExpectRunStatus: pub.Run.Status})
	switch {
	case err == nil && created:
		return OutcomePublished, nil
	case err == nil:
		return OutcomeReplayed, nil
	case errors.Is(err, domain.ErrStaleAttempt), errors.Is(err, domain.ErrNotFound):
		return OutcomeSkipped, nil
	case errors.Is(err, domain.ErrConflict):
		return "", fmt.Errorf("%w: revision taken concurrently", ErrTransient)
	}
	return "", fmt.Errorf("%w: publish: %v", ErrTransient, err)
}

func (p *Publisher) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

// read returns the artifact bytes and whether they match the row (size and
// sha256). A missing file or I/O error is transient; mismatching bytes are
// reported as ok=false.
func (p *Publisher) read(a postgres.ArtifactRecord) ([]byte, bool, error) {
	if a.SizeBytes > report.MaxDecodedBytes {
		return nil, false, nil
	}
	f, err := p.Artifacts.Open(a.StorageKey)
	if err != nil {
		if errors.Is(err, artifactstore.ErrIntegrity) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("%w: open artifact: %v", ErrTransient, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, a.SizeBytes+1))
	if err != nil {
		return nil, false, fmt.Errorf("%w: read artifact: %v", ErrTransient, err)
	}
	if int64(len(b)) != a.SizeBytes || domain.Digest(b) != a.SHA256 {
		return nil, false, nil
	}
	return b, true, nil
}

func errorClass(err error) string {
	for _, e := range []error{ErrTransient, domain.ErrConflict, domain.ErrInvalid, domain.ErrNotFound, context.DeadlineExceeded, context.Canceled} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "unknown"
}
