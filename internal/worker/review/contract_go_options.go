package review

import (
	"go/ast"
	"go/token"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

type goOptionTrace struct {
	windows  []record.Excerpt
	symbol   string
	delivers bool
}

// Only scan source already read for document witnesses. The module
// resolver shares the same read limit when opening in-scope manifests.
func (s *roleStage) goOptionWitness(queries []witnessQuery, sources map[string]string, read func(source.Entry) (string, bool, error)) (goOptionTrace, error) {
	if s.j.PullRequest != nil {
		return goOptionTrace{}, nil
	}
	candidates := map[string]bool{}
	for p, content := range sources {
		if !strings.HasSuffix(p, ".go") || record.SourceKind(p) != "code" || !strings.Contains(content, ".Get") {
			continue
		}
		for _, q := range queries {
			if record.ScopeUnder(p, q.scope) && slices.ContainsFunc(q.navigationNames, func(name string) bool {
				return strings.Contains(content, strconv.Quote(name)) || strings.Contains(content, "\x60"+name+"\x60")
			}) {
				candidates[p] = true
				break
			}
		}
	}
	if len(candidates) == 0 {
		return goOptionTrace{}, nil
	}
	index, err := s.httpRouteIndexFrom(func(e source.Entry) (string, bool, error) {
		if path.Base(e.Path) == "go.mod" {
			return read(e)
		}
		content, ok := sources[e.Path]
		return content, ok, nil
	})
	if err != nil {
		return goOptionTrace{}, err
	}
	// Ambiguous complete routes for one contract fall back to ordinary
	// navigation. Choose at most one route for the overview budget.
	for _, q := range queries {
		var selected goOptionTrace
		ambiguous := false
		for _, f := range index.files {
			if !candidates[f.entry.Path] || !f.eligible || strings.HasPrefix(f.pkg, "\x00") ||
				index.incomplete[path.Dir(f.entry.Path)] || !record.ScopeUnder(f.entry.Path, q.scope) {
				continue
			}
			for _, decl := range f.parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				for at := 1; at < len(fn.Body.List); at++ {
					trace := s.goOptionSwitch(index, f, fn, at, q)
					if len(trace.windows) == 0 {
						continue
					}
					if len(selected.windows) != 0 {
						ambiguous = true
					}
					selected = trace
				}
			}
		}
		if !ambiguous && len(selected.windows) != 0 {
			return selected, nil
		}
	}
	return goOptionTrace{}, nil
}

