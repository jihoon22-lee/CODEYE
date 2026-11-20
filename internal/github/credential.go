package github

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"unicode"
)

// CredentialSchema is the schema_version of a credential file.
const CredentialSchema = "0.1"

// Credential is one user's stored token (UserTokens, 0600) and what is
// known about it. The controller reads it for the API calls made for that
// user and the agent's worker for that user's Git fetches of Host.
type Credential struct {
	SchemaVersion string `json:"schema_version"`
	Host          string `json:"host"`
	Token         string `json:"token"`
	// Source is where the token came from: GH_TOKEN, GITHUB_TOKEN, gh or web.
	Source string `json:"source"`
	// Login is the token's account; "" when GitHub could not be asked.
	Login string `json:"login,omitempty"`
}

// ValidToken reports a single-line printable token of a sane length.
func ValidToken(t string) bool {
	if len(t) < 8 || len(t) > 512 {
		return false
	}
	for _, r := range t {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) || r == ' ' {
			return false
		}
	}
	return true
}

// ReadCredential reads a credential file; nil without error when there is
// none.
func ReadCredential(path string) (*Credential, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 8<<10 {
		return nil, errors.New("the GitHub credential file is too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var c Credential
	if dec.Decode(&c) != nil || c.SchemaVersion != CredentialSchema || !ValidToken(c.Token) || c.Host == "" {
		return nil, errors.New("the GitHub credential file does not decode")
	}
	return &c, nil
}
