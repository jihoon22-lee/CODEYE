package review

import (
	"slices"
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

// sentLines is a sent excerpt of path's lines start to end with text.
func sentLines(t *testing.T, s *roleStage, path string, start, end int) record.Excerpt {
	t.Helper()
	e, ok := s.entries()[path]
	if !ok {
		t.Fatalf("no %s", path)
	}
	lines, ok, err := s.sourceLines(e, nil)
	if err != nil || !ok || start < 1 || end > len(lines) {
		t.Fatalf("read %s %d-%d: %v", path, start, end, err)
	}
	return record.Excerpt{ID: record.ExcerptID(s.j.Snapshot.Digest, path, e.SHA256, start, end), Path: path, SHA256: e.SHA256, Start: start, End: end,
		Kind: windowKind(path), Origin: "context", Text: strings.Join(lines[start-1:end], "\n")}
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
// package, the callers of a function, a method of two that share a name
// bound by its receiver's type, a symbol the cited line does not hold, code
// the request already carried and a language no resolver reads.
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

	flush, flushAnchor := leadWindow(t, s, "store/run.go", "_ = m.Flush()", 0)
	if l = resolveOne(t, s, record.LeadNeedDefinition, "Flush", flush, flushAnchor); !l.Resolved() || len(l.Targets) != 1 ||
		l.Targets[0].Path != "store/helper.go" || l.Targets[0].Start != 16 || l.Targets[0].End != 16 {
		t.Fatalf("memory's Flush %+v", l)
	}
	flush, flushAnchor = leadWindow(t, s, "store/run.go", "_ = d.Flush()", 0)
	if l = resolveOne(t, s, record.LeadNeedDefinition, "Flush", flush, flushAnchor); !l.Resolved() || l.Targets[0].Start != 20 {
		t.Fatalf("disk's Flush %+v", l)
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
	// Sent in two pieces, neither holding the whole declaration, the
	// definition leaves no line a window may send (unsent).
	write := resolveOne(t, s, record.LeadNeedDefinition, "write", window, anchor).Targets[0]
	mid := (write.Start + write.End) / 2
	first, second := sentLines(t, s, write.Path, write.Start-1, mid), sentLines(t, s, write.Path, mid+1, write.End+1)
	l = resolveOne(t, s, record.LeadNeedDefinition, "write", window, anchor, first, second)
	if write.End <= write.Start || l.Reason != record.LeadInRequest || (len(l.InRequest) != 2 || !slices.Contains(l.InRequest, first.ID) || !slices.Contains(l.InRequest, second.ID)) || l.Targets != nil {
		t.Fatalf("definition sent in two pieces %+v (declaration %+v)", l, write)
	}
	// A document excerpt of the same file (an in-source API document) over
	// the declaration's first lines: the window leaves them out too.
	doc := sentLines(t, s, write.Path, write.Start, write.Start+1)
	doc.Kind = "document"
	l = resolveOne(t, s, record.LeadNeedDefinition, "write", window, anchor, doc)
	if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Start <= doc.End || l.Targets[0].End != write.End || !l.Targets[0].Partial {
		t.Fatalf("definition under a sent document excerpt %+v (declaration %+v)", l, write)
	}
	// A sent unit over the first two lines of a Go caller expansion window:
	// the window starts after them and still holds the call.
	callers := resolveOne(t, s, record.LeadNeedCallers, "Save", decl, declAnchor).Targets[0]
	unit := sentLines(t, s, callers.Path, callers.Start, callers.Start+1)
	l = resolveOne(t, s, record.LeadNeedCallers, "Save", decl, declAnchor, unit)
	if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Start != callers.Start+2 || l.Targets[0].End != callers.End ||
		l.Targets[0].Origin != "caller" || l.Targets[0].Symbol != "Save" || !strings.Contains(sentLines(t, s, callers.Path, l.Targets[0].Start, l.Targets[0].End).Text, "Save(") {
		t.Fatalf("caller window under a sent unit %+v (window %+v)", l, callers)
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
		x.Symbol = sym
		many = append(many, x)
	}
	noCandidate := ok
	noCandidate.Candidate = 0
	if leads, omitted = s.checkLeads([]leadOutput{noCandidate}, true, reply, kept); len(leads) != 0 || !omitted {
		t.Fatalf("a lead without a candidate %+v", leads)
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
// bounded return the call binding keeps). A longer one, or one whose first
// lines the investigate request already carried, is sent as that bounded
// return and marked partial.
func TestLeadDefinitionWindowHoldsTheDeclaration(t *testing.T) {
	body := func(pad int) string {
		return "package quote\n\n// Price returns the price of n items; a negative count gives -1.\nfunc Price(n int) int {\n" +
			strings.Repeat("\t// Each item costs the same.\n", pad) + "\tif n < 0 {\n\t\treturn -1\n\t}\n\treturn n * 3\n}\n"
	}
	caller := "package main\n\nimport \"example.org/shop/quote\"\n\nfunc main() {\n\tprintln(quote.Price(2))\n}\n"
	for _, c := range []struct {
		pad        int
		start, end int
		partial    bool
	}{{28, 4, 37, false}, {70, 75, 78, true}} {
		j := treeJob(t, map[string]string{"go.mod": "module example.org/shop\n\ngo 1.23\n", "main.go": caller, "quote/price.go": body(c.pad)})
		s := leadStage(t, j)
		window, anchor := leadWindow(t, s, "main.go", "quote.Price(2)", 0)
		l := resolveOne(t, s, record.LeadNeedDefinition, "Price", window, anchor)
		if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "quote/price.go" || l.Targets[0].Start != c.start || l.Targets[0].End != c.end ||
			l.Targets[0].Partial != c.partial {
			t.Fatalf("pad %d: lead %+v", c.pad, l)
		}
		if c.pad != 28 {
			continue
		}
		head, _ := leadWindow(t, s, "quote/price.go", "func Price(", 2)
		l = resolveOne(t, s, record.LeadNeedDefinition, "Price", window, anchor, head)
		if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Start != 33 || l.Targets[0].End != 36 || !l.Targets[0].Partial {
			t.Fatalf("declaration head already sent: lead %+v", l)
		}
	}
}

// A Go definition lead binds the call on its cited line, never a
// declaration that only shares the name: os.Open is not the package's own
// Open, Sync on an *os.File or through an interface is not conn's Sync, and
// a field assignment calls nothing. A function the package declares and a
// method on the receiver type the package fixes bind. Two declarations of
// one function across build variants are ambiguous.
func TestGoDefinitionLeadsBindTheCall(t *testing.T) {
	files := "package app\n\ntype conn struct{ Timeout int }\n\n// Sync flushes the connection.\nfunc (c *conn) Sync() error {\n\treturn nil\n}\n\n" +
		"// Open opens a connection; os.Open is another function.\nfunc Open(p string) (*conn, error) {\n\treturn &conn{}, nil\n}\n\n" +
		"// Timeout is the default timeout.\nfunc Timeout() int {\n\treturn 5\n}\n\ntype flusher interface{ Sync() error }\n"
	load := "package app\n\nimport \"os\"\n\nfunc Load(p string, w flusher) error {\n\tf, err := os.Open(p)\n\tif err != nil {\n\t\treturn err\n\t}\n" +
		"\tc := &conn{}\n\tc.Timeout = 5\n\tif err := f.Sync(); err != nil {\n\t\treturn err\n\t}\n\tif err := w.Sync(); err != nil {\n\t\treturn err\n\t}\n" +
		"\tif _, err := Open(p); err != nil {\n\t\treturn err\n\t}\n\treturn c.Sync()\n}\n"
	variant := "package app\n\nfunc write(b []byte) int {\n\treturn len(b)\n}\n"
	j := treeJob(t, map[string]string{"go.mod": "module example.org/app\n\ngo 1.23\n", "app/files.go": files, "app/load.go": load,
		"app/write.go": variant, "app/write_windows.go": variant, "app/use.go": "package app\n\nfunc use() int {\n\treturn write(nil)\n}\n"})
	s := leadStage(t, j)
	for _, c := range []struct {
		line, symbol string
		reason       string
		// start and end are the declaration sent from app/files.go.
		start, end int
	}{
		{"f, err := os.Open(p)", "Open", record.LeadDefinitionMissing, 0, 0},
		{"c.Timeout = 5", "Timeout", record.LeadSymbolNotAtAnchor, 0, 0},
		{"if err := f.Sync(); err != nil {", "Sync", record.LeadDefinitionMissing, 0, 0},
		{"if err := w.Sync(); err != nil {", "Sync", record.LeadDefinitionMissing, 0, 0},
		{"if _, err := Open(p); err != nil {", "Open", "", 11, 13},
		{"return c.Sync()", "Sync", "", 6, 8},
	} {
		window, anchor := leadWindow(t, s, "app/load.go", c.line, 0)
		l := resolveOne(t, s, record.LeadNeedDefinition, c.symbol, window, anchor)
		if c.reason != "" {
			if l.Resolved() || l.Reason != c.reason || l.Targets != nil {
				t.Fatalf("%s: lead %+v", c.line, l)
			}
			continue
		}
		if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "app/files.go" || l.Targets[0].Start != c.start || l.Targets[0].End != c.end || l.Targets[0].Partial {
			t.Fatalf("%s: lead %+v", c.line, l)
		}
	}
	window, anchor := leadWindow(t, s, "app/use.go", "return write(nil)", 0)
	if l := resolveOne(t, s, record.LeadNeedDefinition, "write", window, anchor); l.Resolved() || l.Reason != record.LeadAmbiguous {
		t.Fatalf("build variants: lead %+v", l)
	}
}

// A script definition lead names the callee itself: the call's name or the
// imported name its alias binds, never an argument on the line. The
// definition is sent whole when it fits, not only up to its first return; a
// longer one is sent as the bounded window up to that return and marked
// partial.
func TestScriptDefinitionLeadsNameTheCallee(t *testing.T) {
	calc := func(pad int) string {
		return "export function calc(mode: string) {\n  if (mode === 'fast') {\n    return 2;\n  }\n" +
			strings.Repeat("  // Each other mode has the same rate.\n", pad) + "  return 1;\n}\n"
	}
	run := "import { calc as compute } from './calc.js';\n\nexport function run(mode: string) {\n  return compute(mode);\n}\n"
	for _, c := range []struct {
		pad        int
		start, end int
		partial    bool
	}{{0, 1, 6, false}, {70, 1, 3, true}} {
		j := treeJob(t, map[string]string{"app/run.ts": run, "app/calc.ts": calc(c.pad)})
		s := leadStage(t, j)
		window, anchor := leadWindow(t, s, "app/run.ts", "return compute(mode)", 0)
		if l := resolveOne(t, s, record.LeadNeedDefinition, "mode", window, anchor); l.Resolved() || l.Reason != record.LeadSymbolNotAtAnchor {
			t.Fatalf("pad %d: an argument of the call %+v", c.pad, l)
		}
		for _, symbol := range []string{"compute", "calc"} {
			l := resolveOne(t, s, record.LeadNeedDefinition, symbol, window, anchor)
			if !l.Resolved() || len(l.Targets) != 1 || l.Targets[0].Path != "app/calc.ts" || l.Targets[0].Start != c.start || l.Targets[0].End != c.end ||
				l.Targets[0].Partial != c.partial {
				t.Fatalf("pad %d, %s: lead %+v", c.pad, symbol, l)
			}
		}
	}
}

// A callers lead reads through the lead's one scan: a generated caller is
// neither read nor sent (the same caller in a plain file is), and the
// caller expansion's reads count against the lead's file limit.
func TestGoCallersLeadReadsThroughItsScan(t *testing.T) {
	caller := "package store\n\nfunc generated() {\n\t_ = Save(\"k\", 1)\n}\n"
	for _, generated := range []bool{false, true} {
		text := caller
		if generated {
			text = "// Code generated by tool; DO NOT EDIT.\n\n" + caller
		}
		j := treeJob(t, map[string]string{"go.mod": "module example.org/store\n\ngo 1.23\n", "store/save.go": leadsGoTree, "store/gen.go": text})
		s := leadStage(t, j)
		decl, anchor := leadWindow(t, s, "store/save.go", "func Save(", 1)
		l := resolveOne(t, s, record.LeadNeedCallers, "Save", decl, anchor)
		switch {
		case !generated && (!l.Resolved() || l.Targets[0].Path != "store/gen.go"):
			t.Fatalf("plain caller: lead %+v", l)
		case generated && (l.Resolved() || l.Reason != record.LeadGenerated || l.Targets != nil):
			t.Fatalf("generated caller: lead %+v", l)
		}
		if generated {
			continue
		}
		r := s.newLeadResolver([]record.Excerpt{decl})
		scan := r.scan()
		scan.files = maxScanFiles - 1
		hits, reason, err := r.callers(scan, "Save", anchor)
		if err != nil || len(hits) != 0 || reason != record.LeadScanLimit {
			t.Fatalf("spent scan: %d hits, %q, %v", len(hits), reason, err)
		}
	}
}

// unsent keeps a window's lines the investigate request did not send: the
// whole window when it sent none, the run holding the call, else the
// largest run (the first of equal ones); a window with no unsent line of
// text left names the sent excerpts it overlaps.
func TestUnsentKeepsTheNewLinesOfAWindow(t *testing.T) {
	j := treeJob(t, map[string]string{"go.mod": "module example.org/w\n\ngo 1.23\n", "w.go": "package w\n"})
	digest := j.Snapshot.Digest
	excerpt := func(start, end int, blank ...int) record.Excerpt {
		var lines []string
		for n := start; n <= end; n++ {
			if slices.Contains(blank, n) {
				lines = append(lines, "")
			} else {
				lines = append(lines, "\tv += "+strconv.Itoa(n))
			}
		}
		return record.Excerpt{ID: record.ExcerptID(digest, "w.go", "blob", start, end), Path: "w.go", SHA256: "blob", Start: start, End: end,
			Kind: "code", Origin: "caller", Symbol: "F", Text: strings.Join(lines, "\n")}
	}
	for name, c := range map[string]struct {
		sent       [][2]int
		window     [2]int
		blank      []int
		call       [2]int
		want       [2]int // the kept lines; zero when none is left
		heldExcept int    // the sent excerpts named when none is left
	}{
		"no line sent":                         {[][2]int{{20, 30}}, [2]int{10, 16}, nil, [2]int{13, 13}, [2]int{10, 16}, 0},
		"three sent lines before the call":     {[][2]int{{1, 11}}, [2]int{9, 15}, nil, [2]int{12, 12}, [2]int{12, 15}, 0},
		"split: the run of the call":           {[][2]int{{10, 12}}, [2]int{8, 16}, nil, [2]int{9, 9}, [2]int{8, 9}, 0},
		"split, the call sent: the larger run": {[][2]int{{11, 12}, {13, 13}}, [2]int{9, 16}, nil, [2]int{12, 13}, [2]int{14, 16}, 0},
		"split, equal runs: the first":         {[][2]int{{11, 13}}, [2]int{9, 15}, nil, [2]int{12, 12}, [2]int{9, 10}, 0},
		"every line sent":                      {[][2]int{{9, 12}, {13, 16}}, [2]int{9, 16}, nil, [2]int{12, 13}, [2]int{}, 2},
		"only a blank line left":               {[][2]int{{9, 12}, {14, 16}}, [2]int{9, 16}, []int{13}, [2]int{12, 14}, [2]int{}, 2},
	} {
		t.Run(name, func(t *testing.T) {
			r := &leadResolver{s: &roleStage{ctx: t.Context(), j: j}}
			for _, s := range c.sent {
				r.carried = append(r.carried, excerpt(s[0], s[1]))
			}
			x := excerpt(c.window[0], c.window[1], c.blank...)
			out, held, ok := r.unsent(x, sourceCallLocation{"w.go", c.call[0], c.call[1]})
			if c.want == ([2]int{}) {
				if ok || len(held) != c.heldExcept {
					t.Fatalf("kept %+v, held %v", out, held)
				}
				return
			}
			want := excerpt(c.want[0], c.want[1], c.blank...)
			if !ok || held != nil || out != want {
				t.Fatalf("kept %+v, held %v; want %+v", out, held, want)
			}
		})
	}
}
