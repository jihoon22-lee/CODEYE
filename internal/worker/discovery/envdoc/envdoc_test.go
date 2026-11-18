package envdoc_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var bodies = map[string]string{
	"go.mod":           "module r\n\ngo 1.22\n",
	"svc/b/go.mod":     "module b\n\ngo 1.30\n",
	"web/package.json": `{"engines":{"node":">=18"}}`,
	"README.md":        "root\n",
	"svc/b/README.md":  "sub\n",
	"docs/big.md":      strings.Repeat("x", 100),
}

func fixture(t *testing.T) (discovery.Input, envdoc.Document, envdoc.Document) {
	t.Helper()
	m := source.Manifest{}
	for _, p := range []string{"README.md", "docs/big.md", "go.mod", "svc/b/README.md", "svc/b/go.mod", "web/package.json"} {
		size := int64(len(bodies[p]))
		m.Entries = append(m.Entries, source.Entry{Path: p, Kind: "file", Size: &size, SHA256: domain.Digest([]byte(bodies[p])), Encoding: "utf-8"})
	}
	in := discovery.Input{Manifest: m, Digest: domain.Digest([]byte("manifest")), Scope: domain.Scope{Kind: "full"}, RunID: domain.NewID(), AttemptID: domain.NewID()}
	read := func(e source.Entry) ([]byte, error) { return []byte(bodies[e.Path]), nil }
	env, _, err := discovery.Environment(in, read, func(tools []string) discovery.Observed {
		return discovery.StaticObserver{Versions: map[string]string{"go": "1.27.4"}}.Observe(context.Background(), analysis.Job{}, tools, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	docs, err := discovery.Documents(in, discovery.Budget{TotalBytes: 50, PerDocumentBytes: 40, MaxDocuments: 10}, read)
	if err != nil {
		t.Fatal(err)
	}
	return in, env, docs
}

func clone(t *testing.T, d envdoc.Document) envdoc.Document {
	t.Helper()
	b, _ := json.Marshal(d)
	var c envdoc.Document
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func ptr(s string) *string { return &s }

func TestValidateRejectsInconsistentDocuments(t *testing.T) {
	in, env, docs := fixture(t)
	for _, d := range []envdoc.Document{env, docs} {
		b, err := envdoc.Marshal(d, in.Expect(d.Kind))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := envdoc.Decode(b, in.Expect(d.Kind)); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]struct {
		base envdoc.Document
		edit func(*envdoc.Document)
		want envdoc.Expect
	}{
		"other attempt":         {env, func(d *envdoc.Document) { d.AttemptID = domain.NewID() }, envdoc.Expect{}},
		"wrong kind for task":   {env, func(*envdoc.Document) {}, envdoc.Expect{Kind: envdoc.KindDocuments}},
		"both sections":         {env, func(d *envdoc.Document) { d.Documents = docs.Documents }, envdoc.Expect{}},
		"unsorted modules":      {env, func(d *envdoc.Document) { d.Modules[0], d.Modules[1] = d.Modules[1], d.Modules[0] }, envdoc.Expect{}},
		"module id":             {env, func(d *envdoc.Document) { d.Modules[0].ID = "go:elsewhere" }, envdoc.Expect{}},
		"unknown manifest":      {env, func(d *envdoc.Document) { d.Modules[0].Manifests = []string{"go.sum"} }, envdoc.Expect{}},
		"evidence id moved":     {env, func(d *envdoc.Document) { d.Environment.Requirements[0].Source.EndLine++ }, envdoc.Expect{}},
		"blob of another file":  {env, func(d *envdoc.Document) { d.Environment.Requirements[0].Source.SHA256 = strings.Repeat("0", 64) }, envdoc.Expect{}},
		"comparison claimed":    {env, func(d *envdoc.Document) { d.Environment.Requirements[0].Comparison = envdoc.ComparePrefix }, envdoc.Expect{}},
		"verdict flipped":       {env, func(d *envdoc.Document) { flip(d, "go:svc/b", envdoc.VerdictSatisfied) }, envdoc.Expect{}},
		"unobserved compared":   {env, func(d *envdoc.Document) { flip(d, "javascript:web", envdoc.VerdictSatisfied) }, envdoc.Expect{}},
		"wrong unknown reason":  {env, func(d *envdoc.Document) { reason(d, "javascript:web", envdoc.ReasonNotDeclared) }, envdoc.Expect{}},
		"observed no version":   {env, func(d *envdoc.Document) { d.Environment.Observations[0].Version = nil }, envdoc.Expect{}},
		"devel version":         {env, func(d *envdoc.Document) { d.Environment.Observations[0].Version = ptr("devel") }, envdoc.Expect{}},
		"not_probed with exec":  {env, func(d *envdoc.Document) { d.Environment.Observations[1].Execution = ptr("go.version") }, envdoc.Expect{}},
		"unsorted limitations":  {docs, func(d *envdoc.Document) { d.Limitations = []string{"b", "a"} }, envdoc.Expect{}},
		"read bytes":            {docs, func(d *envdoc.Document) { d.Documents.Budget.ReadBytes++ }, envdoc.Expect{}},
		"selected over the cap": {docs, func(d *envdoc.Document) { d.Documents.Budget.PerDocumentBytes = 2 }, envdoc.Expect{}},
		"applies_to":            {docs, func(d *envdoc.Document) { d.Documents.Items[0].AppliesTo = "svc" }, envdoc.Expect{}},
		"line_count unread":     {docs, func(d *envdoc.Document) { n := 1; d.Documents.Items[1].LineCount = &n }, envdoc.Expect{}},
		"platform conflict":     {docs, func(d *envdoc.Document) { d.Documents.OverlappingGuidance[0].Assessment = "conflict" }, envdoc.Expect{}},
		"overlap not applying":  {docs, func(d *envdoc.Document) { d.Documents.OverlappingGuidance[0].Path = "web" }, envdoc.Expect{}},
		"state none":            {docs, func(d *envdoc.Document) { d.Documents.Modules[2].State = envdoc.StateNone }, envdoc.Expect{}},
		"inherited own":         {docs, func(d *envdoc.Document) { d.Documents.Modules[2].Inherited = []string{"svc/b/README.md"} }, envdoc.Expect{}},
	}
	for name, c := range cases {
		d := clone(t, c.base)
		c.edit(&d)
		want := in.Expect(d.Kind)
		if c.want.Kind != "" {
			want.Kind = c.want.Kind
		}
		if _, err := envdoc.Marshal(d, want); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Strict decoding: unknown fields and trailing data are refused.
	b, _ := envdoc.Marshal(env, in.Expect(""))
	if _, err := envdoc.Decode(append(b[:len(b)-1], []byte(`,"extra":1}`)...), in.Expect("")); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func flip(d *envdoc.Document, module, status string) {
	for i := range d.Environment.Verdicts {
		if d.Environment.Verdicts[i].Module == module {
			d.Environment.Verdicts[i].Status, d.Environment.Verdicts[i].Reason = status, nil
		}
	}
}

func reason(d *envdoc.Document, module, r string) {
	for i := range d.Environment.Verdicts {
		if d.Environment.Verdicts[i].Module == module {
			d.Environment.Verdicts[i].Reason = &r
		}
	}
}
