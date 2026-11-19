package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// User is an account as the administrator API and CLI show it. A user
// without Username signs in only with a login link.
type User struct {
	ID                 string
	Username           *string
	DisplayName        *string
	IsAdmin            bool
	MustChangePassword bool
	HasPassword        bool
	DisabledAt         *time.Time
	CreatedAt          time.Time
	// LastLoginAt is the newest session creation (any method).
	LastLoginAt *time.Time
}

const userSelect = `SELECT u.id::text,u.username,u.display_name,u.is_admin,u.must_change_password,u.password_kdf IS NOT NULL,u.disabled_at,u.created_at,
	(SELECT max(created_at) FROM auth_sessions WHERE user_id=u.id) FROM users u`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.IsAdmin, &u.MustChangePassword, &u.HasPassword, &u.DisabledAt, &u.CreatedAt, &u.LastLoginAt)
	u.CreatedAt = u.CreatedAt.UTC()
	if u.DisabledAt != nil {
		t := u.DisabledAt.UTC()
		u.DisabledAt = &t
	}
	if u.LastLoginAt != nil {
		t := u.LastLoginAt.UTC()
		u.LastLoginAt = &t
	}
	return u, err
}

func readUser(ctx context.Context, tx pgx.Tx, id string) (User, error) {
	return scanUser(tx.QueryRow(ctx, userSelect+" WHERE u.id=$1", id))
}

// requireAdmin checks that by is the operator or an enabled administrator,
// holding the administrator's row FOR SHARE until the transaction ends (a
// concurrent demotion waits). A non-administrator gets ErrForbidden.
func requireAdmin(ctx context.Context, tx pgx.Tx, by Principal) error {
	if !by.valid() {
		return domain.ErrInvalid
	}
	if by.operator {
		return nil
	}
	var ok bool
	err := tx.QueryRow(ctx, "SELECT is_admin AND disabled_at IS NULL FROM users WHERE id=$1 FOR SHARE", by.user).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !ok) {
		return domain.ErrForbidden
	}
	return err
}

// lockAdmins locks every enabled administrator row FOR UPDATE in id order
// and checks by. It is the one serializing step of every change that can
// remove an administrator (disable, demote): two such changes cannot both
// see "another administrator remains". Lock order: administrator rows (id
// order) -> other user rows -> sessions.
func lockAdmins(ctx context.Context, tx pgx.Tx, by Principal) ([]string, error) {
	if !by.valid() {
		return nil, domain.ErrInvalid
	}
	rows, err := tx.Query(ctx, "SELECT id::text FROM users WHERE is_admin AND disabled_at IS NULL ORDER BY id FOR UPDATE")
	if err != nil {
		return nil, err
	}
	admins, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if !by.operator && !slices.Contains(admins, by.user) {
		return nil, domain.ErrForbidden
	}
	return admins, nil
}

// NewAccount is an account to create. Username empty creates a login-link
// user. Password, when set, is a verifier derived by the caller outside the
// transaction. LaunchTokenSHA256, when set, issues a login link (LaunchTTL)
// in the same transaction.
type NewAccount struct {
	Username, DisplayName string
	IsAdmin               bool
	Password              *account.Hash
	MustChangePassword    bool
	LaunchTokenSHA256     string
	LaunchTTL             time.Duration
}

// CreateAccount creates a user. by must be the operator or an enabled
// administrator. A taken username is ErrConflict.
func (s *Store) CreateAccount(ctx context.Context, by Principal, in NewAccount) (User, error) {
	var username, display any
	if in.Username != "" {
		n, err := account.NormalizeUsername(in.Username)
		if err != nil {
			return User{}, domain.ErrInvalid
		}
		username = n
	}
	if strings.TrimSpace(in.DisplayName) != "" {
		clean, ok := cleanLabel(in.DisplayName, 200)
		if !ok {
			return User{}, domain.ErrInvalid
		}
		display = clean
	}
	if username == nil && (in.Password != nil || in.MustChangePassword) {
		return User{}, domain.ErrInvalid
	}
	if in.LaunchTokenSHA256 != "" && (!hexDigest.MatchString(in.LaunchTokenSHA256) || in.LaunchTTL < time.Second || in.LaunchTTL > maxLaunchTTL) {
		return User{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (User, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return User{}, err
		}
		id := domain.NewID()
		if _, err := tx.Exec(ctx, "INSERT INTO users(id,username,display_name,is_admin) VALUES($1,$2,$3,$4)", id, username, display, in.IsAdmin); err != nil {
			return User{}, err
		}
		if in.Password != nil || in.MustChangePassword {
			if err := storeHash(ctx, tx, id, in.Password, in.MustChangePassword); err != nil {
				return User{}, err
			}
		}
		method := "login_link"
		if in.Password != nil {
			method = MethodPassword
		}
		detail := map[string]any{"is_admin": in.IsAdmin, "method": method, "must_change_password": in.MustChangePassword}
		if username != nil {
			detail["username"] = username
		}
		if err := writeAudit(ctx, tx, principalAudit(by, AuditUserCreated).target("user", id).with(detail)); err != nil {
			return User{}, err
		}
		if in.LaunchTokenSHA256 != "" {
			if err := insertLaunchToken(ctx, tx, id, in.LaunchTokenSHA256, in.LaunchTTL); err != nil {
				return User{}, err
			}
		}
		return readUser(ctx, tx, id)
	})
}

