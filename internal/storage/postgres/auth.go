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

// Session is an authenticated browser session with the user fields every
// request re-reads (so a disable, demotion or reset applies at the next
// request). The raw token is never stored.
type Session struct {
	ID, UserID            string
	ExpiresAt             time.Time
	Username, DisplayName *string
	IsAdmin               bool
	MustChangePassword    bool
	HasPassword           bool
}

const sessionSelect = `SELECT s.id::text,s.user_id::text,s.expires_at,u.username,u.display_name,u.is_admin,u.must_change_password,u.password_kdf IS NOT NULL
	FROM auth_sessions s JOIN users u ON u.id=s.user_id`

func scanSession(row pgx.Row) (Session, error) {
	var sess Session
	err := row.Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt, &sess.Username, &sess.DisplayName, &sess.IsAdmin, &sess.MustChangePassword, &sess.HasPassword)
	return sess, err
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

// CreateUserNamed provisions a login-link user (no username or password)
// with an optional display name. It is an operator action (CLI), audited as
// operator_cli.
func (s *Store) CreateUserNamed(ctx context.Context, name string) (string, error) {
	u, err := s.CreateAccount(ctx, OperatorPrincipal(), NewAccount{DisplayName: name})
	return u.ID, err
}

// DisableUser is DisableAccount by the operator CLI.
func (s *Store) DisableUser(ctx context.Context, id string) error {
	_, err := s.DisableAccount(ctx, OperatorPrincipal(), id)
	return err
}

// IssueLaunchToken stores the SHA-256 of a single-use launch token for an
// enabled user (operator CLI: login-link, serve --user, personal start).
// Unknown or disabled users yield ErrNotFound.
func (s *Store) IssueLaunchToken(ctx context.Context, user, tokenSHA256 string, ttl time.Duration) error {
	if !validIDs(user) || !hexDigest.MatchString(tokenSHA256) || ttl < time.Second || ttl > maxLaunchTTL {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if err := insertLaunchToken(ctx, tx, user, tokenSHA256, ttl); err != nil {
			return false, err
		}
		e := principalAudit(OperatorPrincipal(), AuditLoginLinkIssued).target("user", user).with(map[string]any{"expires_at": time.Now().Add(ttl).UTC().Truncate(time.Second)})
		return true, writeAudit(ctx, tx, e)
	})
	return err
}

// insertLaunchToken stores a launch token for an enabled user in tx.
func insertLaunchToken(ctx context.Context, tx pgx.Tx, user, tokenSHA256 string, ttl time.Duration) error {
	tag, err := tx.Exec(ctx, `INSERT INTO auth_launch_tokens(token_sha256,user_id,expires_at)
		SELECT $1,id,clock_timestamp()+$3*interval '1 millisecond' FROM users WHERE id=$2 AND disabled_at IS NULL FOR SHARE`,
		tokenSHA256, user, ttl.Milliseconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

// ExchangeLaunchToken is ExchangeLaunchTokenFrom without a client address
// (no address throttle).
func (s *Store) ExchangeLaunchToken(ctx context.Context, tokenSHA256, sessionTokenSHA256 string, ttl time.Duration) (Session, error) {
	return s.ExchangeLaunchTokenFrom(ctx, "", tokenSHA256, sessionTokenSHA256, ttl)
}

// ExchangeLaunchTokenFrom consumes an unused, unexpired launch token of an
// enabled user and creates a session in the same transaction. Concurrent
// exchanges of one token create at most one session; the others get
// ErrNotFound. A client address that keeps presenting bad tokens is
// throttled like failed password logins (*ThrottledError); the throttle
// check consumes nothing. Success and failure are audited.
func (s *Store) ExchangeLaunchTokenFrom(ctx context.Context, client, tokenSHA256, sessionTokenSHA256 string, ttl time.Duration) (Session, error) {
	if !hexDigest.MatchString(tokenSHA256) || !hexDigest.MatchString(sessionTokenSHA256) || ttl < time.Second || ttl > maxSessionTTL {
		return Session{}, domain.ErrInvalid
	}
	if client != "" {
		wait, err := transaction(ctx, s, func(tx pgx.Tx) (time.Duration, error) {
			keys, err := throttleKeys(ctx, tx, "", client)
			if err != nil {
				return 0, err
			}
			states, now, err := lockThrottle(ctx, tx, keys)
			if err != nil {
				return 0, err
			}
			return refuseIfLocked(ctx, tx, states, now, MethodLaunchLink, client)
		})
		if err != nil {
			return Session{}, err
		}
		if wait > 0 {
			return Session{}, &ThrottledError{RetryAfter: wait}
		}
	}
	sess, err := transaction(ctx, s, func(tx pgx.Tx) (Session, error) {
		// Lock order user -> token -> session, the same as DisableAccount (user
		// FOR UPDATE -> sessions), so a concurrent disable and exchange
		// serialize: either the disable sees and revokes the new session, or
		// the exchange sees the disabled user and creates nothing.
		var user string
		if err := tx.QueryRow(ctx, "SELECT user_id::text FROM auth_launch_tokens WHERE token_sha256=$1", tokenSHA256).Scan(&user); err != nil {
			return Session{}, err
		}
		var enabled bool
		if err := tx.QueryRow(ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1 FOR SHARE", user).Scan(&enabled); err != nil {
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
		sess, err := insertSession(ctx, tx, user, sessionTokenSHA256, ttl)
		if err != nil {
			return Session{}, err
		}
		e := auditEntry{kind: ActorUser, user: user, action: AuditLoginSucceeded}.target("user", user).with(loginDetail(MethodLaunchLink, client))
		return sess, writeAudit(ctx, tx, e)
	})
	if !errors.Is(err, domain.ErrNotFound) {
		return sess, err
	}
	// A bad, used or expired token, or a disabled user: count it against the
	// address and audit it, then report the same ErrNotFound.
	_, aerr := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if client != "" {
			keys, err := throttleKeys(ctx, tx, "", client)
			if err != nil {
				return false, err
			}
			var now time.Time
			if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
				return false, err
			}
			if err := countFailures(ctx, tx, keys, now); err != nil {
				return false, err
			}
		}
		return true, writeAudit(ctx, tx, auditEntry{kind: ActorAnonymous, action: AuditLoginFailed}.with(loginDetail(MethodLaunchLink, client)))
	})
	if aerr != nil {
		return Session{}, aerr
	}
	if client != "" {
		s.reapThrottle(ctx)
	}
	return Session{}, domain.ErrNotFound
}

// SessionByTokenHash returns the live session for a token hash with the
// user's current account fields. Expiry uses the database clock. Revoked,
// expired or disabled-user sessions yield ErrNotFound.
func (s *Store) SessionByTokenHash(ctx context.Context, tokenSHA256 string) (Session, error) {
	if !hexDigest.MatchString(tokenSHA256) {
		return Session{}, domain.ErrNotFound
	}
	sess, err := scanSession(s.pool.QueryRow(ctx, sessionSelect+
		" WHERE s.token_sha256=$1 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() AND u.disabled_at IS NULL", tokenSHA256))
	return sess, classify(err)
}

// RevokeSession ends a session (logout). Revoking an already revoked session
// succeeds without a second audit row.
func (s *Store) RevokeSession(ctx context.Context, id string) error {
	if !validIDs(id) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		var user string
		err := tx.QueryRow(ctx, "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE id=$1 AND revoked_at IS NULL RETURNING user_id::text", id).Scan(&user)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, writeAudit(ctx, tx, userAudit(ctx, user, AuditLogout).target("session", id))
	})
	return err
}
