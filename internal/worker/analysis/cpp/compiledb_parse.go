package cpp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

type dbEntry struct {
	Directory *string  `json:"directory"`
	File      *string  `json:"file"`
	Command   *string  `json:"command"`
	Arguments []string `json:"arguments"`
	Output    *string  `json:"output"`
}

// ErrCompileDB is returned for a compile DB that is not a JSON array of
// objects within the size limits.
var ErrCompileDB = errors.New("compile DB unreadable")

func parseDB(b []byte) ([]dbEntry, error) {
	if len(b) > maxCompileDBBytes {
		return nil, ErrCompileDB
	}
	var raw []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&raw); err != nil {
		return nil, ErrCompileDB
	}
	if dec.More() || len(raw) > maxCompileDBEntries {
		return nil, ErrCompileDB
	}
	out := make([]dbEntry, len(raw))
	for i, r := range raw {
		// Unknown keys are tolerated (generators add some); a value of the
		// wrong type makes the entry malformed, not the database.
		if err := json.Unmarshal(r, &out[i]); err != nil {
			out[i] = dbEntry{}
		}
	}
	return out, nil
}

// splitCommand splits a "command" string like clang's JSON compilation
// database on POSIX: whitespace separates, single quotes are literal, double
// quotes and backslashes escape the next character.
func splitCommand(s string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	inArg := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inArg {
				out = append(out, cur.String())
				cur.Reset()
				inArg = false
			}
		case c == '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			i++
			cur.WriteByte(s[i])
			inArg = true
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inArg = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
			inArg = true
		default:
			cur.WriteByte(c)
			inArg = true
		}
	}
	if inArg {
		out = append(out, cur.String())
	}
	return out, true
}

// splitWindowsCommand splits a "command" string of a Windows compile DB
// like CommandLineToArgvW: white space separates outside double quotes,
// backslashes are literal unless they precede a double quote (2n
// backslashes + quote: n backslashes and a quote toggle; 2n+1: n
// backslashes and a literal quote).
func splitWindowsCommand(s string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	inArg, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c == ' ' || c == '\t' || c == '\n' || c == '\r') && !quoted:
			if inArg {
				out = append(out, cur.String())
				cur.Reset()
				inArg = false
			}
		case c == '\\':
			n := 0
			for i < len(s) && s[i] == '\\' {
				n++
				i++
			}
			if i < len(s) && s[i] == '"' {
				cur.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					cur.WriteByte('"')
				} else {
					quoted = !quoted
				}
			} else {
				cur.WriteString(strings.Repeat(`\`, n))
				i--
			}
			inArg = true
		case c == '"':
			quoted = !quoted
			inArg = true
		default:
			cur.WriteByte(c)
			inArg = true
		}
	}
	if quoted {
		return nil, false
	}
	if inArg {
		out = append(out, cur.String())
	}
	return out, true
}
