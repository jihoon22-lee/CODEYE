package review

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

const gorillaMuxPackage = "github.com/gorilla/mux"

type httpRouteRegistration struct {
	file                         *httpRouteFile
	call                         *ast.CallExpr
	method, pattern, patternKind string
	handler                      ast.Expr
}

type httpRouteHandler struct {
	file     *httpRouteFile
	node     ast.Node
	body     *ast.BlockStmt
	name     string
	receiver *ast.Object
}

func (idx *httpRouteIndex) registrations(f *httpRouteFile, out []httpRouteRegistration) []httpRouteRegistration {
	ast.PreorderStack(f.parsed, nil, func(n ast.Node, parents []ast.Node) bool {
		if len(out) >= httpRouteMax {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 1 {
			return true
		}
		r := idx.muxRegistration(f, call, parents)
		if r.handler == nil && len(call.Args) >= 3 {
			r = idx.tableRegistration(f, call)
		}
		if r.handler != nil && httpRouteValidPattern(r.pattern) && idx.handler(f, r.handler).node != nil {
			out = append(out, r)
		}
		return true
	})
	return out
}

func (idx *httpRouteIndex) muxRegistration(f *httpRouteFile, call *ast.CallExpr, parents []ast.Node) httpRouteRegistration {
	sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if sel == nil {
		return httpRouteRegistration{}
	}
	base, method := call, ""
	if sel.Sel.Name == "Methods" && len(call.Args) == 1 {
		method = f.method(call.Args[0])
		base, _ = ast.Unparen(sel.X).(*ast.CallExpr)
		if base == nil || method == "" {
			return httpRouteRegistration{}
		}
		sel, _ = ast.Unparen(base.Fun).(*ast.SelectorExpr)
	}
	if sel == nil {
		return httpRouteRegistration{}
	}
	var receiver, patternArg, handler ast.Expr
	patternKind := ""
	switch {
	case (sel.Sel.Name == "HandleFunc" || sel.Sel.Name == "Handle") && len(base.Args) == 2:
		receiver, patternArg, handler = sel.X, base.Args[0], base.Args[1]
	case (sel.Sel.Name == "HandlerFunc" || sel.Sel.Name == "Handler") && len(base.Args) == 1:
		prefix, _ := ast.Unparen(sel.X).(*ast.CallExpr)
		if prefix == nil || len(prefix.Args) != 1 {
			return httpRouteRegistration{}
		}
		selector, _ := ast.Unparen(prefix.Fun).(*ast.SelectorExpr)
		if selector == nil || selector.Sel.Name != "PathPrefix" {
			return httpRouteRegistration{}
		}
		receiver, patternArg, handler = selector.X, prefix.Args[0], base.Args[0]
		patternKind = "prefix"
	default:
		return httpRouteRegistration{}
	}
	pattern, ok := httpRouteString(patternArg)
	if !ok {
		return httpRouteRegistration{}
	}
	kind, prefix := idx.router(f, receiver, 0)
	switch kind {
	case "stdlib":
		if method != "" || patternKind != "" {
			return httpRouteRegistration{}
		}
		if at := strings.IndexAny(pattern, " \t"); at >= 0 {
			method, pattern = pattern[:at], strings.TrimLeft(pattern[at:], " \t")
			if !httpRouteMethodName(method) {
				return httpRouteRegistration{}
			}
		}
		if strings.HasSuffix(pattern, "/") {
			patternKind = "prefix"
		}
	case "gorilla":
		// An outer modifier can further constrain the same route. The
		// matched registration must be the outermost expression in the chain.
		for _, parent := range slices.Backward(parents) {
			if _, paren := parent.(*ast.ParenExpr); paren {
				continue
			}
			if selector, ok := parent.(*ast.SelectorExpr); ok && ast.Unparen(selector.X) == call {
				return httpRouteRegistration{}
			}
			break
		}
		pattern = prefix + pattern
	default:
		return httpRouteRegistration{}
	}
	if !strings.HasPrefix(pattern, "/") {
		return httpRouteRegistration{}
	}
	return httpRouteRegistration{file: f, call: call, method: method, pattern: pattern, patternKind: patternKind, handler: handler}
}

// router follows an imported router type or constructor, plus a gorilla/mux
// PathPrefix→Subrouter chain held in a single, unreassigned local binding.
func (idx *httpRouteIndex) router(f *httpRouteFile, expr ast.Expr, depth int) (kind, prefix string) {
	if depth >= 5 {
		return "", ""
	}
	if f.imported(expr) == "net/http" {
		return "stdlib", ""
	}
	if id := httpRouteIdent(expr); id != nil && id.Obj != nil && !f.shadowed[id.Obj] {
		if a, ok := id.Obj.Decl.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && len(a.Rhs) == 1 {
			return idx.router(f, a.Rhs[0], depth+1)
		}
	}
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok {
		sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if sel != nil && len(call.Args) == 0 {
			pkg := f.imported(sel.X)
			switch {
			case pkg == "net/http" && sel.Sel.Name == "NewServeMux":
				return "stdlib", ""
			case pkg == gorillaMuxPackage && sel.Sel.Name == "NewRouter":
				return "gorilla", ""
			case sel.Sel.Name == "Subrouter":
				return idx.subrouter(f, sel.X, depth+1)
			}
		}
	}
	switch idx.exprType(f, expr, depth) {
	case httpRouteName{"net/http", "ServeMux"}:
		return "stdlib", ""
	case httpRouteName{gorillaMuxPackage, "Router"}:
		return "gorilla", ""
	}
	return "", ""
}

func (idx *httpRouteIndex) subrouter(f *httpRouteFile, expr ast.Expr, depth int) (string, string) {
	call, _ := ast.Unparen(expr).(*ast.CallExpr)
	if call == nil || len(call.Args) != 1 {
		return "", ""
	}
	sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if sel == nil || sel.Sel.Name != "PathPrefix" {
		return "", ""
	}
	prefix, ok := httpRouteString(call.Args[0])
	if !ok || !strings.HasPrefix(prefix, "/") || !httpRouteValidPattern(prefix) {
		return "", ""
	}
	kind, parent := idx.router(f, sel.X, depth)
	if kind != "gorilla" {
		return "", ""
	}
	return kind, strings.TrimRight(parent+prefix, "/")
}

func (idx *httpRouteIndex) exprType(f *httpRouteFile, expr ast.Expr, depth int) httpRouteName {
	if depth >= 6 {
		return httpRouteName{}
	}
	switch x := ast.Unparen(expr).(type) {
	case *ast.Ident:
		if x.Obj != nil {
			switch d := x.Obj.Decl.(type) {
			case *ast.Field:
				return f.namedType(d.Type)
			case *ast.ValueSpec:
				if d.Type != nil {
					return f.namedType(d.Type)
				}
				if len(d.Names) == 1 && len(d.Values) == 1 && !f.shadowed[x.Obj] {
					return idx.exprType(f, d.Values[0], depth+1)
				}
			case *ast.AssignStmt:
				if len(d.Lhs) == 1 && len(d.Rhs) == 1 && !f.shadowed[x.Obj] {
					return idx.exprType(f, d.Rhs[0], depth+1)
				}
			}
		}
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return idx.exprType(f, x.X, depth+1)
		}
	case *ast.CompositeLit:
		if x.Type != nil {
			return f.namedType(x.Type)
		}
	case *ast.SelectorExpr:
		owner := idx.exprType(f, x.X, depth+1)
		return idx.fieldType(httpRouteField{owner, x.Sel.Name})
	case *ast.CallExpr:
		// A direct package function with a single declared result is enough
		// to identify the static type of an inferred local binding.
		var key httpRouteMethod
		switch call := ast.Unparen(x.Fun).(type) {
		case *ast.Ident:
			key = idx.key(f, call)
		case *ast.SelectorExpr:
			if pkg := f.imported(call.X); pkg != "" {
				key = httpRouteMethod{httpRouteName{pkg: pkg}, call.Sel.Name}
			}
		}
		if fn := idx.function(key); fn.decl != nil {
			if results := httpRouteFields(fn.decl.Type.Results); len(results) == 1 {
				return fn.file.namedType(results[0].Type)
			}
		}
	}
	return httpRouteName{}
}

