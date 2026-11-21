package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const (
	httpAssertionFiles  = 32
	httpAssertionBytes  = 512 << 10
	httpAssertionLines  = 28
	httpAssertionWindow = 1600
)

type httpTestRequest struct {
	start          int
	method, target string
}

type httpTestExchange struct {
	httpTestRequest
	route         string
	end, outcomes int
	server        *ast.Object
	router        string
	success       *ast.IfStmt
	state         httpTestState
}

type httpTestState struct {
	start, end                      int
	method, key, keyType, errorType string
	constructor                     httpRouteMethod
	argument                        int
}

type workflowTestWitness struct {
	excerpt record.Excerpt
	related httpRelatedTest
}

// Tests are optional support for already selected routes. Only take a test
// from its registration or handler directory, and match it against every
// known route in that directory before selecting its source window.
func (s *roleStage) workflowRouteTests(selected, catalog []workflowRoute) ([]record.Excerpt, error) {
	witnesses, err := s.workflowTestWitnesses(selected, catalog)
	out := make([]record.Excerpt, len(witnesses))
	for i, witness := range witnesses {
		out[i] = witness.excerpt
	}
	return out, err
}

func (s *roleStage) workflowTestWitnesses(selected, catalog []workflowRoute) ([]workflowTestWitness, error) {
	out := make([]workflowTestWitness, len(selected))
	dirs := map[string]bool{}
	selectedIDs := map[string]int{}
	for i, r := range selected {
		selectedIDs[httpTestRouteKey(r)] = i
		dirs[path.Dir(r.Registration.Path)] = true
		dirs[path.Dir(r.Handler.Path)] = true
	}
	if len(dirs) == 0 {
		return out, nil
	}
	groups := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	scores := make([]int, len(selected))
	files, bytes := 0, int64(0)
	for e := range groups.orderedFiles() {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		dir := path.Dir(e.Path)
		if !dirs[dir] || !strings.HasSuffix(e.Path, "_test.go") || !s.navigationEligible(groups, e.Path) ||
			*e.Size > httpRouteFileBytes || *e.Size > httpAssertionBytes-bytes {
			continue
		}
		files++
		bytes += *e.Size
		content, ok, err := s.readSource(e)
		if err != nil {
			return nil, err
		}
		if ok && !record.GeneratedHeader([]byte(content)) {
			positions := token.NewFileSet()
			file, err := parser.ParseFile(positions, e.Path, content, parser.DeclarationErrors|parser.ParseComments)
			if err == nil && !ast.IsGenerated(file) {
				f := &httpRouteFile{contractGoFile: contractGoFile{e, positions, file, strings.Split(content, "\n")}, imports: httpRouteImports(file)}
				if f.unconditional() {
					for _, route := range catalog {
						if path.Dir(route.Handler.Path) == dir && route.state.packageName == file.Name.Name {
							f.pkg = route.state.field.owner.pkg
							break
						}
					}
					f.shadowed = testUnstableObjects(file)
					for _, exchanges := range f.httpTestExchanges(catalog) {
						s.httpTestWindows(f, exchanges, selectedIDs, out, scores)
						s.httpTestStateWindows(f, exchanges, selected, catalog, out, scores)
					}
				}
			}
		}
		if files >= httpAssertionFiles {
			break
		}
	}
	return out, nil
}

func (s *roleStage) storeHTTPTestWindow(f *httpRouteFile, first, last, score int, related httpRelatedTest, out *workflowTestWitness, priorScore *int) {
	if score <= *priorScore || last-first >= httpAssertionLines {
		return
	}
	window, _ := s.boundedExcerpt(f.entry, "test", "context", first, f.lines[first-1:last], httpAssertionWindow)
	if window.End == last && window.ID != "" {
		*out, *priorScore = workflowTestWitness{window, related}, score
	}
}

