package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// The model has no file tools (OpenCode tool use is policy_blocked), so the
// platform selects the caller, test and cleanup context a verifier needs.
// Selection is deterministic, limited to the authorized scope and bounded.
const (
	maxSymbols        = 8
	maxExpansion      = 6
	maxExpansionBytes = 8000
	maxWindowLines    = 60
	maxWindowBytes    = 2400
	searchLines       = 40
	maxScanFiles      = 80
	maxScanBytes      = 8 << 20
)

// Expansion limitation codes.
const (
	limNoSymbol        = "no_symbol_for_expansion"
	limNoCallers       = "no_callers_found"
	limCallersOutside  = "callers_outside_review_context"
	limExpansionGoOnly = "caller_expansion_go_only"
	limDirectCalls     = "caller_expansion_package_functions_only"
)

type functionName struct{ dir, pkg, name string }

type expansionSource struct {
	positions *token.FileSet
	file      *ast.File
	lines     []string
}

// citedSymbols selects functions and methods whose declarations or bodies
// intersect the lines a candidate actually cites.
func citedSymbols(catalog []Excerpt, candidates []CandidateText, files map[string]source.Entry,
	load func(source.Entry) (*expansionSource, error)) (map[functionName]bool, map[*ast.Ident]string, error) {
	byID := map[string]Excerpt{}
	for _, e := range catalog {
		byID[e.ID] = e
	}
	out := map[functionName]bool{}
	methods := map[*ast.Ident]string{}
	for _, c := range candidates {
		for _, id := range c.Evidence {
			e, ok := byID[id]
			// Caller expansion reads Go only (caller_expansion_go_only).
			if !ok || (e.Kind != "code" && e.Kind != "test") || !strings.HasSuffix(e.Path, ".go") {
				continue
			}
			src, known := files[e.Path]
			if !known || src.SHA256 != e.SHA256 {
				continue
			}
			parsed, err := load(src)
			if err != nil {
				return nil, nil, err
			}
			if parsed == nil || e.Start < 1 || e.End > len(parsed.lines) {
				continue
			}
			var overlapping []*ast.FuncDecl
			for _, decl := range parsed.file.Decls {
				f, ok := decl.(*ast.FuncDecl)
				if !ok || f.Body == nil || f.Name.Name == "main" || f.Name.Name == "init" ||
					!record.ValidEvidenceSymbol(f.Name.Name) || len(out)+len(methods) >= maxSymbols {
					continue
				}
				first := parsed.positions.PositionFor(f.Pos(), false).Line
				last := parsed.positions.PositionFor(f.End(), false).Line
				if first <= e.End && e.Start <= last {
					if f.Recv != nil {
						overlapping = append(overlapping, f)
					} else {
						out[functionName{path.Dir(e.Path), parsed.file.Name.Name, f.Name.Name}] = true
					}
				}
			}
			for _, f := range overlapping {
				if len(out)+len(methods) >= maxSymbols {
					break
				}
				// A broad source window alone does not choose between
				// distinct methods. A name from the candidate can only
				// disambiguate declarations that intersect its evidence.
				if len(overlapping) == 1 || explicitlyNamedMethod(c, f, overlapping) {
					methods[f.Name] = e.Path
				}
			}
		}
	}
	return out, methods, nil
}

func explicitlyNamedMethod(c CandidateText, fn *ast.FuncDecl, overlapping []*ast.FuncDecl) bool {
	named := func(name string) bool {
		quoted := "`" + name + "`"
		return strings.Contains(c.Title, quoted) || strings.Contains(c.Condition, quoted)
	}
	name := fn.Name.Name
	if named(methodReceiverName(fn) + "." + name) {
		return true
	}
	return named(name) && !slices.ContainsFunc(overlapping, func(other *ast.FuncDecl) bool {
		return other != fn && other.Name.Name == name
	})
}

type window struct{ lo, hi int } // 0-based inclusive

