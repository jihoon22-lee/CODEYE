package pack

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Static analysis gaps (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백). An ecosystem with source in scope but
// no analyzer of this build plans one visible, not-required gap task instead
// of a placeholder of another ecosystem's analyzer.
const (
	// GapName owns the gap task keys "pack-gap.<ecosystem>"; no analyzer
	// may use or overlap it.
	GapName = "pack-gap"
	// ReasonAnalyzerPackMissing: source of the ecosystem is in scope and
	// this build has no analyzer for it.
	ReasonAnalyzerPackMissing = "analyzer_pack_missing"
)

// sourceExtensions are the file extensions that put a language ecosystem in
// scope. Build files alone (Makefile, package.json) do not: a Go repository
// with a Makefile has no C/C++ to analyze.
var sourceExtensions = map[string]string{
	".go": "go",
	".py": "python", ".pyi": "python",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "javascript", ".tsx": "javascript", ".mts": "javascript", ".cts": "javascript",
	".rs": "rust", ".java": "java",
	".c": "cpp", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".c++": "cpp",
	".h": "cpp", ".hh": "cpp", ".hpp": "cpp", ".hxx": "cpp", ".h++": "cpp",
}

// DataDir reports a path segment the analyzers treat as data or vendored
// code, never as the project's own source: hidden directories, testdata,
// vendor and node_modules.
func DataDir(seg string) bool {
	return strings.HasPrefix(seg, ".") || seg == "testdata" || seg == "vendor" || seg == "node_modules"
}

// UnderDataDir reports a path with a data directory segment above it.
func UnderDataDir(p string) bool {
	segs := strings.Split(p, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], DataDir)
}

// PathInScope reports whether a snapshot path is covered by the scope ("full"
// or a path list; a scope path covers itself and everything below it).
func PathInScope(p string, scope domain.Scope) bool {
	if scope.Kind == "" || scope.Kind == "full" {
		return true
	}
	for _, s := range scope.Paths {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "./"), "/")
		if s == "" || s == "." || p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

// SourceFiles counts, per language ecosystem, the included manifest files in
// scope with one of its source extensions, outside data and vendored
// directories. It is the one definition of "the ecosystem is in scope" for
// gap planning (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백); a pack decides its own modules.
func SourceFiles(m source.Manifest, scope domain.Scope) map[string]int {
	out := map[string]int{}
	for _, e := range m.Entries {
		if e.Kind != "file" || e.ExcludedReason != "" || !PathInScope(e.Path, scope) {
			continue
		}
		if UnderDataDir(e.Path) {
			continue
		}
		if eco, ok := sourceExtensions[strings.ToLower(path.Ext(e.Path))]; ok {
			out[eco]++
		}
	}
	return out
}

// InScope lists the language ecosystems with source in scope, sorted.
func InScope(m source.Manifest, scope domain.Scope) []string {
	var out []string
	for eco := range SourceFiles(m, scope) {
		out = append(out, eco)
	}
	slices.Sort(out)
	return out
}

// gapDescriptor is the identity recorded in a gap task's envelope: analyzer
// "<ecosystem>.none", tool "none", not required.
func gapDescriptor(eco string) Descriptor {
	return Descriptor{ID: eco + ".none", Name: GapName, Ecosystem: eco, Tool: "none"}
}
