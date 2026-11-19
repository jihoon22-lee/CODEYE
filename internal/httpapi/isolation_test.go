package httpapi

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// The isolation table (TC-140): every route of the router, every actor. The
// route list comes from Server.routes, so a new route without a case fails
// the test. For each request the test checks the status the actor's role
// allows, that a refused request records nothing, and that an accepted
// request records an audit row of the route's declared actions with the
// request's ID.

// access is what a route requires of the caller.
type access int

const (
	// anyone: no session (POST /session); the request uses no actor cookie.
	anyone access = iota
	// signedIn: any session; the result does not depend on project roles.
	signedIn
	// project roles (owner > manager > runner > viewer).
	viewerRole
	runnerRole
	managerRole
	// rootHolder: only users granted the source root (the owner here).
	rootHolder
	// adminRole: an enabled administrator.
	adminRole
)

// isoWorld is the fixture: project P owned by owner with viewer, runner and
// manager members; a published run; an administrator and a stranger who are
// not members.
type isoWorld struct {
	e        *env
	f        nodeFixture
	run      attemptRun
	actors   map[string]user
	rank     map[string]int // project role rank; 0 = not a member
	findings []map[string]any
	seq      int
	// accepting is set while building a request the actor may make (a
	// refused request does not use up an unreviewed finding).
	accepting bool
}

func (w *isoWorld) name(prefix string) string {
	w.seq++
	return prefix + "-" + strings.ReplaceAll(domain.NewID()[:8], "-", "") + "-" + itoa(w.seq)
}

func (w *isoWorld) namedUser() string { return w.e.account(w.name("u"), "", false) }

func (w *isoWorld) member(role domain.Role) string {
	id := w.namedUser()
	if err := w.e.store.SetMember(context.Background(), w.f.user.id, w.f.projectID, id, role); err != nil {
		w.e.t.Fatal(err)
	}
	return id
}

func (w *isoWorld) schedule() string {
	r := w.e.do("POST", "/api/v1/projects/"+w.f.projectID+"/schedules", scheduleBody(), opts{cookie: w.f.user.cookie, csrf: w.f.user.csrf, idem: domain.NewID()}).want(w.e.t, 201, "")
	return r.body["id"].(string)
}

func scheduleBody() map[string]any {
	return map[string]any{"cron": "0 9 * * 1-5", "timezone": "UTC", "run": map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}}
}

// isoCase builds one request. ids fills the {id} segments in route order.
type isoCase struct {
	need   access
	ok     int
	ids    func(w *isoWorld) []string
	query  string
	body   func(w *isoWorld) any
	opts   func(w *isoWorld, o opts) opts
	stream bool
	// disposable runs the request as a fresh signed-in user (session routes
	// that end or change the caller's own session).
	disposable func(w *isoWorld) user
}

func isoProject(w *isoWorld) []string { return []string{w.f.projectID} }
func isoRun(w *isoWorld) []string     { return []string{w.run.runID} }
func idem(_ *isoWorld, o opts) opts {
	o.idem = domain.NewID()
	return o
}

