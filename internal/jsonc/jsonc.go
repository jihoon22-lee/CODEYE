// Package jsonc removes JSONC syntax before decoding with encoding/json.
package jsonc

// StripJSONC removes comments and trailing commas outside strings, the two
// supported JSONC extensions. An unterminated block comment returns nil.
func StripJSONC(b []byte) []byte { return stripTrailingCommas(stripComments(b)) }

// stripComments drops // and /* */ comments outside JSON strings.
func stripComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inStr:
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' && b[i] != '\r' {
				i++
			}
			if i < len(b) {
				out = append(out, b[i])
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			if i+1 >= len(b) {
				return nil
			}
			i++
			out = append(out, ' ')
		default:
			out = append(out, c)
		}
	}
	return out
}

func stripTrailingCommas(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	var previous byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inStr:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == ',':
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') && previous != 0 && previous != ',' && previous != '[' && previous != '{' {
				continue
			}
		}
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			previous = c
		}
		out = append(out, c)
	}
	return out
}
