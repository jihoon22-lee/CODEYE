package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// TC-149: the throttle key of a connection is the IPv4 address, or the /64
// of an IPv6 address (IPv4-mapped IPv6 counts as IPv4).
func TestClientAddressKey(t *testing.T) {
	for remote, want := range map[string]string{
		"192.0.2.7:4100":                        "192.0.2.7",
		"[::ffff:192.0.2.7]:4100":               "192.0.2.7",
		"[2001:db8:1:2::1]:4100":                "2001:db8:1:2::/64",
		"[2001:db8:1:2:ffff:ffff:ffff:ffff]:80": "2001:db8:1:2::/64",
		"[2001:db8:1:3::1]:4100":                "2001:db8:1:3::/64",
		"[fe80::1%eth0]:4100":                   "fe80::/64",
		"not an address":                        "unknown",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if got := clientAddress(r); got != want {
			t.Errorf("%s: %s, want %s", remote, got, want)
		}
	}
}

// TC-149: two addresses in one IPv6 /64 share a (name, address) lock; an
// address in the next /64 does not.
func TestPairLockSharedAcrossIPv6Prefix(t *testing.T) {
	e := newEnv(t)
	const pw = "correct horse battery"
	e.account("erin", pw, false)
	login := func(remote, password string) int {
		raw, _ := json.Marshal(map[string]string{"username": "erin", "password": password})
		r := httptest.NewRequest("POST", "http://"+e.host+"/api/v1/session", strings.NewReader(string(raw)))
		r.Host = e.host
		r.RemoteAddr = remote
		r.Header.Set("Origin", "http://"+e.host)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		e.hs.Config.Handler.ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 5; i++ {
		if code := login("[2001:db8:1:2::1]:4100", "not the password"); code != 401 {
			t.Fatalf("attempt %d: %d", i, code)
		}
	}
	if code := login("[2001:db8:1:2::abcd]:5100", pw); code != 429 {
		t.Fatalf("same /64: %d", code)
	}
	if code := login("[2001:db8:1:3::1]:4100", pw); code != 201 {
		t.Fatalf("next /64: %d", code)
	}
}
