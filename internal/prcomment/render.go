// Package prcomment renders and posts the one CODEYE comment of a pull
// request (docs/GITHUB.md#결과-댓글): the analyzed head, the top findings in
// the changed files, the counts of the rest and the gaps that matter. The
// comment carries no code: tool messages, evidence and code excerpts are
// never copied, and the model's title and impact are cut to one line
// without code spans.
package prcomment

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// Limits of a rendered comment.
const (
	// MaxBody stays below GitHub's comment limit (65536 characters).
	MaxBody      = 60000
	maxFindings  = 5
	maxDiagnoses = 5
	maxGaps      = 5
	maxLine      = 200
)

// Input is what a comment is rendered from.
type Input struct {
	ProjectID, RunID string
	// Version is the publication version: the run's creation time in
	// microseconds. A later run of the pull request has a higher one.
	Version int64
	Pin     domain.PullRequest
	// Report is the stored report of the run (nil before it is published:
	// the comment then says the analysis has no report yet).
	Report *report.Report
	// CurrentHead is the pull request's head now, "" when unknown.
	CurrentHead string
}

// ProjectMarker is the hidden line that identifies the CODEYE comment of a
// project on a pull request.
func ProjectMarker(projectID string) string {
	return "<!-- codeye:pull-request-comment project=" + projectID + " -->"
}

var publicationMarker = regexp.MustCompile(`<!-- codeye:publication run=([0-9a-f-]{36}) version=([0-9]{1,20}) head=([0-9a-f]{40,64}) -->`)

// Publication is what the publication marker of a posted comment says.
type Publication struct {
	RunID   string
	Version int64
	Head    string
}

// ParsePublication reads the publication marker of a comment body.
func ParsePublication(body string) (Publication, bool) {
	m := publicationMarker.FindStringSubmatch(body)
	if m == nil {
		return Publication{}, false
	}
	v, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return Publication{}, false
	}
	return Publication{RunID: m[1], Version: v, Head: m[3]}, true
}

// Render is the comment body in Korean.
func Render(in Input) string {
	var b strings.Builder
	b.WriteString(ProjectMarker(in.ProjectID) + "\n")
	fmt.Fprintf(&b, "<!-- codeye:publication run=%s version=%d head=%s -->\n", in.RunID, in.Version, in.Pin.HeadSHA)
	b.WriteString("### " + msg("title") + "\n\n")
	p := in.Pin
	r := in.Report
	if r == nil {
		fmt.Fprintf(&b, msg("no_report")+"\n", short(p.HeadSHA))
		return finish(&b)
	}
	fmt.Fprintf(&b, "- "+msg("status")+"\n", report.Korean.Label("status."+r.Status), report.Korean.Label("verdict."+r.QualityVerdict))
	fmt.Fprintf(&b, "- "+msg("head")+"\n", short(p.HeadSHA), short(p.BaseSHA))
	if in.CurrentHead != "" && in.CurrentHead != p.HeadSHA {
		fmt.Fprintf(&b, "- **"+msg("head_moved")+"**\n", short(in.CurrentHead))
	}
	s := r.PullRequest
	if s != nil {
		if s.Review.SentToModel != nil {
			fmt.Fprintf(&b, "- "+msg("files")+"\n", s.Review.Reviewable, *s.Review.SentToModel)
		} else {
			fmt.Fprintf(&b, "- "+msg("files_no_model")+"\n", s.Review.Reviewable)
		}
		if s.FilesRemoved > 0 {
			fmt.Fprintf(&b, "- "+msg("files_removed")+"\n", s.FilesRemoved)
		}
		switch s.LineBasis {
		case report.LineBasisMixed:
			fmt.Fprintf(&b, "- "+msg("basis_mixed")+"\n", s.FilesWithoutLines)
		case report.LineBasisFiles:
			b.WriteString("- " + msg("basis_files") + "\n")
		}
	}
	b.WriteString("\n")
	shownF, shownO := 0, 0
	if s != nil {
		findings, observations := changedItems(r, s)
		// A diagnostic a listed finding stands on is not listed again.
		covered := map[string]bool{}
		for i, f := range findings {
			if i == maxFindings {
				break
			}
			for _, id := range f.f.ObservationIDs {
				covered[id] = true
			}
		}
		var rest []report.Observation
		for _, o := range observations {
			if covered[o.ID] {
				shownO++
			} else {
				rest = append(rest, o)
			}
		}
		observations = rest
		if len(findings) > 0 {
			b.WriteString("#### " + msg("findings") + "\n\n")
			for i, f := range findings {
				if i == maxFindings {
					break
				}
				shownF++
				fmt.Fprintf(&b, "%d. **[%s] %s** — %s\n", i+1, severity(f.f.Severity), oneLine(f.f.Title), place(f.path, f.line))
				if impact := oneLine(f.f.Impact); impact != "" {
					b.WriteString("   " + impact + "\n")
				}
			}
			b.WriteString("\n")
		}
		if len(observations) > 0 && shownF < maxFindings {
			b.WriteString("#### " + msg("diagnostics") + "\n\n")
			for i, o := range observations {
				if i == maxDiagnoses {
					break
				}
				shownO++
				fmt.Fprintf(&b, "- %s %s — %s\n", plain(o.Tool), code(o.RuleID), place(o.Location.Path, o.Location.StartLine))
			}
			b.WriteString("\n")
		}
		inF, inO := s.Findings.InChanges(), s.Observations.InChanges()
		fmt.Fprintf(&b, msg("rest")+"\n\n", inF-shownF, inO-shownO, s.Findings.PreExisting(), s.Observations.PreExisting())
		if inF+inO == 0 {
			b.WriteString(msg("none_in_changes") + "\n\n")
		}
	}
	if gaps := gapLines(r); len(gaps) > 0 {
		b.WriteString("#### " + msg("gaps") + "\n\n")
		for _, g := range gaps {
			b.WriteString("- " + g + "\n")
		}
		b.WriteString("\n")
	}
	return finish(&b)
}

