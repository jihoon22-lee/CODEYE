package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const maxJSONDepth = 100

// CanonicalJSON re-encodes a single JSON object into a deterministic form so
// that equal documents yield equal bytes (for digests and idempotency checks).
//
// The output has no insignificant whitespace, object members sorted by key,
// and no HTML escaping. Numbers keep their exact source text, so nothing is
// rounded through a float; the consequence is that 1 and 1.0 are different
// documents. Strings follow encoding/json normalization: an unpaired surrogate
// escape (\ud800) becomes U+FFFD, and U+2028/U+2029 are written as escapes.
//
// Input is rejected with ErrInvalid if it is not valid UTF-8, is not a single
// object (non-object root, trailing data), repeats a key within an object, is
// not well-formed JSON, or nests deeper than 100 levels.
func CanonicalJSON(b []byte) ([]byte, error) {
	if !utf8.Valid(b) {
		return nil, invalidf("json: input is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, invalidf("json: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, invalidf("json: top-level value must be an object")
	}
	v, err := decodeObject(dec, 1)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, invalidf("json: trailing data after object")
	}

	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil { // map keys are emitted sorted
		return nil, invalidf("json: %v", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// decodeValue reads the next value. Scalars come back as string, json.Number,
// bool or nil; containers as map[string]any or []any.
func decodeValue(dec *json.Decoder, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, invalidf("json: %v", err)
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	if depth+1 > maxJSONDepth {
		return nil, invalidf("json: nesting deeper than %d", maxJSONDepth)
	}
	if d == '{' {
		return decodeObject(dec, depth+1)
	}
	return decodeArray(dec, depth+1)
}

// decodeObject reads members up to the closing brace; '{' is already consumed.
func decodeObject(dec *json.Decoder, depth int) (map[string]any, error) {
	obj := map[string]any{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, invalidf("json: %v", err)
		}
		key, ok := tok.(string)
		if !ok {
			return nil, invalidf("json: expected object key")
		}
		if _, dup := obj[key]; dup {
			return nil, invalidf("json: duplicate key %q", key)
		}
		if obj[key], err = decodeValue(dec, depth); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil { // '}'
		return nil, invalidf("json: %v", err)
	}
	return obj, nil
}

// decodeArray reads elements up to the closing bracket; '[' is already consumed.
func decodeArray(dec *json.Decoder, depth int) ([]any, error) {
	arr := []any{}
	for dec.More() {
		v, err := decodeValue(dec, depth)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	if _, err := dec.Token(); err != nil { // ']'
		return nil, invalidf("json: %v", err)
	}
	return arr, nil
}
