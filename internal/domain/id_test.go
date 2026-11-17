package domain

import (
	"strings"
	"testing"
)

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewID()
		if !ValidID(id) {
			t.Fatalf("NewID() = %q is not a valid ID", id)
		}
		if id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
			t.Fatalf("NewID() = %q is not RFC 4122 version 4", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestValidID(t *testing.T) {
	good := []string{
		"123e4567-e89b-42d3-a456-426614174000",
		"00000000-0000-0000-0000-000000000000",
	}
	for _, s := range good {
		if !ValidID(s) {
			t.Errorf("ValidID(%q) = false", s)
		}
	}
	bad := []string{
		"",
		"123e4567e89b42d3a456426614174000",
		"123E4567-E89B-42D3-A456-426614174000", // uppercase is not canonical
		"123e4567-e89b-42d3-a456-42661417400",  // short
		"123e4567-e89b-42d3-a456-4266141740000",
		"123e4567-e89b-42d3-a456-42661417400g",
		"{123e4567-e89b-42d3-a456-426614174000}",
		" 123e4567-e89b-42d3-a456-426614174000",
		"123e4567-e89b-42d3-a456-426614174000\n",
		"123e4567_e89b_42d3_a456_426614174000",
		"urn:uuid:123e4567-e89b-42d3-a456-426614174000",
		"123e4567-e89b-42d3-a456-4266141740é0",
	}
	for _, s := range bad {
		if ValidID(s) {
			t.Errorf("ValidID(%q) = true", s)
		}
	}
}

func TestDigest(t *testing.T) {
	// Known SHA-256 vectors.
	if got, want := Digest(nil), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"; got != want {
		t.Errorf("Digest(nil) = %s", got)
	}
	if got, want := Digest([]byte("abc")), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; got != want {
		t.Errorf("Digest(abc) = %s", got)
	}
	if Digest([]byte("a")) == Digest([]byte("b")) {
		t.Error("different inputs must differ")
	}
}
