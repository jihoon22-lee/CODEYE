package javaproject

import (
	"encoding/xml"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Coordinate is an unclassified JAR with an exact version.
type Coordinate struct {
	Group, Artifact, Version string
}

func (c Coordinate) String() string { return c.Group + ":" + c.Artifact + ":" + c.Version }

var (
	coordinatePart   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	propertyName     = regexp.MustCompile(`^\$\{([A-Za-z0-9_.-]+)\}$`)
	gradleBlock      = regexp.MustCompile(`^\s*dependencies\s*\{\s*$`)
	gradleCompile    = regexp.MustCompile(`^\s*(?:implementation|api|compileOnlyApi|compileOnly)\b`)
	gradleCoordinate = regexp.MustCompile(`^\s*(?:implementation|api|compileOnlyApi|compileOnly)\s*(?:\(\s*)?["']([^"']+)["']\s*\)?\s*;?\s*$`)
)

func ParseCoordinate(value string) (Coordinate, bool) {
	p := strings.Split(value, ":")
	if len(p) != 3 || strings.HasSuffix(strings.ToUpper(p[2]), "-SNAPSHOT") {
		return Coordinate{}, false
	}
	for _, part := range p {
		if !coordinatePart.MatchString(part) || strings.Contains(part, "..") || strings.HasSuffix(part, ".") {
			return Coordinate{}, false
		}
	}
	if strings.EqualFold(p[2], "LATEST") || strings.EqualFold(p[2], "RELEASE") || strings.EqualFold(p[2], "SNAPSHOT") || strings.HasPrefix(strings.ToLower(p[2]), "latest.") {
		return Coordinate{}, false
	}
	return Coordinate{p[0], p[1], p[2]}, true
}

// Dependencies are the module's literal direct compile dependency
// declarations. Other declarations cannot establish a classpath.
type Dependencies struct {
	Coordinates []Coordinate
	Unresolved  int
}

func DirectDependencies(snapshotDir string, manifest source.Manifest, module Module) (Dependencies, error) {
	var out Dependencies
	if module.BuildSystem == "" {
		return out, nil
	}
	manifests := map[string]bool{}
	for _, p := range module.Manifests {
		manifests[p] = true
	}
	root, err := os.OpenRoot(snapshotDir)
	if err != nil {
		return out, err
	}
	defer root.Close()
	for _, e := range manifest.Entries {
		if !manifests[e.Path] {
			continue
		}
		base := path.Base(e.Path)
		if base != "pom.xml" && base != "build.gradle" && base != "build.gradle.kts" {
			continue
		}
		if e.Size == nil || *e.Size > MaxManifestBytes || e.Encoding != "utf-8" {
			out.Unresolved++
			continue
		}
		b, err := ReadManifest(root, e)
		if err != nil {
			return out, err
		}
		var d Dependencies
		if base == "pom.xml" {
			d = mavenDependencies(b)
		} else {
			d = gradleDependencies(b)
		}
		out.Coordinates = append(out.Coordinates, d.Coordinates...)
		out.Unresolved += d.Unresolved
	}
	slices.SortFunc(out.Coordinates, func(a, b Coordinate) int { return strings.Compare(a.String(), b.String()) })
	out.Coordinates = slices.Compact(out.Coordinates)
	// A different version of the same library must not be chosen by
	// directory order or the compiler's first classpath hit.
	conflict := map[string]bool{}
	first := map[string]string{}
	for _, c := range out.Coordinates {
		key := c.Group + ":" + c.Artifact
		if v, exists := first[key]; exists && v != c.Version {
			conflict[key] = true
		}
		first[key] = c.Version
	}
	out.Coordinates = slices.DeleteFunc(out.Coordinates, func(c Coordinate) bool {
		if conflict[c.Group+":"+c.Artifact] {
			out.Unresolved++
			return true
		}
		return false
	})
	return out, nil
}

type mavenDependency struct {
	Group      string `xml:"groupId"`
	Artifact   string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Type       string `xml:"type"`
	Classifier string `xml:"classifier"`
}

func mavenDependencies(body []byte) Dependencies {
	var pom struct {
		XMLName    xml.Name `xml:"project"`
		Properties struct {
			Values []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
			} `xml:",any"`
		} `xml:"properties"`
		Dependencies []mavenDependency `xml:"dependencies>dependency"`
		Managed      []mavenDependency `xml:"dependencyManagement>dependencies>dependency"`
	}
	if xml.Unmarshal(body, &pom) != nil {
		return Dependencies{Unresolved: 1}
	}
	properties := map[string]string{}
	for _, p := range pom.Properties.Values {
		properties[p.XMLName.Local] = strings.TrimSpace(p.Value)
	}
	resolve := func(v string) string {
		v = strings.TrimSpace(v)
		if m := propertyName.FindStringSubmatch(v); m != nil {
			if value, ok := properties[m[1]]; ok && value != "" {
				return value
			}
		}
		return v
	}
	managed := map[string]string{}
	for _, d := range pom.Managed {
		key := resolve(d.Group) + ":" + resolve(d.Artifact)
		if (d.Type == "" || d.Type == "jar") && d.Classifier == "" && d.Version != "" {
			if v, exists := managed[key]; exists && v != resolve(d.Version) {
				managed[key] = ""
			} else {
				managed[key] = resolve(d.Version)
			}
		}
	}
	var out Dependencies
	for _, d := range pom.Dependencies {
		switch scope := resolve(d.Scope); scope {
		case "test", "runtime":
			continue
		case "", "compile", "provided":
		default:
			out.Unresolved++
			continue
		}
		if kind := resolve(d.Type); kind != "" && kind != "jar" || resolve(d.Classifier) != "" {
			out.Unresolved++
			continue
		}
		key := resolve(d.Group) + ":" + resolve(d.Artifact)
		v := resolve(d.Version)
		if d.Version == "" {
			v = managed[key]
		}
		if c, ok := ParseCoordinate(key + ":" + v); ok {
			out.Coordinates = append(out.Coordinates, c)
		} else {
			out.Unresolved++
		}
	}
	return out
}

func gradleDependencies(body []byte) Dependencies {
	var out Dependencies
	// Only single-line literal declarations are understood. Other forms
	// (catalogs, variables, file dependencies, maps) need Gradle evaluation.
	depth := 0
	inDependencies := false
	for _, line := range strings.Split(gradleComments(body), "\n") {
		if depth == 0 && gradleBlock.MatchString(line) {
			inDependencies = true
		}
		direct := inDependencies && depth == 1
		depth += gradleBraceShift(line)
		if depth <= 0 {
			inDependencies = false
		}
		if !direct || !gradleCompile.MatchString(line) {
			continue
		}
		if m := gradleCoordinate.FindStringSubmatch(line); m != nil {
			if c, ok := ParseCoordinate(m[1]); ok {
				out.Coordinates = append(out.Coordinates, c)
				continue
			}
		}
		out.Unresolved++
	}
	return out
}

// gradleBraceShift keeps literal declarations in the direct, top-level
// dependency block. The syntax of computed and nested declarations is not evaluated.
func gradleBraceShift(line string) int {
	depth := 0
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case escaped:
			escaped = false
		case quote != 0:
			if r == '\\' {
				escaped = true
			} else if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '{':
			depth++
		case r == '}':
			depth--
		}
	}
	return depth
}
