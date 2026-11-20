package github

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// PlatformCredentialFile is shared by the controller and its colocated agent.
func PlatformCredentialFile(stateDir string) string {
	if stateDir == "" {
		return ""
	}
	return filepath.Join(stateDir, "secrets", "github-platform.json")
}

// PlatformCredentials stores one explicitly configured credential per Git host.
// The whole file is replaced atomically on service start to drop stale entries.
type PlatformCredentials map[string]Credential

func ReadPlatformCredentials(path string) (PlatformCredentials, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	const maxSize = 32 << 10
	raw, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return nil, err
	}
	var creds PlatformCredentials
	if len(raw) > maxSize || json.Unmarshal(raw, &creds) != nil {
		return nil, errors.New("the platform GitHub credential file does not decode")
	}
	for host, cred := range creds {
		if domain.ValidateHost(host) != nil || host != strings.ToLower(host) || cred.Host != host || cred.SchemaVersion != CredentialSchema || !ValidToken(cred.Token) {
			return nil, errors.New("the platform GitHub credential file contains an invalid host or token")
		}
	}
	return creds, nil
}

func WritePlatformCredentials(path string, creds PlatformCredentials) error {
	raw, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return writeTokenFile(filepath.Dir(path), path, raw)
}

// HostAllowed applies the offline policy after the host has been approved
// by the provider registry. Offline, only explicitly configured internal
// hosts may be reached, even if the database contains other providers.
func HostAllowed(host string, offline bool, configured []string) bool {
	return domain.ValidateHost(host) == nil && (!offline || (slices.Contains(configured, host) && !PublicHost(host)))
}

func PublicHost(host string) bool {
	name, _, _ := strings.Cut(strings.ToLower(host), ":")
	return name == Host || strings.HasSuffix(name, "."+Host)
}

// ProviderAPI accepts the public service's known API origin or the HTTPS
// origin of the exact enterprise Git host, including its configured port.
func ProviderAPI(host, apiURL string) (string, error) {
	if domain.ValidateHost(host) != nil {
		return "", domain.ErrInvalid
	}
	apiHost := host
	if host == Host {
		apiHost = "api.github.com"
	}
	if apiURL == "" {
		if host == Host {
			return DefaultAPIURL, nil
		}
		return "https://" + apiHost + "/api/v3", nil
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, apiHost) || u.User != nil || u.Opaque != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(apiURL, " \t\r\n#?@") {
		return "", domain.ErrInvalid
	}
	return apiURL, nil
}

// Platform resolves clients using approved provider data and installation credentials.
type Platform struct {
	CredentialFile string
	CABundle       string
	Offline        bool
	Hosts          []string
	// PublicAPI allows the public API fixture in tests.
	PublicAPI string
}

// Client returns nil for an unavailable or incorrectly configured host.
// An approved host without a credential may still serve public repositories.
func (p Platform) Client(host, apiURL string) (*Client, string) {
	if !HostAllowed(host, p.Offline, p.Hosts) {
		return nil, ""
	}
	base, err := ProviderAPI(host, apiURL)
	if err != nil {
		return nil, ""
	}
	if host == Host && p.PublicAPI != "" && apiURL == "" {
		base = p.PublicAPI
	}
	creds, err := ReadPlatformCredentials(p.CredentialFile)
	if err != nil {
		return nil, ""
	}
	cred := creds[host]
	client, err := NewWithCA(base, cred.Token, p.CABundle)
	if err != nil {
		return nil, ""
	}
	return client, cred.Login
}

// NewWithCA adds the installation CA bundle to the system trust roots.
func NewWithCA(base, token, caBundle string) (*Client, error) {
	c := New(base, token)
	if caBundle == "" {
		return c, nil
	}
	pem, err := os.ReadFile(caBundle)
	if err != nil {
		return nil, err
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("the GitHub API CA bundle has no readable certificates")
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, IdleConnTimeout: 30 * time.Second}
	c.HTTP = &http.Client{Timeout: 30 * time.Second, Transport: transport}
	return c, nil
}
