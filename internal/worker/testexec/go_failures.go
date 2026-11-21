package testexec

import (
	"context"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/faildoc"
)

const (
	maxGoTestSourceFiles = 512
	maxGoTestSourceBytes = 32 << 20
	maxGoTestFileBytes   = 1 << 20
)

type goTestDeclaration struct {
	path, sha string
	line      int
}

// goFailureArtifact adds optional detail. It never alters the outcome and
// never runs a second command to locate the origin of a failure.
func goFailureArtifact(ctx context.Context, job analysis.Job, p testPlan, scratch, modulePath string, res executor.Result) (analysis.ArtifactFile, bool) {
	var none analysis.ArtifactFile
	events := faildoc.Failures(res.Stdout.Data)
	if len(events) == 0 || modulePath == "" || ctx.Err() != nil {
		return none, false
	}
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	snapshot, err := os.OpenRoot(job.Snapshot.SnapshotDir)
	if err != nil {
		return none, false
	}
	defer snapshot.Close()
	workspace, err := os.OpenRoot(job.Snapshot.WorkspaceDir)
	if err != nil {
		return none, false
	}
	defer workspace.Close()
	if _, ok := matchingGoTestFile(snapshot, workspace, files[path.Join(p.dir, "go.mod")]); !ok {
		return none, false
	}
	byDir := map[string][]source.Entry{}
	for _, file := range govet.GoFilesInScope(files, govet.ModuleDirs(files), p.dir, p.args) {
		if strings.HasSuffix(file, "_test.go") {
			dir := path.Dir(file)
			byDir[dir] = append(byDir[dir], files[file])
		}
	}
	wanted := map[string]map[string]bool{}
	for _, event := range events {
		dir, ok := goFailureDirectory(modulePath, p.dir, event.Package)
		if !ok || len(byDir[dir]) == 0 || !sourceexcerpt.SafePath(event.Package) {
			continue
		}
		if wanted[dir] == nil {
			wanted[dir] = map[string]bool{}
		}
		wanted[dir][event.Test] = true
	}

	stdoutSHA := domain.Digest(res.Stdout.Data)
	doc := faildoc.Document{SchemaVersion: faildoc.Version, RunID: job.RunID, AttemptID: job.AttemptID,
		Snapshot: job.Snapshot.Digest, TaskKey: p.spec.Key, StdoutSHA256: stdoutSHA}
	checked := map[string]map[string]goTestDeclaration{}
	readFiles, readBytes := 0, int64(0)
	for _, event := range events {
		if ctx.Err() != nil {
			return none, false
		}
		if len(doc.Failures) >= faildoc.MaxFailures {
			break
		}
		dir, ok := goFailureDirectory(modulePath, p.dir, event.Package)
		if !ok || !wanted[dir][event.Test] {
			continue
		}
		decls, complete := checked[dir]
		if !complete {
			decls = goTestDeclarations(ctx, snapshot, workspace, dir, byDir[dir], wanted[dir], &readFiles, &readBytes)
			checked[dir] = decls
		}
		d, ok := decls[event.Test]
		if !ok {
			continue
		}
		doc.Failures = append(doc.Failures, faildoc.Failure{Package: event.Package, Test: event.Test, Path: d.path,
			Line: d.line, BlobSHA256: d.sha, ByteStart: event.ByteStart, ByteEnd: event.ByteEnd})
	}
	if len(doc.Failures) == 0 {
		return none, false
	}
	body, err := faildoc.Encode(doc, faildoc.Expect{RunID: job.RunID, AttemptID: job.AttemptID, Snapshot: job.Snapshot.Digest,
		TaskKey: p.spec.Key, ModuleDir: p.dir, StdoutSHA256: stdoutSHA, Lookup: func(name string) (string, bool) {
			e, ok := files[name]
			return e.SHA256, ok
		}})
	if err != nil {
		return none, false
	}
	file := filepath.Join(scratch, "go-test-failures.json")
	if err := analysis.WriteFileExclusive(file, body); err != nil {
		return none, false
	}
	return analysis.ArtifactFile{Path: file, Role: "other", MediaType: faildoc.MediaType}, true
}

func goFailureDirectory(module, moduleDir, packagePath string) (string, bool) {
	if packagePath == module {
		return moduleDir, true
	}
	relative, ok := strings.CutPrefix(packagePath, module+"/")
	if !ok || domain.ValidateEvidencePath(relative) != nil {
		return "", false
	}
	return path.Join(moduleDir, relative), true
}

