// Package javaproject reads Java module and compiler declarations from a fixed
// snapshot. Maven and Gradle configuration is data and is never evaluated.
package javaproject

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const MaxManifestBytes = 1 << 20

type Module struct {
	Dir, BuildSystem, BuildRoot, Suffix string
	Manifests                           []string
	Files                               []source.Entry
	Release                             string
	ReleaseUnresolved                   bool
}

type Declaration struct {
	Kind, Value string
	Line        int
}

func BuildSystem(base string) string {
	switch base {
	case "pom.xml":
		return "maven"
	case "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts":
		return "gradle"
	}
	return ""
}

func Source(p string) bool { return strings.HasSuffix(p, ".java") }

func Included(p string) bool {
	for _, seg := range strings.Split(path.Dir(p), "/") {
		switch seg {
		case "target", "build", "out", "vendor", "node_modules", "testdata":
			return false
		}
		if seg != "." && strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return true
}

type projectInfo struct {
	modules              []string
	declarations         []Declaration
	name, group, version string
	parent               *mavenParent
}

type mavenParent struct {
	Group        string  `xml:"groupId"`
	Name         string  `xml:"artifactId"`
	Version      string  `xml:"version"`
	RelativePath *string `xml:"relativePath"`
}

var (
	javaVersion    = regexp.MustCompile(`^(?:1\.)?([1-9][0-9]?)$`)
	gradleRelease  = regexp.MustCompile(`\b(options\.release|languageVersion|sourceCompatibility)\s*(?:=\s*|\.set\(\s*|\s+)([^\n;]+)`)
	releaseLiteral = regexp.MustCompile(`^(?:JavaLanguageVersion\.of\(\s*([0-9]+)\s*\)|JavaVersion\.VERSION_([0-9_]+)|["']([0-9.]+)["']|([0-9]+))(?:\s*\))?$`)
	gradleInclude  = regexp.MustCompile(`(?m)^\s*include\s*(?:\(([^)]*)\)|([^\r\n;]+))`)
	javaPackage    = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_$][A-Za-z0-9_$]*(?:\s*\.\s*[A-Za-z_$][A-Za-z0-9_$]*)*)\s*;`)
	gradleString   = regexp.MustCompile(`^["'](:?[A-Za-z0-9_.-]+(?::[A-Za-z0-9_.-]+)*)["']$`)
)

func Major(value string) (int, bool) {
	parts := javaVersion.FindStringSubmatch(value)
	if parts == nil {
		return 0, false
	}
	n, err := strconv.Atoi(parts[1])
	return n, err == nil
}

func Parse(name string, body []byte) ([]Declaration, error) {
	info, err := inspect(name, body)
	return info.declarations, err
}

func inspect(name string, body []byte) (projectInfo, error) {
	switch name {
	case "pom.xml":
		return parseMaven(body)
	case "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts":
		return parseGradle(name, body), nil
	}
	return projectInfo{}, nil
}

// parseMaven only recognizes Maven's ordinary project-level declarations.
// Profile-specific configuration and external parent POMs are never resolved.
func parseMaven(body []byte) (projectInfo, error) {
	var info projectInfo
	var stack []string
	var compilerPlugin, project bool
	seen := map[string]bool{}
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		line, _ := dec.InputPos()
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return projectInfo{}, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			p := strings.Join(stack, "/")
			if len(stack) == 1 {
				if project || p != "project" {
					return projectInfo{}, fmt.Errorf("expected one Maven project element")
				}
				project = true
			}
			if p == "project/parent" {
				var parent mavenParent
				if err := dec.DecodeElement(&parent, &t); err != nil {
					return projectInfo{}, err
				}
				info.parent = &parent
				stack = stack[:len(stack)-1]
				continue
			}
			if p == "project/build/plugins/plugin" {
				var plugin struct {
					Artifact string `xml:"artifactId"`
					Config   struct {
						Release string `xml:"release"`
						Source  string `xml:"source"`
					} `xml:"configuration"`
				}
				if err := dec.DecodeElement(&plugin, &t); err != nil {
					return projectInfo{}, err
				}
				if plugin.Artifact == "maven-compiler-plugin" && !compilerPlugin {
					compilerPlugin = true
					for _, d := range []Declaration{{"maven_plugin_release", strings.TrimSpace(plugin.Config.Release), line}, {"maven_plugin_source", strings.TrimSpace(plugin.Config.Source), line}} {
						if d.Value != "" {
							info.declarations = append(info.declarations, d)
						}
					}
				}
				stack = stack[:len(stack)-1]
				continue
			}
			kind := map[string]string{
				"project/properties/maven.compiler.release": "maven_compiler_release",
				"project/properties/maven.compiler.source":  "maven_compiler_source",
				"project/properties/java.version":           "maven_java_version",
				"project/groupId":                           "group",
				"project/artifactId":                        "name",
				"project/version":                           "version",
				"project/modules/module":                    "module",
			}[p]
			if kind != "" {
				var value string
				if err := dec.DecodeElement(&value, &t); err != nil {
					return projectInfo{}, err
				}
				stack = stack[:len(stack)-1]
				value = strings.TrimSpace(value)
				switch kind {
				case "group":
					info.group = value
				case "name":
					info.name = value
				case "version":
					info.version = value
				case "module":
					info.modules = append(info.modules, value)
				default:
					if value != "" && !seen[kind] {
						seen[kind] = true
						info.declarations = append(info.declarations, Declaration{kind, value, line})
					}
				}
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		}
	}
	if !project {
		return projectInfo{}, fmt.Errorf("missing Maven project element")
	}
	return info, nil
}

