package review

import (
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// leadWindow is a sent window of path around the line holding content and
// the lead's cited range on that line, both with text.
func leadWindow(t *testing.T, s *roleStage, path, content string, around int) (window, anchor record.Excerpt) {
	t.Helper()
	e, ok := s.entries()[path]
	if !ok {
		t.Fatalf("no %s", path)
	}
	lines, ok, err := s.sourceLines(e, nil)
	if err != nil || !ok {
		t.Fatalf("read %s: %v", path, err)
	}
	n := 0
	for i, l := range lines {
		if strings.Contains(l, content) {
			n = i + 1
			break
		}
	}
	if n == 0 {
		t.Fatalf("missing %q in %s", content, path)
	}
	start, end := max(1, n-around), min(len(lines), n+around)
	window = record.Excerpt{ID: record.ExcerptID(s.j.Snapshot.Digest, path, e.SHA256, start, end), Path: path, SHA256: e.SHA256, Start: start, End: end,
		Kind: windowKind(path), Origin: "context", Text: strings.Join(lines[start-1:end], "\n")}
	return window, record.Narrow(window, s.j.Snapshot.Digest, n, n)
}

func leadStage(t *testing.T, j analysis.Job) *roleStage {
	return &roleStage{ctx: t.Context(), j: j, role: record.StageInvestigate}
}

func resolveOne(t *testing.T, s *roleStage, need, symbol string, window, anchor record.Excerpt, sent ...record.Excerpt) record.Lead {
	t.Helper()
	l := record.Lead{ID: "lead_1", Need: need, Symbol: symbol, EvidenceID: anchor.ID, Suspicion: "호출한 함수가 오류를 돌려주는지 확인해야 한다."}
	out, err := s.resolveLeads([]record.Lead{l}, map[string]record.Excerpt{anchor.ID: anchor}, append([]record.Excerpt{window}, sent...))
	if err != nil {
		t.Fatal(err)
	}
	return out[0]
}

// A definition lead resolves to the declaration the call in its cited line
// binds to, in every language a source resolver reads, bound to the
// snapshot blob and within the window bound.
func TestLeadDefinitionsResolvePerLanguage(t *testing.T) {
	symbols := map[string]string{"Go": "Quote", "Java": "quote", "Python": "calculate", "TypeScript": "calculate", "C++": "quote", "Rust": "calculate"}
	for lang, project := range testCallProjects() {
		t.Run(lang, func(t *testing.T) {
			j := treeJob(t, project.tree)
			s := leadStage(t, j)
			window, anchor := leadWindow(t, s, project.caller, project.call, 1)
			l := resolveOne(t, s, record.LeadNeedDefinition, symbols[lang], window, anchor)
			if !l.Resolved() || len(l.Targets) != 1 {
				t.Fatalf("lead %+v", l)
			}
			target := l.Targets[0]
			text, _, ok, err := s.readLines(s.entries()[target.Path], target.Start, target.End)
			if err != nil || !ok || target.Path != project.callee || !strings.Contains(text, project.result) || target.Origin != "context" ||
				target.ID != record.ExcerptID(j.Snapshot.Digest, target.Path, s.entries()[target.Path].SHA256, target.Start, target.End) ||
				target.End-target.Start+1 > record.MaxLeadWindowLines {
				t.Fatalf("target %+v %q", target, text)
			}
		})
	}
}

const leadsGoTree = `package store

// Save writes the value; it fails on an empty key.
func Save(key string, value int) error {
	if key == "" {
		return errEmpty
	}
	return write(key, value)
}
`

const leadsGoHelper = `package store

import "errors"

var errEmpty = errors.New("empty key")

func write(key string, value int) error {
	if value < 0 {
		return errors.New("negative")
	}
	return nil
}

type memory struct{}

func (memory) Flush() error { return nil }

type disk struct{}

func (disk) Flush() error { return errors.New("disk") }
`

const leadsGoCaller = `package store

func Run(keys []string) {
	for _, k := range keys {
		_ = Save(k, 1)
	}
}

func Close(m memory, d disk) {
	_ = m.Flush()
	_ = d.Flush()
}
`

func leadsGoJob(t *testing.T, extra map[string]string) analysis.Job {
	tree := map[string]string{"go.mod": "module example.org/store\n\ngo 1.23\n", "store/save.go": leadsGoTree, "store/helper.go": leadsGoHelper, "store/run.go": leadsGoCaller}
	for k, v := range extra {
		tree[k] = v
	}
	return treeJob(t, tree)
}

// Go leads inside one package: a definition in another file of the
// package, the callers of a function, two methods of one name (ambiguous),
// a symbol the cited line does not hold, code the request already carried
// and a language no resolver reads.
func TestLeadResolutionOutcomes(t *testing.T) {
	j := leadsGoJob(t, map[string]string{"tools/gen.rb": "def save(key)\n  write(key)\nend\n"})
	s := leadStage(t, j)
	window, anchor := leadWindow(t, s, "store/save.go", "return write(key, value)", 1)

	l := resolveOne(t, s, record.LeadNeedDefinition, "write", window, anchor)
	if !l.Resolved() || l.Targets[0].Path != "store/helper.go" || l.Targets[0].Origin != "context" {
		t.Fatalf("same-package definition %+v", l)
	}

	decl, declAnchor := leadWindow(t, s, "store/save.go", "func Save(", 1)
	l = resolveOne(t, s, record.LeadNeedCallers, "Save", decl, declAnchor)
	if !l.Resolved() || l.Targets[0].Path != "store/run.go" || l.Targets[0].Origin != "caller" || l.Targets[0].Symbol != "Save" {
		t.Fatalf("callers %+v", l)
	}

	flush, flushAnchor := leadWindow(t, s, "store/run.go", "_ = m.Flush()", 1)
	if l = resolveOne(t, s, record.LeadNeedDefinition, "Flush", flush, flushAnchor); l.Resolved() || l.Reason != record.LeadAmbiguous {
		t.Fatalf("two methods named Flush %+v", l)
	}

	if l = resolveOne(t, s, record.LeadNeedDefinition, "Missing", window, anchor); l.Reason != record.LeadSymbolNotAtAnchor {
		t.Fatalf("symbol not at the cited line %+v", l)
	}
	if l = resolveOne(t, s, record.LeadNeedCallers, "write", window, anchor); l.Reason != record.LeadSymbolNotAtAnchor {
		t.Fatalf("callers of a function the lines do not declare %+v", l)
	}

	helper, _ := leadWindow(t, s, "store/helper.go", "func write(", 6)
	l = resolveOne(t, s, record.LeadNeedDefinition, "write", window, anchor, helper)
	if l.Reason != record.LeadInRequest || len(l.InRequest) != 1 || l.InRequest[0] != helper.ID || l.Targets != nil {
		t.Fatalf("definition already sent %+v", l)
	}

	ruby, rubyAnchor := leadWindow(t, s, "tools/gen.rb", "write(key)", 0)
	if l = resolveOne(t, s, record.LeadNeedDefinition, "write", ruby, rubyAnchor); l.Reason != record.LeadLanguage {
		t.Fatalf("unsupported language %+v", l)
	}

	if l = resolveOne(t, s, record.LeadNeedDefinition, "Unknown", window, record.Narrow(window, j.Snapshot.Digest, window.Start, window.Start)); l.Reason != record.LeadSymbolNotAtAnchor {
		t.Fatalf("%+v", l)
	}
}

// The resolution reads and sends only what the run may send: a definition
// withheld by the secrets screen, outside the scope, outside a pull
// request's changed files, generated or past the follow-up's caps is never
// a target, and the lead says why.
func TestLeadResolutionBoundaries(t *testing.T) {
	project := testCallProjects()["Go"]
	cases := []struct {
		name   string
		job    func(t *testing.T) analysis.Job
		stage  func(s *roleStage)
		reason string
	}{
		{"withheld", func(t *testing.T) analysis.Job { return treeJob(t, project.tree) },
			func(s *roleStage) {
				s.screen = secretScreen{state: screenApplied, withheld: map[string]bool{project.callee: true}}
			}, record.LeadWithheld},
		{"outside scope", func(t *testing.T) analysis.Job {
			j := treeJob(t, project.tree)
			j.Scope = domain.Scope{Kind: "paths", Paths: []string{"app"}}
			return j
		}, nil, record.LeadOutsideScope},
		{"outside the change", func(t *testing.T) analysis.Job {
			j := treeJob(t, project.tree)
			j.PullRequest = &domain.PullRequestJob{Changed: []string{project.caller}}
			return j
		}, nil, record.LeadOutsideChange},
		{"generated", func(t *testing.T) analysis.Job {
			tree := map[string]string{}
			for k, v := range project.tree {
				tree[k] = v
			}
			tree[project.callee] = "// Code generated by tool; DO NOT EDIT.\n\n" + tree[project.callee]
			return treeJob(t, tree)
		}, nil, record.LeadGenerated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := c.job(t)
			s := leadStage(t, j)
			if c.stage != nil {
				c.stage(s)
			}
			window, anchor := leadWindow(t, s, project.caller, project.call, 1)
			l := resolveOne(t, s, record.LeadNeedDefinition, "Quote", window, anchor)
			if l.Resolved() || l.Reason != c.reason || l.Targets != nil {
				t.Fatalf("lead %+v", l)
			}
		})
	}
	t.Run("over cap", func(t *testing.T) {
		j := leadsGoJob(t, nil)
		s := leadStage(t, j)
		window, anchor := leadWindow(t, s, "store/save.go", "return write(key, value)", 1)
		r := s.newLeadResolver([]record.Excerpt{window})
		r.excerpts = 0
		l, err := r.resolve(record.Lead{ID: "lead_1", Need: record.LeadNeedDefinition, Symbol: "write", EvidenceID: anchor.ID, Suspicion: "x"}, anchor)
		if err != nil || l.Reason != record.LeadOverCap {
			t.Fatalf("lead %+v %v", l, err)
		}
		r.excerpts, r.bytes = 1, 10
		if l, _ = r.resolve(record.Lead{ID: "lead_1", Need: record.LeadNeedDefinition, Symbol: "write", EvidenceID: anchor.ID, Suspicion: "x"}, anchor); l.Reason != record.LeadOverCap {
			t.Fatalf("byte cap %+v", l)
		}
	})
}

