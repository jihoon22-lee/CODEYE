package report

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	hrefRE = regexp.MustCompile(`href="#([^"]+)"`)
	idRE   = regexp.MustCompile(` id="([^"]+)"`)
)

// checkInPageLinks fails when an in-page link of an HTML rendering has no
// element with that id.
func checkInPageLinks(t *testing.T, html []byte) {
	t.Helper()
	ids := map[string]bool{}
	for _, m := range idRE.FindAllSubmatch(html, -1) {
		ids[string(m[1])] = true
	}
	for _, m := range hrefRE.FindAllSubmatch(html, -1) {
		if !ids[string(m[1])] {
			t.Fatalf("link to #%s without a target", m[1])
		}
	}
}

// The summary layout (docs/REPORT.md#내보내기-상세도) of the stored
// examples: sections in order of value, no format errors, every in-page
// link has its target, and the full level is the sectioned layout as it was.
func TestSummaryLayout(t *testing.T) {
	for _, path := range []string{"../../examples/report.json", "../../examples/report-slices.json", "testdata/report-slices-builder7.json"} {
		t.Run(path, func(t *testing.T) {
			r := storedExample(t, path)
			d := strings.Repeat("0", 64)
			md := string(RenderMarkdownAt(r, d, Korean, DetailSummary))
			html, err := RenderHTMLAt(r, d, Korean, DetailSummary)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(md, "%!") || bytes.Contains(html, []byte("%!")) {
				t.Fatal("format error")
			}
			checkInPageLinks(t, html)
			checkTableWidths(t, md)
			var at []int
			for _, k := range []string{"overview", SectionCodeReview, SectionStaticAnalysis, SectionMetrics, SectionSecrets, SectionEnvironment, SectionDocuments, SectionGaps} {
				at = append(at, strings.Index(md, "## "+mdText(Korean.Label("section."+k))+"\n"))
			}
			for i := range at {
				if at[i] < 0 || (i > 0 && at[i] < at[i-1]) {
					t.Fatalf("section order %v", at)
				}
			}
			if !strings.Contains(md, mdText(Korean.Label("summary.layout_note"))) {
				t.Fatal("summary without its note")
			}
			full := RenderMarkdownAt(r, d, Korean, DetailFull)
			checkTableWidths(t, string(full))
			if !bytes.Equal(full, RenderMarkdown(r, d, Korean)) {
				t.Fatal("full level differs from the sectioned layout")
			}
			fullHTML, err := RenderHTMLAt(r, d, Korean, DetailFull)
			if err != nil {
				t.Fatal(err)
			}
			checkInPageLinks(t, fullHTML)
		})
	}
}

// checkTableWidths fails when a Markdown table row has another number of
// cells than its header.
func checkTableWidths(t *testing.T, md string) {
	t.Helper()
	width := 0
	for _, line := range strings.Split(md, "\n") {
		if !strings.HasPrefix(line, "|") {
			width = 0
			continue
		}
		n := strings.Count(strings.ReplaceAll(line, `\|`, ""), "|")
		if width == 0 {
			width = n
		} else if n != width {
			t.Fatalf("table row of %d cells under a header of %d: %s", n-1, width-1, line)
		}
	}
}

// A builder 8 ledger shows each module's class and the secondary files left
// out within the ceiling, so that every row adds up to its eligible files;
// the summary's sum row of the secondary modules does too.
func TestLedgerRowsShowSecondaryFiles(t *testing.T) {
	r := storedExample(t, "../../examples/report-slices.json")
	m := &r.CodeReview.Ledger.Modules[1]
	// go:store as a secondary module whose 13 files the ceiling refused.
	m.Class, m.FilesSecondary, m.FilesSecondarySent = "secondary", m.FilesEligible, m.FilesSentToModel
	m.FilesSecondaryNotReached, m.FilesBudgetExhausted = m.FilesBudgetExhausted, 0
	d := strings.Repeat("0", 64)
	for _, detail := range []string{DetailSummary, DetailFull} {
		md := string(RenderMarkdownAt(r, d, Korean, detail))
		checkTableWidths(t, md)
		row := "| `go:store` | " + Korean.Label("class.secondary") + " | "
		if detail == DetailSummary {
			row = "| " + mdText(tf(Korean, "summary.ledger_secondary_modules", 1)) + " | " + Korean.Label("class.secondary") + " | "
		}
		i := strings.Index(md, row)
		if i < 0 {
			t.Fatalf("%s: no secondary ledger row", detail)
		}
		line := md[i : i+strings.Index(md[i:], "\n")]
		if !strings.Contains(line, " | 0 | 0 | 13 | 0 | 17 |") {
			t.Fatalf("%s: secondary row %s", detail, line)
		}
	}
}

// Rule groups count every diagnostic once: per language the group counts add
// up to the diagnostics that are neither suppressed nor metrics, and the
// locations a row shows plus its "외 N곳" equal its count.
func TestSummaryStaticGroupsAddUp(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	v := newSecView(r, "0", Korean)
	obs := map[string]Observation{}
	for _, o := range r.Observations {
		obs[o.ID] = o
	}
	total := 0
	for _, lang := range r.StaticAnalysis.Languages {
		want := 0
		for _, x := range lang.Tools {
			if optText(x.Purpose, PurposeDiagnostics) != PurposeDiagnostics {
				continue
			}
			for _, id := range x.ObservationIDs {
				if o := obs[id]; !o.Suppressed && o.Metric == nil {
					want++
				}
			}
		}
		got := 0
		for _, g := range v.staticGroups(lang, obs, nil) {
			got += g.Count
			if len(g.Primary)+len(g.Secondary) != g.Count {
				t.Fatalf("group %+v", g)
			}
		}
		if got != want {
			t.Fatalf("groups %d diagnostics %d", got, want)
		}
		total += got
	}
	if total == 0 {
		t.Fatal("example without diagnostics")
	}
	// placesText: shown plus "외 N곳" is the count.
	more := regexp.MustCompile(`외 (\d+)곳`)
	for _, c := range []struct{ primary, secondary int }{{0, 0}, {3, 2}, {7, 0}, {0, 9}, {2, 30}, {6, 6}} {
		var p, s []string
		for i := range c.primary {
			p = append(p, "a/p"+strconv.Itoa(i)+".go:1")
		}
		for i := range c.secondary {
			s = append(s, "fixtures/s"+strconv.Itoa(i)+".go:1")
		}
		text := placesText(Korean, p, s)
		shown := 0
		if text != "" {
			shown = strings.Count(more.ReplaceAllString(text, ""), ".go:1")
		}
		rest := 0
		if m := more.FindStringSubmatch(text); m != nil {
			rest, _ = strconv.Atoi(m[1])
		}
		if shown+rest != c.primary+c.secondary || (c.primary > 0 && c.primary+c.secondary > summaryShown && strings.Contains(strings.Split(text, " 외 ")[0], "fixtures/")) {
			t.Fatalf("%+v: %q", c, text)
		}
	}
}

// The summary takes the one-line description from the projected message: a
// code-free export shows the omission label, never the tool text.
func TestSummaryCodeFreeKeepsMessagesOut(t *testing.T) {
	r := storedExample(t, "../../examples/report.json")
	p := Project(r, false)
	md := string(RenderMarkdownAt(p, "0", Korean, DetailSummary))
	for _, o := range r.Observations {
		if o.RawMessage != nil && len(*o.RawMessage) > 12 && strings.Contains(md, mdText(cutLine(*o.RawMessage, summaryMessageRunes))) {
			t.Fatalf("code-free summary shows tool text %q", *o.RawMessage)
		}
	}
}
