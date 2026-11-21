package review

import (
	"go/ast"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var goCommandToken = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_-]*`)

type goContractCommand struct{ binary, subcommand, dir string }

// Accept the same binary and first argument in the statement and its citation.
// The parser below also requires that pair to identify a real cmd/<binary> route.
func goCommandsInContract(commands []goContractCommand, statement, cited string) []goContractCommand {
	tokens := goCommandToken.FindAllStringIndex(statement, -1)
	for i, binary := range tokens {
		if i+1 == len(tokens) {
			break
		}
		arg := tokens[i+1]
		if binary[1] == arg[0] || strings.Trim(statement[binary[1]:arg[0]], " \t") != "" {
			continue
		}
		command := goContractCommand{binary: statement[binary[0]:binary[1]], subcommand: statement[arg[0]:arg[1]]}
		if slices.Contains(commands, command) || !hasGoCommand(statement, command) || !hasGoCommand(cited, command) {
			continue
		}
		commands = append(commands, command)
	}
	return commands
}

func hasGoCommand(text string, command goContractCommand) bool {
	// Inspect each possible binary occurrence: arbitrary prose before an
	// invocation may itself contain two adjacent words matching the pattern.
	for i := 0; i < len(text); {
		offset := strings.Index(text[i:], command.binary)
		if offset < 0 {
			return false
		}
		start := i + offset
		i = start + len(command.binary)
		if start >= 2 && text[start-2:start] == "./" {
			start -= 2
		}
		if start != 0 && !strings.ContainsRune(" \t\r\n`", rune(text[start-1])) {
			continue
		}
		arg := strings.TrimLeft(text[i:], " \t")
		if len(arg) == len(text)-i || !strings.HasPrefix(arg, command.subcommand) {
			continue
		}
		rest := arg[len(command.subcommand):]
		next, _ := utf8.DecodeRuneInString(rest)
		if rest == "" || strings.ContainsRune(" \t\r\n`,!?:;)]}|", rune(rest[0])) ||
			unicode.Is(unicode.Hangul, next) || rest[0] == '.' && (len(rest) == 1 || strings.ContainsRune(" \t\r\n`", rune(rest[1]))) {
			return true
		}
	}
	return false
}

// Read the entire conventional command package under the existing witness
// limits. Missing siblings, duplicated packages and PR-unchanged context
// must never yield a partial static route.
func (s *roleStage) goCommandTraces(g codeGroups, queries []witnessQuery, read func(source.Entry) (string, bool, error)) ([]goWrapperTrace, error) {
	if g.tier != nil || !slices.ContainsFunc(queries, func(q witnessQuery) bool { return len(q.goCommands) != 0 }) {
		return nil, nil
	}
	byBinary := map[string][]string{}
	for _, e := range s.j.Snapshot.Manifest.Entries {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		dir := path.Dir(e.Path)
		if path.Base(path.Dir(dir)) != "cmd" || record.SourceEcosystem(e.Path) != "go" || record.SourceKind(e.Path) != "code" || !s.navigationEligible(g, e.Path) {
			continue
		}
		name := path.Base(dir)
		if !slices.Contains(byBinary[name], dir) {
			byBinary[name] = append(byBinary[name], dir)
		}
	}
	packages := map[string]goWrapperPackage{}
	var used []record.Excerpt
	remaining := roleMaxCodeBytes
	var traces []goWrapperTrace
	type commandQuery struct {
		index   int
		command goContractCommand
	}
	var commands []commandQuery
	for _, standalone := range []bool{false, true} {
		for i, q := range queries {
			for _, command := range q.goCommands {
				if (command.dir != "") == standalone {
					commands = append(commands, commandQuery{i, command})
				}
			}
		}
	}
	for _, item := range commands {
		i, command := item.index, item.command
		q := queries[i]
		if q.hasGoCommandRoute {
			continue
		}
		var dir string
		for _, p := range byBinary[command.binary] {
			if !record.ScopeUnder(p, q.scope) || (command.dir != "" && p != command.dir) {
				continue
			}
			if dir != "" {
				dir = ""
				break
			}
			dir = p
		}
		if dir == "" {
			continue
		}
		pkg, exists := packages[dir]
		if !exists {
			var err error
			pkg, err = s.goWrapperPackage(g, dir, read)
			if err != nil {
				return nil, err
			}
			packages[dir] = pkg
		}
		main := pkg.definitions[functionName{dir, "main", "main"}]
		if main.fn == nil || pkg.generated[main.file.entry.Path] || goFieldCount(main.fn.Type.Params) != 0 || goFieldCount(main.fn.Type.Results) != 0 {
			continue
		}
		var trace goWrapperTrace
		if command.dir != "" {
			trace = s.goStandaloneTrace(pkg, main)
		} else if options := s.goCommandTrace(pkg, main, nil, command.subcommand, 2); len(options) == 1 {
			trace = options[0]
		}
		if len(trace.excerpts) == 0 {
			continue
		}
		windows, ok := wholeTraceWindows(used, trace.excerpts, remaining, maxGoWrapperWindows)
		if !ok && command.dir != "" && len(trace.excerpts) > 1 {
			trace.excerpts, trace.callees = trace.excerpts[:1], nil
			windows, ok = wholeTraceWindows(used, trace.excerpts, remaining, maxGoWrapperWindows)
		}
		if !ok {
			continue
		}
		used = append(used, windows...)
		for _, x := range windows {
			remaining -= record.JSONLen(x.Text)
		}
		queries[i].hasGoCommandRoute = true
		if len(windows) == 0 {
			continue
		}
		traces = append(traces, trace)
		if len(traces) == maxGoWrapperTraces {
			return traces, nil
		}
	}
	return traces, nil
}

