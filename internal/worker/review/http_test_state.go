package review

import (
	"go/ast"
	"go/token"
	"path"
	"slices"
	"strings"
)

// Recognize a typed read error checked right after an asserted successful
// HTTP mutation, using the same key and a store passed to the test server.
func (x *httpTestFlow) stateAssertion(guard *ast.IfStmt, status, prior ast.Stmt) {
	if len(x.exchanges) == 0 || !x.testFailure(guard.Body) {
		return
	}
	e := &x.exchanges[len(x.exchanges)-1]
	if e.success == nil || e.success != status || e.server == nil || e.route == "" || !httpMutationMethod(e.method) {
		return
	}
	assign, _ := prior.(*ast.AssignStmt)
	if assign == nil || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return
	}
	call := httpStatementCall(assign)
	if call == nil || len(call.Args) != 1 {
		return
	}
	sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if sel == nil {
		return
	}
	f := x.file
	store := httpRouteIdent(sel.X)
	server, _ := e.server.Decl.(*ast.AssignStmt)
	if store == nil || store.Obj == nil || f.shadowed[store.Obj] || server == nil ||
		len(server.Lhs) != 1 || len(server.Rhs) != 1 {
		return
	}
	dependency, _ := store.Obj.Decl.(ast.Node)
	if dependency == nil || dependency.Pos() < x.testStart || dependency.End() >= server.Pos() {
		return
	}
	init := httpStatementCall(server)
	if init == nil || init.Ellipsis.IsValid() {
		return
	}
	ctor := httpRouteMethod{}
	switch fn := ast.Unparen(init.Fun).(type) {
	case *ast.Ident:
		if fn.Obj != nil && fn.Obj.Kind != ast.Fun {
			return
		}
		ctor = httpRouteMethod{httpRouteName{pkg: f.pkg}, fn.Name}
	case *ast.SelectorExpr:
		if pkg := f.imported(fn.X); pkg != "" {
			ctor = httpRouteMethod{httpRouteName{pkg: pkg}, fn.Sel.Name}
		}
	}
	if ctor.owner.pkg == "" || ctor.name == "" {
		return
	}
	argument := -1
	for i, a := range init.Args {
		if addr, ok := ast.Unparen(a).(*ast.UnaryExpr); ok && addr.Op == token.AND {
			a = addr.X
		}
		if id := httpRouteIdent(a); id != nil && id.Obj == store.Obj {
			if argument >= 0 {
				return
			}
			argument = i
		}
	}
	errorType := f.httpTypeGuard(guard, httpRouteIdent(assign.Lhs[1]), true)
	key, keyType := f.testLiteralPath(call.Args[0], 0), ""
	if key == "" {
		// One explicit conversion can preserve a literal path key.
		// The handler and test excerpts carry the conversion as written.
		if convert, ok := ast.Unparen(call.Args[0]).(*ast.CallExpr); ok && len(convert.Args) == 1 {
			keyType = f.signatureType(convert.Fun, 0)
			key = f.testLiteralPath(convert.Args[0], 0)
			if keyType == "" {
				return
			}
		}
	}
	if argument < 0 || errorType == "" || key == "" {
		return
	}
	e.state = httpTestState{start: f.line(dependency.Pos()), end: f.line(guard.End()), method: sel.Sel.Name,
		key: key, keyType: keyType, errorType: errorType, constructor: ctor, argument: argument}
}

func (s *roleStage) httpTestStateWindows(f *httpRouteFile, exchanges []httpTestExchange, selected, catalog []workflowRoute, out []workflowTestWitness, scores []int) {
	for _, e := range exchanges {
		if e.outcomes == 0 || e.state.method == "" {
			continue
		}
		index := slices.IndexFunc(catalog, func(r workflowRoute) bool { return httpTestRouteKey(r) == e.route })
		if index < 0 {
			continue
		}
		mutation := catalog[index]
		argument, found := mutation.state.constructors[e.state.constructor]
		if !found || argument != e.state.argument || !testRouteKeyMatches(mutation, e.target, e.state.key) {
			continue
		}
		matchingRead := func(r workflowRoute) bool {
			return r.Method == "GET" && path.Dir(r.Handler.Path) == path.Dir(f.entry.Path) && r.state.field == mutation.state.field &&
				r.state.method == e.state.method && r.state.errorType == e.state.errorType && r.state.keyType == mutation.state.keyType &&
				(e.state.keyType == "" || e.state.keyType == r.state.keyType)
		}
		if !slices.ContainsFunc(catalog, matchingRead) {
			continue
		}
		for i, route := range selected {
			var related httpRelatedTest
			switch {
			case httpTestRouteKey(route) == e.route:
			case matchingRead(route):
				related = httpRelatedTest{method: mutation.Method, pattern: mutation.Path, registration: mutation.Registration, handler: mutation.Handler}
			default:
				continue
			}
			// Prefer a full HTTP sequence when both responses are asserted.
			s.storeHTTPTestWindow(f, e.state.start, e.state.end, 7, related, &out[i], &scores[i])
		}
	}
}

func testRouteKeyMatches(route workflowRoute, target, key string) bool {
	capture := "{" + route.state.pathKey + "}"
	for pattern := range strings.SplitSeq(route.Path, "/") {
		part, next, _ := strings.Cut(target, "/")
		target = next
		if prefix, suffix, found := strings.Cut(pattern, capture); found && strings.HasPrefix(part, prefix) && strings.HasSuffix(part, suffix) &&
			len(part) >= len(prefix)+len(suffix) {
			return part[len(prefix):len(part)-len(suffix)] == key
		}
	}
	return false
}
