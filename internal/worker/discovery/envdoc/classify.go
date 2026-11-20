package envdoc

import (
	"path"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/docpath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// ClassifyDocument identifies guidance from its snapshot-relative path.
func ClassifyDocument(p string) string { return docpath.Classify(p) }

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
