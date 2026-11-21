package review

import (
	"context"
	"go/ast"
	"go/token"
	"go/types"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Leave room for actual tests even when a package contains many production
// files. These reads use the same file and byte counters as the caller scan.
func scanMethodFiles(files []source.Entry, anchors map[*ast.Ident]string, load func(source.Entry) (*expansionSource, error)) error {
	names, dirs := map[string]bool{}, map[string]bool{}
	for _, p := range anchors {
		names[strings.TrimSuffix(p, ".go")+"_test.go"] = true
		dirs[path.Dir(p)] = true
	}
	remainingBytes := int64(maxScanBytes / 4)
	remainingFiles := maxScanFiles / 4
	for _, sameFile := range []bool{true, false} {
		for _, file := range files {
			if remainingFiles == 0 || !dirs[path.Dir(file.Path)] || !strings.HasSuffix(file.Path, "_test.go") ||
				names[file.Path] != sameFile || *file.Size > remainingBytes {
				continue
			}
			if _, err := load(file); err != nil {
				return err
			}
			remainingFiles--
			remainingBytes -= *file.Size
		}
	}
	for _, file := range files {
		if dirs[path.Dir(file.Path)] {
			if _, err := load(file); err != nil {
				return err
			}
		}
	}
	return nil
}

type methodPackage struct{ dir, name string }

// methodReceiverName extracts the declared base type, including generic
// receivers. The type checker, rather than this name, resolves each call.
func methodReceiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	expr := ast.Unparen(fn.Recv.List[0].Type)
	if pointer, ok := expr.(*ast.StarExpr); ok {
		expr = ast.Unparen(pointer.X)
	}
	switch indexed := expr.(type) {
	case *ast.IndexExpr:
		expr = indexed.X
	case *ast.IndexListExpr:
		expr = indexed.X
	}
	if name, ok := expr.(*ast.Ident); ok {
		return name.Name
	}
	return ""
}

