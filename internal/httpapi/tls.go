package httpapi

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
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
//   - with TLS (for any non-loopback address unless plain HTTP is chosen):
//     HTTPS only on the listener, Host must be the public origin's host,
//     Origin must equal the public origin, cookie __Host-codeye_session with
//     Secure, HSTS;
//   - plain HTTP on a non-loopback address (the operator's explicit choice
//     for a trusted network): Host is the public origin's host or, for an
//     unspecified or loopback bind, a loopback name; Origin "http://"+Host;
//     cookie codeye_session without Secure (it travels unencrypted);
//   - the optional local listener: loopback plain HTTP as above, for the
//     agent and commands on the controller's machine.
//
// Plain HTTP behind a TLS-terminating proxy (trusting forwarded headers) is
// not supported.

// ErrTLSRequired is returned for a non-loopback listen address without TLS
// or the plain-HTTP choice.
var ErrTLSRequired = errors.New("listening on a non-loopback address needs TLS (--tls-cert, --tls-key and --public-origin https://HOST[:PORT]) or --plain-http")

// plainFace is the face of a plain-HTTP listener on listen. origin
// ("http://HOST[:PORT]") is required for an unspecified address and
// defaults to http://listen otherwise.
func plainFace(listen netip.AddrPort, origin string) (*face, error) {
	f := &face{cookieName: sessionCookie, hosts: map[string]bool{}}
	addr := listen.Addr()
	if addr.IsUnspecified() || addr.IsLoopback() {
		f = loopbackFace(listen.Port())
	}
	if origin == "" {
		if addr.IsUnspecified() {
			return nil, errors.New("plain HTTP on an unspecified address (0.0.0.0, ::) needs --public-origin http://HOST:PORT, the address other machines use")
		}
		origin = "http://" + listen.String()
	}
	canon, hosts, err := parseOrigin(origin, "http")
	if err != nil {
		return nil, err
	}
	f.origin = canon
	for _, h := range hosts {
		f.hosts[h] = true
	}
	return f, nil
}

// ParseHTTPOrigin is ParsePublicOrigin for a plain-HTTP origin.
func ParseHTTPOrigin(s string) (string, []string, error) { return parseOrigin(s, "http") }

// hstsValue is the Strict-Transport-Security header sent over TLS.
const hstsValue = "max-age=31536000"

// ParsePublicOrigin checks an https origin without path, query, fragment or
// credentials and returns its canonical form ("https://host[:port]", the
// port omitted when 443) and the Host header values that address it.
func ParsePublicOrigin(s string) (origin string, hosts []string, err error) {
	return parseOrigin(s, "https")
}

// parseOrigin checks an origin of scheme (https: default port 443, http: 80).
func parseOrigin(s, scheme string) (origin string, hosts []string, err error) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != scheme || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" {
		return "", nil, fmt.Errorf("public origin must be %s://HOST[:PORT] without a path", scheme)
	}
	def := "443"
	if scheme == "http" {
		def = "80"
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
	if port == "" || port == def {
		return scheme + "://" + hostport(""), []string{hostport(""), hostport(def)}, nil
	}
	return scheme + "://" + hostport(port), []string{hostport(port)}, nil
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
	if f := s.faceOf(r); f.tls != nil {
		return f.origin
	}
	return "http://" + r.Host
}

// loginOrigin is the origin of login links the server issues: the public
// origin when one is configured (TLS or plain HTTP), so a link handed to
// another person works from their machine, else this request's origin.
func (s *Server) loginOrigin(r *http.Request) string {
	if s.pub.origin != "" {
		return s.pub.origin
	}
	return s.requestOrigin(r)
}

// guardListener wraps ln in TLS when configured; a plain listener must be
// loopback unless plain HTTP was chosen.
func (s *Server) guardListener(ln net.Listener) (net.Listener, error) {
	if s.pub.tls != nil {
		return tls.NewListener(ln, s.pub.tls), nil
	}
	if s.pub.origin != "" {
		return ln, nil
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return nil, ErrTLSRequired
	}
	return ln, nil
}

// Secure reports whether the server serves HTTPS (TLS configured).
func (s *Server) Secure() bool { return s.pub.tls != nil }

// Origin is the public origin (TLS or plain HTTP), "" for loopback only.
func (s *Server) Origin() string { return s.pub.origin }
