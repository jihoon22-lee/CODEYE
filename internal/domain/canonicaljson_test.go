package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonicalJSON(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty object", `{}`, `{}`},
		{"whitespace", " \n\t{ \"a\" : 1 }\r\n ", `{"a":1}`},
		{"key order", `{"b":1,"a":2,"B":3}`, `{"B":3,"a":2,"b":1}`},
		{"nested order", `{"z":{"y":1,"x":[{"d":1,"c":2}]},"a":null}`, `{"a":null,"z":{"x":[{"c":2,"d":1}],"y":1}}`},
		{"array order kept", `{"a":[3,1,2]}`, `{"a":[3,1,2]}`},
		{"empty containers", `{"a":[],"b":{}}`, `{"a":[],"b":{}}`},
		{"literals", `{"t":true,"f":false,"n":null}`, `{"f":false,"n":null,"t":true}`},
		{"big integer exact", `{"n":12345678901234567890123}`, `{"n":12345678901234567890123}`},
		{"float exact", `{"n":0.1000000000000000055511151231257827}`, `{"n":0.1000000000000000055511151231257827}`},
		{"exponent kept", `{"n":1E+400,"m":-0.0e-2}`, `{"m":-0.0e-2,"n":1E+400}`},
		{"escape normalised", `{"a":"\u0041\/\u00e9\u000a"}`, "{\"a\":\"A/é\\n\"}"},
		{"no html escape", `{"a":"<&>"}`, `{"a":"<&>"}`},
		{"control char escape", `{"a":"\u0001\u001f\b\f\t\r"}`, `{"a":"\u0001\u001f\b\f\t\r"}`},
		{"quote and backslash", `{"a":"\"\\"}`, `{"a":"\"\\"}`},
		{"surrogate pair", `{"a":"\ud83d\ude00"}`, `{"a":"😀"}`},
		{"literal fffd", "{\"a\":\"\uFFFD\"}", "{\"a\":\"\uFFFD\"}"},
		{"escaped key sorted decoded", `{"\u0062":1,"a":2}`, `{"a":2,"b":1}`},
	}
	for _, c := range cases {
		got, err := CanonicalJSON([]byte(c.in))
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		// Canonical output is a fixed point.
		again, err := CanonicalJSON(got)
		if err != nil || string(again) != string(got) {
			t.Errorf("%s: not idempotent: %s, %v", c.name, again, err)
		}
	}
}

// Unpaired surrogate escapes follow encoding/json: they become U+FFFD.
func TestCanonicalJSONSurrogateNormalization(t *testing.T) {
	cases := map[string]string{
		`{"a":"\ud83d"}`:       "{\"a\":\"\uFFFD\"}",
		`{"a":"\ude00"}`:       "{\"a\":\"\uFFFD\"}",
		`{"a":"\ud83d\u0041"}`: "{\"a\":\"\uFFFDA\"}",
	}
	for in, want := range cases {
		got, err := CanonicalJSON([]byte(in))
		if err != nil || string(got) != want {
			t.Errorf("%s -> %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestCanonicalJSONEquivalentInputs(t *testing.T) {
	a, err1 := CanonicalJSON([]byte(`{"x":[1,{"b":true,"a":false}],"y":"s"}`))
	b, err2 := CanonicalJSON([]byte("{\n  \"y\": \"s\",\n  \"x\": [ 1, { \"a\": false, \"b\": true } ]\n}"))
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if Digest(a) != Digest(b) {
		t.Errorf("equivalent documents differ: %s vs %s", a, b)
	}
}

func TestCanonicalJSONRejects(t *testing.T) {
	deep := strings.Repeat(`{"a":`, maxJSONDepth+1) + "1" + strings.Repeat("}", maxJSONDepth+1)
	deepArr := `{"a":` + strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1) + "}"
	bad := map[string]string{
		"empty":             ``,
		"blank":             `   `,
		"array top":         `[1]`,
		"string top":        `"a"`,
		"number top":        `1`,
		"null top":          `null`,
		"trailing object":   `{"a":1}{"b":2}`,
		"trailing garbage":  `{"a":1} x`,
		"trailing comma":    `{"a":1,}`,
		"array trailing":    `{"a":[1,]}`,
		"missing value":     `{"a":}`,
		"missing colon":     `{"a" 1}`,
		"unquoted key":      `{a:1}`,
		"single quotes":     `{'a':1}`,
		"truncated":         `{"a":1`,
		"truncated string":  `{"a":"x`,
		"duplicate key":     `{"a":1,"a":2}`,
		"duplicate via esc": `{"a":1,"\u0061":2}`,
		"nested duplicate":  `{"o":{"k":1,"k":1}}`,
		"leading zero":      `{"n":01}`,
		"plus sign":         `{"n":+1}`,
		"bare dot":          `{"n":.5}`,
		"trailing dot":      `{"n":1.}`,
		"bare exponent":     `{"n":1e}`,
		"nan":               `{"n":NaN}`,
		"infinity":          `{"n":-Infinity}`,
		"hex":               `{"n":0x10}`,
		"bad literal":       `{"n":tru}`,
		"bad escape":        `{"a":"\x"}`,
		"short unicode":     `{"a":"\u12"}`,
		"non-hex unicode":   `{"a":"\u12G4"}`,
		"signed unicode":    `{"a":"\u+123"}`,
		"raw control":       "{\"a\":\"x\ny\"}",
		"raw tab":           "{\"a\":\"x\ty\"}",
		"invalid utf8":      "{\"a\":\"\xff\"}",
		"invalid utf8 key":  "{\"\xc3\":1}",
		"truncated utf8":    "{\"a\":\"\xe2\x82\"}",
		"too deep objects":  deep,
		"too deep arrays":   deepArr,
		"comment":           `{"a":1}//x`,
		"bom":               "\xef\xbb\xbf{}",
		"nul":               "{}\x00",
	}
	for name, in := range bad {
		out, err := CanonicalJSON([]byte(in))
		if err == nil {
			t.Errorf("%s: accepted %q -> %s", name, in, out)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: error %v does not wrap ErrInvalid", name, err)
		}
	}
}

func TestCanonicalJSONMaxDepthAccepted(t *testing.T) {
	in := strings.Repeat(`{"a":`, maxJSONDepth) + "1" + strings.Repeat("}", maxJSONDepth)
	if _, err := CanonicalJSON([]byte(in)); err != nil {
		t.Errorf("depth %d rejected: %v", maxJSONDepth, err)
	}
}

func TestCanonicalJSONDoesNotAliasInput(t *testing.T) {
	in := []byte(`{"n":123}`)
	out, err := CanonicalJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	in[6] = '9'
	if string(out) != `{"n":123}` {
		t.Errorf("output aliases input: %s", out)
	}
}

func TestSentinelErrors(t *testing.T) {
	all := []error{ErrInvalid, ErrForbidden, ErrNotFound, ErrConflict, ErrStaleAttempt, ErrUnavailable, ErrMigration}
	for i, a := range all {
		for j, b := range all {
			if (i == j) != errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v) wrong", a, b)
			}
		}
	}
}
