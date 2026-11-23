package executor

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCertPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestCheckCABundle(t *testing.T) {
	dir := t.TempDir()
	cert := testCertPEM(t)
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if n, err := CheckCABundle(write("two.pem", append(append([]byte{}, cert...), cert...))); err != nil || n != 2 {
		t.Fatalf("two certificates: %d, %v", n, err)
	}
	for name, p := range map[string]string{
		"missing":     filepath.Join(dir, "none.pem"),
		"directory":   dir,
		"not PEM":     write("text.pem", []byte("not a certificate\n")),
		"private key": write("key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1}})),
		"broken cert": write("bad.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1, 2, 3}})),
	} {
		if _, err := CheckCABundle(p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCAEnvNamesOneBundleForEveryTool(t *testing.T) {
	if len(CAEnv("")) != 0 || AddCAEnv(nil, "") != nil {
		t.Fatal("no bundle, no variables")
	}
	env := AddCAEnv(map[string]string{"PATH": "/bin"}, "/etc/codeye/ca.pem")
	for _, k := range []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "GIT_SSL_CAINFO", "PIP_CERT", "CARGO_HTTP_CAINFO"} {
		if env[k] != "/etc/codeye/ca.pem" {
			t.Errorf("%s = %q", k, env[k])
		}
	}
	if _, err := validateEnv(env); err != nil {
		t.Fatalf("the CA variables must pass the credential filter: %v", err)
	}
}

// OpenCode's start-up npm lookup goes to a closed loopback port whatever the
// provider.
func TestManagedOpenCodePinsTheNpmRegistry(t *testing.T) {
	for _, p := range []*ModelProvider{nil, {ID: "openai", APIKey: "k", BaseURL: "https://llm.internal/v1"}} {
		env := managedOpenCodeEnv("openai/gpt-6.2-sol", p, 0)
		if env["npm_config_registry"] != OpenCodeNpmRegistry {
			t.Fatalf("npm_config_registry = %q", env["npm_config_registry"])
		}
	}
}
