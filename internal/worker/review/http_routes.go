package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	httpRouteScanFiles = 512
	httpRouteScanBytes = 4 << 20
	httpRouteFileBytes = 96 << 10
	httpRouteMax       = 64
	httpRouteMaxPath   = 128
)

// workflowRoute describes a registration and the bounded source windows behind
// it. Path is the pattern at registration, which may be relative to a router.
// A missing method means the registration has no explicit method constraint.
type workflowRoute struct {
	Method, Path, Symbol, PatternKind string
	handlerOffset                     int // byte position of an eligible shared handler body, plus one
	DecisionSymbol                    string
	Registration, Handler             record.Excerpt
	DecisionCall, Decision            record.Excerpt
	OutcomeCall, Outcome              record.Excerpt
	OutcomeSymbol                     string
	OutcomeDepth                      int
	Test                              record.Excerpt
	state                             httpRouteState
	relatedTest                       httpRelatedTest
}

type httpRouteName struct{ pkg, name string }
type httpRouteMethod struct {
	owner httpRouteName // the package and optional receiver type
	name  string
}

type httpRouteFile struct {
	contractGoFile
	pkg      string
	imports  map[string]string
	shadowed map[*ast.Object]bool
	eligible bool
}

type httpRouteFunc struct {
	file *httpRouteFile
	decl *ast.FuncDecl
}

type httpRouteType struct {
	file *httpRouteFile
	decl ast.Expr
}

type httpRouteIndex struct {
	files        []*httpRouteFile
	functions    map[httpRouteMethod][]httpRouteFunc
	types        map[httpRouteName][]httpRouteType
	bindings     map[httpRouteName]int
	incomplete   map[string]bool // directory with unread ordinary source
	injections   map[httpRouteField]httpRouteInjection
	constructors map[httpRouteMethod][]httpRouteArgument
}

func (s *roleStage) workflowRoutes() ([]workflowRoute, error) {
	return s.workflowRoutesFor(nil)
}

