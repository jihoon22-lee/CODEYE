package review

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// A state check is a source hint connecting a handler's path-keyed call on
// an injected store to an error type checked by the handler after a read.
type httpRouteState struct {
	field        httpRouteField
	constructors map[httpRouteMethod]int
	pathKey      string
	keyType      string
	method       string
	errorType    string
	packageName  string
}

type httpRelatedTest struct {
	method, pattern       string
	registration, handler record.Excerpt
}

func (r httpRelatedTest) inScope(scope string) bool {
	return r.method == "" || record.ScopeUnder(r.registration.Path, scope) && record.ScopeUnder(r.handler.Path, scope)
}

func (idx *httpRouteIndex) httpRouteState(r httpRouteRegistration, handler httpRouteHandler, end int) httpRouteState {
	f := handler.file
	signature := httpHandlerSignature(handler.node)
	req := f.httpHandlerParam(signature, "Request")
	if handler.receiver == nil || req == nil || r.method != "GET" && !httpMutationMethod(r.method) {
		return httpRouteState{}
	}
	owner := idx.exprType(f, &ast.Ident{Name: handler.receiver.Name, Obj: handler.receiver}, 0)
	for i, stmt := range handler.body.List {
		if f.line(stmt.End()) > end {
			break
		}
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}
		call := httpStatementCall(assign)
		if call == nil || len(call.Args) != 1 {
			continue
		}
		sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if sel == nil {
			continue
		}
		key := idx.httpPathKey(f, call.Args[0], req, 0)
		if key == "" || !strings.Contains(r.pattern, "{"+key+"}") {
			continue
		}
		field := idx.httpStateField(f, sel.X, handler.receiver, owner)
		if field.name == "" {
			continue
		}
		keyType := idx.httpStoreKeyType(field, sel.Sel.Name)
		if keyType == "" {
			continue
		}
		var errorType string
		if r.method == "GET" {
			if i+1 == len(handler.body.List) || len(assign.Lhs) != 2 {
				continue
			}
			guard, _ := handler.body.List[i+1].(*ast.IfStmt)
			if guard == nil || f.line(guard.Body.End()) > end || !f.httpErrorResponse(guard.Body, signature) {
				continue
			}
			errorType = f.httpTypeGuard(guard, httpRouteIdent(assign.Lhs[1]), false)
			if errorType == "" {
				continue
			}
		} else if len(assign.Lhs) != 1 {
			continue
		}
		constructors := map[httpRouteMethod]int{}
		for ctor, args := range idx.constructors {
			for _, arg := range args {
				if arg.field == field {
					constructors[ctor] = arg.arg
				}
			}
		}
		if len(constructors) > 0 {
			return httpRouteState{field: field, constructors: constructors, pathKey: key, keyType: keyType, method: sel.Sel.Name,
				errorType: errorType, packageName: f.parsed.Name.Name}
		}
	}
	return httpRouteState{}
}

func (idx *httpRouteIndex) httpStoreKeyType(field httpRouteField, method string) string {
	typ := idx.typeDecl(idx.fieldType(field))
	if iface, ok := typ.decl.(*ast.InterfaceType); ok {
		for _, f := range iface.Methods.List {
			if len(f.Names) == 1 && f.Names[0].Name == method {
				if sig, ok := f.Type.(*ast.FuncType); ok && len(sig.Params.List) == 1 {
					arg := sig.Params.List[0]
					if len(arg.Names) <= 1 && idx.httpStringType(typ.file, arg.Type) {
						return typ.file.signatureType(arg.Type, 0)
					}
				}
				break
			}
		}
	}
	return ""
}

func httpHandlerSignature(handler ast.Node) *ast.FuncType {
	switch fn := handler.(type) {
	case *ast.FuncDecl:
		return fn.Type
	case *ast.FuncLit:
		return fn.Type
	}
	return nil
}

func (f *httpRouteFile) httpHandlerParam(syntax *ast.FuncType, name string) *ast.Object {
	if syntax == nil {
		return nil
	}
	for _, field := range httpRouteFields(syntax.Params) {
		if len(field.Names) == 1 && f.namedType(field.Type) == (httpRouteName{"net/http", name}) && !f.shadowed[field.Names[0].Obj] {
			return field.Names[0].Obj
		}
	}
	return nil
}

