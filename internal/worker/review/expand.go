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
	"regexp"
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

var funcDecl = regexp.MustCompile(`^func\s+([A-Za-z_][A-Za-z0-9_]{0,127})\s*[\[(]`)

// Expansion limitation codes.
const (
	limNoSymbol        = "no_symbol_for_expansion"
	limNoCallers       = "no_callers_found"
	limCallersOutside  = "callers_outside_review_context"
	limExpansionGoOnly = "caller_expansion_go_only"
	limDirectCalls     = "caller_expansion_package_functions_only"
)

type functionName struct{ dir, pkg, name string }

// citedSymbols selects up to maxSymbols package functions from cited Go excerpts.
func citedSymbols(catalog []Excerpt, candidates []Candidate) map[functionName]bool {
	byID := map[string]Excerpt{}
	for _, e := range catalog {
		byID[e.ID] = e
	}
	out := map[functionName]bool{}
	for _, c := range candidates {
		for _, id := range c.Evidence {
			e, ok := byID[id]
			if !ok || (e.Kind != "code" && e.Kind != "test") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), e.Path, e.Text, parser.PackageClauseOnly)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(e.Text, "\n") {
				m := funcDecl.FindStringSubmatch(line)
				if m == nil || m[1] == "main" || m[1] == "init" || len(out) >= maxSymbols {
					continue
				}
				out[functionName{path.Dir(e.Path), file.Name.Name, m[1]}] = true
			}
		}
	}
	return out
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

// expand returns caller/test excerpts for the cited symbols, each bound to
// the snapshot blob and line range, plus limitation codes.
func expand(ctx context.Context, snapshot source.Snapshot, scope domain.Scope, catalog []Excerpt, candidates []Candidate) ([]Excerpt, []string, error) {
	symbols := citedSymbols(catalog, candidates)
	lims := []string{limExpansionGoOnly, limDirectCalls}
	if len(symbols) == 0 {
		return nil, append(lims, limNoSymbol), nil
	}
	dirs := map[string]bool{}
	for sym := range symbols {
		dirs[sym.dir] = true
	}
	var files []source.Entry
	for _, e := range snapshot.Manifest.Entries {
		if e.Kind == "file" && e.SHA256 != "" && e.Size != nil && e.ExcludedReason == "" && strings.HasSuffix(e.Path, ".go") &&
			dirs[path.Dir(e.Path)] && inScope(e.Path, scope) && *e.Size <= maxFileBytes && domain.ValidateEvidencePath(e.Path) == nil {
			files = append(files, e)
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		ti, tj := strings.HasSuffix(files[i].Path, "_test.go"), strings.HasSuffix(files[j].Path, "_test.go")
		if ti != tj {
			return !ti
		}
		return files[i].Path < files[j].Path
	})
	root, err := os.OpenRoot(snapshot.SnapshotDir)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
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
	used, hits, overflow := 0, 0, false
	scannedBytes := int64(0)
	for n, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if n >= maxScanFiles || scannedBytes+*f.Size > maxScanBytes {
			overflow = true
			break
		}
		scannedBytes += *f.Size
		b, err := readVerified(root, f)
		if err != nil {
			return nil, nil, err
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 || record.Sensitive(b) {
			continue
		}
		lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, f.Path, b, 0)
		if err != nil {
			overflow = true
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
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
			if !ok || (name.Obj != nil && name.Obj.Kind != ast.Fun) || !symbols[functionName{path.Dir(f.Path), file.Name.Name, name.Name}] {
				return true
			}
			i := positions.PositionFor(name.Pos(), false).Line - 1
			hits++
			w := enclosing(lines, i)
			if covered(f.Path, w, out) {
				return true
			}
			size := 0
			for _, l := range lines[w.lo : w.hi+1] {
				size += len(l) + 1
			}
			if size > maxWindowBytes || len(out) >= maxExpansion || used+size > maxExpansionBytes {
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
