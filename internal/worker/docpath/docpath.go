// Package docpath classifies snapshot documents by their relative paths.
package docpath

import (
	"path"
	"strings"
)

const (
	Readme       = "readme"
	Agent        = "agent_instructions"
	Design       = "design"
	Contributing = "contributing"
	JavaAPI      = "java_javadoc"
	Other        = "other"
)

var (
	agentNames = map[string]bool{
		"agents.md": true, "agent.md": true, "claude.md": true, "claude.local.md": true, "gemini.md": true,
		".cursorrules": true, ".windsurfrules": true, ".clinerules": true,
	}
	docExt    = map[string]bool{"": true, ".md": true, ".markdown": true, ".mdx": true, ".rst": true, ".txt": true, ".adoc": true, ".asciidoc": true, ".org": true}
	markupExt = map[string]bool{".md": true, ".markdown": true, ".mdx": true, ".rst": true, ".adoc": true, ".asciidoc": true}
	docDirs   = map[string]bool{"docs": true, "doc": true, "design": true, "adr": true, "adrs": true, "rfc": true, "rfcs": true, "architecture": true}
)

// Classify returns the document class of a snapshot path, or "" when the
// path is not a document. Classification looks at names only: an
// agent-instruction file is data for later review roles and grants nothing.
func Classify(p string) string {
	base := path.Base(p)
	lower := strings.ToLower(base)
	ext := strings.ToLower(path.Ext(base))
	dir := strings.ToLower(path.Dir(p))
	switch {
	case agentNames[lower],
		lower == "copilot-instructions.md" && (dir == ".github" || strings.HasSuffix(dir, "/.github")),
		ext == ".mdc" && (dir == ".cursor/rules" || strings.HasSuffix(dir, "/.cursor/rules")):
		return Agent
	case strings.HasPrefix(lower, "readme") && docExt[ext]:
		return Readme
	case strings.HasPrefix(lower, "contributing") && docExt[ext]:
		return Contributing
	case JavaAPIPath(p):
		return JavaAPI
	case !markupExt[ext]:
		return ""
	}
	for _, seg := range strings.Split(dir, "/") {
		if docDirs[seg] {
			return Design
		}
	}
	if namedGuide(p) != notGuide && !administrative(p, "") {
		return Design
	}
	stem := strings.TrimSuffix(lower, ext)
	for _, w := range []string{"design", "architecture", "adr-", "rfc"} {
		if strings.Contains(stem, w) {
			return Design
		}
	}
	return Other
}

// JavaAPIPath identifies conventional production Java paths that can carry API
// documentation. A declaration parser still has to confirm an attached comment.
func JavaAPIPath(p string) bool {
	base := path.Base(p)
	if !strings.HasSuffix(base, ".java") || strings.HasSuffix(base, "Test.java") ||
		strings.HasSuffix(base, "Tests.java") || base == "package-info.java" || base == "module-info.java" {
		return false
	}
	dirs := strings.Split(path.Dir(p), "/")
	for _, dir := range dirs {
		switch strings.ToLower(dir) {
		case "test", "tests", "testdata", "vendor", "third_party", "third-party", "generated", "fixtures", "target", "build", "node_modules":
			return false
		}
		if strings.HasPrefix(dir, ".") {
			return false
		}
	}
	return strings.Contains("/"+p, "/src/main/java/")
}

// Scope returns the default code directory described by a guidance document.
// Design documents in a conventional docs tree describe the directory
// enclosing that tree. Local instruction files and readmes keep their
// actual directory, including when they occur inside documentation trees.
func Scope(p string) string {
	dir := path.Dir(p)
	if Classify(p) != Design {
		return dir
	}
	parts := strings.Split(dir, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		switch strings.ToLower(parts[i]) {
		case "docs", "doc":
			if i == 0 {
				return "."
			}
			return strings.Join(parts[:i], "/")
		}
	}
	return dir
}
