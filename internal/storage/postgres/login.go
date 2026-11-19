package postgres

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Login throttling (docs/SECURITY.md#로그인과-제한). Counters live in
// auth_throttle so every controller process sees the same state. Three keys
// count one failed attempt each:
//
//   - pair: the submitted username (whether or not it exists) together with
//     the connection address (IPv6 by /64). 5 failures lock that pair for
//     30s, doubling with each further failure up to 15 minutes. This is the
//     lock a guesser meets; it never blocks the same name from another
//     address or another name from the same address.
//   - client: the address alone. 100 failures (over any names) lock the
//     address for 15 minutes after the last one.
//   - account: the username alone. 50 failures (from any addresses) lock the
//     name everywhere for 15 minutes after the last one. A client with no
//     failures of its own meets this lock only at that count.
//
// A lock is derived from (failures, last_failure_at); nothing stores an end
// time, so giving a count back can never leave a stale lock. A row whose last
// failure is older than 15 minutes counts as zero and is reaped. An attempt
// is counted before the password is derived (parallel guesses all count); a
// success deletes its pair and gives the one count back on client and
// account.
const (
	pairThreshold    = 5
	clientThreshold  = 100
	accountThreshold = 50
	throttleBase     = 30 * time.Second
	throttleMax      = 15 * time.Minute
	throttleDecay    = 15 * time.Minute
	reapBatch        = 100
)

// Throttle scopes (auth_throttle.scope). Rows of one transaction are always
// locked in this order: account, client, pair.
const (
	scopeAccount = "account"
	scopeClient  = "client"
	scopePair    = "pair"
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
}

// lockFor is how long a key stays locked after its last failure, given the
// failures counted within the decay window (0: not locked).
func (k throttleKey) lockFor(failures int) time.Duration {
	switch k.scope {
	case scopePair:
		if failures >= pairThreshold {
			return min(throttleBase<<min(failures-pairThreshold, 10), throttleMax)
		}
	case scopeClient:
		if failures >= clientThreshold {
			return throttleMax
		}
	case scopeAccount:
		if failures >= accountThreshold {
			return throttleMax
		}
	}
	return 0
}

type throttleState struct {
	throttleKey
	failures int
	last     time.Time
	audited  bool
}

// lockedUntil is the end of the key's current lock (zero when unlocked).
func (st throttleState) lockedUntil() time.Time {
	d := st.lockFor(st.failures)
	if d == 0 {
		return time.Time{}
	}
	return st.last.Add(d)
}

// throttleKeys derives the row keys with the installation's HMAC key, so
// neither a submitted username (which may be a mistyped password) nor an
// address is stored as text or as a plain hash. Keys come back in lock
// order (account, client, pair).
func throttleKeys(ctx context.Context, tx pgx.Tx, username, client string) ([]throttleKey, error) {
	var secret []byte
	if err := tx.QueryRow(ctx, "SELECT throttle_key FROM auth_secrets WHERE id=1").Scan(&secret); err != nil {
		return nil, err
	}
	mac := func(scope, v string) throttleKey {
		h := hmac.New(sha256.New, secret)
		h.Write([]byte(scope + "\x00" + v))
		return throttleKey{scope, hex.EncodeToString(h.Sum(nil))}
	}
	name := strings.ToLower(username)
	var keys []throttleKey
	if username != "" {
		keys = append(keys, mac(scopeAccount, name))
	}
	if client != "" {
		keys = append(keys, mac(scopeClient, client))
	}
	if username != "" && client != "" {
		keys = append(keys, mac(scopePair, name+"\x00"+client))
	}
	return keys, nil
}

// lockThrottle locks the existing rows of keys (in the given order) and
// returns their state with the database time. A key without a row, or with
// only decayed failures, has zero failures. No row is created here, so a
// refused request leaves nothing behind.
func lockThrottle(ctx context.Context, tx pgx.Tx, keys []throttleKey) ([]throttleState, time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return nil, now, err
	}
	out := make([]throttleState, 0, len(keys))
	for _, k := range keys {
		st := throttleState{throttleKey: k}
		err := tx.QueryRow(ctx, "SELECT failures,last_failure_at,lock_audited FROM auth_throttle WHERE scope=$1 AND key_hmac=$2 FOR UPDATE",
			k.scope, k.hmac).Scan(&st.failures, &st.last, &st.audited)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, now, err
		}
		if err == nil && now.Sub(st.last) >= throttleDecay {
			st.failures = 0
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
		until := st.lockedUntil()
		if !until.After(now) {
			continue
		}
		if d := until.Sub(now); d > wait {
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

// countFailures adds one failure to every key, one upsert per key in the
// keys' order (a count whose last failure is older than the decay window
// restarts at 1).
func countFailures(ctx context.Context, tx pgx.Tx, keys []throttleKey, now time.Time) error {
	for _, k := range keys {
		if _, err := tx.Exec(ctx, `INSERT INTO auth_throttle(scope,key_hmac,failures,last_failure_at) VALUES($1,$2,1,$3)
			ON CONFLICT(scope,key_hmac) DO UPDATE SET
				failures=CASE WHEN auth_throttle.last_failure_at <= $3::timestamptz-$4*interval '1 millisecond' THEN 1 ELSE auth_throttle.failures+1 END,
				last_failure_at=$3, lock_audited=false`, k.scope, k.hmac, now, throttleDecay.Milliseconds()); err != nil {
			return err
		}
	}
	return nil
}

// giveBack undoes one counted attempt after a success: the pair row is
// deleted; the client and account rows lose one failure. They are not reset:
// one valid login must not clear an address that guesses at other names, or
// a name that other addresses guess at.
func giveBack(ctx context.Context, tx pgx.Tx, keys []throttleKey) error {
	for _, k := range keys {
		var err error
		if k.scope == scopePair {
			_, err = tx.Exec(ctx, "DELETE FROM auth_throttle WHERE scope=$1 AND key_hmac=$2", k.scope, k.hmac)
		} else {
			_, err = tx.Exec(ctx, "UPDATE auth_throttle SET failures=greatest(failures-1,0) WHERE scope=$1 AND key_hmac=$2", k.scope, k.hmac)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// reapThrottle deletes up to reapBatch rows whose last failure is older than
// the decay window (they count as zero). It is one statement of its own, run
// after a counted failure; rows another transaction holds are skipped, so
// several controllers can reap at once. A failed reap is left to the next.
func (s *Store) reapThrottle(ctx context.Context) {
	_, _ = s.pool.Exec(ctx, `DELETE FROM auth_throttle WHERE (scope,key_hmac) IN (
		SELECT scope,key_hmac FROM auth_throttle WHERE last_failure_at < clock_timestamp()-$1*interval '1 millisecond'
		ORDER BY last_failure_at LIMIT $2 FOR UPDATE SKIP LOCKED)`, throttleDecay.Milliseconds(), reapBatch)
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
// the login fails with ErrUnavailable. With every derivation slot busy for
// the whole wait the login is ErrUnavailable and nothing is counted.
func (s *Store) PasswordLogin(ctx context.Context, in PasswordLogin) (Session, error) {
	if !hexDigest.MatchString(in.SessionTokenSHA256) || in.TTL < time.Second || in.TTL > maxSessionTTL || in.Username == "" || len(in.Username) > 256 || len(in.Password) > 4*account.MaxPasswordLength {
		return Session{}, domain.ErrInvalid
	}
	release, err := s.acquireKDF(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
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
		if err := countFailures(ctx, tx, keys, now); err != nil {
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
	s.reapThrottle(ctx)
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
	// One slot covers verifying the current password and deriving the new one.
	release, err := s.acquireKDF(ctx)
	if err != nil {
		return Session{}, err
	}
	defer release()
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
		// The policy is checked before anything is counted: a rejected new
		// password says nothing about the current one and costs no attempt.
		if account.CheckPassword(in.New, c.username, c.display) != nil {
			return gate{}, domain.ErrInvalid
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
		return g, countFailures(ctx, tx, keys, now)
	})
	if err != nil {
		return Session{}, err
	}
	if g.wait > 0 {
		return Session{}, &ThrottledError{RetryAfter: g.wait}
	}
	if g.keys != nil {
		s.reapThrottle(ctx)
	}
	if g.cred.hash != nil && !g.cred.hash.Verify(in.Current) {
		e := userAudit(ctx, in.UserID, AuditPasswordChangeFailed).target("user", in.UserID).with(loginDetail(MethodPasswordChange, in.Client))
		if err := s.recordAudit(ctx, e); err != nil {
			return Session{}, err
		}
		return Session{}, domain.ErrUnauthenticated
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
		if err := retireLaunchTokens(ctx, tx, in.UserID); err != nil {
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

// retireLaunchTokens ends every unused login link of user (marked used): a
// password change, set or reset must not leave an older link that still
// signs in. Call it with the user row locked and before issuing a new link.
func retireLaunchTokens(ctx context.Context, tx pgx.Tx, user string) error {
	_, err := tx.Exec(ctx, "UPDATE auth_launch_tokens SET used_at=clock_timestamp() WHERE user_id=$1 AND used_at IS NULL AND expires_at>clock_timestamp()", user)
	return err
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

// defaultKDFWait is how long a login or password change waits for a free
// derivation slot before it is refused as unavailable.
const defaultKDFWait = 3 * time.Second

// acquireKDF takes one of the process's derivation slots (runtime.NumCPU()
// by default) for a whole verify-and-derive step. When no slot frees within
// the wait the request fails with ErrUnavailable (HTTP 503) before anything
// is counted or audited, so a flood of password attempts queues on the CPUs
// instead of starving every other request.
func (s *Store) acquireKDF(ctx context.Context) (func(), error) {
	s.kdfMu.Lock()
	if s.kdf == nil {
		s.kdf, s.kdfWait = make(chan struct{}, runtime.NumCPU()), defaultKDFWait
	}
	slots, wait := s.kdf, s.kdfWait
	s.kdfMu.Unlock()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-timer.C:
		return nil, domain.ErrUnavailable
	case <-ctx.Done():
		return nil, domain.ErrUnavailable
	}
}

// SetKDFLimit sets the number of concurrent derivations and the wait for a
// slot (defaults runtime.NumCPU() and 3s). Slots held under an earlier limit
// are released to that limit.
func (s *Store) SetKDFLimit(slots int, wait time.Duration) {
	if slots < 1 || wait <= 0 {
		return
	}
	s.kdfMu.Lock()
	s.kdf, s.kdfWait = make(chan struct{}, slots), wait
	s.kdfMu.Unlock()
}