// methodCallers uses concrete selections resolved from verified source in the
// same package. No compiler, importer, build, or repository code is run.
func methodCallers(ctx context.Context, positions *token.FileSet, files []source.Entry, parsed map[string]*expansionSource,
	anchors map[*ast.Ident]string, catalog []Excerpt) ([]Excerpt, int, error) {
	dirs := map[methodPackage]bool{}
	for _, p := range anchors {
		dirs[methodPackage{path.Dir(p), parsed[p].file.Name.Name}] = true
	}
	groups := map[methodPackage][]source.Entry{}
	var order []methodPackage
	for _, file := range files {
		if p := parsed[file.Path]; p != nil {
			key := methodPackage{path.Dir(file.Path), p.file.Name.Name}
			if !dirs[key] {
				continue
			}
			if len(groups[key]) == 0 {
				order = append(order, key)
			}
			groups[key] = append(groups[key], file)
		}
	}

	type match struct {
		excerpt Excerpt
		score   int
	}
	var callers, tests []match
	hits := 0
	remember := func(lane *[]match, result match) {
		a := result.excerpt
		overlaps := func(m match) bool {
			b := m.excerpt
			return a.Path == b.Path && a.Start <= b.End && b.Start <= a.End
		}
		if slices.ContainsFunc(*lane, func(m match) bool { return overlaps(m) && m.score >= result.score }) ||
			(len(*lane) == maxExpansion && (*lane)[len(*lane)-1].score >= result.score) {
			return
		}
		*lane = slices.DeleteFunc(*lane, overlaps)
		*lane = append(*lane, result)
		slices.SortStableFunc(*lane, func(a, b match) int { return b.score - a.score })
		*lane = (*lane)[:min(len(*lane), maxExpansion)]
	}
	for _, key := range order {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		// Count competing declarations before discarding conditional files.
		// This prevents choosing an arbitrary implementation of a method.
		declarations := map[string]int{}
		var inputs []*ast.File
		inputSet := map[*ast.File]bool{}
		for _, e := range groups[key] {
			p := parsed[e.Path]
			for _, d := range p.file.Decls {
				if f, ok := d.(*ast.FuncDecl); ok {
					if base := methodReceiverName(f); base != "" {
						declarations[base+"."+f.Name.Name]++
					}
				}
			}
			f := contractGoFile{entry: e, parsed: p.file}
			if !ast.IsGenerated(p.file) && !record.GeneratedHeader([]byte(strings.Join(p.lines[:min(len(p.lines), 10)], "\n"))) && f.unconditional() {
				inputs = append(inputs, p.file)
				inputSet[p.file] = true
			}
		}
		if len(inputs) == 0 {
			continue
		}
		info := types.Info{Defs: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
		config := types.Config{Error: func(error) {}}
		_, _ = config.Check("review/"+key.dir, positions, inputs, &info)
		symbols := map[*types.Func]bool{}
		for _, f := range inputs {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || anchors[fn.Name] == "" {
					continue
				}
				name := methodReceiverName(fn) + "." + fn.Name.Name
				if obj, ok := info.Defs[fn.Name].(*types.Func); ok && declarations[name] == 1 && record.ValidEvidenceSymbol(fn.Name.Name) {
					symbols[obj] = true
				}
			}
		}
		for _, e := range groups[key] {
			if err := ctx.Err(); err != nil {
				return nil, 0, err
			}
			p := parsed[e.Path]
			if !inputSet[p.file] {
				continue
			}
			test := record.SourceKind(e.Path) == "test"
			for _, d := range p.file.Decls {
				caller, ok := d.(*ast.FuncDecl)
				if !ok || caller.Body == nil || (test && verificationTestParameter(p.file, caller) == nil) {
					continue
				}
				ast.PreorderStack(caller.Body, nil, func(n ast.Node, stack []ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
					if !ok {
						return true
					}
					selection := info.Selections[selector]
					if selection == nil || types.IsInterface(selection.Recv()) {
						return true
					}
					method, ok := selection.Obj().(*types.Func)
					if !ok || !symbols[method.Origin()] || method == info.Defs[caller.Name] {
						return true
					}
					first, last, score := contractGoDecision(call, stack)
					if test {
						first, last = verificationMethodAssertion(p.file, caller, call, stack)
						if !first.IsValid() {
							return true
						}
						score = 3
					} else if score == 0 {
						first, last = call.Pos(), call.End()
					}
					hits++
					line := func(pos token.Pos) int { return positions.PositionFor(pos, false).Line }
					callLine := line(selector.Sel.Pos())
					firstLine, lastLine := line(first), line(last)
					if slices.ContainsFunc(catalog, func(x Excerpt) bool {
						return x.Path == e.Path && x.Start <= firstLine && lastLine <= x.End
					}) {
						return true
					}
					w, ok := methodExpansionWindow(p.lines, line(caller.Pos()), line(caller.End()),
						min(callLine, firstLine), max(callLine, lastLine))
					if !ok {
						return true
					}
					kind, origin := "code", "caller"
					if test {
						kind, origin = "test", "test"
					}
					text := strings.Join(p.lines[w.lo:w.hi+1], "\n")
					x := Excerpt{Path: e.Path, SHA256: e.SHA256, Start: w.lo + 1, End: w.hi + 1,
						Kind: kind, Origin: origin, Symbol: method.Name(), Text: text}
					if test {
						remember(&tests, match{x, score})
					} else {
						remember(&callers, match{x, score})
					}
					return true
				})
			}
		}
	}
	out := make([]Excerpt, 0, len(callers)+len(tests))
	for i := 0; i < max(len(callers), len(tests)); i++ {
		if i < len(tests) {
			out = append(out, tests[i].excerpt)
		}
		if i < len(callers) {
			out = append(out, callers[i].excerpt)
		}
	}
	return out, hits, nil
}

