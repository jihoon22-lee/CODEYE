package pack

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// SkipSpace returns the offset of the first byte at or after i in b that is
// not JSON whitespace (len(b) if none).
func SkipSpace(b []byte, i int64) int64 {
	for i < int64(len(b)) {
		switch b[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// ClassifyDecodeError names why a tool's JSON stdout could not be decoded:
// unexpected_eof, syntax_error, type_error, unknown_field or decode_error.
func ClassifyDecodeError(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.As(err, &syn):
		return "syntax_error"
	case errors.As(err, &typ):
		return "type_error"
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return "unknown_field"
	}
	return "decode_error"
}
