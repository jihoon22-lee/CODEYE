package httpapi

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// account creates a named user through the operator path; password "" means
// none (login links only).
func (e *env) account(username, password string, admin bool) string {
	e.t.Helper()
	in := postgres.NewAccount{Username: username, IsAdmin: admin}
	if password != "" {
		h, err := account.Derive(password, account.MinIterations)
		if err != nil {
			e.t.Fatal(err)
		}
		in.Password = &h
	}
	u, err := e.store.CreateAccount(context.Background(), postgres.OperatorPrincipal(), in)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

// newAdminAccount is an enabled administrator without a password.
func (e *env) newAdminAccount(username string) string { return e.account(username, "", true) }

func sessionCookieOf(r resp) string {
	for _, c := range r.header.Values("Set-Cookie") {
		if strings.HasPrefix(c, sessionCookie+"=") {
			return strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], sessionCookie+"=")
		}
	}
	return ""
}

func (e *env) passwordLogin(username, password string) resp {
	e.t.Helper()
	return e.do("POST", "/api/v1/session", map[string]string{"username": username, "password": password}, opts{})
}

// loginPassword signs in with a password and returns the session.
func (e *env) loginPassword(username, password string) user {
	e.t.Helper()
	r := e.passwordLogin(username, password).want(e.t, 201, "")
	return user{id: r.body["user_id"].(string), cookie: sessionCookieOf(r), csrf: r.body["csrf_token"].(string)}
}

// exchangeLink signs in with the token of a login link URL.
func (e *env) exchangeLink(url string) (user, resp) {
	e.t.Helper()
	_, token, ok := strings.Cut(url, "#launch=")
	if !ok {
		e.t.Fatalf("login link %s", url)
	}
	r := e.do("POST", "/api/v1/session", map[string]string{"launch_token": token}, opts{}).want(e.t, 201, "")
	return user{id: r.body["user_id"].(string), cookie: sessionCookieOf(r), csrf: r.body["csrf_token"].(string)}, r
}

func errMessage(r resp) string {
	m, _ := r.body["error"].(map[string]any)
	s, _ := m["message"].(string)
	return s
}

// TC-141: password login answers 201 with the account fields; an unknown user
// and a wrong password give the same 401; repeated failures give 429 with
// Retry-After even for the right password; mixed bodies are 400.
func TestPasswordLoginOverHTTP(t *testing.T) {
	e := newEnv(t)
	const pw = "correct horse battery"
	alice := e.account("alice", pw, false)
	r := e.passwordLogin("Alice", pw).want(t, 201, "")
	if r.body["user_id"] != alice || r.body["username"] != "alice" || r.body["is_admin"] != false || r.body["must_change_password"] != false ||
		r.body["has_password"] != true || sessionCookieOf(r) == "" {
		t.Fatalf("session %s", r.raw)
	}
	wrong := e.passwordLogin("alice", "not the password").want(t, 401, "UNAUTHENTICATED")
	unknown := e.passwordLogin("nobody", pw).want(t, 401, "UNAUTHENTICATED")
	empty := e.passwordLogin("", pw).want(t, 401, "UNAUTHENTICATED")
	if errMessage(wrong) != errMessage(unknown) || errMessage(wrong) != errMessage(empty) {
		t.Fatalf("messages differ: %q %q %q", errMessage(wrong), errMessage(unknown), errMessage(empty))
	}
	for _, body := range []any{
		map[string]string{"launch_token": NewToken(), "username": "alice", "password": pw},
		map[string]string{"username": "alice"},
		map[string]string{"password": pw},
		map[string]any{},
	} {
		e.do("POST", "/api/v1/session", body, opts{}).want(t, 400, "INVALID_REQUEST")
	}
	// Origin is checked for login too.
	e.do("POST", "/api/v1/session", map[string]string{"username": "alice", "password": pw}, opts{origin: "http://evil.example"}).want(t, 403, "ORIGIN_REJECTED")

	var throttled resp
	for i := 0; i < 6; i++ {
		throttled = e.passwordLogin("alice", "still not the password")
		if throttled.status == 429 {
			break
		}
		throttled.want(t, 401, "UNAUTHENTICATED")
	}
	throttled.want(t, 429, "RATE_LIMITED")
	secs, err := strconv.Atoi(throttled.header.Get("Retry-After"))
	details, _ := throttled.body["error"].(map[string]any)["details"].(map[string]any)
	if err != nil || secs < 1 || details["retry_after_seconds"] != float64(secs) {
		t.Fatalf("Retry-After %q details %v", throttled.header.Get("Retry-After"), details)
	}
	e.passwordLogin("alice", pw).want(t, 429, "RATE_LIMITED")
	e.passwordLogin("ALICE", pw).want(t, 429, "RATE_LIMITED")
	// An unknown name is throttled the same way (no existence signal).
	for i := 0; i < 6; i++ {
		if e.passwordLogin("ghost", "whatever password").status == 429 {
			break
		}
	}
	e.passwordLogin("ghost", "whatever password").want(t, 429, "RATE_LIMITED")
	if strings.Contains(e.logs.String(), pw) || strings.Contains(e.logs.String(), "not the password") {
		t.Fatal("password in logs")
	}
}

