// Package hostaddr knows the addresses and names of this machine: which Host
// headers name it (the controller accepts them) and which addresses to show
// people as the way to reach the service (`codeye up`, the admin screen).
//
// Nothing here asks a resolver: the names are the hostname, hostname.local
// and the hostname under each search domain of /etc/resolv.conf, so an
// offline installation makes no DNS query to know itself.
package hostaddr

import (
	"bufio"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kinds of a shown address, in the order they are shown.
const (
	KindConfigured  = "configured"   // a public origin the operator gave
	KindThisMachine = "this_machine" // loopback: only a browser on this machine
	KindName        = "name"         // the machine's hostname
	KindLAN         = "lan"          // an address of a local network interface
	KindVPN         = "vpn"          // an overlay network (Tailscale, WireGuard, ZeroTier)
)

// Address is one way to reach the service.
type Address struct {
	Origin string `json:"origin"`
	Kind   string `json:"kind"`
}

// Iface is one network interface as the source reports it.
type Iface struct {
	Name     string
	Up       bool
	Loopback bool
	Addrs    []netip.Addr
}

// Source reads the machine. System is the real one; tests give their own.
type Source struct {
	Interfaces    func() ([]Iface, error)
	Hostname      func() (string, error)
	SearchDomains func() []string
}

// System reads the interfaces, the hostname and the resolver search domains
// of this machine.
func System() Source {
	return Source{Interfaces: systemInterfaces, Hostname: os.Hostname, SearchDomains: resolvSearch}
}

func systemInterfaces() ([]Iface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]Iface, 0, len(ifs))
	for _, i := range ifs {
		f := Iface{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Loopback: i.Flags&net.FlagLoopback != 0}
		addrs, err := i.Addrs()
		if err == nil {
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok {
					if ip, ok := netip.AddrFromSlice(n.IP); ok {
						f.Addrs = append(f.Addrs, ip.Unmap())
					}
				}
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// resolvSearch is the search domains of /etc/resolv.conf (none where the
// file does not exist, as on Windows).
func resolvSearch() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 1 && (fields[0] == "search" || fields[0] == "domain") {
			out = append(out, fields[1:]...)
		}
	}
	return out
}

// refreshEvery bounds how stale the interface list may be: an interface
// that comes up later (a VPN) is known within this time.
const refreshEvery = 3 * time.Second

// Machine caches what Source reports. The zero value is not usable; use New.
type Machine struct {
	src Source
	now func() time.Time

	mu     sync.Mutex
	at     time.Time
	ifs    []Iface
	own    map[netip.Addr]bool
	names  map[string]bool
	host   string
	loaded bool
}

// New is a Machine reading src.
func New(src Source) *Machine { return &Machine{src: src, now: time.Now} }

var (
	systemOnce sync.Once
	system     *Machine
)

// SystemMachine is the shared Machine of this process.
func SystemMachine() *Machine {
	systemOnce.Do(func() { system = New(System()) })
	return system
}

func (m *Machine) load(force bool) {
	now := m.now()
	if m.loaded && !force && now.Sub(m.at) < refreshEvery {
		return
	}
	if m.loaded && force && now.Sub(m.at) < 500*time.Millisecond {
		return
	}
	m.loaded, m.at = true, now
	m.ifs, _ = m.src.Interfaces()
	m.own = map[netip.Addr]bool{}
	for _, i := range m.ifs {
		for _, a := range i.Addrs {
			m.own[a.WithZone("")] = true
		}
	}
	m.names = map[string]bool{"localhost": true}
	m.host = ""
	if h, err := m.src.Hostname(); err == nil {
		h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
		if validName(h) {
			short, _, _ := strings.Cut(h, ".")
			m.host = short
			m.names[h], m.names[short], m.names[short+".local"] = true, true, true
			if m.src.SearchDomains != nil {
				for _, d := range m.src.SearchDomains() {
					d = strings.TrimSuffix(strings.ToLower(d), ".")
					if validName(d) {
						m.names[short+"."+d] = true
					}
				}
			}
		}
	}
}

// validName is a DNS name of letters, digits, '-' and '.'.
func validName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return !strings.HasPrefix(s, ".") && !strings.Contains(s, "..")
}