func (s *roleStage) workflowRoutesFor(needsOutcome func(workflowRoute) bool) ([]workflowRoute, error) {
	idx, err := s.httpRouteIndex()
	if err != nil || len(idx.files) == 0 {
		return nil, err
	}
	var registrations []httpRouteRegistration
	for _, f := range idx.files {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if f.eligible {
			registrations = idx.registrations(f, registrations)
		}
		if len(registrations) >= httpRouteMax {
			break
		}
	}
	if len(registrations) == 0 {
		return nil, nil
	}
	if err := idx.indexInjections(s.ctx); err != nil {
		return nil, err
	}
	out := make([]workflowRoute, 0, len(registrations))
	for _, registration := range registrations {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if r, ok := s.httpRouteWindows(idx, registration, needsOutcome); ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *roleStage) httpRouteIndex() (*httpRouteIndex, error) {
	manifest := s.j.Snapshot.Manifest
	scope := normalizedScope(s.j.Scope)
	groups := planCodeGroupsFor(manifest, scope, s.h.secondary(), changedPaths(s.j))
	idx := &httpRouteIndex{
		functions: map[httpRouteMethod][]httpRouteFunc{}, types: map[httpRouteName][]httpRouteType{},
		bindings: map[httpRouteName]int{}, incomplete: map[string]bool{},
	}
	modules := map[string]string{}
	var eligible []source.Entry
	for e := range groups.orderedFiles() {
		if strings.HasSuffix(e.Path, ".go") && record.SourceKind(e.Path) == "code" &&
			s.navigationEligible(groups, e.Path) && *e.Size <= httpRouteFileBytes {
			eligible = append(eligible, e)
		}
	}
	if len(eligible) == 0 {
		return idx, nil
	}
	moduleReads := 0
	for _, e := range manifest.Entries {
		if err := s.ctx.Err(); err != nil {
			return idx, err
		}
		if path.Base(e.Path) != "go.mod" || e.Kind != "file" || goIgnored(e.Path) {
			continue
		}
		dir := path.Dir(e.Path)
		modules[dir] = ""
		if e.ExcludedReason != "" || e.SHA256 == "" || e.Size == nil || *e.Size > 16<<10 || s.secretFiles()[e.Path] ||
			!inScope(e.Path, scope) || s.h.secondary().Match(e.Path) != "" || groups.tier != nil || moduleReads == 16 {
			continue
		}
		moduleReads++
		content, ok, err := s.readSource(e)
		if err != nil {
			return idx, err
		}
		if ok {
			modules[dir] = httpRouteModule(content)
		}
	}
	var scanned int64
	parsed := map[string]bool{}
	packageNames := map[string]string{}
	for i, e := range eligible {
		if err := s.ctx.Err(); err != nil {
			return idx, err
		}
		if i == httpRouteScanFiles {
			break
		}
		if scanned+*e.Size > httpRouteScanBytes {
			continue
		}
		scanned += *e.Size
		content, ok, err := s.readSource(e)
		if err != nil {
			return idx, err
		}
		if !ok {
			continue
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, e.Path, content, parser.DeclarationErrors|parser.ParseComments)
		if err != nil {
			continue
		}
		parsed[e.Path] = true
		if ast.IsGenerated(file) || record.GeneratedHeader([]byte(content)) {
			continue
		}
		dir := path.Dir(e.Path)
		if name := packageNames[dir]; name != "" && name != file.Name.Name {
			idx.incomplete[dir] = true
		}
		packageNames[dir] = file.Name.Name
		f := &httpRouteFile{
			contractGoFile: contractGoFile{entry: e, positions: positions, parsed: file, lines: strings.Split(content, "\n")},
			pkg:            httpRoutePackage(dir, file.Name.Name, modules),
			imports:        httpRouteImports(file),
		}
		f.eligible = f.unconditional()
		idx.addFile(f)
	}
	for _, e := range manifest.Entries {
		if e.Kind == "file" && strings.HasSuffix(e.Path, ".go") && record.SourceKind(e.Path) == "code" && !goIgnored(e.Path) && !parsed[e.Path] {
			idx.incomplete[path.Dir(e.Path)] = true
		}
	}
	return idx, nil
}

func httpRouteModule(content string) string {
	for line := range strings.Lines(content) {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "module" {
			continue
		}
		name := fields[1]
		if strings.HasPrefix(name, `"`) {
			name, _ = strconv.Unquote(name)
		}
		if name != "" && len(name) <= 256 && path.Clean(name) == name && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "/") {
			return name
		}
		break
	}
	return ""
}

func httpRoutePackage(dir, name string, modules map[string]string) string {
	for root := dir; ; root = path.Dir(root) {
		if mod, ok := modules[root]; ok {
			if mod != "" {
				if dir == root {
					return mod
				}
				return path.Join(mod, strings.TrimPrefix(dir, root+"/"))
			}
			break
		}
		if root == "." {
			break
		}
	}
	return "\x00" + dir + ":" + name
}

func httpRouteImports(f *ast.File) map[string]string {
	imports := map[string]string{}
	for _, i := range f.Imports {
		p, _ := strconv.Unquote(i.Path.Value)
		name := path.Base(p)
		if i.Name != nil {
			name = i.Name.Name
		} else if p == "github.com/jackc/pgx/v4" || p == "github.com/jackc/pgx/v5" {
			name = "pgx"
		}
		if name != "." && name != "_" {
			imports[name] = p
		}
	}
	return imports
}

