package httpapi

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// writeCert writes a self-signed certificate for host (and 127.0.0.1) and
// its key with mode keyMode; it returns the paths and a pool trusting it.
func writeCert(t *testing.T, host string, keyMode os.FileMode) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), keyMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyFile, keyMode); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	cert, _ := x509.ParseCertificate(der)
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

// TC-146
func TestListenRules(t *testing.T) {
	for _, bad := range []string{"127.0.0.1:0", "localhost:7331", "codeye.example:443", ":7331"} {
		if _, err := ParseListen(bad); err == nil {
			t.Errorf("ParseListen(%q) accepted", bad)
		}
	}
	for _, ok := range []string{"127.0.0.1:7331", "[::1]:7331", "0.0.0.0:443", "[::]:8443", "192.168.1.2:7331"} {
		if _, err := ParseListen(ok); err != nil {
			t.Errorf("ParseListen(%q): %v", ok, err)
		}
	}
	// Without TLS only loopback is served.
	for _, addr := range []string{"0.0.0.0:7331", "192.168.1.2:7331", "[::]:7331"} {
		if _, err := New(Config{Listen: netip.MustParseAddrPort(addr)}, &postgres.Store{}); !errors.Is(err, ErrTLSRequired) {
			t.Fatalf("New(%s) without TLS: %v", addr, err)
		}
	}
	cert, key, _ := writeCert(t, "codeye.test", 0o600)
	all := Config{Listen: netip.MustParseAddrPort("0.0.0.0:8443"), TLSCertFile: cert, TLSKeyFile: key, PublicOrigin: "https://codeye.test:8443"}
	if _, err := New(all, &postgres.Store{}); err != nil {
		t.Fatalf("TLS config refused: %v", err)
	}
	for name, mutate := range map[string]func(*Config){
		"no key":            func(c *Config) { c.TLSKeyFile = "" },
		"no cert":           func(c *Config) { c.TLSCertFile = "" },
		"no origin":         func(c *Config) { c.PublicOrigin = "" },
		"http origin":       func(c *Config) { c.PublicOrigin = "http://codeye.test:8443" },
		"origin with path":  func(c *Config) { c.PublicOrigin = "https://codeye.test/app" },
		"origin with user":  func(c *Config) { c.PublicOrigin = "https://u@codeye.test" },
		"missing key file":  func(c *Config) { c.TLSKeyFile = filepath.Join(t.TempDir(), "none.pem") },
		"cert is not a key": func(c *Config) { c.TLSKeyFile = c.TLSCertFile },
	} {
		c := all
		mutate(&c)
		if name == "cert is not a key" && runtime.GOOS != "windows" {
			_ = os.Chmod(c.TLSKeyFile, 0o600)
		}
		if _, err := New(c, &postgres.Store{}); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if runtime.GOOS != "windows" {
		_, loose, _ := writeCert(t, "codeye.test", 0o640)
		c := all
		c.TLSKeyFile = loose
		if _, err := New(c, &postgres.Store{}); err == nil || !strings.Contains(err.Error(), "group or others") {
			t.Fatalf("group-readable key: %v", err)
		}
	}
	for in, want := range map[string]string{"https://Codeye.Test": "https://codeye.test", "https://codeye.test:443": "https://codeye.test", "https://codeye.test:8443/": "https://codeye.test:8443"} {
		if got, _, err := ParsePublicOrigin(in); err != nil || got != want {
			t.Fatalf("%s: %s %v", in, got, err)
		}
	}
}

// TestServeRefusesPlainNonLoopbackListener: even a caller that hands Serve a
// listener on a non-loopback address gets no plain HTTP served.
// TC-146
func TestServeRefusesPlainNonLoopbackListener(t *testing.T) {
	srv, err := New(Config{Listen: mustLoopback(t), SweepInterval: -1, ReconcileInterval: -1, PublishInterval: -1}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind a wildcard address here: %v", err)
	}
	defer ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, ln); !errors.Is(err, ErrTLSRequired) {
		t.Fatalf("Serve on a wildcard listener without TLS: %v", err)
	}
}

