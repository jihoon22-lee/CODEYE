package testexec

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/faildoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

func linkResultCases(ctx context.Context, job analysis.Job, p testPlan, runner, module string, stdout []byte, cases []resultdoc.Case) {
	if len(cases) == 0 {
		return
	}
	snapshot, err := os.OpenRoot(job.Snapshot.SnapshotDir)
	if err != nil {
		clearCaseLocations(cases)
		return
	}
	defer snapshot.Close()
	workspace, err := os.OpenRoot(job.Snapshot.WorkspaceDir)
	if err != nil {
		clearCaseLocations(cases)
		return
	}
	defer workspace.Close()
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	if runner == "go" {
		linkGoCases(ctx, p, module, stdout, cases, files, snapshot, workspace)
		return
	}
	for i := range cases {
		test := &cases[i]
		file, line := test.Path, test.Line
		test.Path, test.Line = "", 0
		if ctx.Err() != nil {
			continue
		}
		switch runner {
		case "maven", "gradle":
			file, line = javaCaseLocation(p, test.Name, files, snapshot, workspace)
		case "cargo":
			file, line = rustDocLocation(test.Name)
		}
		if file == "" || line <= 0 {
			continue
		}
		if filepath.IsAbs(file) {
			rel, err := filepath.Rel(job.Snapshot.WorkspaceDir, file)
			if err != nil {
				continue
			}
			file = filepath.ToSlash(rel)
		} else if runner != "maven" && runner != "gradle" {
			file = path.Join(p.dir, filepath.ToSlash(file))
		}
		if !sourceexcerpt.SafePath(file) || !pack.UnderDir(file, p.dir) || !pack.PathInScope(file, job.Scope) {
			continue
		}
		body, ok := matchingGoTestFile(snapshot, workspace, files[file])
		if !ok {
			continue
		}
		lines := bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'})
		if line > len(lines) || len(lines[line-1]) == 0 {
			continue
		}
		test.Path, test.Line = file, line
	}
}

func clearCaseLocations(cases []resultdoc.Case) {
	for i := range cases {
		cases[i].Path, cases[i].Line = "", 0
	}
}

var (
	javaIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	rustDocCase    = regexp.MustCompile(`^([^ ]+\.rs) - .+ \(line ([1-9][0-9]*)\)$`)
)

func javaCaseLocation(p testPlan, name string, files map[string]source.Entry, snapshot, workspace *os.Root) (string, int) {
	if i := strings.IndexAny(name, "(["); i >= 0 {
		name = name[:i]
	}
	class, method, ok := cutLast(name, ".")
	if !ok || !javaIdentifier.MatchString(method) {
		return "", 0
	}
	parts := strings.Split(class, ".")
	for _, part := range parts {
		if !javaIdentifier.MatchString(part) {
			return "", 0
		}
	}
	if strings.Contains(parts[len(parts)-1], "$") {
		return "", 0
	}
	pattern := regexp.MustCompile(`(?m)^[ \t]*(?:(?:public|protected|private|static|final|synchronized)[ \t]+)*void[ \t]+` + regexp.QuoteMeta(method) + `[ \t]*\(`)
	var matched string
	line := 0
	for _, dir := range p.args {
		for _, sourceDir := range []string{"src/test/java", "src/integrationTest/java"} {
			file := path.Join(dir, sourceDir, strings.Join(parts, "/")+".java")
			body, ok := matchingGoTestFile(snapshot, workspace, files[file])
			if !ok {
				continue
			}
			occurrences := pattern.FindAllIndex(body, 2)
			if len(occurrences) > 1 || len(occurrences) == 1 && matched != "" {
				return "", 0
			}
			if len(occurrences) == 1 {
				matched = file
				line = bytes.Count(body[:occurrences[0][0]], []byte{'\n'}) + 1
			}
		}
	}
	return matched, line
}

func rustDocLocation(name string) (string, int) {
	m := rustDocCase.FindStringSubmatch(name)
	if m == nil {
		return "", 0
	}
	line, _ := strconv.Atoi(m[2])
	return m[1], line
}

