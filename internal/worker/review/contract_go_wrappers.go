package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	maxGoWrapperTraces  = 2
	maxGoWrapperWindows = 5
)

var goExampleCall = regexp.MustCompile(`\b(?:(\w+)\.)?([A-Z][A-Za-z0-9_]*)[ \t]*\(([^()\n]*)\)`)

type goContractExample struct {
	pkg, name string
	literal   string
}

type goWrapperTrace struct {
	excerpts []record.Excerpt
	callees  []string
}

// Admit the whole trace only when its distinct, non-overlapping spans fit the
// existing overview and per-file limits. Follow-up links use these exact IDs.
func goWrapperWindows(existing, trace []record.Excerpt, remain int) ([]record.Excerpt, bool) {
	var windows []record.Excerpt
	for _, x := range trace {
		if slices.ContainsFunc(existing, func(e record.Excerpt) bool { return e.ID == x.ID }) ||
			slices.ContainsFunc(windows, func(e record.Excerpt) bool { return e.ID == x.ID }) {
			continue
		}
		lines, bytes := x.End-x.Start+1, record.JSONLen(x.Text)
		remain -= bytes
		for _, list := range [][]record.Excerpt{existing, windows} {
			for _, e := range list {
				if e.Path != x.Path {
					continue
				}
				if e.Start <= x.End && x.Start <= e.End {
					return nil, false
				}
				lines += e.End - e.Start + 1
				bytes += record.JSONLen(e.Text)
			}
		}
		if lines > roleMaxCodeLines || bytes > roleMaxCodeFile || remain < 128 || len(existing)+len(windows) >= maxGoWrapperWindows {
			return nil, false
		}
		windows = append(windows, x)
	}
	return windows, true
}

func goExamplesInContract(examples []goContractExample, statement, cited string) []goContractExample {
	statementCalls := goExampleCall.FindAllStringSubmatch(statement, 12)
	for _, at := range goExampleCall.FindAllStringSubmatchIndex(cited, 32) {
		if at[0] > 0 && cited[at[0]-1] == '.' {
			continue
		}
		name := cited[at[4]:at[5]]
		if !record.ValidContractLiteral(name) || !containsContractLiteral(statement, name) {
			continue
		}
		example := goContractExample{name: name}
		if at[2] >= 0 {
			example.pkg = cited[at[2]:at[3]]
		}
		if slices.ContainsFunc(statementCalls, func(ref []string) bool {
			return ref[2] == name && ref[1] != "" && example.pkg != "" && ref[1] != example.pkg
		}) {
			continue
		}
		// Use a literal only when the exact example input also appears
		// in the accepted statement. Other examples identify the API.
		arg := strings.TrimSpace(cited[at[6]:at[7]])
		if expr, err := parser.ParseExpr(arg); err == nil && goBranchLiteral(expr) == arg {
			for _, ref := range statementCalls {
				if ref[2] == name && strings.TrimSpace(ref[3]) == arg {
					example.literal = arg
					break
				}
			}
		}
		for i, prev := range examples {
			if prev.name != name {
				continue
			}
			if prev.pkg == "" {
				examples[i].pkg = example.pkg
			} else if example.pkg != "" && prev.pkg != example.pkg {
				examples[i].pkg = "." // Conflicting qualifiers.
			}
			if example.literal != "" {
				examples[i].literal = example.literal
			}
			example.name = ""
			break
		}
		if example.name != "" && len(examples) < maxGoWrapperTraces {
			examples = append(examples, example)
		}
	}
	return examples
}

// Identify only package functions in the contract's directory. Reading every
// eligible non-test Go file there prevents an unseen sibling from supplying
// an alternate definition. All reads share the ordinary witness scan budget.
func (s *roleStage) goWrapperTraces(g codeGroups, queries []witnessQuery, read func(source.Entry) (string, bool, error)) ([]goWrapperTrace, error) {
	if g.tier != nil {
		return nil, nil
	}
	packages := map[string]goWrapperPackage{}
	var traces []goWrapperTrace
	var visited []functionName
	for _, q := range queries {
		if len(q.goExamples) == 0 {
			continue
		}
		pkg, ok := packages[q.scope]
		if !ok {
			var err error
			pkg, err = s.goWrapperPackage(g, q.scope, read)
			if err != nil {
				return nil, err
			}
			packages[q.scope] = pkg
		}
		for _, example := range q.goExamples {
			key := functionName{q.scope, pkg.name, example.name}
			if pkg.name == "" || (example.pkg != "" && example.pkg != pkg.name) || slices.Contains(visited, key) {
				continue
			}
			visited = append(visited, key)
			if trace := s.goWrapperTrace(pkg, key, example); len(trace.excerpts) > 0 {
				traces = append(traces, trace)
				if len(traces) == maxGoWrapperTraces {
					return traces, nil
				}
			}
		}
	}
	return traces, nil
}

