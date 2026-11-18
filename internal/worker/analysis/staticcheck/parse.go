package staticcheck

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Parser identity (staticcheck -f json, one JSON object per line).
const (
	ParserID      = "staticcheck-json-v1"
	ParserVersion = "1"
)

// position is staticcheck's {"file","line","column"} (1-based line, byte
// column; file "" and line 0 when unknown).
type position struct {
	File   *string `json:"file"`
	Line   *int    `json:"line"`
	Column *int    `json:"column"`
}

type relatedJSON struct {
	Location *position `json:"location"`
	End      *position `json:"end"`
	Message  *string   `json:"message"`
}

// severityIgnored is the severity -show-ignored gives a problem that an
// in-source //lint:ignore or //lint:file-ignore directive hides.
const severityIgnored = "ignored"

// recordJSON is one output line of `staticcheck -f json` 2026.2. Unknown
// fields are a shape error: the parser is pinned to this version's format.
type recordJSON struct {
	Code     *string       `json:"code"`
	Severity *string       `json:"severity"`
	Location *position     `json:"location"`
	End      *position     `json:"end"`
	Message  *string       `json:"message"`
	Related  []relatedJSON `json:"related"`
}

type pos struct {
	File      string
	Line, Col int
}

type relatedRecord struct {
	Start, End pos
	Message    string
}

// record is one parsed line with its byte range in stdout.
type record struct {
	Index              int
	Code, Severity     string
	Start, End         pos
	Message            string
	Related            []relatedRecord
	ByteStart, ByteEnd int64
}

// stream is the parse of stdout. UnparsedBytes > 0 or a DecodeError means
// the rest of stdout could not be read; ShapeErrors counts JSON values that
// are not staticcheck records. Neither may ever become "no findings".
type stream struct {
	Records       []record
	Values        int
	ShapeErrors   int
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

func (p *position) valid() bool {
	return p != nil && p.File != nil && p.Line != nil && p.Column != nil && *p.Line >= 0 && *p.Column >= 0
}

func (p *position) value() pos { return pos{File: *p.File, Line: *p.Line, Col: *p.Column} }

// parseStream reads stdout value by value with byte offsets. Each value is
// decoded strictly; a value of the wrong shape is counted, not dropped.
func parseStream(stdout []byte) stream {
	var out stream
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
		end := dec.InputOffset()
		index := out.Values
		out.Values++
		var r recordJSON
		strict := json.NewDecoder(bytes.NewReader(raw))
		strict.DisallowUnknownFields()
		if err := strict.Decode(&r); err != nil || r.Code == nil || *r.Code == "" || r.Severity == nil || r.Message == nil || !r.Location.valid() || !r.End.valid() {
			out.ShapeErrors++
			continue
		}
		rec := record{Index: index, Code: *r.Code, Severity: *r.Severity, Start: r.Location.value(), End: r.End.value(), Message: *r.Message, ByteStart: start, ByteEnd: end}
		bad := false
		for _, rel := range r.Related {
			if !rel.Location.valid() || !rel.End.valid() || rel.Message == nil {
				bad = true
				break
			}
			rec.Related = append(rec.Related, relatedRecord{Start: rel.Location.value(), End: rel.End.value(), Message: *rel.Message})
		}
		if bad {
			out.ShapeErrors++
			continue
		}
		out.Records = append(out.Records, rec)
	}
}

// versionPattern reads `staticcheck -version`: "staticcheck 2026.2.2 (0.8.2)".
var versionPattern = regexp.MustCompile(`^staticcheck ([0-9]{4}\.[0-9]+(?:\.[0-9]+)?(?:-[0-9A-Za-z.]+)?) \(([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?)\)$`)

// ParseVersion returns "2026.2.2 (0.8.2)" (release and module version).
// Development builds ("devel", no release) are not recognized.
func ParseVersion(stdout []byte) (string, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return m[1] + " (" + m[2] + ")", true
}

// TestedRelease is the staticcheck release series whose output the parser
// fixtures pin (captured from 2026.2.2).
const TestedRelease = "2026.2"

// VersionTested reports whether a parsed version is of the tested series.
func VersionTested(v string) bool {
	rel, _, _ := strings.Cut(v, " ")
	return rel == TestedRelease || strings.HasPrefix(rel, TestedRelease+".")
}

// stderrLine is one classified stderr line of staticcheck.
type stderrLine struct {
	Kind    string // no_packages | fatal | other
	Message string
}

const maxStderrLines = 200

// parseStderr classifies staticcheck stderr by stable prefixes only.
func parseStderr(stderr []byte) (lines []stderrLine, dropped int) {
	sc := bufio.NewScanner(bytes.NewReader(stderr))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		text := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		l := stderrLine{Kind: "other", Message: text}
		switch {
		case strings.HasPrefix(text, "warning: ") && strings.HasSuffix(text, "matched no packages"):
			l.Kind = "no_packages"
		case strings.HasPrefix(text, "err: "):
			l.Kind = "fatal"
		}
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

// compileHeader reads "# <import path>" from a compile record message and
// the first "path:line:col: msg" position after it.
var compilePos = regexp.MustCompile(`(?m)^([^\s:][^:\n]*\.go):([0-9]+)(?::([0-9]+))?: `)

func compileUnit(message string) string {
	first, _, _ := strings.Cut(message, "\n")
	if strings.HasPrefix(first, "# ") {
		return strings.TrimSpace(first[2:])
	}
	return ""
}