// Follow up to two local argv transfers before the subcommand dispatch. Match
// only top-level switches and equality branches. Calls inside a help branch,
// closure, or arbitrary conditional block do not establish a command route.
func (s *roleStage) goCommandTrace(pkg goWrapperPackage, def contractGoDefinition, argv *ast.Object, command string, depth int) []goWrapperTrace {
	if goCommandArgsWritten(def, argv) {
		return nil
	}
	var traces []goWrapperTrace
	for _, stmt := range def.fn.Body.List {
		if len(traces) > 1 {
			return traces
		}
		var calls []ast.Expr
		switch n := stmt.(type) {
		case *ast.SwitchStmt:
			if n.Init != nil || !goCommandArg(def.file, n.Tag, argv) {
				continue
			}
			for _, item := range n.Body.List {
				c := item.(*ast.CaseClause)
				if slices.ContainsFunc(c.List, func(e ast.Expr) bool { return goCommandLiteral(e, command) }) {
					traces = append(traces, s.goCommandBranch(pkg, def, n.Pos(), c.Body))
				}
			}
		case *ast.IfStmt:
			if a, ok := n.Init.(*ast.AssignStmt); ok {
				calls = a.Rhs
			}
			if eq, ok := ast.Unparen(n.Cond).(*ast.BinaryExpr); n.Init == nil && ok && eq.Op == token.EQL &&
				((goCommandArg(def.file, eq.X, argv) && goCommandLiteral(eq.Y, command)) || (goCommandArg(def.file, eq.Y, argv) && goCommandLiteral(eq.X, command))) {
				traces = append(traces, s.goCommandBranch(pkg, def, n.Pos(), n.Body.List))
			}
		case *ast.ExprStmt:
			calls = goCommandCallExpr(def.file, n.X)
		case *ast.ReturnStmt:
			calls = n.Results
		}
		if depth == 0 {
			continue
		}
		for _, expr := range calls {
			call, ok := ast.Unparen(expr).(*ast.CallExpr)
			if !ok || call.Ellipsis.IsValid() {
				continue
			}
			target := pkg.resolveGoFunction(def.file, call.Fun)
			if target.fn == nil || target.fn == def.fn || pkg.generated[target.file.entry.Path] {
				continue
			}
			param := goForwardedCommandArgs(def.file, target.fn, call.Args, argv)
			if param == nil {
				continue
			}
			for _, trace := range s.goCommandTrace(pkg, target, param, command, depth-1) {
				start, end := def.file.line(stmt.Pos()), def.file.line(call.End())
				x := s.goCommandExcerpt(def, start, end)
				if x.ID != "" && len(trace.excerpts) != 0 {
					trace.excerpts = append([]record.Excerpt{x}, trace.excerpts...)
					trace.callees = append([]string{target.fn.Name.Name}, trace.callees...)
				} else {
					trace = goWrapperTrace{}
				}
				traces = append(traces, trace)
			}
		}
	}
	return traces
}

// A reassignment or element update invalidates this simple argument binding.
func goCommandArgsWritten(def contractGoDefinition, argv *ast.Object) bool {
	written := false
	ast.Inspect(def.fn.Body, func(n ast.Node) bool {
		if written {
			return false
		}
		if assignment, ok := n.(*ast.AssignStmt); ok {
			written = slices.ContainsFunc(assignment.Lhs, func(lhs ast.Expr) bool {
				lhs = ast.Unparen(lhs)
				if at, ok := lhs.(*ast.IndexExpr); ok {
					lhs = ast.Unparen(at.X)
				}
				if argv == nil {
					return goOSMember(def.file, lhs, "Args")
				}
				id, ok := lhs.(*ast.Ident)
				return ok && id.Obj == argv
			})
		}
		return !written
	})
	return written
}