func isoCases() map[string]isoCase {
	freshNamed := func(w *isoWorld) user { return w.e.login(w.namedUser()) }
	return map[string]isoCase{
		"health": {need: anyone, ok: 200},
		"ready":  {need: anyone, ok: 200},
		"session.create": {need: anyone, ok: 201, body: func(w *isoWorld) any {
			token := NewToken()
			if err := w.e.store.IssueLaunchToken(context.Background(), w.namedUser(), TokenHash(token), time.Minute); err != nil {
				w.e.t.Fatal(err)
			}
			return map[string]string{"launch_token": token}
		}},
		"session.get":       {need: signedIn, ok: 200},
		"session.delete":    {need: signedIn, ok: 204, disposable: freshNamed},
		"session.password":  {need: signedIn, ok: 200, disposable: freshNamed, body: func(*isoWorld) any { return map[string]string{"new_password": "a long enough passphrase"} }},
		"source_roots.list": {need: signedIn, ok: 200},
		"providers.list":    {need: signedIn, ok: 200},
		"projects.list":     {need: signedIn, ok: 200},
		"projects.create": {need: rootHolder, ok: 201, body: func(w *isoWorld) any {
			return map[string]any{"name": w.name("p"), "source": map[string]any{"kind": "local", "source_root_id": w.f.root.ID, "relative_path": "."}}
		}},
		"projects.get":   {need: viewerRole, ok: 200, ids: isoProject},
		"members.list":   {need: managerRole, ok: 200, ids: isoProject},
		"members.put":    {need: managerRole, ok: 200, ids: func(w *isoWorld) []string { return []string{w.f.projectID, w.namedUser()} }, body: func(*isoWorld) any { return map[string]string{"role": "viewer"} }},
		"members.delete": {need: managerRole, ok: 204, ids: func(w *isoWorld) []string { return []string{w.f.projectID, w.member(domain.RoleViewer)} }},
		"projects.audit": {need: managerRole, ok: 200, ids: isoProject},
		"runs.create": {need: runnerRole, ok: 202, ids: isoProject, opts: idem, body: func(*isoWorld) any {
			return map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}}
		}},
		"schedules.list":   {need: viewerRole, ok: 200, ids: isoProject},
		"schedules.create": {need: managerRole, ok: 201, ids: isoProject, opts: idem, body: func(*isoWorld) any { return scheduleBody() }},
		"schedules.update": {need: managerRole, ok: 200, ids: func(w *isoWorld) []string { return []string{w.f.projectID, w.schedule()} },
			opts: func(_ *isoWorld, o opts) opts { o.ifMatch = `"1"`; return o }, body: func(*isoWorld) any { return map[string]any{"enabled": false} }},
		"schedules.delete": {need: managerRole, ok: 204, ids: func(w *isoWorld) []string { return []string{w.f.projectID, w.schedule()} },
			opts: func(_ *isoWorld, o opts) opts { o.ifMatch = `"1"`; return o }},
		"runs.get":             {need: viewerRole, ok: 200, ids: isoRun},
		"runs.events":          {need: viewerRole, ok: 200, ids: isoRun, stream: true},
		"runs.cancel":          {need: runnerRole, ok: 202, ids: func(w *isoWorld) []string { return []string{w.e.createRun(w.f.user, w.f.projectID)} }},
		"runs.report":          {need: viewerRole, ok: 200, ids: isoRun, query: "?format=json&include_code=true"},
		"runs.reports":         {need: viewerRole, ok: 200, ids: isoRun},
		"runs.comparison":      {need: viewerRole, ok: 200, ids: isoRun},
		"finding_reviews.list": {need: viewerRole, ok: 200, ids: isoProject},
		"finding_reviews.create": {need: runnerRole, ok: 201, ids: isoProject,
			opts: func(_ *isoWorld, o opts) opts { o.idem, o.ifNoneMatch = domain.NewID(), "*"; return o },
			body: func(w *isoWorld) any {
				if len(w.findings) == 0 {
					w.e.t.Fatal("no unreviewed finding left")
				}
				f := w.findings[0]
				if w.accepting {
					w.findings = w.findings[1:]
				}
				return map[string]any{"fingerprint": f["fingerprint"], "disposition": "false_positive", "reason": "격리 시험", "run_id": w.run.runID, "revision": 1, "finding_id": f["id"]}
			}},
		"admin.users.list":      {need: adminRole, ok: 200},
		"admin.users.create":    {need: adminRole, ok: 201, body: func(w *isoWorld) any { return map[string]string{"username": w.name("a")} }},
		"admin.users.disable":   {need: adminRole, ok: 200, ids: func(w *isoWorld) []string { return []string{w.namedUser()} }},
		"admin.users.set_admin": {need: adminRole, ok: 200, ids: func(w *isoWorld) []string { return []string{w.namedUser()} }, body: func(*isoWorld) any { return map[string]bool{"is_admin": true} }},
		"admin.users.reset_password": {need: adminRole, ok: 200, ids: func(w *isoWorld) []string {
			return []string{w.e.account(w.name("r"), "a long enough passphrase", false)}
		}},
		"admin.source_roots.list":  {need: adminRole, ok: 200},
		"admin.source_roots.grant": {need: adminRole, ok: 204, ids: func(w *isoWorld) []string { return []string{w.f.root.ID, w.namedUser()} }},
		"admin.source_roots.revoke": {need: adminRole, ok: 204, ids: func(w *isoWorld) []string {
			u := w.namedUser()
			if err := w.e.store.GrantSourceRoot(context.Background(), w.f.root.ID, u); err != nil {
				w.e.t.Fatal(err)
			}
			return []string{w.f.root.ID, u}
		}},
		"admin.providers.list": {need: adminRole, ok: 200},
		"audit.list":           {need: adminRole, ok: 200},
	}
}

