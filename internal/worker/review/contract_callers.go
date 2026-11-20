package review

import (
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const maxContractCallers = 2

type contractGoFile struct {
	entry     source.Entry
	positions *token.FileSet
	parsed    *ast.File
	lines     []string
}

type contractGoDefinition struct {
	fn            *ast.FuncDecl
	file          contractGoFile
	unconditional bool
}

func (f contractGoFile) line(pos token.Pos) int {
	return f.positions.PositionFor(pos, false).Line
}

// contractCallerExcerpts opens new decision and return windows for package
// functions in selected Go implementation excerpts. It resolves source
// bindings only and does not build, import, or execute the project.
func (s *roleStage) contractCallerExcerpts(scope string, anchors, overview []record.Excerpt) ([]record.Excerpt, error) {
	anchorPaths, dirs := map[string]bool{}, map[string]bool{}
	for _, x := range anchors {
		if x.Kind == "code" && strings.HasSuffix(x.Path, ".go") && record.ScopeUnder(x.Path, scope) && record.HasNewContractEvidence(x, overview) {
			anchorPaths[x.Path], dirs[path.Dir(x.Path)] = true, true
		}
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	groups := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	files := map[string]contractGoFile{}
	incomplete := map[string]bool{}
	var order []string
	var scannedBytes int64
	scanned := 0
	// Read selected files before siblings. If a directory cannot be fully
	// scanned, only a parser-resolved reference in the definition's file is
	// eligible; unseen files may otherwise introduce ambiguous bindings.
	for _, selected := range []bool{true, false} {
		for _, e := range s.j.Snapshot.Manifest.Entries {
			dir := path.Dir(e.Path)
			if !dirs[dir] || e.Kind != "file" || record.SourceKind(e.Path) != "code" || !strings.HasSuffix(e.Path, ".go") || anchorPaths[e.Path] != selected {
				continue
			}
			if err := s.ctx.Err(); err != nil {
				return nil, err
			}
			if !s.navigationEligible(groups, e.Path) || !record.ScopeUnder(e.Path, scope) || e.Size == nil ||
				scanned >= navigationMaxFiles || *e.Size > navigationScanBytes-scannedBytes {
				incomplete[dir] = true
				continue
			}
			scanned++
			scannedBytes += *e.Size
			text, ok, err := s.readSource(e)
			if err != nil {
				return nil, err
			}
			if !ok || record.GeneratedHeader([]byte(text)) {
				incomplete[dir] = true
				continue
			}
			positions := token.NewFileSet()
			f, err := parser.ParseFile(positions, e.Path, text, parser.DeclarationErrors|parser.ParseComments)
			if err != nil || ast.IsGenerated(f) {
				incomplete[dir] = true
				continue
			}
			files[e.Path] = contractGoFile{e, positions, f, strings.Split(text, "\n")}
			order = append(order, e.Path)
		}
	}
	byName := map[functionName]int{}
	for _, f := range files {
		for name := range f.parsed.Scope.Objects {
			key := functionName{path.Dir(f.entry.Path), f.parsed.Name.Name, name}
			byName[key]++
		}
	}
	definitions := map[functionName]contractGoDefinition{}
	for _, f := range files {
		dir, pkg := path.Dir(f.entry.Path), f.parsed.Name.Name
		unconditional := f.unconditional()
		for _, d := range f.parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !record.ValidContractLiteral(fn.Name.Name) {
				continue
			}
			key := functionName{dir, pkg, fn.Name.Name}
			if byName[key] == 1 {
				definitions[key] = contractGoDefinition{fn, f, unconditional}
			}
		}
	}
	selected := map[*ast.FuncDecl][]record.Excerpt{}
	for _, x := range anchors {
		f, ok := files[x.Path]
		if !ok || f.entry.SHA256 != x.SHA256 || !anchorPaths[x.Path] {
			continue
		}
		for _, d := range f.parsed.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || (len(selected) >= maxContractCallers && len(selected[fn]) == 0) {
				continue
			}
			key := functionName{path.Dir(f.entry.Path), f.parsed.Name.Name, fn.Name.Name}
			if definitions[key].fn != fn || f.line(fn.Body.End()) < x.Start || f.line(fn.Body.Pos()) > x.End {
				continue
			}
			if f.freshBody(fn.Body, x, overview) {
				selected[fn] = append(selected[fn], x)
			}
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	type ranked struct {
		record.Excerpt
		score int
	}
	var found []ranked
	var callee record.Excerpt
	for _, p := range order {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		f := files[p]
		dir := path.Dir(p)
		unresolved := map[*ast.Ident]bool{}
		imports := map[string]bool{}
		for _, i := range f.parsed.Imports {
			if i.Name != nil {
				imports[i.Name.Name] = true
			}
		}
		if !incomplete[dir] && !imports["."] {
			for _, ident := range f.parsed.Unresolved {
				unresolved[ident] = true
			}
		}
		for _, d := range f.parsed.Decls {
			caller, ok := d.(*ast.FuncDecl)
			if !ok || caller.Body == nil {
				continue
			}
			ast.PreorderStack(caller.Body, nil, func(n ast.Node, stack []ast.Node) bool {
				switch n.(type) {
				case *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
					return false
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, indexed := contractGoCallName(call)
				if name == nil || imports[name.Name] {
					return true
				}
				target := definitions[functionName{dir, f.parsed.Name.Name, name.Name}]
				if target.fn == nil || caller == target.fn || (indexed && target.fn.Type.TypeParams == nil) ||
					(name.Obj != target.fn.Name.Obj && !(target.unconditional && unresolved[name])) {
					return true
				}
				start, end, score := contractGoDecision(call, stack)
				from, to := f.line(start), f.line(end)
				if score == 0 || to-from >= record.MaxContractLocationLines {
					return true
				}
				callLine := f.line(name.Pos())
				if callee.ID == "" && slices.ContainsFunc(selected[caller], func(x record.Excerpt) bool {
					return x.Start <= from && to <= x.End && (record.NewContractEvidenceLine(x, callLine, overview) || record.NewContractEvidenceLine(x, to, overview))
				}) {
					callee = s.contractCalleeExcerpt(target, overview)
				}
				if len(selected[target.fn]) == 0 {
					return true
				}
				windowStart := max(f.line(caller.Pos()), from-3, to-record.MaxContractLocationLines+1)
				windowEnd := min(f.line(caller.End()), to+3, windowStart+record.MaxContractLocationLines-1)
				x, ok := s.boundedExcerpt(f.entry, "code", "caller", windowStart, f.lines[windowStart-1:windowEnd], roleMaxWindowBytes)
				if !ok || x.End < to {
					x, ok = s.boundedExcerpt(f.entry, "code", "caller", from, f.lines[from-1:to], roleMaxWindowBytes)
				}
				if !ok || x.End < to || (!record.NewContractEvidenceLine(x, callLine, overview) &&
					!record.NewContractEvidenceLine(x, to, overview)) {
					return true
				}
				overlaps := func(c ranked) bool { return c.Path == x.Path && c.Start <= x.End && x.Start <= c.End }
				if slices.ContainsFunc(found, func(c ranked) bool { return overlaps(c) && c.score >= score }) ||
					(len(found) == maxContractCallers && found[len(found)-1].score >= score) {
					return true
				}
				found = slices.DeleteFunc(found, overlaps)
				x.Symbol = name.Name
				found = append(found, ranked{x, score})
				slices.SortStableFunc(found, func(a, b ranked) int { return b.score - a.score })
				found = found[:min(len(found), maxContractCallers)]
				return true
			})
		}
	}
	var out []record.Excerpt
	if callee.ID != "" && len(found) == maxContractCallers {
		found = found[:maxContractCallers-1]
	}
	for _, candidate := range found {
		out = append(out, candidate.Excerpt)
	}
	if callee.ID != "" {
		out = append(out, callee)
	}
	return out, nil
}

func (s *roleStage) contractCalleeExcerpt(target contractGoDefinition, overview []record.Excerpt) record.Excerpt {
	f := target.file
	from, to := f.line(target.fn.Pos()), f.line(target.fn.End())
	if to-from >= record.MaxContractLocationLines {
		return record.Excerpt{}
	}
	x, ok := s.boundedExcerpt(f.entry, "code", "context", from, f.lines[from-1:to], roleMaxWindowBytes)
	if !ok || x.End < to || !f.freshBody(target.fn.Body, x, overview) {
		return record.Excerpt{}
	}
	return x
}

func (f contractGoFile) freshBody(body *ast.BlockStmt, x record.Excerpt, overview []record.Excerpt) bool {
	fresh := false
	ast.Inspect(body, func(n ast.Node) bool {
		if fresh {
			return false
		}
		switch n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.AssignStmt, *ast.ExprStmt, *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.ReturnStmt:
			line := f.line(n.Pos())
			fresh = x.Start <= line && line <= x.End && record.NewContractEvidenceLine(x, line, overview)
		}
		return !fresh
	})
	return fresh
}

func (f contractGoFile) unconditional() bool {
	for _, group := range f.parsed.Comments {
		if group.Pos() > f.parsed.Package {
			break
		}
		for _, comment := range group.List {
			if constraint.IsGoBuild(comment.Text) || constraint.IsPlusBuild(comment.Text) {
				return false
			}
		}
	}
	if slices.ContainsFunc(f.parsed.Imports, func(i *ast.ImportSpec) bool { return i.Path.Value == `"C"` || i.Path.Value == "`C`" }) {
		return false
	}
	// Ask go/build to consider the filename alone, with no host OS or
	// architecture. This rejects GOOS and GOARCH suffixes without reading
	// any other files or assuming the target uses the worker's platform.
	c := build.Context{OpenFile: func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil }}
	ok, _ := c.MatchFile("", path.Base(f.entry.Path))
	return ok
}

func contractGoCallName(call *ast.CallExpr) (*ast.Ident, bool) {
	fun := ast.Unparen(call.Fun)
	indexed := true
	switch x := fun.(type) {
	case *ast.IndexExpr:
		fun = ast.Unparen(x.X)
	case *ast.IndexListExpr:
		fun = ast.Unparen(x.X)
	default:
		indexed = false
	}
	name, _ := fun.(*ast.Ident)
	return name, indexed
}

// Prefer a guard with a visible outcome, then an explicitly returned call.
// A call used only for a side effect does not produce a window here.
func contractGoDecision(call *ast.CallExpr, stack []ast.Node) (start, end token.Pos, score int) {
	child := ast.Node(call)
	for i := len(stack) - 1; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.ReturnStmt:
			return n.Pos(), n.End(), 1
		case *ast.IfStmt:
			if n.Cond == child || n.Init == child {
				if a, ok := n.Init.(*ast.AssignStmt); n.Init == child && (!ok || !goAssignmentUsed(a, n.Cond)) {
					break
				}
				if r := goGuardOutcome(n); r != nil {
					return n.Pos(), r.End(), 2
				}
			}
		case *ast.BlockStmt:
			if a, ok := child.(*ast.AssignStmt); ok {
				at := slices.Index(n.List, ast.Stmt(a)) + 1
				if 0 < at && at < len(n.List) {
					switch next := n.List[at].(type) {
					case *ast.IfStmt:
						if r := goGuardOutcome(next); r != nil && goAssignmentUsed(a, next.Cond) {
							return a.Pos(), r.End(), 2
						}
					case *ast.ReturnStmt:
						if slices.ContainsFunc(next.Results, func(e ast.Expr) bool { return goAssignmentUsed(a, e) }) {
							return a.Pos(), next.End(), 1
						}
					}
				}
			}
		}
		child = stack[i]
	}
	return 0, 0, 0
}

func goAssignmentUsed(a *ast.AssignStmt, expr ast.Expr) bool {
	for node := range ast.Preorder(expr) {
		if id, ok := node.(*ast.Ident); ok && id.Obj != nil && slices.ContainsFunc(a.Lhs, func(lhs ast.Expr) bool {
			decl, ok := lhs.(*ast.Ident)
			return ok && decl.Obj == id.Obj
		}) {
			return true
		}
	}
	return false
}

func goGuardOutcome(n *ast.IfStmt) ast.Stmt {
	for _, branch := range []ast.Stmt{n.Body, n.Else} {
		if block, ok := branch.(*ast.BlockStmt); ok {
			for _, stmt := range block.List {
				switch r := stmt.(type) {
				case *ast.ReturnStmt:
					return r
				case *ast.BranchStmt:
					if r.Tok == token.BREAK || r.Tok == token.CONTINUE {
						return r
					}
				}
			}
		}
	}
	return nil
}
