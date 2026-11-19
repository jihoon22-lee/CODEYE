package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const testPassword = "correct horse battery staple"

func fastStore(t *testing.T) *Store {
	s := testStore(t)
	s.SetPasswordIterations(account.MinIterations)
	return s
}

func newAccount(t *testing.T, s *Store, by Principal, username string, admin bool) User {
	t.Helper()
	h, err := account.Derive(testPassword, s.kdfIterations())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateAccount(context.Background(), by, NewAccount{Username: username, IsAdmin: admin, Password: &h})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func login(s *Store, user, password, client string) (Session, error) {
	return s.PasswordLogin(context.Background(), PasswordLogin{Username: user, Password: password, Client: client,
		SessionTokenSHA256: domain.Digest([]byte(domain.NewID())), TTL: time.Hour})
}

// auditRows returns (action, actor_kind, target_id, detail) of rows matching action, oldest first.
func auditRows(t *testing.T, s *Store, action string) []map[string]any {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), "SELECT actor_kind,coalesce(actor_user_id::text,''),coalesce(target_id,''),coalesce(project_id::text,''),detail FROM audit_events WHERE action=$1 ORDER BY id", action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var kind, actor, target, project string
		var detail map[string]any
		if err := rows.Scan(&kind, &actor, &target, &project, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, map[string]any{"kind": kind, "actor": actor, "target": target, "project": project, "detail": detail})
	}
	return out
}