// expected is the status (and error code) the actor gets.
func (w *isoWorld) expected(c isoCase, actor string) (int, string) {
	switch c.need {
	case anyone, signedIn:
		return c.ok, ""
	case rootHolder:
		if actor == "owner" {
			return c.ok, ""
		}
		return 404, "NOT_FOUND"
	case adminRole:
		if actor == "admin" {
			return c.ok, ""
		}
		return 403, "FORBIDDEN"
	}
	need := map[access]int{viewerRole: 1, runnerRole: 2, managerRole: 3}[c.need]
	switch r := w.rank[actor]; {
	case r == 0:
		return 404, "NOT_FOUND"
	case r < need:
		return 403, "FORBIDDEN"
	}
	return c.ok, ""
}

func (w *isoWorld) auditActions(requestID string) []string {
	w.e.t.Helper()
	var actions []string
	if err := w.e.pool.QueryRow(context.Background(), "SELECT coalesce(array_agg(action ORDER BY id), '{}') FROM audit_events WHERE request_id=$1", requestID).Scan(&actions); err != nil {
		w.e.t.Fatal(err)
	}
	return actions
}

func newIsoWorld(t *testing.T) *isoWorld {
	e := newEnv(t)
	fastStreams(e, StreamConfig{})
	a := e.driveAttempt("succeeded")
	e.publishRun(a)
	w := &isoWorld{e: e, f: a.f, run: a, actors: map[string]user{}, rank: map[string]int{}}
	w.actors["owner"], w.rank["owner"] = a.f.user, 4
	for name, role := range map[string]domain.Role{"viewer": domain.RoleViewer, "runner": domain.RoleRunner, "manager": domain.RoleManager} {
		w.actors[name] = e.login(w.member(role))
		w.rank[name] = map[domain.Role]int{domain.RoleViewer: 1, domain.RoleRunner: 2, domain.RoleManager: 3}[role]
	}
	w.actors["admin"] = e.login(e.newAdminAccount("iso-admin"))
	w.actors["stranger"] = e.newUser()
	full := e.do("GET", "/api/v1/runs/"+a.runID+"/report?include_code=true", nil, a.f.user.o()).want(t, 200, "")
	seen := map[string]bool{}
	for _, x := range full.body["findings"].([]any) {
		m := x.(map[string]any)
		if fp := m["fingerprint"].(string); !seen[fp] {
			seen[fp] = true
			w.findings = append(w.findings, m)
		}
	}
	return w
}

