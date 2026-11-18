package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Review input selection for the source ecosystems besides Go (W24 phase
// 2c). Go files, README/AGENTS documents and go.mod keep the W11/W15 rules
// byte for byte (priority in context.go, codeCorpus and goIgnored in
// r1_inputs.go); this file only decides the C/C++, Python, JS/TS and Rust
// files. Classification is by path (record/source.go): no file is parsed.

// generatedDirs are directory names that hold build output, installed
// dependencies or copied third-party code in the non-Go ecosystems. The
// snapshot already drops node_modules and __pycache__ everywhere and
// build/dist/target/venv at its top (source.exclusion); these catch the
// nested ones (a crate's own target, packages/x/dist, a C/C++ build tree).
// Go is exempt: Go packages named build or dist are ordinary source.
var generatedDirs = []string{"build", "dist", "target", "coverage", "htmlcov", "site-packages", "venv", "__pycache__", "third_party"}

// reviewDataPath reports a non-Go path below a directory that is not the
// project's own source: the analyzer packs' data directories (hidden,
// testdata, vendor, node_modules: anypack.DataDir, the rule of
// pack.SourceFiles) or one of generatedDirs.
func reviewDataPath(p string) bool {
	segs := strings.Split(p, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], func(seg string) bool {
		return anypack.DataDir(seg) || slices.Contains(generatedDirs, strings.ToLower(seg))
	})
}

// otherSource returns the ecosystem of a non-Go source path a review may
// read, or "". A Go path (any case of .go) is never "other": it follows the
// Go rules or, as before W24, is not selected.
func otherSource(p string) string {
	eco := record.SourceEcosystem(p)
	if eco == "" || eco == "go" || reviewDataPath(p) {
		return ""
	}
	return eco
}

// entryPoint reports a conventional program or library entry file of a
// non-Go ecosystem. Test paths are never entry points.
func entryPoint(p string) bool {
	if record.IsTestPath(p) {
		return false
	}
	base := strings.ToLower(path.Base(p))
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch record.SourceEcosystem(p) {
	case "python":
		return slices.Contains([]string{"__main__", "main", "app", "cli", "manage"}, stem)
	case "javascript":
		return slices.Contains([]string{"index", "main", "app", "server", "cli"}, stem)
	case "rust":
		return base == "main.rs" || base == "lib.rs"
	case "cpp":
		return stem == "main" && slices.Contains([]string{".c", ".cc", ".cpp", ".cxx", ".c++"}, ext)
	}
	return false
}

// otherPriority is the review-v1 rank of a path the Go rules do not select
// (context.go priority): entry points 0, other sources 2, build manifests 3
// and tests 4, for ecosystems with source in scope only (pack.InScope:
// build files alone never add a manifest). 5 is not selected.
func otherPriority(name string, ecosystems []string) (rank int, kind string) {
	if eco := record.ManifestEcosystem(name); eco != "" && eco != "go" {
		if !slices.Contains(ecosystems, eco) || reviewDataPath(name) {
			return 5, ""
		}
		return 3, "config"
	}
	eco := otherSource(name)
	switch {
	case eco == "" || !slices.Contains(ecosystems, eco):
		return 5, ""
	case record.IsTestPath(name):
		return 4, "test"
	case entryPoint(name):
		return 0, "code"
	}
	return 2, "code"
}

// otherModules indexes the module roots of the non-Go ecosystems: the
// directories of their build manifests (record.ManifestEcosystem) outside
// data directories, in or out of scope, as go.mod directories are for Go.
type otherModules map[string][]string // ecosystem -> sorted directories

func otherModuleRoots(files map[string]source.Entry) otherModules {
	out := otherModules{}
	for _, p := range sortedKeys(files) {
		eco := record.ManifestEcosystem(p)
		if eco == "" || eco == "go" || reviewDataPath(p) {
			continue
		}
		dir := record.DocumentDir(p)
		if !slices.Contains(out[eco], dir) {
			out[eco] = append(out[eco], dir)
		}
	}
	for eco := range out {
		slices.Sort(out[eco])
	}
	return out
}

// owner is the deepest module root of the file's own ecosystem, "." when
// none contains it.
func (m otherModules) owner(eco, p string) string {
	best := "."
	for _, d := range m[eco] {
		if record.ScopeUnder(p, d) && len(d) >= len(best) {
			best = d
		}
	}
	return best
}

// groupKey is the round-robin group of a non-Go file in the r1 code corpus:
// its module root and ecosystem. A Go module's key is its bare directory
// (codeCorpus); a NUL never occurs in a snapshot path.
func groupKey(dir, eco string) string { return dir + "\x00" + eco }

// groupOrder is the order in which codeCorpus visits the groups in every
// round: the first group of each ecosystem (ecosystem names sorted), then
// the second of each, and so on; an ecosystem's groups go by directory. So
// the first round reaches every language in scope before a second module of
// any language, and a Go-only snapshot keeps the sorted module order.
func groupOrder(byModule map[string][]source.Entry) []string {
	per := map[string][]string{}
	for _, k := range sortedKeys(byModule) {
		eco := "go"
		if _, e, ok := strings.Cut(k, "\x00"); ok {
			eco = e
		}
		per[eco] = append(per[eco], k)
	}
	var out []string
	for i := 0; len(out) < len(byModule); i++ {
		for _, eco := range sortedKeys(per) {
			if i < len(per[eco]) {
				out = append(out, per[eco][i])
			}
		}
	}
	return out
}

// otherRank is the r1 code corpus rank of a non-Go file within its group
// (Go: main.go 0, other 1, tests 2).
func otherRank(p string) int {
	switch {
	case entryPoint(p):
		return 0
	case record.IsTestPath(p):
		return 2
	}
	return 1
}

// windowKind is the excerpt kind of an interpret window on p, or "" when p
// gets no window. Go keeps its W15 rule; other source is classified by
// path; any other file (a manifest, a configuration, a document) gets none.
func windowKind(p string) string {
	switch {
	case strings.HasSuffix(p, "_test.go"):
		return "test"
	case strings.HasSuffix(p, ".go"):
		return "code"
	case record.SourceEcosystem(p) == "go":
		return ""
	}
	return record.SourceKind(p)
}

// otherModuleDirs lists the module roots of the non-Go excerpts in code
// whose build manifest is in scope, as investigate's modules list names the
// go.mod directories in scope.
func (s *r1Stage) otherModuleDirs(code []record.Excerpt) []string {
	files := s.entries()
	roots := otherModuleRoots(files)
	scope := normalizedScope(s.j.Scope)
	var out []string
	for _, x := range code {
		eco := otherSource(x.Path)
		if eco == "" {
			continue
		}
		dir := roots.owner(eco, x.Path)
		for _, p := range sortedKeys(files) {
			if record.ManifestEcosystem(p) == eco && record.DocumentDir(p) == dir && inScope(p, scope) && !slices.Contains(out, dir) {
				out = append(out, dir)
			}
		}
	}
	return out
}
