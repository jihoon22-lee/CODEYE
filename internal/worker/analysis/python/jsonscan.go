package python

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

// element is one raw JSON value with its byte range in a stream and its
// JSON pointer in the document.
type element struct {
	Index      int
	Raw        json.RawMessage
	Start, End int64
	Pointer    string
}

// lines is the parse of a JSON-lines stream (ruff, mypy): one value per
// line with its byte range. A line that is not JSON makes the rest unparsed.
type lines struct {
	Values        []element
	UnparsedBytes int64
	DecodeError   string
}

func skipSpace(b []byte, i int64) int64 {
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

func classifyDecodeError(err error) string {
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

// parseJSONLines reads stdout value by value with byte offsets.
func parseJSONLines(stdout []byte) lines {
	var out lines
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		start := skipSpace(stdout, dec.InputOffset())
		if start >= int64(len(stdout)) {
			return out
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			out.DecodeError = classifyDecodeError(err)
			out.UnparsedBytes = int64(len(stdout)) - start
			return out
		}
		if len(raw) == 0 || raw[0] != '{' {
			out.DecodeError = "not_an_object"
			out.UnparsedBytes = int64(len(stdout)) - start
			return out
		}
		out.Values = append(out.Values, element{Index: len(out.Values), Raw: raw, Start: start, End: dec.InputOffset()})
	}
}

// document is the parse of a single JSON document (pyright, bandit): the
// top-level fields and, for the listed array fields, every element with its
// byte range and pointer.
type document struct {
	Fields        map[string]json.RawMessage
	Arrays        map[string][]element
	UnparsedBytes int64
	DecodeError   string
}

// parseDocument walks one JSON object. Anything after it, or a value that
// is not an object, is unparsed.
func parseDocument(b []byte, arrays ...string) document {
	out := document{Fields: map[string]json.RawMessage{}, Arrays: map[string][]element{}}
	fail := func(code string, at int64) document {
		out.DecodeError = code
		out.UnparsedBytes = int64(len(b)) - at
		if out.UnparsedBytes <= 0 {
			out.UnparsedBytes = int64(len(b))
		}
		return out
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return fail(classifyDecodeError(err), 0)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fail("not_an_object", 0)
	}
	wanted := map[string]bool{}
	for _, a := range arrays {
		wanted[a] = true
	}
	for dec.More() {
		at := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return fail(classifyDecodeError(err), at)
		}
		key, ok := tok.(string)
		if !ok {
			return fail("syntax_error", at)
		}
		if _, dup := out.Fields[key]; dup {
			return fail("duplicate_field", at)
		}
		if !wanted[key] {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return fail(classifyDecodeError(err), at)
			}
			out.Fields[key] = raw
			continue
		}
		at = dec.InputOffset()
		tok, err = dec.Token()
		if err != nil {
			return fail(classifyDecodeError(err), at)
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return fail("type_error", at)
		}
		elems := []element{}
		for dec.More() {
			start := skipSpace(b, dec.InputOffset())
			if start < int64(len(b)) && b[start] == ',' {
				start = skipSpace(b, start+1)
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return fail(classifyDecodeError(err), start)
			}
			elems = append(elems, element{Index: len(elems), Raw: raw, Start: start, End: dec.InputOffset(),
				Pointer: "/" + key + "/" + strconv.Itoa(len(elems))})
		}
		if _, err := dec.Token(); err != nil {
			return fail(classifyDecodeError(err), dec.InputOffset())
		}
		out.Fields[key] = json.RawMessage("[]")
		out.Arrays[key] = elems
	}
	if _, err := dec.Token(); err != nil {
		return fail(classifyDecodeError(err), dec.InputOffset())
	}
	if rest := skipSpace(b, dec.InputOffset()); rest < int64(len(b)) {
		return fail("trailing_data", rest)
	}
	return out
}

// decodeStrict decodes one raw value into v, refusing unknown fields: the
// parsers are pinned to the tested versions' formats.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}