// TC-140: route × actor isolation and the audit row of every accepted
// state change.
func TestRouteIsolationAndAuditTable(t *testing.T) {
	w := newIsoWorld(t)
	e := w.e
	cases := isoCases()
	actors := []string{"admin", "stranger", "viewer", "runner", "manager", "owner"}
	covered := map[string]bool{}
	for _, rt := range e.srv.routes {
		if rt.node != nodeNone {
			// Node routes never take a user session.
			ids := []string{}
			for _, part := range rt.parts {
				if part == "{id}" {
					ids = append(ids, domain.NewID())
				}
			}
			path := isoPath(rt, ids)
			for _, a := range actors {
				r := e.do(rt.method, path, nil, opts{cookie: w.actors[a].cookie, csrf: w.actors[a].csrf, noOrigin: true})
				if r.status < 400 || (rt.node == nodeAuth && r.status != 401) {
					t.Errorf("%s with a user session (%s): %d %s", rt.name, a, r.status, r.raw)
				}
			}
			if len(rt.audit) == 0 {
				t.Errorf("node route %s declares no audit", rt.name)
			}
			continue
		}
		c, ok := cases[rt.name]
		if !ok {
			t.Errorf("route %s %s has no isolation case", rt.method, rt.name)
			continue
		}
		covered[rt.name] = true
		if (unsafeMethod(rt.method) || rt.name == "runs.report") && len(rt.audit) == 0 {
			t.Errorf("state-changing route %s declares no audit action", rt.name)
		}
		t.Run(rt.name, func(t *testing.T) {
			for _, a := range actors {
				u := w.actors[a]
				if c.need == anyone {
					u = user{}
				}
				if c.disposable != nil {
					u = c.disposable(w)
				}
				var ids []string
				if c.ids != nil {
					ids = c.ids(w)
				}
				path := isoPath(rt, ids) + c.query
				status, code := w.expected(c, a)
				if c.disposable != nil {
					status, code = c.ok, ""
				}
				if c.stream {
					s := e.openStream("", u, ids[0], "?cursor=0", "")
					s.close()
					if s.status != status {
						t.Errorf("%s as %s: stream %d, want %d", rt.name, a, s.status, status)
					}
					continue
				}
				o := opts{cookie: u.cookie, csrf: u.csrf}
				if c.opts != nil {
					o = c.opts(w, o)
				}
				var body any
				w.accepting = status < 400
				if c.body != nil {
					body = c.body(w)
				}
				r := e.do(rt.method, path, body, o)
				if r.status != status || r.code() != code {
					t.Errorf("%s as %s: %d %s (%s), want %d %s", rt.name, a, r.status, r.code(), r.raw, status, code)
					continue
				}
				got := w.auditActions(r.header.Get("X-Request-ID"))
				switch {
				case status >= 400 && len(got) != 0:
					t.Errorf("%s as %s refused but audited %v", rt.name, a, got)
				case status < 400 && len(rt.audit) > 0 && (len(got) == 0 || !slices.ContainsFunc(got, func(x string) bool { return slices.Contains(rt.audit, x) })):
					t.Errorf("%s as %s: audit %v, want one of %v", rt.name, a, got, rt.audit)
				case status < 400 && len(rt.audit) == 0 && len(got) != 0:
					t.Errorf("%s as %s: read audited %v", rt.name, a, got)
				}
			}
		})
	}
	for name := range cases {
		if !covered[name] {
			t.Errorf("isolation case %s matches no route", name)
		}
	}
}

