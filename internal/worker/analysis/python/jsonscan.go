package python

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
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

// parseJSONLines reads stdout value by value with byte offsets.
func parseJSONLines(stdout []byte) lines {
	var out lines
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		start := pack.SkipSpace(stdout, dec.InputOffset())
		if start >= int64(len(stdout)) {
			return out
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			out.DecodeError = pack.ClassifyDecodeError(err)
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
		return fail(pack.ClassifyDecodeError(err), 0)
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
			return fail(pack.ClassifyDecodeError(err), at)
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
				return fail(pack.ClassifyDecodeError(err), at)
			}
			out.Fields[key] = raw
			continue
		}
		at = dec.InputOffset()
		tok, err = dec.Token()
		if err != nil {
			return fail(pack.ClassifyDecodeError(err), at)
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return fail("type_error", at)
		}
		elems := []element{}
		for dec.More() {
			start := pack.SkipSpace(b, dec.InputOffset())
			if start < int64(len(b)) && b[start] == ',' {
				start = pack.SkipSpace(b, start+1)
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return fail(pack.ClassifyDecodeError(err), start)
			}
			elems = append(elems, element{Index: len(elems), Raw: raw, Start: start, End: dec.InputOffset(),
				Pointer: "/" + key + "/" + strconv.Itoa(len(elems))})
		}
		if _, err := dec.Token(); err != nil {
			return fail(pack.ClassifyDecodeError(err), dec.InputOffset())
		}
		out.Fields[key] = json.RawMessage("[]")
		out.Arrays[key] = elems
	}
	if _, err := dec.Token(); err != nil {
		return fail(pack.ClassifyDecodeError(err), dec.InputOffset())
	}
	if rest := pack.SkipSpace(b, dec.InputOffset()); rest < int64(len(b)) {
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
