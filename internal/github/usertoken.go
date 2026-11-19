package github

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// TokenFromWeb is Credential.Source of a token the user pasted in the web
// account screen. The discovery sources (GH_TOKEN, GITHUB_TOKEN, gh) are
// written by `codeye up` for the administrator who runs it.
const TokenFromWeb = "web"

// UserTokens is the directory of per-user GitHub tokens
// (<state>/secrets/github-users, 0700): <user_id>.json, 0600, readable by
// the service account only. The controller reads a user's file for the
// GitHub API calls of that user's projects and the agent's worker for their
// Git fetches; nothing else sees a token, and the API never returns one.
type UserTokens string

// UserTokensDir is the directory under a state directory.
func UserTokensDir(stateDir string) UserTokens {
	if stateDir == "" {
		return ""
	}
	return UserTokens(filepath.Join(stateDir, "secrets", "github-users"))
}

// Path is user's token file ("" for an invalid ID or no directory).
func (d UserTokens) Path(user string) string {
	if d == "" || !domain.ValidID(user) {
		return ""
	}
	return filepath.Join(string(d), user+".json")
}

// Read is user's stored credential; nil without error when there is none.
func (d UserTokens) Read(user string) (*Credential, error) {
	p := d.Path(user)
	if p == "" {
		return nil, nil
	}
	return ReadCredential(p)
}

// Write stores c as user's credential (0600, in a 0700 directory),
// replacing the previous one atomically.
func (d UserTokens) Write(user string, c Credential) error {
	p := d.Path(user)
	if p == "" {
		return errors.New("no token directory")
	}
	if !ValidToken(c.Token) || c.Host == "" {
		return errors.New("invalid token")
	}
	c.SchemaVersion = CredentialSchema
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		return errors.New("cannot create the token directory")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(string(d), 0o700); err != nil {
			return errors.New("cannot restrict the token directory")
		}
	}
	tmp, err := os.CreateTemp(string(d), ".token-*")
	if err != nil {
		return errors.New("cannot write the token")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return errors.New("cannot restrict the token file")
		}
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return errors.New("cannot write the token")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("cannot write the token")
	}
	if err := os.Rename(name, p); err != nil {
		return errors.New("cannot write the token")
	}
	return nil
}

// Remove deletes user's credential; a missing one is no error.
func (d UserTokens) Remove(user string) error {
	p := d.Path(user)
	if p == "" {
		return nil
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove the token")
	}
	return nil
}
