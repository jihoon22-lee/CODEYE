package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Source text travels to the model with its line numbers. Excerpt.Text holds
// the raw lines everywhere in the worker; only the JSON encoding of an
// excerpt that carries text (a request: records never store text) prefixes
// each line with "<line>| ", and decoding such an encoding removes the
// prefixes again. The model cites a line range of an excerpt as
// "<evidence id>:<start>-<end>" (Citation); the worker narrows the citation
// to the evidence ID of that range (ExcerptID), so stored records only hold
// plain IDs.

// linePrefix separates a line number from the source line.
const linePrefix = "| "

type plainExcerpt Excerpt

// MarshalJSON numbers the lines of an excerpt that carries text. An excerpt
// without text (every stored record) encodes exactly as its fields.
func (e Excerpt) MarshalJSON() ([]byte, error) {
	if e.Text != "" {
		e.Text = NumberLines(e.Text, e.Start)
	}
	return json.Marshal(plainExcerpt(e))
}

// UnmarshalJSON decodes an excerpt strictly (unknown fields are rejected, as
// for the record around it) and removes line numbers that match its range.
func (e *Excerpt) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var p plainExcerpt
	if err := dec.Decode(&p); err != nil {
		return err
	}
	if p.Text != "" {
		if raw, ok := StripLineNumbers(p.Text, p.Start); ok {
			p.Text = raw
		}
	}
	*e = Excerpt(p)
	return nil
}

// NumberLines prefixes every line of text with its line number, the first
// line being start.
func NumberLines(text string, start int) string {
	var b strings.Builder
	b.Grow(len(text) + 8*strings.Count(text, "\n") + 8)
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(start + i))
		b.WriteString(linePrefix)
		b.WriteString(line)
	}
	return b.String()
}

// StripLineNumbers reverses NumberLines; ok is false when a line does not
// carry the expected number.
func StripLineNumbers(text string, start int) (string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		prefix := strconv.Itoa(start+i) + linePrefix
		if !strings.HasPrefix(line, prefix) {
			return "", false
		}
		lines[i] = line[len(prefix):]
	}
	return strings.Join(lines, "\n"), true
}

// NumberedLen is the JSON-encoded size of text once its lines are numbered
// from start (the size an excerpt's text takes in a request).
func NumberedLen(text string, start int) int {
	n := JSONLen(text)
	for i := range strings.Count(text, "\n") + 1 {
		n += len(strconv.Itoa(start+i)) + len(linePrefix)
	}
	return n
}

// MaxCitedLines bounds a narrowed citation: a finding points at the lines
// that matter, not at a whole file.
const MaxCitedLines = 80

// WholeCitationLines is the longest excerpt a model may cite without a line
// range; a longer code or test excerpt (a whole file) needs one.
const WholeCitationLines = MaxCitedLines

// Citation is one parsed model citation: an evidence ID and, when Ranged,
// the cited lines inside that excerpt.
type Citation struct {
	ID         string
	Start, End int
	Ranged     bool
}

var citation = regexp.MustCompile(`^(ev_[0-9a-f]{24}):([1-9][0-9]{0,6})(?:-([1-9][0-9]{0,6}))?$`)

// ParseCitation reads "ev_…" or "ev_…:<start>-<end>" ("ev_…:<line>" for one
// line). ok is false for a malformed range; an unranged string is returned
// as is for the caller's ID check.
func ParseCitation(s string) (Citation, bool) {
	if !strings.Contains(s, ":") {
		return Citation{ID: s}, true
	}
	m := citation.FindStringSubmatch(s)
	if m == nil {
		return Citation{}, false
	}
	start, _ := strconv.Atoi(m[2])
	end := start
	if m[3] != "" {
		end, _ = strconv.Atoi(m[3])
	}
	if end < start {
		return Citation{}, false
	}
	return Citation{ID: m[1], Start: start, End: end, Ranged: true}, true
}

// String is the citation in the model's notation.
func (c Citation) String() string {
	if !c.Ranged {
		return c.ID
	}
	return fmt.Sprintf("%s:%d-%d", c.ID, c.Start, c.End)
}

// Narrow is the excerpt of the cited lines of e: the same blob, kind and
// source text, origin cited and the ID of the narrower range. A range equal
// to e is e itself.
func Narrow(e Excerpt, snapshot string, start, end int) Excerpt {
	if start == e.Start && end == e.End {
		return e
	}
	n := Excerpt{ID: ExcerptID(snapshot, e.Path, e.SHA256, start, end), Path: e.Path, SHA256: e.SHA256, Start: start, End: end,
		Kind: e.Kind, Origin: OriginCited}
	if e.Text != "" {
		lines := strings.Split(e.Text, "\n")
		if start-e.Start < len(lines) {
			n.Text = strings.Join(lines[start-e.Start:min(len(lines), end-e.Start+1)], "\n")
		}
	}
	return n
}
