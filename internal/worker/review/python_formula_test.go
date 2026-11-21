package review

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const demurrageFormula = `def settlement_total(items):
    """Aggregate demurrage charges for active berths."""
    amounts = []
    for item in items:
        if item.in_service:
            amounts.append((item.elapsed, item.tariff))
    return sum(
        duration * tariff
        for duration, tariff in amounts
    )
`

func TestDocumentedPythonFormulaPromotesBoundedResultAndCondition(t *testing.T) {
	const (
		p         = "port/ledger.py"
		statement = "Sum the demurrage charges by multiplying each active interval by its rate."
	)
	for _, tc := range []struct {
		name, before, after string
		statement, document string
		extra               string
		setup               func(*roleStage)
		want                bool
	}{
		{name: "formula and docstring corroborated", want: true},
		{name: "Korean source prose", before: "Aggregate demurrage charges for active berths.", after: "체선료를 합산한다.",
			statement: "체선료는 사용 시간에 요율을 곱해 합산한다.", want: true},
		{name: "documented function name", before: `"""Aggregate demurrage charges for active berths."""`, after: "# Prepare the rows.",
			statement: "Use `settlement_total` to sum charges by multiplying each active interval by its rate.", want: true},
		{name: "cited passage omits the arithmetic", document: "Sum the demurrage charges for the port."},
		{name: "cited passage uses a different operation", document: "Sum the demurrage charges by dividing each interval by its rate."},
		{name: "docstring does not corroborate the subject", before: "demurrage charges for active berths", after: "waiting windows for the calendar"},
		{name: "wrong operation", before: "duration * tariff", after: "duration + tariff"},
		{name: "arithmetic outside the aggregate", before: `return sum(
        duration * tariff
        for duration, tariff in amounts
    )`, after: "return sum(duration for duration, _ in amounts) * tariff"},
		{name: "same operators only in a comment", before: "duration * tariff", after: "duration  # duration * tariff"},
		{name: "sum is shadowed", before: "    amounts = []", after: "    sum = fake_aggregate\n    amounts = []"},
		{name: "equally supported second module", extra: demurrageFormula},
		{name: "equally supported second definition", before: "    )", after: "    )\n\n" + strings.Replace(demurrageFormula, "settlement_total", "other_total", 1)},
		{name: "withheld source", setup: func(s *roleStage) {
			s.screen.withheld = map[string]bool{p: true}
		}},
		{name: "unchanged PR source", setup: func(s *roleStage) {
			s.j.PullRequest = &domain.PullRequestJob{Changed: []string{"port/README.md"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := demurrageFormula
			if tc.before != "" {
				body = strings.Replace(body, tc.before, tc.after, 1)
			}
			contract := statement
			if tc.statement != "" {
				contract = tc.statement
			}
			docText := tc.document
			if docText == "" {
				docText = contract
			}
			files := map[string]string{"port/README.md": "# Berth charges\n\n" + docText + "\n",
				p: strings.Repeat("# Boilerplate near the file prefix\n", 80) + body}
			if tc.extra != "" {
				files["port/extra.py"] = tc.extra
			}
			s := roleStage{ctx: t.Context(), j: treeJob(t, files)}
			if tc.setup != nil {
				tc.setup(&s)
			}
			doc, _, ok, err := s.excerpt(s.entries()["port/README.md"], "document", "", 1, 4, witnessMaxExcerpt)
			if err != nil || !ok {
				t.Fatalf("document excerpt: %+v, %v", doc, err)
			}
			docs := record.Record{Contracts: []record.Contract{{Statement: contract, AppliesTo: "port", EvidenceIDs: []string{doc.ID}}},
				Evidence: []record.Excerpt{doc}}
			list, _, _, err := s.codeCorpus(docs)
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range list {
				// Ordinary lexical windows may still show the docstring
				// or return. The formula starts at the preparatory block.
				if x.Path == p && x.Start == 83 && strings.Contains(x.Text, "return sum(") {
					if !tc.want || x.End-x.Start >= witnessMaxLines || !strings.Contains(x.Text, "if item.in_service:") ||
						!strings.Contains(x.Text, "duration * tariff") {
						t.Fatalf("unexpected calculation excerpt: %+v", x)
					}
					reread, err := s.withText([]record.Excerpt{x})
					if err != nil || len(reread) != 1 || reread[0] != x {
						t.Fatalf("formula does not re-read from its evidence ID: %+v, %v", reread, err)
					}
					return
				}
			}
			if tc.want {
				t.Fatalf("calculation and its condition missing from overview: %+v", list)
			}
		})
	}
}