func (idx *httpRouteIndex) key(f *httpRouteFile, expr ast.Expr) httpRouteMethod {
	switch x := ast.Unparen(expr).(type) {
	case *ast.Ident:
		if x.Obj == nil || x.Obj.Kind == ast.Fun {
			return httpRouteMethod{httpRouteName{pkg: f.pkg}, x.Name}
		}
	case *ast.SelectorExpr:
		if pkg := f.imported(x.X); pkg != "" {
			return httpRouteMethod{httpRouteName{pkg: pkg}, x.Sel.Name}
		}
		owner := idx.exprType(f, x.X, 0)
		if owner.name == "" {
			owner = f.namedType(x.X) // a method expression, such as (*Server).Serve
			if idx.typeDecl(owner).decl == nil {
				return httpRouteMethod{}
			}
		}
		return httpRouteMethod{owner, x.Sel.Name}
	}
	return httpRouteMethod{}
}

func (idx *httpRouteIndex) handler(f *httpRouteFile, expr ast.Expr) httpRouteHandler {
	switch x := ast.Unparen(expr).(type) {
	case *ast.FuncLit:
		return httpRouteHandler{file: f, node: x, body: x.Body}
	case *ast.CallExpr:
		if sel, ok := ast.Unparen(x.Fun).(*ast.SelectorExpr); ok && f.imported(sel.X) == "net/http" && sel.Sel.Name == "HandlerFunc" && len(x.Args) == 1 {
			if fn, ok := ast.Unparen(x.Args[0]).(*ast.FuncLit); ok {
				return httpRouteHandler{file: f, node: fn, body: fn.Body}
			}
			switch arg := ast.Unparen(x.Args[0]).(type) {
			case *ast.Ident, *ast.SelectorExpr:
				return idx.handler(f, arg)
			}
			return httpRouteHandler{}
		}
		factory := idx.function(idx.key(f, x.Fun))
		if factory.decl == nil {
			return httpRouteHandler{}
		}
		results := httpRouteFields(factory.decl.Type.Results)
		if len(results) != 1 || factory.file.namedType(results[0].Type) != (httpRouteName{"net/http", "HandlerFunc"}) {
			return httpRouteHandler{}
		}
		if result := httpRouteReturns(factory.decl.Body); len(result) == 1 && len(result[0].Results) == 1 {
			if fn, ok := ast.Unparen(result[0].Results[0]).(*ast.FuncLit); ok {
				return httpRouteHandler{factory.file, fn, fn.Body, factory.decl.Name.Name, httpRouteReceiver(factory.decl)}
			}
		}
	case *ast.Ident, *ast.SelectorExpr:
		fn := idx.function(idx.key(f, x))
		if fn.decl != nil {
			return httpRouteHandler{fn.file, fn.decl, fn.decl.Body, fn.decl.Name.Name, httpRouteReceiver(fn.decl)}
		}
	}
	return httpRouteHandler{}
}