// TC-140: the gates in front of every handler, over the router's route list:
// no session is 401 on every authenticated route; a foreign Origin, and a
// missing CSRF token, are 403 on every state-changing user route and record
// nothing; a session that must change its password is 403 on every route
// that does not allow it.
func TestRouteGatesForEveryRoute(t *testing.T) {
	e := newEnv(t)
	admin := e.login(e.newAdminAccount("gate-admin"))
	created := e.do("POST", "/api/v1/admin/users", map[string]string{"username": "gate-pending"}, admin.o()).want(t, 201, "")
	pending, _ := e.exchangeLink(created.body["login_link"].(map[string]any)["url"].(string))
	refusedSilently := func(r resp, what string) {
		t.Helper()
		var n int
		if err := e.pool.QueryRow(context.Background(), "SELECT count(*) FROM audit_events WHERE request_id=$1", r.header.Get("X-Request-ID")).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s recorded %d audit rows", what, n)
		}
	}
	checked := 0
	for _, rt := range e.srv.routes {
		if rt.node != nodeNone {
			continue
		}
		ids := []string{}
		for _, part := range rt.parts {
			if part == "{id}" {
				ids = append(ids, domain.NewID())
			}
		}
		path := isoPath(rt, ids)
		var body any
		if rt.body {
			body = map[string]any{}
		}
		name := rt.method + " " + rt.name
		if rt.auth {
			if r := e.do(rt.method, path, body, opts{}); r.status != 401 || r.code() != "UNAUTHENTICATED" {
				t.Errorf("%s without a session: %d %s", name, r.status, r.code())
			}
		}
		if unsafeMethod(rt.method) {
			r := e.do(rt.method, path, body, opts{cookie: admin.cookie, csrf: admin.csrf, origin: "http://evil.example"})
			if r.status != 403 || r.code() != "ORIGIN_REJECTED" {
				t.Errorf("%s from a foreign origin: %d %s", name, r.status, r.code())
			}
			refusedSilently(r, name+" from a foreign origin")
			if rt.auth {
				r := e.do(rt.method, path, body, opts{cookie: admin.cookie})
				if r.status != 403 || r.code() != "CSRF_REJECTED" {
					t.Errorf("%s without CSRF: %d %s", name, r.status, r.code())
				}
				refusedSilently(r, name+" without CSRF")
			}
		}
		if rt.auth && !rt.pending {
			if rt.name == "runs.events" {
				if s := e.openStream("", pending, ids[0], "?cursor=0", ""); s.status != 403 {
					t.Errorf("%s with a pending session: %d", name, s.status)
				}
			} else if r := e.do(rt.method, path, body, pending.o()); r.status != 403 || r.code() != "PASSWORD_CHANGE_REQUIRED" {
				t.Errorf("%s with a pending session: %d %s", name, r.status, r.code())
			}
		}
		checked++
	}
	if checked < 30 {
		t.Fatalf("only %d user routes checked", checked)
	}
}

func isoPath(rt route, ids []string) string {
	parts := slices.Clone(rt.parts)
	for i, part := range parts {
		if part == "{id}" {
			if len(ids) == 0 {
				return "/api/v1/missing-ids"
			}
			parts[i], ids = ids[0], ids[1:]
		}
	}
	return apiPrefix + "/" + strings.Join(parts, "/")
}

// TC-143: the role a member holds is reported on the project; projects
// outside the caller's membership never appear, administrators included.
func TestProjectRoleFieldsAndLists(t *testing.T) {
	w := newIsoWorld(t)
	e := w.e
	want := map[string][3]any{"viewer": {"viewer", false, false}, "runner": {"runner", true, false}, "manager": {"manager", true, true}, "owner": {"owner", true, true}}
	for a, u := range w.actors {
		list := e.do("GET", "/api/v1/projects", nil, u.o()).want(t, 200, "")
		var found map[string]any
		for _, x := range list.body["items"].([]any) {
			if m := x.(map[string]any); m["id"] == w.f.projectID {
				found = m
			}
		}
		exp, member := want[a]
		if !member {
			if found != nil || strings.Contains(string(list.raw), w.f.projectID) || strings.Contains(string(list.raw), w.run.runID) {
				t.Errorf("%s sees project P: %s", a, list.raw)
			}
			continue
		}
		one := e.do("GET", "/api/v1/projects/"+w.f.projectID, nil, u.o()).want(t, 200, "")
		for _, m := range []map[string]any{found, one.body} {
			if m == nil || m["role"] != exp[0] || m["can_run"] != exp[1] || m["can_manage"] != exp[2] {
				t.Errorf("%s project view %v", a, m)
			}
		}
	}
	// The project audit holds that project's rows only; the global one is
	// for administrators.
	r := e.do("GET", "/api/v1/projects/"+w.f.projectID+"/audit?limit=200", nil, w.actors["manager"].o()).want(t, 200, "")
	items := r.body["items"].([]any)
	if len(items) == 0 {
		t.Fatal("empty project audit")
	}
	for _, x := range items {
		if x.(map[string]any)["project_id"] != w.f.projectID {
			t.Fatalf("foreign row in project audit: %v", x)
		}
	}
	e.do("GET", "/api/v1/projects/"+w.f.projectID+"/audit?project="+w.f.projectID, nil, w.actors["manager"].o()).want(t, 400, "INVALID_REQUEST")
	g := e.do("GET", "/api/v1/audit?project="+w.f.projectID+"&action="+postgres.AuditMemberSet, nil, w.actors["admin"].o()).want(t, 200, "")
	if len(g.body["items"].([]any)) != 3 {
		t.Fatalf("member_set rows %s", g.raw)
	}
}