func (s *roleStage) httpTestWindows(f *httpRouteFile, exchanges []httpTestExchange, ids map[string]int, out []workflowTestWitness, scores []int) {
	for j, x := range exchanges {
		if x.outcomes == 0 {
			continue
		}
		if i, ok := ids[x.route]; ok {
			s.storeHTTPTestWindow(f, x.start, x.end, x.outcomes, httpRelatedTest{}, &out[i], &scores[i])
		}
		if j == 0 || x.method != "GET" {
			continue
		}
		prev := exchanges[j-1]
		if prev.outcomes == 0 || prev.server == nil || x.server != prev.server || x.router != prev.router ||
			prev.method == "GET" || prev.method == "HEAD" || prev.method == "OPTIONS" ||
			prev.start > x.start || prev.end >= x.start || (prev.target != x.target && !strings.HasPrefix(prev.target, x.target+"/")) {
			continue
		}
		for _, id := range []string{prev.route, x.route} {
			if i, ok := ids[id]; ok {
				s.storeHTTPTestWindow(f, prev.start, x.end, 8+prev.outcomes+x.outcomes, httpRelatedTest{}, &out[i], &scores[i])
			}
		}
	}
}

func (f *httpRouteFile) httpTestExchanges(catalog []workflowRoute) [][]httpTestExchange {
	var out [][]httpTestExchange
	for _, decl := range f.parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") || (fn.Type.Results != nil && len(fn.Type.Results.List) > 0) {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(strings.TrimPrefix(fn.Name.Name, "Test")); unicode.IsLower(r) {
			continue
		}
		params := httpRouteFields(fn.Type.Params)
		if len(params) != 1 || len(params[0].Names) != 1 || !f.testingParameter(params[0]) {
			continue
		}
		flow := &httpTestFlow{file: f, t: params[0].Names[0].Obj, catalog: catalog,
			requests: map[*ast.Object]httpTestRequest{}, recorders: map[*ast.Object]bool{}, served: map[*ast.Object]int{},
			testStart: fn.Body.Pos()}
		flow.walk(fn.Body)
		out = append(out, flow.exchanges)
	}
	return out
}

func (f *httpRouteFile) testingParameter(field *ast.Field) bool {
	ptr, _ := field.Type.(*ast.StarExpr)
	if ptr != nil {
		if x, ok := ptr.X.(*ast.SelectorExpr); ok {
			return f.imported(x.X) == "testing" && x.Sel.Name == "T"
		}
	}
	return false
}

type httpTestFlow struct {
	file      *httpRouteFile
	t         *ast.Object
	catalog   []workflowRoute
	requests  map[*ast.Object]httpTestRequest
	recorders map[*ast.Object]bool
	served    map[*ast.Object]int // exchange index by recorder
	exchanges []httpTestExchange
	testStart token.Pos
}

func (x *httpTestFlow) walk(body *ast.BlockStmt) {
	for i, stmt := range body.List {
		switch st := stmt.(type) {
		case *ast.BlockStmt:
			x.walk(st)
		case *ast.AssignStmt:
			if st.Tok != token.DEFINE || len(st.Lhs) == 0 || len(st.Rhs) != 1 {
				continue
			}
			id := httpRouteIdent(st.Lhs[0])
			call, _ := ast.Unparen(st.Rhs[0]).(*ast.CallExpr)
			if id == nil || id.Obj == nil || call == nil || x.file.shadowed[id.Obj] {
				continue
			}
			if len(st.Lhs) == 1 && x.file.testPackageCall(call, "net/http/httptest") == "NewRecorder" && len(call.Args) == 0 {
				x.recorders[id.Obj] = true
				continue
			}
			arity := 1
			if x.file.testPackageCall(call, "net/http") == "NewRequest" ||
				x.file.testPackageCall(call, "net/http") == "NewRequestWithContext" {
				arity = 2
			}
			if method, target := x.file.testRequest(call); len(st.Lhs) == arity && method != "" && target != "" {
				x.requests[id.Obj] = httpTestRequest{x.file.line(st.Pos()), method, target}
			}
		case *ast.ExprStmt:
			call, _ := ast.Unparen(st.X).(*ast.CallExpr)
			if call == nil || len(call.Args) != 2 {
				continue
			}
			sel, _ := ast.Unparen(call.Fun).(*ast.SelectorExpr)
			if sel == nil || sel.Sel.Name != "ServeHTTP" {
				continue
			}
			res, req := httpRouteIdent(call.Args[0]), httpRouteIdent(call.Args[1])
			if res == nil || req == nil || !x.recorders[res.Obj] {
				continue
			}
			// A response recorder must identify a single exchange.
			if i, reused := x.served[res.Obj]; reused {
				if i >= 0 {
					x.exchanges[i].outcomes = 0
				}
				x.served[res.Obj] = -1
				continue
			}
			r, found := x.requests[req.Obj]
			if found && len(x.exchanges) < 24 {
				server, router := x.file.testServer(sel.X)
				x.served[res.Obj] = len(x.exchanges)
				x.exchanges = append(x.exchanges, httpTestExchange{httpTestRequest: r, route: testRouteID(x.catalog, path.Dir(x.file.entry.Path), r), server: server, router: router})
			} else {
				x.served[res.Obj] = -1
			}
		case *ast.IfStmt:
			x.assertion(st)
			if i >= 2 {
				x.stateAssertion(st, body.List[i-2], body.List[i-1])
			}
		}
	}
}

