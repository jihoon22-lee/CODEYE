// Package domain defines CODEYE's storage- and transport-independent contracts:
// error classes, run/task state machines, roles, identifiers and canonical
// encodings. It depends only on the standard library.
package domain

import (
	"errors"
	"fmt"
)

// Sentinel error classes. Callers wrap them (fmt.Errorf("%w: ...")) and
// transports map them with errors.Is.
var (
	ErrInvalid      = errors.New("invalid input")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrStaleAttempt = errors.New("stale attempt")
	ErrUnavailable  = errors.New("unavailable")
	ErrMigration    = errors.New("migration failed")
	// ErrUnauthenticated marks a credential that is unknown, revoked, used or
	// expired (node secrets and enrollment tokens).
	ErrUnauthenticated = errors.New("unauthenticated")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
