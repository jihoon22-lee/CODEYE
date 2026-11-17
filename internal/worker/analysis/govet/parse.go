package govet

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Parser identity recorded in every observations document.
const (
	ParserID      = "go-vet-json-v1"
	ParserVersion = "1"
)

// rawDiagnostic is one go vet -json diagnostic before location resolution.
type rawDiagnostic struct {
	PackageKey     string
	Analyzer       string
	Posn, End      string
	Message        string
	SuggestedFixes int
	Related        int
	StreamIndex    int
	JSONPointer    string
	ByteStart      int64
	ByteEnd        int64
}

// AnalyzerError is an analyzer that reported {"error": "..."} instead of diagnostics.
type AnalyzerError struct {
	PackageKey string `json:"package_key"`
	Analyzer   string `json:"analyzer"`
	Message    string `json:"message"`
	RawRef     RawRef `json:"raw_ref"`
}

// vetStream is the result of decoding go vet -json stdout: a sequence of
// top-level JSON objects (one per vetted unit, "{}" when it had nothing).
type vetStream struct {
	Diagnostics    []rawDiagnostic
	AnalyzerErrors []AnalyzerError
	Values         int   // top-level values decoded
	EmptyValues    int   // "{}" values: a unit was vetted but cannot be attributed
	ParsedBytes    int64 // offset after the last decoded value
	UnparsedBytes  int64 // non-whitespace bytes after ParsedBytes (0 on a clean parse)
	ShapeErrors    int   // values that decoded as JSON but not in the vet shape
	DecodeError    string
}

type vetDiagJSON struct {
	Posn           string            `json:"posn"`
	End            string            `json:"end"`
	Message        string            `json:"message"`
	SuggestedFixes []json.RawMessage `json:"suggested_fixes"`
	Related        []json.RawMessage `json:"related"`
}

// jsonPointerToken escapes one RFC 6901 reference token.
func jsonPointerToken(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}

func skipSpace(b []byte, i int64) int64 {
	for i < int64(len(b)) {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// parseVetStream decodes go vet -json stdout. It never turns undecodable
// output into "no findings": UnparsedBytes/ShapeErrors/DecodeError say what
// could not be read, and everything decoded before that point is kept.
func parseVetStream(stdout []byte) vetStream {
	var out vetStream
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		start := skipSpace(stdout, dec.InputOffset())
		if start >= int64(len(stdout)) {
			out.ParsedBytes = int64(len(stdout))
			return out
		}
		var value map[string]json.RawMessage
		if err := dec.Decode(&value); err != nil {
			if errors.Is(err, io.EOF) {
				out.ParsedBytes = int64(len(stdout))
				return out
			}
			out.DecodeError = classifyDecodeError(err)
			out.ParsedBytes = start
			out.UnparsedBytes = int64(len(stdout)) - start
			return out
		}
		end := dec.InputOffset()
		index := out.Values
		out.Values++
		if value == nil {
			out.ShapeErrors++
			continue
		}
		if len(value) == 0 {
			out.EmptyValues++
		}
		pkgs := sortedKeys(value)
		for _, pkg := range pkgs {
			var analyzers map[string]json.RawMessage
			if err := json.Unmarshal(value[pkg], &analyzers); err != nil || analyzers == nil {
				out.ShapeErrors++
				continue
			}
			for _, analyzer := range sortedKeys(analyzers) {
				pointer := "/" + jsonPointerToken(pkg) + "/" + jsonPointerToken(analyzer)
				raw := bytes.TrimSpace(analyzers[analyzer])
				if len(raw) > 0 && raw[0] == '{' {
					var e struct {
						Error *string `json:"error"`
					}
					if err := json.Unmarshal(raw, &e); err != nil || e.Error == nil {
						out.ShapeErrors++
						continue
					}
					out.AnalyzerErrors = append(out.AnalyzerErrors, AnalyzerError{PackageKey: pkg, Analyzer: analyzer, Message: *e.Error,
						RawRef: RawRef{Execution: execVet, Stream: "stdout", StreamIndex: index, JSONPointer: pointer + "/error", ByteStart: start, ByteEnd: end}})
					continue
				}
				var diags []vetDiagJSON
				if err := json.Unmarshal(raw, &diags); err != nil {
					out.ShapeErrors++
					continue
				}
				for i, d := range diags {
					out.Diagnostics = append(out.Diagnostics, rawDiagnostic{
						PackageKey: pkg, Analyzer: analyzer, Posn: d.Posn, End: d.End, Message: d.Message,
						SuggestedFixes: len(d.SuggestedFixes), Related: len(d.Related), StreamIndex: index,
						JSONPointer: pointer + "/" + strconv.Itoa(i), ByteStart: start, ByteEnd: end,
					})
				}
			}
		}
		out.ParsedBytes = end
	}
}

func classifyDecodeError(err error) string {
	var syntax *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return "json_syntax_error"
	case errors.As(err, &typeErr):
		return "json_not_object"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "json_unexpected_eof"
	default:
		return "json_decode_error"
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// listPackage is the subset of `go list -e -json=...` this adapter reads.
type listPackage struct {
	Dir            string
	ImportPath     string
	Name           string
	GoFiles        []string
	CgoFiles       []string
	IgnoredGoFiles []string
	TestGoFiles    []string
	XTestGoFiles   []string
	Incomplete     bool
	Error          *listError
	DepsErrors     []*listError
}

type listError struct {
	Pos string
	Err string
}

// listFields is the exact -json field list passed to go list.
const listFields = "Dir,ImportPath,Name,GoFiles,CgoFiles,IgnoredGoFiles,TestGoFiles,XTestGoFiles,Incomplete,Error,DepsErrors"

type listStream struct {
	Packages      []listPackage
	UnparsedBytes int64
	DecodeError   string
}

func parseListStream(stdout []byte) listStream {
	var out listStream
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		start := skipSpace(stdout, dec.InputOffset())
		if start >= int64(len(stdout)) {
			return out
		}
		var p listPackage
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			out.DecodeError = classifyDecodeError(err)
			out.UnparsedBytes = int64(len(stdout)) - start
			return out
		}
		out.Packages = append(out.Packages, p)
	}
}