// The caps of one follow-up follow the request size tiers: three windows for
// each of three leads, within the verify expansion's count and bytes.
func TestLeadCapsFollowTheRequestTiers(t *testing.T) {
	for _, c := range []struct {
		b               Budget
		excerpts, bytes int
	}{
		{Budget{RequestBytes: MinRequestBytes}, 6, 8000},
		{Budget{}, 9, 21600},
		{Budget{RequestBytes: MaxRequestBytes, OutputTokens: MaxOutputTokens}, 9, 21600},
	} {
		if l := c.b.limits(); l.leadExcerpts != c.excerpts || l.leadBytes != c.bytes {
			t.Fatalf("%+v: %d/%d", c.b, l.leadExcerpts, l.leadBytes)
		}
	}
}

// Leads are optional items of the investigate output: only offered, at
// most record.MaxLeads, each with a need, one identifier (a qualified name
// is cut to its last part), sent code lines and a candidate position that
// was kept; any other is left out.
func TestLeadParsing(t *testing.T) {
	j := leadsGoJob(t, nil)
	s := leadStage(t, j)
	window, _ := leadWindow(t, s, "store/save.go", "return write(key, value)", 1)
	doc := record.Excerpt{ID: "ev_000000000000000000000000", Path: "README.md", Start: 1, End: 2, Kind: "document"}
	reply := newCiter(j.Snapshot.Digest, []record.Excerpt{window, doc})
	cite := func(line int) string { return window.ID + ":" + strconv.Itoa(line) + "-" + strconv.Itoa(line) }
	line := window.Start + 1
	ok := leadOutput{Need: "definition", EvidenceID: cite(line), Symbol: "store.write()", Suspicion: "음수 값이 오류가 되는지 정의를 봐야 한다.", Candidate: 2}
	kept := map[int]string{2: "investigate_1"}
	if leads, omitted := s.checkLeads([]leadOutput{ok}, false, reply, kept); leads != nil || !omitted {
		t.Fatal("leads parsed without an offer")
	}
	bad := []leadOutput{
		{Need: "usage", EvidenceID: cite(line), Symbol: "write", Suspicion: "x"},
		{Need: "definition", EvidenceID: doc.ID, Symbol: "write", Suspicion: "문서만 인용한 단서다."},
		{Need: "definition", EvidenceID: cite(line), Symbol: "write", Suspicion: "없는 후보의 단서다.", Candidate: 1},
		{Need: "callers", EvidenceID: cite(window.End + 5), Symbol: "write", Suspicion: "보내지 않은 줄의 단서다."},
	}
	leads, omitted := s.checkLeads(append(bad, ok, ok, ok, ok, ok), true, reply, kept)
	if !omitted || len(leads) != 1 || leads[0].ID != "lead_1" || leads[0].Symbol != "write" || leads[0].CandidateID != "investigate_1" ||
		leads[0].EvidenceID != record.ExcerptID(j.Snapshot.Digest, window.Path, window.SHA256, line, line) {
		t.Fatalf("leads %+v omitted %v", leads, omitted)
	}
	var many []leadOutput
	for _, sym := range []string{"write", "Save", "errEmpty", "key"} {
		x := ok
		x.Symbol, x.Candidate = sym, 0
		many = append(many, x)
	}
	if leads, omitted = s.checkLeads(many, true, reply, kept); len(leads) != record.MaxLeads || !omitted {
		t.Fatalf("more than %d leads: %d", record.MaxLeads, len(leads))
	}
}