func (idx *httpRouteIndex) addFile(f *httpRouteFile) {
	idx.files = append(idx.files, f)
	for name := range f.parsed.Scope.Objects {
		idx.bindings[httpRouteName{f.pkg, name}]++
	}
	f.shadowed = map[*ast.Object]bool{}
	ast.Inspect(f.parsed, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range a.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Obj != nil && id.Obj.Decl != a {
					f.shadowed[id.Obj] = true
				}
			}
		}
		return true
	})
	for _, d := range f.parsed.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			owner := httpRouteName{pkg: f.pkg}
			if d.Recv != nil {
				if len(d.Recv.List) != 1 || len(d.Recv.List[0].Names) > 1 {
					continue
				}
				owner = f.namedType(d.Recv.List[0].Type)
				if owner.pkg != f.pkg || owner.name == "" {
					continue
				}
			}
			key := httpRouteMethod{owner, d.Name.Name}
			idx.functions[key] = append(idx.functions[key], httpRouteFunc{f, d})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				typ := spec.(*ast.TypeSpec)
				if !typ.Assign.IsValid() {
					key := httpRouteName{f.pkg, typ.Name.Name}
					idx.types[key] = append(idx.types[key], httpRouteType{f, typ.Type})
				}
			}
		}
	}
}

func (f *httpRouteFile) imported(id ast.Expr) string {
	if id, ok := ast.Unparen(id).(*ast.Ident); ok && id.Obj == nil {
		return f.imports[id.Name]
	}
	return ""
}

func (f *httpRouteFile) namedType(expr ast.Expr) httpRouteName {
	if ptr, ok := ast.Unparen(expr).(*ast.StarExpr); ok {
		expr = ptr.X
	}
	switch expr := ast.Unparen(expr).(type) {
	case *ast.Ident:
		if expr.Obj == nil {
			return httpRouteName{f.pkg, expr.Name}
		}
		if spec, ok := expr.Obj.Decl.(*ast.TypeSpec); ok && f.parsed.Scope.Lookup(expr.Name) == spec.Name.Obj {
			return httpRouteName{f.pkg, expr.Name}
		}
	case *ast.SelectorExpr:
		if pkg := f.imported(expr.X); pkg != "" {
			return httpRouteName{pkg, expr.Sel.Name}
		}
	}
	return httpRouteName{}
}

func (idx *httpRouteIndex) function(key httpRouteMethod) httpRouteFunc {
	list := idx.functions[key]
	if key.owner.name == "" && idx.bindings[httpRouteName{key.owner.pkg, key.name}] != 1 {
		return httpRouteFunc{}
	}
	if len(list) != 1 || !list[0].file.eligible || list[0].decl.Body == nil || idx.incomplete[path.Dir(list[0].file.entry.Path)] {
		return httpRouteFunc{}
	}
	return list[0]
}

func (idx *httpRouteIndex) typeDecl(t httpRouteName) httpRouteType {
	list := idx.types[t]
	if len(list) != 1 || idx.bindings[t] != 1 || !list[0].file.eligible || idx.incomplete[path.Dir(list[0].file.entry.Path)] {
		return httpRouteType{}
	}
	return list[0]
}

func (s *roleStage) httpRouteExcerpt(f *httpRouteFile, node ast.Node, maxLines, maxBytes int, exact bool) record.Excerpt {
	return s.httpRouteExcerptLines(f, f.line(node.Pos()), f.line(node.End()), maxLines, maxBytes, exact)
}

func (s *roleStage) httpRouteExcerptLines(f *httpRouteFile, start, end, maxLines, maxBytes int, exact bool) record.Excerpt {
	if start < 1 || end < start || end > len(f.lines) {
		return record.Excerpt{}
	}
	if exact && end-start >= maxLines {
		return record.Excerpt{}
	}
	x, _ := s.boundedExcerpt(f.entry, "code", "context", start, f.lines[start-1:min(end, start+maxLines-1)], maxBytes)
	if exact && x.End != end {
		return record.Excerpt{}
	}
	return x
}

