package gograph

import (
	"context"
	"strings"
	"testing"
)

func TestProductionCycleAndTestOnlyControl(t *testing.T) {
	sha := strings.Repeat("a", 64)
	m := "example.test/project"
	body := map[string]string{
		"a/a.go":      "package a\n//line misleading.go:500\nimport \"" + m + "/b\"\n",
		"b/b.go":      "package b\nimport \"" + m + "/a\"\n",
		"b/b_test.go": "package b_test\nimport \"" + m + "/a\"\n",
	}
	read := func(p string) ([]byte, string, bool) { s, ok := body[p]; return []byte(s), sha, ok }
	pkgs := []ListedPackage{
		{Node: Node{m + "/a", "a"}, Production: []string{"a/a.go"}},
		{Node: Node{m + "/b", "b"}, Production: []string{"b/b.go"}, ExternalTest: []string{"b/b_test.go"}},
	}
	for _, test := range []struct {
		name, b string
		cycle   bool
	}{
		{"cycle", body["b/b.go"], true},
		{"fixed", "package b\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body["b/b.go"] = test.b
			d := Create(context.Background(), Document{Module: m, Dir: ".", Platform: "linux/amd64", Scope: "all_module_packages"}, pkgs, read)
			if err := Validate(d, func(string) (string, bool) { return sha, true }); err != nil {
				t.Fatal(err)
			}
			es := Cycle(d)
			if (len(es) > 0) != test.cycle {
				t.Fatalf("cycle=%+v", es)
			}
			if test.cycle && (len(es) != 2 || es[0].Witness.Path != "a/a.go" || es[0].Witness.Line != 3 || es[1].Witness.Path != "b/b.go" || es[1].Witness.Line != 2) {
				t.Fatalf("bad physical witness %+v", es)
			}
		})
	}
	pkgs = pkgs[:1]
	d := Create(context.Background(), Document{Module: m, Dir: ".", Platform: "linux/amd64", Scope: "selected_packages"}, pkgs, read)
	if d.Status != "partial" || d.ParsedFiles != 1 || d.UnresolvedImports != 1 || len(d.Edges) != 0 || len(Cycle(d)) != 0 {
		t.Fatalf("requested scope was overstated: %+v", d)
	}
}
