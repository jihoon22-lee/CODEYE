package report

import (
	"strconv"
	"strings"
)

// withInsights adds the same evidence-backed assessments to both export layouts.
func withInsights(v secView) secView {
	l := v.L
	for i := range v.Sections {
		block := &v.Sections[i]
		var parts []secPart
		switch block.ID {
		case "overview":
			table := &secTable{Head: []string{l.Label("identity.verdict"), l.Label("insight.scope"), l.Label("insight.statement")}}
			for _, c := range v.R.Documents.Comparisons {
				table.Rows = append(table.Rows, []secCell{txt(l.Label("insight." + c.Status)), code(c.AppliesTo), txt(c.Statement)})
			}
			for _, s := range v.R.CodeReview.Strengths[:min(3, len(v.R.CodeReview.Strengths))] {
				table.Rows = append(table.Rows, []secCell{txt(l.Label("insight.strength")), code(s.Scope), txt(s.Title + " · " + s.Benefit)})
			}
			if len(table.Rows) > 0 {
				parts = append(parts, secPart{Heading: l.Label("insight.overview"), Table: table, Model: l.Label("insight.interpretation")})
			}
		case SectionDocuments:
			doc := v.R.Documents
			for _, q := range doc.Quality {
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Heading: l.Label("insight."+q.Aspect) + " · " + l.Label("insight."+q.Assessment), Paras: []string{q.Statement}, Sources: sourceCitations(q.EvidenceIDs, doc.Evidence)})
			}
			for _, c := range doc.Comparisons {
				ids := append(append([]string{}, c.DocumentEvidenceIDs...), c.CodeEvidenceIDs...)
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Heading: l.Label("insight.comparison") + " · " + l.Label("insight."+c.Status), Paras: []string{l.Label("insight.scope") + " · " + c.AppliesTo, c.Statement, c.Reason}, Sources: sourceCitations(ids, doc.Evidence)})
			}
		case SectionCodeReview:
			for _, s := range v.R.CodeReview.Strengths {
				parts = append(parts, secPart{Model: l.Label("insight.interpretation"), Heading: l.Label("insight.strength") + " · " + s.Title, Paras: []string{l.Label("insight.scope") + " · " + s.Scope, s.Benefit}, Sources: sourceCitations(s.EvidenceIDs, v.R.CodeReview.Evidence)})
			}
		}
		block.Parts = append(parts, block.Parts...)
	}
	return v
}

type sourceCitation struct{ Location, Text string }

func sourceCitations(ids []string, evidence []SectionEvidence) []sourceCitation {
	var list []sourceCitation
	for _, id := range ids {
		for _, e := range evidence {
			if e.ID != id {
				continue
			}
			x := sourceCitation{Location: e.Path + ":" + strconv.Itoa(e.StartLine)}
			if e.Excerpt != nil {
				x.Location = e.Path + ":" + strconv.Itoa(e.Excerpt.StartLine) + "-" + strconv.Itoa(e.Excerpt.EndLine)
				x.Text = e.Excerpt.Text
			}
			list = append(list, x)
			break
		}
	}
	return list
}

// A fence longer than any backtick run in the snapshot keeps its lines literal.
func mdCitations(p func(...string), sources []sourceCitation) {
	for _, x := range sources {
		p(mdCode(x.Location))
		p()
		if x.Text != "" {
			fence := strings.Repeat("`", max(3, longestBacktickRun(x.Text)+1))
			p(fence)
			p(x.Text)
			p(fence)
			p()
		}
	}
}
