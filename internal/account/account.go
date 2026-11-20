// Package account holds the local-account rules: login identifiers, the
// password policy and the password key derivation. It has no storage and
// no clock; the store calls it outside database transactions so a slow
// derivation never holds a connection or a row lock.
package account

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// KDF is the only password key derivation in use: PBKDF2 with HMAC-SHA-256
// from the Go standard library.
const KDF = "pbkdf2-sha256"

const (
	// DefaultIterations follows the current OWASP Password Storage guidance
	// for PBKDF2-HMAC-SHA256 (600,000). Stored hashes keep their own count;
	// a successful login with a lower count re-derives with this one.
	DefaultIterations = 600_000
	// MinIterations is the lowest count the database accepts. Production
	// uses DefaultIterations; tests may lower it to keep runs short.
	MinIterations = 1000
	SaltBytes     = 16
	KeyBytes      = 32
	// MinPasswordLength and MaxPasswordLength count characters (runes).
	MinPasswordLength = 12
	MaxPasswordLength = 256
)

var (
	// ErrPasswordPolicy is a password the policy rejects (length, equal to
	// the username, one repeated character, invalid text).
	ErrPasswordPolicy = errors.New("account: password does not meet the policy")
	// ErrUsername is a username outside the allowed form.
	ErrUsername = errors.New("account: invalid username")
	// ErrEmail is an email address outside the supported form.
	ErrEmail = errors.New("account: invalid email")
)

var usernameForm = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)
var emailForm = regexp.MustCompile("^[a-z0-9!#$%&'*+/=?^_`{|}~-]+(\\.[a-z0-9!#$%&'*+/=?^_`{|}~-]+)*@[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$")

// NormalizeEmail trims surrounding whitespace and folds ASCII letters to
// lowercase. It accepts a single address with a DNS domain.
func NormalizeEmail(s string) (string, error) {
	n := strings.TrimSpace(s)
	if len(n) > 254 || strings.IndexByte(n, '@') > 64 {
		return "", ErrEmail
	}
	for i := 0; i < len(n); i++ {
		if n[i] >= utf8.RuneSelf {
			return "", ErrEmail
		}
	}
	n = strings.ToLower(n)
	if !emailForm.MatchString(n) {
		return "", ErrEmail
	}
	return n, nil
}

// NormalizeUsername lower-cases ASCII letters and checks the stored form:
// 2..64 characters of a-z, 0-9, '.', '_' and '-', starting with a letter or
// digit. Usernames are therefore unique without regard to case.
func NormalizeUsername(s string) (string, error) {
	n := strings.ToLower(s)
	if !usernameForm.MatchString(n) {
		return "", ErrUsername
	}
	return n, nil
}

// CheckPassword applies the policy: 12..256 characters of valid UTF-8 without
// NUL, not equal to the login identifier or the display name (ignoring case), and
// not one character repeated. There are no composition rules.
func CheckPassword(password, identifier, displayName string) error {
	if !utf8.ValidString(password) || strings.ContainsRune(password, 0) {
		return ErrPasswordPolicy
	}
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength || n > MaxPasswordLength {
		return ErrPasswordPolicy
	}
	if identifier != "" && strings.EqualFold(password, identifier) {
		return ErrPasswordPolicy
	}
	if strings.TrimSpace(displayName) != "" && strings.EqualFold(password, strings.TrimSpace(displayName)) {
		return ErrPasswordPolicy
	}
	first, _ := utf8.DecodeRuneInString(password)
	if strings.Trim(password, string(first)) == "" {
		return ErrPasswordPolicy
	}
	return nil
}

// Hash is a stored password verifier with its parameters.
type Hash struct {
	KDF        string
	Iterations int
	Salt, Key  []byte
}

// Derive computes a new verifier with a fresh random salt.
func Derive(password string, iterations int) (Hash, error) {
	if iterations < MinIterations {
		return Hash{}, errors.New("account: iteration count below the minimum")
	}
	salt := make([]byte, SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return Hash{}, err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, KeyBytes)
	if err != nil {
		return Hash{}, err
	}
	return Hash{KDF: KDF, Iterations: iterations, Salt: salt, Key: key}, nil
}

// Verify derives with h's parameters and compares in constant time. An
// unusable verifier (unknown KDF, bad sizes) never matches but still costs
// one derivation with the default count, so the caller's timing does not
// depend on what was stored.
func (h Hash) Verify(password string) bool {
	usable := h.KDF == KDF && h.Iterations >= MinIterations && len(h.Salt) >= SaltBytes && len(h.Key) == KeyBytes
	if !usable {
		Dummy(DefaultIterations).Verify(password)
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, h.Salt, h.Iterations, KeyBytes)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, h.Key) == 1
}

// Dummy is a verifier no password matches. A login for an unknown user, a
// user without a password or a disabled user verifies against it (or
// against the real hash) so every failure costs one derivation with the
// current parameters.
func Dummy(iterations int) Hash {
	salt := sha256.Sum256([]byte("codeye-dummy-salt"))
	return Hash{KDF: KDF, Iterations: iterations, Salt: salt[:SaltBytes], Key: make([]byte, KeyBytes)}
}

// Equal reports whether two stored verifiers are the same record (used to
// detect a password change between verification and session creation).
func (h Hash) Equal(o Hash) bool {
	return h.KDF == o.KDF && h.Iterations == o.Iterations && hmac.Equal(h.Salt, o.Salt) && hmac.Equal(h.Key, o.Key)
}
