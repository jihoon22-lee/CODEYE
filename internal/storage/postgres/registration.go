package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/account"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// ErrEmailTaken is a conflict on a normalized address.
var ErrEmailTaken = fmt.Errorf("%w: email address is already registered", domain.ErrConflict)

// Registration creates a regular account and its first browser session.
type Registration struct {
	Email, Password    string
	SessionTokenSHA256 string
	TTL                time.Duration
}

// RegisterAccount derives the verifier outside the transaction. The account,
// session and audit entry all commit together. The email constraint decides
// conflicts, including concurrent requests for the same normalized address.
func (s *Store) RegisterAccount(ctx context.Context, in Registration) (Session, error) {
	email, err := account.NormalizeEmail(in.Email)
	if err != nil || account.CheckPassword(in.Password, email, "") != nil ||
		!hexDigest.MatchString(in.SessionTokenSHA256) || in.TTL < time.Second || in.TTL > maxSessionTTL {
		return Session{}, domain.ErrInvalid
	}
	release, err := s.acquireKDF(ctx)
	if err != nil {
		return Session{}, err
	}
	hash, err := account.Derive(in.Password, s.kdfIterations())
	release()
	if err != nil {
		return Session{}, domain.ErrUnavailable
	}
	return transaction(ctx, s, func(tx pgx.Tx) (Session, error) {
		id := domain.NewID()
		tag, err := tx.Exec(ctx, "INSERT INTO users(id,email) VALUES($1,$2) ON CONFLICT ON CONSTRAINT users_email_unique DO NOTHING", id, email)
		if err != nil {
			return Session{}, err
		}
		if tag.RowsAffected() == 0 {
			return Session{}, ErrEmailTaken
		}
		if err := storeHash(ctx, tx, id, &hash, false); err != nil {
			return Session{}, err
		}
		sess, err := insertSession(ctx, tx, id, in.SessionTokenSHA256, in.TTL, nil, false)
		if err != nil {
			return Session{}, err
		}
		e := userAudit(ctx, id, AuditUserCreated).target("user", id).with(map[string]any{
			"is_admin": false, "method": MethodPassword, "self_registered": true,
		})
		return sess, writeAudit(ctx, tx, e)
	})
}
