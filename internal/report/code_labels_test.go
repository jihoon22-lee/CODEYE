package report

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// emittedCode is a code string the analyzer, discovery, dependency, review and
// run packages can put into a task reason, a limitation or a coverage counter.
type emittedCode struct {
	value string
	// unit is true for a coverage counter key and false for a reason or a
	// limitation.
	unit  bool
	where string
}

var (
	codeShape = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)+$|^[a-z]{3,}$`)
	// A named constant or variable whose name starts with one of these holds a
	// code. The unit prefixes name coverage counter keys.
	reasonNames = []string{"Reason", "reason", "Lim", "lim", "Note", "note", "ScopeReason"}
	unitNames   = []string{"Unit", "unit", "Package", "TU", "File", "Crate", "Skip"}
	// A call to one of these takes a reason code.
	reasonCalls = map[string]bool{"Failed": true, "NotRun": true, "Fail": true, "Unsupported": true, "Skipped": true,
		"NotConfigured": true, "fail": true, "notRun": true}
)

func hasPrefixIn(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// mentionsLimitation is true for a name such as limitations, lims, addLim or
// ExtraLimitations, and false for limit (a number).
func mentionsLimitation(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "lim") && (!strings.Contains(n, "limit") || strings.Contains(n, "limitation"))
}

func nameOf(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// scanEmittedCodes reads the non-test Go sources under the given directories
// and collects the code strings in the places codes are declared: named
// constants, the Reason and Limitations fields, assignments and appends to a
// limitations list, limitation helpers, reason-taking calls, and the keys of a
// coverage counter map. A code a helper returns or builds from parts is not
// seen (docs/TESTING.md TC-329).
func scanEmittedCodes(t *testing.T, dirs ...string) []emittedCode {
	t.Helper()
	var out []emittedCode
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				return err
			}
			harvest := func(n ast.Node, unit bool) {
				ast.Inspect(n, func(n ast.Node) bool {
					lit, ok := n.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					if v, err := strconv.Unquote(lit.Value); err == nil && codeShape.MatchString(v) {
						out = append(out, emittedCode{v, unit, filepath.ToSlash(path) + ":" + strconv.Itoa(set.Position(lit.Pos()).Line)})
					}
					return true
				})
			}
			ast.Inspect(file, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.ValueSpec:
					for i, name := range x.Names {
						if i >= len(x.Values) {
							continue
						}
						switch {
						case hasPrefixIn(name.Name, unitNames):
							harvest(x.Values[i], true)
						case hasPrefixIn(name.Name, reasonNames) || mentionsLimitation(name.Name):
							harvest(x.Values[i], false)
						}
					}
				case *ast.KeyValueExpr:
					if id, ok := x.Key.(*ast.Ident); ok && (id.Name == "Reason" || id.Name == "TaskReason" || id.Name == "reason" || mentionsLimitation(id.Name)) {
						harvest(x.Value, false)
					}
				case *ast.CallExpr:
					name := nameOf(x.Fun)
					if reasonCalls[name] || mentionsLimitation(name) {
						for _, a := range x.Args {
							harvest(a, false)
						}
					}
					if name == "append" && len(x.Args) >= 2 && mentionsLimitation(nameOf(x.Args[0])) {
						for _, a := range x.Args[1:] {
							harvest(a, false)
						}
					}
				case *ast.IndexExpr:
					if name := nameOf(x.X); name == "Units" || name == "Packages" || name == "units" {
						harvest(x.Index, true)
					}
				case *ast.AssignStmt:
					for i, l := range x.Lhs {
						if i < len(x.Rhs) || len(x.Rhs) == 1 {
							if name := nameOf(l); name == "Reason" || mentionsLimitation(name) {
								harvest(x.Rhs[min(i, len(x.Rhs)-1)], false)
							}
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func labeled(l Locale, code string, unit bool) bool {
	if _, ok := l.CodeLabel(code); ok {
		return true
	}
	if unit {
		_, ok := l.Units[code]
		return ok
	}
	return false
}

// Every code the analyzers, the discovery and dependency tasks, the review and
// the run bookkeeping can emit is worded in the catalog, or is listed in
// labelExempt with the reason it never reaches a report reader. A new code
// without a text fails here, and so does an exemption that no longer applies.
func TestEmittedCodesHaveLabels(t *testing.T) {
	found := scanEmittedCodes(t, "../worker", "../domain", ".")
	if len(found) < 500 {
		t.Fatalf("the scan found only %d codes; it no longer sees the places codes are declared", len(found))
	}
	seen := map[string]bool{}
	var missing []string
	for _, c := range found {
		seen[c.value] = true
		if _, exempt := labelExempt[c.value]; exempt || labeled(Korean, c.value, c.unit) {
			continue
		}
		kind := "reason"
		if c.unit {
			kind = "unit"
		}
		missing = append(missing, kind+" "+c.value+" ("+c.where+")")
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d emitted codes have no text in reportui/code_labels_ko.json:\n%s", len(missing), strings.Join(missing, "\n"))
	}
	for code, why := range labelExempt {
		if why == "" {
			t.Errorf("exemption %q has no reason", code)
		}
		if !seen[code] {
			t.Errorf("exemption %q is stale: the scan no longer finds that code", code)
		} else if labeled(Korean, code, false) {
			t.Errorf("exemption %q is stale: the code has a text", code)
		}
	}
}

// Codes in the stored example reports and observation documents, which real
// worker runs wrote, have texts too: a pattern the scan above cannot see
// (a code built from parts) still has to read as words.
func TestStoredExampleCodesHaveLabels(t *testing.T) {
	type codeAt struct {
		code, where string
		unit        bool
	}
	var codes []codeAt
	var walk func(v any, file string)
	walk = func(v any, file string) {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				switch k {
				case "packages", "units":
					if m, ok := val.(map[string]any); ok {
						for unit := range m {
							codes = append(codes, codeAt{unit, file, true})
						}
						continue
					}
				case "limitations":
					if list, ok := val.([]any); ok {
						for _, s := range list {
							if code, ok := s.(string); ok && codeShape.MatchString(strings.SplitN(code, ":", 2)[0]) {
								codes = append(codes, codeAt{strings.SplitN(code, ":", 2)[0], file, false})
							}
						}
						continue
					}
				case "task_reason", "reason":
					if s, ok := val.(string); ok && codeShape.MatchString(strings.SplitN(s, ":", 2)[0]) {
						codes = append(codes, codeAt{strings.SplitN(s, ":", 2)[0], file, false})
						continue
					}
				}
				walk(val, file)
			}
		case []any:
			for _, e := range x {
				walk(e, file)
			}
		}
	}
	for _, pattern := range []string{"../../examples/report*.json", "../../examples/observations-*.json", "testdata/*.json"} {
		files, err := filepath.Glob(pattern)
		if err != nil || len(files) == 0 {
			t.Fatalf("%s: %v %d", pattern, err, len(files))
		}
		for _, f := range files {
			if strings.HasSuffix(f, "document_examples.json") {
				continue
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var doc any
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			walk(doc, f)
		}
	}
	if len(codes) < 100 {
		t.Fatalf("only %d stored codes found", len(codes))
	}
	var missing []string
	for _, c := range codes {
		if _, exempt := storedExempt[c.code]; !exempt && !labeled(Korean, c.code, c.unit) {
			missing = append(missing, c.code+" in "+c.where)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("stored example codes with no text:\n%s", strings.Join(missing, "\n"))
	}
	for code, why := range storedExempt {
		if why == "" || labeled(Korean, code, false) {
			t.Errorf("stored-code exemption %q is empty or stale", code)
		}
	}
}

func TestCatalogEntriesAreWordedAndDisjoint(t *testing.T) {
	c := koreanCodeLabels
	if len(c.Reasons) < 400 || len(c.Units) < 40 {
		t.Fatalf("catalog too small: %d reasons, %d units", len(c.Reasons), len(c.Units))
	}
	for name, table := range map[string]map[string]string{"reasons": c.Reasons, "model_reasons": c.ModelReasons, "units": c.Units} {
		for code, text := range table {
			if !hasHangul(text) || strings.TrimSpace(text) != text {
				t.Errorf("%s.%s is not a Korean text: %q", name, code, text)
			}
			if strings.Contains(text, "TODO") {
				t.Errorf("%s.%s is unfinished: %q", name, code, text)
			}
		}
	}
	for code := range c.ModelReasons {
		if _, ok := c.Reasons[code]; ok {
			t.Errorf("%s is in both reasons and model_reasons; the first would shadow the second", code)
		}
	}
	// A state that did not run, was unsupported or failed is never worded as a
	// pass: those words stay out of the texts of the codes that mean them.
	for code, text := range c.Reasons {
		if strings.HasSuffix(code, "_failed") && (strings.Contains(text, "통과함") || strings.Contains(text, "문제 없음")) {
			t.Errorf("%s reads as a pass: %q", code, text)
		}
	}
}

func hasHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}

func TestCodeLabelForms(t *testing.T) {
	l := Korean
	if got := l.Code("mypy_failed"); got == "mypy_failed" || !strings.Contains(got, "mypy") {
		t.Errorf("mypy_failed = %q", got)
	}
	if got := l.Code("static_analysis_gap:go"); !strings.HasSuffix(got, " (go)") {
		t.Errorf("a detail follows the text in parentheses: %q", got)
	}
	if got := l.Code("upstream_interpret_skipped"); !strings.Contains(got, "interpret") || got == "upstream_interpret_skipped" {
		t.Errorf("a composed upstream code reads as words with its role: %q", got)
	}
	if got := l.Code("upstream_go_vet_exploded"); got != "upstream_go_vet_exploded" {
		t.Errorf("an unknown upstream state is shown as is: %q", got)
	}
	if got := l.Code("code_nobody_defined"); got != "code_nobody_defined" {
		t.Errorf("an unknown code is shown as is: %q", got)
	}
	if got := l.Unit("analyzed_clean"); got == "analyzed_clean" {
		t.Error("unit text missing")
	}
	if got := l.Unit("no_such_counter"); got != "no_such_counter" {
		t.Errorf("an unknown counter is shown as is: %q", got)
	}
	if got := l.Unit("imports_unresolved"); got == "imports_unresolved" {
		t.Error("a counter with only a reason text reads as that text")
	}
}

// The catalog words what happened and what it means. Where on the web screens
// to act on it is a web-only hint, so no export carries a menu or screen
// pointer, and every text uses the one noun-ending register. TC-329
func TestCatalogHasNoScreenHintsAndOneRegister(t *testing.T) {
	c := koreanCodeLabels
	formal := regexp.MustCompile(`습니다|니다|하세요|으세요|보세요|해야 합`)
	plain := regexp.MustCompile(`(했|았|었|됐|된|한|있|없|않|이)다(\.|\)|$| )`)
	for name, table := range map[string]map[string]string{"reasons": c.Reasons, "model_reasons": c.ModelReasons, "units": c.Units, "upstream": c.Upstream} {
		for code, text := range table {
			if strings.Contains(text, "메뉴") {
				t.Errorf("%s.%s points into a web menu: %q", name, code, text)
			}
			if formal.MatchString(text) || plain.MatchString(text) {
				t.Errorf("%s.%s is not in the noun-ending register: %q", name, code, text)
			}
		}
	}
}
