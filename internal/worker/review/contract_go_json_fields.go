package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

var goJSONPath = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*){0,2}$`)

type goJSONTrace struct {
	contractIndex int
	excerpts      []record.Excerpt
	core          []record.Excerpt
	guard         bool
}

type goJSONField struct {
	file  *httpRouteFile
	decl  *ast.Field
	owner *ast.StructType
	name  string
}

type goJSONSite struct {
	write ast.Node
	owner ast.Node
	ret   *ast.ReturnStmt
	value ast.Expr
}

// Exact wire names from code spans in both the accepted statement and its
// citation. These select source positions without making claims about values.
func documentedGoJSONFields(out []string, statement, raw string) []string {
	for _, code := range witnessCode.FindAllStringSubmatch(statement, 12) {
		name := code[1]
		if len(out) == 3 {
			break
		}
		if len(name) <= 64 && goJSONPath.MatchString(name) && name != "true" && name != "false" && name != "null" && name != "nil" && !slices.Contains(out, name) && strings.Contains(raw, "`"+name+"`") {
			out = append(out, name)
		}
	}
	return out
}

// Reuse the caller's verified content and read budget. Use directory+package
// identity for same-package bindings, and reject unread source siblings.
func (s *roleStage) goJSONFieldTraces(queries []witnessQuery, sources map[string]string) ([]goJSONTrace, error) {
	if !slices.ContainsFunc(queries, func(q witnessQuery) bool { return len(q.goJSONFields) != 0 }) {
		return nil, nil
	}
	idx := &httpRouteIndex{functions: map[httpRouteMethod][]httpRouteFunc{}, types: map[httpRouteName][]httpRouteType{},
		bindings: map[httpRouteName]int{}, incomplete: map[string]bool{}}
	packages := map[string]string{}
	runScope := normalizedScope(s.j.Scope)
	// Walk the manifest for stable ordering and completeness within the
	// requested run, including files lost to screening or scan limits.
	for _, e := range s.j.Snapshot.Manifest.Entries {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if e.Kind != "file" || !strings.HasSuffix(e.Path, ".go") || record.SourceKind(e.Path) != "code" || goIgnored(e.Path) || !inScope(e.Path, runScope) ||
			!slices.ContainsFunc(queries, func(q witnessQuery) bool { return len(q.goJSONFields) > 0 && record.ScopeUnder(e.Path, q.scope) }) {
			continue
		}
		dir := path.Dir(e.Path)
		text, available := sources[e.Path]
		if !available {
			idx.incomplete[dir] = true
			continue
		}
		pos := token.NewFileSet()
		root, err := parser.ParseFile(pos, e.Path, text, parser.DeclarationErrors|parser.ParseComments)
		if err != nil || ast.IsGenerated(root) || record.GeneratedHeader([]byte(text)) {
			idx.incomplete[dir] = true
			continue
		}
		if name := packages[dir]; name != "" && name != root.Name.Name {
			idx.incomplete[dir] = true
		}
		packages[dir] = root.Name.Name
		f := &httpRouteFile{contractGoFile: contractGoFile{e, pos, root, strings.Split(text, "\n")}, pkg: httpRoutePackage(dir, root.Name.Name, nil)}
		f.eligible = f.unconditional()
		idx.addFile(f)
	}
	var out []goJSONTrace
	for _, q := range queries {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if len(q.goJSONFields) == 0 {
			continue
		}
		trace := s.goJSONClaimTraces(idx, q)
		if len(trace.core) > 0 {
			trace.contractIndex = q.contractIndex
			out = append(out, trace)
		}
	}
	return out, nil
}

func (s *roleStage) goJSONClaimTraces(idx *httpRouteIndex, q witnessQuery) goJSONTrace {
	roots := map[httpRouteName][][]goJSONField{}
	for name := range idx.types {
		var all [][]goJSONField
		for _, field := range q.goJSONFields {
			chain := goJSONFieldChain(idx, name, strings.Split(field, "."), q.scope)
			if len(chain) == 0 {
				break
			}
			all = append(all, chain)
		}
		if len(all) == len(q.goJSONFields) {
			roots[name] = all
		}
	}
	// Repeated generic wire names are common. Only prefer a uniquely
	// identified shape over regular lexical evidence.
	if len(roots) != 1 {
		return goJSONTrace{}
	}
	var decisions, mappings, values, returns, outer []record.Excerpt
	for _, f := range idx.files {
		if !f.eligible || idx.incomplete[path.Dir(f.entry.Path)] || !record.ScopeUnder(f.entry.Path, q.scope) {
			continue
		}
		for _, d := range f.parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || len(fn.Body.List) == 0 || fn.Type.Results == nil {
				continue
			}
			name := f.namedType(fn.Type.Results.List[0].Type)
			fields := roots[name]
			if len(fields) == 0 || name.pkg != f.pkg {
				continue
			}
			for _, chain := range fields {
				site := goJSONReturnField(f, fn, name, chain)
				if site.write == nil {
					continue
				}
				// Share a short bounded constructor across claims for its
				// different fields. Larger writes keep the precise key/value.
				write := s.httpRouteExcerpt(f, site.owner, witnessMaxLines, witnessMaxExcerpt, true)
				if write.ID == "" {
					write = s.httpRouteExcerpt(f, site.write, witnessMaxLines, witnessMaxExcerpt, true)
				}
				if write.ID == "" {
					continue
				}
				decisions = appendDistinctGoJSON(decisions, write)
				if len(decisions) > 2 {
					return goJSONTrace{}
				}
				// Prefer the leaf tag and value definition, then the outer
				// tag and return when there is room for both.
				leaf := chain[len(chain)-1]
				mappings = appendDistinctGoJSON(mappings, s.goJSONMappingWindow(leaf))
				values = appendDistinctGoJSON(values, s.goJSONValueWindows(idx, f, site.value, q.scope)...)
				for _, field := range chain[:len(chain)-1] {
					outer = appendDistinctGoJSON(outer, s.httpRouteExcerpt(field.file, field.decl, witnessMaxLines, witnessMaxExcerpt, true))
				}
				returns = appendDistinctGoJSON(returns, s.httpRouteExcerpt(f, site.ret, witnessMaxLines, witnessMaxExcerpt, true))
			}
		}
	}
	if len(decisions) == 0 {
		if len(q.goJSONFields) != 1 {
			return goJSONTrace{}
		}
		for name, chains := range roots {
			windows := s.goJSONValidationWindows(idx, name, chains[0], q.scope)
			return goJSONTrace{core: windows, excerpts: windows, guard: true}
		}
	}
	files := make(map[string]*httpRouteFile, len(idx.files))
	for _, f := range idx.files {
		files[f.entry.Path] = f
	}
	core := s.compactGoJSONWindows(slices.Concat(decisions, mappings, values), files)
	return goJSONTrace{core: core, excerpts: s.compactGoJSONWindows(slices.Concat(core, returns, outer), files)}
}

// When the only local operation on a wire field is a guard, show the
// typed check and its rejection path without inferring what produced it.
func (s *roleStage) goJSONValidationWindows(idx *httpRouteIndex, name httpRouteName, chain []goJSONField, scope string) []record.Excerpt {
	var seen record.Excerpt
	var names []string
	for _, f := range chain {
		names = append(names, f.name)
	}
	for _, f := range idx.files {
		if f.pkg != name.pkg || !f.eligible || idx.incomplete[path.Dir(f.entry.Path)] || !record.ScopeUnder(f.entry.Path, scope) {
			continue
		}
		for _, d := range f.parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			result := httpRouteIdent(fn.Type.Results.List[0].Type)
			if result == nil || result.Name != "error" || result.Obj != nil || idx.bindings[httpRouteName{f.pkg, "error"}] != 0 {
				continue
			}
			for _, formal := range fn.Type.Params.List {
				if f.namedType(formal.Type) != name || len(formal.Names) != 1 || formal.Names[0].Obj == nil {
					continue
				}
				root := formal.Names[0].Obj
				for _, stmt := range fn.Body.List {
					if !goJSONNoOverwrite(stmt, root, names) {
						break
					}
					guard, ok := stmt.(*ast.IfStmt)
					if !ok || guard.Init != nil || len(guard.Body.List) != 1 || !goJSONConditionReads(guard.Cond, root, names) {
						continue
					}
					ret, ok := guard.Body.List[0].(*ast.ReturnStmt)
					if !ok || len(ret.Results) != 1 || zeroConfigValue(ret.Results[0]) {
						continue
					}
					window := s.httpRouteExcerptLines(f, f.line(fn.Pos()), f.line(guard.Body.End()), witnessMaxLines, witnessMaxExcerpt, true)
					if window.ID == "" {
						continue
					}
					if seen.ID != "" {
						return nil
					}
					seen = window
				}
			}
		}
	}
	if seen.ID == "" {
		return nil
	}
	list := []record.Excerpt{seen}
	for _, field := range chain {
		list = appendDistinctGoJSON(list, s.httpRouteExcerpt(field.file, field.decl, witnessMaxLines, witnessMaxExcerpt, true))
	}
	return list
}

func goJSONConditionReads(expr ast.Expr, root *ast.Object, names []string) bool {
	if chain, bound := goJSONSelectors(expr, root); bound {
		return slices.Equal(chain, names)
	}
	switch x := ast.Unparen(expr).(type) {
	case *ast.BinaryExpr:
		switch x.Op {
		case token.LAND, token.LOR, token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return goJSONConditionReads(x.X, root, names) || goJSONConditionReads(x.Y, root, names)
		}
	case *ast.UnaryExpr:
		return x.Op == token.NOT && goJSONConditionReads(x.X, root, names)
	}
	return false
}

func goJSONFieldChain(idx *httpRouteIndex, name httpRouteName, parts []string, scope string) []goJSONField {
	var chain []goJSONField
	for _, part := range parts {
		def := idx.typeDecl(name)
		st, ok := def.decl.(*ast.StructType)
		if !ok || !record.ScopeUnder(def.file.entry.Path, scope) {
			return nil
		}
		var selected goJSONField
		for _, f := range st.Fields.List {
			// Promoted fields and custom codecs need type checking and
			// serialization semantics beyond a direct tag binding.
			if len(f.Names) == 0 {
				return nil
			}
			if len(f.Names) != 1 || !f.Names[0].IsExported() || f.Tag == nil {
				continue
			}
			raw, err := strconv.Unquote(f.Tag.Value)
			tag, _, _ := strings.Cut(reflect.StructTag(raw).Get("json"), ",")
			if err != nil || tag != part {
				continue
			}
			if selected.decl != nil {
				return nil
			}
			selected = goJSONField{def.file, f, st, f.Names[0].Name}
		}
		if selected.decl == nil || len(idx.functions[httpRouteMethod{name, "MarshalJSON"}]) != 0 ||
			len(idx.functions[httpRouteMethod{name, "MarshalText"}]) != 0 {
			return nil
		}
		chain = append(chain, selected)
		name = def.file.namedType(selected.decl.Type)
	}
	return chain
}

func goJSONReturnField(f *httpRouteFile, fn *ast.FuncDecl, typ httpRouteName, chain []goJSONField) goJSONSite {
	body := fn.Body.List
	ret, ok := body[len(body)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) == 0 {
		return goJSONSite{}
	}
	if lit, ok := ast.Unparen(ret.Results[0]).(*ast.CompositeLit); ok && f.namedType(lit.Type) == typ {
		if node, value := goJSONLiteralField(f, lit, chain); node != nil {
			return goJSONSite{node, ret, ret, value}
		}
	}
	out := httpRouteIdent(ret.Results[0])
	if out == nil || out.Obj == nil || !goJSONResultObject(f, out.Obj, typ) {
		return goJSONSite{}
	}
	var names []string
	for _, field := range chain {
		names = append(names, field.name)
	}
	for i := len(body) - 2; i >= 0; i-- {
		stmt := body[i]
		a, ok := stmt.(*ast.AssignStmt)
		if ok && len(a.Lhs) == 1 && len(a.Rhs) == 1 && (a.Tok == token.DEFINE || a.Tok == token.ASSIGN) {
			left, bound := goJSONSelectors(a.Lhs[0], out.Obj)
			if bound && len(left) <= len(names) && slices.Equal(left, names[:len(left)]) {
				if len(left) == len(names) {
					return goJSONSite{a, a, ret, a.Rhs[0]}
				}
				lit, ok := ast.Unparen(a.Rhs[0]).(*ast.CompositeLit)
				if !ok || len(left) != 0 && f.namedType(lit.Type) != chain[len(left)-1].file.namedType(chain[len(left)-1].decl.Type) ||
					len(left) == 0 && f.namedType(lit.Type) != typ {
					return goJSONSite{}
				}
				if node, value := goJSONLiteralField(f, lit, chain[len(left):]); node != nil {
					return goJSONSite{node, a, ret, value}
				}
				return goJSONSite{}
			}
		}
		// A later conditional overwrite cannot safely be paired with
		// an earlier initializer as the selected returned value.
		if !goJSONNoOverwrite(stmt, out.Obj, names) {
			return goJSONSite{}
		}
	}
	return goJSONSite{}
}

func goJSONResultObject(f *httpRouteFile, obj *ast.Object, typ httpRouteName) bool {
	switch d := obj.Decl.(type) {
	case *ast.Field:
		return f.namedType(d.Type) == typ
	case *ast.AssignStmt:
		if len(d.Rhs) != 1 || len(d.Lhs) != 1 {
			return false
		}
		switch rhs := ast.Unparen(d.Rhs[0]).(type) {
		case *ast.CompositeLit:
			return f.namedType(rhs.Type) == typ
		case *ast.Ident:
			if rhs.Obj != nil {
				param, ok := rhs.Obj.Decl.(*ast.Field)
				return ok && f.namedType(param.Type) == typ
			}
		}
	}
	return false
}

func goJSONSelectors(expr ast.Expr, root *ast.Object) ([]string, bool) {
	var names []string
	for {
		if id := httpRouteIdent(expr); id != nil {
			slices.Reverse(names)
			return names, id.Obj == root
		}
		sel, ok := ast.Unparen(expr).(*ast.SelectorExpr)
		if !ok {
			return nil, false
		}
		names = append(names, sel.Sel.Name)
		expr = sel.X
	}
}

func goJSONNoOverwrite(stmt ast.Stmt, root *ast.Object, names []string) bool {
	overwrites := func(expr ast.Expr) bool {
		left, bound := goJSONSelectors(expr, root)
		common := min(len(left), len(names))
		return bound && slices.Equal(left[:common], names[:common])
	}
	stable := true
	ast.Inspect(stmt, func(n ast.Node) bool {
		if !stable {
			return false
		}
		switch x := n.(type) {
		case *ast.AssignStmt:
			stable = !slices.ContainsFunc(x.Lhs, overwrites)
		case *ast.IncDecStmt:
			stable = !overwrites(x.X)
		}
		return stable
	})
	return stable
}

func goJSONLiteralField(f *httpRouteFile, lit *ast.CompositeLit, fields []goJSONField) (ast.Node, ast.Expr) {
	field := fields[0]
	var selected *ast.KeyValueExpr
	for _, child := range lit.Elts {
		pair, ok := child.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if id := httpRouteIdent(pair.Key); id != nil && id.Name == field.name {
			if selected != nil {
				return nil, nil
			}
			selected = pair
		}
	}
	if selected == nil {
		return nil, nil
	}
	if len(fields) == 1 {
		return selected, selected.Value
	}
	if next, ok := ast.Unparen(selected.Value).(*ast.CompositeLit); ok && f.namedType(next.Type) == field.file.namedType(field.decl.Type) {
		if inner, value := goJSONLiteralField(f, next, fields[1:]); inner != nil {
			return selected, value
		}
	}
	return nil, nil
}

func (s *roleStage) goJSONValueWindows(idx *httpRouteIndex, f *httpRouteFile, expr ast.Expr, scope string) []record.Excerpt {
	switch x := ast.Unparen(expr).(type) {
	case *ast.Ident:
		name := httpRouteName{f.pkg, x.Name}
		if idx.bindings[name] != 1 || x.Obj != nil && f.parsed.Scope.Lookup(x.Name) != x.Obj {
			return nil
		}
		for _, owner := range idx.files {
			if owner.pkg != f.pkg || !owner.eligible || !record.ScopeUnder(owner.entry.Path, scope) {
				continue
			}
			binding := owner.parsed.Scope.Lookup(x.Name)
			if binding == nil || binding.Kind != ast.Con {
				continue
			}
			spec, ok := binding.Decl.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 || !goDefaultValue(spec.Values[0]) {
				return nil
			}
			return []record.Excerpt{s.httpRouteExcerpt(owner, spec, witnessMaxLines, witnessMaxExcerpt, true)}
		}
	case *ast.CallExpr:
		callee := httpRouteIdent(x.Fun)
		if callee == nil || callee.Obj != nil && f.parsed.Scope.Lookup(callee.Name) != callee.Obj {
			return nil
		}
		def := idx.function(httpRouteMethod{httpRouteName{pkg: f.pkg}, callee.Name})
		if def.decl == nil || !record.ScopeUnder(def.file.entry.Path, scope) {
			return nil
		}
		start, end := def.file.line(def.decl.Pos()), def.file.line(def.decl.End())
		if window := s.httpRouteExcerpt(def.file, def.decl, witnessMaxLines, witnessMaxExcerpt, true); window.ID != "" {
			return []record.Excerpt{window}
		}
		// Keep a longer function only as two complete contiguous windows.
		// A tail return alone may miss the inputs that decide its value.
		if end-start >= 2*witnessMaxLines || record.JSONLen(strings.Join(def.file.lines[start-1:end], "\n")) > roleMaxCodeFile {
			return nil
		}
		for cut := min(start+witnessMaxLines-1, end-1); cut >= max(start, end-witnessMaxLines); cut-- {
			a := s.httpRouteExcerptLines(def.file, start, cut, witnessMaxLines, witnessMaxExcerpt, true)
			b := s.httpRouteExcerptLines(def.file, cut+1, end, witnessMaxLines, witnessMaxExcerpt, true)
			if a.ID != "" && b.ID != "" {
				return []record.Excerpt{a, b}
			}
		}
	}
	return nil
}

func appendDistinctGoJSON(out []record.Excerpt, add ...record.Excerpt) []record.Excerpt {
	for _, x := range add {
		if x.ID != "" && !slices.ContainsFunc(out, func(y record.Excerpt) bool { return x.ID == y.ID }) {
			out = append(out, x)
		}
	}
	return out
}

func (s *roleStage) goJSONMappingWindow(field goJSONField) record.Excerpt {
	// A short named type can be shared by several contracts rather than
	// occupying one request slot for each adjacent tag.
	if field.owner.Fields.NumFields() <= 6 {
		if x := s.httpRouteExcerpt(field.file, field.owner, witnessMaxLines, witnessMaxExcerpt, true); x.ID != "" {
			return x
		}
	}
	return s.httpRouteExcerpt(field.file, field.decl, witnessMaxLines, witnessMaxExcerpt, true)
}

func (s *roleStage) compactGoJSONWindows(list []record.Excerpt, files map[string]*httpRouteFile) []record.Excerpt {
	out := make([]record.Excerpt, 0, min(len(list), roleMaxCodeFiles-2))
	for _, next := range list {
		if next.ID == "" {
			continue
		}
		for i, prev := range out {
			if next.Path != prev.Path || next.Start > prev.End+3 || prev.Start > next.End+3 {
				continue
			}
			if prev.Start <= next.Start && next.End <= prev.End {
				next.ID = ""
				break
			}
			// Merge nearby related lines from the already verified file,
			// while keeping the priority and the same total file budget.
			f := files[next.Path]
			start, end := min(prev.Start, next.Start), max(prev.End, next.End)
			x := s.httpRouteExcerptLines(f, start, end, witnessMaxLines, witnessMaxExcerpt, true)
			if x.ID == "" {
				continue
			}
			others := append(slices.Clone(out[:i]), out[i+1:]...)
			if _, ok := wholeTraceWindows(others, []record.Excerpt{x}, roleMaxCodeBytes, roleMaxCodeFiles-2); ok {
				out[i] = x
				next.ID = ""
				break
			}
		}
		if next.ID != "" {
			if windows, ok := wholeTraceWindows(out, []record.Excerpt{next}, roleMaxCodeBytes, roleMaxCodeFiles-2); ok {
				out = append(out, windows...)
			}
		}
	}
	return out
}
