package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Login throttling (docs/SECURITY.md#로그인과-제한). Counters live in
// auth_throttle so every controller process sees the same state:
//
//   - account: keyed by the submitted username (whether or not it exists),
//     5 failures within the decay window lock it;
//   - client: keyed by the connection address (IPv6 by /64), 20 failures
//     lock it.
//
// A lock lasts 30s, doubling with each further failure up to 15 minutes.
// Failures older than 15 minutes are forgotten. A password attempt is
// counted before the password is derived (so parallel guesses all count) and
// a success gives the count back.
const (
	accountThreshold = 5
	clientThreshold  = 20
	throttleBase     = 30 * time.Second
	throttleMax      = 15 * time.Minute
	throttleDecay    = 15 * time.Minute
)

// Login methods (audit detail "method").
const (
	MethodPassword       = "password"
	MethodLaunchLink     = "launch_link"
	MethodPasswordChange = "password_change"
)

// ThrottledError is a login refused because too many attempts failed. It is
// returned outside any transaction; the refusal was recorded.
type ThrottledError struct {
	RetryAfter time.Duration
}

func (e *ThrottledError) Error() string { return "login throttled" }

// RetryAfterSeconds rounds the wait up to whole seconds (at least 1).
func (e *ThrottledError) RetryAfterSeconds() int {
	return max(1, int(math.Ceil(e.RetryAfter.Seconds())))
}

type throttleKey struct {
	scope, hmac string
	threshold   int
}

type throttleState struct {
	throttleKey
	failures int
	last     *time.Time
	locked   *time.Time
	audited  bool
}

// throttleKeys derives the row keys with the installation's HMAC key, so
// neither a submitted username (which may be a mistyped password) nor an
// address is stored as text or as a plain hash.
func throttleKeys(ctx context.Context, tx pgx.Tx, username, client string) ([]throttleKey, error) {
	var secret []byte
	if err := tx.QueryRow(ctx, "SELECT throttle_key FROM auth_secrets WHERE id=1").Scan(&secret); err != nil {
		return nil, err
	}
	mac := func(scope, v string) string {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(scope + "\x00" + v))
		return hex.EncodeToString(h.Sum(nil))
	}
	var keys []throttleKey
	if username != "" {
		keys = append(keys, throttleKey{"account", mac("account", strings.ToLower(username)), accountThreshold})
	}
	if client != "" {
		keys = append(keys, throttleKey{"client", mac("client", client), clientThreshold})
	}
	return keys, nil
}

// lockThrottle locks the rows of keys (account before client, the only
// order used) and returns them with the database time.
func lockThrottle(ctx context.Context, tx pgx.Tx, keys []throttleKey) ([]throttleState, time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return nil, now, err
	}
	out := make([]throttleState, 0, len(keys))
	for _, k := range keys {
		if _, err := tx.Exec(ctx, "INSERT INTO auth_throttle(scope,key_hmac) VALUES($1,$2) ON CONFLICT DO NOTHING", k.scope, k.hmac); err != nil {
			return nil, now, err
		}
		st := throttleState{throttleKey: k}
		if err := tx.QueryRow(ctx, "SELECT failures,last_failure_at,locked_until,lock_audited FROM auth_throttle WHERE scope=$1 AND key_hmac=$2 FOR UPDATE",
			k.scope, k.hmac).Scan(&st.failures, &st.last, &st.locked, &st.audited); err != nil {
			return nil, now, err
		}
		out = append(out, st)
	}
	return out, now, nil
}

// refuseIfLocked returns the longest remaining lock. The first refusal of a
// lock is audited; later refusals of the same lock are not (bounded audit
// growth under a flood).
func refuseIfLocked(ctx context.Context, tx pgx.Tx, states []throttleState, now time.Time, method, client string) (time.Duration, error) {
	var wait time.Duration
	var first *throttleState
	for i := range states {
		st := &states[i]
		if st.locked == nil || !st.locked.After(now) {
			continue
		}
		if d := st.locked.Sub(now); d > wait {
			wait = d
		}
		if !st.audited && first == nil {
			first = st
		}
	}
	if wait == 0 || first == nil {
		return wait, nil
	}
	if _, err := tx.Exec(ctx, "UPDATE auth_throttle SET lock_audited=true WHERE scope=$1 AND key_hmac=$2", first.scope, first.hmac); err != nil {
		return 0, err
	}
	detail := map[string]any{"method": method, "scope": first.scope, "retry_after_seconds": int(math.Ceil(wait.Seconds()))}
	if client != "" {
		detail["client_address"] = client
	}
	return wait, writeAudit(ctx, tx, auditEntry{kind: ActorAnonymous, action: AuditLoginThrottled}.with(detail))
}

