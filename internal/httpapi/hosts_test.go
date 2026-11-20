package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/hostaddr"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// wslMachine is a fixed machine: loopback, eth0, a docker bridge and
// Tailscale, hostname Main under the tailnet's search domain.
func wslMachine() *hostaddr.Machine {
	a := func(s ...string) []netip.Addr {
		out := []netip.Addr{}
		for _, x := range s {
			out = append(out, netip.MustParseAddr(x))
		}
		return out
	}
	ifs := []hostaddr.Iface{
		{Name: "lo", Up: true, Loopback: true, Addrs: a("127.0.0.1", "::1")},
		{Name: "eth0", Up: true, Addrs: a("172.24.157.79", "fe80::215:5dff:fe42:3274")},
		{Name: "br-2d92d005db3e", Up: true, Addrs: a("172.20.0.1")},
		{Name: "tailscale0", Up: true, Addrs: a("100.108.34.23", "fd7a:115c:a1e0::6701:22d4")},
	}
	return hostaddr.New(hostaddr.Source{
		Interfaces:    func() ([]hostaddr.Iface, error) { return ifs, nil },
		Hostname:      func() (string, error) { return "Main", nil },
		SearchDomains: func() []string { return []string{"tail30f401.ts.net"} },
	})
}

// TC-264: a wildcard plain-HTTP service needs no public origin and answers
// on every name and address of this machine on its port, plus each
// configured origin; anything else (another port, a lookalike name, a
// foreign name) is refused.
func TestHostAcceptance(t *testing.T) {
	srv, err := New(Config{Listen: netip.MustParseAddrPort("0.0.0.0:7331"), PlainHTTP: true, Machine: wslMachine()}, &postgres.Store{})
	if err != nil {
		t.Fatalf("wildcard without an origin: %v", err)
	}
	if o := srv.Origin(); o != "http://172.24.157.79:7331" {
		t.Fatalf("address to share %s", o)
	}
	srv, err = New(Config{Listen: netip.MustParseAddrPort("0.0.0.0:7331"), PlainHTTP: true, Machine: wslMachine(),
		PublicOrigins: []string{"http://codeye.example:8080", "http://CODEYE.lan:7331/", "http://codeye.example:8080"}}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	if srv.Origin() != "http://codeye.example:8080" || len(srv.origins) != 2 {
		t.Fatalf("origins %v share %s", srv.origins, srv.Origin())
	}
	for _, h := range []string{"172.24.157.79:7331", "100.108.34.23:7331", "MAIN:7331", "main:7331", "main.local:7331", "main.tail30f401.ts.net:7331",
		"localhost:7331", "127.0.0.1:7331", "127.0.0.2:7331", "[::1]:7331", "[fd7a:115c:a1e0::6701:22d4]:7331", "172.20.0.1:7331",
		"codeye.example:8080", "codeye.lan:7331"} {
		if !srv.pub.accepts(h) {
			t.Errorf("%s refused", h)
		}
	}
	for _, h := range []string{"", "172.24.157.79", "172.24.157.79:7332", "172.24.157.79:07331", "main.evil.example:7331", "evil.example:7331", "codeye.example:7331",
		"codeye.example", "[::1]", "[main]:7331", "0.0.0.0:7331", "[::]:7331", "172.24.157.79:7331:1", "::1:7331", "a b:7331", "main:7331/x", "[fe80::1%eth0]:7331", "8.8.8.8:7331"} {
		if srv.pub.accepts(h) {
			t.Errorf("%q accepted", h)
		}
	}
	// The default port may be left out of Host.
	srv80, err := New(Config{Listen: netip.MustParseAddrPort("[::]:80"), PlainHTTP: true, Machine: wslMachine()}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"main", "main:80", "[fd7a:115c:a1e0::6701:22d4]", "100.108.34.23"} {
		if !srv80.pub.accepts(h) {
			t.Errorf("port 80: %s refused", h)
		}
	}
	if srv80.pub.accepts("main:443") {
		t.Error("port 80: another port accepted")
	}
	// A loopback listener keeps the strict rule: the loopback names, the
	// listen address and the configured origins, not this machine's other
	// names and addresses (nobody else reaches it; a page resolving such a
	// name to 127.0.0.1 must not get in).
	for _, cfg := range []Config{
		{Listen: netip.MustParseAddrPort("127.0.0.1:7331"), Machine: wslMachine()},
		{Listen: netip.MustParseAddrPort("127.0.0.1:7331"), PlainHTTP: true, Machine: wslMachine(), PublicOrigins: []string{"http://codeye.lan:7331"}},
		{Listen: netip.MustParseAddrPort("127.0.0.2:7331"), PlainHTTP: true, Machine: wslMachine(), PublicOrigins: []string{"http://codeye.lan:7331"}},
	} {
		lo, err := New(cfg, &postgres.Store{})
		if err != nil {
			t.Fatal(err)
		}
		accepted := []string{"127.0.0.1:7331", "localhost:7331", "LOCALHOST:7331", "[::1]:7331", lo.listen.String()}
		if len(cfg.PublicOrigins) > 0 {
			accepted = append(accepted, "codeye.lan:7331")
		}
		for _, h := range accepted {
			if !lo.pub.accepts(h) {
				t.Errorf("loopback %s: %s refused", cfg.Listen, h)
			}
		}
		refused := []string{"main:7331", "main.local:7331", "main.tail30f401.ts.net:7331", "172.24.157.79:7331", "100.108.34.23:7331", "127.0.0.3:7331", "10.255.255.254:7331", "localhost:7332"}
		if len(cfg.PublicOrigins) == 0 {
			refused = append(refused, "codeye.lan:7331")
		}
		for _, h := range refused {
			if lo.pub.accepts(h) {
				t.Errorf("loopback %s: %s accepted", cfg.Listen, h)
			}
		}
	}
	// The address list for people: configured first, then loopback, name,
	// LAN, VPN; no bridge.
	var got []string
	for _, a := range srv.Addresses() {
		got = append(got, a.Kind+" "+a.Origin)
	}
	want := "configured http://codeye.example:8080|configured http://codeye.lan:7331|this_machine http://127.0.0.1:7331|this_machine http://localhost:7331|name http://main:7331|lan http://172.24.157.79:7331|vpn http://100.108.34.23:7331"
	if strings.Join(got, "|") != want {
		t.Fatalf("addresses %v", got)
	}
}

// TC-264: a refused Host is told, in plain words, which address it used,
// which ones work and the command that adds it; a browser gets a page
// (escaped), the API the same as JSON details. A Host that does not read
// as host[:port] is not echoed.
func TestHostRejectionTellsWhatWorks(t *testing.T) {
	srv, err := New(Config{Listen: netip.MustParseAddrPort("0.0.0.0:7331"), PlainHTTP: true, Machine: wslMachine(), PublicOrigins: []string{"http://codeye.example:8080"}}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, host, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	cmd := "codeye up --public-origin http://codeye.example:8080 --public-origin http://windows-host.lan:7331"
	page := get("/", "Windows-Host.lan:7331", "text/html,application/xhtml+xml")
	body := page.Body.String()
	if page.Code != 403 || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") || page.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("page %d %v", page.Code, page.Header())
	}
	for _, want := range []string{"windows-host.lan:7331", "http://172.24.157.79:7331", "http://100.108.34.23:7331", "http://main:7331", cmd, "받게 하려면"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "172.20.0.1") || strings.Contains(body, "<script") || strings.Contains(body, "style=") {
		t.Errorf("page shows a bridge or inline code:\n%s", body)
	}
	api := get("/api/v1/health", "windows-host.lan:7331", "text/html")
	var e struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(api.Body.Bytes(), &e); err != nil || api.Code != 403 || e.Error.Code != "HOST_REJECTED" {
		t.Fatalf("api %d %s", api.Code, api.Body)
	}
	if e.Error.Details["host"] != "windows-host.lan:7331" || e.Error.Details["add_command"] != cmd || !strings.Contains(e.Error.Message, cmd) || !strings.Contains(e.Error.Message, "들어올 수 없습니다") {
		t.Fatalf("details %v message %s", e.Error.Details, e.Error.Message)
	}
	if acc, _ := e.Error.Details["accepted_origins"].([]any); len(acc) < 5 || acc[0] != "http://codeye.example:8080" {
		t.Fatalf("accepted %v", e.Error.Details["accepted_origins"])
	}
	odd := get("/", `ev"il<x>:7331`, "text/html")
	if odd.Code != 403 || strings.Contains(odd.Body.String(), "ev\"il") || strings.Contains(odd.Body.String(), "<x>") || strings.Contains(odd.Body.String(), "codeye up") {
		t.Fatalf("odd host echoed: %s", odd.Body)
	}
	// The default loopback service refuses this machine's name and offers no
	// --public-origin command (it would need TLS or plain HTTP).
	srv, err = New(Config{Listen: netip.MustParseAddrPort("127.0.0.1:7331"), Machine: wslMachine()}, &postgres.Store{})
	if err != nil {
		t.Fatal(err)
	}
	lo := get("/api/v1/health", "main:7331", "")
	e.Error.Details = nil
	if err := json.Unmarshal(lo.Body.Bytes(), &e); err != nil || lo.Code != 403 || e.Error.Code != "HOST_REJECTED" {
		t.Fatalf("loopback %d %s", lo.Code, lo.Body)
	}
	if _, ok := e.Error.Details["add_command"]; ok || strings.Contains(e.Error.Message, "codeye up") {
		t.Fatalf("loopback offers a command: %v %s", e.Error.Details, e.Error.Message)
	}
	if acc, _ := e.Error.Details["accepted_origins"].([]any); len(acc) != 2 || acc[0] != "http://127.0.0.1:7331" || acc[1] != "http://localhost:7331" {
		t.Fatalf("loopback accepted %v", e.Error.Details["accepted_origins"])
	}
}
