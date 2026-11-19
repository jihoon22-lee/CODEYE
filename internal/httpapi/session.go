package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// NewToken returns a random URL-safe token (32 bytes, base64url, no padding).
func NewToken() string {
	var b [tokenBytes]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// TokenHash is the stored form of a token: lowercase hex SHA-256 of its text.
func TokenHash(token string) string { return domain.Digest([]byte(token)) }

func validToken(t string) bool {
	if len(t) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(t)
	return err == nil && len(b) == tokenBytes
}

// csrfToken derives the CSRF token from the session token. Only a holder of
// the HttpOnly session cookie (the server) can compute it; the browser app
// receives it from /session responses.
func csrfToken(session string) string {
	sum := sha256.Sum256([]byte("codeye-csrf\x00" + session))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func itoa(n int) string { return strconv.Itoa(n) }

// clientAddress is the throttle key of the connection: the remote IP for
// IPv4, the /64 prefix for IPv6. Forwarded headers are never read (no proxy
// mode), so a client cannot choose its own key.
func clientAddress(r *http.Request) string {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	a := ap.Addr().Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return "unknown"
	}
	return p.String()
}

type sessionView struct {
	UserID             string    `json:"user_id"`
	CSRFToken          string    `json:"csrf_token"`
	ExpiresAt          time.Time `json:"expires_at"`
	Username           *string   `json:"username"`
	DisplayName        *string   `json:"display_name"`
	IsAdmin            bool      `json:"is_admin"`
	MustChangePassword bool      `json:"must_change_password"`
	HasPassword        bool      `json:"has_password"`
	// PasswordReset is the account's last password reset (null if none),
	// for the screen to tell the user who reset it and when.
	PasswordReset *resetView `json:"password_reset"`
}

type resetView struct {
	ResetAt         time.Time `json:"reset_at"`
	ResetByKind     string    `json:"reset_by_kind"`
	ResetByUserID   *string   `json:"reset_by_user_id"`
	ResetByUsername *string   `json:"reset_by_username"`
	SignInsSince    int       `json:"sign_ins_since"`
	Acknowledged    bool      `json:"acknowledged"`
}

func viewSession(sess postgres.Session, token string) sessionView {
	v := sessionView{UserID: sess.UserID, CSRFToken: csrfToken(token), ExpiresAt: sess.ExpiresAt.UTC(), Username: sess.Username,
		DisplayName: sess.DisplayName, IsAdmin: sess.IsAdmin, MustChangePassword: sess.MustChangePassword, HasPassword: sess.HasPassword}
	if r := sess.PasswordReset; r != nil {
		kind := postgres.ActorUser
		if r.ByUserID == nil {
			kind = postgres.ActorOperator
		}
		v.PasswordReset = &resetView{ResetAt: r.At, ResetByKind: kind, ResetByUserID: r.ByUserID, ResetByUsername: r.ByUsername,
			SignInsSince: r.SignInsSince, Acknowledged: r.Acknowledged}
	}
	return v
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	c := &http.Cookie{Name: s.cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.tls != nil}
	if token == "" {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(time.Until(expires).Seconds())
	}
	http.SetCookie(w, c)
}

// POST /session: {launch_token} or {username, password}. Every credential
// failure is the same 401 with the same message; throttled attempts are 429
// with Retry-After.
func (s *Server) createSession(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		LaunchToken *string `json:"launch_token"`
		Username    *string `json:"username"`
		Password    *string `json:"password"`
	}
	if domain.DecodeStrict(body, &in) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"launch_token": string} or {"username": string, "password": string}`, false)
		return
	}
	launch := in.LaunchToken != nil
	if launch == (in.Username != nil || in.Password != nil) || (!launch && (in.Username == nil || in.Password == nil)) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"launch_token": string} or {"username": string, "password": string}`, false)
		return
	}
	token := NewToken()
	ctx, cancel := q.ctx()
	defer cancel()
	var sess postgres.Session
	var err error
	if launch {
		if !validToken(*in.LaunchToken) {
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "launch token is invalid, used or expired", false)
			return
		}
		sess, err = s.store.ExchangeLaunchTokenFrom(ctx, clientAddress(q.r), TokenHash(*in.LaunchToken), TokenHash(token), s.sessionTTL)
	} else {
		if *in.Username == "" || len(*in.Username) > 256 || len(*in.Password) > 1024 {
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "username or password is incorrect", false)
			return
		}
		sess, err = s.store.PasswordLogin(ctx, postgres.PasswordLogin{Username: *in.Username, Password: *in.Password, Client: clientAddress(q.r),
			SessionTokenSHA256: TokenHash(token), TTL: s.sessionTTL})
	}
	if err != nil {
		switch {
		case launch && errors.Is(err, domain.ErrNotFound):
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "launch token is invalid, used or expired", false)
		case errors.Is(err, domain.ErrUnauthenticated):
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "username or password is incorrect", false)
		default:
			s.storeError(q, err)
		}
		return
	}
	s.setSessionCookie(q.w, token, sess.ExpiresAt)
	writeJSON(q.w, http.StatusCreated, viewSession(sess, token))
}

func (s *Server) getSession(q *request) {
	writeJSON(q.w, http.StatusOK, viewSession(q.session, q.cookie))
}

// POST /session/password-reset/acknowledge: the user has seen the notice of
// the last password reset. The record stays in the session view.
func (s *Server) acknowledgeReset(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.AcknowledgePasswordReset(ctx, q.session.UserID); err != nil {
		s.storeError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSession(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RevokeSession(ctx, q.session.ID); err != nil {
		s.storeError(q, err)
		return
	}
	s.setSessionCookie(q.w, "", time.Time{})
	q.w.WriteHeader(http.StatusNoContent)
}

// POST /session/password: {current_password?, new_password}. The current
// password is required when the user has one. The other sessions of the
// user are revoked; this session stays and its response clears
// must_change_password.
func (s *Server) changePassword(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Current *string `json:"current_password"`
		New     string  `json:"new_password"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.New == "" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"current_password"?: string, "new_password": string}`, false)
		return
	}
	current := ""
	if in.Current != nil {
		current = *in.Current
	}
	if q.session.HasPassword && in.Current == nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "current_password is required", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	sess, err := s.store.ChangePassword(ctx, postgres.PasswordChange{SessionID: q.session.ID, UserID: q.session.UserID, Current: current, New: in.New, Client: clientAddress(q.r)})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrUnauthenticated):
			writeError(q.w, q.r, http.StatusForbidden, "PASSWORD_INCORRECT", "the current password is incorrect", false)
		case errors.Is(err, domain.ErrInvalid):
			writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "the new password does not meet the policy (12 to 256 characters, not the username)", false)
		default:
			s.storeError(q, err)
		}
		return
	}
	writeJSON(q.w, http.StatusOK, viewSession(sess, q.cookie))
}
