package javapack

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nativepath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
)

const (
	catalogLimit   = 1 << 20
	jarLimit       = 64 << 20
	classpathLimit = 128 << 20
)

var sha256Text = regexp.MustCompile(`^[0-9a-f]{64}$`)

type catalogArtifact struct {
	Coordinate string `json:"coordinate"`
	Path       string `json:"path,omitempty"`
	SHA256     string `json:"sha256"`
}

type classpathCatalog struct {
	MavenRepository string            `json:"maven_repository,omitempty"`
	Artifacts       []catalogArtifact `json:"artifacts"`
}

func loadCatalog(filename string) (classpathCatalog, error) {
	var catalog classpathCatalog
	body, err := readRegular(filename, catalogLimit)
	if err != nil {
		return catalog, err
	}
	canonical, err := domain.CanonicalJSON(body)
	if err != nil {
		return catalog, err
	}
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&catalog); err != nil {
		return catalog, err
	}
	if catalog.Artifacts == nil || len(catalog.Artifacts) > 2048 ||
		catalog.MavenRepository != "" && !absoluteClean(catalog.MavenRepository) {
		return catalog, errors.New("invalid Java classpath catalog")
	}
	seen := map[string]bool{}
	for _, a := range catalog.Artifacts {
		if _, ok := javaproject.ParseCoordinate(a.Coordinate); !ok || seen[a.Coordinate] ||
			!sha256Text.MatchString(a.SHA256) || a.Path != "" && (!absoluteClean(a.Path) || filepath.Ext(a.Path) != ".jar") ||
			a.Path == "" && catalog.MavenRepository == "" {
			return catalog, errors.New("invalid Java classpath catalog artifact")
		}
		seen[a.Coordinate] = true
	}
	return catalog, nil
}

func absoluteClean(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "\x00\r\n")
}

