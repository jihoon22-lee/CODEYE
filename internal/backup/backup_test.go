package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jihoon22-lee/CODEYE/internal/artifactstore"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

const deepReview = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// pgBin finds pg_dump for tests: CODEYE_TEST_PG_BIN, else the PATH that
// scripts/test_postgres.py prepares. The product never searches PATH.
func pgBin(t *testing.T) string {
	if dir := os.Getenv("CODEYE_TEST_PG_BIN"); dir != "" {
		return dir
	}
	p, err := exec.LookPath("pg_dump")
	if err != nil {
		t.Skip("pg_dump not available (CODEYE_TEST_PG_BIN or PATH)")
	}
	if _, err := exec.LookPath(filepath.Join(filepath.Dir(p), "pg_restore")); err != nil {
		t.Skip("pg_restore not next to pg_dump")
	}
	return filepath.Dir(p)
}

// freshDatabase creates an empty database next to CODEYE_TEST_DATABASE_URL
// and returns its URL; it is dropped at cleanup.
func freshDatabase(t *testing.T, name string) string {
	t.Helper()
	admin := os.Getenv("CODEYE_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("isolated PostgreSQL URL not configured")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal("test DB connection failed")
	}
	db := "w25_" + name + "_" + strings.ReplaceAll(domain.NewID(), "-", "")[:12]
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)")
		_ = conn.Close(ctx)
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + db
	return u.String()
}