// TC-142: an account created by an administrator signs in once with the
// login link and can only change the password or log out; a password change
// revokes the other sessions; reset and disable end every session.
func TestPasswordChangeResetAndPendingGate(t *testing.T) {
	e := newEnv(t)
	const adminPW, carolPW, carolPW2 = "administrator passphrase", "carol first passphrase", "carol second passphrase"
	e.account("root", adminPW, true)
	admin := e.loginPassword("root", adminPW)
	r := e.do("POST", "/api/v1/admin/users", map[string]any{"username": "carol", "display_name": "Carol"}, admin.o()).want(t, 201, "")
	carolID := r.body["user"].(map[string]any)["id"].(string)
	link := r.body["login_link"].(map[string]any)["url"].(string)
	if r.body["user"].(map[string]any)["must_change_password"] != true || !strings.HasPrefix(link, "http://"+e.host+"/#launch=") {
		t.Fatalf("create %s", r.raw)
	}
	e.do("POST", "/api/v1/admin/users", map[string]any{"username": "CAROL"}, admin.o()).want(t, 409, "CONFLICT")
	carol, s := e.exchangeLink(link)
	if s.body["must_change_password"] != true || s.body["has_password"] != false || s.body["display_name"] != "Carol" {
		t.Fatalf("pending session %s", s.raw)
	}
	e.do("POST", "/api/v1/session", map[string]string{"launch_token": strings.SplitN(link, "#launch=", 2)[1]}, opts{}).want(t, 401, "UNAUTHENTICATED")
	e.do("GET", "/api/v1/projects", nil, carol.o()).want(t, 403, "PASSWORD_CHANGE_REQUIRED")
	e.do("POST", "/api/v1/projects", map[string]any{}, carol.o()).want(t, 403, "PASSWORD_CHANGE_REQUIRED")
	e.do("GET", "/api/v1/session", nil, carol.o()).want(t, 200, "")
	e.do("POST", "/api/v1/session/password", map[string]string{"new_password": "short"}, carol.o()).want(t, 422, "VALIDATION_FAILED")
	e.do("POST", "/api/v1/session/password", map[string]string{"new_password": "carol"}, carol.o()).want(t, 422, "VALIDATION_FAILED")
	r = e.do("POST", "/api/v1/session/password", map[string]string{"new_password": carolPW}, carol.o()).want(t, 200, "")
	if r.body["must_change_password"] != false || r.body["has_password"] != true {
		t.Fatalf("after first password %s", r.raw)
	}
	e.do("GET", "/api/v1/projects", nil, carol.o()).want(t, 200, "")

	// A password change needs the current password and ends the other sessions.
	other := e.loginPassword("carol", carolPW)
	e.do("POST", "/api/v1/session/password", map[string]string{"new_password": carolPW2}, carol.o()).want(t, 400, "INVALID_REQUEST")
	e.do("POST", "/api/v1/session/password", map[string]string{"current_password": "wrong passphrase", "new_password": carolPW2}, carol.o()).want(t, 403, "PASSWORD_INCORRECT")
	// The policy is checked first: a weak new password says nothing about the current one.
	e.do("POST", "/api/v1/session/password", map[string]string{"current_password": "wrong passphrase", "new_password": "short"}, carol.o()).want(t, 422, "VALIDATION_FAILED")
	e.do("POST", "/api/v1/session/password", map[string]string{"current_password": carolPW, "new_password": carolPW2}, opts{cookie: carol.cookie}).want(t, 403, "CSRF_REJECTED")
	e.do("POST", "/api/v1/session/password", map[string]string{"current_password": carolPW, "new_password": carolPW2}, carol.o()).want(t, 200, "")
	e.do("GET", "/api/v1/session", nil, other.o()).want(t, 401, "UNAUTHENTICATED")
	e.do("GET", "/api/v1/session", nil, carol.o()).want(t, 200, "")
	e.passwordLogin("carol", carolPW).want(t, 401, "UNAUTHENTICATED")
	second := e.loginPassword("carol", carolPW2)

	// Reset: password cleared, every session gone, a new single-use link.
	e.do("POST", "/api/v1/admin/users/"+admin.id+"/password-reset", nil, admin.o()).want(t, 409, "CONFLICT")
	e.do("POST", "/api/v1/admin/users/"+carolID+"/password-reset", nil, carol.o()).want(t, 403, "FORBIDDEN")
	r = e.do("POST", "/api/v1/admin/users/"+carolID+"/password-reset", nil, admin.o()).want(t, 200, "")
	for _, u := range []user{carol, second} {
		e.do("GET", "/api/v1/session", nil, u.o()).want(t, 401, "UNAUTHENTICATED")
	}
	e.passwordLogin("carol", carolPW2).want(t, 401, "UNAUTHENTICATED")
	carol, s = e.exchangeLink(r.body["login_link"].(map[string]any)["url"].(string))
	if s.body["must_change_password"] != true || s.body["has_password"] != false {
		t.Fatalf("after reset %s", s.raw)
	}

	// Disable ends the session; the last administrator stays.
	e.do("POST", "/api/v1/admin/users/"+carolID+"/disable", nil, admin.o()).want(t, 200, "")
	e.do("GET", "/api/v1/session", nil, carol.o()).want(t, 401, "UNAUTHENTICATED")
	e.do("POST", "/api/v1/admin/users/"+admin.id+"/disable", nil, admin.o()).want(t, 409, "CONFLICT")
	e.do("PUT", "/api/v1/admin/users/"+admin.id+"/admin", map[string]bool{"is_admin": false}, admin.o()).want(t, 409, "CONFLICT")

	var leaked int
	if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE detail::text ILIKE '%passphrase%'").Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("password in audit detail: %d %v", leaked, err)
	}
	for _, pw := range []string{adminPW, carolPW, carolPW2} {
		if strings.Contains(e.logs.String(), pw) {
			t.Fatal("password in logs")
		}
	}
	var actions []string
	rows, err := e.pool.Query(context.Background(), "SELECT DISTINCT action FROM audit_events WHERE target_id=$1 OR actor_user_id::text=$1", carolID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		_ = rows.Scan(&a)
		actions = append(actions, a)
	}
	rows.Close()
	joined := strings.Join(actions, " ")
	for _, want := range []string{postgres.AuditUserCreated, postgres.AuditPasswordChanged, postgres.AuditPasswordChangeFailed,
		postgres.AuditPasswordReset, postgres.AuditUserDisabled, postgres.AuditLoginSucceeded, postgres.AuditLoginFailed} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %s audit row for the user (have %s)", want, joined)
		}
	}
}