// enclosing grows a window around a call site, preferring the enclosing
// top-level Go function so deferred cleanup and guards stay in view.
func enclosing(lines []string, at int) window {
	lo, hi := max(0, at-10), min(len(lines)-1, at+10)
	for i := at; i >= max(0, at-searchLines); i-- {
		if strings.HasPrefix(lines[i], "func ") {
			lo = i
			break
		}
	}
	for i := at; i <= min(len(lines)-1, at+searchLines); i++ {
		if strings.TrimSuffix(lines[i], "\r") == "}" {
			hi = i
			break
		}
	}
	w, used := window{at, at}, len(lines[at])+1
	for grown := true; grown; {
		grown = false
		if w.hi < hi && w.hi-w.lo+1 < maxWindowLines && used+len(lines[w.hi+1])+1 <= maxWindowBytes {
			w.hi++
			used += len(lines[w.hi]) + 1
			grown = true
		}
		if w.lo > lo && w.hi-w.lo+1 < maxWindowLines && used+len(lines[w.lo-1])+1 <= maxWindowBytes {
			w.lo--
			used += len(lines[w.lo]) + 1
			grown = true
		}
	}
	return w
}

// expansionCaps bound the excerpts and bytes an expansion adds. read, when
// set, replaces the expansion's own bounded reads: the lead resolver passes
// its scan, so the reads of one lead share one file and byte limit and skip
// the files the lead may not read (ok false).
type expansionCaps struct {
	count, bytes int
	read         func(source.Entry) (text string, ok bool, err error)
}

// expand returns caller/test excerpts for the cited symbols, each bound to
// the snapshot blob and line range, plus limitation codes, within the caps
// of the smallest request budget.
func expand(ctx context.Context, snapshot source.Snapshot, scope domain.Scope, catalog []Excerpt, candidates []CandidateText, withheld, changed map[string]bool) ([]Excerpt, []string, error) {
	return expandWithin(ctx, snapshot, scope, catalog, candidates, withheld, changed, expansionCaps{count: maxExpansion, bytes: maxExpansionBytes})
}