func (x *httpTestFlow) assertion(st *ast.IfStmt) {
	condition, _ := ast.Unparen(st.Cond).(*ast.BinaryExpr)
	if condition == nil || condition.Op != token.NEQ || !x.testFailure(st.Body) {
		return
	}
	actual := x.file.testAssigned(condition.X, 0)
	recorder, status := x.testResponse(actual)
	expected := x.file.testAssigned(condition.Y, 0)
	if recorder == nil {
		return
	}
	if status {
		if !x.file.testExpectedStatus(expected) {
			return
		}
	} else if _, ok := httpRouteString(expected); !ok {
		return
	}
	if i, ok := x.served[recorder]; ok && i >= 0 {
		e := &x.exchanges[i]
		end := x.file.line(st.End())
		if end-e.start < httpAssertionLines {
			e.end, e.outcomes = end, min(3, e.outcomes+1)
			if status && x.file.httpSuccessfulStatus(expected, false) {
				e.success = st
			}
		}
	}
}

func (x *httpTestFlow) testFailure(body *ast.BlockStmt) bool {
	for _, stmt := range body.List {
		if exp, ok := stmt.(*ast.ExprStmt); ok {
			if call, ok := ast.Unparen(exp.X).(*ast.CallExpr); ok {
				if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
					if id := httpRouteIdent(sel.X); id != nil && id.Obj != nil && id.Obj == x.t {
						switch sel.Sel.Name {
						case "Error", "Errorf", "Fatal", "Fatalf", "Fail", "FailNow":
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func (x *httpTestFlow) testResponse(expr ast.Expr) (*ast.Object, bool) {
	if sel, ok := ast.Unparen(expr).(*ast.SelectorExpr); ok {
		switch sel.Sel.Name {
		case "Code":
			if id := httpRouteIdent(sel.X); id != nil {
				return id.Obj, true
			}
		case "StatusCode":
			if call, ok := ast.Unparen(x.file.testAssigned(sel.X, 0)).(*ast.CallExpr); ok {
				return testReceiver(call, "Result", 0), true
			}
		}
	}
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok {
		if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
			if field, ok := ast.Unparen(sel.X).(*ast.SelectorExpr); ok && field.Sel.Name == "Body" && sel.Sel.Name == "String" && len(call.Args) == 0 {
				if id := httpRouteIdent(field.X); id != nil {
					return id.Obj, false
				}
			}
			if inner, ok := ast.Unparen(sel.X).(*ast.CallExpr); ok && sel.Sel.Name == "Get" && len(call.Args) == 1 {
				if _, literal := httpRouteString(call.Args[0]); literal {
					return testReceiver(inner, "Header", 0), false
				}
			}
		}
	}
	return nil, false
}

func testReceiver(call *ast.CallExpr, method string, arity int) *ast.Object {
	if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == method && len(call.Args) == arity {
		if id := httpRouteIdent(sel.X); id != nil {
			return id.Obj
		}
	}
	return nil
}

// Follow simple short declarations, including an if initializer's got/want.
func (f *httpRouteFile) testAssigned(expr ast.Expr, depth int) ast.Expr {
	id := httpRouteIdent(expr)
	if id == nil || id.Obj == nil || f.shadowed[id.Obj] || depth >= 4 {
		return expr
	}
	if d, ok := id.Obj.Decl.(*ast.AssignStmt); ok && d.Tok == token.DEFINE && len(d.Lhs) == len(d.Rhs) {
		for i, target := range d.Lhs {
			if name := httpRouteIdent(target); name != nil && name.Obj == id.Obj {
				return f.testAssigned(d.Rhs[i], depth+1)
			}
		}
	}
	return expr
}

func (f *httpRouteFile) testExpectedStatus(expr ast.Expr) bool {
	if lit, ok := ast.Unparen(expr).(*ast.BasicLit); ok && lit.Kind == token.INT {
		n, err := strconv.Atoi(lit.Value)
		return err == nil && n >= 100 && n <= 599
	}
	if sel, ok := ast.Unparen(expr).(*ast.SelectorExpr); ok {
		return f.imported(sel.X) == "net/http" && strings.HasPrefix(sel.Sel.Name, "Status")
	}
	return false
}

func (f *httpRouteFile) testServer(expr ast.Expr) (*ast.Object, string) {
	accessor := ""
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && len(call.Args) == 0 {
		if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
			accessor, expr = sel.Sel.Name, sel.X
		}
	}
	if id := httpRouteIdent(expr); id != nil && id.Obj != nil && !f.shadowed[id.Obj] {
		return id.Obj, accessor
	}
	return nil, ""
}

func testUnstableObjects(file *ast.File) map[*ast.Object]bool {
	writes := map[*ast.Object]bool{}
	served := map[*ast.Object]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				for depth := 0; depth < 5; depth++ {
					switch root := ast.Unparen(lhs).(type) {
					case *ast.SelectorExpr:
						lhs = root.X
						continue
					case *ast.StarExpr:
						lhs = root.X
						continue
					case *ast.Ident:
						if root.Obj != nil && root.Obj.Decl != n {
							writes[root.Obj] = true
						}
					}
					break
				}
			}
		case *ast.CallExpr:
			if sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "ServeHTTP" && len(n.Args) == 2 {
					if id := httpRouteIdent(n.Args[0]); id != nil && id.Obj != nil {
						writes[id.Obj] = writes[id.Obj] || served[id.Obj]
						served[id.Obj] = true
					}
				} else if sel.Sel.Name == "WriteHeader" || sel.Sel.Name == "Write" || sel.Sel.Name == "Flush" {
					if id := httpRouteIdent(sel.X); id != nil && id.Obj != nil {
						writes[id.Obj] = true
					}
				}
			}
		}
		return true
	})
	return writes
}