func (f *httpRouteFile) httpErrorResponse(block *ast.BlockStmt, signature *ast.FuncType) bool {
	writer := f.responseWriter(signature.Params)
	if writer == nil || len(block.List) < 2 {
		return false
	}
	ret, _ := block.List[len(block.List)-1].(*ast.ReturnStmt)
	if ret == nil || len(ret.Results) != 0 {
		return false
	}
	for _, stmt := range block.List[:len(block.List)-1] {
		if call := httpStatementCall(stmt); call != nil && len(call.Args) == 3 && f.testPackageCall(call, "net/http") == "Error" {
			if w := httpRouteIdent(call.Args[0]); w != nil && w.Obj == writer && f.testExpectedStatus(call.Args[2]) {
				return true
			}
		}
	}
	return false
}

// Follow the request parameter through local bindings and conversions that
// preserve its literal value on the successful path.
func (idx *httpRouteIndex) httpPathKey(f *httpRouteFile, expr ast.Expr, req *ast.Object, depth int) string {
	if depth >= 4 {
		return ""
	}
	if id := httpRouteIdent(expr); id != nil && id.Obj != nil && !f.shadowed[id.Obj] {
		if assign, ok := id.Obj.Decl.(*ast.AssignStmt); ok && len(assign.Rhs) == 1 && len(assign.Lhs) > 0 {
			if name := httpRouteIdent(assign.Lhs[0]); name != nil && name.Obj == id.Obj {
				return idx.httpPathKey(f, assign.Rhs[0], req, depth+1)
			}
		}
	}
	switch expr := ast.Unparen(expr).(type) {
	case *ast.CallExpr:
		if len(expr.Args) == 1 {
			if testReceiver(expr, "PathValue", 1) == req {
				key, _ := httpRouteString(expr.Args[0])
				return key
			}
			if idx.httpStringType(f, expr.Fun) || idx.httpPathKeyValidator(f, expr.Fun) {
				return idx.httpPathKey(f, expr.Args[0], req, depth+1)
			}
		}
	case *ast.IndexExpr:
		if call, ok := ast.Unparen(expr.X).(*ast.CallExpr); ok && f.testPackageCall(call, gorillaMuxPackage) == "Vars" && len(call.Args) == 1 {
			if id := httpRouteIdent(call.Args[0]); id != nil && id.Obj == req {
				key, _ := httpRouteString(expr.Index)
				return key
			}
		}
	}
	return ""
}

func (idx *httpRouteIndex) httpStringType(f *httpRouteFile, expr ast.Expr) bool {
	if id := httpRouteIdent(expr); id != nil && id.Name == "string" && id.Obj == nil && idx.bindings[httpRouteName{f.pkg, "string"}] == 0 {
		return true
	}
	typ := idx.typeDecl(f.namedType(expr))
	if id := httpRouteIdent(typ.decl); id != nil {
		return id.Name == "string" && id.Obj == nil && idx.bindings[httpRouteName{typ.file.pkg, "string"}] == 0
	}
	return false
}