// TC-144: every export format is isolated and each export is audited with
// its format and whether code was included; a baseline from another project
// is refused even to a user who owns both.
func TestExportIsolationAndCrossProjectBaseline(t *testing.T) {
	w := newIsoWorld(t)
	e := w.e
	base := "/api/v1/runs/" + w.run.runID + "/report"
	for _, q := range []string{"?format=json", "?format=json&include_code=true", "?format=markdown", "?format=markdown&include_code=true", "?format=html", "?format=html&include_code=true"} {
		for _, a := range []string{"admin", "stranger"} {
			r := e.do("GET", base+q, nil, w.actors[a].o()).want(t, 404, "NOT_FOUND")
			if strings.Contains(string(r.raw), w.run.message) || len(w.auditActions(r.header.Get("X-Request-ID"))) != 0 {
				t.Fatalf("%s %s leaked or audited: %s", a, q, r.raw)
			}
		}
		r := e.do("GET", base+q, nil, w.actors["viewer"].o()).want(t, 200, "")
		var detail map[string]any
		if err := e.pool.QueryRow(context.Background(), "SELECT detail FROM audit_events WHERE request_id=$1 AND action=$2 AND project_id=$3 AND actor_user_id=$4",
			r.header.Get("X-Request-ID"), postgres.AuditReportExported, w.f.projectID, w.actors["viewer"].id).Scan(&detail); err != nil {
			t.Fatalf("%s export not audited: %v", q, err)
		}
		format := strings.TrimPrefix(strings.SplitN(q, "&", 2)[0], "?format=")
		if detail["format"] != format || detail["include_code"] != strings.Contains(q, "include_code=true") {
			t.Fatalf("%s audit detail %v", q, detail)
		}
		raw, _ := json.Marshal(detail)
		if strings.Contains(string(raw), w.run.message) {
			t.Fatal("finding text in the audit detail")
		}
	}

	// Another project of the same owner and its published run.
	second := e.do("POST", "/api/v1/projects", map[string]any{"name": "second", "source": map[string]any{"kind": "local", "source_root_id": w.f.root.ID, "relative_path": "."}}, w.f.user.o()).want(t, 201, "")
	other := e.drive(&nodeFixture{user: w.f.user, root: w.f.root, projectID: second.body["id"].(string)}, driveOpts{taskStatus: "succeeded"})
	e.publishRun(other)
	for _, a := range []string{"owner", "viewer"} {
		e.do("GET", "/api/v1/runs/"+w.run.runID+"/comparison?baseline="+other.runID, nil, w.actors[a].o()).want(t, 404, "NOT_FOUND")
	}
	e.do("GET", "/api/v1/runs/"+other.runID+"/comparison", nil, w.actors["viewer"].o()).want(t, 404, "NOT_FOUND")

	// An audit row that cannot be written fails the request: no report body,
	// no run, no member.
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `CREATE FUNCTION refuse_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit refused'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION refuse_audit()`); err != nil {
		t.Fatal(err)
	}
	r := e.do("GET", base+"?format=html&include_code=true", nil, w.actors["viewer"].o()).want(t, 503, "UNAVAILABLE")
	if strings.Contains(string(r.raw), w.run.message) || strings.Contains(string(r.raw), "<html") {
		t.Fatalf("report sent without its audit row: %s", r.raw)
	}
	var runs int
	_ = e.pool.QueryRow(ctx, "SELECT count(*) FROM runs WHERE project_id=$1", w.f.projectID).Scan(&runs)
	e.do("POST", "/api/v1/projects/"+w.f.projectID+"/runs", map[string]any{"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": map[string]any{"kind": "full"}},
		opts{cookie: w.f.user.cookie, csrf: w.f.user.csrf, idem: domain.NewID()}).want(t, 503, "UNAVAILABLE")
	var after int
	_ = e.pool.QueryRow(ctx, "SELECT count(*) FROM runs WHERE project_id=$1", w.f.projectID).Scan(&after)
	if after != runs {
		t.Fatalf("run created without its audit row: %d -> %d", runs, after)
	}
	stranger := w.actors["stranger"]
	e.do("PUT", "/api/v1/projects/"+w.f.projectID+"/members/"+stranger.id, map[string]string{"role": "viewer"}, w.f.user.o()).want(t, 503, "UNAVAILABLE")
	e.do("GET", "/api/v1/projects/"+w.f.projectID, nil, stranger.o()).want(t, 404, "NOT_FOUND")
}