func readRegular(filename string, limit int64) ([]byte, error) {
	st, err := os.Stat(filename)
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("file unavailable")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("file unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("file too large")
	}
	return b, nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func safeArtifact(run *anypack.Run, p string) (string, bool) {
	real, err := nativepath.Resolve(p)
	if err != nil {
		return "", false
	}
	for _, root := range []string{run.Job.SourceDir, run.Job.Snapshot.Root, run.Workspace, run.Job.Scratch} {
		if root == "" {
			continue
		}
		if abs, err := nativepath.Resolve(root); err == nil {
			root = abs
		}
		if within(root, real) {
			return "", false
		}
	}
	return real, true
}

func (a *Analyzer) auxiliaryClasspath(run *anypack.Run, module javaproject.Module, doc *obsdoc.Document) ([]string, string) {
	deps, err := javaproject.DirectDependencies(run.Job.Snapshot.SnapshotDir, run.Job.Snapshot.Manifest, module)
	if err != nil {
		return nil, "snapshot_integrity_failed"
	}
	if module.BuildSystem != "" {
		doc.Limitations = append(doc.Limitations, "java_classpath_direct_only")
	}
	if len(deps.Coordinates)+deps.Unresolved == 0 {
		return nil, ""
	}
	doc.Coverage.Units["java_classpath_direct_dependencies"] = len(deps.Coordinates) + deps.Unresolved
	unresolved := deps.Unresolved
	addUnresolved := func() {
		if unresolved > 0 {
			doc.Coverage.Units["java_classpath_unresolved"] = unresolved
			doc.Limitations = append(doc.Limitations, "java_classpath_unresolved")
		}
	}
	defer addUnresolved()
	if a.ClasspathCatalog == "" {
		unresolved += len(deps.Coordinates)
		return nil, ""
	}
	catalogPath, catalogSafe := safeArtifact(run, a.ClasspathCatalog)
	if !catalogSafe {
		unresolved += len(deps.Coordinates)
		return nil, "java_classpath_catalog_invalid"
	}
	catalog, err := loadCatalog(catalogPath)
	if err != nil {
		unresolved += len(deps.Coordinates)
		return nil, "java_classpath_catalog_invalid"
	}
	byCoordinate := map[string]catalogArtifact{}
	for _, p := range catalog.Artifacts {
		byCoordinate[p.Coordinate] = p
	}
	var jars, bindings []string
	types := map[string]bool{}
	counts := map[string]int{}
	var size int64
	for _, dep := range deps.Coordinates {
		artifact, found := byCoordinate[dep.String()]
		if !found {
			unresolved++
			continue
		}
		filename := artifact.Path
		origin := "catalog"
		if filename == "" {
			origin = "mirror"
			filename = filepath.Join(catalog.MavenRepository, filepath.FromSlash(strings.ReplaceAll(dep.Group, ".", "/")), dep.Artifact, dep.Version, dep.Artifact+"-"+dep.Version+".jar")
			mirror, err := nativepath.Resolve(catalog.MavenRepository)
			jar, jarErr := nativepath.Resolve(filename)
			if err != nil || jarErr != nil || !within(mirror, jar) {
				unresolved++
				continue
			}
		}
		resolved, safe := safeArtifact(run, filename)
		if !safe {
			unresolved++
			continue
		}
		body, err := readRegular(resolved, jarLimit)
		if err != nil || obsdoc.SHA256Hex(body) != artifact.SHA256 || size+int64(len(body)) > classpathLimit {
			unresolved++
			continue
		}
		classes, ok := jarTypes(body, dep)
		if !ok {
			unresolved++
			continue
		}
		for _, name := range classes {
			if types[name] {
				unresolved = len(deps.Coordinates) + deps.Unresolved
				return nil, "java_classpath_conflicting_types"
			}
			types[name] = true
		}
		staged, err := run.WriteFile(fmt.Sprintf("auxiliary-%03d.jar", len(jars)+1), body)
		if err != nil {
			return nil, "scratch_unavailable"
		}
		jars = append(jars, staged)
		bindings = append(bindings, dep.String()+"="+artifact.SHA256)
		counts[origin]++
		size += int64(len(body))
	}
	if len(jars) > 0 {
		for origin, count := range counts {
			doc.Coverage.Units["java_classpath_"+origin+"_verified"] = count
		}
		doc.Coverage.Units["java_classpath_verified"] = len(jars)
		doc.Limitations = append(doc.Limitations, "java_classpath_verified_local_jars")
		digest := obsdoc.SHA256Hex([]byte(strings.Join(bindings, "\n")))
		doc.Target.Properties["java_classpath_digest"] = digest
		doc.RulePack.Selection = append(doc.RulePack.Selection, "java-classpath-sha256="+digest)
	}
	return jars, ""
}

func jarTypes(body []byte, coordinate javaproject.Coordinate) ([]string, bool) {
	container, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || len(container.File) > 32000 {
		return nil, false
	}
	types := map[string]bool{}
	seen := map[string]bool{}
	var classBytes uint64
	var hasMavenMetadata, matchesMavenCoordinate bool
	pomProperties := path.Join("META-INF/maven", coordinate.Group, coordinate.Artifact, "pom.properties")
	for _, f := range container.File {
		if seen[f.Name] {
			return nil, false
		}
		seen[f.Name] = true
		switch {
		case strings.EqualFold(f.Name, "META-INF/MANIFEST.MF"):
			b, ok := readZipEntry(f, 64<<10)
			if !ok || manifestReferencesClasspath(b) {
				return nil, false
			}
		case strings.HasPrefix(f.Name, "META-INF/maven/") && path.Base(f.Name) == "pom.properties":
			hasMavenMetadata = true
			if f.Name == pomProperties {
				b, ok := readZipEntry(f, 16<<10)
				if !ok || !matchesPomProperties(b, coordinate) {
					return nil, false
				}
				matchesMavenCoordinate = true
			}
		case strings.HasSuffix(f.Name, ".class"):
			classBytes += f.UncompressedSize64
			if f.UncompressedSize64 < 4 || f.UncompressedSize64 > 8<<20 || classBytes > 256<<20 ||
				domain.ValidateEvidencePath(f.Name) != nil {
				return nil, false
			}
			r, err := f.Open()
			if err != nil {
				return nil, false
			}
			var magic [4]byte
			_, err = io.ReadFull(r, magic[:])
			r.Close()
			if err != nil || magic != [4]byte{0xca, 0xfe, 0xba, 0xbe} {
				return nil, false
			}
			name := f.Name
			if rest, ok := strings.CutPrefix(name, "META-INF/versions/"); ok {
				_, name, _ = strings.Cut(rest, "/")
			}
			if name != "" && name != "module-info.class" {
				types[name] = true
			}
		}
	}
	if hasMavenMetadata && !matchesMavenCoordinate {
		return nil, false
	}
	out := make([]string, 0, len(types))
	for name := range types {
		out = append(out, name)
	}
	slices.Sort(out)
	return out, len(out) > 0
}

func readZipEntry(f *zip.File, limit int64) ([]byte, bool) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, false
	}
	r, err := f.Open()
	if err != nil {
		return nil, false
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	return b, err == nil && int64(len(b)) <= limit
}

func manifestReferencesClasspath(body []byte) bool {
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r", "\n"), "\n") {
		if key, _, found := strings.Cut(line, ":"); found && strings.EqualFold(strings.TrimSpace(key), "Class-Path") {
			// Even a folded header can direct the compiler to other JARs.
			return true
		}
	}
	return false
}

func matchesPomProperties(b []byte, c javaproject.Coordinate) bool {
	props := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			props[k] = v
		}
	}
	return props["groupId"] == c.Group && props["artifactId"] == c.Artifact && props["version"] == c.Version
}