// Plain HTTP for other machines, chosen explicitly: the public origin's Host
// and Origin are accepted and foreign ones refused, the cookie is not
// Secure, and login links use the public origin. The loopback listener for
// the local agent and commands accepts loopback Hosts only, and a session
// made there is not taken on the public face (another cookie name is not
// involved: the Host check refuses the request first).
func TestPlainHTTPServiceAndLocalListener(t *testing.T) {
	e := newEnv(t)
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind a wildcard address here: %v", err)
	}
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port, lport := ln.Addr().(*net.TCPAddr).Port, local.Addr().(*net.TCPAddr).Port
	origin := "http://codeye.lan:" + itoa(port)
	if _, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), PlainHTTP: true}, e.store); err == nil {
		t.Fatal("plain HTTP on 0.0.0.0 without a public origin accepted")
	}
	if _, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), PlainHTTP: true, PublicOrigin: "https://codeye.lan"}, e.store); err == nil {
		t.Fatal("plain HTTP with an https origin accepted")
	}
	srv, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), PlainHTTP: true, PublicOrigin: origin, LocalListen: netip.MustParseAddrPort(local.Addr().String()),
		Logger: slog.New(slog.DiscardHandler), SweepInterval: -1, ReconcileInterval: -1, PublishInterval: -1}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Secure() || srv.Origin() != origin {
		t.Fatalf("secure %v origin %s", srv.Secure(), srv.Origin())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	go func() { done <- srv.Serve(ctx, ln) }()
	go func() { done <- srv.ServeLocal(ctx, local) }()
	t.Cleanup(func() { cancel(); <-done; <-done })
	do := func(dial, method, path, host, originHeader, cookie, csrf string, body any) (*http.Response, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rd = strings.NewReader(string(raw))
		}
		req, _ := http.NewRequest(method, "http://"+dial+path, rd)
		req.Host = host
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if originHeader != "" {
			req.Header.Set("Origin", originHeader)
		}
		if cookie != "" {
			req.Header.Set("Cookie", sessionCookie+"="+cookie)
		}
		if csrf != "" {
			req.Header.Set(csrfHeader, csrf)
		}
		res, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res, out
	}
	pubDial, localDial := "127.0.0.1:"+itoa(port), "127.0.0.1:"+itoa(lport)
	publicHost := "codeye.lan:" + itoa(port)
	if res, _ := do(pubDial, "GET", "/api/v1/health", publicHost, "", "", "", nil); res.StatusCode != 200 || res.Header.Get("Strict-Transport-Security") != "" {
		t.Fatalf("health over plain HTTP: %d", res.StatusCode)
	}
	if res, _ := do(pubDial, "GET", "/api/v1/health", "evil.example:"+itoa(port), "", "", "", nil); res.StatusCode != 403 {
		t.Fatalf("foreign Host answered %d", res.StatusCode)
	}
	admin := e.newAdminAccount("plain-admin")
	login := func(dial, host, originHeader string) (*http.Response, map[string]any) {
		token := NewToken()
		if err := e.store.IssueLaunchToken(context.Background(), admin, TokenHash(token), time.Minute); err != nil {
			t.Fatal(err)
		}
		return do(dial, "POST", "/api/v1/session", host, originHeader, "", "", map[string]string{"launch_token": token})
	}
	if res, _ := login(pubDial, publicHost, "http://evil.example"); res.StatusCode != 403 {
		t.Fatalf("foreign Origin accepted: %d", res.StatusCode)
	}
	res, body := login(pubDial, publicHost, origin)
	set := strings.Join(res.Header.Values("Set-Cookie"), "; ")
	if res.StatusCode != 201 || !strings.HasPrefix(set, sessionCookie+"=") || strings.Contains(set, "Secure") || !strings.Contains(set, "HttpOnly") {
		t.Fatalf("session over plain HTTP: %d %s", res.StatusCode, set)
	}
	cookie, csrf := strings.TrimPrefix(strings.SplitN(set, ";", 2)[0], sessionCookie+"="), body["csrf_token"].(string)
	res, body = do(pubDial, "POST", "/api/v1/admin/users", publicHost, origin, cookie, csrf, map[string]any{"username": "lan-user"})
	if link, _ := body["login_link"].(map[string]any); res.StatusCode != 201 || link == nil || !strings.HasPrefix(link["url"].(string), origin+"/#launch=") {
		t.Fatalf("login link over plain HTTP: %d %v", res.StatusCode, body)
	}
	// The loopback listener: loopback Hosts only.
	localHost := "127.0.0.1:" + itoa(lport)
	if res, _ := do(localDial, "GET", "/api/v1/health", localHost, "", "", "", nil); res.StatusCode != 200 {
		t.Fatalf("local health %d", res.StatusCode)
	}
	if res, _ := do(localDial, "GET", "/api/v1/session", publicHost, "", cookie, "", nil); res.StatusCode != 403 {
		t.Fatalf("public Host on the local listener answered %d", res.StatusCode)
	}
	if res, _ := do(localDial, "GET", "/api/v1/session", localHost, "", cookie, "", nil); res.StatusCode != 200 {
		t.Fatalf("session on the local listener: %d", res.StatusCode)
	}
	if res, _ := login(localDial, localHost, "http://"+localHost); res.StatusCode != 201 {
		t.Fatalf("sign-in on the local listener: %d", res.StatusCode)
	}
}

