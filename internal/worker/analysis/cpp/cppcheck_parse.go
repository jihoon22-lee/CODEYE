package cpp

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Parser identity: cppcheck --output-format=xmlv2 on stderr (results
// version 2, measured with cppcheck 2.22.0) plus the stdout progress lines.
const (
	CppcheckParserID      = "cppcheck-xml-v2"
	CppcheckParserVersion = "1"
)

// ccLocation is one <location> of an <error>.
type ccLocation struct {
	File      string
	Line, Col int
	Info      string
}

// ccRecord is one <error> element with its byte range in stderr.
type ccRecord struct {
	Index              int
	ID, Severity, Msg  string
	CWE                string
	File0              string
	Inconclusive       bool
	Locations          []ccLocation
	ByteStart, ByteEnd int64
}

// ccStream is the parse of the XML document. UnparsedBytes > 0 or a
// DecodeError means the rest could not be read; ShapeErrors counts elements
// or attributes that are not of the pinned format. Neither is ever "no
// findings".
type ccStream struct {
	Records       []ccRecord
	Values        int
	ShapeErrors   int
	UnparsedBytes int64
	DecodeError   string
	Version       string
	Complete      bool // </results> was read
}

var (
	errorAttrs    = map[string]bool{"id": true, "severity": true, "msg": true, "verbose": true, "cwe": true, "hash": true, "inconclusive": true, "file0": true, "remark": true}
	locationAttrs = map[string]bool{"file": true, "line": true, "column": true, "info": true}
)

func attrMap(se xml.StartElement, allowed map[string]bool) (map[string]string, bool) {
	out := map[string]string{}
	for _, a := range se.Attr {
		if a.Name.Space != "" || !allowed[a.Name.Local] {
			return nil, false
		}
		if _, dup := out[a.Name.Local]; dup {
			return nil, false
		}
		out[a.Name.Local] = a.Value
	}
	return out, true
}

func isSpace(b []byte) bool { return len(bytes.TrimSpace(b)) == 0 }

// parseCppcheckXML walks the tokens of the version 2 document strictly:
// results(version=2) → cppcheck(version) → errors → error* (location*,
// symbol*). Unknown elements or attributes are shape errors; text outside
// <symbol> must be white space; nothing may follow </results>.
func parseCppcheckXML(data []byte) ccStream {
	var out ccStream
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	fail := func(err error, at int64) ccStream {
		switch {
		case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
			out.DecodeError = "unexpected_eof"
		default:
			out.DecodeError = "syntax_error"
		}
		out.UnparsedBytes = int64(len(data)) - at
		return out
	}
	// depth path of open elements we understand.
	type frame struct {
		name string
		rec  *ccRecord
	}
	var stack []frame
	var cur *ccRecord
	symbolText := false
	bad := false // the current <error> is malformed
	sawResults := false
	for {
		start := dec.InputOffset()
		tok, err := dec.Token()
		if err == io.EOF {
			if !out.Complete {
				if len(stack) > 0 || !sawResults {
					return fail(io.ErrUnexpectedEOF, start)
				}
			}
			return out
		}
		if err != nil {
			return fail(err, start)
		}
		if out.Complete {
			// After </results> only white space is allowed.
			if cd, ok := tok.(xml.CharData); ok && isSpace(cd) {
				continue
			}
			out.UnparsedBytes = int64(len(data)) - start
			out.DecodeError = "trailing_data"
			return out
		}
		switch t := tok.(type) {
		case xml.ProcInst:
			if t.Target != "xml" || len(stack) > 0 || sawResults {
				out.ShapeErrors++
			}
		case xml.Directive, xml.Comment:
			// No DOCTYPE (entities) and no comments in the pinned format.
			out.ShapeErrors++
		case xml.CharData:
			if symbolText {
				continue
			}
			if !isSpace(t) {
				out.ShapeErrors++
			}
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1].name
			}
			name := t.Name.Local
			switch {
			case parent == "" && name == "results" && !sawResults:
				sawResults = true
				a, ok := attrMap(t, map[string]bool{"version": true})
				if !ok || a["version"] != "2" {
					out.ShapeErrors++
				}
			case parent == "results" && name == "cppcheck":
				a, ok := attrMap(t, map[string]bool{"version": true})
				if !ok || a["version"] == "" {
					out.ShapeErrors++
				} else {
					out.Version = a["version"]
				}
			case parent == "results" && name == "errors":
			case parent == "errors" && name == "error":
				out.Values++
				a, ok := attrMap(t, errorAttrs)
				rec := &ccRecord{Index: out.Values - 1, ByteStart: start}
				bad = !ok || a["id"] == "" || a["severity"] == "" || a["msg"] == ""
				if ok {
					rec.ID, rec.Severity, rec.Msg, rec.CWE, rec.File0 = a["id"], a["severity"], a["msg"], a["cwe"], a["file0"]
					if v, has := a["inconclusive"]; has {
						rec.Inconclusive = v == "true"
						bad = bad || (v != "true" && v != "false")
					}
				}
				cur = rec
				stack = append(stack, frame{name: name, rec: rec})
				continue
			case parent == "error" && name == "location":
				a, ok := attrMap(t, locationAttrs)
				line, e1 := strconv.Atoi(a["line"])
				col, e2 := strconv.Atoi(a["column"])
				if !ok || a["file"] == "" || e1 != nil || e2 != nil || line < 0 || col < 0 {
					bad = true
				} else if cur != nil {
					cur.Locations = append(cur.Locations, ccLocation{File: a["file"], Line: line, Col: col, Info: a["info"]})
				}
			case parent == "error" && name == "symbol":
				if len(t.Attr) != 0 {
					bad = true
				}
				symbolText = true
			default:
				if parent == "error" {
					bad = true
				} else {
					out.ShapeErrors++
				}
				if err := dec.Skip(); err != nil {
					return fail(err, start)
				}
				continue
			}
			stack = append(stack, frame{name: name})
		case xml.EndElement:
			if len(stack) == 0 {
				return fail(errors.New("unbalanced"), start)
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch top.name {
			case "symbol":
				symbolText = false
			case "error":
				if top.rec != nil {
					top.rec.ByteEnd = dec.InputOffset()
					if bad {
						out.ShapeErrors++
					} else {
						out.Records = append(out.Records, *top.rec)
					}
				}
				cur, bad = nil, false
			case "results":
				out.Complete = true
			}
		}
	}
}

