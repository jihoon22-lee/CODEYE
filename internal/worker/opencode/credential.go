package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// ReasonCredentialUnavailable: the credential file is missing, unreadable,
// readable by others, malformed or has no API key for the model's provider.
const ReasonCredentialUnavailable = "model_credential_unavailable"

// CredentialSchema is the schema_version of a CODEYE model credential.
const CredentialSchema = "0.1"

// Credential is CODEYE's model credential file (personal mode writes it to
// <state>/secrets/model.json, mode 0600). Only the worker's model call reads
// the key; it never travels in argv, the hello or a log.
type Credential struct {
	SchemaVersion string `json:"schema_version"`
	// Provider is the OpenCode provider ID (openai, anthropic, opencode, ...).
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
	// BaseURL overrides the provider endpoint (an OpenAI-compatible server,
	// a proxy, a test double).
	BaseURL string `json:"base_url,omitempty"`
	// OpenAICompatible sends requests with OpenCode's OpenAI-compatible
	// (chat completions) provider package instead of the provider's own.
	OpenAICompatible bool `json:"openai_compatible,omitempty"`
}

const maxCredentialBytes = 64 << 10

var errCredential = errors.New("model credential unavailable")

// ReadCredential reads path for provider. The file is either a CODEYE
// Credential or an OpenCode auth.json, whose entry for provider must be of
// type "api". It must be a regular file without group or other permissions
// (except on Windows).
func ReadCredential(path, provider string) (*executor.ModelProvider, error) {
	b, err := readPrivate(path, maxCredentialBytes)
	if err != nil {
		return nil, errCredential
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(b, &probe) != nil {
		return nil, errCredential
	}
	if _, ok := probe["api_key"]; ok {
		var c Credential
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if dec.Decode(&c) != nil || c.SchemaVersion != CredentialSchema || c.Provider != provider {
			return nil, errCredential
		}
		return checked(&executor.ModelProvider{ID: provider, APIKey: c.APIKey, BaseURL: c.BaseURL, OpenAICompatible: c.OpenAICompatible})
	}
	var entry struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if raw, ok := probe[provider]; !ok || json.Unmarshal(raw, &entry) != nil || entry.Type != "api" {
		return nil, errCredential
	}
	return checked(&executor.ModelProvider{ID: provider, APIKey: entry.Key})
}

// checked applies the executor's provider rules with a model name that
// always passes, so a malformed key or endpoint is a credential error.
func checked(p *executor.ModelProvider) (*executor.ModelProvider, error) {
	if !executor.ValidProvider(p) {
		return nil, errCredential
	}
	return p, nil
}

// readPrivate reads a small regular file that only its owner can read.
func readPrivate(path string, limit int64) ([]byte, error) {
	bad := errors.New("unreadable private file")
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0) {
		return nil, bad
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, bad
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil || !os.SameFile(st, current) {
		return nil, bad
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, bad
	}
	return b, nil
}
