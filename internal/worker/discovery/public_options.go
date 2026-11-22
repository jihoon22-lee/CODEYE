package discovery

import (
	"bytes"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const maxOptionFiles = 192
const optionReadBytes = 4 << 20
const maxOptionFileBytes = 128 << 10

type publicOption struct {
	kind, name, fallback string
	start, end           int
}

func publicOptions(p string, b []byte) []publicOption {
	switch path.Ext(p) {
	case ".go":
		return goPublicOptions(p, b)
	case ".java", ".py", ".js", ".ts", ".mjs", ".mts", ".c", ".cc", ".cpp", ".cxx", ".c++", ".rs":
		return tokenPublicOptions(p, b)
	}
	return nil
}

func optionSource(p string) bool {
	if record.SourceKind(p) != "code" || record.DefaultSecondary.Match(p) != "" {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") || slices.Contains([]string{"vendor", "node_modules", "third_party", "target", "dist", "build", "fixtures", "testdata", "examples", "tests", "test"}, part) {
			return false
		}
	}
	switch path.Ext(p) {
	case ".go", ".java", ".py", ".js", ".ts", ".mjs", ".mts", ".c", ".cc", ".cpp", ".cxx", ".c++", ".rs":
		return true
	}
	return false
}

func documentationGaps(m source.Manifest, scope envdoc.Scope, modules []envdoc.Module, inventory envdoc.Documents,
	documentBodies map[string][]byte, read readFunc) ([]envdoc.DocumentationGap, error) {
	if len(inventory.Items) == 0 {
		return nil, nil
	}
	type guideSet struct {
		paths []string
		terms []map[string]bool
	}
	byScope := map[string]guideSet{}
	indexed := map[string]map[string]bool{}
	guides := func(root string) guideSet {
		if files, ok := byScope[root]; ok {
			return files
		}
		var set guideSet
		for _, d := range inventory.Items {
			if !envdoc.UserGuide(d, root) {
				continue
			}
			b, read := documentBodies[d.Path]
			if d.Status != envdoc.DocSelected || !read || len(set.paths) == envdoc.MaxGapDocuments {
				set = guideSet{}
				break
			}
			words := indexed[d.Path]
			if words == nil {
				words = map[string]bool{}
				for _, word := range docOptionWord.FindAll(b, -1) {
					words[optionSpelling(string(word))] = true
				}
				indexed[d.Path] = words
			}
			set.paths, set.terms = append(set.paths, d.Path), append(set.terms, words)
		}
		byScope[root] = set
		return set
	}
	type foundGap struct {
		gap   envdoc.DocumentationGap
		count int
	}
	var order [][3]string
	found := map[[3]string]foundGap{}
	readFiles, bytesLeft := 0, int64(optionReadBytes)
	for _, e := range m.Entries {
		if e.Kind != "file" || e.ExcludedReason != "" || e.Size == nil || e.Encoding != "utf-8" || !pathInScope(e.Path, scope) || !optionSource(e.Path) {
			continue
		}
		root := envdoc.DocumentationScope(e.Path, modules)
		search := guides(root)
		if len(search.paths) == 0 || *e.Size > maxOptionFileBytes {
			continue
		}
		if readFiles == maxOptionFiles || bytesLeft < *e.Size {
			break
		}
		readFiles++
		bytesLeft -= *e.Size
		body, err := read(e)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 || record.Sensitive(body) || record.GeneratedHeader(body) {
			continue
		}
		for _, option := range publicOptions(e.Path, body) {
			if !envdoc.ValidDocumentationSymbol(option.kind, option.name) || option.start < 1 || option.end-option.start >= 25 ||
				docMentions(search.terms, option.name) {
				continue
			}
			key := [3]string{root, option.kind, option.name}
			tally, seen := found[key]
			if !seen {
				order = append(order, key)
				tally.gap = envdoc.DocumentationGap{Kind: option.kind, Symbol: option.name, Default: safeOptionDefault(option), Scope: root,
					Source: envdoc.Location{Path: e.Path, SHA256: e.SHA256, StartLine: option.start, EndLine: option.end}, Searched: search.paths}
			}
			tally.count++
			found[key] = tally
		}
	}
	var out []envdoc.DocumentationGap
	for _, key := range order {
		if gap := found[key]; gap.count == 1 {
			out = append(out, gap.gap)
			if len(out) == envdoc.MaxDocumentationGaps {
				break
			}
		}
	}
	return out, nil
}

