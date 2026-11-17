package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateEvidencePath(t *testing.T) {
	good := []string{
		"main.go",
		"src/pkg/file.go",
		"a/b/c/d.txt",
		"dir with space/한글.c",
		"..hidden/file",
		"a/..b/c",
		".github/workflows/ci.yml",
		"file.",
		"a/b...",
	}
	for _, p := range good {
		if err := ValidateEvidencePath(p); err != nil {
			t.Errorf("ValidateEvidencePath(%q) = %v", p, err)
		}
	}
	bad := map[string]string{
		"empty":           "",
		"dot":             ".",
		"dotdot":          "..",
		"parent":          "../etc/passwd",
		"embedded parent": "a/../../b",
		"inner parent":    "a/b/..",
		"dot segment":     "./a",
		"inner dot":       "a/./b",
		"trailing dot":    "a/.",
		"absolute":        "/etc/passwd",
		"unc":             "//server/share",
		"backslash":       `a\b`,
		"win parent":      `..\a`,
		"win drive":       `C:\Windows`,
		"drive relative":  "C:foo",
		"drive slash":     "C:/foo",
		"stream":          "file.txt:stream",
		"nul":             "a\x00b",
		"newline":         "a\nb",
		"tab":             "a\tb",
		"del":             "a\x7fb",
		"trailing slash":  "a/",
		"double slash":    "a//b",
		"invalid utf8":    "a\xffb",
		"too long":        strings.Repeat("a", maxEvidencePath+1),
	}
	for name, p := range bad {
		err := ValidateEvidencePath(p)
		if err == nil {
			t.Errorf("%s: ValidateEvidencePath(%q) = nil", name, p)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error %v does not wrap ErrInvalid", name, err)
		}
	}
	if err := ValidateEvidencePath(strings.Repeat("a", maxEvidencePath)); err != nil {
		t.Errorf("max-length path rejected: %v", err)
	}
}
