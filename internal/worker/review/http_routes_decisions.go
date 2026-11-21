package review

import (
	"context"
	"go/ast"
	"go/token"
	"slices"
)

type httpRouteField struct {
	owner httpRouteName
	name  string
}

type httpRouteInjection struct {
	concrete  httpRouteName
	ambiguous bool
}

type httpRouteArgument struct {
	field   httpRouteField
	arg     int
	literal *ast.CompositeLit
}

type httpRouteDecision struct {
	call   *ast.CallExpr
	target httpRouteFunc
	guard  *ast.IfStmt
	score  int
}

func (idx *httpRouteIndex) fieldType(field httpRouteField) httpRouteName {
	def := idx.typeDecl(field.owner)
	if typ, ok := def.decl.(*ast.StructType); ok {
		for _, f := range typ.Fields.List {
			for _, id := range f.Names {
				if id.Name == field.name {
					return def.file.namedType(f.Type)
				}
			}
		}
	}
	return httpRouteName{}
}

// indexInjections links only a directly initialized interface field to the
// concrete type of a constructor argument seen in eligible source. Competing
// or unresolved arguments leave the field without a downstream navigation tip.
func (idx *httpRouteIndex) indexInjections(ctx context.Context) error {
	constructors := map[httpRouteMethod][]httpRouteArgument{}
	initializers := httpRouteInitializers{}
	for key := range idx.functions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if key.owner.name != "" {
			continue
		}
		fn := idx.function(key)
		if fn.decl == nil {
			continue
		}
		if args := idx.interfaceArguments(fn); len(args) > 0 {
			constructors[key] = args
			for _, arg := range args {
				fields := initializers[arg.field.owner]
				if fields == nil {
					fields = map[string]map[*ast.CompositeLit]bool{}
					initializers[arg.field.owner] = fields
				}
				if fields[arg.field.name] == nil {
					fields[arg.field.name] = map[*ast.CompositeLit]bool{}
				}
				fields[arg.field.name][arg.literal] = true
			}
		}
	}
	if len(constructors) == 0 {
		return nil
	}
	idx.injections = map[httpRouteField]httpRouteInjection{}
	for _, f := range idx.files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !f.eligible {
			continue
		}
		ast.Inspect(f.parsed, func(node ast.Node) bool {
			switch node.(type) {
			case *ast.AssignStmt, *ast.CompositeLit:
				idx.checkFieldWrite(f, node, initializers)
				return true
			case *ast.CallExpr:
			default:
				return true
			}
			call := node.(*ast.CallExpr)
			for _, arg := range constructors[idx.key(f, call.Fun)] {
				if arg.arg >= len(call.Args) || call.Ellipsis.IsValid() {
					continue
				}
				name := idx.exprType(f, call.Args[arg.arg], 0)
				_, concrete := idx.typeDecl(name).decl.(*ast.StructType)
				prev := idx.injections[arg.field]
				if !concrete || (prev.concrete.name != "" && prev.concrete != name) {
					prev.ambiguous = true
				}
				prev.concrete = name
				idx.injections[arg.field] = prev
			}
			return true
		})
	}
	return nil
}

type httpRouteInitializers map[httpRouteName]map[string]map[*ast.CompositeLit]bool

func (idx *httpRouteIndex) checkFieldWrite(f *httpRouteFile, node ast.Node, initializers httpRouteInitializers) {
	mark := func(field httpRouteField, literal *ast.CompositeLit) {
		if sources, tracked := initializers[field.owner][field.name]; tracked && !sources[literal] {
			prev := idx.injections[field]
			prev.ambiguous = true
			idx.injections[field] = prev
		}
	}
	switch node := node.(type) {
	case *ast.AssignStmt:
		for _, lhs := range node.Lhs {
			if selector, ok := ast.Unparen(lhs).(*ast.SelectorExpr); ok {
				mark(httpRouteField{idx.exprType(f, selector.X, 0), selector.Sel.Name}, nil)
			}
		}
	case *ast.CompositeLit:
		if node.Type == nil {
			return
		}
		owner := f.namedType(node.Type)
		if len(initializers[owner]) == 0 {
			return
		}
		for _, elt := range node.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if id := httpRouteIdent(kv.Key); id != nil {
					mark(httpRouteField{owner, id.Name}, node)
				}
			} else {
				// A positional literal can initialize any field of this type.
				for name := range initializers[owner] {
					mark(httpRouteField{owner, name}, node)
				}
				break
			}
		}
	}
}