type goWrapperPackage struct {
	name        string
	definitions map[functionName]contractGoDefinition
	generated   map[string]bool
}

func (s *roleStage) goWrapperPackage(g codeGroups, dir string, read func(source.Entry) (string, bool, error)) (goWrapperPackage, error) {
	pkg := goWrapperPackage{definitions: map[functionName]contractGoDefinition{}, generated: map[string]bool{}}
	var files []source.Entry
	var size int64
	for _, e := range s.j.Snapshot.Manifest.Entries {
		if path.Dir(e.Path) != dir || e.Kind != "file" || record.SourceKind(e.Path) != "code" || !strings.HasSuffix(e.Path, ".go") {
			continue
		}
		if err := s.ctx.Err(); err != nil {
			return goWrapperPackage{}, err
		}
		if !s.navigationEligible(g, e.Path) || record.DefaultSecondary.Match(e.Path) != "" ||
			len(files) >= navigationMaxFiles || *e.Size > navigationScanBytes-size {
			return goWrapperPackage{}, nil
		}
		files = append(files, e)
		size += *e.Size
	}
	declared := map[string]int{}
	for _, e := range files {
		content, ok, err := read(e)
		if !ok || err != nil {
			return goWrapperPackage{}, err
		}
		positions := token.NewFileSet()
		parsed, err := parser.ParseFile(positions, e.Path, content, parser.DeclarationErrors|parser.ParseComments)
		if err != nil || (pkg.name != "" && pkg.name != parsed.Name.Name) {
			return goWrapperPackage{}, nil
		}
		pkg.name = parsed.Name.Name
		f := contractGoFile{entry: e, positions: positions, parsed: parsed, lines: strings.Split(content, "\n")}
		marked := record.GeneratedHeader([]byte(content))
		pkg.generated[e.Path] = marked || ast.IsGenerated(parsed)
		for name := range parsed.Scope.Objects {
			declared[name]++
		}
		unconditional := f.unconditional()
		for _, d := range parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !unconditional {
				continue
			}
			pkg.definitions[functionName{dir, pkg.name, fn.Name.Name}] = contractGoDefinition{fn, f, unconditional}
		}
	}
	for key := range pkg.definitions {
		if declared[key.name] != 1 {
			delete(pkg.definitions, key)
		}
	}
	return pkg, nil
}

func (s *roleStage) goWrapperTrace(pkg goWrapperPackage, key functionName, example goContractExample) goWrapperTrace {
	wrapper := pkg.definitions[key]
	if wrapper.fn == nil || !ast.IsExported(key.name) ||
		!record.GeneratedHeader([]byte(strings.Join(wrapper.file.lines[:min(10, len(wrapper.file.lines))], "\n"))) ||
		!wrapper.isolatedLines() {
		return goWrapperTrace{}
	}
	var trace goWrapperTrace
	for _, allowCallback := range []bool{false, true} {
		target := pkg.forwardedGoFunction(wrapper, allowCallback)
		if target.fn == nil {
			if !allowCallback {
				return goWrapperTrace{}
			}
			break
		}
		if pkg.generated[target.file.entry.Path] {
			return goWrapperTrace{}
		}
		from, to := wrapper.file.line(wrapper.fn.Pos()), wrapper.file.line(wrapper.fn.End())
		x := s.goWrapperExcerpt(wrapper.file, from, to, witnessMaxLines, witnessMaxExcerpt)
		if x.ID == "" {
			return goWrapperTrace{}
		}
		trace.excerpts = append(trace.excerpts, x)
		trace.callees = append(trace.callees, target.fn.Name.Name)
		wrapper = target
	}
	x := s.goWrapperDecisionWindow(wrapper, example.literal)
	if x.ID == "" {
		return goWrapperTrace{}
	}
	trace.excerpts = append(trace.excerpts, x)
	return trace
}