func (s *roleStage) httpRouteWindows(idx *httpRouteIndex, r httpRouteRegistration, needsOutcome func(workflowRoute) bool) (workflowRoute, bool) {
	x := workflowRoute{
		Method: r.method, Path: r.pattern, PatternKind: r.patternKind,
		Registration: s.httpRouteExcerpt(r.file, r.call, 8, 800, true),
	}
	target := idx.handler(r.file, r.handler)
	if x.Registration.ID == "" || target.node == nil {
		return workflowRoute{}, false
	}
	x.Symbol = target.name
	if call, factory := ast.Unparen(r.handler).(*ast.CallExpr); !factory || len(call.Args) == 0 {
		x.handlerOffset = target.file.positions.PositionFor(target.node.Pos(), false).Offset + 1
	}
	x.Handler = s.httpRouteExcerpt(target.file, target.node, 24, 1000, false)
	if x.Handler.ID == "" || x.Handler.End < target.file.line(target.body.Lbrace) {
		return workflowRoute{}, false
	}
	if needsOutcome != nil && len(idx.constructors) != 0 {
		x.state = idx.httpRouteState(r, target, x.Handler.End)
	}
	for _, d := range idx.decisions(target) {
		f := target.file
		call := record.Excerpt{}
		if d.guard != nil {
			call = s.httpRouteExcerptLines(f, f.line(d.guard.Pos()), f.line(d.call.End()), 16, 1600, true)
		}
		if call.ID == "" {
			call = s.httpRouteExcerpt(f, d.call, 6, 600, true)
		}
		if call.ID == "" {
			continue
		}
		decision := s.httpRouteDecisionWindow(d.target)
		if decision.ID != "" && decision.End >= d.target.file.line(d.target.decl.Body.Lbrace) {
			x.DecisionCall, x.Decision = call, decision
			if name := d.target.decl.Name.Name; record.ValidContractLiteral(name) {
				x.DecisionSymbol = name
			}
			if needsOutcome != nil && needsOutcome(x) {
				x.OutcomeCall, x.Outcome, x.OutcomeSymbol, x.OutcomeDepth = s.httpRouteOutcome(idx, d.target, decision, r.method)
			}
			break
		}
	}
	return x, true
}

func httpRouteFields(fields *ast.FieldList) []*ast.Field {
	var out []*ast.Field
	if fields != nil {
		for _, f := range fields.List {
			for i := 0; i < max(1, len(f.Names)); i++ {
				out = append(out, f)
			}
		}
	}
	return out
}

func httpRouteFieldIndex(fields *ast.FieldList, id *ast.Ident) int {
	idx := 0
	if id != nil && id.Obj != nil && fields != nil {
		for _, f := range fields.List {
			for i, name := range f.Names {
				if name.Obj == id.Obj {
					return idx + i
				}
			}
			idx += max(1, len(f.Names))
		}
	}
	return -1
}

func httpRouteIdent(expr ast.Expr) *ast.Ident {
	id, _ := ast.Unparen(expr).(*ast.Ident)
	return id
}

func httpRouteString(expr ast.Expr) (string, bool) {
	lit, ok := ast.Unparen(expr).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

func httpRouteValidPattern(pattern string) bool {
	if len(pattern) == 0 || len(pattern) > httpRouteMaxPath || record.Sensitive([]byte(pattern)) {
		return false
	}
	for _, r := range pattern {
		if r <= ' ' || r > '~' || r == '?' || r == '#' || r == '\\' {
			return false
		}
	}
	return true
}

func (f *httpRouteFile) method(expr ast.Expr) string {
	value, _ := httpRouteString(expr)
	if sel, ok := ast.Unparen(expr).(*ast.SelectorExpr); ok && f.imported(sel.X) == "net/http" {
		if suffix, has := strings.CutPrefix(sel.Sel.Name, "Method"); has && len(suffix) > 0 &&
			suffix == strings.ToUpper(suffix[:1])+strings.ToLower(suffix[1:]) {
			value = strings.ToUpper(suffix)
		}
	}
	if httpRouteMethodName(value) {
		return value
	}
	return ""
}

func httpRouteMethodName(value string) bool {
	return slices.Contains([]string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "CONNECT", "OPTIONS", "TRACE"}, value)
}