func methodExpansionWindow(lines []string, declFirst, declLast, first, last int) (window, bool) {
	if first < declFirst || last > declLast || len(lines) < declLast || last-first >= maxWindowLines {
		return window{}, false
	}
	size := 0
	for _, line := range lines[first-1 : last] {
		size += len(line) + 1
	}
	if size > maxWindowBytes {
		return window{}, false
	}
	w := window{first - 1, last - 1}
	for {
		prev := w
		if w.lo >= declFirst && w.hi-w.lo+1 < maxWindowLines && size+len(lines[w.lo-1])+1 <= maxWindowBytes {
			w.lo--
			size += len(lines[w.lo]) + 1
		}
		if w.hi+1 < declLast && w.hi-w.lo+1 < maxWindowLines && size+len(lines[w.hi+1])+1 <= maxWindowBytes {
			w.hi++
			size += len(lines[w.hi]) + 1
		}
		if prev == w {
			return w, true
		}
	}
}

func verificationTestParameter(file *ast.File, fn *ast.FuncDecl) *ast.Ident {
	suffix, test := strings.CutPrefix(fn.Name.Name, "Test")
	first, _ := utf8.DecodeRuneInString(suffix)
	if !test || unicode.IsLower(first) || fn.Recv != nil || fn.Type.Results != nil ||
		len(fn.Type.Params.List) != 1 || len(fn.Type.Params.List[0].Names) != 1 {
		return nil
	}
	param := fn.Type.Params.List[0]
	ptr, ok := param.Type.(*ast.StarExpr)
	if !ok {
		return nil
	}
	qual, ok := ptr.X.(*ast.SelectorExpr)
	if !ok || qual.Sel.Name != "T" {
		return nil
	}
	pkg, ok := qual.X.(*ast.Ident)
	if !ok {
		return nil
	}
	for _, decl := range file.Imports {
		imported, _ := strconv.Unquote(decl.Path.Value)
		if imported == "testing" && (decl.Name == nil && pkg.Name == "testing" || decl.Name != nil && decl.Name.Name == pkg.Name) {
			return param.Names[0]
		}
	}
	return nil
}

func verificationMethodAssertion(file *ast.File, fn *ast.FuncDecl, call *ast.CallExpr, stack []ast.Node) (token.Pos, token.Pos) {
	param := verificationTestParameter(file, fn)
	child := ast.Node(call)
	for i := len(stack) - 1; i >= 0; i-- {
		parent := stack[i]
		if branch, ok := parent.(*ast.IfStmt); ok {
			assign, assigned := branch.Init.(*ast.AssignStmt)
			if (child == branch.Cond || assigned && child == assign && goAssignmentUsed(assign, branch.Cond)) &&
				verificationTestingFailure(branch.Body, param) {
				return branch.Pos(), branch.Body.End()
			}
		}
		if block, ok := parent.(*ast.BlockStmt); ok {
			if assign, ok := child.(*ast.AssignStmt); ok {
				next := slices.Index(block.List, ast.Stmt(assign)) + 1
				if next > 0 && next < len(block.List) {
					if branch, ok := block.List[next].(*ast.IfStmt); ok && goAssignmentUsed(assign, branch.Cond) &&
						verificationTestingFailure(branch.Body, param) {
						return assign.Pos(), branch.Body.End()
					}
				}
			}
		}
		child = parent
	}
	return token.NoPos, token.NoPos
}

func verificationTestingFailure(block *ast.BlockStmt, param *ast.Ident) bool {
	for _, stmt := range block.List {
		expr, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := expr.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		receiver, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		test, ok := receiver.X.(*ast.Ident)
		if ok && test.Obj == param.Obj && slices.Contains([]string{"Error", "Errorf", "Fail", "FailNow", "Fatal", "Fatalf"}, receiver.Sel.Name) {
			return true
		}
	}
	return false
}