func (s *roleStage) goOptionSwitch(idx *httpRouteIndex, f *httpRouteFile, fn *ast.FuncDecl, at int, q witnessQuery) goOptionTrace {
	sw, ok := fn.Body.List[at].(*ast.SwitchStmt)
	if !ok || sw.Init != nil || sw.Tag == nil {
		return goOptionTrace{}
	}
	init, ok := fn.Body.List[at-1].(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 {
		return goOptionTrace{}
	}
	option := httpRouteIdent(init.Lhs[0])
	if option == nil || option.Obj == nil || option.Obj.Decl != init || !idx.goOptionBool(f, init.Rhs[0]) {
		return goOptionTrace{}
	}
	name := idx.goOptionKey(f, sw.Tag)
	if name == "" || !slices.Contains(q.navigationNames, name) {
		return goOptionTrace{}
	}
	writes := map[*ast.AssignStmt]bool{init: true}
	for _, entry := range sw.Body.List {
		clause, ok := entry.(*ast.CaseClause)
		if !ok {
			return goOptionTrace{}
		}
		for _, label := range clause.List {
			lit, ok := ast.Unparen(label).(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return goOptionTrace{}
			}
		}
		for _, stmt := range clause.Body {
			if a, ok := stmt.(*ast.AssignStmt); ok && a.Tok == token.ASSIGN && len(a.Lhs) == 1 &&
				len(a.Rhs) == 1 && goOptionRef(a.Lhs[0], option.Obj) && idx.goOptionBool(f, a.Rhs[0]) {
				writes[a] = true
			}
		}
	}
	if len(writes) < 2 || !goOptionWrites(fn.Body, option.Obj, writes) {
		return goOptionTrace{}
	}
	start, end := f.line(init.Pos()), f.line(sw.End())
	parse := s.goWrapperExcerpt(f.contractGoFile, start, end, witnessMaxLines, witnessMaxExcerpt)
	if parse.ID == "" {
		return goOptionTrace{}
	}
	var selected goOptionTrace
	for _, stmt := range fn.Body.List[at+1:] {
		call := goOptionHandoff(stmt)
		if call == nil {
			continue
		}
		position := -1
		for i, arg := range call.Args {
			if goOptionRef(arg, option.Obj) {
				if position >= 0 {
					position = -1
					break
				}
				position = i
			}
		}
		if position < 0 {
			continue
		}
		key := idx.key(f, call.Fun)
		if key.owner.name != "" || strings.HasPrefix(key.owner.pkg, "\x00") {
			continue
		}
		target := idx.function(key)
		if target.decl == nil || !record.ScopeUnder(target.file.entry.Path, q.scope) ||
			(target.file.pkg != f.pkg && !ast.IsExported(key.name)) {
			continue
		}
		params := httpRouteFields(target.decl.Type.Params)
		if call.Ellipsis.IsValid() || len(params) != len(call.Args) || len(params[position].Names) != 1 ||
			target.file.signatureType(params[position].Type, 0) != "bool" ||
			idx.bindings[httpRouteName{target.file.pkg, "bool"}] != 0 {
			continue
		}
		decision := s.goOptionDecision(idx, target, params[position].Names[0].Obj, name, q.scope)
		if len(decision) == 0 {
			continue
		}
		handoff := s.goOptionHandoffWindow(f, fn, stmt, end)
		if handoff.ID == "" {
			continue
		}
		var delivery record.Excerpt
		if assignment, ok := stmt.(*ast.AssignStmt); ok {
			delivery = s.goOptionDelivery(idx, f, fn, assignment, option.Obj, handoff)
			if delivery.ID == "" {
				continue
			}
		}
		if len(selected.windows) != 0 {
			return goOptionTrace{}
		}
		selected = goOptionTrace{windows: append([]record.Excerpt{parse, handoff}, decision...), symbol: key.name}
		if delivery.ID != "" {
			selected.delivers = true
			if delivery.ID != handoff.ID {
				selected.windows = append(selected.windows, delivery)
			}
		}
	}
	return selected
}

func goOptionHandoff(stmt ast.Stmt) *ast.CallExpr {
	var values []ast.Expr
	switch x := stmt.(type) {
	case *ast.AssignStmt:
		values = x.Rhs
	case *ast.ReturnStmt:
		values = x.Results
	}
	if len(values) == 1 {
		call, _ := ast.Unparen(values[0]).(*ast.CallExpr)
		return call
	}
	return nil
}

func (s *roleStage) goOptionHandoffWindow(f *httpRouteFile, fn *ast.FuncDecl, stmt ast.Stmt, parseEnd int) record.Excerpt {
	first, last := f.line(stmt.Pos()), f.line(stmt.End())
	for _, span := range [][2]int{
		{max(parseEnd+1, first-11), min(f.line(fn.Body.End()), last+7)},
		{first, last},
	} {
		if x := s.goWrapperExcerpt(f.contractGoFile, span[0], span[1], witnessMaxLines, witnessMaxExcerpt); x.ID != "" {
			return x
		}
	}
	return record.Excerpt{}
}

func (idx *httpRouteIndex) goOptionBool(f *httpRouteFile, e ast.Expr) bool {
	id := httpRouteIdent(e)
	return id != nil && id.Obj == nil && (id.Name == "true" || id.Name == "false") &&
		idx.bindings[httpRouteName{f.pkg, id.Name}] == 0
}

func goOptionRef(e ast.Expr, obj *ast.Object) bool {
	id := httpRouteIdent(e)
	return obj != nil && id != nil && id.Obj == obj
}

func goOptionWrites(body *ast.BlockStmt, option *ast.Object, allowed map[*ast.AssignStmt]bool) bool {
	safe := true
	ast.Inspect(body, func(n ast.Node) bool {
		if !safe {
			return false
		}
		switch x := n.(type) {
		case *ast.AssignStmt:
			safe = allowed[x] || !slices.ContainsFunc(x.Lhs, func(v ast.Expr) bool { return goOptionWriteTarget(v, option) })
		case *ast.UnaryExpr:
			safe = x.Op != token.AND || !goOptionWriteTarget(x.X, option)
		case *ast.RangeStmt:
			safe = !goOptionRef(x.Key, option) && !goOptionRef(x.Value, option)
		case *ast.IncDecStmt:
			safe = !goOptionWriteTarget(x.X, option)
		}
		return safe
	})
	return safe
}

func goOptionWriteTarget(e ast.Expr, root *ast.Object) bool {
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		default:
			return goOptionRef(e, root)
		}
	}
}

func (idx *httpRouteIndex) goOptionKey(f *httpRouteFile, e ast.Expr) string {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return ""
	}
	get, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || get.Sel.Name != "Get" || !idx.goQueryValues(f, get.X) {
		return ""
	}
	lit, ok := ast.Unparen(call.Args[0]).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	name, _ := strconv.Unquote(lit.Value)
	return name
}

func (idx *httpRouteIndex) goQueryValues(f *httpRouteFile, expr ast.Expr) bool {
	if idx.exprType(f, expr, 0) == (httpRouteName{"net/url", "Values"}) {
		return true
	}
	if id := httpRouteIdent(expr); id != nil && id.Obj != nil && !f.shadowed[id.Obj] {
		if assign, ok := id.Obj.Decl.(*ast.AssignStmt); ok && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 {
			expr = assign.Rhs[0]
		}
	}
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	query, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || query.Sel.Name != "Query" {
		return false
	}
	if idx.exprType(f, query.X, 0) == (httpRouteName{"net/url", "URL"}) {
		return true
	}
	url, ok := ast.Unparen(query.X).(*ast.SelectorExpr)
	return ok && url.Sel.Name == "URL" && idx.exprType(f, url.X, 0) == (httpRouteName{"net/http", "Request"})
}

