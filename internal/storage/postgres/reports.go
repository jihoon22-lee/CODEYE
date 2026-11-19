package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Report publication. The controller publishes a report for the run's
// current attempt (attempt.epoch = run.lease_epoch) only after the attempt was
// released with confirmed cleanup:
//   - finalizing: the publish transaction also moves the run to the report
//     status (completed/partial/failed). Until then the run stays finalizing.
//   - cancel_requested/cancelling after an accepted completion: nothing else
//     can end the run, so publication settles it to cancelled.
//   - failed/cancelled with a fixed snapshot: report only, the run is unchanged.
// Everything is re-checked under the attempt lock in PublishReport, so a
// stale pass (retried run, newer epoch, changed state) cannot publish.

// PublicationAttempt is the attempt a pending report describes.
type PublicationAttempt struct {
	ID               string
	Number, Epoch    int64
	Status, Cleanup  string
	CompletionStatus *string
	CompletionReason *string
	ResultManifestID *string
}

// PublicationTask is a task row plus the ordinal of its execution in the attempt.
type PublicationTask struct {
	ID               string
	Key              *string
	Status           string
	Required         bool
	Reason           *string
	ExecutionOrdinal int64
	// Stage is the stored task stage; nil for a task recorded without one.
	Stage *string
}

// Publication is everything the report service loads for one run.
type Publication struct {
	Run                Run
	Profile, ScopeKind string
	Snapshot           struct {
		ID, Consistency string
		ManifestDigest  *string
		SourceSHA       *string
	}
	Attempt        PublicationAttempt
	Tasks          []PublicationTask
	Artifacts      []ArtifactRecord // finalized artifacts of the attempt
	SourceManifest *ArtifactRecord  // a finalized source_manifest of the run whose sha256 = manifest digest
	NextRevision   int
	// Reviews are the project's latest human review per fingerprint;
	// the builder stamps those in effect at publication.
	Reviews []FindingReview
}

const pendingReportsSQL = `SELECT r.id::text FROM runs r
	JOIN attempts a ON a.run_id=r.id AND a.epoch=r.lease_epoch
	WHERE a.status='released' AND a.cleanup_state='confirmed' AND r.snapshot_id IS NOT NULL
	  AND NOT EXISTS (SELECT 1 FROM report_rejections x WHERE x.attempt_id=a.id)
	  AND NOT EXISTS (SELECT 1 FROM reports p WHERE p.attempt_id=a.id)
	  AND (r.status='finalizing'
	       OR (r.status IN ('cancel_requested','cancelling') AND a.completion_status='finalizing')
	       OR r.status IN ('failed','cancelled'))
	ORDER BY r.created_at, r.id LIMIT $1`

