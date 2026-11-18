package tsc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Parser identity: tsc --pretty false diagnostics (TypeScript 6.0 text
// format) and --showConfig JSON.
const (
	ParserID      = "tsc-text-v1"
	ParserVersion = "1"
)

var (
	// fileDiag is "<path>(<line>,<col>): error TS<code>: <text>".
	fileDiag = regexp.MustCompile(`^(.+)\(([0-9]+),([0-9]+)\): (error|warning|message) TS([0-9]{1,6}): (.*)$`)
	// globalDiag is "error TS<code>: <text>" (no file).
	globalDiag = regexp.MustCompile(`^(error|warning|message) TS([0-9]{1,6}): (.*)$`)
)

// record is one diagnostic with its continuation lines and byte range.
type record struct {
	Index              int
	File               string // "" for a global diagnostic
	Line, Col          int    // 1-based; Col in UTF-16 units
	Category           string
	Code               int
	Text               string
	ByteStart, ByteEnd int64
}

// stream is the parse of one check's stdout. UnparsedBytes > 0 means a line
// was neither a diagnostic nor a continuation: never "no findings".
type stream struct {
	Records       []record
	UnparsedBytes int64
	ShapeErrors   int
}

// parseDiagnostics reads tsc --pretty false output. Continuation lines (two
// leading spaces) belong to the previous diagnostic.
func parseDiagnostics(b []byte) stream {
	var st stream
	var off int64
	for len(b) > 0 {
		line := b
		next := int64(len(b))
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line, next = b[:i], int64(i+1)
		}
		text := strings.TrimRight(string(line), "\r")
		end := off + int64(len(strings.TrimRight(string(line), "\r")))
		switch {
		case text == "":
		case strings.HasPrefix(text, "  ") && len(st.Records) > 0:
			r := &st.Records[len(st.Records)-1]
			r.Text += "\n" + text[2:]
			r.ByteEnd = end
		default:
			if m := fileDiag.FindStringSubmatch(text); m != nil {
				l, _ := strconv.Atoi(m[2])
				c, _ := strconv.Atoi(m[3])
				code, _ := strconv.Atoi(m[5])
				st.Records = append(st.Records, record{Index: len(st.Records), File: m[1], Line: l, Col: c, Category: m[4], Code: code, Text: m[6], ByteStart: off, ByteEnd: end})
			} else if m := globalDiag.FindStringSubmatch(text); m != nil {
				code, _ := strconv.Atoi(m[2])
				st.Records = append(st.Records, record{Index: len(st.Records), Category: m[1], Code: code, Text: m[3], ByteStart: off, ByteEnd: end})
			} else {
				st.UnparsedBytes += int64(len(line))
			}
		}
		off += next
		b = b[next:]
	}
	for _, r := range st.Records {
		if r.ByteEnd <= r.ByteStart || r.Line < 0 || r.Col < 0 {
			st.ShapeErrors++
		}
	}
	return st
}

// shownConfig is the part of `tsc --showConfig` CODEYE reads: the root files
// (relative to the config directory), the references and the effective
// compiler options after extends and command-line overrides.
type shownConfig struct {
	Files      []string
	HasFiles   bool
	References []string
	Options    map[string]string
}

// optionProperties are the effective compiler options recorded as target
// properties.
var optionProperties = map[string]string{"module": "module", "moduleResolution": "module_resolution", "target": "target", "strict": "strict", "jsx": "jsx",
	"allowJs": "allow_js", "checkJs": "check_js"}

func parseShowConfig(b []byte) (shownConfig, bool) {
	var raw struct {
		CompilerOptions map[string]json.RawMessage `json:"compilerOptions"`
		Files           *[]string                  `json:"files"`
		References      []struct {
			Path string `json:"path"`
		} `json:"references"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&raw); err != nil || dec.More() {
		return shownConfig{}, false
	}
	c := shownConfig{Options: map[string]string{}}
	if raw.Files != nil {
		c.HasFiles = true
		c.Files = append([]string{}, (*raw.Files)...)
	}
	for _, r := range raw.References {
		c.References = append(c.References, r.Path)
	}
	for k, prop := range optionProperties {
		v, ok := raw.CompilerOptions[k]
		if !ok {
			continue
		}
		var s string
		var bv bool
		switch {
		case json.Unmarshal(v, &s) == nil:
			c.Options[prop] = strings.ToLower(s)
		case json.Unmarshal(v, &bv) == nil:
			c.Options[prop] = fmt.Sprint(bv)
		}
	}
	sort.Strings(c.Files)
	return c, true
}

// versionLine is `tsc --version` output: "Version <major>.<minor>.<patch>".
var versionLine = regexp.MustCompile(`^Version ([0-9]{1,4})\.([0-9]{1,4})\.([0-9]{1,4})$`)

// ParseVersion reads "Version 6.0.3\n" and returns "6.0.3". Development and
// pre-release builds are rejected: the parser is pinned to a release format.
func ParseVersion(stdout []byte) (string, bool) {
	m := versionLine.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2] + "." + m[3], true
}

// TestedSeries is the TypeScript release series the parser, the exit codes
// and the gap codes were measured on.
const TestedSeries = "6.0"

// VersionTested reports a version of the tested series.
func VersionTested(v string) bool { return strings.HasPrefix(v, TestedSeries+".") }
