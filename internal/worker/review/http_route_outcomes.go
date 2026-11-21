package review

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type httpOutcomeKind int

const (
	httpOutcomeNone httpOutcomeKind = iota
	httpOutcomeRead
	httpOutcomeWrite
)

type httpOutcome struct {
	window record.Excerpt
	score  int
}

// Follow result forwarding only through unique same-package functions and
// direct calls on the same receiver. At most one extra bridge can be sent,
// so an earlier step must already be present in the baseline decision.
func (s *roleStage) httpRouteOutcome(idx *httpRouteIndex, first httpRouteFunc, baseline record.Excerpt, method string) (bridge, outcome record.Excerpt, symbol string, hops int) {
	var best int
	var incoming record.Excerpt
	visited := map[*ast.FuncDecl]bool{}
	preferRead := method == "GET" || method == "HEAD"
	for target, depth := first, 0; target.decl != nil && depth <= 2 && !visited[target.decl]; depth++ {
		visited[target.decl] = true
		blocks := idx.httpOutcomeBlocks(target)
		for _, block := range blocks {
			x := s.httpOutcomeInBlock(idx, target.file, block, preferRead)
			if x.window.ID != "" && x.score > best && !httpContainsWindow(baseline, x.window) {
				name := target.decl.Name.Name
				if !record.ValidContractLiteral(name) {
					name = ""
				}
				best, bridge, outcome, symbol, hops = x.score, incoming, x.window, name, depth
			}
		}
		if depth > 0 && !httpContainsWindow(baseline, incoming) {
			break
		}
		next, site := idx.httpOutcomeForward(target, blocks)
		if next.decl == nil {
			break
		}
		incoming = s.httpRouteExcerpt(target.file, site, record.MaxContractLocationLines, roleMaxWindowBytes, true)
		if incoming.ID == "" {
			break
		}
		target = next
	}
	return bridge, outcome, symbol, hops
}

func httpContainsWindow(parent, child record.Excerpt) bool {
	return child.ID != "" && parent.Path == child.Path && parent.SHA256 == child.SHA256 && parent.Start <= child.Start && child.End <= parent.End
}

// Only open a function literal passed directly to an identified wrapper
// which invokes that exact callback parameter in a top-level statement.
// The resulting window is conditional source evidence about the callback.
func (idx *httpRouteIndex) httpOutcomeBlocks(target httpRouteFunc) []*ast.BlockStmt {
	body := target.decl.Body
	blocks := []*ast.BlockStmt{body}
	for _, stmt := range body.List {
		call := httpStatementCall(stmt)
		if call == nil || call.Ellipsis.IsValid() {
			continue
		}
		for i, arg := range call.Args {
			fn, ok := ast.Unparen(arg).(*ast.FuncLit)
			if !ok {
				continue
			}
			wrapper := idx.httpOutcomeTarget(target, call)
			if wrapper.decl != nil && wrapper.decl != target.decl && httpInvokesArgument(wrapper, i) {
				blocks = append(blocks, fn.Body)
				if len(blocks) >= 3 {
					return blocks
				}
			}
		}
	}
	return blocks
}

func httpInvokesArgument(wrapper httpRouteFunc, i int) bool {
	params := httpRouteFields(wrapper.decl.Type.Params)
	if i >= len(params) || len(params[i].Names) != 1 {
		return false
	}
	binding := params[i].Names[0].Obj
	if _, ok := ast.Unparen(params[i].Type).(*ast.FuncType); !ok || binding == nil || wrapper.file.shadowed[binding] {
		return false
	}
	for _, stmt := range wrapper.decl.Body.List {
		call := httpStatementCall(stmt)
		if call != nil {
			if id := httpRouteIdent(call.Fun); id != nil && id.Obj == binding {
				return true
			}
		}
	}
	return false
}

func (idx *httpRouteIndex) httpOutcomeForward(from httpRouteFunc, blocks []*ast.BlockStmt) (httpRouteFunc, ast.Stmt) {
	var target httpRouteFunc
	var site ast.Stmt
	for _, block := range blocks {
		if len(block.List) == 0 {
			continue
		}
		ret, ok := block.List[len(block.List)-1].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			continue
		}
		call := httpStatementCall(ret)
		if call == nil || slices.ContainsFunc(call.Args, func(arg ast.Expr) bool {
			_, callback := ast.Unparen(arg).(*ast.FuncLit)
			return callback
		}) {
			continue
		}
		next := idx.httpOutcomeTarget(from, call)
		if next.decl == nil || next.decl == from.decl {
			continue
		}
		if target.decl != nil {
			return httpRouteFunc{}, nil
		}
		target, site = next, ret
	}
	return target, site
}