func (idx *httpRouteIndex) interfaceArguments(fn httpRouteFunc) []httpRouteArgument {
	results := httpRouteFields(fn.decl.Type.Results)
	if len(results) == 0 {
		return nil
	}
	owner := fn.file.namedType(results[0].Type)
	if owner.pkg != fn.file.pkg || owner.name == "" {
		return nil
	}
	if _, ok := idx.typeDecl(owner).decl.(*ast.StructType); !ok {
		return nil
	}
	params := httpRouteFields(fn.decl.Type.Params)
	if len(params) == 0 {
		return nil
	}
	returned := map[*ast.CompositeLit]bool{}
	unresolvedReturn := false
	ast.Inspect(fn.decl.Body, func(n ast.Node) bool {
		if _, nested := n.(*ast.FuncLit); nested {
			return false
		}
		if r, ok := n.(*ast.ReturnStmt); ok && len(r.Results) > 0 {
			if lit := fn.file.constructedLiteral(r.Results[0]); lit != nil {
				returned[lit] = true
			} else if id := httpRouteIdent(r.Results[0]); id == nil || id.Name != "nil" || id.Obj != nil {
				unresolvedReturn = true
			}
		}
		return true
	})
	if len(returned) != 1 || unresolvedReturn {
		return nil
	}
	var out []httpRouteArgument
	for lit := range returned {
		if lit.Type == nil || fn.file.namedType(lit.Type) != owner {
			continue
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key := httpRouteIdent(kv.Key)
			if key == nil {
				continue
			}
			at := httpRouteFieldIndex(fn.decl.Type.Params, httpRouteIdent(kv.Value))
			if at < 0 {
				continue
			}
			field := httpRouteField{owner, key.Name}
			t := idx.fieldType(field)
			if t != fn.file.namedType(params[at].Type) {
				continue
			}
			if _, ok := idx.typeDecl(t).decl.(*ast.InterfaceType); ok {
				out = append(out, httpRouteArgument{field, at, lit})
			}
		}
	}
	return out
}

func (f *httpRouteFile) constructedLiteral(expr ast.Expr) *ast.CompositeLit {
	if id := httpRouteIdent(expr); id != nil && id.Obj != nil && !f.shadowed[id.Obj] {
		if a, ok := id.Obj.Decl.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && len(a.Rhs) == 1 {
			expr = a.Rhs[0]
		}
	}
	if addr, ok := ast.Unparen(expr).(*ast.UnaryExpr); ok && addr.Op == token.AND {
		expr = addr.X
	}
	lit, _ := ast.Unparen(expr).(*ast.CompositeLit)
	return lit
}

func (idx *httpRouteIndex) decisionTarget(f *httpRouteFile, call *ast.CallExpr) (httpRouteFunc, int) {
	key := idx.key(f, call.Fun)
	if target := idx.function(key); target.decl != nil {
		if key.owner.name != "" && target.file.pkg == f.pkg {
			return target, 5
		}
		return target, 1
	}
	sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if sel == nil {
		return httpRouteFunc{}, 0
	}
	field, _ := ast.Unparen(sel.X).(*ast.SelectorExpr)
	if field == nil {
		return httpRouteFunc{}, 0
	}
	injection := idx.injections[httpRouteField{idx.exprType(f, field.X, 0), field.Sel.Name}]
	if injection.ambiguous || injection.concrete.name == "" {
		return httpRouteFunc{}, 0
	}
	target := idx.function(httpRouteMethod{injection.concrete, sel.Sel.Name})
	if target.decl == nil {
		return httpRouteFunc{}, 0
	}
	def := idx.typeDecl(key.owner)
	iface, _ := def.decl.(*ast.InterfaceType)
	if iface == nil {
		return httpRouteFunc{}, 0
	}
	for _, method := range iface.Methods.List {
		if len(method.Names) == 1 && method.Names[0].Name == sel.Sel.Name {
			sig, _ := method.Type.(*ast.FuncType)
			if sig != nil && httpRouteSameSignature(sig, def.file, target.decl.Type, target.file) {
				return target, 8
			}
		}
	}
	return httpRouteFunc{}, 0
}

