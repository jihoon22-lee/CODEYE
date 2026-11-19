package httpapi

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// TC-156: an administrator's password reset is attributed and shown. A user
// without a username cannot be reset (409; links come from the operator
// CLI). The reset-link session is gated to the password change on its own,
// even if the account flag were cleared. The sign-in row names the issuing
// administrator, and so does every later row of that session. The user's
// session view shows who reset the password and when on every sign-in for
// 30 days; there is no way to dismiss it.
func TestAdminResetIsAttributedAndShown(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const pw = "administrator passphrase"
	adminID := e.account("root", pw, true)
	admin := e.loginPassword("root", pw)
	linkOnly, err := e.store.CreateUserNamed(ctx, "link only")
	if err != nil {
		t.Fatal(err)
	}
	r := e.do("POST", "/api/v1/admin/users/"+linkOnly+"/password-reset", nil, admin.o()).want(t, 409, "CONFLICT")
	if !strings.Contains(errMessage(r), "no username") {
		t.Fatalf("no-username reset message %q", errMessage(r))
	}
	r = e.do("POST", "/api/v1/admin/users/"+adminID+"/password-reset", nil, admin.o()).want(t, 409, "CONFLICT")
	if !strings.Contains(errMessage(r), "/session/password") {
		t.Fatalf("self reset message %q", errMessage(r))
	}

	victimID := e.account("victim", "victim original passphrase", false)
	before := e.loginPassword("victim", "victim original passphrase")
	if s := e.do("GET", "/api/v1/session", nil, before.o()).want(t, 200, ""); s.body["password_reset"] != nil {
		t.Fatalf("notice before any reset: %s", s.raw)
	}
	r = e.do("POST", "/api/v1/admin/users/"+victimID+"/password-reset", nil, admin.o()).want(t, 200, "")
	linked, s := e.exchangeLink(r.body["login_link"].(map[string]any)["url"].(string))
	if s.body["must_change_password"] != true {
		t.Fatalf("reset-link session %s", s.raw)
	}
	// The session's own gate holds even if the account flag is cleared.
	if _, err := e.pool.Exec(ctx, "UPDATE users SET must_change_password=false WHERE id=$1", victimID); err != nil {
		t.Fatal(err)
	}
	e.do("GET", "/api/v1/projects", nil, linked.o()).want(t, 403, "PASSWORD_CHANGE_REQUIRED")
	e.do("POST", "/api/v1/session/password", map[string]string{"new_password": "taken over passphrase"}, linked.o()).want(t, 200, "")
	e.do("GET", "/api/v1/projects", nil, linked.o()).want(t, 200, "")
	e.do("DELETE", "/api/v1/session", nil, linked.o()).want(t, 204, "")

	rows, err := e.pool.Query(ctx, "SELECT action,detail FROM audit_events WHERE actor_user_id=$1 AND id>(SELECT max(id) FROM audit_events WHERE action=$2) ORDER BY id",
		victimID, postgres.AuditPasswordReset)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for rows.Next() {
		var action string
		var detail map[string]any
		if err := rows.Scan(&action, &detail); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, action)
		if detail["link_issued_by"] != adminID {
			t.Errorf("%s row does not name the administrator: %v", action, detail)
		}
		if action == postgres.AuditLoginSucceeded && detail["link_purpose"] != "password_reset" {
			t.Errorf("sign-in purpose %v", detail)
		}
	}
	rows.Close()
	if !slices.Equal(seen, []string{postgres.AuditLoginSucceeded, postgres.AuditPasswordChanged, postgres.AuditLogout}) {
		t.Fatalf("rows of the reset-link session %v", seen)
	}
	// An operator CLI link is attributed to the operator, not an administrator.
	op := e.login(e.account("by-operator", "", false))
	var issuer string
	if err := e.pool.QueryRow(ctx, "SELECT detail->>'link_issued_by' FROM audit_events WHERE action=$1 AND actor_user_id=$2",
		postgres.AuditLoginSucceeded, op.id).Scan(&issuer); err != nil || issuer != postgres.ActorOperator {
		t.Fatalf("operator link issuer %q %v", issuer, err)
	}

	// The owner sees who reset the password and when, on every sign-in, for
	// 30 days; nothing can dismiss it.
	for i := 0; i < 3; i++ {
		owner := e.loginPassword("victim", "taken over passphrase")
		s = e.do("GET", "/api/v1/session", nil, owner.o()).want(t, 200, "")
		n, _ := s.body["password_reset"].(map[string]any)
		if n == nil || n["reset_by_user_id"] != adminID || n["reset_by_username"] != "root" || n["reset_at"] == nil || len(n) != 3 {
			t.Fatalf("notice on sign-in %d: %s", i, s.raw)
		}
	}
	owner := e.loginPassword("victim", "taken over passphrase")
	if r := e.do("POST", "/api/v1/session/password-reset/acknowledge", nil, owner.o()); r.status != 404 {
		t.Fatalf("a dismiss route exists: %d", r.status)
	}
	if _, err := e.pool.Exec(ctx, "UPDATE users SET password_reset_at=password_reset_at-interval '31 days' WHERE id=$1", victimID); err != nil {
		t.Fatal(err)
	}
	if s := e.do("GET", "/api/v1/session", nil, owner.o()).want(t, 200, ""); s.body["password_reset"] != nil {
		t.Fatalf("notice after 30 days: %s", s.raw)
	}
}
