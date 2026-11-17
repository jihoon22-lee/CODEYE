// Package postgres implements control-plane persistence. Workers never receive a Store.
package postgres

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

type Store struct{ pool *pgxpool.Pool }

// Open requires an explicit URL and never applies migrations.
func Open(ctx context.Context, dsn string) (*Store, error) {
	config, err := connectionConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, classify(err)
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, classify(err)
	}
	return &Store{pool: pool}, nil
}
func (s *Store) Close() { s.pool.Close() }

func connectionConfig(dsn string) (*pgxpool.Config, error) {
	// Fail closed rather than silently inheriting a service, credentials, or options.
	connectionVariables := map[string]bool{}
	for _, key := range strings.Fields("PGHOST PGPORT PGDATABASE PGUSER PGPASSWORD PGPASSFILE PGAPPNAME PGCONNECT_TIMEOUT PGSSLMODE PGSSLKEY PGSSLCERT PGSSLCERTMODE PGSSLSNI PGSSLROOTCERT PGSSLPASSWORD PGSSLNEGOTIATION PGTARGETSESSIONATTRS PGSERVICE PGSERVICEFILE PGTZ PGOPTIONS PGMINPROTOCOLVERSION PGMAXPROTOCOLVERSION PGCHANNELBINDING PGREQUIREAUTH") {
		connectionVariables[key] = true
	}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if connectionVariables[strings.ToUpper(key)] {
			return nil, domain.ErrInvalid
		}
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), ",/\\") || u.User == nil || u.User.Username() == "" || len(u.Path) < 2 || u.Fragment != "" {
		return nil, domain.ErrInvalid
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, domain.ErrInvalid
	}
	for k, v := range q {
		if len(v) != 1 || (k != "sslmode" && k != "sslrootcert") {
			return nil, domain.ErrInvalid
		}
	}
	mode := q.Get("sslmode")
	if mode == "" {
		mode = "verify-full"
	}
	if mode != "verify-full" && !(mode == "disable" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")) {
		return nil, domain.ErrInvalid
	}
	q.Set("sslmode", mode)
	q.Set("passfile", os.DevNull)
	q.Set("sslcert", "")
	q.Set("sslkey", "")
	if !q.Has("sslrootcert") {
		q.Set("sslrootcert", "")
	}
	password, _ := u.User.Password()
	q.Set("password", password)
	q.Set("connect_timeout", "5")
	if u.Port() == "" {
		u.Host = u.Host + ":5432"
	}
	u.RawQuery = q.Encode()
	config, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		return nil, domain.ErrInvalid
	}
	config.MaxConns = 4
	config.ConnConfig.RuntimeParams = map[string]string{"application_name": "codeye", "timezone": "UTC", "search_path": "public", "lock_timeout": "5000", "statement_timeout": "15000"}
	return config, nil
}

// Public errors deliberately omit SQL text, detail, paths, and credentials.
func classify(err error) error {
	if err == nil {
		return nil
	}
	for _, e := range []error{domain.ErrUnsupported, domain.ErrInvalid, domain.ErrForbidden, domain.ErrNotFound, domain.ErrConflict, domain.ErrStaleAttempt, domain.ErrUnavailable, domain.ErrMigration, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, e) {
			return e
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23505", "40001", "40P01", "CY001", "CY002", "CY003", "CY004":
			return domain.ErrConflict
		case "23503", "23514", "23502":
			return domain.ErrInvalid
		}
		// Class 22 is a data exception (bad text, NUL escape, numeric overflow, ...):
		// the caller's value is at fault, not the service.
		if strings.HasPrefix(pgerr.Code, "22") {
			return domain.ErrInvalid
		}
	}
	return domain.ErrUnavailable
}
func transaction[T any](ctx context.Context, s *Store, fn func(pgx.Tx) (T, error)) (out T, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, classify(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	out, err = fn(tx)
	if err != nil {
		return out, classify(err)
	}
	if err = tx.Commit(ctx); err != nil {
		var zero T
		return zero, classify(err)
	}
	return out, nil
}
func validIDs(ids ...string) bool {
	for _, id := range ids {
		if !domain.ValidID(id) {
			return false
		}
	}
	return true
}
