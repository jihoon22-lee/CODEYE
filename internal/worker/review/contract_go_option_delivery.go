package review

import (
	"go/ast"
	"go/token"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func goOptionCondition(e ast.Expr, option *ast.Object) bool {
	cond := ast.Unparen(e)
	if n, ok := cond.(*ast.UnaryExpr); ok && n.Op == token.NOT {
		cond = n.X
	}
	return goOptionRef(cond, option)
}

// Track the projection's original local binding to a direct return, or
// to bytes produced under the option guard and passed to ResponseWriter.
// A competing write to either value makes the delivery ambiguous.
func (s *roleStage) goOptionDelivery(idx *httpRouteIndex, f *httpRouteFile, fn *ast.FuncDecl,
	assign *ast.AssignStmt, option *ast.Object, handoff record.Excerpt) record.Excerpt {
	if assign.Tok != token.DEFINE || len(assign.Lhs) != 1 {
		return record.Excerpt{}
	}
	projected := httpRouteIdent(assign.Lhs[0])
	if projected == nil || projected.Obj == nil || projected.Obj.Decl != assign ||
		!goOptionWrites(fn.Body, projected.Obj, map[*ast.AssignStmt]bool{assign: true}) ||
		goOptionHasAlias(fn.Body, projected.Obj) {
		return record.Excerpt{}
	}
	body := fn.Body.List
	last := body[len(body)-1]
	if last == assign {
		return record.Excerpt{}
	}
	start := f.line(last.Pos())
	var sent *ast.Ident
	if ret, ok := last.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
		if goOptionRef(ret.Results[0], projected.Obj) {
			return s.goOptionDeliveryWindow(f, handoff, start, f.line(last.End()))
		}
		sent = httpRouteIdent(ret.Results[0])
	} else {
		sent = idx.goOptionWrittenValue(f, last)
	}
	if sent == nil || sent.Obj == nil || goOptionHasAlias(fn.Body, sent.Obj) {
		return record.Excerpt{}
	}
	original, ok := sent.Obj.Decl.(*ast.AssignStmt)
	if !ok || original.Tok != token.DEFINE {
		return record.Excerpt{}
	}
	allowed := map[*ast.AssignStmt]bool{original: true}
	var guarded *ast.IfStmt
	for _, stmt := range body {
		if stmt.Pos() <= assign.Pos() || stmt == last {
			continue
		}
		isGuard := false
		if check, ok := stmt.(*ast.IfStmt); ok && check.Init == nil && check.Else == nil {
			isGuard = goOptionCondition(check.Cond, option)
		}
		ast.Inspect(stmt, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
				return false
			case *ast.AssignStmt:
				if idx.goOptionSerializes(f, x, projected.Obj, sent.Obj) {
					allowed[x] = true
					if isGuard {
						guarded, _ = stmt.(*ast.IfStmt)
					}
				}
			}
			return true
		})
	}
	if guarded == nil || !goOptionWrites(fn.Body, sent.Obj, allowed) {
		return record.Excerpt{}
	}
	// The handoff must include the guarded serializer. The last switch
	// can contain alternative serializers using the same projection.
	if f.line(guarded.End()) > handoff.End {
		return record.Excerpt{}
	}
	for _, stmt := range body {
		if x, ok := stmt.(*ast.SwitchStmt); ok && x.Pos() > guarded.End() && f.line(x.Pos()) > handoff.End {
			start = f.line(x.Pos())
		}
	}
	return s.goOptionDeliveryWindow(f, handoff, start, f.line(last.End()))
}

func goOptionHasAlias(body *ast.BlockStmt, value *ast.Object) bool {
	alias := false
	ast.Inspect(body, func(n ast.Node) bool {
		if !alias {
			var values []ast.Expr
			switch x := n.(type) {
			case *ast.AssignStmt:
				values = x.Rhs
			case *ast.ValueSpec:
				values = x.Values
			}
			alias = slices.ContainsFunc(values, func(e ast.Expr) bool {
				if x, ok := ast.Unparen(e).(*ast.SliceExpr); ok {
					e = x.X
				}
				return goOptionRef(e, value)
			})
		}
		return !alias
	})
	return alias
}

func (s *roleStage) goOptionDeliveryWindow(f *httpRouteFile, handoff record.Excerpt, from, to int) record.Excerpt {
	if handoff.Start <= from && to <= handoff.End {
		return handoff
	}
	if from <= handoff.End {
		return record.Excerpt{}
	}
	return s.goWrapperExcerpt(f.contractGoFile, from, to, witnessMaxLines, witnessMaxExcerpt)
}

func (idx *httpRouteIndex) goOptionWrittenValue(f *httpRouteFile, stmt ast.Stmt) *ast.Ident {
	call := goOptionHandoff(stmt)
	if expr, ok := stmt.(*ast.ExprStmt); ok {
		call, _ = ast.Unparen(expr.X).(*ast.CallExpr)
	}
	if call == nil || len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return nil
	}
	fun, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || fun.Sel.Name != "Write" || idx.exprType(f, fun.X, 0) != (httpRouteName{"net/http", "ResponseWriter"}) {
		return nil
	}
	return httpRouteIdent(call.Args[0])
}

func (idx *httpRouteIndex) goOptionSerializes(f *httpRouteFile, assign *ast.AssignStmt, projected, bytes *ast.Object) bool {
	if len(assign.Lhs) == 0 || !goOptionRef(assign.Lhs[0], bytes) {
		return false
	}
	call := goOptionHandoff(assign)
	if call == nil || call.Ellipsis.IsValid() || !slices.ContainsFunc(call.Args, func(e ast.Expr) bool { return goOptionRef(e, projected) }) {
		return false
	}
	key := idx.key(f, call.Fun)
	if key.owner == (httpRouteName{pkg: "encoding/json"}) && key.name == "Marshal" {
		return len(call.Args) == 1
	}
	next := idx.function(key)
	if next.decl == nil {
		return false
	}
	results := httpRouteFields(next.decl.Type.Results)
	return len(results) == len(assign.Lhs) && next.file.signatureType(results[0].Type, 0) == "[]byte"
}
