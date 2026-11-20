package review

import (
	"encoding/json"
	"os"
	"path"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const packageManifestScanLimit = 256
const packageManifestScanBytes = 2 << 20

type packageManifest struct {
	entry  source.Entry
	fields map[string]json.RawMessage
}

type documentPackageResolver struct {
	stage    *roleStage
	files    map[string]source.Entry
	packages map[string]packageManifest
}

func (r *documentPackageResolver) attach(excerpts []record.Excerpt, documents map[string][]string) error {
	for i, e := range excerpts {
		if !e.PackageReference.IsZero() || e.DirectivePath != "" || !record.PackageGuidePath(e.Path) ||
			(!strings.Contains(e.Text, "import") && !strings.Contains(e.Text, "require")) {
			continue
		}
		excerpts[i] = r.resolve(e, documentedPackageUses(documents[e.Path], e.Start, e.End))
		if err := r.stage.ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (r *documentPackageResolver) resolve(e record.Excerpt, uses []packageUsePassage) record.Excerpt {
	if len(uses) == 0 {
		return e
	}
	if r.packages == nil {
		r.packages = r.stage.packageManifests(r.files)
	}
	var m packageManifest
	var use packageUse
	for _, u := range uses {
		p, local := r.packages[u.name]
		if !local {
			continue
		}
		if p.entry.Path == "" || (use.name != "" && (use.name != u.name || use.require != u.require)) {
			return e
		}
		if use.name == "" {
			m, use = p, u.packageUse
		}
	}
	if use.name != "" {
		e.PackageReference = r.stage.packageReference(r.files, m, use)
		if !e.ValidPackageReference(false) {
			e.PackageReference = record.PackageReference{}
		}
	}
	return e
}

// keepCrossingExample uses a spare document slot for a complete in-snapshot
// package example where the leading limit would otherwise split the call
// from its import. The two windows still use their original source lines.
func (r *documentPackageResolver) keepCrossingExample(e source.Entry, lines []string, first record.Excerpt, remain int) (record.Excerpt, record.Excerpt) {
	if first.End >= len(lines) || !record.PackageGuidePath(e.Path) || !strings.Contains(first.Text, "```") && !strings.Contains(first.Text, "~~~") {
		return first, record.Excerpt{}
	}
	uses := documentedPackageUses(lines, 1, min(len(lines), first.End+roleMaxDocLines-1))
	for _, u := range uses {
		if u.start <= 1 || u.start > first.End || u.end <= first.End || u.end-u.start+1 > roleMaxDocLines {
			continue
		}
		prefix, prefixOK := r.stage.boundedExcerpt(e, "document", "", 1, lines[:u.start-1], min(remain, roleMaxDocBytes))
		if !prefixOK {
			continue
		}
		budget := min(roleMaxDocBytes, remain-record.JSONLen(prefix.Text))
		example, ok := r.stage.boundedExcerpt(e, "document", "", u.start, lines[u.start-1:u.end], budget)
		if !ok || example.End != u.end {
			continue
		}
		example = r.resolve(example, documentedPackageUses(lines, example.Start, example.End))
		if !example.PackageReference.IsZero() {
			return prefix, example
		}
	}
	return first, record.Excerpt{}
}

// The index is complete within its bounded input or empty. Stopping early
// could mistake the first of several manifests of the same name as unique.
func (s *roleStage) packageManifests(files map[string]source.Entry) map[string]packageManifest {
	packages := map[string]packageManifest{}
	root, err := os.OpenRoot(s.j.Snapshot.SnapshotDir)
	if err != nil {
		return packages
	}
	defer root.Close()
	remaining := packageManifestScanBytes
	count := 0
	for _, p := range sortedKeys(files) {
		e := files[p]
		if path.Base(p) != "package.json" || reviewDataPath(p) || !inScope(p, normalizedScope(s.j.Scope)) {
			continue
		}
		count++
		if count > packageManifestScanLimit || *e.Size > min(maxFileBytes, int64(remaining)) || s.ctx.Err() != nil {
			return map[string]packageManifest{}
		}
		remaining -= int(*e.Size)
		body, err := readVerified(root, e)
		if err != nil {
			return map[string]packageManifest{}
		}
		canonical, err := domain.CanonicalJSON(body)
		var fields map[string]json.RawMessage
		if err != nil || json.Unmarshal(canonical, &fields) != nil {
			continue
		}
		var name string
		if json.Unmarshal(fields["name"], &name) != nil || !record.ValidPackageName(name) {
			continue
		}
		// A withheld manifest's name participates in ambiguity detection only.
		// Its fields, name and entry cannot be added to an excerpt or request.
		if _, found := packages[name]; found || s.secretFiles()[p] || record.Sensitive(body) {
			packages[name] = packageManifest{}
		} else {
			packages[name] = packageManifest{entry: e, fields: fields}
		}
	}
	return packages
}

func (s *roleStage) packageReference(files map[string]source.Entry, m packageManifest, use packageUse) record.PackageReference {
	target := packageEntryTarget(m.fields, use.require)
	target = strings.TrimPrefix(target, "./")
	if domain.ValidateEvidencePath(target) != nil || path.Clean(target) != target || strings.ContainsAny(target, "?#%") {
		return record.PackageReference{}
	}
	dir := path.Dir(m.entry.Path)
	entryPath := path.Join(dir, target)
	for sub := path.Dir(entryPath); sub != dir; sub = path.Dir(sub) {
		if _, nested := files[path.Join(sub, "package.json")]; nested {
			return record.PackageReference{}
		}
	}
	entry, exists := files[entryPath]
	if !exists || otherSource(entryPath) != "javascript" || !record.PackageRuntimePath(entryPath) ||
		!inScope(entryPath, normalizedScope(s.j.Scope)) || s.h.secondary().Match(entryPath) != "" {
		return record.PackageReference{}
	}
	body, ok, err := s.readSource(entry)
	if !ok || err != nil || strings.TrimSpace(body) == "" || record.GeneratedHeader([]byte(body)) {
		return record.PackageReference{}
	}
	return record.PackageReference{ManifestPath: m.entry.Path, ManifestSHA256: m.entry.SHA256, EntryPath: entry.Path, EntrySHA256: entry.SHA256,
		Name: use.name, Callable: use.callable, Export: use.exported}
}

func packageEntryTarget(fields map[string]json.RawMessage, require bool) string {
	if exports, set := fields["exports"]; set {
		var object map[string]json.RawMessage
		if json.Unmarshal(exports, &object) == nil {
			if root, rootSet := object["."]; rootSet {
				for key := range object {
					if !strings.HasPrefix(key, ".") {
						return ""
					}
				}
				exports = root
			}
		}
		return packageExportTarget(exports, require, 0)
	}
	main := "index.js"
	if raw, set := fields["main"]; set {
		main = ""
		if json.Unmarshal(raw, &main) != nil || main == "" {
			return ""
		}
	}
	if raw, set := fields["module"]; set && !require {
		var module string
		if json.Unmarshal(raw, &module) != nil || strings.TrimPrefix(module, "./") != strings.TrimPrefix(main, "./") {
			return ""
		}
	}
	return main
}

func packageExportTarget(raw json.RawMessage, require bool, depth int) string {
	var literal string
	if json.Unmarshal(raw, &literal) == nil && strings.HasPrefix(literal, "./") {
		return literal
	}
	var branches map[string]json.RawMessage
	if depth >= 4 || json.Unmarshal(raw, &branches) != nil {
		return ""
	}
	var entry string
	for condition, next := range branches {
		switch condition {
		case "types":
			continue
		case "import":
			if require {
				continue
			}
		case "require":
			if !require {
				continue
			}
		case "node", "default":
		default:
			return ""
		}
		target := packageExportTarget(next, require, depth+1)
		if target == "" || (entry != "" && target != entry) {
			return ""
		}
		entry = target
	}
	return entry
}