// TC-148
func TestPasswordLoginOutcomesAreIndistinguishable(t *testing.T) {
	s := fastStore(t)
	ctx := context.Background()
	alice := newAccount(t, s, OperatorPrincipal(), "Alice", false)
	if alice.Username == nil || *alice.Username != "alice" || !alice.HasPassword || alice.IsAdmin {
		t.Fatalf("%+v", alice)
	}
	if _, err := s.CreateAccount(ctx, OperatorPrincipal(), NewAccount{Username: "ALICE"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	linkOnly, _ := s.CreateUserNamed(ctx, "link only")
	gone := newAccount(t, s, OperatorPrincipal(), "gone", false)
	if err := s.DisableUser(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	sess, err := login(s, "ALICE", testPassword, "192.0.2.1")
	if err != nil || sess.UserID != alice.ID || sess.Username == nil || *sess.Username != "alice" || sess.MustChangePassword || !sess.HasPassword {
		t.Fatalf("login: %+v %v", sess, err)
	}
	for _, c := range []struct{ user, password string }{
		{"alice", "wrong password value"}, {"nobody", testPassword}, {"gone", testPassword}, {"not a valid name!", testPassword},
	} {
		if _, err := login(s, c.user, c.password, "192.0.2.1"); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("%s: %v", c.user, err)
		}
	}
	_ = linkOnly
	ok := auditRows(t, s, AuditLoginSucceeded)
	failed := auditRows(t, s, AuditLoginFailed)
	if len(ok) != 1 || ok[0]["actor"] != alice.ID || ok[0]["detail"].(map[string]any)["method"] != MethodPassword {
		t.Fatalf("success rows %v", ok)
	}
	if len(failed) != 4 {
		t.Fatalf("failure rows %v", failed)
	}
	for _, f := range failed {
		raw, _ := json.Marshal(f)
		if f["kind"] != ActorAnonymous || f["actor"] != "" || strings.Contains(string(raw), "nobody") || strings.Contains(string(raw), "valid name") || strings.Contains(string(raw), "wrong password") {
			t.Fatalf("failure row leaks the submitted name or password: %s", raw)
		}
	}
	// Known accounts are the target; an unknown name is not recorded at all.
	if failed[0]["target"] != alice.ID || failed[1]["target"] != "" || failed[2]["target"] != gone.ID {
		t.Fatalf("targets %v", failed)
	}
	var stored int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM auth_throttle WHERE key_hmac LIKE '%alice%' OR key_hmac LIKE '%nobody%'").Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("throttle keys hold names: %d %v", stored, err)
	}
}

// TC-149
func TestLoginThrottleByAccountAndAddress(t *testing.T) {
	s := fastStore(t)
	ctx := context.Background()
	newAccount(t, s, OperatorPrincipal(), "bob", false)
	// Five failures lock the account whether or not it exists; the sixth is
	// refused before any derivation, for the right password too.
	for _, name := range []string{"bob", "ghost"} {
		for i := 0; i < accountThreshold; i++ {
			if _, err := login(s, name, "wrong password value", "198.51.100.7"); !errors.Is(err, domain.ErrUnauthenticated) {
				t.Fatalf("%s attempt %d: %v", name, i, err)
			}
		}
		var th *ThrottledError
		if _, err := login(s, name, testPassword, "203.0.113.9"); !errors.As(err, &th) || th.RetryAfterSeconds() < 1 || th.RetryAfterSeconds() > int(throttleBase.Seconds())+1 {
			t.Fatalf("%s locked: %v", name, err)
		}
		if _, err := login(s, name, testPassword, "203.0.113.9"); !errors.As(err, &th) {
			t.Fatalf("%s still locked: %v", name, err)
		}
	}
	if n := len(auditRows(t, s, AuditLoginThrottled)); n != 2 {
		t.Fatalf("one throttled row per lock, got %d", n)
	}
	// After the lock lapses one more attempt is allowed and a failure locks
	// again for twice as long.
	if _, err := s.pool.Exec(ctx, "UPDATE auth_throttle SET locked_until=clock_timestamp()-interval '1 second' WHERE scope='account'"); err != nil {
		t.Fatal(err)
	}
	if _, err := login(s, "bob", "wrong password value", "203.0.113.9"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal(err)
	}
	var th *ThrottledError
	if _, err := login(s, "bob", testPassword, "203.0.113.9"); !errors.As(err, &th) || th.RetryAfterSeconds() <= int(throttleBase.Seconds()) {
		t.Fatalf("backoff: %v", err)
	}
	// A success clears the account counter.
	if _, err := s.pool.Exec(ctx, "UPDATE auth_throttle SET locked_until=NULL WHERE scope='account'"); err != nil {
		t.Fatal(err)
	}
	if _, err := login(s, "bob", testPassword, "203.0.113.9"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM auth_throttle WHERE scope='account' AND failures>0").Scan(&left); err != nil || left != 1 {
		t.Fatalf("only ghost keeps failures: %d %v", left, err)
	}
	// One address spraying many names is locked at the client threshold.
	for i := 0; i < clientThreshold; i++ {
		_, _ = login(s, "spray"+strings.Repeat("x", i%3)+string(rune('a'+i)), "wrong password value", "2001:db8::/64")
	}
	if _, err := login(s, "bob", testPassword, "2001:db8::/64"); !errors.As(err, &th) {
		t.Fatalf("address lock: %v", err)
	}
	if _, err := login(s, "bob", testPassword, "192.0.2.50"); err != nil {
		t.Fatalf("other address: %v", err)
	}
}

// TC-149
func TestConcurrentGuessesAllCount(t *testing.T) {
	s := fastStore(t)
	newAccount(t, s, OperatorPrincipal(), "carol", false)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := login(s, "carol", "wrong password value", "")
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wrong, throttled := 0, 0
	for err := range results {
		var th *ThrottledError
		switch {
		case errors.Is(err, domain.ErrUnauthenticated):
			wrong++
		case errors.As(err, &th):
			throttled++
		default:
			t.Fatalf("unexpected %v", err)
		}
	}
	if wrong != accountThreshold || throttled != 12-accountThreshold {
		t.Fatalf("parallel guesses got %d derivations and %d refusals", wrong, throttled)
	}
}

// TC-150
func TestPasswordChangeResetAndRehash(t *testing.T) {
	s := fastStore(t)
	ctx := context.Background()
	dave := newAccount(t, s, OperatorPrincipal(), "dave", false)
	first, _ := login(s, "dave", testPassword, "")
	second, _ := login(s, "dave", testPassword, "")
	change := func(current, next string) (Session, error) {
		return s.ChangePassword(ctx, PasswordChange{SessionID: first.ID, UserID: dave.ID, Current: current, New: next})
	}
	if _, err := change("not the password", "a brand new passphrase"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("wrong current: %v", err)
	}
	if _, err := change(testPassword, "dave"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("policy: %v", err)
	}
	got, err := change(testPassword, "a brand new passphrase")
	if err != nil || got.ID != first.ID || got.MustChangePassword {
		t.Fatalf("change: %+v %v", got, err)
	}
	if _, err := s.SessionByTokenHash(ctx, domain.Digest([]byte("x"))); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	var revoked bool
	if err := s.pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1", second.ID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("other session survived: %v %v", revoked, err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1", first.ID).Scan(&revoked); err != nil || revoked {
		t.Fatalf("current session revoked: %v %v", revoked, err)
	}
	if _, err := login(s, "dave", testPassword, ""); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("old password: %v", err)
	}
	if len(auditRows(t, s, AuditPasswordChanged)) != 1 || len(auditRows(t, s, AuditPasswordChangeFailed)) != 1 {
		t.Fatal("password change audit")
	}

	// Reset: password cleared, sessions revoked, a login link issued; the
	// user must set a password (without a current one) before anything else.
	admin := newAccount(t, s, OperatorPrincipal(), "root", true)
	if _, err := s.ResetPassword(ctx, UserPrincipal(admin.ID), admin.ID, domain.Digest([]byte("t0")), time.Minute); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("self reset: %v", err)
	}
	if _, err := s.ResetPassword(ctx, UserPrincipal(dave.ID), admin.ID, domain.Digest([]byte("t0")), time.Minute); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-admin reset: %v", err)
	}
	token := domain.Digest([]byte("reset-token"))
	u, err := s.ResetPassword(ctx, UserPrincipal(admin.ID), dave.ID, token, time.Minute)
	if err != nil || u.HasPassword || !u.MustChangePassword {
		t.Fatalf("reset: %+v %v", u, err)
	}
	if err := s.pool.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM auth_sessions WHERE id=$1", first.ID).Scan(&revoked); err != nil || !revoked {
		t.Fatal("reset left a session")
	}
	if _, err := login(s, "dave", "a brand new passphrase", ""); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("password after reset: %v", err)
	}
	linked, err := s.ExchangeLaunchToken(ctx, token, domain.Digest([]byte("sess")), time.Hour)
	if err != nil || !linked.MustChangePassword || linked.HasPassword {
		t.Fatalf("link session: %+v %v", linked, err)
	}
	if _, err := s.ChangePassword(ctx, PasswordChange{SessionID: linked.ID, UserID: dave.ID, New: "third passphrase here"}); err != nil {
		t.Fatalf("first password after reset: %v", err)
	}
	if _, err := login(s, "dave", "third passphrase here", ""); err != nil {
		t.Fatal(err)
	}

	// A raised cost re-derives at the next login.
	s.SetPasswordIterations(account.MinIterations * 2)
	if _, err := login(s, "dave", "third passphrase here", ""); err != nil {
		t.Fatal(err)
	}
	var iter int
	if err := s.pool.QueryRow(ctx, "SELECT password_iterations FROM users WHERE id=$1", dave.ID).Scan(&iter); err != nil || iter != account.MinIterations*2 {
		t.Fatalf("rehash: %d %v", iter, err)
	}
	if _, err := login(s, "dave", "third passphrase here", ""); err != nil {
		t.Fatal("login after rehash")
	}
}

