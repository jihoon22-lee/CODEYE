package migrations

import (
	"errors"
	"regexp"
	"testing"
	"testing/fstest"
)

func TestEmbeddedSetIsValid(t *testing.T) {
	ms, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	hexSum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for i, m := range ms {
		if m.Version != i+1 {
			t.Fatalf("migration %d has version %d", i, m.Version)
		}
		if !hexSum.MatchString(m.Checksum) {
			t.Fatalf("migration %d checksum %q", m.Version, m.Checksum)
		}
		if regexp.MustCompile(`(?im)^\s*(BEGIN|COMMIT|ROLLBACK)\s*;`).MatchString(m.sql) {
			t.Fatalf("migration %d must not contain transaction control statements", m.Version)
		}
	}
	latest, err := LatestVersion()
	if err != nil || latest != len(ms) {
		t.Fatalf("LatestVersion = %d, %v; want %d", latest, err, len(ms))
	}
	again, _ := Embedded()
	if again[0].Checksum != ms[0].Checksum {
		t.Fatal("checksum is not deterministic")
	}
}

func TestLoadRejectsMalformedSets(t *testing.T) {
	ok := &fstest.MapFile{Data: []byte("SELECT 1;")}
	cases := map[string]fstest.MapFS{
		"subdirectory":  {"m/sub/0001_a.sql": ok},
		"no files":      {"other/0001_a.sql": ok},
		"bad name":      {"m/1_a.sql": ok},
		"upper name":    {"m/0001_Create.sql": ok},
		"not sql":       {"m/0001_a.txt": ok},
		"gap":           {"m/0001_a.sql": ok, "m/0003_c.sql": ok},
		"start at two":  {"m/0002_b.sql": ok},
		"duplicate":     {"m/0001_a.sql": ok, "m/0001_b.sql": ok},
		"empty content": {"m/0001_a.sql": &fstest.MapFile{Data: nil}},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := load(fsys, "m"); !errors.Is(err, ErrInvalidMigrationSet) {
				t.Fatalf("load error = %v, want ErrInvalidMigrationSet", err)
			}
		})
	}
}

func TestVerifyHistory(t *testing.T) {
	ms := []Migration{{Version: 1, Name: "a", Checksum: "c1"}, {Version: 2, Name: "b", Checksum: "c2"}}
	cases := []struct {
		name    string
		applied []appliedMigration
		want    error
	}{
		{"empty", nil, nil},
		{"prefix", []appliedMigration{{1, "a", "c1"}}, nil},
		{"all", []appliedMigration{{1, "a", "c1"}, {2, "b", "c2"}}, nil},
		{"future", []appliedMigration{{1, "a", "c1"}, {2, "b", "c2"}, {3, "c", "c3"}}, ErrUnknownVersion},
		{"gap", []appliedMigration{{2, "b", "c2"}}, ErrMissingVersion},
		{"checksum drift", []appliedMigration{{1, "a", "zz"}}, ErrChecksumMismatch},
		{"rename drift", []appliedMigration{{1, "x", "c1"}}, ErrChecksumMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := verifyHistory(ms, c.applied)
			if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
				t.Fatalf("verifyHistory = %v, want %v", err, c.want)
			}
		})
	}
}