// Stdout progress lines of cppcheck 2.22 (without --quiet).
var (
	ccChecking       = regexp.MustCompile(`^Checking (.+) \.\.\.$`)
	ccCheckingConfig = regexp.MustCompile(`^Checking (.+?): (.*)\.\.\.$`)
	ccProgress       = regexp.MustCompile(`^[0-9]+/[0-9]+ files checked [0-9]+% done$`)
)

// ccStdout is the classified stdout of a check run.
type ccStdout struct {
	Checked []string // files in "Checking <file> ..." order
	Configs int      // "Checking <file>: <cfg>..." lines
	Fatal   []string // "cppcheck: error: ..." and other "cppcheck: " lines
	Other   int
}

const maxStdoutLines = 1 << 20

func parseCppcheckStdout(stdout []byte) ccStdout {
	var out ccStdout
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		if n > maxStdoutLines {
			out.Other++
			break
		}
		l := strings.TrimRight(sc.Text(), "\r")
		switch {
		case strings.TrimSpace(l) == "":
		case strings.HasPrefix(l, "cppcheck: "):
			out.Fatal = append(out.Fatal, l)
		case ccChecking.MatchString(l):
			out.Checked = append(out.Checked, ccChecking.FindStringSubmatch(l)[1])
		case ccCheckingConfig.MatchString(l):
			out.Configs++
		case ccProgress.MatchString(l):
		default:
			out.Other++
		}
	}
	if sc.Err() != nil {
		out.Other++
	}
	return out
}

// cppcheckVersion reads `cppcheck --version`: "Cppcheck 2.22.0".
var cppcheckVersion = regexp.MustCompile(`^Cppcheck ([0-9]+\.[0-9]+(?:\.[0-9]+)?)$`)

// ParseCppcheckVersion returns "2.22.0". Other products ("Cppcheck Premium")
// and development builds ("2.23 dev") are not recognized.
func ParseCppcheckVersion(stdout []byte) (string, bool) {
	s := strings.TrimSpace(string(stdout))
	if strings.Contains(s, "\n") {
		return "", false
	}
	m := cppcheckVersion.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// CppcheckTestedRelease is the series whose output the parser fixtures pin.
const CppcheckTestedRelease = "2.22"

// CppcheckVersionTested reports a version of the tested series.
func CppcheckVersionTested(v string) bool {
	return v == CppcheckTestedRelease || strings.HasPrefix(v, CppcheckTestedRelease+".")
}
