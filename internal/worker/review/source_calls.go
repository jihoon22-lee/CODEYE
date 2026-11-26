package review

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// A sourceCall is a source binding, not an execution result. Both endpoints
// use inclusive, physical line ranges within the fixed snapshot.
type sourceCall struct {
	caller, callee sourceCallLocation
	symbol         string
	calleeExtent   sourceCallLocation
	// local is the name a Python, JavaScript or Rust call site uses for the
	// imported symbol (an import alias makes it differ); empty elsewhere.
	local string
}

type sourceCallLocation struct {
	path       string
	start, end int
}

type sourceCallCatalog struct {
	files   map[string]source.Entry
	ordered []string
	dirs    map[string][]string
}

type sourceCalls struct {
	*sourceCallCatalog
	read    func(source.Entry) (string, bool, error)
	goFiles map[string]contractGoFile
	goMods  map[string]string
}

func manifestCallCatalog(manifest source.Manifest) *sourceCallCatalog {
	files := make(map[string]source.Entry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		files[entry.Path] = entry
	}
	return newSourceCallCatalog(files)
}

func newSourceCallCatalog(files map[string]source.Entry) *sourceCallCatalog {
	ordered := slices.Sorted(maps.Keys(files))
	dirs := make(map[string][]string)
	for _, p := range ordered {
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			dirs[path.Dir(p)] = append(dirs[path.Dir(p)], p)
		}
	}
	return &sourceCallCatalog{files: files, ordered: ordered, dirs: dirs}
}

func (c *sourceCallCatalog) calls(read func(source.Entry) (string, bool, error)) *sourceCalls {
	return &sourceCalls{sourceCallCatalog: c, read: read, goFiles: map[string]contractGoFile{}, goMods: map[string]string{}}
}

func (s *roleStage) callCatalog() *sourceCallCatalog {
	if index := s.sourceIndex(); index != nil {
		return index.callCatalog()
	}
	return manifestCallCatalog(s.j.Snapshot.Manifest)
}

// Module identity can be read without adding a configuration excerpt.
func callPathMetadata(p string) bool {
	return path.Base(p) == "go.mod" || path.Base(p) == "Cargo.toml"
}

func (c *sourceCalls) text(p string) (string, bool, error) {
	e, ok := c.files[p]
	if !ok || e.Kind != "file" || e.Size == nil || e.SHA256 == "" || e.ExcludedReason != "" {
		return "", false, nil
	}
	text, ok, err := c.read(e)
	return text, ok && !record.GeneratedHeader([]byte(text)), err
}

func (c *sourceCalls) from(p, scope string) ([]sourceCall, error) {
	if !record.ScopeUnder(p, scope) {
		return nil, nil
	}
	switch path.Ext(p) {
	case ".go":
		return c.goCalls(p, scope)
	case ".java":
		return c.javaCalls(p, scope)
	case ".c", ".cc", ".cpp", ".cxx", ".c++":
		return c.cCalls(p, scope)
	case ".rs":
		return c.rustCalls(p, scope)
	}
	lang := callLanguage(p)
	if lang == "" {
		return nil, nil
	}
	content, ok, err := c.text(p)
	if !ok || err != nil {
		return nil, err
	}
	var out []sourceCall
	python := lang == "python"
	for _, site := range importedCalls(callTokens(content, python), python) {
		callee, symbol, err := c.scriptDefinition(p, scope, site.module, site.symbol, python)
		if err != nil {
			return nil, err
		}
		if callee.start != 0 {
			out = append(out, sourceCall{caller: sourceCallLocation{p, site.line, site.end}, callee: callee, symbol: symbol, local: site.local})
		}
	}
	return out, nil
}

func (c *sourceCalls) scriptDefinition(caller, scope, module, symbol string, python bool) (sourceCallLocation, string, error) {
	seen := map[string]bool{caller: true}
	for range 4 {
		p := directModule(c.files, caller, module, python)
		if p == "" || seen[p] || !record.ScopeUnder(p, scope) {
			break
		}
		seen[p] = true
		text, ok, err := c.text(p)
		if !ok || err != nil {
			return sourceCallLocation{}, "", err
		}
		tokens := callTokens(text, python)
		if first, last := directDefinition(tokens, symbol, python); first > 0 {
			return sourceCallLocation{p, first, last}, symbol, nil
		}
		binding, ok := scriptReexport(tokens, symbol, python)
		if !ok {
			break
		}
		caller, module, symbol = p, binding.module, binding.symbol
	}
	return sourceCallLocation{}, "", nil
}