func goTestDeclarations(ctx context.Context, snapshot, workspace *os.Root, dir string, entries []source.Entry, wanted map[string]bool, readFiles *int, readBytes *int64) map[string]goTestDeclaration {
	if len(entries) == 0 || len(entries) > maxGoTestSourceFiles-*readFiles || !sameGoTestFileNames(workspace, dir, entries) {
		return nil
	}
	found, count := map[string]goTestDeclaration{}, map[string]int{}
	dotPackages := map[string]string{}
	shadowed := map[string]bool{}
	for _, e := range entries {
		if ctx.Err() != nil || e.Size == nil || *e.Size < 0 || *e.Size > maxGoTestSourceBytes-*readBytes {
			return nil
		}
		*readFiles += 1
		*readBytes += *e.Size
		body, ok := matchingGoTestFile(snapshot, workspace, e)
		if !ok {
			return nil
		}
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, e.Path, body, parser.ParseComments)
		if err != nil {
			return nil
		}
		if file.Scope.Lookup("T") != nil {
			shadowed[file.Name.Name] = true
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !wanted[fn.Name.Name] {
				continue
			}
			name := fn.Name.Name
			count[name]++
			if count[name] > 1 {
				delete(found, name)
				continue
			}
			if valid, dot := goTestSignature(fn, file); valid && sourceexcerpt.SafePath(e.Path) && unconditionalTestFile(e.Path, file) {
				found[name] = goTestDeclaration{e.Path, e.SHA256, positions.PositionFor(fn.Name.Pos(), false).Line}
				if dot {
					dotPackages[name] = file.Name.Name
				}
			}
		}
	}
	for name, pkg := range dotPackages {
		if shadowed[pkg] {
			delete(found, name)
		}
	}
	return found
}

func sameGoTestFileNames(workspace *os.Root, dir string, entries []source.Entry) bool {
	d, err := workspace.Open(dir)
	if err != nil {
		return false
	}
	defer d.Close()
	want := make(map[string]bool, len(entries))
	for _, e := range entries {
		want[path.Base(e.Path)] = true
	}
	// Read no more than a bounded batch at once, and stop on extra inputs.
	for seen := 0; seen < 8192; {
		files, err := d.ReadDir(128)
		for _, file := range files {
			name := file.Name()
			if !strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				continue
			}
			if !want[name] || !file.Type().IsRegular() {
				return false
			}
			delete(want, name)
		}
		if err == io.EOF {
			return len(want) == 0
		}
		if err != nil {
			return false
		}
		seen += len(files)
	}
	return false
}

func matchingGoTestFile(snapshot, workspace *os.Root, e source.Entry) ([]byte, bool) {
	if e.Kind != "file" || e.Size == nil || *e.Size < 0 || *e.Size > maxGoTestFileBytes || e.SHA256 == "" {
		return nil, false
	}
	var body []byte
	for _, root := range []*os.Root{workspace, snapshot} {
		f, err := openGoTestFile(root, e.Path)
		if err != nil {
			return nil, false
		}
		body, err = io.ReadAll(io.LimitReader(f, *e.Size+1))
		f.Close()
		if err != nil || int64(len(body)) != *e.Size || domain.Digest(body) != e.SHA256 {
			return nil, false
		}
	}
	return body, true
}

func openGoTestFile(root *os.Root, file string) (*os.File, error) {
	st, err := root.Lstat(file)
	if err != nil || !st.Mode().IsRegular() {
		return nil, fs.ErrInvalid
	}
	return root.Open(file)
}

func goTestSignature(fn *ast.FuncDecl, file *ast.File) (valid, dot bool) {
	if fn.Body == nil || fn.Type.TypeParams != nil || fn.Type.Results != nil || fn.Type.Params == nil || fn.Type.Params.NumFields() != 1 {
		return false, false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false, false
	}
	switch param := star.X.(type) {
	case *ast.Ident:
		if param.Name != "T" || param.Obj != nil {
			return false, false
		}
		imports := 0
		for _, imp := range file.Imports {
			if imp.Name != nil && imp.Name.Name == "." {
				if name, _ := strconv.Unquote(imp.Path.Value); name != "testing" {
					return false, false
				}
				imports++
			}
		}
		return imports == 1, true
	case *ast.SelectorExpr:
		alias, ok := param.X.(*ast.Ident)
		if !ok || param.Sel.Name != "T" {
			return false, false
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "testing" && (imp.Name == nil && alias.Name == "testing" || imp.Name != nil && imp.Name.Name == alias.Name) {
				return true, false
			}
		}
	}
	return false, false
}

// Match the filename without a host OS or architecture, then reject explicit
// build constraints. Other visible variants still count toward ambiguity.
func unconditionalTestFile(name string, file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, c := range group.List {
			if constraint.IsGoBuild(c.Text) || constraint.IsPlusBuild(c.Text) {
				return false
			}
		}
	}
	c := build.Context{OpenFile: func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil }}
	ok, _ := c.MatchFile("", path.Base(name))
	return ok
}