// countFailures adds one failure to every row and sets a lock where the
// threshold is reached.
func countFailures(ctx context.Context, tx pgx.Tx, states []throttleState, now time.Time) error {
	for _, st := range states {
		n := st.failures
		if st.last != nil && now.Sub(*st.last) > throttleDecay {
			n = 0
		}
		n++
		var locked *time.Time
		if n >= st.threshold {
			d := throttleBase << min(n-st.threshold, 10)
			until := now.Add(min(d, throttleMax))
			locked = &until
		}
		if _, err := tx.Exec(ctx, `UPDATE auth_throttle SET failures=$3,last_failure_at=$4,
			locked_until=CASE WHEN $5::timestamptz IS NULL THEN locked_until ELSE $5 END,
			lock_audited=CASE WHEN $5::timestamptz IS NULL THEN lock_audited ELSE false END
			WHERE scope=$1 AND key_hmac=$2`, st.scope, st.hmac, n, now, locked); err != nil {
			return err
		}
	}
	return nil
}

// giveBack undoes one counted attempt after a success: the account row is
// cleared; the client row loses one failure (it is not reset, so one valid
// account cannot clear an address that guesses at others).
func giveBack(ctx context.Context, tx pgx.Tx, keys []throttleKey) error {
	for _, k := range keys {
		var err error
		if k.scope == "account" {
			_, err = tx.Exec(ctx, "DELETE FROM auth_throttle WHERE scope=$1 AND key_hmac=$2", k.scope, k.hmac)
		} else {
			_, err = tx.Exec(ctx, `UPDATE auth_throttle SET failures=greatest(failures-1,0),
				locked_until=CASE WHEN failures-1 < $3 THEN NULL ELSE locked_until END WHERE scope=$1 AND key_hmac=$2`, k.scope, k.hmac, k.threshold)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// credential is a user's stored verifier as read before derivation.
type credential struct {
	userID   string
	enabled  bool
	hash     *account.Hash
	username string
	display  string
}

func readCredential(ctx context.Context, tx pgx.Tx, where string, arg any, lock string) (credential, error) {
	var c credential
	var kdf, username, display *string
	var iter *int
	var salt, key []byte
	err := tx.QueryRow(ctx, `SELECT id::text,disabled_at IS NULL,password_kdf,password_iterations,password_salt,password_hash,username,display_name
		FROM users WHERE `+where+` `+lock, arg).Scan(&c.userID, &c.enabled, &kdf, &iter, &salt, &key, &username, &display)
	if err != nil {
		return c, err
	}
	if kdf != nil && iter != nil {
		c.hash = &account.Hash{KDF: *kdf, Iterations: *iter, Salt: salt, Key: key}
	}
	if username != nil {
		c.username = *username
	}
	if display != nil {
		c.display = *display
	}
	return c, nil
}

func (c credential) sameHash(o credential) bool {
	if c.hash == nil || o.hash == nil {
		return false
	}
	return c.hash.Equal(*o.hash)
}

// PasswordLogin is a username/password login from a client address.
type PasswordLogin struct {
	Username, Password string
	// Client is the normalized connection address (IPv4, or IPv6 /64).
	Client             string
	SessionTokenSHA256 string
	TTL                time.Duration
}

// PasswordLogin verifies a username and password and creates a session.
// Every failure (unknown user, user without a password, disabled user,
// wrong password) is ErrUnauthenticated after one derivation with the
// current parameters; a throttled login is *ThrottledError before any
// derivation. Each outcome is audited; if the audit row cannot be written
// the login fails with ErrUnavailable.
func (s *Store) PasswordLogin(ctx context.Context, in PasswordLogin) (Session, error) {
	if !hexDigest.MatchString(in.SessionTokenSHA256) || in.TTL < time.Second || in.TTL > maxSessionTTL || in.Username == "" || len(in.Username) > 256 || len(in.Password) > 4*account.MaxPasswordLength {
		return Session{}, domain.ErrInvalid
	}
	name, nameErr := account.NormalizeUsername(in.Username)
	type gate struct {
		wait time.Duration
		keys []throttleKey
		cred *credential
	}
	g, err := transaction(ctx, s, func(tx pgx.Tx) (gate, error) {
		keys, err := throttleKeys(ctx, tx, in.Username, in.Client)
		if err != nil {
			return gate{}, err
		}
		states, now, err := lockThrottle(ctx, tx, keys)
		if err != nil {
			return gate{}, err
		}
		wait, err := refuseIfLocked(ctx, tx, states, now, MethodPassword, in.Client)
		if err != nil || wait > 0 {
			return gate{wait: wait}, err
		}
		if err := countFailures(ctx, tx, states, now); err != nil {
			return gate{}, err
		}
		g := gate{keys: keys}
		if nameErr == nil {
			c, err := readCredential(ctx, tx, "username=$1", name, "")
			switch {
			case err == nil:
				g.cred = &c
			case !errors.Is(err, pgx.ErrNoRows):
				return gate{}, err
			}
		}
		return g, nil
	})
	if err != nil {
		return Session{}, err
	}
	if g.wait > 0 {
		return Session{}, &ThrottledError{RetryAfter: g.wait}
	}
	// Derive outside any transaction. Unknown, password-less and disabled
	// users cost the same derivation as a wrong password.
	verifier := account.Dummy(s.kdfIterations())
	if g.cred != nil && g.cred.hash != nil {
		verifier = *g.cred.hash
	}
	ok := verifier.Verify(in.Password) && g.cred != nil && g.cred.hash != nil && g.cred.enabled
	var rehash *account.Hash
	if ok && g.cred.hash.Iterations < s.kdfIterations() {
		if h, err := account.Derive(in.Password, s.kdfIterations()); err == nil {
			rehash = &h
		}
	}
	fail := func() (Session, error) {
		e := auditEntry{kind: ActorAnonymous, action: AuditLoginFailed}.with(loginDetail(MethodPassword, in.Client))
		if g.cred != nil {
			e = e.target("user", g.cred.userID)
		}
		if err := s.recordAudit(ctx, e); err != nil {
			return Session{}, err
		}
		return Session{}, domain.ErrUnauthenticated
	}
	if !ok {
		return fail()
	}
	type result struct {
		sess   Session
		failed bool
	}
	out, err := transaction(ctx, s, func(tx pgx.Tx) (result, error) {
		// Lock order user -> session -> throttle rows. The verified hash must
		// still be the stored one: a password change or reset committed after
		// the derivation wins and this login fails.
		now, err := readCredential(ctx, tx, "id=$1", g.cred.userID, "FOR NO KEY UPDATE")
		if err != nil {
			return result{}, err
		}
		if !now.enabled || !now.sameHash(*g.cred) {
			return result{failed: true}, nil
		}
		if rehash != nil {
			if _, err := tx.Exec(ctx, `UPDATE users SET password_kdf=$2,password_iterations=$3,password_salt=$4,password_hash=$5 WHERE id=$1`,
				now.userID, rehash.KDF, rehash.Iterations, rehash.Salt, rehash.Key); err != nil {
				return result{}, err
			}
		}
		sess, err := insertSession(ctx, tx, now.userID, in.SessionTokenSHA256, in.TTL)
		if err != nil {
			return result{}, err
		}
		if err := giveBack(ctx, tx, g.keys); err != nil {
			return result{}, err
		}
		e := auditEntry{kind: ActorUser, user: now.userID, action: AuditLoginSucceeded}.target("user", now.userID).with(loginDetail(MethodPassword, in.Client))
		return result{sess: sess}, writeAudit(ctx, tx, e)
	})
	if err != nil {
		return Session{}, err
	}
	if out.failed {
		return fail()
	}
	return out.sess, nil
}

func loginDetail(method, client string) map[string]any {
	d := map[string]any{"method": method}
	if client != "" {
		d["client_address"] = client
	}
	return d
}

// insertSession creates a session for user and reads it back with the user
// fields the API returns.
func insertSession(ctx context.Context, tx pgx.Tx, user, tokenSHA256 string, ttl time.Duration) (Session, error) {
	id := domain.NewID()
	if _, err := tx.Exec(ctx, `INSERT INTO auth_sessions(id,user_id,token_sha256,expires_at)
		VALUES($1,$2,$3,clock_timestamp()+$4*interval '1 millisecond')`, id, user, tokenSHA256, ttl.Milliseconds()); err != nil {
		return Session{}, err
	}
	return scanSession(tx.QueryRow(ctx, sessionSelect+" WHERE s.id=$1", id))
}

// PasswordChange is a signed-in user's change of their own password.
type PasswordChange struct {
	SessionID, UserID string
	// Current is required when the user has a password; a user without one
	// (a login-link user, or one whose password an administrator reset) sets
	// the first password with the session alone.
	Current, New string
	Client       string
}

// ChangePassword sets the user's password, clears must_change_password and
// revokes every other session of the user. A wrong current password is
// counted like a failed login on the user's account and address, and is
// audited; the new password must pass account.CheckPassword (ErrInvalid).
func (s *Store) ChangePassword(ctx context.Context, in PasswordChange) (Session, error) {
	if !validIDs(in.SessionID, in.UserID) || len(in.Current) > 4*account.MaxPasswordLength {
		return Session{}, domain.ErrInvalid
	}
	type gate struct {
		wait time.Duration
		keys []throttleKey
		cred credential
	}
	g, err := transaction(ctx, s, func(tx pgx.Tx) (gate, error) {
		c, err := readCredential(ctx, tx, "id=$1", in.UserID, "")
		if err != nil {
			return gate{}, err
		}
		if !c.enabled || c.username == "" {
			// A user without a username cannot hold a password.
			return gate{cred: c}, domain.ErrConflict
		}
		g := gate{cred: c}
		if c.hash == nil {
			return g, nil
		}
		keys, err := throttleKeys(ctx, tx, c.username, in.Client)
		if err != nil {
			return gate{}, err
		}
		states, now, err := lockThrottle(ctx, tx, keys)
		if err != nil {
			return gate{}, err
		}
		wait, err := refuseIfLocked(ctx, tx, states, now, MethodPasswordChange, in.Client)
		if err != nil || wait > 0 {
			return gate{wait: wait}, err
		}
		g.keys = keys
		return g, countFailures(ctx, tx, states, now)
	})
	if err != nil {
		return Session{}, err
	}
	if g.wait > 0 {
		return Session{}, &ThrottledError{RetryAfter: g.wait}
	}
	if g.cred.hash != nil && !g.cred.hash.Verify(in.Current) {
		e := userAudit(ctx, in.UserID, AuditPasswordChangeFailed).target("user", in.UserID).with(loginDetail(MethodPasswordChange, in.Client))
		if err := s.recordAudit(ctx, e); err != nil {
			return Session{}, err
		}
		return Session{}, domain.ErrUnauthenticated
	}
	if err := account.CheckPassword(in.New, g.cred.username, g.cred.display); err != nil {
		return Session{}, domain.ErrInvalid
	}
	h, err := account.Derive(in.New, s.kdfIterations())
	if err != nil {
		return Session{}, domain.ErrUnavailable
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Session, error) {
		now, err := readCredential(ctx, tx, "id=$1", in.UserID, "FOR UPDATE")
		if err != nil {
			return Session{}, err
		}
		if !now.enabled || (g.cred.hash == nil) != (now.hash == nil) || (now.hash != nil && !now.sameHash(g.cred)) {
			// Changed (reset, disabled, another change) since verification.
			return Session{}, domain.ErrConflict
		}
		if err := storeHash(ctx, tx, in.UserID, &h, false); err != nil {
			return Session{}, err
		}
		tag, err := tx.Exec(ctx, "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND id<>$2 AND revoked_at IS NULL", in.UserID, in.SessionID)
		if err != nil {
			return Session{}, err
		}
		if err := giveBack(ctx, tx, g.keys); err != nil {
			return Session{}, err
		}
		sess, err := scanSession(tx.QueryRow(ctx, sessionSelect+" WHERE s.id=$1 AND s.user_id=$2 AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp()", in.SessionID, in.UserID))
		if err != nil {
			return Session{}, err
		}
		e := userAudit(ctx, in.UserID, AuditPasswordChanged).target("user", in.UserID).with(map[string]any{"sessions_revoked": tag.RowsAffected()})
		return sess, writeAudit(ctx, tx, e)
	})
}

// storeHash writes (or with h nil clears) the user's verifier.
func storeHash(ctx context.Context, tx pgx.Tx, user string, h *account.Hash, mustChange bool) error {
	if h == nil {
		_, err := tx.Exec(ctx, `UPDATE users SET password_kdf=NULL,password_iterations=NULL,password_salt=NULL,password_hash=NULL,password_set_at=NULL,
			must_change_password=$2 AND username IS NOT NULL WHERE id=$1`, user, mustChange)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE users SET password_kdf=$2,password_iterations=$3,password_salt=$4,password_hash=$5,password_set_at=clock_timestamp(),
		must_change_password=$6 WHERE id=$1`, user, h.KDF, h.Iterations, h.Salt, h.Key, mustChange)
	return err
}

// kdfIterations is the derivation cost for new hashes and the dummy.
func (s *Store) kdfIterations() int {
	if s.iterations >= account.MinIterations {
		return s.iterations
	}
	return account.DefaultIterations
}

// SetPasswordIterations sets the derivation cost for new and re-derived
// hashes (default account.DefaultIterations). Tests lower it; an operator
// raising it makes existing hashes re-derive at their next login.
func (s *Store) SetPasswordIterations(n int) {
	if n >= account.MinIterations {
		s.iterations = n
	}
}