func scriptReexport(tokens []callToken, symbol string, python bool) (callBinding, bool) {
	if python {
		bindings := importedBindings(tokens, true)[symbol]
		if len(bindings) != 1 {
			return callBinding{}, false
		}
		b := bindings[0]
		for i, t := range tokens {
			// The original name in "from ... import name as name" is not a
			// second binding. Other uses may reassign or shadow the export.
			if t.name && t.text == symbol && i != b.token && !(i == b.token-2 && tokenIs(tokens, i+1, "as")) {
				return callBinding{}, false
			}
		}
		return b, true
	}
	var found callBinding
	for i, t := range tokens {
		if t.depth != 0 || !tokenIs(tokens, i, "export") || !tokenIs(tokens, i+1, "{") {
			continue
		}
		end := tokens[i+1].close
		if !tokenIs(tokens, end+1, "from") || end+2 >= len(tokens) || !tokens[end+2].quoted {
			continue
		}
		module := tokens[end+2].text
		if !strings.HasPrefix(module, "./") && !strings.HasPrefix(module, "../") {
			continue
		}
		for at := i + 2; at < end && tokenName(tokens, at); at++ {
			name := tokens[at].text
			if tokenIs(tokens, at+1, "as") && at+2 < end && tokenName(tokens, at+2) {
				at += 2
			}
			if at+1 != end && !tokenIs(tokens, at+1, ",") {
				return callBinding{}, false
			}
			if tokens[at].text == symbol {
				if found.module != "" {
					return callBinding{}, false
				}
				found = callBinding{module, name, at}
			}
			at++
		}
	}
	if found.module == "" {
		return callBinding{}, false
	}
	for i, t := range tokens {
		if t.name && t.text == symbol && i != found.token && !(i == found.token-2 && tokenIs(tokens, i+1, "as")) {
			return callBinding{}, false
		}
	}
	return found, true
}

func (c *sourceCalls) goFile(p string) (contractGoFile, error) {
	if f, ok := c.goFiles[p]; ok {
		return f, nil
	}
	text, ok, err := c.text(p)
	if !ok || err != nil {
		return contractGoFile{}, err
	}
	positions := token.NewFileSet()
	f, parseError := parser.ParseFile(positions, p, text, parser.DeclarationErrors|parser.ParseComments)
	if parseError != nil || ast.IsGenerated(f) {
		c.goFiles[p] = contractGoFile{}
		return contractGoFile{}, nil
	}
	file := contractGoFile{entry: c.files[p], positions: positions, parsed: f, lines: strings.Split(text, "\n")}
	c.goFiles[p] = file
	return file, nil
}

func (c *sourceCalls) goModule(dir string) string {
	for root := dir; ; root = path.Dir(root) {
		if _, ok := c.files[path.Join(root, "go.mod")]; ok {
			return root
		}
		if root == "." {
			return ""
		}
	}
}

func (c *sourceCalls) goModuleID(root string) (string, error) {
	if m, ok := c.goMods[root]; ok {
		return m, nil
	}
	text, ok, err := c.text(path.Join(root, "go.mod"))
	if !ok || err != nil {
		return "", err
	}
	module := ""
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "module" {
			continue
		}
		if module != "" || (len(fields) > 2 && !strings.HasPrefix(fields[2], "//")) {
			return "", nil
		}
		module = fields[1]
		if strings.HasPrefix(module, `"`) || strings.HasPrefix(module, "`") {
			module, _ = strconv.Unquote(module)
		}
		if module == "" || path.Clean(module) != module || strings.HasPrefix(module, ".") {
			return "", nil
		}
	}
	c.goMods[root] = module
	return module, nil
}