// DisableAccount permanently disables a user and revokes its sessions. The
// last enabled administrator cannot be disabled (ErrConflict). Disabling an
// already disabled user changes nothing and is not audited again.
func (s *Store) DisableAccount(ctx context.Context, by Principal, id string) (User, error) {
	if !validIDs(id) {
		return User{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (User, error) {
		admins, err := lockAdmins(ctx, tx, by)
		if err != nil {
			return User{}, err
		}
		var disabled *time.Time
		if err := tx.QueryRow(ctx, "SELECT disabled_at FROM users WHERE id=$1 FOR UPDATE", id).Scan(&disabled); err != nil {
			return User{}, err
		}
		if disabled == nil {
			if slices.Contains(admins, id) && len(admins) == 1 {
				return User{}, domain.ErrConflict
			}
			if _, err := tx.Exec(ctx, "UPDATE users SET disabled_at=clock_timestamp() WHERE id=$1", id); err != nil {
				return User{}, err
			}
		}
		tag, err := tx.Exec(ctx, "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", id)
		if err != nil {
			return User{}, err
		}
		if disabled == nil {
			e := principalAudit(by, AuditUserDisabled).target("user", id).with(map[string]any{"sessions_revoked": tag.RowsAffected()})
			if err := writeAudit(ctx, tx, e); err != nil {
				return User{}, err
			}
		}
		return readUser(ctx, tx, id)
	})
}

// SetAdmin grants or revokes the administrator role. A disabled user cannot
// be granted it and the last enabled administrator cannot lose it (both
// ErrConflict). The role is global account and access management; it does
// not open any project.
func (s *Store) SetAdmin(ctx context.Context, by Principal, id string, admin bool) (User, error) {
	if !validIDs(id) {
		return User{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (User, error) {
		admins, err := lockAdmins(ctx, tx, by)
		if err != nil {
			return User{}, err
		}
		var was, enabled bool
		if err := tx.QueryRow(ctx, "SELECT is_admin,disabled_at IS NULL FROM users WHERE id=$1 FOR UPDATE", id).Scan(&was, &enabled); err != nil {
			return User{}, err
		}
		if admin && !enabled {
			return User{}, domain.ErrConflict
		}
		if !admin && slices.Contains(admins, id) && len(admins) == 1 {
			return User{}, domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, "UPDATE users SET is_admin=$2 WHERE id=$1", id, admin); err != nil {
			return User{}, err
		}
		action := AuditAdminGranted
		if !admin {
			action = AuditAdminRevoked
		}
		e := principalAudit(by, action).target("user", id).with(map[string]any{"is_admin": admin, "previous_is_admin": was})
		if err := writeAudit(ctx, tx, e); err != nil {
			return User{}, err
		}
		return readUser(ctx, tx, id)
	})
}

// SetPassword is the operator's password set (CLI "user passwd"): the
// verifier is derived by the caller, every session of the user is revoked.
// The user must have a username.
func (s *Store) SetPassword(ctx context.Context, id string, h account.Hash, mustChange bool) (User, error) {
	if !validIDs(id) {
		return User{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (User, error) {
		var username *string
		var enabled bool
		if err := tx.QueryRow(ctx, "SELECT username,disabled_at IS NULL FROM users WHERE id=$1 FOR UPDATE", id).Scan(&username, &enabled); err != nil {
			return User{}, err
		}
		if username == nil || !enabled {
			return User{}, domain.ErrConflict
		}
		if err := storeHash(ctx, tx, id, &h, mustChange); err != nil {
			return User{}, err
		}
		if err := retireLaunchTokens(ctx, tx, id); err != nil {
			return User{}, err
		}
		tag, err := tx.Exec(ctx, "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", id)
		if err != nil {
			return User{}, err
		}
		e := principalAudit(OperatorPrincipal(), AuditPasswordSet).target("user", id).with(map[string]any{"must_change_password": mustChange, "sessions_revoked": tag.RowsAffected()})
		if err := writeAudit(ctx, tx, e); err != nil {
			return User{}, err
		}
		return readUser(ctx, tx, id)
	})
}

// ResetPassword clears the user's password, revokes every session and
// issues a single-use login link (tokenSHA256, ttl) in one transaction. A
// user with a username must then set a new password before doing anything
// else (must_change_password). An administrator resets others only; their
// own password changes through the session (ErrConflict).
func (s *Store) ResetPassword(ctx context.Context, by Principal, id, tokenSHA256 string, ttl time.Duration) (User, error) {
	if !validIDs(id) || !hexDigest.MatchString(tokenSHA256) || ttl < time.Second || ttl > maxLaunchTTL {
		return User{}, domain.ErrInvalid
	}
	if !by.operator && by.user == id {
		return User{}, domain.ErrConflict
	}
	return transaction(ctx, s, func(tx pgx.Tx) (User, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return User{}, err
		}
		var enabled bool
		if err := tx.QueryRow(ctx, "SELECT disabled_at IS NULL FROM users WHERE id=$1 FOR UPDATE", id).Scan(&enabled); err != nil {
			return User{}, err
		}
		if !enabled {
			return User{}, domain.ErrNotFound
		}
		if err := storeHash(ctx, tx, id, nil, true); err != nil {
			return User{}, err
		}
		tag, err := tx.Exec(ctx, "UPDATE auth_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL", id)
		if err != nil {
			return User{}, err
		}
		if err := retireLaunchTokens(ctx, tx, id); err != nil {
			return User{}, err
		}
		if err := insertLaunchToken(ctx, tx, id, tokenSHA256, ttl); err != nil {
			return User{}, err
		}
		e := principalAudit(by, AuditPasswordReset).target("user", id).with(map[string]any{"method": "login_link", "sessions_revoked": tag.RowsAffected()})
		if err := writeAudit(ctx, tx, e); err != nil {
			return User{}, err
		}
		return readUser(ctx, tx, id)
	})
}

// UserPage is one keyset page of users, by username then id.
type UserPage struct {
	Items      []User
	NextCursor string
}

// ListUsers is the administrator's user list (oldest first). cursor is the
// previous NextCursor (a user ID).
func (s *Store) ListUsers(ctx context.Context, by Principal, cursor string, limit int) (UserPage, error) {
	if limit < 1 || limit > 200 || (cursor != "" && !validIDs(cursor)) {
		return UserPage{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (UserPage, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return UserPage{}, err
		}
		var after any
		if cursor != "" {
			after = cursor
		}
		rows, err := tx.Query(ctx, userSelect+` WHERE $1::uuid IS NULL OR (u.created_at,u.id) > (SELECT created_at,id FROM users WHERE id=$1)
			ORDER BY u.created_at,u.id LIMIT $2`, after, limit+1)
		if err != nil {
			return UserPage{}, err
		}
		defer rows.Close()
		page := UserPage{Items: []User{}}
		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				return UserPage{}, err
			}
			if len(page.Items) == limit {
				page.NextCursor = page.Items[limit-1].ID
				break
			}
			page.Items = append(page.Items, u)
		}
		rows.Close()
		return page, rows.Err()
	})
}

// FindUser resolves a user ID or a username (operator CLI).
func (s *Store) FindUser(ctx context.Context, idOrUsername string) (User, error) {
	if validIDs(idOrUsername) {
		u, err := scanUser(s.pool.QueryRow(ctx, userSelect+" WHERE u.id=$1", idOrUsername))
		return u, classify(err)
	}
	name, err := account.NormalizeUsername(idOrUsername)
	if err != nil {
		return User{}, domain.ErrNotFound
	}
	u, err := scanUser(s.pool.QueryRow(ctx, userSelect+" WHERE u.username=$1", name))
	return u, classify(err)
}

// OperatorUsers lists users for the operator CLI (first page of up to limit).
func (s *Store) OperatorUsers(ctx context.Context, limit int) ([]User, error) {
	page, err := s.ListUsers(ctx, OperatorPrincipal(), "", limit)
	return page.Items, err
}