func (f *httpRouteFile) testPackageCall(call *ast.CallExpr, pkg string) string {
	if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok && f.imported(sel.X) == pkg {
		return sel.Sel.Name
	}
	return ""
}

func (f *httpRouteFile) testRequest(call *ast.CallExpr) (string, string) {
	method, target := f.testRequestArgs(call)
	if method == nil {
		// A local helper may wrap the constructor when its only statement
		// returns a request whose method and target bind to its arguments.
		id := httpRouteIdent(call.Fun)
		if id == nil || id.Obj == nil || id.Obj.Kind != ast.Fun {
			return "", ""
		}
		fn, _ := id.Obj.Decl.(*ast.FuncDecl)
		if fn == nil || fn.Recv != nil || fn.Body == nil || len(fn.Body.List) != 1 || len(httpRouteFields(fn.Type.Params)) != len(call.Args) {
			return "", ""
		}
		ret, _ := fn.Body.List[0].(*ast.ReturnStmt)
		if ret == nil || len(ret.Results) != 1 {
			return "", ""
		}
		inner, _ := ast.Unparen(ret.Results[0]).(*ast.CallExpr)
		if inner == nil || f.testPackageCall(inner, "net/http/httptest") == "" {
			return "", ""
		}
		method, target = f.testRequestArgs(inner)
		if method == nil {
			return "", ""
		}
		m := httpRouteFieldIndex(fn.Type.Params, httpRouteIdent(method))
		p := httpRouteFieldIndex(fn.Type.Params, httpRouteIdent(target))
		if m < 0 || p < 0 || m == p {
			return "", ""
		}
		method, target = call.Args[m], call.Args[p]
	}
	m := f.method(f.testAssigned(method, 0))
	p, _, _ := strings.Cut(f.testLiteralPath(target, 0), "?")
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "%{}") || !httpRouteValidPattern(p) ||
		p != "/" && path.Clean(p) != strings.TrimSuffix(p, "/") {
		return "", ""
	}
	return m, p
}