// TC-145: an open event stream ends when the member is removed (over the
// API), when the user is disabled and when the password is reset.
func TestStreamEndsOnAccessChanges(t *testing.T) {
	w := newIsoWorld(t)
	e := w.e
	manager, admin := w.actors["manager"], w.actors["admin"]
	closedWith := func(s *sseClient) any {
		t.Helper()
		m := s.until(func(m sseMessage) bool { return m.event == "closed" })
		s.ended()
		return m[len(m)-1].data["reason"]
	}

	removed := e.login(w.member(domain.RoleViewer))
	s := e.openStream("", removed, w.run.runID, "?cursor=1", "")
	e.do("DELETE", "/api/v1/projects/"+w.f.projectID+"/members/"+removed.id, nil, manager.o()).want(t, 204, "")
	if r := closedWith(s); r != ClosedAccessRevoked {
		t.Fatalf("member removal closed with %v", r)
	}

	disabled := e.login(w.member(domain.RoleViewer))
	s = e.openStream("", disabled, w.run.runID, "?cursor=1", "")
	e.do("POST", "/api/v1/admin/users/"+disabled.id+"/disable", nil, admin.o()).want(t, 200, "")
	if r := closedWith(s); r != ClosedUnauthenticated {
		t.Fatalf("disable closed with %v", r)
	}

	resetID := w.e.account(w.name("reset"), "a long enough passphrase", false)
	if err := e.store.SetMember(context.Background(), w.f.user.id, w.f.projectID, resetID, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	reset := e.login(resetID)
	s = e.openStream("", reset, w.run.runID, "?cursor=1", "")
	e.do("POST", "/api/v1/admin/users/"+resetID+"/password-reset", nil, admin.o()).want(t, 200, "")
	if r := closedWith(s); r != ClosedUnauthenticated {
		t.Fatalf("password reset closed with %v", r)
	}
	// A pending (must change password) session cannot open a stream.
	pending := e.do("POST", "/api/v1/admin/users", map[string]string{"username": w.name("pending")}, admin.o()).want(t, 201, "")
	pu, _ := e.exchangeLink(pending.body["login_link"].(map[string]any)["url"].(string))
	if err := e.store.SetMember(context.Background(), w.f.user.id, w.f.projectID, pu.id, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if s := e.openStream("", pu, w.run.runID, "?cursor=1", ""); s.status != 403 {
		t.Fatalf("pending session stream %d", s.status)
	}
}
