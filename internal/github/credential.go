package github

import "unicode"

// CredentialSchema is the schema_version of a credential file.
const CredentialSchema = "0.1"

// Credential is the installation token and account for one Git host.
type Credential struct {
	SchemaVersion string `json:"schema_version"`
	Host          string `json:"host"`
	Token         string `json:"token"`
	// Source identifies the installation credential source.
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
