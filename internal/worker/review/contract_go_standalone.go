package review

import (
	"go/ast"
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func standaloneGoCommand(doc, statement, cited string) (goContractCommand, bool) {
	dir := path.Dir(doc)
	name := strings.ToLower(path.Base(doc))
	if path.Base(path.Dir(dir)) != "cmd" || !slices.Contains([]string{"readme.md", "readme.markdown", "readme"}, name) {
		return goContractCommand{}, false
	}
	binary := path.Base(dir)
	if !quotesStandaloneGoCommand(statement, binary) && !standaloneGoInvocation(statement, binary) {
		return goContractCommand{}, false
	}
	if quotesStandaloneGoCommand(cited, binary) || slices.ContainsFunc(strings.Split(cited, "\n"), func(line string) bool {
		return standaloneGoInvocation(line, binary)
	}) {
		return goContractCommand{binary: binary, dir: dir}, true
	}
	return goContractCommand{}, false
}

func quotesStandaloneGoCommand(text, binary string) bool {
	return slices.ContainsFunc(witnessCode.FindAllStringSubmatch(text, -1), func(markup []string) bool {
		return standaloneGoInvocation(markup[1], binary)
	})
}

func standaloneGoInvocation(text, binary string) bool {
	word := strings.Fields(strings.TrimPrefix(strings.TrimSpace(text), "$ "))
	if len(word) == 0 || strings.TrimPrefix(word[0], "./") != binary {
		return false
	}
	// A flag, path or operand placeholder is consistent with a standalone
	// invocation. A bare following word could name a subcommand.
	return len(word) == 1 || strings.HasPrefix(word[1], "-") || strings.HasPrefix(word[1], "<") ||
		strings.HasPrefix(word[1], "[") || strings.ContainsAny(word[1], "/.")
}

func (s *roleStage) goStandaloneTrace(pkg goWrapperPackage, main contractGoDefinition) goWrapperTrace {
	whole := func(def contractGoDefinition) (int, int) {
		return def.file.line(def.fn.Pos()), def.file.line(def.fn.End())
	}
	start, end := whole(main)
	x := s.goWrapperExcerpt(main.file, start, end, roleMaxCodeLines, roleMaxCodeFile)
	if x.ID == "" {
		return goWrapperTrace{}
	}
	trace := goWrapperTrace{excerpts: []record.Excerpt{x}}
	var target contractGoDefinition
	ambiguous := false
	ast.Inspect(main.fn.Body, func(n ast.Node) bool {
		if ambiguous {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit, *ast.GoStmt, *ast.DeferStmt:
			return false
		case *ast.CallExpr:
			fn := pkg.resolveGoFunction(main.file, n.Fun)
			if fn.fn != nil && fn.fn != main.fn && !n.Ellipsis.IsValid() && len(n.Args) == goFieldCount(fn.fn.Type.Params) {
				ambiguous = target.fn != nil && target.fn != fn.fn
				target = fn
			}
		}
		return !ambiguous
	})
	if ambiguous || target.fn == nil || pkg.generated[target.file.entry.Path] {
		return trace
	}
	start, end = whole(target)
	x = s.goWrapperExcerpt(target.file, start, end, roleMaxCodeLines, roleMaxCodeFile)
	if x.ID != "" {
		trace.excerpts = append(trace.excerpts, x)
		trace.callees = append(trace.callees, target.fn.Name.Name)
	}
	return trace
}
