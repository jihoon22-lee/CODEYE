package govet

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gograph"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
)

func savePackageGraph(ctx context.Context, job analysis.Job, dir string, in assembleInput, doc Document) (analysis.ArtifactFile, bool) {
	if ctx.Err() != nil || in.List == nil || doc.Module.Path == "" || doc.Platform == nil {
		return analysis.ArtifactFile{}, false
	}
	d := gograph.Document{TaskKey: doc.TaskKey, Snapshot: doc.ManifestDigest, ListSHA: streamRef(in.List.Stdout).SHA256,
		Module: doc.Module.Path, Dir: doc.Module.Dir, Platform: *doc.Platform, Scope: "all_module_packages"}
	if job.Scope.Kind == "paths" || len(doc.Patterns) != 1 || doc.Patterns[0] != "./..." {
		d.Scope = "selected_packages"
	}
	decoded := parseListStream(in.List.Stdout.Data)
	if pack.ExitedZero(in.List) && pack.StreamsComplete(in.List) && decoded.UnparsedBytes == 0 {
		// Count snapshot candidates missing from the package listing before
		// applying the graph's independent file and edge collection limits.
		d.UnlistedFiles = &doc.Coverage.UnlistedGoFiles
	} else {
		d.Limitations = append(d.Limitations, "go_list_incomplete")
	}
	planned := map[string]bool{}
	for _, p := range in.ScopeGoFiles {
		planned[p] = true
	}
	drift := map[string]bool{}
	for _, p := range doc.WorkspaceDrift {
		drift[p] = true
	}
	if len(drift) > 0 {
		d.Limitations = append(d.Limitations, "go_workspace_drift")
	}
	seen := map[string]bool{}
	invalidPackage := false
	var packages []gograph.ListedPackage
	for _, row := range decoded.Packages {
		pkgDir, ok := in.strip(row.Dir, false)
		if !ok || row.Name == "" || seen[row.ImportPath] {
			invalidPackage = true
			continue
		}
		expected := d.Module
		if pkgDir != d.Dir {
			if d.Dir != "." && !strings.HasPrefix(pkgDir, d.Dir+"/") {
				invalidPackage = true
				continue
			}
			rel := pkgDir
			if d.Dir != "." {
				rel = strings.TrimPrefix(pkgDir, d.Dir+"/")
			}
			expected = path.Join(d.Module, rel)
		}
		if row.ImportPath != expected {
			invalidPackage = true
			continue
		}
		selected := func(files []string) []string {
			var out []string
			for _, name := range files {
				p := path.Join(pkgDir, name)
				if path.Base(name) != name || !planned[p] {
					invalidPackage = true
					continue
				}
				out = append(out, p)
			}
			slices.Sort(out)
			return slices.Compact(out)
		}
		production := selected(append(slices.Clone(row.GoFiles), row.CgoFiles...))
		inTests, outTests := selected(row.TestGoFiles), selected(row.XTestGoFiles)
		if len(production)+len(inTests)+len(outTests) == 0 {
			invalidPackage = true
			continue
		}
		seen[row.ImportPath] = true
		packages = append(packages, gograph.ListedPackage{Node: gograph.Node{Import: row.ImportPath, Dir: pkgDir},
			Production: production, InternalTest: inTests, ExternalTest: outTests,
			Ignored: len(selected(row.IgnoredGoFiles)), Incomplete: row.Incomplete || row.Error != nil || len(row.DepsErrors) > 0})
	}
	if invalidPackage {
		d.Limitations = append(d.Limitations, "go_list_rows_unverified")
	}
	slices.SortFunc(packages, func(a, b gograph.ListedPackage) int { return strings.Compare(a.Import, b.Import) })
	root, err := os.OpenRoot(job.Snapshot.SnapshotDir)
	if err != nil {
		return analysis.ArtifactFile{}, false
	}
	defer root.Close()
	read := func(p string) ([]byte, string, bool) {
		e, ok := in.Files[p]
		if !ok || !planned[p] || drift[p] || e.Size == nil || *e.Size > gograph.MaxFileBytes {
			return nil, "", false
		}
		file, err := root.Open(filepath.FromSlash(p))
		if err != nil {
			return nil, "", false
		}
		defer file.Close()
		b, err := io.ReadAll(io.LimitReader(file, gograph.MaxFileBytes+1))
		return b, e.SHA256, err == nil && int64(len(b)) == *e.Size && sha256Hex(b) == e.SHA256
	}
	d = gograph.Create(ctx, d, packages, read)
	if ctx.Err() != nil || gograph.Validate(d, nil) != nil {
		return analysis.ArtifactFile{}, false
	}
	body, err := json.Marshal(d)
	if err != nil || len(body) > gograph.MaxBytes {
		return analysis.ArtifactFile{}, false
	}
	p := filepath.Join(dir, "go-package-graph.json")
	if err := analysis.WriteFileExclusive(p, body); err != nil {
		return analysis.ArtifactFile{}, false
	}
	return analysis.ArtifactFile{Path: p, Role: "other", MediaType: gograph.MediaType}, true
}