func goCommandCallExpr(f contractGoFile, expr ast.Expr) []ast.Expr {
	if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && len(call.Args) == 1 && goOSMember(f, call.Fun, "Exit") {
		return call.Args
	}
	return []ast.Expr{expr}
}

func goCommandLiteral(expr ast.Expr, expected string) bool {
	lit, ok := ast.Unparen(expr).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	value, err := strconv.Unquote(lit.Value)
	return err == nil && value == expected
}

func goCommandArg(f contractGoFile, expr ast.Expr, argv *ast.Object) bool {
	index, ok := ast.Unparen(expr).(*ast.IndexExpr)
	if !ok {
		return false
	}
	if argv == nil {
		return goOSMember(f, index.X, "Args") && goBranchLiteral(index.Index) == "1"
	}
	id, ok := ast.Unparen(index.X).(*ast.Ident)
	return ok && id.Obj == argv && goBranchLiteral(index.Index) == "0"
}

func goOSMember(f contractGoFile, expr ast.Expr, member string) bool {
	sel, ok := ast.Unparen(expr).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != member {
		return false
	}
	id, ok := ast.Unparen(sel.X).(*ast.Ident)
	return ok && id.Obj == nil && slices.ContainsFunc(f.parsed.Imports, func(spec *ast.ImportSpec) bool {
		pkg, err := strconv.Unquote(spec.Path.Value)
		return err == nil && pkg == "os" && (spec.Name == nil && id.Name == "os" || spec.Name != nil && spec.Name.Name == id.Name)
	})
}

func goForwardedCommandArgs(f contractGoFile, fn *ast.FuncDecl, args []ast.Expr, argv *ast.Object) *ast.Object {
	if len(args) != goFieldCount(fn.Type.Params) {
		return nil
	}
	i := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			expr := ast.Unparen(args[i])
			i++
			if argv == nil {
				if span, ok := expr.(*ast.SliceExpr); ok && span.High == nil && span.Max == nil &&
					goBranchLiteral(span.Low) == "1" && goOSMember(f, span.X, "Args") {
					return name.Obj
				}
			} else if id, ok := expr.(*ast.Ident); ok && id.Obj == argv {
				return name.Obj
			}
		}
	}
	return nil
}

func (s *roleStage) goCommandBranch(pkg goWrapperPackage, def contractGoDefinition, start token.Pos, stmts []ast.Stmt) goWrapperTrace {
	if len(stmts) == 0 {
		return goWrapperTrace{}
	}
	var exprs []ast.Expr
	switch stmt := stmts[len(stmts)-1].(type) {
	case *ast.ReturnStmt:
		exprs = stmt.Results
	case *ast.ExprStmt:
		exprs = goCommandCallExpr(def.file, stmt.X)
	}
	var trace goWrapperTrace
	for _, expr := range exprs {
		call, ok := ast.Unparen(expr).(*ast.CallExpr)
		if !ok || call.Ellipsis.IsValid() {
			continue
		}
		target := pkg.resolveGoFunction(def.file, call.Fun)
		if target.fn == nil || target.fn == def.fn || pkg.generated[target.file.entry.Path] || len(call.Args) != goFieldCount(target.fn.Type.Params) {
			continue
		}
		end := def.file.line(call.End())
		from := def.file.line(start)
		caller := s.goCommandExcerpt(def, from, end)
		callee := s.goCommandHandlerWindow(target)
		if caller.ID != "" && callee.ID != "" {
			if len(trace.excerpts) != 0 {
				return goWrapperTrace{}
			}
			trace = goWrapperTrace{excerpts: []record.Excerpt{caller, callee}, callees: []string{target.fn.Name.Name}}
		}
	}
	return trace
}

func (s *roleStage) goCommandHandlerWindow(def contractGoDefinition) record.Excerpt {
	f, fn := def.file, def.fn
	// Small handlers fit in full. For a longer handler, preserve the final
	// operations and result, which frequently follow startup or flag handling.
	start, end := f.line(fn.Pos()), f.line(fn.End())
	for from := max(start, end-39); from < end; from++ {
		if x := s.goWrapperExcerpt(f, from, end, roleMaxCodeLines, roleMaxCodeFile); x.ID != "" {
			return x
		}
	}
	return s.goWrapperDecisionWindow(def, "")
}

func (s *roleStage) goCommandExcerpt(def contractGoDefinition, start, end int) record.Excerpt {
	if x := s.goWrapperExcerpt(def.file, def.file.line(def.fn.Pos()), end, witnessMaxLines, witnessMaxExcerpt); x.ID != "" {
		return x
	}
	return s.goWrapperExcerpt(def.file, start, end, witnessMaxLines, witnessMaxExcerpt)
}
