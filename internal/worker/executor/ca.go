package executor

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
)

// CAEnv is the environment that makes the tools CODEYE runs against a
// registry, a Git host or a model endpoint trust the PEM file bundle
// (docs/OPERATIONS.md#사내-ca): the go command and OpenSSL-based tools
// (SSL_CERT_FILE, which replaces their system store), Node and therefore
// npm, pnpm, yarn and OpenCode (NODE_EXTRA_CA_CERTS, added to the built-in
// store), git (GIT_SSL_CAINFO, also for a GnuTLS build), pip (PIP_CERT) and
// cargo (CARGO_HTTP_CAINFO). uv reads SSL_CERT_FILE. Every variable names
// the same file, so the bundle must hold the system CAs as well as the
// internal one. Verification is never turned off.
func CAEnv(bundle string) map[string]string {
	if bundle == "" {
		return map[string]string{}
	}
	return map[string]string{
		"SSL_CERT_FILE": bundle, "NODE_EXTRA_CA_CERTS": bundle, "GIT_SSL_CAINFO": bundle,
		"PIP_CERT": bundle, "CARGO_HTTP_CAINFO": bundle,
	}
}

// AddCAEnv adds CAEnv(bundle) to env (a nil env is left nil when bundle is
// empty) and returns env.
func AddCAEnv(env map[string]string, bundle string) map[string]string {
	if bundle == "" {
		return env
	}
	if env == nil {
		env = map[string]string{}
	}
	for k, v := range CAEnv(bundle) {
		env[k] = v
	}
	return env
}

// maxCABundleBytes bounds a CA bundle (a system store is about 200 KiB).
const maxCABundleBytes = 8 << 20

// CheckCABundle reports how many certificates the PEM file at path holds;
// an error when it is not a readable regular file of PEM certificates.
func CheckCABundle(path string) (int, error) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return 0, errors.New("the CA bundle is not a readable regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, errors.New("the CA bundle is not readable")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxCABundleBytes+1))
	if err != nil || len(raw) > maxCABundleBytes {
		return 0, errors.New("the CA bundle is not readable or larger than 8 MiB")
	}
	n := 0
	for rest := bytes.TrimSpace(raw); len(rest) > 0; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return 0, errors.New("the CA bundle holds a certificate that does not parse")
		}
		n++
	}
	if n == 0 {
		return 0, errors.New("the CA bundle holds no PEM certificate")
	}
	return n, nil
}
