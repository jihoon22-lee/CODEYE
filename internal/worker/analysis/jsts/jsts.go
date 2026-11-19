// Package jsts is the shared part of the W18 JS/TS pack (docs/ANALYZERS.md): module
// planning from the W15 discovery modules, the operator's Node script tools
// run through the shared node toolchain, snapshot path and column mapping,
// the tsconfig chain read as data, and the dependency-gap code list the tsc
// and type-aware ESLint adapters share. It never executes repository code:
// configuration is read as JSON, never loaded as a program.
package jsts

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Ecosystem is the envdoc ecosystem of JS and TS sources.
const Ecosystem = envdoc.EcoJavaScript

// MaxModules bounds the planned JS/TS modules (as go vet's module cap).
const MaxModules = 64

// jsExt and tsExt are the source extensions of the ecosystem
// (pack.SourceFiles counts the same set).
var (
	jsExt = []string{".js", ".jsx", ".mjs", ".cjs"}
	tsExt = []string{".ts", ".tsx", ".mts", ".cts"}
)

// IsSource reports a JS or TS source path by extension.
func IsSource(p string) bool { return IsJS(p) || IsTS(p) }

// IsJS reports a JavaScript source path.
func IsJS(p string) bool { return slices.Contains(jsExt, strings.ToLower(path.Ext(p))) }

// IsTS reports a TypeScript source path (declaration files included).
func IsTS(p string) bool { return slices.Contains(tsExt, strings.ToLower(path.Ext(p))) }

// dataDir is pack's rule: hidden, testdata, vendor and node_modules
// directories hold data or vendored code, never the project's own source.
func dataDir(seg string) bool {
	return strings.HasPrefix(seg, ".") || seg == "testdata" || seg == "vendor" || seg == "node_modules"
}

// inDataDir reports a path below a data directory.
func inDataDir(p string) bool {
	segs := strings.Split(p, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], dataDir)
}

// InScope reports whether a snapshot path is covered by the job scope ("full"
// or a path list; a scope path covers itself and everything below it), the
// rule of pack.SourceFiles.
func InScope(p string, scope domain.Scope) bool {
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

// Module is one planned JS/TS module: a package.json directory found by the
// W15 module discovery, or the implicit root that holds JS/TS sources outside
// every package.json module (docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백: a pack shows the source it cannot
// place instead of dropping it).
type Module struct {
	// Dir is the snapshot-relative module directory ("." = root).
	Dir string
	// Index numbers the module in path order; 0 is the root ("."), the task
	// key of module i>0 is <name>.m<i>.
	Index int
	// Implicit marks the root without a package.json.
	Implicit bool
	// Nested are the directories of the JS/TS modules inside this one; their
	// files belong to their own tasks.
	Nested []string
	// Files are the module's own source files in scope, sorted; TSFiles the
	// TypeScript subset.
	Files, TSFiles []string
	// Patterns are the module-relative path arguments for the scope.
	Patterns []string
	// TSConfig reports <Dir>/tsconfig.json in the manifest.
	TSConfig bool
}

// Included maps included regular file paths of the manifest to entries.
func Included(m source.Manifest) map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range m.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			out[e.Path] = e
		}
	}
	return out
}

// Join joins a module dir and a module-relative path.
func Join(dir, rel string) string {
	switch {
	case dir == "." || dir == "":
		return path.Clean(rel)
	case rel == "." || rel == "":
		return dir
	}
	return path.Clean(dir + "/" + rel)
}

// Rel is p relative to dir (p must be under dir).
func Rel(p, dir string) string {
	switch {
	case dir == "." || dir == "":
		return p
	case p == dir:
		return "."
	}
	return strings.TrimPrefix(p, dir+"/")
}

// Owns reports whether module m owns snapshot path p: p is under m.Dir and
// not under a nested module.
func (m Module) Owns(p string) bool {
	if !envdoc.Under(p, m.Dir) {
		return false
	}
	for _, n := range m.Nested {
		if envdoc.Under(p, n) {
			return false
		}
	}
	return true
}