// Permit a local validator with a final identity return and explicit error
// returns. A helper that rewrites the path key has no direct test association.
func (idx *httpRouteIndex) httpPathKeyValidator(f *httpRouteFile, expr ast.Expr) bool {
	key := idx.key(f, expr)
	if key.owner != (httpRouteName{pkg: f.pkg}) {
		return false
	}
	fn := idx.function(key)
	if fn.decl == nil || len(fn.decl.Body.List) == 0 {
		return false
	}
	params, results := httpRouteFields(fn.decl.Type.Params), httpRouteFields(fn.decl.Type.Results)
	if len(params) != 1 || len(params[0].Names) != 1 || len(results) != 2 || fn.file.signatureType(results[1].Type, 0) != "error" {
		return false
	}
	arg := params[0].Names[0].Obj
	final, _ := fn.decl.Body.List[len(fn.decl.Body.List)-1].(*ast.ReturnStmt)
	if final == nil || len(final.Results) != 2 || !httpRouteNil(final.Results[1]) || fn.file.shadowed[arg] {
		return false
	}
	value := final.Results[0]
	if cast, ok := ast.Unparen(value).(*ast.CallExpr); ok && len(cast.Args) == 1 && idx.httpStringType(fn.file, cast.Fun) {
		value = cast.Args[0]
	}
	if id := httpRouteIdent(value); id == nil || id.Obj != arg {
		return false
	}
	badReturn := false
	ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
		switch rt := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if rt != final {
				if len(rt.Results) != 2 {
					badReturn = true
					break
				}
				call, _ := ast.Unparen(rt.Results[1]).(*ast.CallExpr)
				badReturn = badReturn || call == nil || len(call.Args) == 0 ||
					fn.file.testPackageCall(call, "fmt") != "Errorf" && fn.file.testPackageCall(call, "errors") != "New"
			}
		}
		return !badReturn
	})
	return !badReturn
}

func httpRouteNil(expr ast.Expr) bool {
	id := httpRouteIdent(expr)
	return id != nil && id.Name == "nil" && id.Obj == nil
}

// An accessor with a simple field return can link a handler to a constructor
// dependency. A build-tagged variant can supply this source path. That does
// not establish which variant is active or that all variants return the field.
func (idx *httpRouteIndex) httpStateField(f *httpRouteFile, expr ast.Expr, receiver *ast.Object, owner httpRouteName) httpRouteField {
	fieldName := func(expr ast.Expr, obj *ast.Object) string {
		if sel, ok := ast.Unparen(expr).(*ast.SelectorExpr); ok {
			if id := httpRouteIdent(sel.X); id != nil && id.Obj == obj && obj != nil {
				return sel.Sel.Name
			}
		}
		return ""
	}
	if field := fieldName(expr, receiver); field != "" {
		return httpRouteField{owner, field}
	}
	call, _ := ast.Unparen(expr).(*ast.CallExpr)
	if call == nil {
		return httpRouteField{}
	}
	sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if sel == nil || fieldName(sel, receiver) == "" {
		return httpRouteField{}
	}
	var field string
	for _, fn := range idx.functions[httpRouteMethod{owner, sel.Sel.Name}] {
		if fn.decl.Body == nil || len(fn.decl.Body.List) != 1 || len(httpRouteFields(fn.decl.Type.Params)) != len(call.Args) {
			continue
		}
		ret, _ := fn.decl.Body.List[0].(*ast.ReturnStmt)
		if ret == nil || len(ret.Results) != 1 {
			continue
		}
		if name := fieldName(ret.Results[0], httpRouteReceiver(fn.decl)); name != "" {
			if field != "" && name != field {
				return httpRouteField{}
			}
			field = name
		}
	}
	if field == "" {
		return httpRouteField{}
	}
	return httpRouteField{owner, field}
}

func (f *httpRouteFile) httpTypeGuard(guard *ast.IfStmt, actual *ast.Ident, negated bool) string {
	if actual == nil || actual.Obj == nil {
		return ""
	}
	assign, _ := guard.Init.(*ast.AssignStmt)
	if assign == nil || assign.Tok != token.DEFINE || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return ""
	}
	cond := ast.Unparen(guard.Cond)
	if negated {
		unary, _ := cond.(*ast.UnaryExpr)
		if unary == nil || unary.Op != token.NOT {
			return ""
		}
		cond = ast.Unparen(unary.X)
	}
	name, ok := httpRouteIdent(cond), httpRouteIdent(assign.Lhs[1])
	if name == nil || ok == nil || name.Obj != ok.Obj || ok.Obj == nil {
		return ""
	}
	assert, _ := ast.Unparen(assign.Rhs[0]).(*ast.TypeAssertExpr)
	if assert == nil || assert.Type == nil {
		return ""
	}
	if id := httpRouteIdent(assert.X); id == nil || id.Obj != actual.Obj {
		return ""
	}
	return f.signatureType(assert.Type, 0)
}

func httpMutationMethod(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}
