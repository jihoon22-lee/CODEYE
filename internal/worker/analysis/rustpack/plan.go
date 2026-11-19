package rustpack

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// maxModules bounds the number of tasks one snapshot can create per analyzer.
const maxModules = 64

// Placeholder reasons: Rust source is in scope but no Cargo package can be
// planned for it. The placeholder runs nothing (RepositoryCode=false), so the
// real reason is not hidden behind the repository-code grant.
const (
	ReasonManifestMissing = "cargo_manifest_missing"
	ReasonScopeNoPackage  = "scope_has_no_cargo_package"
)

// moduleTask is one planned Cargo module (an outermost Cargo.toml directory).
type moduleTask struct {
	Key, Title, Dir string
	// Patterns are the scope paths relative to Dir; "." covers the module.
	Patterns []string
	// Files are the in-scope .rs files of the module, snapshot-relative.
	Files []string
}

type planResult struct {
	Modules     []moduleTask
	Placeholder string // non-empty: a single visible placeholder, no modules
}

// skippedSegment reports a path segment that is data, vendored code or build
// output, never the project's own source or a module root.
func skippedSegment(seg string) bool {
	return strings.HasPrefix(seg, ".") || seg == "testdata" || seg == "vendor" || seg == "node_modules" || seg == "target"
}

func skippedPath(p string) bool {
	segs := strings.Split(p, "/")
	for _, s := range segs[:len(segs)-1] {
		if skippedSegment(s) {
			return true
		}
	}
	return false
}

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

func underDir(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

func relTo(p, dir string) string {
	switch {
	case dir == ".":
		return p
	case p == dir:
		return "."
	}
	return strings.TrimPrefix(p, dir+"/")
}

// scopeCovers reports whether a module-relative file is covered by patterns.
func scopeCovers(rel string, patterns []string) bool {
	for _, p := range patterns {
		if p == "." || rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// planModules derives the Cargo modules from the manifest and scope only: a
// module is the directory of a Cargo.toml that has no Cargo.toml in an
// ancestor directory (so a virtual workspace and its members are one module,
// run with --workspace). A nested manifest that is not a workspace member is
// not analyzed; its sources show as files_unlisted (docs/ANALYZERS.md#rust-계획).
func planModules(m source.Manifest, scope domain.Scope, name, title string) (planResult, error) {
	var scopePaths []string
	switch scope.Kind {
	case "", "full":
	case "paths":
		for _, p := range scope.Paths {
			if err := domain.ValidateEvidencePath(p); err != nil {
				return planResult{}, fmt.Errorf("scope path: %w", err)
			}
			scopePaths = append(scopePaths, strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/"))
		}
	default:
		return planResult{}, fmt.Errorf("scope kind %q is not supported", scope.Kind)
	}
	files := govet.IncludedFiles(m)
	var manifestDirs []string
	var rs []string
	for p := range files {
		switch {
		case skippedPath(p):
		case path.Base(p) == "Cargo.toml":
			manifestDirs = append(manifestDirs, dirOf(p))
		case strings.EqualFold(path.Ext(p), ".rs"):
			rs = append(rs, p)
		}
	}
	sort.Strings(manifestDirs)
	sort.Strings(rs)
	// A root has no Cargo.toml in any ancestor directory (membership, not
	// sort order: "-x" sorts before ".").
	has := map[string]bool{}
	for _, d := range manifestDirs {
		has[d] = true
	}
	var roots []string
	for _, d := range manifestDirs {
		nested := false
		for a := d; a != "."; {
			a = dirOf(a)
			if has[a] {
				nested = true
				break
			}
		}
		if !nested {
			roots = append(roots, d)
		}
	}
	var out []moduleTask
	index := 0
	for _, root := range roots {
		patterns := []string{"."}
		if scopePaths != nil {
			set := map[string]bool{}
			for _, sp := range scopePaths {
				switch {
				case sp == "" || sp == "." || underDir(root, sp):
					set["."] = true
				case underDir(sp, root):
					set[relTo(sp, root)] = true
				}
			}
			patterns = patterns[:0]
			if set["."] {
				patterns = append(patterns, ".")
			} else {
				for p := range set {
					patterns = append(patterns, p)
				}
				sort.Strings(patterns)
			}
		}
		var inScope []string
		for _, f := range rs {
			if underDir(f, root) && scopeCovers(relTo(f, root), patterns) {
				inScope = append(inScope, f)
			}
		}
		if len(inScope) == 0 {
			continue
		}
		key := name
		if root != "." {
			index++
			key = fmt.Sprintf("%s.m%02d", name, index)
		}
		out = append(out, moduleTask{Key: key, Title: title + " in " + root, Dir: root, Patterns: patterns, Files: inScope})
	}
	if len(out) > maxModules {
		return planResult{}, fmt.Errorf("snapshot has %d Cargo modules; at most %d are planned", len(out), maxModules)
	}
	if len(out) == 0 && sourceInScope(m, scope) {
		reason := ReasonScopeNoPackage
		if len(roots) == 0 {
			reason = ReasonManifestMissing
		}
		return planResult{Placeholder: reason}, nil
	}
	return planResult{Modules: out}, nil
}

func sourceInScope(m source.Manifest, scope domain.Scope) bool {
	return pack.SourceFiles(m, scope)["rust"] > 0
}
