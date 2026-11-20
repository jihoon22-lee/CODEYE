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
	Email              *string   `json:"email,omitempty"`
	DisplayName        *string   `json:"display_name"`
	IsAdmin            bool      `json:"is_admin"`
	MustChangePassword bool      `json:"must_change_password"`
	HasPassword        bool      `json:"has_password"`
	// PasswordReset is the account's most recent administrator reset for 30
	// days after it (null otherwise), for the screen to tell the user who
	// reset it and when. It cannot be dismissed.
	PasswordReset *resetView `json:"password_reset"`
}

type resetView struct {
	ResetAt         time.Time `json:"reset_at"`
	ResetByUserID   string    `json:"reset_by_user_id"`
	ResetByUsername *string   `json:"reset_by_username"`
}

func viewSession(sess postgres.Session, token string) sessionView {
	v := sessionView{UserID: sess.UserID, CSRFToken: csrfToken(token), ExpiresAt: sess.ExpiresAt.UTC(), Username: sess.Username, Email: sess.Email,
		DisplayName: sess.DisplayName, IsAdmin: sess.IsAdmin, MustChangePassword: sess.MustChangePassword, HasPassword: sess.HasPassword}
	if r := sess.PasswordReset; r != nil {
		v.PasswordReset = &resetView{ResetAt: r.At, ResetByUserID: r.ByUserID, ResetByUsername: r.ByUsername}
	}
	return v
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	f := s.faceOf(r)
	c := &http.Cookie{Name: f.cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: f.tls != nil}
	if token == "" {
		c.MaxAge = -1
	} else {
		c.MaxAge = int(time.Until(expires).Seconds())
	}
	http.SetCookie(w, c)
}

func (s *Server) registerAccount(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Email    *string `json:"email"`
		Password *string `json:"password"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Email == nil || in.Password == nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"email": string, "password": string}`, false)
		return
	}
	token := NewToken()
	ctx, cancel := q.ctx()
	defer cancel()
	sess, err := s.store.RegisterAccount(ctx, postgres.Registration{Email: *in.Email, Password: *in.Password, SessionTokenSHA256: TokenHash(token), TTL: s.sessionTTL})
	switch {
	case errors.Is(err, postgres.ErrEmailTaken):
		writeError(q.w, q.r, http.StatusConflict, "EMAIL_TAKEN", "the email address is already registered", false)
	case errors.Is(err, domain.ErrInvalid):
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "a valid email address and a password that meets the policy (12 to 256 characters) are required", false)
	case err != nil:
		s.storeError(q, err)
	default:
		s.setSessionCookie(q.w, q.r, token, sess.ExpiresAt)
		writeJSON(q.w, http.StatusCreated, viewSession(sess, token))
	}
}

// POST /session: {launch_token}, {email, password}, or {username, password}. Every credential
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
		Email       *string `json:"email"`
		Password    *string `json:"password"`
	}
	if domain.DecodeStrict(body, &in) != nil ||
		(in.LaunchToken != nil && (in.Username != nil || in.Email != nil || in.Password != nil)) ||
		(in.LaunchToken == nil && (in.Password == nil || (in.Username == nil) == (in.Email == nil))) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"launch_token": string}, {"email": string, "password": string}, or {"username": string, "password": string}`, false)
		return
	}
	launch := in.LaunchToken != nil
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
		creds := postgres.PasswordLogin{Password: *in.Password, Client: clientAddress(q.r), SessionTokenSHA256: TokenHash(token), TTL: s.sessionTTL}
		if in.Email != nil {
			creds.Email = *in.Email
		} else {
			creds.Username = *in.Username
		}
		if (creds.Username == "" && creds.Email == "") || len(creds.Username) > 256 || len(creds.Email) > 1024 || len(creds.Password) > 1024 {
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "login identifier or password is incorrect", false)
			return
		}
		sess, err = s.store.PasswordLogin(ctx, creds)
	}
	if err != nil {
		switch {
		case launch && errors.Is(err, domain.ErrNotFound):
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "launch token is invalid, used or expired", false)
		case errors.Is(err, domain.ErrUnauthenticated):
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "login identifier or password is incorrect", false)
		default:
			s.storeError(q, err)
		}
		return
	}
	s.setSessionCookie(q.w, q.r, token, sess.ExpiresAt)
	writeJSON(q.w, http.StatusCreated, viewSession(sess, token))
}

func (s *Server) getSession(q *request) {
	writeJSON(q.w, http.StatusOK, viewSession(q.session, q.cookie))
}

func (s *Server) deleteSession(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RevokeSession(ctx, q.session.ID); err != nil {
		s.storeError(q, err)
		return
	}
	s.setSessionCookie(q.w, q.r, "", time.Time{})
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
			writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "the new password does not meet the policy (12 to 256 characters, distinct from the login identifier)", false)
		default:
			s.storeError(q, err)
		}
		return
	}
	writeJSON(q.w, http.StatusOK, viewSession(sess, q.cookie))
}