func (d contractGoDefinition) isolatedLines() bool {
	start := d.file.positions.PositionFor(d.fn.Pos(), false)
	end := d.file.positions.PositionFor(d.fn.End(), false)
	return strings.TrimSpace(d.file.lines[start.Line-1][:start.Column-1]) == "" &&
		strings.TrimSpace(d.file.lines[end.Line-1][end.Column-1:]) == ""
}

func (pkg goWrapperPackage) forwardedGoFunction(wrapper contractGoDefinition, allowCallback bool) contractGoDefinition {
	call, results := goForwardingCall(wrapper.fn)
	if call == nil || call.Ellipsis.IsValid() {
		return contractGoDefinition{}
	}
	target := pkg.resolveGoFunction(wrapper.file, call.Fun)
	if target.fn == nil || target.fn == wrapper.fn ||
		goFieldCount(target.fn.Type.Params) != len(call.Args) || goFieldCount(target.fn.Type.Results) != results {
		return contractGoDefinition{}
	}
	used := 0
	for _, param := range wrapper.fn.Type.Params.List {
		for _, name := range param.Names {
			if used >= len(call.Args) {
				return contractGoDefinition{}
			}
			id, ok := ast.Unparen(call.Args[used]).(*ast.Ident)
			if !ok || name.Name == "_" || id.Obj == nil || id.Obj != name.Obj {
				return contractGoDefinition{}
			}
			used++
		}
	}
	if used != goFieldCount(wrapper.fn.Type.Params) {
		return contractGoDefinition{}
	}
	for _, arg := range call.Args[used:] {
		fn := pkg.resolveGoFunction(wrapper.file, arg)
		if !allowCallback || fn.fn == nil || pkg.generated[fn.file.entry.Path] || fn.fn == target.fn {
			return contractGoDefinition{}
		}
	}
	return target
}

func goFieldCount(fields *ast.FieldList) int {
	n := 0
	if fields != nil {
		for _, f := range fields.List {
			n += max(1, len(f.Names))
		}
	}
	return n
}

func goForwardingCall(fn *ast.FuncDecl) (*ast.CallExpr, int) {
	stmts := fn.Body.List
	switch len(stmts) {
	case 1:
		if r, ok := stmts[0].(*ast.ReturnStmt); ok && len(r.Results) == 1 {
			call, _ := ast.Unparen(r.Results[0]).(*ast.CallExpr)
			return call, goFieldCount(fn.Type.Results)
		}
	case 2:
		a, assign := stmts[0].(*ast.AssignStmt)
		r, returns := stmts[1].(*ast.ReturnStmt)
		if !assign || !returns || a.Tok != token.DEFINE || len(a.Rhs) != 1 || len(r.Results) == 0 ||
			len(r.Results) != goFieldCount(fn.Type.Results) {
			return nil, 0
		}
		result := 0
		for _, expr := range a.Lhs {
			id, ok := expr.(*ast.Ident)
			if !ok {
				return nil, 0
			}
			if id.Name == "_" {
				continue
			}
			if result >= len(r.Results) {
				return nil, 0
			}
			ref, ok := ast.Unparen(r.Results[result]).(*ast.Ident)
			if !ok || ref.Obj == nil || ref.Obj != id.Obj {
				return nil, 0
			}
			result++
		}
		if result == len(r.Results) {
			call, _ := ast.Unparen(a.Rhs[0]).(*ast.CallExpr)
			return call, len(a.Lhs)
		}
	}
	return nil, 0
}