var versionPattern = regexp.MustCompile(`^go version (go[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:[a-z0-9.-]*)?)(?: [^ ]+)* ([a-z0-9]+)/([a-z0-9]+)\s*$`)

// parseVersion reads `go version` stdout, e.g. "go version go1.27.4 linux/amd64".
func parseVersion(stdout []byte) (version, platform string, ok bool) {
	m := versionPattern.FindSubmatch(bytes.TrimSpace(stdout))
	if m == nil {
		return "", "", false
	}
	return string(m[1]), string(m[2]) + "/" + string(m[3]), true
}

// testedMinor is the Go minor version whose output shape the parser fixtures pin.
const testedMinor = "go1.27"

func versionTested(v string) bool { return v == testedMinor || strings.HasPrefix(v, testedMinor+".") }

// stderrLine is one classified line of go vet / go list stderr.
type stderrLine struct {
	Kind       string // typecheck | position | go_command | header | other
	Package    string // from the preceding "# pkg" header, if any
	Pos        string // raw "path:line:col" (redacted workspace prefix kept)
	Message    string
	ByteOffset int64
}

var posLine = regexp.MustCompile(`^(.+?\.go):([0-9]+)(?::([0-9]+))?: (.*)$`)

const maxStderrLines = 500

// parseStderr classifies stderr lines without interpreting free text beyond
// stable prefixes. Lines past maxStderrLines are counted, not kept.
func parseStderr(stderr []byte) (lines []stderrLine, dropped int) {
	sc := bufio.NewScanner(bytes.NewReader(stderr))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var offset int64
	current := ""
	for sc.Scan() {
		raw := sc.Text()
		lineOffset := offset
		offset += int64(len(raw)) + 1
		text := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		var l stderrLine
		switch {
		case strings.HasPrefix(text, "# "):
			current = strings.TrimSpace(text[2:])
			l = stderrLine{Kind: "header", Package: current}
		case strings.HasPrefix(text, "vet: "), strings.HasPrefix(text, "vet.exe: "):
			_, detail, _ := strings.Cut(text, ": ")
			l = stderrLine{Kind: "typecheck", Package: current, Message: detail}
			if m := posLine.FindStringSubmatch(detail); m != nil {
				l.Pos, l.Message = posOf(m), m[4]
			}
		case strings.HasPrefix(text, "go: "):
			l = stderrLine{Kind: "go_command", Message: text}
		case strings.HasPrefix(text, "\t") || strings.HasPrefix(text, " "):
			l = stderrLine{Kind: "continuation", Package: current, Message: strings.TrimSpace(text)}
		default:
			if m := posLine.FindStringSubmatch(text); m != nil {
				l = stderrLine{Kind: "position", Package: current, Pos: posOf(m), Message: m[4]}
			} else {
				l = stderrLine{Kind: "other", Package: current, Message: text}
			}
		}
		l.ByteOffset = lineOffset
		if len(lines) >= maxStderrLines {
			dropped++
			continue
		}
		lines = append(lines, l)
	}
	if sc.Err() != nil {
		dropped++
	}
	return lines, dropped
}

func posOf(m []string) string {
	if m[3] == "" {
		return m[1] + ":" + m[2]
	}
	return m[1] + ":" + m[2] + ":" + m[3]
}

// splitPosition splits "path:line[:col]" from the right, so drive letters and
// colons inside the redacted prefix stay in the path.
func splitPosition(p string) (path string, line, col int, ok bool) {
	if p == "" {
		return "", 0, 0, false
	}
	i := strings.LastIndexByte(p, ':')
	if i <= 0 {
		return "", 0, 0, false
	}
	last, err := strconv.Atoi(p[i+1:])
	if err != nil || last < 0 {
		return "", 0, 0, false
	}
	rest := p[:i]
	j := strings.LastIndexByte(rest, ':')
	if j > 0 {
		if l, err := strconv.Atoi(rest[j+1:]); err == nil && l >= 0 {
			return rest[:j], l, last, true
		}
	}
	return rest, last, 0, true
}

// dependencyMarkers are stable go command phrases for unavailable modules
// in offline mode (GOPROXY=off, GOFLAGS unset, GOTOOLCHAIN=local).
var dependencyMarkers = []string{
	"missing go.sum entry",
	"cannot find module providing package",
	"module lookup disabled",
	"import lookup disabled",
	"no required module provides package",
	"go: updates to go.mod needed",
	"inconsistent vendoring",
	"verifying module",
}

func isDependencyError(s string) bool {
	for _, m := range dependencyMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func isToolchainError(s string) bool {
	return strings.Contains(s, "requires go >=") || strings.Contains(s, "toolchain not available") || strings.Contains(s, "GOTOOLCHAIN=local")
}

func isModuleMissing(s string) bool {
	return strings.Contains(s, "does not contain main module") || strings.Contains(s, "go.mod file not found") || strings.Contains(s, "cannot find main module")
}
