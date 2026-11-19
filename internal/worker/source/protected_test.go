package source

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A local project folder that holds, or lies inside, a protected directory
// is refused, whether reached directly, through "." or through a symlink;
// a sibling folder and an unresolvable folder pass (the latter is left to
// AcquireLocal).
func TestCheckProtected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	home := t.TempDir()
	state := filepath.Join(home, ".local", "state", "codeye")
	for _, d := range []string{filepath.Join(state, "run"), filepath.Join(home, "src", "app")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(home, filepath.Join(home, "src", "back")); err != nil {
		t.Fatal(err)
	}
	protected := []string{state, filepath.Join(home, "missing-backups")}
	for rel, want := range map[string]error{
		".":                       ErrHoldsProtected,
		".local":                  ErrHoldsProtected,
		".local/state/codeye/run": ErrHoldsProtected,
		"src/back":                ErrHoldsProtected,
		"src/app":                 nil,
		"src/none":                nil,
	} {
		if err := CheckProtected(home, rel, protected); !errors.Is(err, want) || (want == nil && err != nil) {
			t.Errorf("%s: %v, want %v", rel, err, want)
		}
	}
	if err := CheckProtected(home, ".", nil); err != nil {
		t.Fatal(err)
	}
}
