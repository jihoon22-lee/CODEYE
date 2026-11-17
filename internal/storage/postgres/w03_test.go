package postgres

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

func hashOf(s string) string { return domain.Digest([]byte(s)) }

func TestLaunchTokenSessionLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	user, err := s.CreateUserNamed(ctx, " Alice ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUserNamed(ctx, "bad\x01name"); err != domain.ErrInvalid {
		t.Fatalf("control char name: %v", err)
	}
	expect(t, s.IssueLaunchToken(ctx, domain.NewID(), hashOf("x"), time.Minute), domain.ErrNotFound)
	expect(t, s.IssueLaunchToken(ctx, user, "nothex", time.Minute), domain.ErrInvalid)
	if err := s.IssueLaunchToken(ctx, user, hashOf("launch"), time.Minute); err != nil {
		t.Fatal(err)
	}
	// Concurrent exchanges of one token create exactly one session.
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = s.ExchangeLaunchToken(ctx, hashOf("launch"), hashOf("session-"+string(rune('a'+i))), time.Hour)
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if err != domain.ErrNotFound {
			t.Fatalf("exchange error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d sessions from one launch token", ok)
	}
	var sessions int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM auth_sessions").Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions = %d, %v", sessions, err)
	}
	var tokenHash string
	if err := s.pool.QueryRow(ctx, "SELECT token_sha256 FROM auth_sessions").Scan(&tokenHash); err != nil {
		t.Fatal(err)
	}
	sess, err := s.SessionByTokenHash(ctx, tokenHash)
	if err != nil || sess.UserID != user || time.Until(sess.ExpiresAt) < 50*time.Minute {
		t.Fatalf("session = %+v, %v", sess, err)
	}
	// Expired launch token (DB clock).
	if err := s.IssueLaunchToken(ctx, user, hashOf("short"), time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	_, err = s.ExchangeLaunchToken(ctx, hashOf("short"), hashOf("s2"), time.Hour)
	expect(t, err, domain.ErrNotFound)
	// Expired session (DB clock).
	if err := s.IssueLaunchToken(ctx, user, hashOf("l3"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExchangeLaunchToken(ctx, hashOf("l3"), hashOf("s3"), time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	_, err = s.SessionByTokenHash(ctx, hashOf("s3"))
	expect(t, err, domain.ErrNotFound)
	// Revocation.
	if err := s.RevokeSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeSession(ctx, sess.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	_, err = s.SessionByTokenHash(ctx, tokenHash)
	expect(t, err, domain.ErrNotFound)
	// Disabling revokes sessions and blocks new tokens/exchanges.
	if err := s.IssueLaunchToken(ctx, user, hashOf("l4"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExchangeLaunchToken(ctx, hashOf("l4"), hashOf("s4"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.IssueLaunchToken(ctx, user, hashOf("l5"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.DisableUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	_, err = s.SessionByTokenHash(ctx, hashOf("s4"))
	expect(t, err, domain.ErrNotFound)
	_, err = s.ExchangeLaunchToken(ctx, hashOf("l5"), hashOf("s5"), time.Hour)
	expect(t, err, domain.ErrNotFound)
	expect(t, s.IssueLaunchToken(ctx, user, hashOf("l6"), time.Minute), domain.ErrNotFound)
	if err := s.DisableUser(ctx, user); err != nil {
		t.Fatalf("second disable: %v", err)
	}
	expect(t, s.DisableUser(ctx, domain.NewID()), domain.ErrNotFound)
}

func registered(t *testing.T, s *Store) (context.Context, string, SourceRoot, Provider) {
	t.Helper()
	ctx := context.Background()
	user, err := s.CreateUserNamed(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.AddSourceRoot(ctx, "work", "/srv/codeye/work")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSourceRoot(ctx, root.ID, user); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSourceRoot(ctx, root.ID, user); err != nil {
		t.Fatalf("repeat grant: %v", err)
	}
	prov, err := s.AddProvider(ctx, "github", "GitHub.com", "")
	if err != nil {
		t.Fatal(err)
	}
	return ctx, user, root, prov
}

func TestSourceRegistryAndRegisterProject(t *testing.T) {
	s := testStore(t)
	ctx, user, root, prov := registered(t, s)
	for _, bad := range [][2]string{{"", "/x"}, {"rel", "x/y"}, {"dots", "/a/../b"}, {"ctl", "/a\x00"}} {
		if _, err := s.AddSourceRoot(ctx, bad[0], bad[1]); err != domain.ErrInvalid {
			t.Errorf("AddSourceRoot(%q,%q) = %v", bad[0], bad[1], err)
		}
	}
	_, err := s.AddSourceRoot(ctx, "work", "/other")
	expect(t, err, domain.ErrConflict)
	for _, bad := range [][3]string{{"github", "ghe.example", ""}, {"gitlab", "gitlab.com", ""}, {"ghes", "127.0.0.1", ""}, {"ghes", "ghe.example", "http://ghe.example/api"}, {"ghes", "ghe.example", "https://u:p@ghe.example/api"}} {
		if _, err := s.AddProvider(ctx, bad[0], bad[1], bad[2]); err != domain.ErrInvalid {
			t.Errorf("AddProvider(%v) = %v", bad, err)
		}
	}
	ghes, err := s.AddProvider(ctx, "ghes", "ghe.example", "https://ghe.example/api/v3")
	if err != nil {
		t.Fatal(err)
	}
	roots, err := s.ListSourceRoots(ctx, user)
	if err != nil || len(roots) != 1 || roots[0] != root {
		t.Fatalf("roots = %+v, %v", roots, err)
	}
	provs, err := s.ListProviders(ctx, user)
	if err != nil || len(provs) != 2 || prov.Host != "github.com" {
		t.Fatalf("providers = %+v, %v", provs, err)
	}

	local := domain.Source{Kind: domain.SourceLocal, SourceRootID: root.ID, RelativePath: "svc/api"}
	p, err := s.RegisterProject(ctx, user, ProjectRegistration{Source: local, Description: "  "})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "api" || p.Revision != 1 {
		t.Fatalf("project = %+v", p)
	}
	var cfg map[string]any
	if err := json.Unmarshal(p.Config, &cfg); err != nil || cfg["config_version"] != float64(1) || cfg["description"] != nil {
		t.Fatalf("config = %s", p.Config)
	}
	whole, err := s.RegisterProject(ctx, user, ProjectRegistration{Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: root.ID, RelativePath: "."}})
	if err != nil || whole.Name != "work" {
		t.Fatalf("root project = %+v, %v", whole, err)
	}
	git := domain.Source{Kind: domain.SourceGit, ProviderID: ghes.ID, RepositoryURL: "https://ghe.example/team/tool.git", Branch: "main"}
	gp, err := s.RegisterProject(ctx, user, ProjectRegistration{Name: "Tool", Source: git, Description: "desc"})
	if err != nil || gp.Name != "Tool" {
		t.Fatalf("git project = %+v, %v", gp, err)
	}
	mismatch := git
	mismatch.ProviderID = prov.ID
	_, err = s.RegisterProject(ctx, user, ProjectRegistration{Source: mismatch})
	expect(t, err, domain.ErrInvalid)
	unknownProv := git
	unknownProv.ProviderID = domain.NewID()
	_, err = s.RegisterProject(ctx, user, ProjectRegistration{Source: unknownProv})
	expect(t, err, domain.ErrNotFound)
	ssh := git
	ssh.RepositoryURL = "git@ghe.example:team/tool.git"
	_, err = s.RegisterProject(ctx, user, ProjectRegistration{Source: ssh})
	expect(t, err, domain.ErrUnsupported)

	other, err := s.CreateUserNamed(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RegisterProject(ctx, other, ProjectRegistration{Source: local})
	expect(t, err, domain.ErrNotFound) // root not granted
	if roots, _ := s.ListSourceRoots(ctx, other); len(roots) != 0 {
		t.Fatalf("ungranted roots visible: %+v", roots)
	}

	// Summaries and keyset pagination.
	page, err := s.ListProjects(ctx, user, "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" || page.Items[0].ID != gp.ID {
		t.Fatalf("page1 = %+v, %v", page, err)
	}
	page2, err := s.ListProjects(ctx, user, page.NextCursor, 2)
	if err != nil || len(page2.Items) != 1 || page2.NextCursor != "" || page2.Items[0].ID != p.ID {
		t.Fatalf("page2 = %+v, %v", page2, err)
	}
	_, err = s.ListProjects(ctx, user, "!!", 2)
	expect(t, err, domain.ErrInvalid)
	if none, err := s.ListProjects(ctx, other, "", 10); err != nil || len(none.Items) != 0 {
		t.Fatalf("other sees %+v, %v", none, err)
	}
	src, err := domain.ParseSource(page2.Items[0].Source)
	if err != nil || src != local {
		t.Fatalf("summary source = %s, %v", page2.Items[0].Source, err)
	}
	r, err := s.Enqueue(ctx, user, p.ID, "k1", json.RawMessage(`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := s.GetProjectSummary(ctx, user, p.ID)
	if err != nil || sum.LastRun == nil || sum.LastRun.ID != r.ID || sum.LastRun.Status != domain.RunQueued {
		t.Fatalf("summary = %+v, %v", sum, err)
	}
	_, err = s.GetProjectSummary(ctx, other, p.ID)
	expect(t, err, domain.ErrNotFound)

	v, err := s.GetRunView(ctx, user, r.ID)
	if err != nil || v.TotalTasks != 0 || v.CompletedTasks != 0 || v.Status != domain.RunQueued {
		t.Fatalf("view = %+v, %v", v, err)
	}
	_, err = s.GetRunView(ctx, other, r.ID)
	expect(t, err, domain.ErrNotFound)
	run, changed, err := s.CancelRun(ctx, user, r.ID)
	if err != nil || !changed || run.Status != domain.RunCancelled {
		t.Fatalf("cancel = %+v %v %v", run, changed, err)
	}
	run, changed, err = s.CancelRun(ctx, user, r.ID)
	if err != nil || changed || run.Status != domain.RunCancelled {
		t.Fatalf("repeat cancel = %+v %v %v", run, changed, err)
	}
}

func TestSchemaStatusReadOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	st, err := s.SchemaStatus(ctx)
	if err != nil || st.State != "current" || st.Applied != st.Latest {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if _, err := s.pool.Exec(ctx, "INSERT INTO codeye_schema_migrations(version,name,checksum) VALUES($1,'future',$2)", st.Latest+1, hashOf("f")); err != nil {
		t.Fatal(err)
	}
	st, err = s.SchemaStatus(ctx)
	if err != nil || st.State != "newer" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM codeye_schema_migrations WHERE version >= $1", st.Latest); err != nil {
		t.Fatal(err)
	}
	st, err = s.SchemaStatus(ctx)
	if err != nil || st.State != "outdated" || st.Applied != st.Latest-1 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if _, err := s.pool.Exec(ctx, "UPDATE codeye_schema_migrations SET checksum=$1 WHERE version=1", hashOf("drift")); err != nil {
		t.Fatal(err)
	}
	st, err = s.SchemaStatus(ctx)
	if err != nil || st.State != "inconsistent" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM codeye_schema_migrations").Scan(&n); err != nil || n != st.Latest-1 {
		t.Fatalf("SchemaStatus changed history: %d, %v", n, err)
	}
}

// TestDisableAndExchangeSerialize races DisableUser against launch-token
// exchanges. Whatever the interleaving, no live session may remain afterwards.
func TestDisableAndExchangeSerialize(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for round := 0; round < 20; round++ {
		user, err := s.CreateUserNamed(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		launch := hashOf("launch-" + user)
		if err := s.IssueLaunchToken(ctx, user, launch, time.Minute); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var exErr, disErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, exErr = s.ExchangeLaunchToken(ctx, launch, hashOf("sess-"+user), time.Hour)
		}()
		go func() { defer wg.Done(); <-start; disErr = s.DisableUser(ctx, user) }()
		close(start)
		wg.Wait()
		if disErr != nil || (exErr != nil && exErr != domain.ErrNotFound) {
			t.Fatalf("round %d: exchange %v disable %v", round, exErr, disErr)
		}
		var live int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM auth_sessions WHERE user_id=$1 AND revoked_at IS NULL", user).Scan(&live); err != nil || live != 0 {
			t.Fatalf("round %d: %d unrevoked sessions after disable (%v)", round, live, err)
		}
	}
}