// TC-151
func TestLastAdministratorStays(t *testing.T) {
	s := fastStore(t)
	ctx := context.Background()
	// A database without administrators keeps working (plain disables).
	plain, _ := s.CreateUserNamed(ctx, "")
	if err := s.DisableUser(ctx, plain); err != nil {
		t.Fatal(err)
	}
	a := newAccount(t, s, OperatorPrincipal(), "admin-a", true)
	if _, err := s.DisableAccount(ctx, OperatorPrincipal(), a.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("disable last admin: %v", err)
	}
	if _, err := s.SetAdmin(ctx, UserPrincipal(a.ID), a.ID, false); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("demote last admin: %v", err)
	}
	b := newAccount(t, s, OperatorPrincipal(), "admin-b", false)
	if _, err := s.SetAdmin(ctx, UserPrincipal(b.ID), b.ID, true); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("self-promotion: %v", err)
	}
	if u, err := s.SetAdmin(ctx, UserPrincipal(a.ID), b.ID, true); err != nil || !u.IsAdmin {
		t.Fatalf("grant: %+v %v", u, err)
	}
	// Two administrators removing each other at once: exactly one wins.
	for round := 0; round < 5; round++ {
		var wg sync.WaitGroup
		errs := make([]error, 2)
		pairs := [][2]string{{a.ID, b.ID}, {b.ID, a.ID}}
		for i, p := range pairs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if round%2 == 0 {
					_, errs[i] = s.SetAdmin(ctx, UserPrincipal(p[0]), p[1], false)
				} else {
					_, errs[i] = s.DisableAccount(ctx, UserPrincipal(p[0]), p[1])
				}
			}()
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else if !errors.Is(err, domain.ErrForbidden) && !errors.Is(err, domain.ErrConflict) {
				t.Fatalf("round %d: %v", round, err)
			}
		}
		var admins int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE is_admin AND disabled_at IS NULL").Scan(&admins); err != nil {
			t.Fatal(err)
		}
		if ok != 1 || admins != 1 {
			t.Fatalf("round %d: %d succeeded, %d admins left (%v)", round, ok, admins, errs)
		}
		// Restore two administrators for the next round with fresh accounts.
		var remaining string
		if err := s.pool.QueryRow(ctx, "SELECT id::text FROM users WHERE is_admin AND disabled_at IS NULL").Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		next := newAccount(t, s, OperatorPrincipal(), "admin-r"+string(rune('0'+round)), true)
		a, b = User{ID: remaining}, next
	}
	if _, err := s.ListUsers(ctx, UserPrincipal(plain), "", 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("disabled non-admin lists users: %v", err)
	}
	page, err := s.ListUsers(ctx, UserPrincipal(a.ID), "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page %+v %v", page, err)
	}
	rest, err := s.ListUsers(ctx, UserPrincipal(a.ID), page.NextCursor, 200)
	if err != nil || len(rest.Items) == 0 || rest.Items[0].ID == page.Items[1].ID || rest.NextCursor != "" {
		t.Fatalf("next page %+v %v", rest, err)
	}
}

