package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// TC-248 (controller part): retention lists only raw roles of finished runs
// older than the age; a content shared with a newer present row is not
// freed; applying marks the rows once, writes one audit row, keeps reports,
// and backup and the artifact quota stop counting the removed contents.
func TestRetentionMarksOldRawArtifactsOfFinishedRuns(t *testing.T) {
	e := newNodeEnv(t)
	n := e.enroll("a", e.root.ID)
	e.idle(n)
	r := e.enqueue("r1", deepReview)
	o := owner(n, e.claimOK(n, n.workers[0], "k"))
	shared := domain.Digest([]byte("shared stderr"))
	stdout := e.artifact(n, o, "stdout", domain.Digest([]byte("old stdout")))
	stderr := e.artifact(n, o, "stderr", shared)
	observations := e.artifact(n, o, "observations", domain.Digest([]byte("observations")))
	if _, err := e.s.PlanRetention(e.ctx, time.Minute); err == nil {
		t.Fatal("an age under an hour was accepted")
	}
	// Not finished yet: nothing is a candidate even when old.
	backdateArtifacts(t, e.s, 40*24*time.Hour)
	p, err := e.s.PlanRetention(e.ctx, 30*24*time.Hour)
	if err != nil || len(p.Artifacts) != 0 {
		t.Fatalf("running run: %+v %v", p, err)
	}
	if _, err := e.s.CompleteAttempt(e.ctx, n.id, o, Completion{Outcome: "failed", Cleanup: "confirmed"}); err != nil {
		t.Fatal(err)
	}
	// A newer run shares the stderr content.
	e.idle(n)
	e.enqueue("r2", deepReview)
	o2 := owner(n, e.claimOK(n, n.workers[0], "k2"))
	e.artifact(n, o2, "stderr", shared)
	p, err = e.s.PlanRetention(e.ctx, 30*24*time.Hour)
	if err != nil || len(p.Artifacts) != 2 || p.Runs != 1 || len(p.FreedBlobs) != 1 || p.FreedBlobs[0].SHA256 != domain.Digest([]byte("old stdout")) {
		t.Fatalf("plan %+v %v", p, err)
	}
	roles := map[string]string{}
	for _, a := range p.Artifacts {
		roles[a.ID] = a.Role
	}
	if roles[stdout] != "stdout" || roles[stderr] != "stderr" || roles[observations] != "" {
		t.Fatalf("roles %v", roles)
	}
	// The dry run changed nothing.
	if e.count("SELECT count(*) FROM artifacts WHERE content_removed_at IS NOT NULL") != 0 {
		t.Fatal("dry run marked rows")
	}
	var usageBefore int64
	if err := e.s.pool.QueryRow(e.ctx, ArtifactUsageSQL).Scan(&usageBefore); err != nil {
		t.Fatal(err)
	}
	applied, err := e.s.ApplyRetention(e.ctx, 30*24*time.Hour)
	if err != nil || len(applied.Artifacts) != 2 {
		t.Fatalf("apply %+v %v", applied, err)
	}
	if e.count("SELECT count(*) FROM artifacts WHERE content_removed_at IS NOT NULL") != 2 ||
		e.count("SELECT count(*) FROM audit_events WHERE action='storage.retention_applied' AND actor_kind='operator_cli' AND (detail->>'artifacts_removed')::int=2 AND (detail->>'older_than_days')::int=30") != 1 {
		t.Fatal("rows or audit row not written")
	}
	var usageAfter int64
	if err := e.s.pool.QueryRow(e.ctx, ArtifactUsageSQL).Scan(&usageAfter); err != nil {
		t.Fatal(err)
	}
	if usageAfter != usageBefore-10 { // the old stdout (10 bytes in the test upload) is no longer counted
		t.Fatalf("usage %d -> %d", usageBefore, usageAfter)
	}
	present, err := e.s.ContentPresent(e.ctx, shared)
	if err != nil || !present {
		t.Fatal("shared content not present")
	}
	err = e.s.WithBackupPoint(e.ctx, func(b BackupPoint) error {
		for _, blob := range b.Blobs {
			if blob.SHA256 == domain.Digest([]byte("old stdout")) {
				t.Error("backup lists a removed content")
			}
		}
		if b.MetadataOnly < 1 {
			t.Errorf("metadata-only count %d", b.MetadataOnly)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Set once: the mark cannot be cleared or moved.
	_, err = e.s.pool.Exec(context.Background(), "UPDATE artifacts SET content_removed_at=NULL WHERE id=$1", stdout)
	if err == nil || !strings.Contains(err.Error(), "CY002") {
		t.Fatalf("clearing the mark: %v", err)
	}
	// A second apply has nothing left; the run view names the removal.
	again, err := e.s.ApplyRetention(e.ctx, 30*24*time.Hour)
	if err != nil || len(again.Artifacts) != 0 {
		t.Fatalf("second apply %+v %v", again, err)
	}
	v, err := e.s.GetRunView(e.ctx, e.user, r.ID)
	if err != nil || v.RawEvidenceRemovedAt == nil {
		t.Fatalf("run view %+v %v", v.RawEvidenceRemovedAt, err)
	}
}

// backdateArtifacts moves every artifact's created_at back by age. The
// column is immutable by trigger; the test bypasses triggers for this one
// statement (session_replication_role; the test role is the superuser of
// its throwaway cluster).
func backdateArtifacts(t *testing.T, s *Store, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL session_replication_role=replica"); err != nil {
		t.Skip("cannot bypass triggers to age artifacts:", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE artifacts SET created_at=created_at-$1*interval '1 millisecond'", age.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