func httpRouteReceiver(fn *ast.FuncDecl) *ast.Object {
	if fn.Recv != nil && len(fn.Recv.List) == 1 && len(fn.Recv.List[0].Names) == 1 {
		return fn.Recv.List[0].Names[0].Obj
	}
	return nil
}

func httpRouteReturns(body *ast.BlockStmt) []*ast.ReturnStmt {
	var result []*ast.ReturnStmt
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			result = append(result, n)
			return false
		}
		return len(result) < 2
	})
	return result
}

func (idx *httpRouteIndex) tableRegistration(f *httpRouteFile, call *ast.CallExpr) httpRouteRegistration {
	if !slices.ContainsFunc(call.Args, func(arg ast.Expr) bool { return f.method(arg) != "" }) {
		return httpRouteRegistration{}
	}
	var signature *ast.FuncType
	var body *ast.BlockStmt
	definition := f
	if id := httpRouteIdent(call.Fun); id != nil && id.Obj != nil && !f.shadowed[id.Obj] {
		if a, ok := id.Obj.Decl.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && len(a.Rhs) == 1 {
			if fn, ok := ast.Unparen(a.Rhs[0]).(*ast.FuncLit); ok {
				signature, body = fn.Type, fn.Body
			}
		}
	}
	if body == nil {
		fn := idx.function(idx.key(f, call.Fun))
		if fn.decl != nil {
			signature, body, definition = fn.decl.Type, fn.decl.Body, fn.file
		}
	}
	if body == nil || len(body.List) != 1 {
		return httpRouteRegistration{}
	}
	ret, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return httpRouteRegistration{}
	}
	table, _ := ast.Unparen(ret.Results[0]).(*ast.CompositeLit)
	if table == nil || table.Type == nil {
		return httpRouteRegistration{}
	}
	params := httpRouteFields(signature.Params)
	if len(params) != len(call.Args) || len(params) < 3 || !idx.routeRecord(definition.namedType(table.Type)) {
		return httpRouteRegistration{}
	}
	var method, pattern string
	var handler ast.Expr
	for _, elt := range table.Elts {
		p, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name := httpRouteIdent(p.Key)
		if name == nil {
			continue
		}
		switch strings.ToLower(name.Name) {
		case "method":
			if at := httpRouteFieldIndex(signature.Params, httpRouteIdent(p.Value)); at >= 0 {
				method = f.method(call.Args[at])
			}
		case "path", "pattern", "parts":
			if at := httpRouteFieldIndex(signature.Params, definition.routePatternBinding(p.Value, 0)); at >= 0 {
				pattern, _ = httpRouteString(call.Args[at])
			}
		case "handler", "handle":
			if at := httpRouteFieldIndex(signature.Params, httpRouteIdent(p.Value)); at >= 0 {
				handler = call.Args[at]
			}
		}
	}
	if method == "" || pattern == "" || handler == nil {
		return httpRouteRegistration{}
	}
	return httpRouteRegistration{file: f, call: call, method: method, pattern: pattern, handler: handler}
}

