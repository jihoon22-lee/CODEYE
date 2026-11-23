package review

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Citations (docs/REVIEW.md#인용). The model cites a code or test excerpt
// of its request as "<evidence id>:<start>-<end>". The worker checks that
// the range lies inside that excerpt and replaces the citation with the
// evidence ID of the range itself (record.Narrow): the same blob, kind and
// a deterministic ID, origin cited (a range that is exactly an excerpt
// already sent, or one an earlier record holds, is that excerpt). Findings
// and report excerpts then point at the cited lines, not at a whole file.
// A range outside the excerpt, a malformed range or a long excerpt cited
// without one is an output error the repair request names. Lines the
// request did not send are an integrity error; the other faults are of form
// only (the excerpt is real and was sent), and an item kept after the
// repair resolves them with degrade.

// Output error codes of citations.
const (
	// errCitationInvalid: a malformed range, one longer than
	// record.MaxCitedLines or a range on a document excerpt (form).
	errCitationInvalid = "citation_range_invalid"
	// errCitationMissing: a long code or test excerpt cited without a range (form).
	errCitationMissing = "citation_range_missing"
	// errCitationOutside: a range with lines the excerpt does not hold (integrity).
	errCitationOutside = "citation_range_outside"
)

// Limitations of a citation the platform resolved after the repair.
const (
	// limCitationUnspecified: the citation stands for the whole sent excerpt
	// it names; the lines were checked against the snapshot, only the
	// precision of the range is lost.
	limCitationUnspecified = "citation_range_unspecified"
	// limCitationFromCandidate: a verify citation of a long excerpt without
	// a usable range stands for the candidate's own cited range inside it.
	limCitationFromCandidate = "citation_range_from_candidate"
	// limCitationListTrimmed: repeated IDs were removed or the list was cut
	// to its limit, keeping the first ones.
	limCitationListTrimmed = "citation_list_trimmed"
)

// citeIssue is one citation of a list not accepted as given: its position
// in the list, the sent excerpt it names ("" when none) and the code.
type citeIssue struct {
	pos  int
	id   string
	code string
}

// citationMode is how one evidence list is resolved.
type citationMode int

const (
	// citeNarrow: a range narrows to its own ID; a code or test excerpt of
	// more than record.WholeCitationLines lines needs one.
	citeNarrow citationMode = iota
	// citeOptional: a range narrows when given.
	citeOptional
	// citeStrip: a range is checked and the excerpt's own ID kept.
	citeStrip
)

// citer resolves the citations of one role's output against the excerpts
// it sent and keeps the narrowed excerpts it derived.
type citer struct {
	snapshot string
	sent     map[string]record.Excerpt
	// known are excerpts an earlier record holds that the request did not
	// resend: a range that narrows to one of their IDs resolves to that
	// entry unchanged (record.CarriesUpstreamEvidence).
	known    map[string]record.Excerpt
	narrowed map[string]record.Excerpt
	order    []string
}

func newCiter(snapshot string, sent ...[]record.Excerpt) *citer {
	c := &citer{snapshot: snapshot, sent: map[string]record.Excerpt{}, narrowed: map[string]record.Excerpt{}}
	for _, list := range sent {
		for _, e := range list {
			if _, ok := c.sent[e.ID]; !ok {
				c.sent[e.ID] = e
			}
		}
	}
	return c
}

// resolve returns the evidence IDs of list (a narrowed range replaced by
// its ID), the IDs of the excerpts cited (base) and error codes. An
// unknown ID is kept so the caller's ID check reports it.
func (c *citer) resolve(list []string, mode citationMode) (ids, base, errs []string) {
	ids, base, issues := c.resolveIssues(list, mode)
	for _, is := range issues {
		errs = addLim(errs, is.code)
	}
	return ids, base, errs
}

// resolveIssues is resolve with each fault and the sent excerpt it names.
// A citation naming a sent excerpt resolves to that excerpt's ID even when
// its range is malformed, so only an ID the request did not hold reaches
// the caller's ID check as unknown.
func (c *citer) resolveIssues(list []string, mode citationMode) (ids, base []string, issues []citeIssue) {
	if list == nil {
		return nil, nil, nil
	}
	ids, base = make([]string, 0, len(list)), make([]string, 0, len(list))
	for i, raw := range list {
		cit, ok := record.ParseCitation(raw)
		if !ok {
			prefix, _, _ := strings.Cut(raw, ":")
			if e, sent := c.sent[strings.TrimSpace(prefix)]; sent {
				ids, base = append(ids, e.ID), append(base, e.ID)
				issues = append(issues, citeIssue{i, e.ID, errCitationInvalid})
				continue
			}
			ids, base = append(ids, raw), append(base, raw)
			issues = append(issues, citeIssue{i, "", errCitationInvalid})
			continue
		}
		e, known := c.sent[cit.ID]
		if !known {
			ids, base = append(ids, cit.ID), append(base, cit.ID)
			continue
		}
		base = append(base, e.ID)
		source := e.Kind == "code" || e.Kind == "test"
		if !cit.Ranged {
			if mode == citeNarrow && source && e.Origin != record.OriginCited && e.End-e.Start+1 > record.WholeCitationLines {
				issues = append(issues, citeIssue{i, e.ID, errCitationMissing})
			}
			ids = append(ids, e.ID)
			continue
		}
		if cit.Start < e.Start || cit.End > e.End {
			ids = append(ids, e.ID)
			issues = append(issues, citeIssue{i, e.ID, errCitationOutside})
			continue
		}
		if !source || cit.End-cit.Start+1 > record.MaxCitedLines {
			ids = append(ids, e.ID)
			issues = append(issues, citeIssue{i, e.ID, errCitationInvalid})
			continue
		}
		if mode == citeStrip {
			ids = append(ids, e.ID)
			continue
		}
		n := record.Narrow(e, c.snapshot, cit.Start, cit.End)
		if _, sent := c.sent[n.ID]; !sent {
			if _, seen := c.narrowed[n.ID]; !seen {
				if k, ok := c.known[n.ID]; ok {
					n = k
				}
				c.narrowed[n.ID] = n
				c.order = append(c.order, n.ID)
			}
		}
		ids = append(ids, n.ID)
	}
	return ids, base, issues
}

// degrade resolves the list of an item the platform keeps after the format
// repair (docs/REVIEW.md#출력-오류-분류). A citation whose only fault is its
// form keeps the sent excerpt it names: the ranges of hints that lie inside
// that excerpt (a verify item's candidate citations), otherwise the whole
// excerpt. Repeated IDs go and the list keeps its first limit known IDs;
// an ID the request did not hold stays for the caller's check. lims names
// what was done; errs holds the faults that are not of form (lines outside
// the excerpt, a malformed citation of an unknown ID).
func (c *citer) degrade(list []string, mode citationMode, limit int, hints []string) (ids, lims, errs []string) {
	resolved, _, issues := c.resolveIssues(list, mode)
	if resolved == nil {
		return nil, nil, nil
	}
	catalog := c.catalog()
	replace := map[int][]string{}
	for _, is := range issues {
		if is.id == "" || is.code == errCitationOutside {
			errs = addLim(errs, is.code)
			continue
		}
		e := c.sent[is.id]
		var inside []string
		for _, h := range hints {
			x, ok := catalog[h]
			if ok && h != e.ID && x.Path == e.Path && x.SHA256 == e.SHA256 && e.Start <= x.Start && x.End <= e.End {
				inside = append(inside, h)
			}
		}
		if len(inside) > 0 {
			replace[is.pos] = inside
			lims = addLim(lims, limCitationFromCandidate)
		} else {
			lims = addLim(lims, limCitationUnspecified)
		}
	}
	ids = make([]string, 0, len(resolved))
	seen, known, trimmed := map[string]bool{}, 0, false
	for i, id := range resolved {
		group := []string{id}
		if r, ok := replace[i]; ok {
			group = r
		}
		for _, x := range group {
			_, isKnown := catalog[x]
			switch {
			case seen[x] || (isKnown && known == limit):
				trimmed = true
			case isKnown:
				known++
				fallthrough
			default:
				seen[x] = true
				ids = append(ids, x)
			}
		}
	}
	if trimmed {
		lims = addLim(lims, limCitationListTrimmed)
	}
	return ids, lims, errs
}

// catalog is the sent excerpts and every narrowed range, by ID.
func (c *citer) catalog() map[string]record.Excerpt {
	out := make(map[string]record.Excerpt, len(c.sent)+len(c.narrowed))
	for id, e := range c.sent {
		out[id] = e
	}
	for id, e := range c.narrowed {
		out[id] = e
	}
	return out
}

// used are the narrowed excerpts any of the lists cites, in the order they
// were first derived.
func (c *citer) used(lists ...[]string) []record.Excerpt {
	want := map[string]bool{}
	for _, l := range lists {
		for _, id := range l {
			want[id] = true
		}
	}
	var out []record.Excerpt
	for _, id := range c.order {
		if want[id] {
			out = append(out, c.narrowed[id])
		}
	}
	return out
}

// withCited appends the narrowed excerpts of extra not already in list.
func withCited(list, extra []record.Excerpt) []record.Excerpt {
	out := slices.Clone(list)
	for _, e := range extra {
		if !slices.ContainsFunc(out, func(x record.Excerpt) bool { return x.ID == e.ID }) {
			out = append(out, e)
		}
	}
	return out
}

// excerptList is the excerpts of a request; it encodes as requestExcerpts.
type excerptList []record.Excerpt

func (l excerptList) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("null"), nil
	}
	return json.Marshal([]record.Excerpt(requestExcerpts(l)))
}

