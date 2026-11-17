package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/migrations"
)

// NewFromPool wraps a caller-built pool. Production code uses Open, which
// enforces the connection policy; tests use this to bind an isolated schema.
func NewFromPool(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Ping checks that the database answers.
func (s *Store) Ping(ctx context.Context) error { return classify(s.pool.Ping(ctx)) }

// SchemaState is the result of comparing the database migration history with
// the migrations embedded in this binary.
type SchemaState struct {
	Applied, Latest int
	// State is current, outdated (pending migrations), newer (unknown future
	// version) or inconsistent (checksum drift or missing version).
	State string
}

// SchemaStatus inspects the migration history read-only. It never migrates.
func (s *Store) SchemaStatus(ctx context.Context) (SchemaState, error) {
	latest, err := migrations.LatestVersion()
	if err != nil {
		return SchemaState{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return SchemaState{}, classify(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	applied, err := migrations.Inspect(ctx, tx)
	st := SchemaState{Applied: applied, Latest: latest}
	switch {
	case err == nil && applied == latest:
		st.State = "current"
	case err == nil:
		st.State = "outdated"
	case errors.Is(err, migrations.ErrUnknownVersion):
		st.State = "newer"
	case errors.Is(err, migrations.ErrChecksumMismatch), errors.Is(err, migrations.ErrMissingVersion):
		st.State = "inconsistent"
	default:
		return SchemaState{}, classify(err)
	}
	return st, nil
}

var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

const (
	maxLaunchTTL  = time.Hour
	maxSessionTTL = 7 * 24 * time.Hour
)

// Session is an authenticated browser session. The raw token is never stored.
type Session struct {
	ID, UserID string
	ExpiresAt  time.Time
}

func cleanLabel(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > max {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return s, true
}

// CreateUserNamed provisions a user with an optional display name. It is a
// trusted operator action (CLI); HTTP never creates users.
func (s *Store) CreateUserNamed(ctx context.Context, name string) (string, error) {
	var display any
	if strings.TrimSpace(name) != "" {
		clean, ok := cleanLabel(name, 200)
		if !ok {
			return "", domain.ErrInvalid
		}
		display = clean
	}
	id := domain.NewID()
	_, err := s.pool.Exec(ctx, "INSERT INTO users(id,display_name) VALUES($1,$2)", id, display)
	return id, classify(err)
}

// DisableUser permanently disables a user and revokes its sessions.
func (s *Store) DisableUser(ctx context.Context, id string) error {
	if !validIDs(id) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		var disabled *time.Time
		if err := tx.QueryRow(ctx, "SELECT disabled_at FROM users WHERE id=$1 FOR UPDATE", id).Scan(&disabled); err != nil {
			return false, err
		}
		if disabled == nil {
			if _, err := tx.Exec(ctx, "UPDATE users SET disabled_at=clock_timestamp() WHERE id=$1", id); err != nil {
				return false, err
			}
		}
		_, err := tx.Exec(ctx, "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", id)
		return true, err
	})
	return err
}

// IssueLaunchToken stores the SHA-256 of a single-use launch token for an
// enabled user. Unknown or disabled users yield ErrNotFound.
func (s *Store) IssueLaunchToken(ctx context.Context, user, tokenSHA256 string, ttl time.Duration) error {
	if !validIDs(user) || !hexDigest.MatchString(tokenSHA256) || ttl < time.Second || ttl > maxLaunchTTL {
		return domain.ErrInvalid
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO auth_launch_tokens(token_sha256,user_id,expires_at)
		SELECT $1,id,clock_timestamp()+$3*interval '1 millisecond' FROM users WHERE id=$2 AND disabled_at IS NULL FOR SHARE`,
		tokenSHA256, user, ttl.Milliseconds())
	if err != nil {
		return classify(err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

// ExchangeLaunchToken consumes an unused, unexpired launch token of an enabled
// user and creates a session in the same transaction. Concurrent exchanges of
// one token create at most one session; the others get ErrNotFound.
func (s *Store) ExchangeLaunchToken(ctx context.Context, tokenSHA256, sessionTokenSHA256 string, ttl time.Duration) (Session, error) {
	if !hexDigest.MatchString(tokenSHA256) || !hexDigest.MatchString(sessionTokenSHA256) || ttl < time.Second || ttl > maxSessionTTL {
		return Session{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Session, error) {
		// Lock order user -> token -> session, the same as DisableUser (user FOR
		// UPDATE -> sessions), so a concurrent disable and exchange serialize:
		// either the disable sees and revokes the new session, or the exchange
		// sees the disabled user and creates nothing.
		var sess Session
		if err := tx.QueryRow(ctx, "SELECT user_id::text FROM auth_launch_tokens WHERE token_sha256=$1", tokenSHA256).Scan(&sess.UserID); err != nil {
			return Session{}, err
		}
		var enabled bool
		if err := tx.QueryRow(ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1 FOR SHARE", sess.UserID).Scan(&enabled); err != nil {
			return Session{}, err
		}
		if !enabled {
			return Session{}, domain.ErrNotFound
		}
		tag, err := tx.Exec(ctx, `UPDATE auth_launch_tokens SET used_at=clock_timestamp()
			WHERE token_sha256=$1 AND used_at IS NULL AND expires_at>clock_timestamp()`, tokenSHA256)
		if err != nil {
			return Session{}, err
		}
		if tag.RowsAffected() != 1 {
			return Session{}, domain.ErrNotFound
		}
		sess.ID = domain.NewID()
		err = tx.QueryRow(ctx, `INSERT INTO auth_sessions(id,user_id,token_sha256,expires_at)
			VALUES($1,$2,$3,clock_timestamp()+$4*interval '1 millisecond') RETURNING expires_at`,
			sess.ID, sess.UserID, sessionTokenSHA256, ttl.Milliseconds()).Scan(&sess.ExpiresAt)
		return sess, err
	})
}

// SessionByTokenHash returns the live session for a token hash. Expiry uses
// the database clock. Revoked, expired or disabled-user sessions yield
// ErrNotFound.
func (s *Store) SessionByTokenHash(ctx context.Context, tokenSHA256 string) (Session, error) {
	if !hexDigest.MatchString(tokenSHA256) {
		return Session{}, domain.ErrNotFound
	}
	var sess Session
	err := s.pool.QueryRow(ctx, `SELECT s.id::text,s.user_id::text,s.expires_at FROM auth_sessions s
		JOIN users u ON u.id=s.user_id
		WHERE s.token_sha256=$1 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND u.disabled_at IS NULL`,
		tokenSHA256).Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt)
	return sess, classify(err)
}

// RevokeSession ends a session. Revoking an already revoked session succeeds.
func (s *Store) RevokeSession(ctx context.Context, id string) error {
	if !validIDs(id) {
		return domain.ErrInvalid
	}
	_, err := s.pool.Exec(ctx, "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL", id)
	return classify(err)
}
