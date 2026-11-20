package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/hostaddr"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
)

// The controller of a service other machines reach gets only the
// configured public origins: on 0.0.0.0 it answers on this machine's own
// addresses by itself, so no single computed address is pinned.
func TestControllerListenArgs(t *testing.T) {
	wild := &instance.Config{Listen: "0.0.0.0:7331", PlainHTTP: true}
	if got := controllerListenArgs(wild); !slices.Equal(got, []string{"--plain-http", "--local-listen", "127.0.0.1:7332"}) {
		t.Fatalf("%v", got)
	}
	wild.PublicOrigins = []string{"http://codeye.lan:7331", "http://codeye.vpn:7331"}
	if got := controllerListenArgs(wild); !slices.Equal(got, []string{"--plain-http", "--public-origin", "http://codeye.lan:7331", "--public-origin", "http://codeye.vpn:7331", "--local-listen", "127.0.0.1:7332"}) {
		t.Fatalf("%v", got)
	}
	if got := controllerListenArgs(&instance.Config{Listen: "127.0.0.1:7331"}); len(got) != 0 {
		t.Fatalf("loopback %v", got)
	}
}

func TestPrintAddressesGroups(t *testing.T) {
	var b strings.Builder
	printAddresses(&b, []hostaddr.Address{{Origin: "http://127.0.0.1:7331", Kind: hostaddr.KindThisMachine}, {Origin: "http://main:7331", Kind: hostaddr.KindName},
		{Origin: "http://172.24.157.79:7331", Kind: hostaddr.KindLAN}, {Origin: "http://100.108.34.23:7331", Kind: hostaddr.KindVPN}, {Origin: "http://localhost:7331", Kind: hostaddr.KindThisMachine}})
	want := "The service answers on:\n  LAN           http://172.24.157.79:7331\n  VPN           http://100.108.34.23:7331\n  name          http://main:7331\n  this machine  http://127.0.0.1:7331  http://localhost:7331\n"
	if b.String() != want {
		t.Fatalf("%q", b.String())
	}
}
