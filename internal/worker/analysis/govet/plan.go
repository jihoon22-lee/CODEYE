package govet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// maxModules bounds the number of go-vet tasks one snapshot can create.
const maxModules = 64

// plannedTask is one go-vet task derived purely from the snapshot manifest and scope.
type plannedTask struct {
	Key         string
	Title       string
	ModuleDir   string   // snapshot-relative, "." = root
	Patterns    []string // "./..."-style package patterns, relative to ModuleDir
	Unsupported string   // limitation code when the task cannot run
}

// includedFiles maps snapshot-relative paths of included regular files to entries.
func includedFiles(m source.Manifest) map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range m.Entries {
		if e.ExcludedReason == "" && e.Kind == "file" {
			out[e.Path] = e
		}
	}
	return out
}

// goIgnoredDir reports whether the go command's ./... pattern skips a path
// segment (vendor, testdata, names starting with "." or "_").
func goIgnoredDir(seg string) bool {
	return seg == "vendor" || seg == "testdata" || strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "_")
}

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

func underDir(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

func skippedPath(p, base string) bool {
	rel := p
	if base != "." {
		rel = strings.TrimPrefix(p, base+"/")
	}
	segs := strings.Split(rel, "/")
	for _, s := range segs[:len(segs)-1] {
		if goIgnoredDir(s) {
			return true
		}
	}
	return false
}

// moduleDirs returns the go.mod directories the go command would treat as
// separate modules, sorted.
func moduleDirs(files map[string]source.Entry) []string {
	var dirs []string
	for p := range files {
		if path.Base(p) != "go.mod" || skippedPath(p, ".") {
			continue
		}
		dirs = append(dirs, dirOf(p))
	}
	sort.Strings(dirs)
	return dirs
}

// owningModule returns the deepest module dir containing p.
func owningModule(p string, modules []string) (string, bool) {
	best, found := "", false
	for _, m := range modules {
		if underDir(p, m) && (!found || len(m) > len(best)) {
			best, found = m, true
		}
	}
	return best, found
}

func hasGoFiles(files map[string]source.Entry) bool {
	for p := range files {
		if strings.HasSuffix(p, ".go") {
			return true
		}
	}
	return false
}

func relTo(p, dir string) string {
	if dir == "." {
		return p
	}
	if p == dir {
		return "."
	}
	return strings.TrimPrefix(p, dir+"/")
}

// planTasks is deterministic for a given manifest and scope; Run re-derives it.
func planTasks(m source.Manifest, scope domain.Scope) ([]plannedTask, error) {
	files := includedFiles(m)
	modules := moduleDirs(files)
	if len(modules) == 0 {
		reason := "no_go_files"
		if hasGoFiles(files) {
			reason = "go_module_missing"
		}
		return []plannedTask{{Key: "go-vet", Title: "go vet (no Go module)", ModuleDir: ".", Unsupported: reason}}, nil
	}
	patterns := map[string]map[string]bool{}
	add := func(mod, pattern string) {
		if patterns[mod] == nil {
			patterns[mod] = map[string]bool{}
		}
		patterns[mod][pattern] = true
	}
	switch scope.Kind {
	case "", "full":
		for _, mod := range modules {
			add(mod, "./...")
		}
	case "paths":
		for _, p := range scope.Paths {
			if err := domain.ValidateEvidencePath(p); err != nil {
				return nil, fmt.Errorf("scope path: %w", err)
			}
			isFile := false
			if _, ok := files[p]; ok {
				isFile = true
			}
			// Modules wholly inside a scope directory are vetted entirely.
			if !isFile {
				for _, mod := range modules {
					if mod != "." && strings.HasPrefix(mod, p+"/") {
						add(mod, "./...")
					}
				}
			}
			mod, ok := owningModule(p, modules)
			if !ok {
				continue
			}
			rel := relTo(p, mod)
			switch {
			case isFile && strings.HasSuffix(p, ".go"):
				d := relTo(dirOf(p), mod)
				if d == "." {
					add(mod, ".")
				} else {
					add(mod, "./"+d)
				}
			case isFile:
				// Non-Go file: nothing for go vet to analyze.
			case rel == ".":
				add(mod, "./...")
			default:
				add(mod, "./"+rel+"/...")
			}
		}
	default:
		return nil, fmt.Errorf("scope kind %q is not supported", scope.Kind)
	}
	var tasks []plannedTask
	index := 0
	for _, mod := range modules {
		set := patterns[mod]
		if len(set) == 0 {
			continue
		}
		var list []string
		if set["./..."] {
			list = []string{"./..."}
		} else {
			for p := range set {
				list = append(list, p)
			}
			sort.Strings(list)
		}
		key := "go-vet"
		if mod != "." {
			index++
			key = fmt.Sprintf("go-vet.m%02d", index)
		}
		tasks = append(tasks, plannedTask{Key: key, Title: "go vet " + strings.Join(list, " ") + " in " + mod, ModuleDir: mod, Patterns: list})
	}
	if len(tasks) > maxModules {
		return nil, fmt.Errorf("snapshot has %d Go modules; at most %d are planned", len(tasks), maxModules)
	}
	if len(tasks) == 0 {
		return []plannedTask{{Key: "go-vet", Title: "go vet (scope has no Go packages)", ModuleDir: ".", Unsupported: "scope_has_no_go_packages"}}, nil
	}
	return tasks, nil
}

// patternCovers reports whether a module-relative Go file is inside the patterns.
func patternCovers(rel string, patterns []string) bool {
	dir := dirOf(rel)
	for _, p := range patterns {
		switch {
		case p == "./...":
			return true
		case p == ".":
			if dir == "." {
				return true
			}
		case strings.HasSuffix(p, "/..."):
			base := strings.TrimSuffix(strings.TrimPrefix(p, "./"), "/...")
			if underDir(dir, base) {
				return true
			}
		default:
			if dir == strings.TrimPrefix(p, "./") {
				return true
			}
		}
	}
	return false
}

// goFilesInScope lists included .go files that `go list <patterns>` could
// report for the module: under the module, not in a nested module, not in a
// directory the go command skips.
func goFilesInScope(files map[string]source.Entry, modules []string, mod string, patterns []string) []string {
	var out []string
	for p := range files {
		if !strings.HasSuffix(p, ".go") || !underDir(p, mod) {
			continue
		}
		if owner, _ := owningModule(p, modules); owner != mod {
			continue
		}
		if base := path.Base(p); strings.HasPrefix(base, "_") || strings.HasPrefix(base, ".") {
			continue // the go command ignores these file names
		}
		if skippedPath(p, mod) || !patternCovers(relTo(p, mod), patterns) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// modulePath reads the module directive from go.mod bytes (best effort).
func modulePath(gomod []byte) string {
	for _, line := range strings.Split(string(gomod), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "module"))
			if i := strings.Index(rest, "//"); i >= 0 {
				rest = strings.TrimSpace(rest[:i])
			}
			return strings.Trim(rest, `"`+"`")
		}
	}
	return ""
}