func open(t *testing.T, dsn string) *postgres.Store {
	t.Helper()
	s, err := postgres.Open(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// must fails the running test through a panic (cleanups still run).
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// world is a database with the TC-32 objects.
type world struct {
	owner, viewer, outsider string
	root, project, schedule string
	node, secret            string
	workers                 []string
	published, inflight     string
	inflightOwner           postgres.Ownership
	reportBody              []byte
	reportBlobs             []string
	session                 string
}

type node struct {
	t       *testing.T
	s       *postgres.Store
	art     *artifactstore.Store
	id      string
	root    string
	workers []string
}

func (n node) beat(states map[string]*string) {
	n.t.Helper()
	in := nodeproto.NodeHeartbeatRequest{ProtocolVersion: "0.1", Capabilities: []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.LocalSourceCapability(n.root)},
		Capacity: nodeproto.Capacity{CPUCores: 8, MemoryAvailableBytes: 16 << 30, DiskAvailableBytes: 100 << 30}, ObservedWorkers: []nodeproto.ObservedWorker{}, CleanupReports: []nodeproto.CleanupReport{}}
	for _, w := range n.workers {
		st := "idle"
		if a := states[w]; a != nil {
			st = "busy"
		}
		in.ObservedWorkers = append(in.ObservedWorkers, nodeproto.ObservedWorker{ID: w, State: st, AttemptID: states[w]})
	}
	must(n.s.NodeHeartbeat(context.Background(), n.id, in))
}

func (n node) claim(worker string) postgres.Ownership {
	n.t.Helper()
	res := must(n.s.ClaimAttempt(context.Background(), n.id, worker, domain.NewID()))
	if res.Response == nil {
		n.t.Fatal("no claim")
	}
	return postgres.Ownership{AttemptID: res.Response.Lease.AttemptID, NodeID: n.id, WorkerID: worker, Epoch: res.Response.Lease.LeaseEpoch}
}

// upload stores data in the artifact store and finalizes its row.
func (n node) upload(o postgres.Ownership, role string, data []byte) (string, string) {
	n.t.Helper()
	sha := digest(data)
	key := must(n.art.Put(bytes.NewReader(data), sha, int64(len(data))))
	out := must(n.s.FinalizeArtifact(context.Background(), n.id, o, postgres.ArtifactUpload{ClientArtifactID: domain.NewID(), Role: role, MediaType: "application/json", SHA256: sha, StorageKey: key, SizeBytes: int64(len(data))}))
	return out.ArtifactID, sha
}

func (n node) event(o postgres.Ownership, kind string, payload any) nodeproto.EventResponse {
	n.t.Helper()
	raw, _ := json.Marshal(payload)
	return must(n.s.RecordAttemptEvent(context.Background(), n.id, o, postgres.AttemptEvent{ClientEventID: domain.NewID(), Kind: kind, Payload: raw}))
}

func seed(t *testing.T, s *postgres.Store, artifactDir string) world {
	ctx := context.Background()
	var w world
	w.owner = must(s.CreateUserNamed(ctx, "owner"))
	w.viewer = must(s.CreateUserNamed(ctx, "viewer"))
	w.outsider = must(s.CreateUserNamed(ctx, "outsider"))
	root := must(s.AddSourceRoot(ctx, "tc32", "/srv/tc32"))
	w.root = root.ID
	if err := s.GrantSourceRoot(ctx, root.ID, w.owner); err != nil {
		t.Fatal(err)
	}
	p := must(s.RegisterProject(ctx, w.owner, postgres.ProjectRegistration{Name: "tc32", Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: root.ID, RelativePath: "."}}))
	w.project = p.ID
	if err := s.SetMember(ctx, w.owner, p.ID, w.viewer, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSchedulerLimits(ctx, nil, ptr(5)); err != nil {
		t.Fatal(err)
	}
	// A session that must not survive the restore.
	if err := s.IssueLaunchToken(ctx, w.owner, digest([]byte("launch")), time.Minute); err != nil {
		t.Fatal(err)
	}
	w.session = digest([]byte("session"))
	must(s.ExchangeLaunchToken(ctx, digest([]byte("launch")), w.session, time.Hour))
	// One node; run 1 is published with evidence, run 2 is in flight.
	token, secret := domain.NewID(), domain.NewID()
	must(s.IssueEnrollmentToken(ctx, "default", []string{root.ID}, digest([]byte(token)), time.Hour))
	ni := must(s.EnrollNode(ctx, postgres.Enrollment{TokenSHA256: digest([]byte(token)), SecretSHA256: digest([]byte(secret)), Name: "n1", OS: "linux", Arch: "amd64", Protocol: "0.1"}))
	w.node, w.secret = ni.ID, secret
	art := must(artifactstore.Open(artifactDir))
	n := node{t: t, s: s, art: art, id: ni.ID, root: root.ID, workers: []string{domain.NewID(), domain.NewID()}}
	w.workers = n.workers
	n.beat(nil)
	r1 := must(s.Enqueue(ctx, w.owner, p.ID, "r1", json.RawMessage(deepReview)))
	o1 := n.claim(n.workers[0])
	mid, msha := n.upload(o1, "source_manifest", []byte(`{"manifest":"r1"}`))
	n.event(o1, nodeproto.EventSnapshotFixed, nodeproto.SnapshotFixedPayload{ManifestDigest: msha, Kind: "local", Consistency: "stable_copy_best_effort", ManifestArtifactID: mid})
	task := n.event(o1, nodeproto.EventTaskCreated, nodeproto.TaskCreatedPayload{Key: "go-vet", Required: true})
	n.event(o1, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "running"})
	v := n.event(o1, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: *task.TaskID, ExpectedVersion: *task.Version, Status: "running"})
	n.event(o1, nodeproto.EventTaskStatus, nodeproto.TaskStatusPayload{TaskID: *task.TaskID, ExpectedVersion: *v.Version, Status: "succeeded"})
	_, osha := n.upload(o1, "observations", []byte(`{"observations":["printf"]}`))
	n.event(o1, nodeproto.EventRunAdvance, nodeproto.RunAdvancePayload{Status: "finalizing"})
	rm, rsha := n.upload(o1, "result_manifest", []byte(`{"result":"r1"}`))
	must(s.CompleteAttempt(ctx, ni.ID, o1, postgres.Completion{Outcome: "succeeded", Cleanup: "confirmed", ResultManifestID: &rm}))
	w.reportBlobs = []string{msha, osha, rsha}
	body, _ := json.Marshal(map[string]any{"run_id": r1.ID, "artifacts": []map[string]string{{"role": "source_manifest", "sha256": msha}, {"role": "observations", "sha256": osha}, {"role": "result_manifest", "sha256": rsha}}})
	w.reportBody = body
	if _, _, err := s.PublishReport(ctx, postgres.ReportPublication{ReportID: domain.NewID(), RunID: r1.ID, AttemptID: o1.AttemptID, Epoch: o1.Epoch, InputDigest: digest(body), Revision: 1,
		SchemaVersion: "0.1", BuilderVersion: "1", Status: "completed", QualityVerdict: "inconclusive", Body: body, ExpectRunStatus: domain.RunFinalizing, ResultManifestID: &rm}); err != nil {
		t.Fatal(err)
	}
	w.published = r1.ID
	n.beat(nil)
	r2 := must(s.Enqueue(ctx, w.owner, p.ID, "r2", json.RawMessage(deepReview)))
	w.inflightOwner = n.claim(n.workers[1])
	w.inflight = r2.ID
	n.upload(w.inflightOwner, "stdout", []byte("partial output of the in-flight attempt\n"))
	// A schedule whose slot is materialized once before the backup.
	sc := must(s.CreateSchedule(ctx, w.owner, p.ID, "sched", postgres.ScheduleInput{Cron: "@hourly", Timezone: "UTC", Overlap: "queue", Run: json.RawMessage(deepReview)}))
	w.schedule = sc.ID
	return w
}

