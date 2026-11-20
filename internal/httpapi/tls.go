package httpapi

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/hostaddr"
)

// Listening rules (docs/SECURITY.md#네트워크와-브라우저):
//
//   - Host: the Host of a configured public origin, or on the listen port
//     a loopback name (127.0.0.1, localhost, [::1], the listen address).
//     A non-loopback listener also accepts every name of this machine (the
//     hostname forms, any loopback address, any address of its interfaces;
//     hostaddr.Machine); a loopback one does not, since nobody else can
//     reach it and a page resolving such a name to 127.0.0.1 must not get
//     in. Anything else is 403 HOST_REJECTED, with the accepted addresses
//     and the command that adds the one used. This keeps DNS rebinding out
//     (a foreign name resolving to this machine) without making people use
//     one particular address;
//   - Origin of a state-changing request: scheme://Host of that request;
//   - loopback without TLS: plain HTTP, cookie codeye_session;
//   - with TLS (for any non-loopback address unless plain HTTP is chosen):
//     HTTPS only on the listener, cookie __Host-codeye_session with Secure,
//     HSTS;
//   - plain HTTP on a non-loopback address (the operator's explicit choice
//     for a trusted network): cookie codeye_session without Secure (it
//     travels unencrypted);
//   - the optional local listener: loopback plain HTTP with loopback Hosts
//     only, for the agent and commands on the controller's machine.
//
// Plain HTTP behind a TLS-terminating proxy (trusting forwarded headers) is
// not supported.

// ErrTLSRequired is returned for a non-loopback listen address without TLS
// or the plain-HTTP choice.
var ErrTLSRequired = errors.New("listening on a non-loopback address needs TLS (--tls-cert, --tls-key and --public-origin https://HOST[:PORT]) or --plain-http")

// scheme is the public face's URL scheme.
func (s *Server) scheme() string {
	if s.pub.tls != nil {
		return "https"
	}
	return "http"
}

// splitHost splits a Host header into its host part and port ("" when
// absent). ok is false for anything that is not host[:port] or
// [IPv6][:port].
func splitHost(h string) (host, port string, ok bool) {
	if h == "" || len(h) > 300 {
		return "", "", false
	}
	if strings.HasPrefix(h, "[") {
		end := strings.Index(h, "]")
		if end < 0 {
			return "", "", false
		}
		host, rest := h[:end+1], h[end+1:]
		if _, err := netip.ParseAddr(host[1:end]); err != nil {
			return "", "", false
		}
		if rest == "" {
			return host, "", true
		}
		if !strings.HasPrefix(rest, ":") {
			return "", "", false
		}
		return host, rest[1:], validPort(rest[1:])
	}
	if strings.Count(h, ":") > 1 {
		return "", "", false
	}
	host, port, found := strings.Cut(h, ":")
	if host == "" || (found && !validPort(port)) {
		return "", "", false
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
			return "", "", false
		}
	}
	return host, port, true
}

func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == p
}

// accepts reports whether the Host header h reaches this face.
func (f *face) accepts(h string) bool {
	h = strings.ToLower(h)
	if f.hosts[h] {
		return true
	}
	if !f.ownNames || f.machine == nil {
		return false
	}
	host, port, ok := splitHost(h)
	if !ok {
		return false
	}
	if port == "" {
		port = "80"
		if f.tls != nil {
			port = "443"
		}
	}
	return port == strconv.Itoa(int(f.port)) && f.machine.Own(host)
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

// parseOrigin checks an origin of scheme (hostaddr.ParseOrigin).
func parseOrigin(s, scheme string) (string, []string, error) { return hostaddr.ParseOrigin(s, scheme) }

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

// requestOrigin is the Origin a state-changing request must carry: this
// request's own scheme://Host (the Host was accepted before).
func (s *Server) requestOrigin(r *http.Request) string {
	scheme := "http"
	if s.faceOf(r).tls != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(r.Host)
}

// loginOrigin is the origin of login links the server issues: the address
// the administrator is browsing from, so a link made through the VPN
// address is a VPN link. Browsing from this machine's loopback, the link
// is for someone else, so it gets the address to share (Origin) instead.
func (s *Server) loginOrigin(r *http.Request) string {
	host, _, _ := splitHost(strings.ToLower(r.Host))
	if s.faceOf(r) == s.pub && (host == "localhost" || isLoopbackIP(host)) {
		if o := s.Origin(); o != "" {
			return o
		}
	}
	return s.requestOrigin(r)
}

func isLoopbackIP(host string) bool {
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err == nil && ip.Unmap().IsLoopback()
}

// guardListener wraps ln in TLS when configured; a plain listener must be
// loopback unless plain HTTP was chosen.
func (s *Server) guardListener(ln net.Listener) (net.Listener, error) {
	if s.pub.tls != nil {
		return tls.NewListener(ln, s.pub.tls), nil
	}
	if s.pub.plain {
		return ln, nil
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return nil, ErrTLSRequired
	}
	return ln, nil
}

// Addresses are the ways to reach the public face: the configured origins,
// then this machine's loopback, name, LAN and VPN addresses on the listen
// port (container bridges left out).
func (s *Server) Addresses() []hostaddr.Address {
	return s.pub.machine.Addresses(s.scheme(), s.listen, s.origins)
}

// Secure reports whether the server serves HTTPS (TLS configured).
func (s *Server) Secure() bool { return s.pub.tls != nil }

// Origin is the address to share: the first configured origin, else the
// first LAN, VPN or name address of a service other machines reach (never
// 0.0.0.0), or the listen address itself; "" for a loopback service that
// chose neither TLS nor plain HTTP for others.
func (s *Server) Origin() string {
	if s.pub.tls == nil && !s.pub.plain {
		return ""
	}
	return hostaddr.Primary(s.Addresses())
}
