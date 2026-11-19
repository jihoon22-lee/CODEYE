package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
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

type sessionView struct {
	UserID    string    `json:"user_id"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) createSession(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		LaunchToken string `json:"launch_token"`
	}
	if domain.DecodeStrict(body, &in) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "body must be {\"launch_token\": string}", false)
		return
	}
	if !validToken(in.LaunchToken) {
		writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "launch token is invalid, used or expired", false)
		return
	}
	token := NewToken()
	ctx, cancel := q.ctx()
	defer cancel()
	sess, err := s.store.ExchangeLaunchToken(ctx, TokenHash(in.LaunchToken), TokenHash(token), s.sessionTTL)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "launch token is invalid, used or expired", false)
		} else {
			s.storeError(q, err)
		}
		return
	}
	http.SetCookie(q.w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(time.Until(sess.ExpiresAt).Seconds()),
	})
	writeJSON(q.w, http.StatusCreated, sessionView{sess.UserID, csrfToken(token), sess.ExpiresAt.UTC()})
}

func (s *Server) getSession(q *request) {
	writeJSON(q.w, http.StatusOK, sessionView{q.session.UserID, csrfToken(q.cookie), q.session.ExpiresAt.UTC()})
}

func (s *Server) deleteSession(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RevokeSession(ctx, q.session.ID); err != nil {
		s.storeError(q, err)
		return
	}
	http.SetCookie(q.w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	q.w.WriteHeader(http.StatusNoContent)
}