func ptr(n int) *int { return &n }

func exec1(t *testing.T, dsn, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, q, args...); err != nil {
		t.Fatal(err)
	}
}

func scalar[T any](t *testing.T, dsn, q string, args ...any) T {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var v T
	if err := c.QueryRow(ctx, q, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TC-32 (W25, docs/47 §5): a backup taken while an attempt runs, with an
// artifact finalized between the snapshot and the blob copy, restores into a
// fresh database and artifact root with projects, runs, reports, evidence
// links (report -> blob hashes), ACL and schedule de-duplication intact, and
// the in-flight attempt fenced (its old lease and node are never honoured).
func TestTC32BackupRestoreRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PostgreSQL client tests run on the Linux CI job")
	}
	bin := pgBin(t)
	ctx := context.Background()
	srcURL := freshDatabase(t, "src")
	if err := postgres.Migrate(ctx, srcURL); err != nil {
		t.Fatal(err)
	}
	src := open(t, srcURL)
	base := t.TempDir()
	srcArt := filepath.Join(base, "src-artifacts")
	w := seed(t, src, srcArt)
	// Materialize the schedule's current slot once (make it due now).
	exec1(t, srcURL, "UPDATE schedules SET next_at=date_trunc('hour',clock_timestamp()) WHERE id=$1", w.schedule)
	ds := must(src.MaterializeSchedules(ctx))
	if len(ds) != 1 {
		t.Fatalf("materialized %+v", ds)
	}
	slot := ds[0].ScheduledFor
	scheduledRuns := scalar[int64](t, srcURL, "SELECT count(*) FROM runs WHERE schedule_id=$1", w.schedule)
	if scheduledRuns != 1 {
		t.Fatalf("scheduled runs %d", scheduledRuns)
	}

	// Backup; an artifact is finalized after the snapshot, before the copy.
	out := filepath.Join(base, "backup")
	art := must(artifactstore.Open(srcArt))
	late := node{t: t, s: src, art: art, id: w.node, root: w.root, workers: w.workers}
	var lateSHA string
	m, err := Create(ctx, src, Options{DatabaseURL: srcURL, PostgresBinDir: bin, ArtifactDir: srcArt, Output: out, PersonalUserID: w.owner,
		AfterSnapshot: func() { _, lateSHA = late.upload(w.inflightOwner, "stderr", []byte("written after the snapshot\n")) }})
	if err != nil {
		t.Fatal(err)
	}
	if m.Rows["artifacts"] != 4 || m.Artifacts.Count != 4 || m.MigrationVersion < 9 || m.PersonalUserID != w.owner || m.Consistency != "exported_snapshot" {
		t.Fatalf("manifest %+v", m)
	}
	if got := scalar[int64](t, srcURL, "SELECT count(*) FROM artifacts"); got != 5 {
		t.Fatalf("source must have the late artifact: %d", got)
	}
	for _, f := range []string{ManifestFile, DumpFile, ArtifactsFile} {
		st, err := os.Stat(filepath.Join(out, f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v %v", f, st.Mode(), err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(out, ManifestFile))
	if strings.Contains(string(raw), "127.0.0.1") || strings.Contains(string(raw), "password") || strings.Contains(string(raw), "w25_src") {
		t.Fatal("manifest holds connection details")
	}
	if _, err := os.Stat(blobPath(filepath.Join(out, BlobDir), lateSHA)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("blob finalized after the snapshot was copied")
	}
	// An existing non-empty output is refused.
	if _, err := Create(ctx, src, Options{DatabaseURL: srcURL, PostgresBinDir: bin, ArtifactDir: srcArt, Output: out}); err == nil {
		t.Fatal("backup overwrote a non-empty directory")
	}

	v, err := Verify(out)
	if err != nil {
		t.Fatal(err)
	}
	// A tampered copy is refused before any target is touched.
	tampered := filepath.Join(base, "tampered")
	copyTree(t, out, tampered)
	victim := blobPath(filepath.Join(tampered, BlobDir), w.reportBlobs[1])
	if err := os.WriteFile(victim, []byte(`{"observations":["altered"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(tampered); err == nil || !strings.Contains(err.Error(), "altered") {
		t.Fatalf("tampered blob: %v", err)
	}

	// Restore into a fresh database and artifact root.
	dstURL := freshDatabase(t, "dst")
	dst := open(t, dstURL)
	dstArt := filepath.Join(base, "dst-artifacts")
	notEmpty := filepath.Join(base, "busy-artifacts")
	if err := os.MkdirAll(filepath.Join(notEmpty, "sha256", "aa"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(notEmpty, "sha256", "aa", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(ctx, dst, v, RestoreOptions{DatabaseURL: dstURL, PostgresBinDir: bin, ArtifactDir: notEmpty}); err == nil {
		t.Fatal("restore into a non-empty artifact root")
	}
	if empty, err := dst.SchemaEmpty(ctx); err != nil || !empty {
		t.Fatal("refused restore touched the database")
	}
	rep, err := Restore(ctx, dst, v, RestoreOptions{DatabaseURL: dstURL, PostgresBinDir: bin, ArtifactDir: dstArt})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Blobs != 4 || rep.Fence.NodesRevoked != 1 || rep.Fence.AttemptsFenced != 1 || rep.Fence.SessionsRevoked != 1 {
		t.Fatalf("restore %+v", rep)
	}
	// A second restore into the now non-empty database is refused.
	if _, err := Restore(ctx, dst, v, RestoreOptions{DatabaseURL: dstURL, PostgresBinDir: bin, ArtifactDir: filepath.Join(base, "other")}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("restore into a non-empty database: %v", err)
	}
	if empty, _ := ArtifactRootEmpty(filepath.Join(base, "other")); !empty {
		t.Fatal("refused restore left artifacts behind")
	}

	// Projects, runs, reports and the evidence links.
	p := must(dst.GetProject(ctx, w.owner, w.project))
	if p.ID != w.project {
		t.Fatal("project")
	}
	reportRec := must(dst.GetReport(ctx, w.owner, w.published, 1))
	if !bytes.Equal(reportRec.Body, w.reportBody) {
		t.Fatal("report body changed")
	}
	var body struct {
		Artifacts []struct{ SHA256 string } `json:"artifacts"`
	}
	if err := json.Unmarshal(reportRec.Body, &body); err != nil || len(body.Artifacts) != 3 {
		t.Fatal("report body")
	}
	for _, a := range body.Artifacts {
		data, err := os.ReadFile(blobPath(dstArt, a.SHA256))
		if err != nil || digest(data) != a.SHA256 {
			t.Fatalf("evidence blob %s missing or altered", a.SHA256)
		}
		if n := scalar[int64](t, dstURL, "SELECT count(*) FROM artifacts WHERE sha256=$1 AND finalized AND run_id=$2", a.SHA256, w.published); n != 1 {
			t.Fatalf("evidence row %s: %d", a.SHA256, n)
		}
	}
	if n := scalar[int64](t, dstURL, "SELECT count(*) FROM artifacts WHERE sha256=$1", lateSHA); n != 0 {
		t.Fatal("the post-snapshot artifact row was restored")
	}
	// ACL: the viewer still sees the project, the outsider does not.
	if _, err := dst.GetProject(ctx, w.viewer, w.project); err != nil {
		t.Fatalf("viewer: %v", err)
	}
	if _, err := dst.GetProject(ctx, w.outsider, w.project); err == nil {
		t.Fatal("outsider sees the project")
	}
	if roots := must(dst.ListSourceRoots(ctx, w.owner)); len(roots) != 1 || roots[0].ID != w.root {
		t.Fatalf("source grant %+v", roots)
	}
	// Schedules: the materialized slot is not materialized again.
	scheds, _ := must2(t)(dst.ListSchedules(ctx, w.owner, w.project))
	if len(scheds) != 1 || scheds[0].ID != w.schedule {
		t.Fatalf("schedules %+v", scheds)
	}
	exec1(t, dstURL, "UPDATE schedules SET next_at=$2 WHERE id=$1", w.schedule, slot)
	must(dst.MaterializeSchedules(ctx))
	if n := scalar[int64](t, dstURL, "SELECT count(*) FROM runs WHERE schedule_id=$1 AND scheduled_for=$2", w.schedule, slot); n != 1 {
		t.Fatalf("schedule slot duplicated after restore: %d runs", n)
	}
	if n := scalar[int64](t, dstURL, "SELECT count(*) FROM schedule_occurrences WHERE schedule_id=$1 AND scheduled_for=$2", w.schedule, slot); n != 1 {
		t.Fatalf("occurrences %d", n)
	}
	// The in-flight attempt is fenced; the old node and session are dead.
	r2 := must(dst.GetRun(ctx, w.owner, w.inflight))
	if r2.Status != domain.RunRecoveryRequired {
		t.Fatalf("in-flight run %s", r2.Status)
	}
	if _, err := dst.RenewAttempt(ctx, w.node, w.inflightOwner); !errors.Is(err, domain.ErrStaleAttempt) {
		t.Fatalf("old lease renewed: %v", err)
	}
	if _, err := dst.AuthenticateNode(ctx, digest([]byte(w.secret))); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("old node authenticated: %v", err)
	}
	if _, err := dst.SessionByTokenHash(ctx, w.session); err == nil {
		t.Fatal("old session valid")
	}
	// A new node enrolls and runs the queued (scheduled) run.
	token, secret := domain.NewID(), domain.NewID()
	must(dst.IssueEnrollmentToken(ctx, "default", []string{w.root}, digest([]byte(token)), time.Hour))
	ni := must(dst.EnrollNode(ctx, postgres.Enrollment{TokenSHA256: digest([]byte(token)), SecretSHA256: digest([]byte(secret)), Name: "n2", OS: "linux", Arch: "amd64", Protocol: "0.1"}))
	fresh := node{t: t, s: dst, id: ni.ID, root: w.root, workers: []string{domain.NewID()}}
	fresh.beat(nil)
	o := fresh.claim(fresh.workers[0])
	if got := scalar[string](t, dstURL, "SELECT schedule_id::text FROM runs r JOIN attempts a ON a.run_id=r.id WHERE a.id=$1", o.AttemptID); got != w.schedule {
		t.Fatalf("new node claimed %s", got)
	}
}

func must2(t *testing.T) func(a []postgres.Schedule, b bool, err error) ([]postgres.Schedule, bool) {
	return func(a []postgres.Schedule, b bool, err error) ([]postgres.Schedule, bool) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return a, b
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o700)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSchemaEntryFilter(t *testing.T) {
	for line, want := range map[string]bool{
		"4; 2615 2200 SCHEMA - public pg_database_owner":            true,
		"4169; 0 0 COMMENT - SCHEMA public pg_database_owner":       true,
		"231; 1259 16390 TABLE public attempts codeye":              false,
		"; Archive created at 2026-11-18 18:34:43 UTC":              false,
		"3; 2615 16385 SCHEMA - test_abc codeye":                    true,
		"5000; 0 0 COMMENT public TABLE attempts SCHEMA-like owner": false,
	} {
		if got := schemaEntry.MatchString(line); got != want {
			t.Errorf("%q: %v", line, got)
		}
	}
}

func TestVerifyRejectsIncompleteSets(t *testing.T) {
	dir := t.TempDir()
	if _, err := Verify(dir); err == nil || !strings.Contains(err.Error(), "manifest.json is missing") {
		t.Fatalf("empty dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(`{"format":"codeye-backup/0.1","extra":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err == nil {
		t.Fatal("unknown manifest field accepted")
	}
	empty, err := ArtifactRootEmpty(filepath.Join(dir, "absent"))
	if err != nil || !empty {
		t.Fatal("absent root is empty")
	}
	if err := os.MkdirAll(filepath.Join(dir, "root", "sha256", "ab"), 0o700); err != nil {
		t.Fatal(err)
	}
	if empty, _ := ArtifactRootEmpty(filepath.Join(dir, "root")); !empty {
		t.Fatal("empty dirs only")
	}
}
