package postgres

import (
	"errors"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// A folder opened to everyone is usable by every user without a grant
// (registration, readable lists, claim re-check); closing it refuses new
// registrations and queued runs again. A node on the controller's machine
// (source:controller) takes the root without having it in its enrollment
// and gets its path; the job names the project owner for the Git token and
// carries the service's offer for that owner.
func TestEveryoneRootsOfferAndControllerNode(t *testing.T) {
	e := newNodeEnv(t)
	s, ctx := e.s, e.ctx
	admin, err := s.CreateAccount(ctx, OperatorPrincipal(), NewAccount{DisplayName: "admin", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	by := UserPrincipal(admin.ID)
	dir := t.TempDir()
	shared, err := s.AddSourceRootBy(ctx, by, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSourceRootBy(ctx, by, "again", dir); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("same path twice: %v", err)
	}
	if _, err := s.AddSourceRootBy(ctx, UserPrincipal(e.user), "x", t.TempDir()); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin add: %v", err)
	}
	other, err := s.CreateUserNamed(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	reg := ProjectRegistration{Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: shared.ID, RelativePath: "."}}
	if _, err := s.RegisterProject(ctx, other, reg); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("closed root: %v", err)
	}
	if err := s.SetSourceRootEveryone(ctx, by, shared.ID, true); err != nil {
		t.Fatal(err)
	}
	if roots, err := s.GrantedRootPaths(ctx, other); err != nil || len(roots) != 1 || roots[0].Path != dir {
		t.Fatalf("readable roots %v %v", roots, err)
	}
	p, err := s.RegisterProject(ctx, other, reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(ctx, other, p.ID, "k1", []byte(deepReview)); err != nil {
		t.Fatal(err)
	}
	four := 4
	if _, err := s.SetSchedulerLimits(ctx, &four, &four); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetPoolPolicy(ctx, PoolPolicyUpdate{Name: "default", MaxWorkers: &four}); err != nil {
		t.Fatal(err)
	}
	// The controller's own node, enrolled for no root.
	n := e.enroll("local")
	e.beat(n, hb(n.workers, "idle", []string{nodeproto.AnalysisCapability("deep-review"), nodeproto.ControllerSourcesCapability}))
	resp := e.claimOK(n, n.workers[0], "c1")
	if resp.Job.Source.RootPath != dir || resp.Job.Offered == nil || !resp.Job.Offered.ModelReview || resp.Job.Offered.BuildScripts {
		t.Fatalf("job %+v offer %+v", resp.Job.Source, resp.Job.Offered)
	}
	// The administrator's switches change what other users' projects get.
	off, on := false, true
	if _, err := s.SetServiceSettings(ctx, by, ServiceSettingsPatch{OthersModelReview: &off, OthersBuildScripts: &on}); err != nil {
		t.Fatal(err)
	}
	if o, err := s.OfferFor(ctx, other); err != nil || o.ModelReview || !o.BuildScripts {
		t.Fatalf("offer %+v %v", o, err)
	}
	if o, err := s.OfferFor(ctx, admin.ID); err != nil || !o.ModelReview || !o.BuildScripts {
		t.Fatalf("admin offer %+v %v", o, err)
	}
	if _, err := s.SetServiceSettings(ctx, UserPrincipal(other), ServiceSettingsPatch{OthersModelReview: &on}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin settings: %v", err)
	}
	// Closing the folder refuses the queued run of a user without a grant.
	queued, err := s.Enqueue(ctx, other, p.ID, "k2", []byte(deepReview))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceRootEveryone(ctx, by, shared.ID, false); err != nil {
		t.Fatal(err)
	}
	if roots, _ := s.GrantedRootPaths(ctx, other); len(roots) != 0 {
		t.Fatalf("closed root still readable: %v", roots)
	}
	res, err := s.ClaimAttempt(ctx, n.id, n.workers[1], "c2")
	if err != nil || res.Response != nil {
		t.Fatalf("claim after close: %+v %v", res.Response, err)
	}
	var status string
	if err := s.pool.QueryRow(ctx, "SELECT status FROM runs WHERE id=$1", queued.ID).Scan(&status); err != nil || status != string(domain.RunFailed) {
		t.Fatalf("queued run after close: %q %v", status, err)
	}
	// The offer follows who started the run, not who owns the project: a
	// member's run of the administrator's project gets the member's offer.
	if err := s.SetSourceRootEveryone(ctx, by, shared.ID, true); err != nil {
		t.Fatal(err)
	}
	ap, err := s.RegisterProject(ctx, admin.ID, ProjectRegistration{Name: "admin-project", Source: reg.Source})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMember(ctx, admin.ID, ap.ID, other, domain.RoleRunner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(ctx, other, ap.ID, "k3", []byte(deepReview)); err != nil {
		t.Fatal(err)
	}
	member := e.claimOK(n, n.workers[1], "c3")
	if member.Job.Offered == nil || member.Job.Offered.ModelReview || !member.Job.Offered.BuildScripts {
		t.Fatalf("member's run of the administrator's project: offer %+v", member.Job.Offered)
	}
	// Git hosts the administrator approves.
	g, created, err := s.AddProviderBy(ctx, by, "GHE.Example.com")
	if err != nil || !created || g.Kind != "ghes" || g.Host != "ghe.example.com" {
		t.Fatalf("provider %+v %v %v", g, created, err)
	}
	if _, created, err := s.AddProviderBy(ctx, by, "ghe.example.com"); err != nil || created {
		t.Fatalf("second add %v %v", created, err)
	}
	access, err := s.ListSourceRootAccess(ctx, by)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range access {
		if a.ID == shared.ID && (!a.Everyone || a.Path != dir) {
			t.Fatalf("access %+v", a)
		}
	}
}