func parseGradle(name string, body []byte) projectInfo {
	var info projectInfo
	text := gradleComments(body)
	if strings.HasPrefix(name, "settings.") {
		for _, match := range gradleInclude.FindAllStringSubmatch(text, -1) {
			for _, item := range strings.Split(match[1]+match[2], ",") {
				if m := gradleString.FindStringSubmatch(strings.TrimSpace(item)); m != nil {
					info.modules = append(info.modules, strings.ReplaceAll(strings.TrimPrefix(m[1], ":"), ":", "/"))
				}
			}
		}
		return info
	}
	seen := map[string]bool{}
	for i, line := range strings.Split(text, "\n") {
		m := gradleRelease.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		kind := map[string]string{"options.release": "gradle_compiler_release", "languageVersion": "gradle_toolchain_version", "sourceCompatibility": "gradle_source_compatibility"}[m[1]]
		value := "unresolved"
		if lit := releaseLiteral.FindStringSubmatch(strings.TrimSpace(m[2])); lit != nil {
			value = strings.ReplaceAll(strings.Join(lit[1:], ""), "_", ".")
		}
		info.declarations = append(info.declarations, Declaration{kind, value, i + 1})
	}
	return info
}

// gradleComments blanks comments without changing line numbers or quoted
// literals. This recognizes statement-shaped declarations, never Gradle code.
func gradleComments(body []byte) string {
	b := bytes.Clone(body)
	var quote byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(b) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c != '/' || i+1 >= len(b) {
			continue
		}
		switch b[i+1] {
		case '/':
			for i < len(b) && b[i] != '\n' {
				b[i] = ' '
				i++
			}
		case '*':
			b[i], b[i+1] = ' ', ' '
			i += 2
			for i < len(b) {
				if i+1 < len(b) && b[i] == '*' && b[i+1] == '/' {
					b[i], b[i+1] = ' ', ' '
					i++
					break
				}
				if b[i] != '\n' {
					b[i] = ' '
				}
				i++
			}
		}
	}
	return string(b)
}

// SourceName is the Java package-relative name that appears in bytecode
// reports. The caller may use it only to select among verified input files.
func SourceName(filename string, body []byte) string {
	match := javaPackage.FindStringSubmatch(gradleComments(body))
	if match == nil {
		return path.Base(filename)
	}
	packagePath := strings.Join(strings.Fields(strings.ReplaceAll(match[1], ".", "/")), "")
	return path.Join(packagePath, path.Base(filename))
}

func ReadManifest(root *os.Root, e source.Entry) ([]byte, error) {
	f, err := root.Open(e.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != *e.Size {
		return nil, fmt.Errorf("manifest file changed")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxManifestBytes+1))
	sum := sha256.Sum256(b)
	if err != nil || int64(len(b)) != *e.Size || hex.EncodeToString(sum[:]) != e.SHA256 {
		return nil, fmt.Errorf("manifest content changed")
	}
	return b, nil
}

