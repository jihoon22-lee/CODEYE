package httpapi

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Listening rules (docs/SECURITY.md#네트워크와-브라우저):
//
//   - loopback without TLS: plain HTTP, Host allowlist of loopback names,
//     Origin "http://"+Host, cookie codeye_session;
//   - with TLS (required for any non-loopback address): HTTPS only on the
//     listener, Host must be the public origin's host, Origin must equal the
//     public origin, cookie __Host-codeye_session with Secure, HSTS.
//
// Plain HTTP behind a TLS-terminating proxy (trusting forwarded headers) is
// not supported.

// ErrTLSRequired is returned for a non-loopback listen address without TLS.
var ErrTLSRequired = errors.New("listening on a non-loopback address requires TLS: give --tls-cert, --tls-key and --public-origin https://HOST[:PORT]")

// hstsValue is the Strict-Transport-Security header sent over TLS.
const hstsValue = "max-age=31536000"

// ParsePublicOrigin checks an https origin without path, query, fragment or
// credentials and returns its canonical form ("https://host[:port]", the
// port omitted when 443) and the Host header values that address it.
func ParsePublicOrigin(s string) (origin string, hosts []string, err error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" {
		return "", nil, errors.New("public origin must be https://HOST[:PORT] without a path")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", nil, errors.New("public origin port must be 1..65535")
		}
	}
	hostport := func(p string) string {
		if p == "" {
			if strings.Contains(host, ":") {
				return "[" + host + "]"
			}
			return host
		}
		return net.JoinHostPort(host, p)
	}
	if port == "" || port == "443" {
		return "https://" + hostport(""), []string{hostport(""), hostport("443")}, nil
	}
	return "https://" + hostport(port), []string{hostport(port)}, nil
}

// loadTLS reads the certificate chain and key. The key file must not be
// readable or writable by group or others (POSIX; Windows ACLs are not
// checked).
func loadTLS(certFile, keyFile string) (*tls.Config, error) {
	st, err := os.Stat(keyFile)
	if err != nil {
		return nil, errors.New("TLS key file cannot be read")
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("TLS key file is not a regular file")
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("TLS key file mode %04o is accessible by group or others; use 0600 or 0400", st.Mode().Perm())
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		// The parse error can quote file content; keep it out of the message.
		return nil, errors.New("TLS certificate or key cannot be loaded (PEM certificate chain and matching private key expected)")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}, nil
}

// requestOrigin is the Origin a state-changing request must carry.
func (s *Server) requestOrigin(r *http.Request) string {
	if s.tls != nil {
		return s.origin
	}
	return "http://" + r.Host
}

// loginOrigin is the origin of login links the server issues (the public
// origin with TLS, else this request's loopback origin).
func (s *Server) loginOrigin(r *http.Request) string {
	return s.requestOrigin(r)
}

// guardListener wraps ln in TLS when configured; without TLS it refuses a
// listener that is not bound to a loopback address.
func (s *Server) guardListener(ln net.Listener) (net.Listener, error) {
	if s.tls != nil {
		return tls.NewListener(ln, s.tls), nil
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return nil, ErrTLSRequired
	}
	return ln, nil
}

// Secure reports whether the server serves HTTPS (TLS configured).
func (s *Server) Secure() bool { return s.tls != nil }

// Origin is the public origin with TLS, "" otherwise.
func (s *Server) Origin() string { return s.origin }