func (idx *httpRouteIndex) httpOutcomeTarget(from httpRouteFunc, call *ast.CallExpr) httpRouteFunc {
	if call.Ellipsis.IsValid() {
		return httpRouteFunc{}
	}
	fun := ast.Unparen(call.Fun)
	// AST resolution of explicit generic arguments still uses that exact
	// function binding. Indexing a field or a function variable is excluded.
	switch expr := fun.(type) {
	case *ast.IndexExpr:
		fun = ast.Unparen(expr.X)
	case *ast.IndexListExpr:
		fun = ast.Unparen(expr.X)
	}
	key := idx.key(from.file, fun)
	if key.owner.pkg != from.file.pkg {
		return httpRouteFunc{}
	}
	if key.owner.name != "" {
		sel, _ := fun.(*ast.SelectorExpr)
		if sel == nil {
			return httpRouteFunc{}
		}
		id := httpRouteIdent(sel.X)
		if id == nil || id.Obj == nil || id.Obj != httpRouteReceiver(from.decl) || from.file.shadowed[id.Obj] {
			return httpRouteFunc{}
		}
	}
	next := idx.function(key)
	if next.decl != nil {
		if len(httpRouteFields(next.decl.Type.Params)) != len(call.Args) ||
			(fun != ast.Unparen(call.Fun) && next.decl.Type.TypeParams == nil) {
			return httpRouteFunc{}
		}
	}
	return next
}

func (s *roleStage) httpOutcomeInBlock(idx *httpRouteIndex, f *httpRouteFile, block *ast.BlockStmt, preferRead bool) httpOutcome {
	var best httpOutcome
	kinds := make([]httpOutcomeKind, len(block.List))
	for i, stmt := range block.List {
		kinds[i] = idx.httpOutcomeStatement(f, stmt, preferRead)
	}
	for i, kind := range kinds {
		if kind == httpOutcomeNone {
			continue
		}
		x := s.httpOutcomeWindow(f, block.List, i)
		if x.ID == "" {
			continue
		}
		score := 1
		if (kind == httpOutcomeRead) == preferRead {
			score = 32
		}
		for j := max(0, i-4); j < min(len(block.List), i+7); j++ {
			nearby := block.List[j]
			if j != i && kinds[j] == kind && x.Start <= f.line(nearby.Pos()) && f.line(nearby.End()) <= x.End {
				score++
			}
		}
		if score > best.score {
			best = httpOutcome{x, score}
		}
	}
	return best
}

func (s *roleStage) httpOutcomeWindow(f *httpRouteFile, statements []ast.Stmt, at int) record.Excerpt {
	start, end := f.line(statements[at].Pos()), f.line(statements[at].End())
	window := s.httpRouteExcerptLines(f, start, end, record.MaxContractLocationLines, roleMaxWindowBytes, true)
	if window.ID == "" {
		return window
	}
	// Include nearby branches and return statements without truncating the
	// effect itself or spanning statements from outside this block.
	for i := at + 1; i < min(len(statements), at+7); i++ {
		end := f.line(statements[i].End())
		x := s.httpRouteExcerptLines(f, window.Start, end, record.MaxContractLocationLines, roleMaxWindowBytes, true)
		if x.ID == "" {
			break
		}
		window = x
	}
	for i := at - 1; i >= max(0, at-4); i-- {
		start := f.line(statements[i].Pos())
		x := s.httpRouteExcerptLines(f, start, window.End, record.MaxContractLocationLines, roleMaxWindowBytes, true)
		if x.ID == "" {
			break
		}
		window = x
	}
	return window
}

