package faildoc

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Event is the identity and exact half-open byte range of a complete JSON
// failure line in the redacted test.run stdout. The line-ending LF is excluded.
type Event struct {
	Package, Test      string
	ByteStart, ByteEnd int
}

type testKey struct{ pkg, name string }

type parsedEvent struct {
	Action, Package, Test string
}

// Failures selects named, top-level tests with an unambiguous run/fail pair
// and a later failed package result. Output messages are never decoded as
// events, including JSON written by a test through t.Log or stdout.
func Failures(stdout []byte) []Event {
	if len(stdout) == 0 || len(stdout) > MaxStdoutBytes {
		return nil
	}
	type runState struct {
		runs, terminals int
		failure         Event
	}
	type packageState struct {
		terminals, failedAt int
	}
	tests := map[testKey]runState{}
	packages := map[string]packageState{}
	for offset := 0; offset < len(stdout); {
		line, _, complete := bytes.Cut(stdout[offset:], []byte{'\n'})
		if !complete {
			break
		}
		start, end := offset, offset+len(line)
		offset = end + 1
		if len(line) == 0 || len(line) > MaxEventBytes {
			continue
		}
		var e parsedEvent
		if json.Unmarshal(line, &e) != nil || !validPackage(e.Package) ||
			(e.Action != "run" && e.Action != "pass" && e.Action != "fail" && e.Action != "skip") ||
			!distinctEventFields(line) {
			continue
		}
		if e.Test == "" {
			if e.Action == "run" {
				continue
			}
			p := packages[e.Package]
			p.terminals++
			if e.Action == "fail" {
				p.failedAt = start
			}
			packages[e.Package] = p
			continue
		}
		if !TestName(e.Test) {
			continue
		}
		key := testKey{e.Package, e.Test}
		s := tests[key]
		if e.Action == "run" {
			s.runs++
		} else {
			s.terminals++
			if e.Action == "fail" && s.runs == 1 {
				s.failure = Event{e.Package, e.Test, start, end}
			}
		}
		tests[key] = s
	}
	var out []Event
	for key, s := range tests {
		p := packages[key.pkg]
		if s.runs == 1 && s.terminals == 1 && s.failure.Test != "" && p.terminals == 1 && p.failedAt > s.failure.ByteEnd {
			out = append(out, s.failure)
		}
	}
	slices.SortFunc(out, func(a, b Event) int { return a.ByteStart - b.ByteStart })
	return out
}

// Go emits exact field names. Reject duplicate or case-folded identity keys
// so a line cannot represent a different event to another JSON reader.
func distinctEventFields(line []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	fields := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		name, ok := tok.(string)
		if err != nil || !ok || fields[name] {
			return false
		}
		var value json.RawMessage
		if dec.Decode(&value) != nil {
			return false
		}
		switch name {
		case "Action", "Package", "Test", "Time", "Elapsed":
		default:
			return false
		}
		fields[name] = true
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return false
	}
	return fields["Action"] && fields["Package"] && dec.Decode(new(json.RawMessage)) == io.EOF
}

// ValidateEvents independently matches every recorded range against the
// original complete stdout bytes. It need not read stderr or source text.
func ValidateEvents(doc Document, stdout []byte) error {
	if len(stdout) > MaxStdoutBytes || doc.StdoutSHA256 != domain.Digest(stdout) || len(doc.Failures) == 0 || len(doc.Failures) > MaxFailures {
		return invalid
	}
	events := Failures(stdout)
	byStart := make(map[int]Event, len(events))
	for _, e := range events {
		byStart[e.ByteStart] = e
	}
	for _, f := range doc.Failures {
		if e, ok := byStart[f.ByteStart]; !ok || e.ByteEnd != f.ByteEnd || e.Package != f.Package || e.Test != f.Test {
			return invalid
		}
	}
	return nil
}
