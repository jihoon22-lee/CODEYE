package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Administrator routes (/admin/…, /audit). The administrator role manages
// accounts and access; it opens no project. Every handler passes the session
// user as the principal and the store re-checks the role in the same
// transaction as the change.

// launchTTL is the lifetime of login links issued by the API (create, reset).
const launchTTL = 10 * time.Minute

type userView struct {
	ID                 string     `json:"id"`
	Username           *string    `json:"username"`
	DisplayName        *string    `json:"display_name"`
	IsAdmin            bool       `json:"is_admin"`
	MustChangePassword bool       `json:"must_change_password"`
	HasPassword        bool       `json:"has_password"`
	Disabled           bool       `json:"disabled"`
	DisabledAt         *time.Time `json:"disabled_at"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

func viewUser(u postgres.User) userView {
	return userView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, IsAdmin: u.IsAdmin, MustChangePassword: u.MustChangePassword,
		HasPassword: u.HasPassword, Disabled: u.DisabledAt != nil, DisabledAt: u.DisabledAt, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt}
}

// loginLinkView is a single-use login link returned once to the
// administrator, who hands it to the user. Only its hash is stored.
type loginLinkView struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (q *request) principal() postgres.Principal { return postgres.UserPrincipal(q.session.UserID) }

// queryInt reads an optional bounded integer query parameter.
func queryInt(v url.Values, name string, def, lo, hi int) (int, bool) {
	raw := v.Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi || strconv.Itoa(n) != raw {
		return 0, false
	}
	return n, true
}

// queryValues is onlyQuery that also returns the values.
func queryValues(q *request, allowed ...string) (url.Values, bool) {
	if !onlyQuery(q, allowed...) {
		return nil, false
	}
	return q.r.URL.Query(), true
}

func (s *Server) adminListUsers(q *request) {
	v, ok := queryValues(q, "cursor", "limit")
	if !ok {
		return
	}
	limit, ok := queryInt(v, "limit", 50, 1, 200)
	if !ok || (v.Get("cursor") != "" && !domain.ValidID(v.Get("cursor"))) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "limit must be 1..200 and cursor a user ID", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	page, err := s.store.ListUsers(ctx, q.principal(), v.Get("cursor"), limit)
	if err != nil {
		s.storeError(q, err)
		return
	}
	items := make([]userView, 0, len(page.Items))
	for _, u := range page.Items {
		items = append(items, viewUser(u))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// POST /admin/users {username, display_name?, is_admin?}: the account has no
// password; the response carries a single-use login link with which the
// user signs in once and must set a password.
func (s *Server) adminCreateUser(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Username    string  `json:"username"`
		DisplayName *string `json:"display_name"`
		IsAdmin     bool    `json:"is_admin"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Username == "" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"username": string, "display_name"?: string, "is_admin"?: boolean}`, false)
		return
	}
	display := ""
	if in.DisplayName != nil {
		display = *in.DisplayName
	}
	token := NewToken()
	ctx, cancel := q.ctx()
	defer cancel()
	u, err := s.store.CreateAccount(ctx, q.principal(), postgres.NewAccount{Username: in.Username, DisplayName: display, IsAdmin: in.IsAdmin,
		MustChangePassword: true, LaunchTokenSHA256: TokenHash(token), LaunchTTL: launchTTL})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "the username is taken", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	q.w.Header().Set("Location", apiPrefix+"/admin/users/"+u.ID)
	writeJSON(q.w, http.StatusCreated, map[string]any{"user": viewUser(u), "login_link": s.loginLink(q, token)})
}

func (s *Server) loginLink(q *request, token string) loginLinkView {
	return loginLinkView{URL: s.loginOrigin(q.r) + "/#launch=" + token, ExpiresAt: time.Now().Add(launchTTL).UTC().Truncate(time.Second)}
}

func (s *Server) adminDisableUser(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	u, err := s.store.DisableAccount(ctx, q.principal(), q.params[0])
	if err != nil {
		s.lastAdminError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, viewUser(u))
}

func (s *Server) lastAdminError(q *request, err error) {
	if errors.Is(err, domain.ErrConflict) {
		writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "the last enabled administrator must stay an enabled administrator (or the user is disabled)", false)
		return
	}
	s.storeError(q, err)
}

// PUT /admin/users/{id}/admin {is_admin}
func (s *Server) adminSetAdmin(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		IsAdmin *bool `json:"is_admin"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.IsAdmin == nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"is_admin": boolean}`, false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	u, err := s.store.SetAdmin(ctx, q.principal(), q.params[0], *in.IsAdmin)
	if err != nil {
		s.lastAdminError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, viewUser(u))
}

// POST /admin/users/{id}/password-reset: the password is cleared, every
// session revoked and a single-use login link returned; the user then sets
// a new password. An administrator's own password changes through
// POST /session/password (409 here).
func (s *Server) adminResetPassword(q *request) {
	token := NewToken()
	ctx, cancel := q.ctx()
	defer cancel()
	u, err := s.store.ResetPassword(ctx, q.principal(), q.params[0], TokenHash(token), launchTTL)
	if err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"user": viewUser(u), "login_link": s.loginLink(q, token)})
}

func (s *Server) adminListSourceRoots(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	roots, err := s.store.ListSourceRootAccess(ctx, q.principal())
	if err != nil {
		s.storeError(q, err)
		return
	}
	type grant struct {
		UserID    string    `json:"user_id"`
		Username  *string   `json:"username"`
		GrantedAt time.Time `json:"granted_at"`
	}
	type item struct {
		ID        string    `json:"id"`
		Label     string    `json:"label"`
		CreatedAt time.Time `json:"created_at"`
		Grants    []grant   `json:"grants"`
	}
	items := make([]item, 0, len(roots))
	for _, r := range roots {
		it := item{ID: r.ID, Label: r.Label, CreatedAt: r.CreatedAt, Grants: []grant{}}
		for _, g := range r.Grants {
			it.Grants = append(it.Grants, grant{g.UserID, g.Username, g.GrantedAt})
		}
		items = append(items, it)
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminGrantSourceRoot(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.GrantSourceRootBy(ctx, q.principal(), q.params[0], q.params[1]); err != nil {
		s.storeError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminRevokeSourceRoot(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RevokeSourceRootGrant(ctx, q.principal(), q.params[0], q.params[1]); err != nil {
		s.storeError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminListProviders(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	provs, err := s.store.ListProvidersBy(ctx, q.principal())
	if err != nil {
		s.storeError(q, err)
		return
	}
	type item struct {
		ID     string  `json:"id"`
		Kind   string  `json:"kind"`
		Host   string  `json:"host"`
		APIURL *string `json:"api_url"`
	}
	items := make([]item, 0, len(provs))
	for _, p := range provs {
		it := item{ID: p.ID, Kind: p.Kind, Host: p.Host}
		if p.APIURL != "" {
			api := p.APIURL
			it.APIURL = &api
		}
		items = append(items, it)
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items})
}

type auditView struct {
	ID            string          `json:"id"`
	OccurredAt    time.Time       `json:"occurred_at"`
	ActorKind     string          `json:"actor_kind"`
	ActorUserID   *string         `json:"actor_user_id"`
	ActorUsername *string         `json:"actor_username"`
	ActorNodeID   *string         `json:"actor_node_id"`
	Action        string          `json:"action"`
	TargetType    *string         `json:"target_type"`
	TargetID      *string         `json:"target_id"`
	ProjectID     *string         `json:"project_id"`
	RequestID     *string         `json:"request_id"`
	Detail        json.RawMessage `json:"detail"`
}

// auditFilter parses since, until (RFC 3339), actor (user ID), action,
// project (global read only), cursor and limit (1..200, default 50).
func auditFilter(q *request, global bool) (postgres.AuditFilter, bool) {
	allowed := []string{"since", "until", "actor", "action", "cursor", "limit"}
	if global {
		allowed = append(allowed, "project")
	}
	v, ok := queryValues(q, allowed...)
	if !ok {
		return postgres.AuditFilter{}, false
	}
	bad := func() (postgres.AuditFilter, bool) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "invalid audit filter", false)
		return postgres.AuditFilter{}, false
	}
	f := postgres.AuditFilter{ActorUserID: v.Get("actor"), Action: v.Get("action"), ProjectID: v.Get("project"), Cursor: v.Get("cursor")}
	if f.Limit, ok = queryInt(v, "limit", 50, 1, 200); !ok {
		return bad()
	}
	for name, dst := range map[string]**time.Time{"since": &f.Since, "until": &f.Until} {
		if raw := v.Get(name); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return bad()
			}
			*dst = &t
		}
	}
	if (f.ActorUserID != "" && !domain.ValidID(f.ActorUserID)) || (f.ProjectID != "" && !domain.ValidID(f.ProjectID)) {
		return bad()
	}
	return f, true
}

func (s *Server) writeAudit(q *request, page postgres.AuditPage, err error) {
	if err != nil {
		if errors.Is(err, domain.ErrInvalid) {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "invalid audit filter", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	items := make([]auditView, 0, len(page.Items))
	for _, r := range page.Items {
		items = append(items, auditView{ID: strconv.FormatInt(r.ID, 10), OccurredAt: r.OccurredAt, ActorKind: r.ActorKind, ActorUserID: r.ActorUserID,
			ActorUsername: r.ActorUsername, ActorNodeID: r.ActorNodeID, Action: r.Action, TargetType: r.TargetType, TargetID: r.TargetID,
			ProjectID: r.ProjectID, RequestID: r.RequestID, Detail: r.Detail})
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

// GET /audit (administrator): every row.
func (s *Server) listAudit(q *request) {
	f, ok := auditFilter(q, true)
	if !ok {
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	page, err := s.store.ListAudit(ctx, q.session.UserID, f)
	s.writeAudit(q, page, err)
}

// GET /projects/{id}/audit (project manager): that project's rows only.
func (s *Server) projectAudit(q *request) {
	f, ok := auditFilter(q, false)
	if !ok {
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	page, err := s.store.ListProjectAudit(ctx, q.session.UserID, q.params[0], f)
	s.writeAudit(q, page, err)
}