func safeOptionDefault(option publicOption) string {
	name := strings.ToLower(option.name)
	if !envdoc.ValidDocumentationDefault(option.fallback) || strings.Contains(name, "secret") || strings.Contains(name, "token") ||
		strings.Contains(name, "password") || strings.Contains(name, "credential") || strings.HasSuffix(name, "key") {
		return ""
	}
	return option.fallback
}

var docOptionWord = regexp.MustCompile(`[A-Za-z0-9_]+(?:-[A-Za-z0-9_]+)*`)

func optionSpelling(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(name, "--")), "_", "-")
}

func docMentions(documents []map[string]bool, name string) bool {
	word := optionSpelling(name)
	return slices.ContainsFunc(documents, func(words map[string]bool) bool { return words[word] })
}

func goPublicOptions(p string, b []byte) []publicOption {
	filename := build.Context{OpenFile: func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil }}
	if unconditional, _ := filename.MatchFile("", path.Base(p)); !unconditional {
		return nil
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, p, b, parser.DeclarationErrors|parser.ParseComments)
	if err != nil || file.Name.Name != "main" || ast.IsGenerated(file) || bytes.Contains(b, []byte("//go:build")) || bytes.Contains(b, []byte("// +build")) {
		return nil
	}
	imports := map[string]string{}
	for _, declaration := range file.Imports {
		pkg, _ := strconv.Unquote(declaration.Path.Value)
		if pkg != "flag" && pkg != "github.com/spf13/pflag" {
			continue
		}
		name := path.Base(pkg)
		if declaration.Name != nil {
			name = declaration.Name.Name
		}
		imports[name] = pkg
	}
	// References to CommandLine allow the imported global parser to be replaced
	// or reconfigured outside the simple package-level registration contract.
	guarded := false
	ast.Inspect(file, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "CommandLine" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Obj == nil && imports[id.Name] != "" {
				guarded = true
			}
		}
		return !guarded
	})
	if guarded {
		return nil
	}
	var calls []ast.Expr
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil || d.Name.Name != "main" || d.Body == nil {
				continue
			}
			for _, stmt := range d.Body.List {
				switch s := stmt.(type) {
				case *ast.ExprStmt:
					calls = append(calls, s.X)
				case *ast.AssignStmt:
					calls = append(calls, s.Rhs...)
				}
			}
		case *ast.GenDecl:
			if d.Tok == token.VAR {
				for _, spec := range d.Specs {
					calls = append(calls, spec.(*ast.ValueSpec).Values...)
				}
			}
		}
	}
	parsed := map[string]token.Pos{}
	var out []struct {
		publicOption
		pkg string
		pos token.Pos
	}
	for _, expr := range calls {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			continue
		}
		s, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		id, ok := s.X.(*ast.Ident)
		if !ok || id.Obj != nil || imports[id.Name] == "" {
			continue
		}
		if s.Sel.Name == "Parse" && len(call.Args) == 0 {
			parsed[id.Name] = max(parsed[id.Name], call.Pos())
			continue
		}
		switch s.Sel.Name {
		case "String", "Bool", "Int", "Int64", "Uint", "Uint64", "Float64":
		default:
			continue
		}
		if len(call.Args) != 3 {
			continue
		}
		name := goOptionLiteral(call.Args[0])
		if name == "" {
			continue
		}
		op := publicOption{kind: envdoc.DocGapOption, name: "--" + name, fallback: goOptionLiteral(call.Args[1]),
			start: positions.PositionFor(call.Pos(), false).Line, end: positions.PositionFor(call.End(), false).Line}
		out = append(out, struct {
			publicOption
			pkg string
			pos token.Pos
		}{op, id.Name, call.Pos()})
	}
	var options []publicOption
	for _, option := range out {
		if parsed[option.pkg] > option.pos {
			options = append(options, option.publicOption)
		}
	}
	return options
}

func goOptionLiteral(x ast.Expr) string {
	switch x := x.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			v, _ := strconv.Unquote(x.Value)
			return v
		}
		if x.Kind == token.INT || x.Kind == token.FLOAT {
			if x.Kind == token.INT {
				if val, err := strconv.ParseInt(x.Value, 0, 64); err == nil {
					return strconv.FormatInt(val, 10)
				}
			}
			if val, err := strconv.ParseFloat(x.Value, 64); err == nil {
				return strconv.FormatFloat(val, 'g', -1, 64)
			}
		}
	case *ast.Ident:
		if x.Obj == nil && (x.Name == "true" || x.Name == "false") {
			return x.Name
		}
	}
	return ""
}