// TC-152
func TestMembersAndAuditReads(t *testing.T) {
	s := fastStore(t)
	ctx := WithRequestID(context.Background(), "req_0123456789abcdef01234567")
	owner, _ := s.CreateUserNamed(ctx, "owner")
	viewer := newAccount(t, s, OperatorPrincipal(), "viewer", false)
	manager := newAccount(t, s, OperatorPrincipal(), "manager", false)
	stranger, _ := s.CreateUserNamed(ctx, "stranger")
	admin := newAccount(t, s, OperatorPrincipal(), "admin", true)
	p, err := s.CreateProject(ctx, owner, "proj", json.RawMessage(`{"source":{"kind":"local"}}`))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.CreateProject(ctx, stranger, "other", json.RawMessage(`{"source":{"kind":"local"}}`))
	if _, err := s.PutMember(ctx, owner, p.ID, viewer.ID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if m, err := s.AddMemberByUsername(ctx, owner, p.ID, "MANAGER", domain.RoleManager); err != nil || m.UserID != manager.ID {
		t.Fatalf("by username: %+v %v", m, err)
	}
	for _, c := range []struct {
		actor, user string
		want        error
	}{
		{viewer.ID, stranger, domain.ErrForbidden}, {stranger, viewer.ID, domain.ErrNotFound},
		{admin.ID, stranger, domain.ErrNotFound}, {manager.ID, owner, domain.ErrConflict},
		{owner, domain.NewID(), domain.ErrNotFound},
	} {
		if _, err := s.PutMember(ctx, c.actor, p.ID, c.user, domain.RoleRunner); !errors.Is(err, c.want) {
			t.Fatalf("%v: %v", c, err)
		}
	}
	if _, err := s.AddMemberByUsername(ctx, owner, p.ID, "nobody", domain.RoleViewer); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown username: %v", err)
	}
	members, err := s.ListMembers(ctx, manager.ID, p.ID)
	if err != nil || len(members) != 3 || members[0].Role != RoleOwner || members[1].Role != "manager" || members[2].Role != "viewer" {
		t.Fatalf("members %+v %v", members, err)
	}
	if _, err := s.ListMembers(ctx, viewer.ID, p.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("viewer lists members: %v", err)
	}
	if err := s.RemoveMember(ctx, manager.ID, p.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, manager.ID, p.ID, viewer.ID); err != nil {
		t.Fatal(err)
	}
	if n := len(auditRows(t, s, AuditMemberRemoved)); n != 1 {
		t.Fatalf("removal rows %d", n)
	}
	sum, err := s.GetProjectSummary(ctx, manager.ID, p.ID)
	if err != nil || sum.Role != "manager" {
		t.Fatalf("role %q %v", sum.Role, err)
	}

	// Audit reads: the administrator sees every row; a manager sees only the
	// project's rows; the request ID travels from the context.
	all, err := s.ListAudit(ctx, admin.ID, AuditFilter{Limit: 200})
	if err != nil || len(all.Items) < 8 {
		t.Fatalf("audit %d %v", len(all.Items), err)
	}
	for i := 1; i < len(all.Items); i++ {
		if all.Items[i].ID >= all.Items[i-1].ID {
			t.Fatal("audit not newest first")
		}
	}
	if _, err := s.ListAudit(ctx, manager.ID, AuditFilter{Limit: 10}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("manager global audit: %v", err)
	}
	proj, err := s.ListProjectAudit(ctx, manager.ID, p.ID, AuditFilter{Limit: 200})
	if err != nil || len(proj.Items) != 4 { // registered, two member_set, member_removed
		t.Fatalf("project audit %d %v", len(proj.Items), err)
	}
	for _, r := range proj.Items {
		if r.ProjectID == nil || *r.ProjectID != p.ID || r.RequestID == nil || *r.RequestID != "req_0123456789abcdef01234567" {
			t.Fatalf("row %+v", r)
		}
	}
	if _, err := s.ListProjectAudit(ctx, admin.ID, p.ID, AuditFilter{Limit: 10}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("admin without membership reads project audit: %v", err)
	}
	if _, err := s.ListProjectAudit(ctx, manager.ID, p.ID, AuditFilter{Limit: 10, ProjectID: other.ID}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("cross-project filter: %v", err)
	}
	first, err := s.ListAudit(ctx, admin.ID, AuditFilter{Limit: 1, Action: AuditMemberSet})
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("filtered page %+v %v", first, err)
	}
	second, err := s.ListAudit(ctx, admin.ID, AuditFilter{Limit: 1, Action: AuditMemberSet, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].ID >= first.Items[0].ID {
		t.Fatalf("second page %+v %v", second, err)
	}
	since := time.Now().Add(time.Hour)
	if future, err := s.ListAudit(ctx, admin.ID, AuditFilter{Limit: 10, Since: &since}); err != nil || len(future.Items) != 0 {
		t.Fatalf("since filter %v", err)
	}
	for _, bad := range []AuditFilter{{Limit: 0}, {Limit: 10, Action: "user.deleted"}, {Limit: 10, Cursor: "01"}, {Limit: 10, ActorUserID: "x"}} {
		if _, err := s.ListAudit(ctx, admin.ID, bad); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

// TC-152
func TestAuditDetailAllowlist(t *testing.T) {
	for _, bad := range []map[string]any{
		{"password": "x"}, {"token": "x"}, {"username": strings.Repeat("a", 201)}, {"user_id": map[string]any{}}, {"role": new(string)},
	} {
		if checkAuditDetail(bad) == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
	if err := checkAuditDetail(map[string]any{"method": "password", "is_admin": true, "revision": int64(3), "fields": []string{"cron"}, "previous_role": nil}); err != nil {
		t.Fatal(err)
	}
	for k := range auditDetailKeys {
		if k == "must_change_password" { // a boolean flag, not a password
			continue
		}
		for _, s := range []string{"password", "token", "secret", "path", "prompt", "reason", "key", "hash", "salt"} {
			if strings.Contains(k, s) {
				t.Fatalf("detail key %q names sensitive content", k)
			}
		}
	}
}