// PendingReports lists runs whose current attempt has no published or
// rejected report yet. It performs no ACL check (controller service).
func (s *Store) PendingReports(ctx context.Context, limit int) ([]string, error) {
	if limit < 1 || limit > 1000 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.pool.Query(ctx, pendingReportsSQL, limit)
	if err != nil {
		return nil, classify(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return ids, classify(err)
}

// LoadPublication reads the run, snapshot, current attempt, tasks and
// artifacts for the report builder (controller service, no ACL).
func (s *Store) LoadPublication(ctx context.Context, runID string) (Publication, error) {
	if !validIDs(runID) {
		return Publication{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Publication, error) {
		var p Publication
		r, err := readRun(tx.QueryRow(ctx, "SELECT "+runColumns+" FROM runs WHERE id=$1", runID))
		if err != nil {
			return p, err
		}
		p.Run = r
		if r.SnapshotID == nil {
			return p, domain.ErrConflict
		}
		var request []byte
		if err = tx.QueryRow(ctx, "SELECT request FROM runs WHERE id=$1", runID).Scan(&request); err != nil {
			return p, err
		}
		var req struct {
			Profile string `json:"profile"`
			Scope   struct {
				Kind string `json:"kind"`
			} `json:"scope"`
		}
		if request == nil || json.Unmarshal(request, &req) != nil {
			return p, domain.ErrInvalid
		}
		p.Profile, p.ScopeKind = req.Profile, req.Scope.Kind
		if err = tx.QueryRow(ctx, "SELECT id::text,consistency,manifest_digest,source_sha FROM snapshots WHERE id=$1", *r.SnapshotID).
			Scan(&p.Snapshot.ID, &p.Snapshot.Consistency, &p.Snapshot.ManifestDigest, &p.Snapshot.SourceSHA); err != nil {
			return p, err
		}
		a := &p.Attempt
		if err = tx.QueryRow(ctx, `SELECT id::text,number,epoch,status,cleanup_state,completion_status,completion_reason,result_manifest_id::text
			FROM attempts WHERE run_id=$1 AND epoch=$2`, runID, r.LeaseEpoch).
			Scan(&a.ID, &a.Number, &a.Epoch, &a.Status, &a.Cleanup, &a.CompletionStatus, &a.CompletionReason, &a.ResultManifestID); err != nil {
			return p, err
		}
		rows, err := tx.Query(ctx, `SELECT t.id::text,t.key,t.status,t.required_for_verdict,t.reason,COALESCE((SELECT max(e.ordinal) FROM task_executions e WHERE e.attempt_id=$2 AND e.task_id=t.id),0),t.stage
			FROM tasks t WHERE t.run_id=$1 ORDER BY t.id`, runID, a.ID)
		if err != nil {
			return p, err
		}
		p.Tasks, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (PublicationTask, error) {
			var t PublicationTask
			err := row.Scan(&t.ID, &t.Key, &t.Status, &t.Required, &t.Reason, &t.ExecutionOrdinal, &t.Stage)
			return t, err
		})
		if err != nil {
			return p, err
		}
		rows, err = tx.Query(ctx, "SELECT id::text,run_id::text,attempt_id::text,role,media_type,sha256,storage_key,size_bytes FROM artifacts WHERE attempt_id=$1 AND finalized ORDER BY id", a.ID)
		if err != nil {
			return p, err
		}
		p.Artifacts, err = pgx.CollectRows(rows, scanArtifact)
		if err != nil {
			return p, err
		}
		if p.Snapshot.ManifestDigest != nil {
			rows, err = tx.Query(ctx, `SELECT id::text,run_id::text,attempt_id::text,role,media_type,sha256,storage_key,size_bytes FROM artifacts
				WHERE run_id=$1 AND role='source_manifest' AND finalized AND sha256=$2 ORDER BY (attempt_id=$3) DESC, created_at LIMIT 1`, runID, *p.Snapshot.ManifestDigest, a.ID)
			if err != nil {
				return p, err
			}
			found, err := pgx.CollectRows(rows, scanArtifact)
			if err != nil {
				return p, err
			}
			if len(found) == 1 {
				p.SourceManifest = &found[0]
			}
		}
		if err = tx.QueryRow(ctx, "SELECT COALESCE(max(revision),0)+1 FROM reports WHERE run_id=$1", runID).Scan(&p.NextRevision); err != nil {
			return p, err
		}
		p.Reviews, err = latestReviews(ctx, tx, r.ProjectID)
		return p, err
	})
}

func scanArtifact(row pgx.CollectableRow) (ArtifactRecord, error) {
	var a ArtifactRecord
	err := row.Scan(&a.ID, &a.RunID, &a.AttemptID, &a.Role, &a.MediaType, &a.SHA256, &a.StorageKey, &a.SizeBytes)
	return a, err
}

// ReportPublication is a built, validated report ready to store.
type ReportPublication struct {
	ReportID, RunID, AttemptID string
	Epoch                      int64
	ResultManifestID           *string
	InputDigest                string
	Revision                   int
	SchemaVersion              string
	BuilderVersion             string
	Status, QualityVerdict     string
	Body                       []byte
	// ExpectRunStatus is the run status observed when the input was loaded.
	ExpectRunStatus domain.RunStatus
}

// ReportRecord is a stored report.
type ReportRecord struct {
	ID, ProjectID, RunID, AttemptID string
	Revision                        int
	Status, QualityVerdict          string
	SchemaVersion                   string
	BodySHA256                      string
	SizeBytes                       int64
	Body                            []byte
	CreatedAt                       time.Time
}

// targetRunStatus maps the observed run status and the report status to the
// run status after publication. ok=false means this report may not be
// published for that run state.
func targetRunStatus(run domain.RunStatus, report string) (domain.RunStatus, bool) {
	switch run {
	case domain.RunFinalizing:
		switch report {
		case "completed", "partial", "failed":
			return domain.RunStatus(report), true
		}
	case domain.RunCancelRequested, domain.RunCancelling, domain.RunCancelled:
		return domain.RunCancelled, report == "cancelled"
	case domain.RunFailed:
		return domain.RunFailed, report == "failed"
	}
	return "", false
}

// PublishReport stores the report and, for a finalizing or cancel-pending run,
// moves the run in the same transaction (with a report.published event). The
// same input digest replays the stored report (created=false). A changed run
// state, lease epoch, attempt or result manifest is ErrStaleAttempt; a revision
// taken concurrently is ErrConflict (reload and rebuild).
func (s *Store) PublishReport(ctx context.Context, in ReportPublication) (ReportRecord, bool, error) {
	if !validIDs(in.ReportID, in.RunID, in.AttemptID) || in.Epoch < 1 || in.Revision < 1 || !hashPattern.MatchString(in.InputDigest) || len(in.Body) < 2 {
		return ReportRecord{}, false, domain.ErrInvalid
	}
	type out struct {
		rec     ReportRecord
		created bool
	}
	res, err := transaction(ctx, s, func(tx pgx.Tx) (out, error) {
		r, a, err := lockAttempt(ctx, tx, in.AttemptID)
		if err != nil {
			return out{}, err
		}
		if a.RunID != in.RunID {
			return out{}, domain.ErrNotFound
		}
		if existing, err := readReport(tx.QueryRow(ctx, "SELECT "+reportColumns+" FROM reports WHERE run_id=$1 AND input_digest=$2", in.RunID, in.InputDigest)); err == nil {
			return out{rec: existing}, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return out{}, err
		}
		var manifest *string
		if err = tx.QueryRow(ctx, "SELECT result_manifest_id::text FROM attempts WHERE id=$1", a.ID).Scan(&manifest); err != nil {
			return out{}, err
		}
		if a.Epoch != in.Epoch || r.LeaseEpoch != in.Epoch || a.Status != "released" || a.Cleanup != "confirmed" || r.Status != in.ExpectRunStatus ||
			(manifest == nil) != (in.ResultManifestID == nil) || (manifest != nil && *manifest != *in.ResultManifestID) {
			return out{}, domain.ErrStaleAttempt
		}
		next, ok := targetRunStatus(r.Status, in.Status)
		if !ok {
			return out{}, domain.ErrInvalid
		}
		var published bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM reports WHERE attempt_id=$1)", a.ID).Scan(&published); err != nil {
			return out{}, err
		}
		if published && r.Status == domain.RunFinalizing {
			return out{}, domain.ErrConflict
		}
		var expected int
		if err = tx.QueryRow(ctx, "SELECT COALESCE(max(revision),0)+1 FROM reports WHERE run_id=$1", in.RunID).Scan(&expected); err != nil {
			return out{}, err
		}
		if expected != in.Revision {
			return out{}, domain.ErrConflict
		}
		rec, err := readReport(tx.QueryRow(ctx, `INSERT INTO reports(id,project_id,run_id,revision,attempt_id,lease_epoch,result_manifest_id,input_digest,schema_version,builder_version,status,quality_verdict,body,body_sha256)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+reportColumns,
			in.ReportID, r.ProjectID, in.RunID, in.Revision, a.ID, in.Epoch, in.ResultManifestID, in.InputDigest, in.SchemaVersion, in.BuilderVersion, in.Status, in.QualityVerdict, in.Body, domain.Digest(in.Body)))
		if err != nil {
			return out{}, err
		}
		payload := map[string]any{"report_id": rec.ID, "revision": rec.Revision, "status": rec.Status, "quality_verdict": rec.QualityVerdict, "body_sha256": rec.BodySHA256, "attempt_id": a.ID}
		if r.Status == domain.RunCancelRequested {
			if err = transitionRun(ctx, tx, &r, domain.RunCancelling, "run.status", map[string]any{"status": domain.RunCancelling}); err != nil {
				return out{}, err
			}
		}
		if next == r.Status {
			// Report-only publication: the run state and version do not change.
			err = appendEvent(ctx, tx, &r, "report.published", payload, false)
		} else {
			err = transitionRun(ctx, tx, &r, next, "report.published", payload)
		}
		if err != nil {
			return out{}, err
		}
		return out{rec: rec, created: true}, nil
	})
	return res.rec, res.created, err
}

// RejectPublication records a deterministic refusal for the current attempt.
// A finalizing run cannot otherwise end, so it becomes failed with an event;
// failed/cancelled runs are unchanged. Stale callers get ErrStaleAttempt.
func (s *Store) RejectPublication(ctx context.Context, runID, attemptID string, epoch int64, expect domain.RunStatus, reason string, inputDigest *string) error {
	if !validIDs(runID, attemptID) || !reasonPattern.MatchString(reason) || (inputDigest != nil && !hashPattern.MatchString(*inputDigest)) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		r, a, err := lockAttempt(ctx, tx, attemptID)
		if err != nil {
			return false, err
		}
		if a.RunID != runID || a.Epoch != epoch || r.LeaseEpoch != epoch || r.Status != expect || a.Status != "released" || a.Cleanup != "confirmed" {
			return false, domain.ErrStaleAttempt
		}
		tag, err := tx.Exec(ctx, "INSERT INTO report_rejections(attempt_id,run_id,reason,input_digest) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", attemptID, runID, reason, inputDigest)
		if err != nil || tag.RowsAffected() == 0 {
			return false, err
		}
		payload := map[string]any{"attempt_id": attemptID, "reason": reason}
		switch r.Status {
		case domain.RunFinalizing:
			return true, transitionRun(ctx, tx, &r, domain.RunFailed, "report.rejected", payload)
		case domain.RunCancelRequested:
			if err = transitionRun(ctx, tx, &r, domain.RunCancelling, "run.status", map[string]any{"status": domain.RunCancelling}); err != nil {
				return false, err
			}
			return true, transitionRun(ctx, tx, &r, domain.RunCancelled, "report.rejected", payload)
		case domain.RunCancelling:
			return true, transitionRun(ctx, tx, &r, domain.RunCancelled, "report.rejected", payload)
		}
		return true, appendEvent(ctx, tx, &r, "report.rejected", payload, false)
	})
	return err
}

const reportColumns = "id::text,project_id::text,run_id::text,attempt_id::text,revision,status,quality_verdict,schema_version,body_sha256,octet_length(body),body,created_at"

func readReport(row pgx.Row) (ReportRecord, error) {
	var r ReportRecord
	err := row.Scan(&r.ID, &r.ProjectID, &r.RunID, &r.AttemptID, &r.Revision, &r.Status, &r.QualityVerdict, &r.SchemaVersion, &r.BodySHA256, &r.SizeBytes, &r.Body, &r.CreatedAt)
	r.CreatedAt = r.CreatedAt.UTC()
	return r, err
}

// GetReport returns revision (0 = latest) of the run's report for an actor who
// may read the run. A run without a published report is ErrNotFound.
func (s *Store) GetReport(ctx context.Context, actor, runID string, revision int) (ReportRecord, error) {
	return s.getReport(ctx, actor, runID, revision, nil)
}

// ReportExport is the format and code choice of a report download.
type ReportExport struct {
	Format      string
	IncludeCode bool
}

// ExportReport is GetReport for a download: the export (format, whether
// code is included) is audited in the same transaction as the permission
// check, before any byte is sent. If the audit row cannot be written the
// read fails.
func (s *Store) ExportReport(ctx context.Context, actor, runID string, revision int, ex ReportExport) (ReportRecord, error) {
	if ex.Format == "" || len(ex.Format) > 20 {
		return ReportRecord{}, domain.ErrInvalid
	}
	return s.getReport(ctx, actor, runID, revision, &ex)
}

func (s *Store) getReport(ctx context.Context, actor, runID string, revision int, ex *ReportExport) (ReportRecord, error) {
	if !validIDs(actor, runID) || revision < 0 {
		return ReportRecord{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (ReportRecord, error) {
		run, err := authorizeRunRead(ctx, tx, actor, runID)
		if err != nil {
			return ReportRecord{}, err
		}
		var rec ReportRecord
		if revision == 0 {
			rec, err = readReport(tx.QueryRow(ctx, "SELECT "+reportColumns+" FROM reports WHERE run_id=$1 ORDER BY revision DESC LIMIT 1", runID))
		} else {
			rec, err = readReport(tx.QueryRow(ctx, "SELECT "+reportColumns+" FROM reports WHERE run_id=$1 AND revision=$2", runID, revision))
		}
		if err != nil || ex == nil {
			return rec, err
		}
		e := userAudit(ctx, actor, AuditReportExported).target("report", rec.ID).in(run.ProjectID).
			with(map[string]any{"run_id": runID, "revision": rec.Revision, "format": ex.Format, "include_code": ex.IncludeCode})
		return rec, writeAudit(ctx, tx, e)
	})
}

// ReportSummary is a report row without its body.
type ReportSummary struct {
	ID                     string
	Revision               int
	Status, QualityVerdict string
	BodySHA256             string
	SizeBytes              int64
	CreatedAt              time.Time
}

// ListReports returns the run's report revisions (oldest first) for an actor
// who may read the run.
func (s *Store) ListReports(ctx context.Context, actor, runID string) ([]ReportSummary, error) {
	if !validIDs(actor, runID) {
		return nil, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) ([]ReportSummary, error) {
		if _, err := authorizeRunRead(ctx, tx, actor, runID); err != nil {
			return nil, err
		}
		rows, err := tx.Query(ctx, "SELECT id::text,revision,status,quality_verdict,body_sha256,octet_length(body),created_at FROM reports WHERE run_id=$1 ORDER BY revision", runID)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ReportSummary, error) {
			var r ReportSummary
			err := row.Scan(&r.ID, &r.Revision, &r.Status, &r.QualityVerdict, &r.BodySHA256, &r.SizeBytes, &r.CreatedAt)
			r.CreatedAt = r.CreatedAt.UTC()
			return r, err
		})
	})
}
