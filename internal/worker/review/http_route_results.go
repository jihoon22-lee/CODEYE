package review

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Keep a complete helper when it fits. For a long helper, prefer a single
// response in the outer block and keep preceding whole statements that fit.
func (s *roleStage) httpRouteDecisionWindow(target httpRouteFunc) record.Excerpt {
	f, decl := target.file, target.decl
	prefix := s.httpRouteExcerpt(f, decl, record.MaxContractLocationLines, roleMaxWindowBytes, false)
	if prefix.End == f.line(decl.End()) {
		return prefix
	}
	writer := f.responseWriter(decl.Type.Params)
	if writer == nil {
		return prefix
	}
	result, status := -1, -1
	statusCount := 0
	for i, stmt := range decl.Body.List {
		switch f.httpResponseCall(httpStatementCall(stmt), writer) {
		case responseContent:
			if result >= 0 {
				return prefix
			}
			result = i
		case responseStatus:
			status, statusCount = i, statusCount+1
		}
	}
	if result < 0 && statusCount == 1 {
		result = status
	}
	if result < 0 {
		return prefix
	}
	stmts := decl.Body.List
	end := f.line(stmts[result].End())
	if prefix.End >= end {
		return prefix
	}
	window := prefix
	for i := result; i >= 0; i-- {
		start := f.line(stmts[i].Pos())
		x := s.httpRouteExcerptLines(f, start, end, record.MaxContractLocationLines, roleMaxWindowBytes, true)
		if x.ID == "" {
			break
		}
		window = x
	}
	return window
}

func (f *httpRouteFile) responseWriter(fields *ast.FieldList) *ast.Object {
	var writer *ast.Object
	for _, field := range httpRouteFields(fields) {
		typ, ok := ast.Unparen(field.Type).(*ast.SelectorExpr)
		if !ok || f.imported(typ.X) != "net/http" || typ.Sel.Name != "ResponseWriter" {
			continue
		}
		if len(field.Names) != 1 || writer != nil || f.shadowed[field.Names[0].Obj] {
			return nil
		}
		writer = field.Names[0].Obj
	}
	return writer
}

func httpStatementCall(stmt ast.Stmt) *ast.CallExpr {
	var expr ast.Expr
	switch stmt := stmt.(type) {
	case *ast.ExprStmt:
		expr = stmt.X
	case *ast.AssignStmt:
		if len(stmt.Rhs) == 1 {
			expr = stmt.Rhs[0]
		}
	case *ast.ReturnStmt:
		if len(stmt.Results) == 1 {
			expr = stmt.Results[0]
		}
	case *ast.IfStmt:
		return httpStatementCall(stmt.Init)
	}
	if expr == nil {
		return nil
	}
	call, _ := ast.Unparen(expr).(*ast.CallExpr)
	return call
}

type httpResponseCall uint8

const (
	noHTTPResponse httpResponseCall = iota
	responseStatus
	responseContent
)

func (f *httpRouteFile) httpResponseCall(call *ast.CallExpr, writer *ast.Object) httpResponseCall {
	if call == nil || len(call.Args) == 0 || call.Ellipsis.IsValid() {
		return noHTTPResponse
	}
	sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if sel == nil {
		return noHTTPResponse
	}
	isWriter := func(expr ast.Expr) bool {
		id := httpRouteIdent(expr)
		return id != nil && id.Obj == writer
	}
	switch {
	case f.imported(sel.X) == "net/http" && isWriter(call.Args[0]):
		switch sel.Sel.Name {
		case "ServeContent":
			if len(call.Args) == 5 {
				return responseContent
			}
		case "ServeFile":
			if len(call.Args) == 3 {
				return responseContent
			}
		case "Redirect":
			if len(call.Args) == 4 {
				return responseContent
			}
		}
	case isWriter(sel.X) && len(call.Args) == 1:
		switch sel.Sel.Name {
		case "Write":
			return responseContent
		case "WriteHeader":
			if f.httpSuccessfulStatus(call.Args[0], true) {
				return responseStatus
			}
		}
	case sel.Sel.Name == "Encode" && len(call.Args) == 1:
		expr := sel.X
		if name := httpRouteIdent(expr); name != nil && name.Obj != nil && !f.shadowed[name.Obj] {
			if binding, ok := name.Obj.Decl.(*ast.AssignStmt); ok && len(binding.Lhs) == 1 && len(binding.Rhs) == 1 {
				expr = binding.Rhs[0]
			}
		}
		if ctor, ok := ast.Unparen(expr).(*ast.CallExpr); ok && len(ctor.Args) == 1 && isWriter(ctor.Args[0]) {
			if fn, ok := ast.Unparen(ctor.Fun).(*ast.SelectorExpr); ok && fn.Sel.Name == "NewEncoder" {
				switch f.imported(fn.X) {
				case "encoding/json", "encoding/xml":
					return responseContent
				}
			}
		}
	}
	return noHTTPResponse
}

func (f *httpRouteFile) httpSuccessfulStatus(expr ast.Expr, redirects bool) bool {
	switch expr := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		n, err := strconv.ParseInt(expr.Value, 0, 32)
		return expr.Kind == token.INT && err == nil && n >= 200 && (n < 300 || redirects && n < 400)
	case *ast.SelectorExpr:
		if f.imported(expr.X) == "net/http" {
			switch expr.Sel.Name {
			case "StatusOK", "StatusCreated", "StatusAccepted", "StatusNonAuthoritativeInfo", "StatusNoContent",
				"StatusResetContent", "StatusPartialContent", "StatusMultiStatus", "StatusAlreadyReported", "StatusIMUsed":
				return true
			case "StatusMultipleChoices", "StatusMovedPermanently", "StatusFound", "StatusSeeOther", "StatusNotModified",
				"StatusUseProxy", "StatusTemporaryRedirect", "StatusPermanentRedirect":
				return redirects
			}
		}
	}
	return false
}