func linkGoCases(ctx context.Context, p testPlan, module string, stdout []byte, cases []resultdoc.Case, files map[string]source.Entry, snapshot, workspace *os.Root) {
	if module == "" {
		return
	}
	names := map[string]string{}
	for _, test := range cases {
		names[test.Name] = ""
	}
	ambiguous := map[string]bool{}
	for line := range bytes.SplitSeq(stdout, []byte{'\n'}) {
		var e goTestEvent
		if len(line) > faildoc.MaxEventBytes || json.Unmarshal(line, &e) != nil || e.Action != "run" {
			continue
		}
		if pkg, wanted := names[e.Test]; wanted {
			ambiguous[e.Test] = ambiguous[e.Test] || pkg != "" && pkg != e.Package
			names[e.Test] = e.Package
		}
	}
	inScope := govet.GoFilesInScope(files, govet.ModuleDirs(files), p.dir, p.args)
	byDir := map[string][]source.Entry{}
	for _, file := range inScope {
		if strings.HasSuffix(file, "_test.go") {
			dir := path.Dir(file)
			byDir[dir] = append(byDir[dir], files[file])
		}
	}
	wanted := map[string]map[string]bool{}
	for _, test := range cases {
		top, _, _ := strings.Cut(test.Name, "/")
		dir, ok := goFailureDirectory(module, p.dir, names[test.Name])
		if !ok || ambiguous[test.Name] || !faildoc.TestName(top) || len(byDir[dir]) == 0 {
			continue
		}
		if wanted[dir] == nil {
			wanted[dir] = map[string]bool{}
		}
		wanted[dir][top] = true
	}
	checked := map[string]map[string]goTestDeclaration{}
	readFiles, readBytes := 0, int64(0)
	for i := range cases {
		if ctx.Err() != nil {
			return
		}
		test := &cases[i]
		top, subpath, _ := strings.Cut(test.Name, "/")
		dir, ok := goFailureDirectory(module, p.dir, names[test.Name])
		if !ok || ambiguous[test.Name] || !wanted[dir][top] {
			continue
		}
		if _, available := checked[dir]; !available {
			checked[dir] = goTestDeclarations(ctx, snapshot, workspace, dir, byDir[dir], wanted[dir], &readFiles, &readBytes)
		}
		decl, ok := checked[dir][top]
		if !ok || !sourceexcerpt.SafePath(decl.path) {
			continue
		}
		line := decl.line
		if subpath != "" {
			body, ok := matchingGoTestFile(snapshot, workspace, files[decl.path])
			if !ok {
				continue
			}
			line = goSubtestLine(body, top, strings.Split(subpath, "/"))
		}
		if line > 0 {
			test.Path, test.Line = decl.path, line
		}
	}
}

func goSubtestLine(body []byte, top string, parts []string) int {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, "", body, 0)
	if err != nil {
		return 0
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != top || fn.Body == nil || fn.Recv != nil {
			continue
		}
		block, params := fn.Body, fn.Type.Params
		var position token.Pos
		for i := range parts {
			if params.NumFields() != 1 || len(params.List[0].Names) != 1 {
				return 0
			}
			arg := params.List[0].Names[0]
			call, callback := goNamedSubtest(block, arg.Obj, parts[i:])
			if callback == nil {
				return 0
			}
			position, block, params = call.Pos(), callback.Body, callback.Type.Params
		}
		return positions.PositionFor(position, false).Line
	}
	return 0
}

func goNamedSubtest(block *ast.BlockStmt, t *ast.Object, parts []string) (call *ast.CallExpr, callback *ast.FuncLit) {
	if t == nil {
		return nil, nil
	}
	name := parts[0]
	pathName := strings.Join(parts, "/")
	base, numbered := goNumberedSubtest(name)
	count := 0
	ambiguous := false
	ast.Inspect(block, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		c, ok := node.(*ast.CallExpr)
		if !ok || len(c.Args) != 2 {
			return true
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Run" {
			return true
		}
		recv, validRecv := sel.X.(*ast.Ident)
		str, validString := c.Args[0].(*ast.BasicLit)
		if !validRecv || recv.Obj != t || !validString || str.Kind != token.STRING {
			return true
		}
		n, err := strconv.Unquote(str.Value)
		if err != nil {
			return true
		}
		label := goRunnerSubtestName(n)
		// A slash in one literal can look like a nested T.Run path.
		// An occurrence suffix can look like an explicitly named case.
		flattened := strings.ContainsRune(label, '/') && (label == pathName || strings.HasPrefix(pathName, label+"/"))
		if flattened || numbered && label == base {
			ambiguous = true
		}
		if label != name {
			return true
		}
		count++
		call = c
		callback, _ = c.Args[1].(*ast.FuncLit)
		return true
	})
	if count != 1 || ambiguous {
		return nil, nil
	}
	return call, callback
}

// The testing package displays whitespace as underscores and escapes
// non-printable characters before it names a subtest in go test -json.
func goRunnerSubtestName(literal string) string {
	var name strings.Builder
	for _, r := range literal {
		switch {
		case unicode.IsSpace(r):
			name.WriteByte('_')
		case !strconv.IsPrint(r):
			quoted := strconv.QuoteRune(r)
			name.WriteString(quoted[1 : len(quoted)-1])
		default:
			name.WriteRune(r)
		}
	}
	return name.String()
}

func goNumberedSubtest(name string) (string, bool) {
	base, suffix, found := cutLast(name, "#")
	if !found || len(suffix) < 2 || len(suffix) > 2 && suffix[0] == '0' || suffix == "00" && base != "" {
		return "", false
	}
	_, err := strconv.ParseUint(suffix, 10, 31)
	return base, err == nil
}
