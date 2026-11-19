package pack

import (
	"path"
	"strings"
)

// Snapshot paths are slash-separated and relative to the snapshot root; the
// root directory itself is ".".

// DirOf is the directory of a snapshot path ("." at the root).
func DirOf(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

// UnderDir reports whether snapshot path p is dir or below it.
func UnderDir(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

// RelTo is p relative to dir ("." for dir itself); p must be under dir.
func RelTo(p, dir string) string {
	if dir == "." {
		return p
	}
	if p == dir {
		return "."
	}
	return strings.TrimPrefix(p, dir+"/")
}
