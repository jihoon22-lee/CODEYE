package discovery

import (
	"path"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Limitation codes recorded by module detection.
const (
	LimModuleLimit      = "module_limit_exceeded"
	LimNestedBuildFiles = "nested_build_files_merged"
)

// manifestEcosystem maps a manifest base name to its ecosystem
// (envdoc.ManifestEcosystem).
func manifestEcosystem(base string) (string, bool) { return envdoc.ManifestEcosystem(base) }

// skippedManifest reports whether a manifest lies in a directory the tools
// treat as data: testdata and hidden directories for every ecosystem, and for
// Go also vendor and "_"-prefixed directories (the go-vet planner's rule, so
// discovered Go modules match the go-vet tasks).
func skippedManifest(p, eco string) bool {
	segs := strings.Split(p, "/")
	for _, s := range segs[:len(segs)-1] {
		if s == "testdata" || strings.HasPrefix(s, ".") {
			return true
		}
		if eco == envdoc.EcoGo && (s == "vendor" || strings.HasPrefix(s, "_")) {
			return true
		}
	}
	return false
}

// included maps snapshot-relative paths of included regular files to entries.
func included(m source.Manifest) map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range m.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil {
			out[e.Path] = e
		}
	}
	return out
}

// normalScope returns the recorded scope. An empty kind is full, as for the
// go-vet planner; the server only ever sends "full" or "paths".
func normalScope(s domain.Scope) (envdoc.Scope, bool) {
	switch s.Kind {
	case "", "full":
		return envdoc.Scope{Kind: "full", Paths: []string{}}, true
	case "paths":
		if len(s.Paths) == 0 || len(s.Paths) > 100 {
			return envdoc.Scope{}, false
		}
		ps := append([]string(nil), s.Paths...)
		for _, p := range ps {
			if domain.ValidateEvidencePath(p) != nil {
				return envdoc.Scope{}, false
			}
		}
		sort.Strings(ps)
		for i := 1; i < len(ps); i++ {
			if ps[i] == ps[i-1] {
				return envdoc.Scope{}, false
			}
		}
		return envdoc.Scope{Kind: "paths", Paths: ps}, true
	}
	return envdoc.Scope{}, false
}

// pathInScope reports whether a file path is selected by the scope.
func pathInScope(p string, s envdoc.Scope) bool {
	if s.Kind == "full" {
		return true
	}
	for _, q := range s.Paths {
		if p == q || strings.HasPrefix(p, q+"/") {
			return true
		}
	}
	return false
}

// moduleInScope: a module is in scope when a scope path lies inside its own
// scope (not inside a nested module), or the module lies inside a scope path.
func moduleInScope(m envdoc.Module, s envdoc.Scope) bool {
	if s.Kind == "full" {
		return true
	}
	for _, q := range s.Paths {
		if envdoc.Under(m.Path, q) || inModuleScope(q, m) {
			return true
		}
	}
	return false
}

func inModuleScope(p string, m envdoc.Module) bool {
	if !envdoc.Under(p, m.Path) {
		return false
	}
	for _, x := range m.ExcludedPaths {
		if envdoc.Under(p, x) {
			return false
		}
	}
	return true
}

// DetectModules derives modules from included manifest entries only. A module
// is one (directory, ecosystem) pair; its scope is the directory minus nested
// modules of the same ecosystem. C/C++ build files nested under another C/C++
// module are merged into the outermost one, because add_subdirectory and
// recursive make layouts cannot be told apart without evaluating build files.
func DetectModules(m source.Manifest, scope envdoc.Scope) ([]envdoc.Module, []string) {
	modules, limits, _ := detectModules(m, scope, nil)
	return modules, limits
}

func detectModules(m source.Manifest, scope envdoc.Scope, read readFunc) ([]envdoc.Module, []string, error) {
	type key struct{ eco, dir string }
	found := map[key][]string{}
	for p := range included(m) {
		base := path.Base(p)
		eco, ok := manifestEcosystem(base)
		if !ok || skippedManifest(p, eco) || (read != nil && eco == envdoc.EcoJava) {
			continue
		}
		k := key{eco, envdoc.Dir(p)}
		found[k] = append(found[k], p)
	}
	var lims []string
	// Merge nested C/C++ build directories into their outermost ancestor.
	var cpp []string
	for k := range found {
		if k.eco == envdoc.EcoCpp {
			cpp = append(cpp, k.dir)
		}
	}
	sort.Slice(cpp, func(i, j int) bool {
		return len(cpp[i]) < len(cpp[j]) || (len(cpp[i]) == len(cpp[j]) && cpp[i] < cpp[j])
	})
	for i, d := range cpp {
		for _, outer := range cpp[:i] {
			if _, alive := found[key{envdoc.EcoCpp, outer}]; alive && d != outer && envdoc.Under(d, outer) {
				found[key{envdoc.EcoCpp, outer}] = append(found[key{envdoc.EcoCpp, outer}], found[key{envdoc.EcoCpp, d}]...)
				delete(found, key{envdoc.EcoCpp, d})
				lims = append(lims, LimNestedBuildFiles)
				break
			}
		}
	}
	mods := make([]envdoc.Module, 0, len(found))
	for k, manifests := range found {
		sort.Strings(manifests)
		mods = append(mods, envdoc.Module{ID: envdoc.ModuleID(k.eco, k.dir), Ecosystem: k.eco, Path: k.dir, Manifests: manifests, ExcludedPaths: []string{}})
	}
	if read != nil {
		java, err := javaproject.ScanWithRead(m, read)
		if err != nil {
			return nil, nil, err
		}
		for _, mod := range java {
			mans := append([]string{}, mod.Manifests...)
			mods = append(mods, envdoc.Module{ID: envdoc.ModuleID(envdoc.EcoJava, mod.Dir), Ecosystem: envdoc.EcoJava, Path: mod.Dir, Manifests: mans, ExcludedPaths: []string{}, BuildSystem: mod.BuildSystem, BuildRoot: mod.BuildRoot})
		}
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].Path != mods[j].Path {
			return mods[i].Path < mods[j].Path
		}
		return mods[i].Ecosystem < mods[j].Ecosystem
	})
	if len(mods) > envdoc.MaxModules {
		mods = mods[:envdoc.MaxModules]
		lims = append(lims, LimModuleLimit)
	}
	for i := range mods {
		for j := range mods {
			n := mods[j]
			if i == j || n.Ecosystem != mods[i].Ecosystem || n.Path == mods[i].Path || !envdoc.Under(n.Path, mods[i].Path) {
				continue
			}
			// Only the outermost nested modules; deeper ones are inside them.
			outermost := true
			for _, x := range mods[i].ExcludedPaths {
				if envdoc.Under(n.Path, x) {
					outermost = false
				}
			}
			if outermost {
				var kept []string
				for _, x := range mods[i].ExcludedPaths {
					if !envdoc.Under(x, n.Path) {
						kept = append(kept, x)
					}
				}
				mods[i].ExcludedPaths = append(kept, n.Path)
			}
		}
		sort.Strings(mods[i].ExcludedPaths)
		if mods[i].ExcludedPaths == nil {
			mods[i].ExcludedPaths = []string{}
		}
	}
	for i := range mods {
		mods[i].InScope = moduleInScope(mods[i], scope)
	}
	return mods, lims, nil
}