func finish(b *strings.Builder) string {
	b.WriteString("<sub>" + msg("footer") + "</sub>\n")
	out := b.String()
	if len(out) > MaxBody {
		cut := MaxBody - 200
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut] + "\n\n" + msg("cut") + "\n"
	}
	return out
}

type findingAt struct {
	f    report.Finding
	path string
	line int
}

var severityOrder = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4}
var validationOrder = map[string]int{"reproduced": 0, "supported": 1}

// changedItems are the open findings and the diagnostics in the changes,
// most severe first (reproduced and supported before unconfirmed ones), in
// the order of the section's items otherwise.
func changedItems(r *report.Report, s *report.PullRequestSection) ([]findingAt, []report.Observation) {
	findings := map[string]report.Finding{}
	for _, f := range r.Findings {
		findings[f.ID] = f
	}
	observations := map[string]report.Observation{}
	for _, o := range r.Observations {
		observations[o.ID] = o
	}
	var fs []findingAt
	var os []report.Observation
	for _, it := range s.Items {
		switch it.Kind {
		case "finding":
			if f, ok := findings[it.ID]; ok && f.Disposition == "open" {
				fs = append(fs, findingAt{f: f, path: it.Path, line: it.Line})
			}
		case "observation":
			if o, ok := observations[it.ID]; ok && o.Location != nil {
				os = append(os, o)
			}
		}
	}
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i].f, fs[j].f
		if sa, sb := rank(severityOrder, a.Severity, 5), rank(severityOrder, b.Severity, 5); sa != sb {
			return sa < sb
		}
		return rank(validationOrder, a.Validation, 2) < rank(validationOrder, b.Validation, 2)
	})
	return fs, os
}

func rank(m map[string]int, k string, missing int) int {
	if v, ok := m[k]; ok {
		return v
	}
	return missing
}

// gapLines are the report's gaps grouped by section and code, most
// frequent first, at most maxGaps lines.
func gapLines(r *report.Report) []string {
	type key struct{ section, code string }
	count := map[key]int{}
	var keys []key
	for _, g := range r.Gaps {
		k := key{g.Section, g.Code}
		if count[k] == 0 {
			keys = append(keys, k)
		}
		count[k]++
	}
	sort.SliceStable(keys, func(i, j int) bool { return count[keys[i]] > count[keys[j]] })
	var out []string
	for i, k := range keys {
		if i == maxGaps {
			out = append(out, fmt.Sprintf(msg("gaps_more"), len(keys)-maxGaps))
			break
		}
		out = append(out, fmt.Sprintf("%s: %s (%d)", report.Korean.Label("section."+k.section), report.Korean.Label("gap."+k.code), count[k]))
	}
	return out
}

func severity(s string) string {
	if t, ok := severities[s]; ok {
		return t
	}
	return plain(s)
}

func place(path string, line int) string {
	if line > 0 {
		return code(path + ":" + strconv.Itoa(line))
	}
	return code(path)
}

func short(sha string) string {
	if len(sha) > 7 {
		return code(sha[:7])
	}
	return code(sha)
}

var (
	codeSpan = regexp.MustCompile("`[^`]*`")
	fence    = regexp.MustCompile("(?s)```.*?(```|$)")
	mention  = regexp.MustCompile(`@([A-Za-z0-9])`)
	issueRef = regexp.MustCompile(`#([0-9])`)
	// GH-123 is an issue reference too; URLs and www. addresses autolink.
	ghRef     = regexp.MustCompile(`(?i)\b(gh)-([0-9])`)
	autolink  = regexp.MustCompile(`(?i)(https?:|www)(//|\.)`)
	spaceRuns = regexp.MustCompile(`\s+`)
)

// oneLine is model text made safe for the comment: code fences and code
// spans are dropped, the first line kept and cut, HTML and the CODEYE
// marker neutralized, and mentions and issue references broken so the
// comment notifies nobody and links nothing.
func oneLine(s string) string {
	s = fence.ReplaceAllString(s, " ")
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = codeSpan.ReplaceAllString(s, "…")
	s = strings.TrimSpace(spaceRuns.ReplaceAllString(plain(s), " "))
	if utf8.RuneCountInString(s) > maxLine {
		s = string([]rune(s)[:maxLine-1]) + "…"
	}
	return s
}

// plain neutralizes text outside code spans: HTML (and so the hidden
// markers), mentions, issue references, links and markdown emphasis. "&"
// is escaped first, so a character reference such as &#64; or &commat;
// shows as text and cannot become a mention or a reference after the
// other replacements.
func plain(s string) string {
	s = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "`", "'", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "|", "\\|").Replace(s)
	s = mention.ReplaceAllString(s, "@⁠$1")
	s = issueRef.ReplaceAllString(s, "#⁠$1")
	s = ghRef.ReplaceAllString(s, "${1}⁠-$2")
	return autolink.ReplaceAllString(s, "$1⁠$2")
}

// code is a code span for a path or rule; backticks and line breaks in it
// are replaced so the span cannot end early.
func code(s string) string {
	s = strings.NewReplacer("`", "'", "\n", " ", "\r", " ").Replace(s)
	return "`" + s + "`"
}