// UnmarshalJSON reads a request's excerpts back as the worker held them: a
// reference gets its lines from the excerpt it lies within.
func (l *excerptList) UnmarshalJSON(b []byte) error {
	var list []record.Excerpt
	if err := json.Unmarshal(b, &list); err != nil {
		return err
	}
	byID := map[string]record.Excerpt{}
	for _, e := range list {
		if e.Text != "" {
			byID[e.ID] = e
		}
	}
	for i, e := range list {
		c, ok := byID[e.Within]
		if !ok || e.Text != "" || c.Start > e.Start || c.End < e.End {
			continue
		}
		lines := strings.Split(c.Text, "\n")
		if e.End-c.Start < len(lines) {
			list[i].Text = strings.Join(lines[e.Start-c.Start:e.End-c.Start+1], "\n")
			list[i].Within = ""
		}
	}
	*l = list
	return nil
}

// requestExcerpts is how a list of excerpts travels in a request: an
// excerpt whose lines lie inside another excerpt of the same blob in the
// list is sent as a reference (no text, within naming the excerpt that
// carries its lines), so a selector window inside a whole file is not
// sent twice. The containing excerpt is the largest one; ties go to the
// earlier.
func requestExcerpts(list []record.Excerpt) []record.Excerpt {
	out := slices.Clone(list)
	for i, e := range list {
		if e.Text == "" {
			continue
		}
		best := -1
		for j, x := range list {
			if j == i || x.Text == "" || x.Path != e.Path || x.SHA256 != e.SHA256 || x.Start > e.Start || x.End < e.End {
				continue
			}
			if x.Start == e.Start && x.End == e.End && j > i {
				continue
			}
			if best < 0 || x.End-x.Start > list[best].End-list[best].Start {
				best = j
			}
		}
		if best >= 0 {
			out[i].Text, out[i].Within = "", list[best].ID
		}
	}
	// A container that is itself a reference points at its own container.
	for i := range out {
		for depth := 0; out[i].Within != "" && depth < len(out); depth++ {
			j := slices.IndexFunc(out, func(x record.Excerpt) bool { return x.ID == out[i].Within })
			if j < 0 || out[j].Within == "" {
				break
			}
			out[i].Within = out[j].Within
		}
	}
	return out
}
