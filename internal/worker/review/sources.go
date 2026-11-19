package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Review input selection for the source ecosystems besides Go. Go files,
// README/AGENTS documents and go.mod keep their own rules byte for byte
// (priority in context.go, codeCorpus and goIgnored in rolepack_inputs.go);
// this file only decides the C/C++, Python, JS/TS and Rust files.
// Classification is by path (record/source.go): no file is parsed.

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
// Go rules or is not selected.
func otherSource(p string) string {
	eco := record.SourceEcosystem(p)
	if eco == "" || eco == "go" || reviewDataPath(p) {
		return ""
	}
	return eco
}

// limExcludedSource says that non-Go source in scope lies below an excluded
// directory (reviewDataPath) and was not offered to the model, so an empty
// or thin code context is explained.
const limExcludedSource = "source_in_excluded_directories"

// excludedSource reports non-Go source that reviewDataPath leaves out.
func excludedSource(p string) bool {
	eco := record.SourceEcosystem(p)
	return eco != "" && eco != "go" && reviewDataPath(p)
}

// entryPoint reports a conventional program or library entry file of a
// non-Go ecosystem. Test paths are never entry points. A JS/TS index file is
// one only in its module root (the deepest package.json or tsconfig.json
// directory, else ".") or that root's src: barrel index files in every
// directory would otherwise fill the context before any real source.
func entryPoint(p string, roots otherModules) bool {
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
		if stem == "index" {
			root, dir := roots.owner("javascript", p), record.DocumentDir(p)
			return dir == root || dir == path.Join(root, "src")
		}
		return slices.Contains([]string{"main", "app", "server", "cli"}, stem)
	case "rust":
		return base == "main.rs" || base == "lib.rs"
	case "cpp":
		return stem == "main" && slices.Contains([]string{".c", ".cc", ".cpp", ".cxx", ".c++"}, ext)
	}
	return false
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

// groupKey is the round-robin group of a non-Go file in the role-pack code corpus:
// its module root and ecosystem. A Go module's key is its bare directory
// (codeCorpus); a NUL never occurs in a snapshot path.
func groupKey(dir, eco string) string { return dir + "\x00" + eco }

// groupOrder is the order in which codeCorpus visits the groups in every
// round: the first group of each ecosystem (ecosystem names sorted), then
// the second of each, and so on; an ecosystem's groups go by directory. So
// the first round reaches every language in scope before a second module of
// any language, and a Go-only snapshot keeps the sorted module order. The
// groups of secondary modules (test input, examples, third-party material:
// record.SecondaryRule) follow every primary group, in the same order among
// themselves.
func groupOrder(byModule map[string][]source.Entry, secondary func(string) bool) []string {
	var out []string
	for _, want := range []bool{false, true} {
		per := map[string][]string{}
		n := 0
		for _, k := range sortedKeys(byModule) {
			if secondary(k) != want {
				continue
			}
			eco := "go"
			if _, e, ok := strings.Cut(k, "\x00"); ok {
				eco = e
			}
			per[eco] = append(per[eco], k)
			n++
		}
		for i, start := 0, len(out); len(out)-start < n; i++ {
			for _, eco := range sortedKeys(per) {
				if i < len(per[eco]) {
					out = append(out, per[eco][i])
				}
			}
		}
	}
	return out
}

// otherRank is the role-pack code corpus rank of a non-Go file within its group
// (Go: main.go 0, other 1, tests 2).
func otherRank(p string, roots otherModules) int {
	switch {
	case entryPoint(p, roots):
		return 0
	case record.IsTestPath(p):
		return 2
	}
	return 1
}

// windowKind is the excerpt kind of an interpret window on p, or "" when p
// gets no window. Go keeps its own rule; other source is classified by
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
func (s *roleStage) otherModuleDirs(code []record.Excerpt) []string {
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