func (idx *httpRouteIndex) routeRecord(t httpRouteName) bool {
	typ, _ := idx.typeDecl(t).decl.(*ast.StructType)
	if typ == nil {
		return false
	}
	method, pattern, handler := false, false, false
	for _, field := range typ.Fields.List {
		for _, id := range field.Names {
			switch strings.ToLower(id.Name) {
			case "method":
				method = isRouteStringType(field.Type)
			case "path", "pattern":
				pattern = pattern || isRouteStringType(field.Type)
			case "parts":
				list, ok := field.Type.(*ast.ArrayType)
				pattern = pattern || (ok && list.Len == nil && isRouteStringType(list.Elt))
			case "handle", "handler":
				_, handler = field.Type.(*ast.FuncType)
			}
		}
	}
	return method && pattern && handler
}

func isRouteStringType(expr ast.Expr) bool {
	id := httpRouteIdent(expr)
	return id != nil && id.Name == "string" && id.Obj == nil
}

func (f *httpRouteFile) routePatternBinding(expr ast.Expr, depth int) *ast.Ident {
	if depth >= 3 {
		return nil
	}
	if id := httpRouteIdent(expr); id != nil {
		return id
	}
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && len(call.Args) == 2 {
		sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		literal, _ := httpRouteString(call.Args[1])
		if sel != nil && f.imported(sel.X) == "strings" && literal == "/" &&
			(sel.Sel.Name == "Split" || sel.Sel.Name == "TrimPrefix") {
			return f.routePatternBinding(call.Args[0], depth+1)
		}
	}
	return nil
}