// expandWithin is expand within caps (the request budget's expansion).
func expandWithin(ctx context.Context, snapshot source.Snapshot, scope domain.Scope, catalog []Excerpt, candidates []CandidateText, withheld, changed map[string]bool, caps expansionCaps) ([]Excerpt, []string, error) {
	lims := []string{limExpansionGoOnly, limDirectCalls}
	if !slices.ContainsFunc(catalog, func(e Excerpt) bool { return strings.HasSuffix(e.Path, ".go") }) {
		return nil, append(lims, limNoSymbol), nil
	}
	eligible := map[string]source.Entry{}
	for _, e := range snapshot.Manifest.Entries {
		if e.Kind == "file" && e.SHA256 != "" && e.Size != nil && e.ExcludedReason == "" && strings.HasSuffix(e.Path, ".go") &&
			inScope(e.Path, scope) && *e.Size <= maxFileBytes &&
			domain.ValidateEvidencePath(e.Path) == nil && !record.Sensitive([]byte(e.Path)) {
			eligible[e.Path] = e
		}
	}
	root, err := os.OpenRoot(snapshot.SnapshotDir)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	overflow := false
	scannedBytes := int64(0)
	parsedFiles := map[string]*expansionSource{}
	positions := token.NewFileSet()
	load := func(f source.Entry) (*expansionSource, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if withheld[f.Path] {
			lims = addLim(lims, limSecretWithheld)
			return nil, nil
		}
		if p, cached := parsedFiles[f.Path]; cached {
			return p, nil
		}
		var b []byte
		if caps.read != nil {
			parsedFiles[f.Path] = nil
			text, ok, err := caps.read(f)
			if err != nil || !ok {
				return nil, err
			}
			b = []byte(text)
		} else {
			if len(parsedFiles) >= maxScanFiles || scannedBytes+*f.Size > maxScanBytes {
				overflow = true
				return nil, nil
			}
			scannedBytes += *f.Size
			parsedFiles[f.Path] = nil
			read, err := readVerified(root, f)
			if err != nil {
				return nil, err
			}
			b = read
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 || record.Sensitive(b) {
			return nil, nil
		}
		file, err := parser.ParseFile(positions, f.Path, b, parser.ParseComments|parser.DeclarationErrors)
		if err != nil {
			overflow = true
			return nil, nil
		}
		p := &expansionSource{positions: positions, file: file, lines: strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")}
		parsedFiles[f.Path] = p
		return p, nil
	}
	symbols, methods, err := citedSymbols(catalog, candidates, eligible, load)
	if err != nil {
		return nil, nil, err
	}
	if len(symbols)+len(methods) == 0 {
		if overflow {
			lims = append(lims, limCallersOutside)
		}
		return nil, append(lims, limNoSymbol), nil
	}
	dirs := map[string]bool{}
	for sym := range symbols {
		dirs[sym.dir] = true
	}
	for _, filename := range methods {
		dirs[path.Dir(filename)] = true
	}
	var files []source.Entry
	for _, e := range eligible {
		if dirs[path.Dir(e.Path)] {
			files = append(files, e)
		}
	}
	sort.Slice(files, func(i, j int) bool {
		ti, tj := strings.HasSuffix(files[i].Path, "_test.go"), strings.HasSuffix(files[j].Path, "_test.go")
		if ti != tj {
			return !ti
		}
		return files[i].Path < files[j].Path
	})
	covered := func(path string, w window, picked []Excerpt) bool {
		for _, e := range catalog {
			if e.Path == path && e.Start <= w.lo+1 && w.hi+1 <= e.End {
				return true
			}
		}
		for _, e := range picked {
			if e.Path == path && e.Start <= w.hi+1 && w.lo+1 <= e.End {
				return true
			}
		}
		return false
	}
	var out []Excerpt
	used, hits := 0, 0
	if len(methods) > 0 {
		lims = slices.DeleteFunc(lims, func(lim string) bool { return lim == limDirectCalls })
		methodFiles := files
		if changed != nil {
			methodFiles = slices.DeleteFunc(slices.Clone(files), func(e source.Entry) bool { return !changed[e.Path] })
		}
		if err := scanMethodFiles(methodFiles, methods, load); err != nil {
			return nil, nil, err
		}
		required := map[string]bool{}
		for _, candidate := range candidates {
			for _, id := range candidate.Evidence {
				required[id] = true
			}
		}
		anchors := slices.DeleteFunc(slices.Clone(catalog), func(x Excerpt) bool { return !required[x.ID] })
		matches, found, err := methodCallers(ctx, positions, methodFiles, parsedFiles, methods, anchors)
		if err != nil {
			return nil, nil, err
		}
		hits = found
		for _, match := range matches {
			size := len(match.Text) + 1
			if slices.ContainsFunc(out, func(e Excerpt) bool {
				return e.Path == match.Path && e.Start <= match.End && match.Start <= e.End
			}) {
				continue
			}
			if len(out) >= caps.count || used+size > caps.bytes {
				overflow = true
				continue
			}
			match.ID = record.ExcerptID(snapshot.Digest, match.Path, match.SHA256, match.Start, match.End)
			out = append(out, match)
			used += size
		}
	}
	for _, f := range files {
		p, err := load(f)
		if err != nil {
			return nil, nil, err
		}
		if p == nil {
			continue
		}
		lines := p.lines
		ast.Inspect(p.file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			fun := call.Fun
			switch indexed := fun.(type) {
			case *ast.IndexExpr:
				fun = indexed.X
			case *ast.IndexListExpr:
				fun = indexed.X
			}
			name, ok := fun.(*ast.Ident)
			if !ok || (name.Obj != nil && name.Obj.Kind != ast.Fun) || !symbols[functionName{path.Dir(f.Path), p.file.Name.Name, name.Name}] {
				return true
			}
			i := p.positions.PositionFor(name.Pos(), false).Line - 1
			hits++
			w := enclosing(lines, i)
			if covered(f.Path, w, out) {
				return true
			}
			size := 0
			for _, l := range lines[w.lo : w.hi+1] {
				size += len(l) + 1
			}
			if size > maxWindowBytes || len(out) >= caps.count || used+size > caps.bytes {
				overflow = true
				return true
			}
			kind, origin := "code", "caller"
			if strings.HasSuffix(f.Path, "_test.go") {
				kind, origin = "test", "test"
			}
			out = append(out, Excerpt{ID: record.ExcerptID(snapshot.Digest, f.Path, f.SHA256, w.lo+1, w.hi+1), Path: f.Path, SHA256: f.SHA256,
				Start: w.lo + 1, End: w.hi + 1, Kind: kind, Origin: origin, Symbol: name.Name, Text: strings.Join(lines[w.lo:w.hi+1], "\n")})
			used += size
			return true
		})
	}
	switch {
	case overflow:
		lims = append(lims, limCallersOutside)
	case hits == 0:
		lims = append(lims, limNoCallers)
	}
	return out, lims, nil
}

func readVerified(root *os.Root, e source.Entry) ([]byte, error) {
	f, err := root.Open(e.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != *e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
		return nil, errors.New("snapshot_integrity_failed")
	}
	return b, nil
}