func (idx *httpRouteIndex) httpOutcomeStatement(f *httpRouteFile, stmt ast.Stmt, preferRead bool) httpOutcomeKind {
	best := httpOutcomeNone
	ast.Inspect(stmt, func(n ast.Node) bool {
		var kind httpOutcomeKind
		switch n := n.(type) {
		case *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
			return false
		case *ast.CallExpr:
			kind = idx.httpOutcomeSQL(f, n)
		case *ast.AssignStmt:
			if slices.ContainsFunc(n.Lhs, func(e ast.Expr) bool { return idx.httpOutcomeField(f, e, true) }) {
				kind = httpOutcomeWrite
			}
		case *ast.ReturnStmt:
			if slices.ContainsFunc(n.Results, func(e ast.Expr) bool { return idx.httpOutcomeField(f, e, false) }) {
				kind = httpOutcomeRead
			}
		}
		if kind != httpOutcomeNone && (best == httpOutcomeNone || (kind == httpOutcomeRead) == preferRead) {
			best = kind
		}
		return true
	})
	return best
}

func (idx *httpRouteIndex) httpOutcomeField(f *httpRouteFile, expr ast.Expr, write bool) bool {
	indexed := false
	if item, ok := ast.Unparen(expr).(*ast.IndexExpr); ok {
		expr, indexed = item.X, true
	}
	field, _ := ast.Unparen(expr).(*ast.SelectorExpr)
	if field == nil {
		return false
	}
	id := httpRouteIdent(field.X)
	if id == nil || id.Obj == nil || f.shadowed[id.Obj] {
		return false
	}
	param, _ := id.Obj.Decl.(*ast.Field)
	if param == nil {
		return false
	}
	if write && !indexed {
		if _, pointer := ast.Unparen(param.Type).(*ast.StarExpr); !pointer {
			return false
		}
	}
	typ, ok := idx.typeDecl(f.namedType(param.Type)).decl.(*ast.StructType)
	if !ok {
		return false
	}
	for _, member := range typ.Fields.List {
		for _, name := range member.Names {
			if name.Name == field.Sel.Name {
				if write && indexed {
					switch member.Type.(type) {
					case *ast.MapType:
						return true
					case *ast.ArrayType:
						return member.Type.(*ast.ArrayType).Len == nil
					default:
						return false
					}
				}
				return true
			}
		}
	}
	return false
}

func (idx *httpRouteIndex) httpOutcomeSQL(f *httpRouteFile, call *ast.CallExpr) httpOutcomeKind {
	sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if sel == nil {
		return httpOutcomeNone
	}
	t := idx.exprType(f, sel.X, 0)
	method := sel.Sel.Name
	arg := 0
	switch t.pkg {
	case "database/sql":
		if t.name != "DB" && t.name != "Tx" && t.name != "Conn" {
			return httpOutcomeNone
		}
		if base, contextual := strings.CutSuffix(method, "Context"); contextual {
			method = base
			arg = 1
		} else if t.name == "Conn" {
			return httpOutcomeNone
		}
	case "github.com/jackc/pgx/v4", "github.com/jackc/pgx/v5":
		if t.name != "Tx" && t.name != "Conn" {
			return httpOutcomeNone
		}
		arg = 1
	case "github.com/jackc/pgx/v4/pgxpool", "github.com/jackc/pgx/v5/pgxpool":
		if t.name != "Pool" && t.name != "Conn" {
			return httpOutcomeNone
		}
		arg = 1
	default:
		return httpOutcomeNone
	}
	switch method {
	case "Exec", "Query", "QueryRow":
		if arg < len(call.Args) {
			if keyword := httpSQLPrefix(call.Args[arg], 0); keyword != "" {
				switch keyword {
				case "SELECT":
					return httpOutcomeRead
				case "INSERT", "UPDATE", "DELETE", "REPLACE":
					return httpOutcomeWrite
				}
			}
		}
	}
	return httpOutcomeNone
}

func httpSQLPrefix(expr ast.Expr, depth int) string {
	if depth >= 5 {
		return ""
	}
	if text, ok := httpRouteString(expr); ok {
		if fields := strings.Fields(text); len(fields) >= 1 {
			return strings.ToUpper(fields[0])
		}
	}
	switch expr := ast.Unparen(expr).(type) {
	case *ast.BinaryExpr:
		if expr.Op == token.ADD {
			return httpSQLPrefix(expr.X, depth+1)
		}
	case *ast.Ident:
		if expr.Obj != nil && expr.Obj.Kind == ast.Con {
			if value, ok := expr.Obj.Decl.(*ast.ValueSpec); ok && len(value.Names) == 1 && len(value.Values) == 1 {
				return httpSQLPrefix(value.Values[0], depth+1)
			}
		}
	}
	return ""
}