func (idx *httpRouteIndex) decisions(handler httpRouteHandler) []httpRouteDecision {
	var out []httpRouteDecision
	ast.PreorderStack(handler.body, nil, func(n ast.Node, parents []ast.Node) bool {
		if _, nested := n.(*ast.FuncLit); nested || len(out) >= 32 {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		target, score := idx.decisionTarget(handler.file, call)
		if target.decl == nil || handler.node == target.decl || len(parents) < 2 {
			return true
		}
		switch parents[len(parents)-1].(type) {
		case *ast.ReturnStmt, *ast.AssignStmt:
			score += 4
		case *ast.ExprStmt:
			score++
		}
		for _, p := range parents[1:] {
			if _, nested := p.(*ast.BlockStmt); nested {
				score--
				break
			}
		}
		guard := httpRoutePreviousGuard(parents)
		if guard != nil && handler.file.line(call.End())-handler.file.line(guard.Pos()) < 16 {
			if _, statement := parents[len(parents)-1].(*ast.ExprStmt); statement && handler.callsOnReceiver(call) {
				score += 8
			}
		} else {
			guard = nil
		}
		out = append(out, httpRouteDecision{call, target, guard, score})
		return true
	})
	slices.SortStableFunc(out, func(a, b httpRouteDecision) int { return b.score - a.score })
	return out
}

func (h httpRouteHandler) callsOnReceiver(call *ast.CallExpr) bool {
	if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok && h.receiver != nil {
		id := httpRouteIdent(sel.X)
		return id != nil && id.Obj == h.receiver
	}
	return false
}

func httpRoutePreviousGuard(parents []ast.Node) *ast.IfStmt {
	if len(parents) < 2 {
		return nil
	}
	block, ok := parents[len(parents)-2].(*ast.BlockStmt)
	if !ok {
		return nil
	}
	at := slices.IndexFunc(block.List, func(stmt ast.Stmt) bool { return stmt == parents[len(parents)-1] })
	if at < 1 {
		return nil
	}
	guard, _ := block.List[at-1].(*ast.IfStmt)
	if guard == nil {
		return nil
	}
	if len(httpRouteReturns(guard.Body)) == 0 {
		return nil
	}
	return guard
}

func httpRouteSameSignature(a *ast.FuncType, af *httpRouteFile, b *ast.FuncType, bf *httpRouteFile) bool {
	sameFields := func(x, y *ast.FieldList) bool {
		left, right := httpRouteFields(x), httpRouteFields(y)
		if len(left) != len(right) {
			return false
		}
		for i, field := range left {
			name := af.signatureType(field.Type, 0)
			if name == "" || name != bf.signatureType(right[i].Type, 0) {
				return false
			}
		}
		return true
	}
	return sameFields(a.Params, b.Params) && sameFields(a.Results, b.Results)
}

func (f *httpRouteFile) signatureType(expr ast.Expr, depth int) string {
	if depth >= 8 {
		return ""
	}
	prefix, child := "", (ast.Expr)(nil)
	switch x := ast.Unparen(expr).(type) {
	case *ast.Ident:
		if x.Obj == nil && slices.Contains([]string{"any", "bool", "byte", "error", "float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr"}, x.Name) {
			return x.Name
		}
	case *ast.StarExpr:
		prefix, child = "*", x.X
	case *ast.ArrayType:
		if x.Len == nil {
			prefix, child = "[]", x.Elt
		}
	case *ast.Ellipsis:
		prefix, child = "...", x.Elt
	}
	if child != nil {
		if name := f.signatureType(child, depth+1); name != "" {
			return prefix + name
		}
	}
	if name := f.namedType(expr); name.name != "" {
		return name.pkg + ":" + name.name
	}
	return ""
}