// Own reports whether host (a Host header's host part: a name, an IPv4
// address or a bracketed IPv6 address) names this machine: localhost, its
// hostname forms, any loopback address or an address of one of its
// interfaces. Names match exactly (case-insensitive), never by prefix.
func (m *Machine) Own(host string) bool {
	host = strings.ToLower(host)
	bracketed := strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]")
	if bracketed {
		host = host[1 : len(host)-1]
		if ip, err := netip.ParseAddr(host); err != nil || !ip.Is6() {
			return false
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load(false)
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap().WithZone("")
		if ip.IsLoopback() {
			return true
		}
		if ip.IsUnspecified() {
			return false
		}
		if m.own[ip] {
			return true
		}
		// An address that just appeared (a VPN coming up) is learned now.
		m.load(true)
		return m.own[ip]
	}
	return m.names[host]
}

// noisy reports interfaces whose addresses no other person uses to reach
// this machine: container bridges and virtual pairs, Hyper-V switches.
func noisy(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"docker", "br-", "veth", "virbr", "cni", "flannel", "podman", "lxcbr", "lxdbr", "kube", "vethernet", "vmnet", "vboxnet"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

var (
	cgnat     = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// overlay reports a VPN interface or address.
func overlay(name string, ip netip.Addr) bool {
	if cgnat.Contains(ip) || tailnetV6.Contains(ip) {
		return true
	}
	n := strings.ToLower(name)
	for _, p := range []string{"tailscale", "wg", "wireguard", "zt", "zerotier", "tun", "utun"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// Addresses are the origins (scheme://host:port) that reach a service
// listening on listen, for people to use: the configured origins first,
// then this machine's loopback, its name, its LAN and VPN addresses. A
// specific listen address is reachable only by that address (and the
// configured origins); container bridges and link-local addresses are left
// out, and an interface's IPv6 addresses only when it has no IPv4 one.
func (m *Machine) Addresses(scheme string, listen netip.AddrPort, configured []string) []Address {
	out := []Address{}
	seen := map[string]bool{}
	add := func(origin, kind string) {
		if !seen[origin] {
			seen[origin] = true
			out = append(out, Address{Origin: origin, Kind: kind})
		}
	}
	for _, o := range configured {
		add(o, KindConfigured)
	}
	port := listen.Port()
	origin := func(host string) string {
		def := uint16(80)
		if scheme == "https" {
			def = 443
		}
		if port == def {
			return scheme + "://" + host
		}
		return scheme + "://" + net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(int(port)))
	}
	addr := listen.Addr().Unmap()
	if addr.IsLoopback() {
		add(origin(addr.String()), KindThisMachine)
		if addr == netip.MustParseAddr("127.0.0.1") {
			add(origin("localhost"), KindThisMachine)
		}
		return out
	}
	if !addr.IsUnspecified() {
		kind := KindLAN
		m.mu.Lock()
		m.load(false)
		for _, i := range m.ifs {
			for _, a := range i.Addrs {
				if a.WithZone("") == addr && overlay(i.Name, a) {
					kind = KindVPN
				}
			}
		}
		m.mu.Unlock()
		add(origin(addr.String()), kind)
		return out
	}
	add(origin("127.0.0.1"), KindThisMachine)
	add(origin("localhost"), KindThisMachine)
	m.mu.Lock()
	m.load(false)
	ifs := append([]Iface(nil), m.ifs...)
	host := m.host
	m.mu.Unlock()
	if host != "" {
		add(origin(host), KindName)
	}
	var lan, vpn []Address
	for _, i := range ifs {
		if !i.Up || i.Loopback || noisy(i.Name) {
			continue
		}
		var v4, v6 []netip.Addr
		for _, a := range i.Addrs {
			if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
				continue
			}
			if a.Is4() {
				v4 = append(v4, a)
			} else {
				v6 = append(v6, a)
			}
		}
		use := v4
		if len(use) == 0 {
			use = v6
		}
		for _, a := range use {
			h := a.String()
			if a.Is6() {
				h = "[" + h + "]"
			}
			if overlay(i.Name, a) {
				vpn = append(vpn, Address{Origin: origin(h), Kind: KindVPN})
			} else {
				lan = append(lan, Address{Origin: origin(h), Kind: KindLAN})
			}
		}
	}
	for _, group := range [][]Address{lan, vpn} {
		for _, a := range group {
			add(a.Origin, a.Kind)
		}
	}
	return out
}

// Primary is the one address to name when only one is shown: the first
// configured origin, else the first LAN, VPN, name or loopback address of
// Addresses (never the unspecified address).
func Primary(addrs []Address) string {
	for _, kind := range []string{KindConfigured, KindLAN, KindVPN, KindName, KindThisMachine} {
		for _, a := range addrs {
			if a.Kind == kind {
				return a.Origin
			}
		}
	}
	return ""
}