// TC-146
func TestTLSServerOnWildcardAddress(t *testing.T) {
	e := newEnv(t)
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind a wildcard address here: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	host := "codeye.test"
	origin := "https://" + host + ":" + itoa(port)
	cert, key, pool := writeCert(t, host, 0o600)
	srv, err := New(Config{Listen: netip.MustParseAddrPort(ln.Addr().String()), TLSCertFile: cert, TLSKeyFile: key, PublicOrigin: origin,
		Logger: slog.New(slog.DiscardHandler), SweepInterval: -1, ReconcileInterval: -1, PublishInterval: -1}, e.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); <-done })
	addr := "127.0.0.1:" + itoa(port)

	// Plain HTTP on the TLS port serves nothing of the API.
	if conn, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = io.WriteString(conn, "GET /api/v1/health HTTP/1.1\r\nHost: "+host+":"+itoa(port)+"\r\n\r\n")
		res, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err == nil {
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode == 200 || strings.Contains(string(body), `"status"`) {
				t.Fatalf("plain HTTP answered: %d %s", res.StatusCode, body)
			}
		}
		conn.Close()
	}

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: host},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, path, hostHeader, originHeader, cookie, csrf string, body any) (*http.Response, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rd = strings.NewReader(string(raw))
		}
		req, _ := http.NewRequest(method, origin+path, rd)
		req.Host = hostHeader
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if originHeader != "" {
			req.Header.Set("Origin", originHeader)
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		if csrf != "" {
			req.Header.Set(csrfHeader, csrf)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res, out
	}
	publicHost := host + ":" + itoa(port)
	res, _ := do("GET", "/api/v1/health", publicHost, "", "", "", nil)
	if res.StatusCode != 200 || res.Header.Get("Strict-Transport-Security") != hstsValue {
		t.Fatalf("health over TLS %d hsts %q", res.StatusCode, res.Header.Get("Strict-Transport-Security"))
	}
	for _, other := range []string{addr, "localhost:" + itoa(port), "evil.example:" + itoa(port)} {
		if res, _ := do("GET", "/api/v1/health", other, "", "", "", nil); res.StatusCode != 403 {
			t.Fatalf("Host %s answered %d", other, res.StatusCode)
		}
	}

	admin := e.newAdminAccount("tls-admin")
	token := NewToken()
	if err := e.store.IssueLaunchToken(context.Background(), admin, TokenHash(token), time.Minute); err != nil {
		t.Fatal(err)
	}
	if res, _ := do("POST", "/api/v1/session", publicHost, "http://"+publicHost, "", "", map[string]string{"launch_token": token}); res.StatusCode != 403 {
		t.Fatalf("http Origin accepted: %d", res.StatusCode)
	}
	res, body := do("POST", "/api/v1/session", publicHost, origin, "", "", map[string]string{"launch_token": token})
	if res.StatusCode != 201 {
		t.Fatalf("session over TLS: %d %v", res.StatusCode, body)
	}
	set := strings.Join(res.Header.Values("Set-Cookie"), "; ")
	if !strings.HasPrefix(set, secureSessionCookie+"=") || !strings.Contains(set, "Secure") || !strings.Contains(set, "HttpOnly") || !strings.Contains(set, "Path=/") || strings.Contains(set, "Domain") {
		t.Fatalf("cookie %s", set)
	}
	value := strings.TrimPrefix(strings.SplitN(set, ";", 2)[0], secureSessionCookie+"=")
	csrf := body["csrf_token"].(string)
	if res, _ := do("GET", "/api/v1/session", publicHost, "", sessionCookie+"="+value, "", nil); res.StatusCode != 401 {
		t.Fatalf("plain cookie name accepted over TLS: %d", res.StatusCode)
	}
	if res, _ := do("GET", "/api/v1/session", publicHost, "", secureSessionCookie+"="+value, "", nil); res.StatusCode != 200 {
		t.Fatalf("__Host- cookie: %d", res.StatusCode)
	}
	res, body = do("POST", "/api/v1/admin/users", publicHost, origin, secureSessionCookie+"="+value, csrf, map[string]any{"username": "remote-user"})
	link, _ := body["login_link"].(map[string]any)
	if res.StatusCode != 201 || link == nil || !strings.HasPrefix(link["url"].(string), origin+"/#launch=") {
		t.Fatalf("login link over TLS: %d %v", res.StatusCode, body)
	}
}