func (pkg goWrapperPackage) resolveGoFunction(f contractGoFile, expr ast.Expr) contractGoDefinition {
	id, indexed := contractGoCallName(&ast.CallExpr{Fun: expr})
	if id == nil || slices.ContainsFunc(f.parsed.Imports, func(i *ast.ImportSpec) bool {
		if i.Name != nil {
			return i.Name.Name == "." || i.Name.Name == id.Name
		}
		p, _ := strconv.Unquote(i.Path.Value)
		return path.Base(p) == id.Name
	}) {
		return contractGoDefinition{}
	}
	target := pkg.definitions[functionName{path.Dir(f.entry.Path), pkg.name, id.Name}]
	if target.fn == nil || (indexed && target.fn.Type.TypeParams == nil) {
		return contractGoDefinition{}
	}
	if id.Obj == target.fn.Name.Obj || slices.Contains(f.parsed.Unresolved, id) {
		return target
	}
	return contractGoDefinition{}
}

func (s *roleStage) goWrapperExcerpt(f contractGoFile, from, to, maxLines, maxBytes int) record.Excerpt {
	if to-from >= maxLines {
		return record.Excerpt{}
	}
	text := strings.Join(f.lines[from-1:to], "\n")
	if record.JSONLen(text) > maxBytes {
		return record.Excerpt{}
	}
	x, _ := s.boundedExcerpt(f.entry, "code", "context", from, f.lines[from-1:to], maxBytes)
	if x.End != to {
		return record.Excerpt{}
	}
	return x
}

func goBranchLiteral(expr ast.Expr) string {
	switch x := ast.Unparen(expr).(type) {
	case *ast.BasicLit:
		return x.Value
	case *ast.Ident:
		if x.Obj == nil && (x.Name == "nil" || x.Name == "true" || x.Name == "false") {
			return x.Name
		}
	}
	return ""
}

// Prefer a directly returned case of the example's argument. Otherwise
// include the callee's leading control flow through a return.
func (s *roleStage) goWrapperDecisionWindow(def contractGoDefinition, literal string) record.Excerpt {
	f, fn := def.file, def.fn
	start, end := f.line(fn.Pos()), f.line(fn.End())
	if x := s.goWrapperExcerpt(f, start, end, witnessMaxLines, witnessMaxExcerpt); x.ID != "" && f.freshBody(fn.Body, x, nil) {
		return x
	}
	if literal != "" {
		for _, stmt := range goParameterSwitchCases(fn) {
			c, ok := stmt.(*ast.CaseClause)
			if !ok || !slices.ContainsFunc(c.List, func(e ast.Expr) bool { return goBranchLiteral(e) == literal }) {
				continue
			}
			for _, stmt := range c.Body {
				if r, ok := stmt.(*ast.ReturnStmt); ok {
					to := f.line(r.End())
					if x := s.goWrapperExcerpt(f, start, to, roleMaxCodeLines, roleMaxCodeFile); x.ID != "" {
						return x
					}
					from := f.line(c.Pos())
					if x := s.goWrapperExcerpt(f, from, to, witnessMaxLines, witnessMaxExcerpt); x.ID != "" {
						return x
					}
				}
			}
		}
	}
	var x record.Excerpt
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if x.ID != "" {
			return false
		}
		switch n := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			x = s.goWrapperExcerpt(f, start, f.line(n.End()), witnessMaxLines, witnessMaxExcerpt)
		}
		return x.ID == ""
	})
	return x
}

func goParameterSwitchCases(fn *ast.FuncDecl) []ast.Stmt {
	if len(fn.Body.List) == 0 || len(fn.Type.Params.List) == 0 || len(fn.Type.Params.List[0].Names) == 0 {
		return nil
	}
	param := fn.Type.Params.List[0].Names[0]
	var tag ast.Expr
	var body *ast.BlockStmt
	switch n := fn.Body.List[0].(type) {
	case *ast.SwitchStmt:
		if n.Init != nil || n.Tag == nil {
			return nil
		}
		tag, body = n.Tag, n.Body
	case *ast.TypeSwitchStmt:
		if n.Init != nil {
			return nil
		}
		var assertion ast.Expr
		switch a := n.Assign.(type) {
		case *ast.AssignStmt:
			if len(a.Rhs) == 1 {
				assertion = a.Rhs[0]
			}
		case *ast.ExprStmt:
			assertion = a.X
		}
		if a, ok := assertion.(*ast.TypeAssertExpr); ok {
			tag, body = a.X, n.Body
		}
	}
	if id, ok := ast.Unparen(tag).(*ast.Ident); ok && id.Obj != nil && id.Obj == param.Obj {
		return body.List
	}
	return nil
}
