package hostaddr

import (
	"net/netip"
	"slices"
	"testing"
	"time"
)

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddr(a)
	}
	return out
}

// A machine like the WSL2 host this was written on: loopback (with a DNS
// stub address), eth0, docker bridges and veths, Tailscale.
func fakeMachine(ifs *[]Iface) *Machine {
	src := Source{
		Interfaces:    func() ([]Iface, error) { return *ifs, nil },
		Hostname:      func() (string, error) { return "Main", nil },
		SearchDomains: func() []string { return []string{"tail30f401.ts.net"} },
	}
	return New(src)
}

func wsl() []Iface {
	return []Iface{
		{Name: "lo", Up: true, Loopback: true, Addrs: addrs("127.0.0.1", "10.255.255.254", "::1")},
		{Name: "eth0", Up: true, Addrs: addrs("172.24.157.79", "fe80::215:5dff:fe42:3274")},
		{Name: "docker0", Up: false, Addrs: addrs("172.17.0.1")},
		{Name: "br-2d92d005db3e", Up: true, Addrs: addrs("172.20.0.1")},
		{Name: "veth77f8c4c", Up: true, Addrs: addrs("fe80::f4ad:5eff:feb5:282e")},
		{Name: "tailscale0", Up: true, Addrs: addrs("100.108.34.23", "fd7a:115c:a1e0::6701:22d4", "fe80::bf29:4397:c8cd:290d")},
	}
}

func TestOwnNamesAndAddresses(t *testing.T) {
	ifs := wsl()
	m := fakeMachine(&ifs)
	for _, h := range []string{"localhost", "main", "MAIN", "main.local", "main.tail30f401.ts.net", "127.0.0.1", "127.0.0.2", "[::1]",
		"172.24.157.79", "100.108.34.23", "[fd7a:115c:a1e0::6701:22d4]", "[fe80::215:5dff:fe42:3274]", "172.20.0.1", "10.255.255.254", "[::ffff:172.24.157.79]"} {
		if !m.Own(h) {
			t.Errorf("%s is this machine", h)
		}
	}
	for _, h := range []string{"main.evil.example", "evil.main", "mainx", "codeye.example", "0.0.0.0", "[::]", "8.8.8.8", "172.24.157.80", "", "[main]", "main."} {
		if m.Own(h) {
			t.Errorf("%s is not this machine", h)
		}
	}
}

// An interface that comes up after the first look (a VPN) is known at the
// next request that names it.
func TestOwnLearnsNewAddresses(t *testing.T) {
	ifs := wsl()[:2]
	m := fakeMachine(&ifs)
	clock := time.Unix(1000, 0)
	m.now = func() time.Time { return clock }
	if m.Own("100.108.34.23") {
		t.Fatal("not up yet")
	}
	ifs = wsl()
	clock = clock.Add(time.Second)
	if !m.Own("100.108.34.23") {
		t.Fatal("the new address is not learned")
	}
}

func TestAddressesGroupsAndSkipsBridges(t *testing.T) {
	ifs := wsl()
	m := fakeMachine(&ifs)
	got := m.Addresses("http", netip.MustParseAddrPort("0.0.0.0:7331"), nil)
	want := []Address{
		{"http://127.0.0.1:7331", KindThisMachine}, {"http://localhost:7331", KindThisMachine}, {"http://main:7331", KindName},
		{"http://172.24.157.79:7331", KindLAN}, {"http://100.108.34.23:7331", KindVPN},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	if p := Primary(got); p != "http://172.24.157.79:7331" {
		t.Fatalf("primary %s", p)
	}
	// IPv6 wildcard and the configured origins first.
	got = m.Addresses("http", netip.MustParseAddrPort("[::]:80"), []string{"http://codeye.example"})
	if got[0] != (Address{"http://codeye.example", KindConfigured}) || got[1].Origin != "http://127.0.0.1" || Primary(got) != "http://codeye.example" {
		t.Fatalf("%v", got)
	}
	// A specific address is the only way in.
	got = m.Addresses("https", netip.MustParseAddrPort("100.108.34.23:8443"), nil)
	if !slices.Equal(got, []Address{{"https://100.108.34.23:8443", KindVPN}}) {
		t.Fatalf("%v", got)
	}
	got = m.Addresses("http", netip.MustParseAddrPort("127.0.0.1:7331"), nil)
	if !slices.Equal(got, []Address{{"http://127.0.0.1:7331", KindThisMachine}, {"http://localhost:7331", KindThisMachine}}) || Primary(got) != "http://127.0.0.1:7331" {
		t.Fatalf("%v", got)
	}
	// Only IPv6 on an interface: that address, bracketed.
	ifs = []Iface{{Name: "Ethernet 2", Up: true, Addrs: addrs("2001:db8::5")}}
	m = fakeMachine(&ifs)
	got = m.Addresses("http", netip.MustParseAddrPort("0.0.0.0:7331"), nil)
	if got[len(got)-1] != (Address{"http://[2001:db8::5]:7331", KindLAN}) {
		t.Fatalf("%v", got)
	}
}

// The real machine: no assumption about interface names or the OS, only
// that loopback and localhost are this machine and nothing fails.
func TestSystemMachine(t *testing.T) {
	m := SystemMachine()
	if !m.Own("127.0.0.1") || !m.Own("localhost") || !m.Own("[::1]") {
		t.Fatal("loopback is not this machine")
	}
	got := m.Addresses("http", netip.MustParseAddrPort("0.0.0.0:7331"), nil)
	if len(got) < 2 || got[0].Origin != "http://127.0.0.1:7331" || Primary(got) == "" {
		t.Fatalf("%v", got)
	}
	for _, a := range got {
		if a.Origin == "http://0.0.0.0:7331" {
			t.Fatal("the unspecified address is shown")
		}
	}
}
