package eslint

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Parser identity: ESLint's built-in json formatter (one JSON array of file
// results on stdout), pinned to the ESLint 10 result shape.
const (
	ParserID      = "eslint-json-v1"
	ParserVersion = "1"
)

// resultJSON is one file result of `eslint --format json` (ESLint 10.15).
// Unknown fields are a shape error.
type resultJSON struct {
	FilePath            *string           `json:"filePath"`
	Messages            []json.RawMessage `json:"messages"`
	SuppressedMessages  []json.RawMessage `json:"suppressedMessages"`
	ErrorCount          *int              `json:"errorCount"`
	FatalErrorCount     *int              `json:"fatalErrorCount"`
	WarningCount        *int              `json:"warningCount"`
	FixableErrorCount   *int              `json:"fixableErrorCount"`
	FixableWarningCount *int              `json:"fixableWarningCount"`
	Source              *string           `json:"source"`
	Output              *string           `json:"output"`
	UsedDeprecatedRules []json.RawMessage `json:"usedDeprecatedRules"`
	Stats               json.RawMessage   `json:"stats"`
}

// messageJSON is one lint message.
type messageJSON struct {
	RuleID       *string           `json:"ruleId"`
	Severity     *int              `json:"severity"`
	Message      *string           `json:"message"`
	Line         *int              `json:"line"`
	Column       *int              `json:"column"`
	EndLine      *int              `json:"endLine"`
	EndColumn    *int              `json:"endColumn"`
	MessageID    *string           `json:"messageId"`
	NodeType     *string           `json:"nodeType"`
	Fatal        *bool             `json:"fatal"`
	Fix          json.RawMessage   `json:"fix"`
	Suggestions  []json.RawMessage `json:"suggestions"`
	Suppressions json.RawMessage   `json:"suppressions"`
}

// message is one parsed lint message with its byte range in stdout.
type message struct {
	Index              int // global message index in stdout
	Result, InResult   int
	RuleID             string // "" = null
	Severity           int
	Text               string
	Line, Col          int // 1-based, UTF-16 columns; 0 = none
	EndLine, EndCol    int
	Fatal              bool
	Fixes              int
	ByteStart, ByteEnd int64
	Pointer            string
}

// result is one parsed file result.
type result struct {
	Index      int
	FilePath   string
	Messages   []message
	Suppressed int
}

// stream is the parse of stdout. UnparsedBytes > 0, ShapeErrors > 0 or a
// DecodeError mean the output was not fully read: never "no findings".
type stream struct {
	Results       []result
	Values        int // file results read
	ShapeErrors   int
	UnparsedBytes int64
	DecodeError   string
}

func skip(b []byte, i int64, extra byte) int64 {
	for i < int64(len(b)) {
		switch b[i] {
		case ' ', '\t', '\n', '\r', extra:
			i++
		default:
			return i
		}
	}
	return i
}

// arrayRanges returns the byte ranges of the elements of the JSON array b.
func arrayRanges(b []byte) ([][2]int64, int64, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, 0, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, 0, errors.New("not an array")
	}
	var out [][2]int64
	for dec.More() {
		start := skip(b, dec.InputOffset(), ',')
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out, 0, err
		}
		out = append(out, [2]int64{start, dec.InputOffset()})
	}
	if _, err := dec.Token(); err != nil {
		return out, 0, err
	}
	return out, dec.InputOffset(), nil
}

// fieldRange returns the byte range of the value of key in the JSON object b.
func fieldRange(b []byte, key string) (int64, int64, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return 0, 0, false
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0, false
		}
		k, _ := tok.(string)
		start := skip(b, dec.InputOffset(), ':')
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, 0, false
		}
		if k == key {
			return start, dec.InputOffset(), true
		}
	}
	return 0, 0, false
}

func strict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