// A reason names what the lead's own call reaches: one line calls into a
// file the read filters withhold, the next calls a function of the standard
// library (not in the snapshot) and a function of another package. The
// withheld file is the reason only of the call into it; the other calls
// are not found or resolved, never matched to another call on their line.
func TestLeadReasonsFollowTheirOwnCall(t *testing.T) {
	credential := "const password = \"" + "codeye" + "fixture" + strings.Repeat("0", 8) + "\"\n"
	j := treeJob(t, map[string]string{
		"go.mod":         "module example.org/chain\n\ngo 1.23\n",
		"main.go":        "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.org/chain/p01\"\n\t\"example.org/chain/vault\"\n)\n\nfunc main() {\n\ttoken := vault.Token()\n\tfmt.Println(p01.F01(3), len(token))\n}\n",
		"p01/f.go":       "package p01\n\n// F01 doubles n.\nfunc F01(n int) int {\n\treturn 2 * n\n}\n",
		"vault/vault.go": "package vault\n\n" + credential + "\nfunc Token() string { return password }\n",
	})
	s := leadStage(t, j)
	window, token := leadWindow(t, s, "main.go", "token := vault.Token()", 2)
	_, println := leadWindow(t, s, "main.go", "fmt.Println(", 0)
	for _, c := range []struct {
		symbol string
		anchor record.Excerpt
		reason string
	}{{"Token", token, record.LeadWithheld}, {"Println", println, record.LeadDefinitionMissing}, {"F01", println, ""}} {
		l := resolveOne(t, s, record.LeadNeedDefinition, c.symbol, window, c.anchor)
		if c.reason == "" {
			if !l.Resolved() || l.Targets[0].Path != "p01/f.go" {
				t.Fatalf("%s: %+v", c.symbol, l)
			}
			continue
		}
		if l.Resolved() || l.Reason != c.reason {
			t.Fatalf("%s: %+v", c.symbol, l)
		}
	}
}