func (c *sourceCalls) goCalls(p, scope string) ([]sourceCall, error) {
	f, err := c.goFile(p)
	if f.parsed == nil || err != nil || !f.unconditional() {
		return nil, err
	}
	root := c.goModule(path.Dir(p))
	if root == "" {
		return nil, nil
	}
	mod, err := c.goModuleID(root)
	if mod == "" || err != nil {
		return nil, err
	}
	type importedPackage struct {
		dir   string
		alias bool
	}
	imports := map[string][]importedPackage{}
	visibleNames := map[string]int{}
	for _, spec := range f.parsed.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		if spec.Name != nil && spec.Name.Name == "." {
			return nil, nil
		}
		alias, name := spec.Name != nil, path.Base(imported)
		if alias {
			name = spec.Name.Name
		}
		relative, local := strings.CutPrefix(imported, mod+"/")
		if !local || relative == "" || path.Clean(relative) != relative || strings.HasPrefix(relative, "../") {
			visibleNames[name]++
			continue
		}
		dir := path.Join(root, relative)
		if name == "_" || dir == path.Dir(p) || !record.ScopeUnder(dir, scope) || c.goModule(dir) != root {
			continue
		}
		if !alias && len(c.dirs[dir]) > 0 {
			first, err := c.goFile(c.dirs[dir][0])
			if err != nil {
				return nil, err
			}
			if first.parsed == nil {
				continue
			}
			name = first.parsed.Name.Name
		}
		visibleNames[name]++
		imports[name] = append(imports[name], importedPackage{dir, alias})
	}
	if len(imports) == 0 {
		return nil, nil
	}
	var out []sourceCall
	for _, decl := range f.parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for node := range ast.Preorder(fn.Body) {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				continue
			}
			fun := ast.Unparen(call.Fun)
			usesTypes := true
			switch indexed := fun.(type) {
			case *ast.IndexExpr:
				fun = ast.Unparen(indexed.X)
			case *ast.IndexListExpr:
				fun = ast.Unparen(indexed.X)
			default:
				usesTypes = false
			}
			selector, ok := fun.(*ast.SelectorExpr)
			if !ok || !selector.Sel.IsExported() {
				continue
			}
			name, ok := selector.X.(*ast.Ident)
			if !ok || name.Obj != nil || len(imports[name.Name]) != 1 || visibleNames[name.Name] != 1 {
				continue
			}
			binding := imports[name.Name][0]
			target, extent, err := c.goDefinition(binding.dir, name.Name, selector.Sel.Name, binding.alias, usesTypes)
			if err != nil {
				return nil, err
			}
			if target.start != 0 {
				out = append(out, sourceCall{caller: sourceCallLocation{p, f.line(call.Pos()), f.line(call.End())}, callee: target, symbol: selector.Sel.Name, calleeExtent: extent})
				if len(out) >= callMaxSites {
					return nil, nil
				}
			}
		}
	}
	return out, nil
}

func (c *sourceCalls) goDefinition(dir, qualifier, name string, alias, usesTypes bool) (sourceCallLocation, sourceCallLocation, error) {
	var found, extent sourceCallLocation
	pkg := ""
	for _, p := range c.dirs[dir] {
		f, err := c.goFile(p)
		if f.parsed == nil || err != nil {
			return sourceCallLocation{}, sourceCallLocation{}, err
		}
		if pkg != "" && f.parsed.Name.Name != pkg {
			return sourceCallLocation{}, sourceCallLocation{}, nil
		}
		pkg = f.parsed.Name.Name
		obj := f.parsed.Scope.Lookup(name)
		if obj == nil {
			continue
		}
		fn, ok := obj.Decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil || usesTypes && fn.Type.TypeParams == nil || !f.unconditional() || found.start != 0 {
			return sourceCallLocation{}, sourceCallLocation{}, nil
		}
		from, to := f.line(fn.Pos()), f.line(fn.End())
		extent = sourceCallLocation{p, from, to}
		if !fitsWitness(f.lines, from, to) {
			// Long functions still have a known symbol range for reverse
			// verification. Use a bounded decisive return for forward reading.
			if len(fn.Body.List) == 0 {
				return sourceCallLocation{}, sourceCallLocation{}, nil
			}
			trailing, ok := fn.Body.List[len(fn.Body.List)-1].(*ast.ReturnStmt)
			if !ok {
				return sourceCallLocation{}, sourceCallLocation{}, nil
			}
			from, to = f.line(trailing.Pos()), f.line(trailing.End())
			if !fitsWitness(f.lines, from, to) {
				return sourceCallLocation{}, sourceCallLocation{}, nil
			}
			if wider := max(f.line(fn.Body.Pos())+1, from-3); fitsWitness(f.lines, wider, to) {
				from = wider
			}
		}
		found = sourceCallLocation{p, from, to}
	}
	if pkg != qualifier && !alias {
		return sourceCallLocation{}, sourceCallLocation{}, nil
	}
	return found, extent, nil
}