func (f *httpRouteFile) testRequestArgs(call *ast.CallExpr) (ast.Expr, ast.Expr) {
	name := f.testPackageCall(call, "net/http/httptest")
	if name == "" {
		name = f.testPackageCall(call, "net/http")
	}
	switch name {
	case "NewRequest":
		if len(call.Args) == 3 {
			return call.Args[0], call.Args[1]
		}
	case "NewRequestWithContext":
		if len(call.Args) == 4 {
			return call.Args[1], call.Args[2]
		}
	}
	return nil, nil
}

func (f *httpRouteFile) testLiteralPath(expr ast.Expr, depth int) string {
	if depth >= 5 {
		return ""
	}
	expr = f.testAssigned(expr, 0)
	if literal, ok := httpRouteString(expr); ok {
		return literal
	}
	if concat, ok := ast.Unparen(expr).(*ast.BinaryExpr); ok && concat.Op == token.ADD {
		l, r := f.testLiteralPath(concat.X, depth+1), f.testLiteralPath(concat.Y, depth+1)
		if l != "" && r != "" && len(l)+len(r) <= httpRouteMaxPath*2 {
			return l + r
		}
	}
	return ""
}

func testRouteID(catalog []workflowRoute, dir string, r httpTestRequest) string {
	id := ""
	for _, candidate := range catalog {
		if (path.Dir(candidate.Registration.Path) != dir && path.Dir(candidate.Handler.Path) != dir) ||
			(candidate.Method != "" && candidate.Method != r.method) || !testRouteMatches(candidate, r.target) {
			continue
		}
		// Even a method-independent match can be a competing registration.
		if id != "" {
			return ""
		}
		id = httpTestRouteKey(candidate)
	}
	return id
}

func httpTestRouteKey(route workflowRoute) string {
	return route.Registration.ID + "\x00" + route.Method + "\x00" + route.Path
}

func testRouteMatches(route workflowRoute, target string) bool {
	pattern := route.Path
	if !strings.HasPrefix(pattern, "/") {
		return false // a relative route table needs a proven mount path
	}
	prefix := route.PatternKind == "prefix"
	if !strings.Contains(pattern, "{") {
		return pattern == target || prefix && (strings.HasPrefix(target, pattern+"/") || strings.HasSuffix(pattern, "/") && strings.HasPrefix(target, pattern))
	}
	for part := range strings.SplitSeq(pattern[1:], "/") {
		// Advance by a segment, keeping any trailing slash meaningful.
		if len(target) == 0 || target[0] != '/' {
			return false
		}
		segment, tail, extra := strings.Cut(target[1:], "/")
		if !testRouteSegment(part, segment) {
			return false
		}
		if extra {
			target = "/" + tail
		} else {
			target = ""
		}
	}
	return prefix || target == ""
}

func testRouteSegment(pattern, actual string) bool {
	left, rest, variable := strings.Cut(pattern, "{")
	if !variable {
		return pattern == actual
	}
	name, right, closed := strings.Cut(rest, "}")
	return closed && token.IsIdentifier(name) && len(actual) > len(left)+len(right) &&
		!strings.ContainsAny(left+right, "{}") && strings.HasPrefix(actual, left) && strings.HasSuffix(actual, right)
}
