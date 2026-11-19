package envdoc

import (
	"path"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Path classification shared by discovery and the planners that must not
// import it (the secrets analyzer): document classes and module manifests by
// name only. Nothing here reads a file.

var (
	agentNames = map[string]bool{
		"agents.md": true, "agent.md": true, "claude.md": true, "claude.local.md": true, "gemini.md": true,
		".cursorrules": true, ".windsurfrules": true, ".clinerules": true,
	}
	docExt    = map[string]bool{"": true, ".md": true, ".markdown": true, ".mdx": true, ".rst": true, ".txt": true, ".adoc": true, ".asciidoc": true, ".org": true}
	markupExt = map[string]bool{".md": true, ".markdown": true, ".mdx": true, ".rst": true, ".adoc": true, ".asciidoc": true}
	docDirs   = map[string]bool{"docs": true, "doc": true, "design": true, "adr": true, "adrs": true, "rfc": true, "rfcs": true, "architecture": true}
)

// ClassifyDocument returns the document class of a snapshot path, or "" when the
// path is not a document. Classification looks at names only: an
// agent-instruction file is data for later review roles and grants nothing.
func ClassifyDocument(p string) string {
	base := path.Base(p)
	lower := strings.ToLower(base)
	ext := strings.ToLower(path.Ext(base))
	dir := strings.ToLower(Dir(p))
	switch {
	case agentNames[lower],
		lower == "copilot-instructions.md" && (dir == ".github" || strings.HasSuffix(dir, "/.github")),
		ext == ".mdc" && (dir == ".cursor/rules" || strings.HasSuffix(dir, "/.cursor/rules")):
		return ClassAgent
	case strings.HasPrefix(lower, "readme") && docExt[ext]:
		return ClassReadme
	case strings.HasPrefix(lower, "contributing") && docExt[ext]:
		return ClassContributing
	case !markupExt[ext]:
		return ""
	}
	for _, seg := range strings.Split(dir, "/") {
		if docDirs[seg] {
			return ClassDesign
		}
	}
	stem := strings.TrimSuffix(lower, ext)
	for _, w := range []string{"design", "architecture", "adr-", "rfc"} {
		if strings.Contains(stem, w) {
			return ClassDesign
		}
	}
	return ClassOther
}

// ManifestEcosystem maps a manifest base name to its ecosystem. Names are
// case-sensitive, as the tools that read them are.
func ManifestEcosystem(base string) (string, bool) {
	switch base {
	case "go.mod", "go.work":
		return EcoGo, true
	case "package.json":
		return EcoJavaScript, true
	case "pyproject.toml", "setup.cfg", ".python-version":
		return EcoPython, true
	case "Cargo.toml", "rust-toolchain", "rust-toolchain.toml":
		return EcoRust, true
	case "CMakeLists.txt", "meson.build", "Makefile", "makefile", "GNUmakefile", "compile_commands.json":
		return EcoCpp, true
	}
	if strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
		return EcoPython, true
	}
	return "", false
}

// ModelInputPath reports a path some review role may send to a model:
// source of a supported language ecosystem (code and observation excerpts),
// a guidance document (the documents role) or a module manifest (the
// environment role quotes its requirement lines). The secrets analyzer is
// planned when such a file is in scope, so documents and configuration are
// screened even without supported source.
func ModelInputPath(p string) bool {
	if record.SourceEcosystem(p) != "" || IsGuidance(ClassifyDocument(p)) {
		return true
	}
	_, ok := ManifestEcosystem(path.Base(p))
	return ok
}
