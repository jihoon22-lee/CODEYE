package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var documentedDefaultWords = regexp.MustCompile(`(?i)\b(default|defaults|omitted|unset|absent|unspecified)\b|기본|생략|미설정|설정하지 않`)

func goDefaultFields(statement, raw string) []string {
	if !documentedDefaultWords.MatchString(statement) || !documentedDefaultWords.MatchString(raw) {
		return nil
	}
	var out []string
	for _, name := range navigationIdentifiers(statement) {
		if record.ValidContractLiteral(name) && containsContractLiteral(raw, name) && !documentedDefaultWords.MatchString(name) {
			out = append(out, defaultFieldName(name))
		}
	}
	return slices.Compact(out)
}

func defaultFieldName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", ""))
}

type goDefaultSite struct {
	field      string
	start, end int
	score      int
}

// goDefaultWitnesses matches documented field names to concrete Go
// initialization or absent-value branches in a verified source file.
// It selects source ranges only. The model still decides what they mean.
func goDefaultWitnesses(e source.Entry, content string, queries []witnessQuery) []contractWitness {
	if !strings.HasSuffix(e.Path, ".go") || record.SourceKind(e.Path) != "code" ||
		!slices.ContainsFunc(queries, func(q witnessQuery) bool {
			return len(q.goDefaults) > 0 && record.ScopeUnder(e.Path, q.scope)
		}) {
		return nil
	}
	set := token.NewFileSet()
	root, err := parser.ParseFile(set, e.Path, content, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	lines := strings.Split(content, "\n")
	var sites []goDefaultSite
	add := func(field string, start, end token.Pos, score int) {
		if field == "" {
			return
		}
		first, last := set.PositionFor(start, false).Line, set.PositionFor(end, false).Line
		if first > 0 && last >= first && last <= len(lines) && fitsWitness(lines, first, last) {
			sites = append(sites, goDefaultSite{defaultFieldName(field), first, last, score})
		}
	}
	for _, d := range root.Decls {
		// A validation rejection can return a zero value. That result
		// is independent of what the product selects for missing input.
		validation, initializerScore := false, 100
		if f, ok := d.(*ast.FuncDecl); ok {
			name := strings.ToLower(f.Name.Name)
			validation = strings.HasPrefix(name, "validat") || strings.HasPrefix(name, "check") || strings.HasPrefix(name, "verify")
			if strings.Contains(name, "default") {
				initializerScore = 130
			}
		}
		ast.Inspect(d, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CompositeLit:
				if validation {
					break
				}
				for _, child := range x.Elts {
					pair, ok := child.(*ast.KeyValueExpr)
					if !ok || !goDefaultValue(pair.Value) {
						continue
					}
					if key, ok := pair.Key.(*ast.Ident); ok {
						start := pair.Pos()
						if set.PositionFor(pair.Pos(), false).Line-set.PositionFor(x.Pos(), false).Line < 3 {
							start = x.Pos()
						}
						add(key.Name, start, pair.End(), initializerScore)
					}
				}
			case *ast.IfStmt:
				if !validation {
					add(goDefaultIf(x), x.Pos(), x.Body.End(), 120)
				}
			case *ast.ReturnStmt:
				if !validation && len(x.Results) == 1 {
					add(goBooleanDefault(x.Results[0]), x.Pos(), x.End(), 120)
				}
			}
			return true
		})
	}
	out := make([]contractWitness, len(queries))
	for i, q := range queries {
		if !record.ScopeUnder(e.Path, q.scope) {
			continue
		}
		for _, site := range sites {
			if !slices.Contains(q.goDefaults, site.field) || site.score <= out[i].score {
				continue
			}
			out[i] = contractWitness{entry: e, line: site.start, end: site.end, score: site.score, query: i + 1, direct: true}
		}
	}
	return out
}

func goDefaultValue(e ast.Expr) bool {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		return x.Name == "true" || x.Name == "false"
	case *ast.BasicLit:
		return x.Kind == token.INT || x.Kind == token.STRING || x.Kind == token.FLOAT
	case *ast.BinaryExpr:
		switch x.Op {
		case token.EQL, token.NEQ, token.GTR, token.GEQ, token.LSS, token.LEQ:
			return true
		}
	}
	return false
}

func goDefaultIf(x *ast.IfStmt) string {
	field := goAbsentField(x.Cond, token.EQL)
	if field == "" {
		return ""
	}
	for _, stmt := range x.Body.List {
		switch s := stmt.(type) {
		case *ast.ReturnStmt:
			if len(s.Results) > 0 && goDefaultValue(s.Results[0]) &&
				(len(s.Results) == 1 || len(s.Results) == 2 && zeroConfigValue(s.Results[1])) {
				return field
			}
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				if i < len(s.Rhs) && goSelectorName(lhs) == field && goDefaultValue(s.Rhs[i]) {
					return field
				}
			}
		}
	}
	return ""
}

func goBooleanDefault(e ast.Expr) string {
	b, ok := ast.Unparen(e).(*ast.BinaryExpr)
	if !ok {
		return ""
	}
	want := token.EQL
	if b.Op == token.LAND {
		want = token.NEQ
	} else if b.Op != token.LOR {
		return ""
	}
	field := goAbsentField(b.X, want)
	rhs := ast.Unparen(b.Y)
	if star, ok := rhs.(*ast.StarExpr); ok {
		rhs = ast.Unparen(star.X)
	}
	if field != "" && goSelectorName(rhs) == field {
		return field
	}
	return ""
}

func goAbsentField(e ast.Expr, op token.Token) string {
	b, ok := ast.Unparen(e).(*ast.BinaryExpr)
	if !ok || b.Op != op {
		return ""
	}
	if zeroConfigValue(b.X) {
		return goSelectorName(b.Y)
	}
	if zeroConfigValue(b.Y) {
		return goSelectorName(b.X)
	}
	return ""
}

func zeroConfigValue(e ast.Expr) bool {
	switch x := ast.Unparen(e).(type) {
	case *ast.Ident:
		return x.Name == "nil"
	case *ast.BasicLit:
		return x.Value == "0" || x.Value == `""` || x.Value == "``"
	}
	return false
}

func goSelectorName(e ast.Expr) string {
	if s, ok := ast.Unparen(e).(*ast.SelectorExpr); ok {
		return s.Sel.Name
	}
	return ""
}
