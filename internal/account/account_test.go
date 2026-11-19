package account

import (
	"strings"
	"testing"
	"time"
)

// TC-147
func TestNormalizeUsername(t *testing.T) {
	for in, want := range map[string]string{"Alice": "alice", "bob.k_9-x": "bob.k_9-x", "AB": "ab"} {
		if got, err := NormalizeUsername(in); err != nil || got != want {
			t.Fatalf("%q: %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "a", "-ab", ".ab", "a b", "a/b", "ä" + "bc", strings.Repeat("a", 65), "ab\n"} {
		if _, err := NormalizeUsername(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// TC-147
func TestCheckPassword(t *testing.T) {
	ok := []string{"correct horse battery", "비밀번호는열두글자이상입니다", strings.Repeat("ab", 6)}
	for _, p := range ok {
		if err := CheckPassword(p, "alice", "Alice Kim"); err != nil {
			t.Fatalf("%q rejected: %v", p, err)
		}
	}
	bad := []string{"short", strings.Repeat("a", 12), "aliceusername", "ALICEUSERNAME", "elevenchars", "bad\x00password12", string([]byte{0xff, 0xfe}) + "aaaaaaaaaaaa", strings.Repeat("x", 257) + "y"}
	for _, p := range bad {
		if err := CheckPassword(p, "aliceusername", "Alice Kim"); err == nil {
			t.Fatalf("%q accepted", p)
		}
	}
	if CheckPassword("Alice Kim Lee", "u1", "alice kim lee") == nil {
		t.Fatal("display name accepted as password")
	}
}

// TC-147
func TestDeriveVerify(t *testing.T) {
	h, err := Derive("correct horse battery", MinIterations)
	if err != nil {
		t.Fatal(err)
	}
	if h.KDF != KDF || len(h.Salt) != SaltBytes || len(h.Key) != KeyBytes || h.Iterations != MinIterations {
		t.Fatalf("%+v", h)
	}
	if !h.Verify("correct horse battery") || h.Verify("correct horse batterY") {
		t.Fatal("verify")
	}
	other, _ := Derive("correct horse battery", MinIterations)
	if string(other.Salt) == string(h.Salt) || h.Equal(other) || !h.Equal(h) {
		t.Fatal("salt must differ per derivation")
	}
	if Dummy(MinIterations).Verify("") || Dummy(MinIterations).Verify("anything at all") {
		t.Fatal("dummy matched")
	}
	if (Hash{KDF: "md5"}).Verify("x") {
		t.Fatal("unusable hash matched")
	}
	if _, err := Derive("x", MinIterations-1); err == nil {
		t.Fatal("low iteration count accepted")
	}
}

// TestDefaultCostIsSlow records the cost of one default derivation on this
// machine (the reason logins derive outside database transactions).
// TC-147
func TestDefaultCostIsSlow(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	start := time.Now()
	Dummy(DefaultIterations).Verify("measure")
	t.Logf("one %s derivation with %d iterations: %v", KDF, DefaultIterations, time.Since(start))
}