func (s *roleStage) goOptionDecision(idx *httpRouteIndex, target httpRouteFunc, option *ast.Object, name, scope string) []record.Excerpt {
	f, fn := target.file, target.decl
	body := fn.Body.List
	if len(body) < 2 || !goOptionWrites(fn.Body, option, nil) {
		return nil
	}
	guard, ok := body[0].(*ast.IfStmt)
	if !ok || guard.Init != nil || len(guard.Body.List) != 1 {
		return nil
	}
	if !goOptionCondition(guard.Cond, option) {
		return nil
	}
	if _, ok := guard.Body.List[0].(*ast.ReturnStmt); !ok {
		return nil
	}
	last, ok := body[len(body)-1].(*ast.ReturnStmt)
	if !ok || len(last.Results) != 1 || guard.Else != nil {
		return nil
	}
	from, to := f.line(fn.Pos()), f.line(last.End())
	if x := s.goWrapperExcerpt(f.contractGoFile, from, to, witnessMaxLines, witnessMaxExcerpt); x.ID != "" {
		return []record.Excerpt{x}
	}
	// A long projection may have a short assignment to the same value
	// it eventually returns. Keep both exact, separated source windows.
	result := httpRouteIdent(last.Results[0])
	if result == nil || result.Obj == nil {
		return nil
	}
	for i, stmt := range body[1 : len(body)-1] {
		a, ok := stmt.(*ast.AssignStmt)
		if !ok || a.Tok != token.ASSIGN || len(a.Lhs) != 1 || len(a.Rhs) != 1 {
			continue
		}
		field, ok := ast.Unparen(a.Lhs[0]).(*ast.SelectorExpr)
		if !ok || !goOptionRef(field.X, result.Obj) {
			continue
		}
		if !goOptionProjectionStable(body[i+2:], result.Obj, field.Sel.Name) {
			continue
		}
		lit, ok := ast.Unparen(a.Rhs[0]).(*ast.CompositeLit)
		if !ok {
			continue
		}
		tag := s.goOptionWireField(idx, f, lit, name, scope)
		if tag.ID == "" {
			continue
		}
		for end := min(from+witnessMaxLines-1, f.line(last.Pos())-1); end >= f.line(a.End()); end-- {
			branch := s.goWrapperExcerpt(f.contractGoFile, from, end, witnessMaxLines, witnessMaxExcerpt)
			ret := s.goWrapperExcerpt(f.contractGoFile, max(end+1, f.line(last.Pos())-3), to, witnessMaxLines, witnessMaxExcerpt)
			if branch.ID != "" && ret.ID != "" {
				return []record.Excerpt{branch, ret, tag}
			}
		}
	}
	return nil
}

func goOptionProjectionStable(stmts []ast.Stmt, result *ast.Object, field string) bool {
	safe := true
	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if !safe {
				return false
			}
			if a, ok := n.(*ast.AssignStmt); ok {
				safe = !slices.ContainsFunc(a.Lhs, func(e ast.Expr) bool {
					if goOptionRef(e, result) {
						return true
					}
					for {
						selector, ok := ast.Unparen(e).(*ast.SelectorExpr)
						if !ok {
							return false
						}
						if goOptionRef(selector.X, result) {
							return field == "" || selector.Sel.Name == field
						}
						e = selector.X
					}
				})
			}
			return safe
		})
	}
	return safe
}

func (s *roleStage) goOptionWireField(idx *httpRouteIndex, f *httpRouteFile, lit *ast.CompositeLit, name, scope string) record.Excerpt {
	def := idx.typeDecl(f.namedType(lit.Type))
	if def.decl == nil || !record.ScopeUnder(def.file.entry.Path, scope) {
		return record.Excerpt{}
	}
	st, ok := def.decl.(*ast.StructType)
	if !ok {
		return record.Excerpt{}
	}
	for _, n := range lit.Elts {
		key, ok := n.(*ast.KeyValueExpr)
		if !ok || !idx.goOptionBool(f, key.Value) {
			continue
		}
		id := httpRouteIdent(key.Key)
		for _, field := range st.Fields.List {
			if id == nil || field.Tag == nil || len(field.Names) != 1 || field.Names[0].Name != id.Name {
				continue
			}
			raw, _ := strconv.Unquote(field.Tag.Value)
			tag, _, _ := strings.Cut(reflect.StructTag(raw).Get("json"), ",")
			if tag == name {
				return s.httpRouteExcerpt(def.file, field, witnessMaxLines, witnessMaxExcerpt, true)
			}
		}
	}
	return record.Excerpt{}
}