// PackageModules are the W15 JavaScript modules of the included files: every
// directory with a package.json outside testdata and hidden directories
// (discovery.DetectModules, ecosystem javascript; the test pins the two
// together), with the outermost nested modules of each. It is restated here
// because discovery's tests import the analyzer catalog.
func PackageModules(files map[string]source.Entry) ([]string, map[string][]string) {
	var dirs []string
	for p := range files {
		if path.Base(p) != "package.json" {
			continue
		}
		segs := strings.Split(p, "/")
		skip := false
		for _, s := range segs[:len(segs)-1] {
			if s == "testdata" || strings.HasPrefix(s, ".") {
				skip = true
			}
		}
		if !skip {
			dirs = append(dirs, envdoc.Dir(p))
		}
	}
	sort.Strings(dirs)
	if len(dirs) > envdoc.MaxModules {
		dirs = dirs[:envdoc.MaxModules]
	}
	nested := map[string][]string{}
	for _, d := range dirs {
		out := []string{}
		for _, n := range dirs {
			if n == d || !envdoc.Under(n, d) {
				continue
			}
			outermost := true
			for _, o := range dirs {
				if o != d && o != n && envdoc.Under(o, d) && envdoc.Under(n, o) {
					outermost = false
				}
			}
			if outermost {
				out = append(out, n)
			}
		}
		nested[d] = out
	}
	return dirs, nested
}

// Modules plans the JS/TS modules of a manifest and scope, deterministically
// and without reading file contents: the W15 package.json modules plus the
// implicit root for sources outside them. A module without its own source in
// scope is not planned.
func Modules(m source.Manifest, scope domain.Scope) ([]Module, error) {
	if scope.Kind != "" && scope.Kind != "full" && scope.Kind != "paths" {
		return nil, fmt.Errorf("scope kind %q is not supported", scope.Kind)
	}
	files := Included(m)
	dirs, nested := PackageModules(files)
	hasRoot := slices.Contains(dirs, ".")
	if !hasRoot {
		// Sources outside every package.json module belong to an implicit
		// root whose nested modules are the outermost discovered ones.
		var outer []string
		for _, d := range dirs {
			top := true
			for _, o := range dirs {
				if o != d && envdoc.Under(d, o) {
					top = false
				}
			}
			if top {
				outer = append(outer, d)
			}
		}
		sort.Strings(outer)
		dirs = append(dirs, ".")
		nested["."] = outer
	}
	sort.Slice(dirs, func(i, j int) bool {
		if dirs[i] == "." || dirs[j] == "." {
			return dirs[i] == "."
		}
		return dirs[i] < dirs[j]
	})
	var out []Module
	index := 0
	for _, d := range dirs {
		mod := Module{Dir: d, Implicit: d == "." && !hasRoot, Nested: nested[d], Files: []string{}, TSFiles: []string{}}
		if d != "." {
			index++
			mod.Index = index
		}
		for p := range files {
			if IsSource(p) && !inDataDir(p) && InScope(p, scope) && mod.Owns(p) {
				mod.Files = append(mod.Files, p)
				if IsTS(p) {
					mod.TSFiles = append(mod.TSFiles, p)
				}
			}
		}
		if len(mod.Files) == 0 {
			continue
		}
		sort.Strings(mod.Files)
		sort.Strings(mod.TSFiles)
		_, mod.TSConfig = files[Join(d, "tsconfig.json")]
		mod.Patterns = patterns(mod, scope, files)
		out = append(out, mod)
	}
	if len(out) > MaxModules {
		return nil, fmt.Errorf("snapshot has %d JS/TS modules; at most %d are planned", len(out), MaxModules)
	}
	return out, nil
}

// patterns are the module-relative path arguments that select the module's
// files in scope: "." for a full scope or a scope path containing the module,
// otherwise the scope directories and source files inside the module.
func patterns(mod Module, scope domain.Scope, files map[string]source.Entry) []string {
	if scope.Kind == "" || scope.Kind == "full" {
		return []string{"."}
	}
	set := map[string]bool{}
	for _, s := range scope.Paths {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "./"), "/")
		switch {
		case s == "" || s == "." || envdoc.Under(mod.Dir, s):
			return []string{"."}
		case !mod.Owns(s):
			continue
		}
		if _, isFile := files[s]; isFile {
			if IsSource(s) {
				set[Rel(s, mod.Dir)] = true
			}
			continue
		}
		for _, f := range mod.Files {
			if envdoc.Under(f, s) {
				set[Rel(s, mod.Dir)] = true
				break
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Key is the task key of module mod for an analyzer named name.
func Key(name string, mod Module) string {
	if mod.Index == 0 {
		return name
	}
	return fmt.Sprintf("%s.m%02d", name, mod.Index)
}

// ModuleName reads the "name" of the module's package.json from the
// snapshot (data, verified against the manifest); "" when absent or unsafe.
func ModuleName(snapshotDir string, files map[string]source.Entry, dir string) string {
	p := Join(dir, "package.json")
	e, ok := files[p]
	if !ok {
		return ""
	}
	b, err := govet.ReadSnapshotFile(snapshotDir, p)
	if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
		return ""
	}
	var v struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(b, &v) != nil || !obsdoc.SafeText(v.Name, 256) || strings.ContainsAny(v.Name, "\n\t") {
		return ""
	}
	return v.Name
}
