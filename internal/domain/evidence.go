package domain

import (
	"strings"
	"unicode/utf8"
)

const maxEvidencePath = 4096

// ValidateEvidencePath checks that p is a snapshot-relative, slash-separated
// path that cannot escape the snapshot root or name a drive/stream. It is a
// purely lexical check: it neither touches the filesystem nor proves the file
// exists. Errors wrap ErrInvalid.
func ValidateEvidencePath(p string) error {
	switch {
	case p == "":
		return invalidf("evidence path is empty")
	case len(p) > maxEvidencePath:
		return invalidf("evidence path exceeds %d bytes", maxEvidencePath)
	case !utf8.ValidString(p):
		return invalidf("evidence path is not valid UTF-8")
	case p[0] == '/':
		return invalidf("evidence path must be relative")
	}
	for _, r := range p {
		switch {
		case r == '\\':
			return invalidf("evidence path contains a backslash")
		case r == ':':
			return invalidf("evidence path contains a colon")
		case r < 0x20 || r == 0x7f:
			return invalidf("evidence path contains a control character")
		}
	}
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "":
			return invalidf("evidence path has an empty segment")
		case ".", "..":
			return invalidf("evidence path contains a %q segment", seg)
		}
	}
	return nil
}