// A Go definition the call binds across packages is sent whole when the
// declaration fits one window (here 34 lines, past the 24 lines of the
// bounded return the call binding keeps). A longer one is sent as that
// bounded return.
func TestLeadDefinitionWindowHoldsTheDeclaration(t *testing.T) {
	body := func(pad int) string {
		return "package quote\n\n// Price returns the price of n items; a negative count gives -1.\nfunc Price(n int) int {\n" +
			strings.Repeat("\t// Each item costs the same.\n", pad) + "\tif n < 0 {\n\t\treturn -1\n\t}\n\treturn n * 3\n}\n"
	}
	caller := "package main\n\nimport \"example.org/shop/quote\"\n\nfunc main() {\n\tprintln(quote.Price(2))\n}\n"
	for _, c := range []struct {
		pad        int
		start, end int
	}{{28, 4, 37}, {70, 75, 78}} {
		j := treeJob(t, map[string]string{"go.mod": "module example.org/shop\n\ngo 1.23\n", "main.go": caller, "quote/price.go": body(c.pad)})
		s := leadStage(t, j)
		window, anchor := leadWindow(t, s, "main.go", "quote.Price(2)", 0)
		l := resolveOne(t, s, record.LeadNeedDefinition, "Price", window, anchor)
		if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "quote/price.go" || l.Targets[0].Start != c.start || l.Targets[0].End != c.end {
			t.Fatalf("pad %d: lead %+v", c.pad, l)
		}
	}
}
