package httpapi

import (
	"context"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const registerPath = "/api/v1/account/register"
const registrationPassword = "registration integration password"

func registerBody(email string) map[string]string {
	return map[string]string{"email": email, "password": registrationPassword}
}

func signupUser(t *testing.T, r resp, email string) user {
	t.Helper()
	if r.body["email"] != email || r.body["username"] != nil || r.body["has_password"] != true || r.body["is_admin"] != false ||
		r.body["must_change_password"] != false || sessionCookieOf(r) == "" || strings.Contains(string(r.raw), registrationPassword) {
		t.Fatalf("unexpected session for new account: %s", r.raw)
	}
	return user{id: r.body["user_id"].(string), cookie: sessionCookieOf(r), csrf: r.body["csrf_token"].(string)}
}

// TC-266: Canonical email is shared by registration, password login and
// account changes. Concurrent duplicates leave a single user and cookie.
func TestEmailRegistrationAndLogin(t *testing.T) {
	e := newEnv(t)
	const email = "dev+review@example.test"
	e.do("POST", registerPath, registerBody(email), opts{origin: "https://other.example"}).want(t, 403, "ORIGIN_REJECTED")
	e.do("POST", registerPath, map[string]string{"email": email}, opts{}).want(t, 400, "INVALID_REQUEST")
	e.do("POST", registerPath, map[string]string{"email": email, "password": registrationPassword, "is_admin": "true"}, opts{}).want(t, 400, "INVALID_REQUEST")
	e.do("POST", registerPath, registerBody("User@invalid"), opts{}).want(t, 422, "VALIDATION_FAILED")
	e.do("POST", registerPath, map[string]string{"email": email, "password": email}, opts{}).want(t, 422, "VALIDATION_FAILED")
	start, results := make(chan struct{}), make(chan resp, 2)
	for _, variant := range []string{email, "\t Dev+Review@EXAMPLE.Test \u00a0"} {
		go func() {
			<-start
			results <- e.do("POST", registerPath, registerBody(variant), opts{})
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.status == 409 {
		first, second = second, first
	}
	u := signupUser(t, first.want(t, 201, ""), email)
	second.want(t, 409, "EMAIL_TAKEN")
	if second.header.Get("Set-Cookie") != "" {
		t.Fatal("duplicate registration issued a session cookie")
	}
	var users, sessions int
	if err := e.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM users), (SELECT count(*) FROM auth_sessions)").Scan(&users, &sessions); err != nil || users != 1 || sessions != 1 {
		t.Fatalf("registration persisted %d users and %d sessions: %v", users, sessions, err)
	}
	e.do("GET", "/api/v1/admin/users", nil, opts{cookie: u.cookie}).want(t, 403, "FORBIDDEN")
	e.do("DELETE", "/api/v1/session", nil, opts{cookie: u.cookie, csrf: u.csrf}).want(t, 204, "")
	e.do("POST", "/api/v1/session", map[string]string{"email": email, "username": "legacy", "password": registrationPassword}, opts{}).want(t, 400, "INVALID_REQUEST")
	wrong := e.do("POST", "/api/v1/session", map[string]string{"email": email, "password": "wrong"}, opts{}).want(t, 401, "UNAUTHENTICATED")
	unknown := e.do("POST", "/api/v1/session", registerBody("unknown@example.test"), opts{}).want(t, 401, "UNAUTHENTICATED")
	if errMessage(wrong) != errMessage(unknown) {
		t.Fatal("password login error reveals whether the email exists")
	}
	loggedIn := signupUser(t, e.do("POST", "/api/v1/session", registerBody(" Dev+Review@EXAMPLE.Test "), opts{}).want(t, 201, ""), email)
	if loggedIn.id != u.id {
		t.Fatal("email login changed account ID")
	}
	const next = "a different registration password"
	e.do("POST", "/api/v1/session/password", map[string]string{"current_password": registrationPassword, "new_password": next},
		opts{cookie: loggedIn.cookie, csrf: loggedIn.csrf}).want(t, 200, "")
	e.do("POST", "/api/v1/session", map[string]string{"email": email, "password": next}, opts{}).want(t, 201, "")
	if strings.Contains(e.logs.String(), registrationPassword) || strings.Contains(e.logs.String(), next) {
		t.Fatal("password logged")
	}
}

func TestSignedUpUsersOwnIndependentProjects(t *testing.T) {
	e := newEnv(t)
	old := e.driveAttempt("succeeded")
	e.publishRun(old)
	const firstEmail, secondEmail = "first@example.test", "second@example.test"
	first := signupUser(t, e.do("POST", registerPath, registerBody(firstEmail), opts{}).want(t, 201, ""), firstEmail)
	second := signupUser(t, e.do("POST", registerPath, registerBody(secondEmail), opts{}).want(t, 201, ""), secondEmail)
	access := func(u user) opts { return opts{cookie: u.cookie, csrf: u.csrf} }
	e.do("GET", "/api/v1/runs/"+old.runID+"/report", nil, access(second)).want(t, 404, "NOT_FOUND")
	e.do("GET", "/api/v1/runs/"+old.runID+"/report", nil, access(old.f.user)).want(t, 200, "")
	provider, _, err := e.store.EnsureProvider(context.Background(), "github.com")
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"open_existing": true, "source": domain.Source{Kind: domain.SourceGit, ProviderID: provider.ID,
		RepositoryURL: "https://github.com/codeye-example/privacy.git", Branch: "main"}}
	firstProject := e.do("POST", "/api/v1/projects", body, access(first)).want(t, 201, "").body["id"].(string)
	firstPath := "/api/v1/projects/" + firstProject
	firstRun := e.createRun(first, firstProject)
	e.do("GET", firstPath, nil, access(second)).want(t, 404, "NOT_FOUND")
	e.do("GET", "/api/v1/runs/"+firstRun, nil, access(second)).want(t, 404, "NOT_FOUND")
	if items := e.do("GET", "/api/v1/projects", nil, access(second)).want(t, 200, "").body["items"].([]any); len(items) != 0 {
		t.Fatalf("second user's project list exposes first user's project: %v", items)
	}
	e.do("POST", firstPath+"/members", map[string]string{"email": " SECOND@example.test ", "role": "viewer"}, access(first)).want(t, 200, "")
	e.do("GET", firstPath, nil, access(second)).want(t, 200, "")
	secondProject := e.do("POST", "/api/v1/projects", body, access(second)).want(t, 201, "").body["id"].(string)
	if secondProject == firstProject || e.createRun(second, secondProject) == firstRun {
		t.Fatal("accounts shared a project or run for the same repository URL")
	}
	e.do("GET", "/api/v1/projects/"+secondProject, nil, access(first)).want(t, 404, "NOT_FOUND")
	if id := e.do("POST", "/api/v1/projects", body, access(first)).want(t, 200, "").body["id"]; id != firstProject {
		t.Fatal("the owner could not reopen the original project")
	}
}