func Scan(snapshotDir string, manifest source.Manifest) ([]Module, error) {
	root, err := os.OpenRoot(snapshotDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return ScanWithRead(manifest, func(e source.Entry) ([]byte, error) { return ReadManifest(root, e) })
}

// ScanWithRead shares module detection with environment discovery. read must verify bytes against the manifest.
func ScanWithRead(manifest source.Manifest, read func(source.Entry) ([]byte, error)) ([]Module, error) {
	modules := map[string]*Module{}
	links := map[string]string{}
	info := map[string][]Declaration{}
	maven := map[string]projectInfo{}
	var files []source.Entry
	for _, e := range manifest.Entries {
		if e.Kind != "file" || e.ExcludedReason != "" || e.Size == nil || !Included(e.Path) {
			continue
		}
		if Source(e.Path) {
			files = append(files, e)
			continue
		}
		base, dir := path.Base(e.Path), path.Dir(e.Path)
		system := BuildSystem(base)
		if system == "" {
			continue
		}
		if modules[dir] == nil {
			modules[dir] = &Module{Dir: dir, BuildSystem: system, BuildRoot: dir}
		} else if modules[dir].BuildSystem != system {
			modules[dir].BuildSystem = "mixed"
		}
		module := modules[dir]
		module.Manifests = append(module.Manifests, e.Path)
		if *e.Size > MaxManifestBytes || e.Encoding != "utf-8" {
			module.ReleaseUnresolved = true
			continue
		}
		body, err := read(e)
		if err != nil {
			return nil, err
		}
		parsed, err := inspect(base, body)
		if err != nil {
			module.ReleaseUnresolved = true
			continue
		}
		if system == "maven" {
			maven[e.Path] = parsed
		}
		info[dir] = append(info[dir], parsed.declarations...)
		for _, name := range parsed.modules {
			child := path.Join(dir, name)
			if domain.ValidateEvidencePath(name) == nil && child != dir && (dir == "." || strings.HasPrefix(child, dir+"/")) {
				if previous, ok := links[child]; !ok || len(dir) > len(previous) {
					links[child] = dir
				}
			}
		}
	}
	for child, parent := range links {
		if modules[child] == nil && modules[parent].BuildSystem == "gradle" {
			modules[child] = &Module{Dir: child, BuildSystem: "gradle", BuildRoot: parent}
		}
	}
	for _, e := range files {
		dir := path.Dir(e.Path)
		for {
			if m := modules[dir]; m != nil {
				m.Files = append(m.Files, e)
				break
			}
			if dir == "." {
				if modules[dir] == nil {
					modules[dir] = &Module{Dir: dir, BuildRoot: dir}
				}
				modules[dir].Files = append(modules[dir].Files, e)
				break
			}
			dir = path.Dir(dir)
		}
	}
	out := make([]Module, 0, len(modules))
	for _, m := range modules {
		declarations := info[m.Dir]
		if m.BuildSystem == "maven" {
			var unresolved bool
			declarations, unresolved = mavenDeclarations(m.Dir, maven)
			m.ReleaseUnresolved = m.ReleaseUnresolved || unresolved
		}
		m.Release, m.ReleaseUnresolved = release(declarations, m.ReleaseUnresolved)

		for parent, ok := links[m.BuildRoot]; ok && modules[parent] != nil && modules[parent].BuildSystem == m.BuildSystem; parent, ok = links[m.BuildRoot] {
			m.BuildRoot = parent
		}
		sort.Strings(m.Manifests)
		slices.SortFunc(m.Files, func(a, b source.Entry) int { return strings.Compare(a.Path, b.Path) })
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b Module) int { return strings.Compare(a.Dir, b.Dir) })
	count := 0
	for i := range out {
		if out[i].Dir != "." {
			count++
			out[i].Suffix = fmt.Sprintf(".m%02d", count)
		}
	}
	return out, nil
}

// mavenDeclarations follows a verified local parent by coordinate. Reactor
// membership alone does not inherit Maven compiler settings.
func mavenDeclarations(dir string, poms map[string]projectInfo) ([]Declaration, bool) {
	var declarations []Declaration
	seen := map[string]bool{}
	pom := path.Join(dir, "pom.xml")
	for {
		model, ok := poms[pom]
		if !ok || seen[pom] {
			return declarations, len(declarations) == 0
		}
		seen[pom] = true
		declarations = append(declarations, model.declarations...)
		parent := model.parent
		if parent == nil {
			return declarations, false
		}
		rel := "../pom.xml"
		if parent.RelativePath != nil {
			rel = strings.TrimSpace(*parent.RelativePath)
		}
		if rel == "" || strings.HasPrefix(rel, "/") {
			return declarations, len(declarations) == 0
		}
		pom = path.Join(path.Dir(pom), rel)
		if path.Base(pom) != "pom.xml" {
			pom = path.Join(pom, "pom.xml")
		}
		model, ok = poms[pom]
		if !ok || model.name == "" || model.name != strings.TrimSpace(parent.Name) ||
			mavenCoordinate(model.group, model.parent, func(p *mavenParent) string { return p.Group }) != strings.TrimSpace(parent.Group) ||
			mavenCoordinate(model.version, model.parent, func(p *mavenParent) string { return p.Version }) != strings.TrimSpace(parent.Version) {
			return declarations, len(declarations) == 0
		}
	}
}

func mavenCoordinate(value string, parent *mavenParent, get func(*mavenParent) string) string {
	if value == "" && parent != nil {
		return strings.TrimSpace(get(parent))
	}
	return value
}

func release(declarations []Declaration, unresolved bool) (string, bool) {
	// An explicit compiler release wins over source syntax and toolchain
	// selection. Even when unresolvable, its value must not be guessed.
	for _, kind := range []string{"maven_plugin_release", "maven_compiler_release", "gradle_compiler_release", "maven_plugin_source", "maven_compiler_source", "gradle_source_compatibility", "gradle_toolchain_version", "maven_java_version"} {
		for _, d := range declarations {
			if d.Kind == kind {
				v := d.Value
				if v == "${java.version}" {
					for _, property := range declarations {
						if property.Kind == "maven_java_version" {
							v = property.Value
						}
					}
				}
				n, ok := Major(v)
				if !ok {
					return "", true
				}
				return strconv.Itoa(n), unresolved
			}
		}
	}
	return "", unresolved
}