// parseModEdit reads `go mod edit -json` (the go command's own go.mod
// parser) and returns the module path and the replace targets that are
// directories (a replacement without a version is a filesystem path).
func parseModEdit(stdout []byte) (modulePath string, dirTargets []string, ok bool) {
	var m struct {
		Module  struct{ Path string }
		Replace []struct {
			New struct{ Path, Version string }
		}
	}
	dec := json.NewDecoder(bytes.NewReader(stdout))
	if err := dec.Decode(&m); err != nil {
		return "", nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", nil, false
	}
	for _, r := range m.Replace {
		if r.New.Version == "" {
			dirTargets = append(dirTargets, r.New.Path)
		}
	}
	return m.Module.Path, dirTargets, true
}

// escapingReplaces returns directory replace targets that are absolute or
// leave the snapshot root when resolved lexically from the module directory.
// The snapshot holds no symlinks (the snapshot does not follow them), so a lexical
// check matches what the go command would open inside the workspace.
func escapingReplaces(targets []string, moduleDir string, windows bool) []string {
	var out []string
	for _, t := range targets {
		// Backslashes are separators on Windows and invalid elsewhere;
		// treating them as separators everywhere only adds refusals.
		norm := strings.ReplaceAll(t, `\`, "/")
		_ = windows
		// The stream was redacted by the executor: a target that contained the
		// workspace path (or any redacted value) was absolute, so it is
		// refused rather than resolved as relative.
		if strings.Contains(t, analysis.RedactedMarker) || norm == "" || strings.HasPrefix(norm, "/") || strings.HasPrefix(norm, `\`) || (len(norm) >= 2 && norm[1] == ':') || strings.ContainsRune(norm, 0) {
			out = append(out, t)
			continue
		}
		joined := path.Clean(path.Join(moduleDir, norm))
		if joined == ".." || strings.HasPrefix(joined, "../") {
			out = append(out, t)
		}
	}
	return out
}