// parseStream reads ESLint json stdout. The output must be exactly one JSON
// array of file results (and a trailing newline); every result and message
// must have the ESLint 10 shape and consistent counts.
func parseStream(b []byte) stream {
	var st stream
	if len(bytes.TrimSpace(b)) == 0 {
		st.DecodeError = "empty output"
		return st
	}
	ranges, end, err := arrayRanges(b)
	if err != nil {
		st.DecodeError = "not one JSON array of results"
		st.UnparsedBytes = int64(len(b))
		return st
	}
	if rest := bytes.TrimSpace(b[end:]); len(rest) > 0 {
		st.UnparsedBytes = int64(len(rest))
	}
	global := 0
	for ri, rr := range ranges {
		raw := b[rr[0]:rr[1]]
		st.Values++
		var r resultJSON
		if strict(raw, &r) != nil || r.FilePath == nil || r.Messages == nil || r.SuppressedMessages == nil || r.ErrorCount == nil || r.FatalErrorCount == nil || r.WarningCount == nil {
			st.ShapeErrors++
			continue
		}
		res := result{Index: ri, FilePath: *r.FilePath, Suppressed: len(r.SuppressedMessages)}
		ms, me, ok := fieldRange(raw, "messages")
		if !ok {
			st.ShapeErrors++
			continue
		}
		mranges, _, err := arrayRanges(raw[ms:me])
		if err != nil || len(mranges) != len(r.Messages) {
			st.ShapeErrors++
			continue
		}
		errorsN, warnings, fatal, bad := 0, 0, 0, false
		for mi, mr := range mranges {
			var m messageJSON
			mraw := raw[ms+mr[0] : ms+mr[1]]
			if strict(mraw, &m) != nil || m.Severity == nil || m.Message == nil || (*m.Severity != 1 && *m.Severity != 2) || m.Suppressions != nil {
				bad = true
				break
			}
			msg := message{Index: global, Result: ri, InResult: mi, Severity: *m.Severity, Text: *m.Message,
				ByteStart: rr[0] + ms + mr[0], ByteEnd: rr[0] + ms + mr[1], Pointer: "/" + strconv.Itoa(ri) + "/messages/" + strconv.Itoa(mi)}
			if m.RuleID != nil {
				msg.RuleID = *m.RuleID
			}
			for _, p := range []struct {
				src *int
				dst *int
			}{{m.Line, &msg.Line}, {m.Column, &msg.Col}, {m.EndLine, &msg.EndLine}, {m.EndColumn, &msg.EndCol}} {
				if p.src != nil {
					if *p.src < 0 {
						bad = true
					}
					*p.dst = *p.src
				}
			}
			msg.Fatal = m.Fatal != nil && *m.Fatal
			if len(m.Fix) > 0 && string(m.Fix) != "null" {
				msg.Fixes++
			}
			msg.Fixes += len(m.Suggestions)
			switch {
			case msg.Fatal:
				fatal++
				errorsN++
			case msg.Severity == 2:
				errorsN++
			default:
				warnings++
			}
			global++
			res.Messages = append(res.Messages, msg)
		}
		if bad || errorsN != *r.ErrorCount || fatal != *r.FatalErrorCount || warnings != *r.WarningCount {
			st.ShapeErrors++
			continue
		}
		st.Results = append(st.Results, res)
	}
	return st
}

// stderr line kinds.
var (
	nodeWarning = regexp.MustCompile(`^\(node:\d+\) ([A-Za-z]+Warning): `)
	traceHint   = regexp.MustCompile("^\\(Use `node --trace-warnings")
)

type stderrInfo struct {
	Warnings []string // node process warning names
	Crash    bool     // "Oops! Something went wrong!"
	Other    int      // other non-empty lines
	Dropped  int
}

func parseStderr(b []byte) stderrInfo {
	var out stderrInfo
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		n++
		if n > 2000 {
			out.Dropped++
			continue
		}
		switch {
		case strings.TrimSpace(l) == "":
		case nodeWarning.MatchString(l):
			out.Warnings = append(out.Warnings, nodeWarning.FindStringSubmatch(l)[1])
		case traceHint.MatchString(l):
		case strings.Contains(l, "Oops! Something went wrong!"):
			out.Crash = true
		default:
			out.Other++
		}
	}
	return out
}

// versionLine is `eslint --version` output: v<major>.<minor>.<patch>.
var versionLine = regexp.MustCompile(`^v([0-9]{1,4})\.([0-9]{1,4})\.([0-9]{1,4})$`)

// ParseVersion reads "v10.15.0\n" and returns "10.15.0". Pre-release and
// development builds are rejected: the parser is pinned to a release format.
func ParseVersion(stdout []byte) (string, bool) {
	m := versionLine.FindStringSubmatch(strings.TrimSpace(string(stdout)))
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2] + "." + m[3], true
}

// TestedSeries is the ESLint release series the parser and the exit-code
// table were measured on.
const TestedSeries = "10.15"

// VersionTested reports a version of the tested series.
func VersionTested(v string) bool { return strings.HasPrefix(v, TestedSeries+".") }

// programDiagnostic is the managed codeye/program-diagnostics message of the
// type-aware task: "TS<code> <program|file>: <text>".
var programDiagnostic = regexp.MustCompile(`(?s)^TS([0-9]{1,6}) (program|file): (.*)$`)

func parseProgramDiagnostic(text string) (int, string, string, bool) {
	m := programDiagnostic.FindStringSubmatch(text)
	if m == nil {
		return 0, "", "", false
	}
	code, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", "", false
	}
	return code, m[2], m[3], true
}

func (m message) String() string { return fmt.Sprintf("%s %d:%d %s", m.RuleID, m.Line, m.Col, m.Text) }
