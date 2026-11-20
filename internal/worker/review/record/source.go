package record

import (
	"path"
	"slices"
	"strings"
)

// Source classification of snapshot paths. It decides from a
// path alone whether an excerpt may carry kind code, test or config, so the
// review builders and every decoder (worker, report) apply one rule. It reads
// no file and parses no language.

// sourceExtensions are the extensions that make a path source of a language
// ecosystem: the analyzer packs' definition of an ecosystem in scope
// (pack.SourceFiles, docs/ANALYZERS.md#범위에-없는-생태계와-팩-공백). A review test keeps the two equal.
var sourceExtensions = map[string]string{
	".go": "go",
	".py": "python", ".pyi": "python",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".ts": "javascript", ".tsx": "javascript", ".mts": "javascript", ".cts": "javascript",
	".rs": "rust",
	".c":  "cpp", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".c++": "cpp",
	".h": "cpp", ".hh": "cpp", ".hpp": "cpp", ".hxx": "cpp", ".h++": "cpp",
}

// SourceEcosystem returns the language ecosystem of a source path ("go",
// "python", "javascript", "rust", "cpp"), or "" when the path is no source.
func SourceEcosystem(p string) string {
	return sourceExtensions[strings.ToLower(path.Ext(p))]
}

// manifests are the build manifests the review reads as module context,
// by lower-cased base name. compile_commands.json is not one: it
// is generated build output that usually holds absolute paths of the machine
// that wrote it, and the C/C++ pack already reads it as data (docs/ANALYZERS.md).
var manifests = map[string]string{
	"go.mod":         "go",
	"pyproject.toml": "python", "setup.cfg": "python",
	"package.json": "javascript", "tsconfig.json": "javascript",
	"cargo.toml":     "rust",
	"cmakelists.txt": "cpp",
}

// ManifestEcosystem returns the ecosystem of a build manifest path, or "".
func ManifestEcosystem(p string) string {
	return manifests[strings.ToLower(path.Base(p))]
}

// IsTestPath reports whether a source path is a test file by the naming
// convention of its ecosystem:
//
//   - Go: *_test.go only (a directory name never makes a Go test);
//   - Python: test_*.py, *_test.py, conftest.py, or a test/tests directory;
//   - JS/TS: *.test.* and *.spec.*, or a __tests__/test/tests directory;
//   - Rust: tests.rs, or a tests directory (integration tests);
//   - C/C++: test_*, *_test, *_tests, *_unittest, *-test, or a test/tests
//     directory.
//
// Tests inside a source file (Rust #[cfg(test)], doctests) stay code.
func IsTestPath(p string) bool {
	lower := strings.ToLower(p)
	eco := SourceEcosystem(lower)
	if eco == "" {
		return false
	}
	segs := strings.Split(lower, "/")
	base := segs[len(segs)-1]
	dirs := segs[:len(segs)-1]
	stem := strings.TrimSuffix(base, path.Ext(base))
	inDir := func(names ...string) bool {
		return slices.ContainsFunc(dirs, func(d string) bool { return slices.Contains(names, d) })
	}
	switch eco {
	case "go":
		return strings.HasSuffix(base, "_test.go")
	case "python":
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(stem, "_test") || base == "conftest.py" || inDir("test", "tests")
	case "javascript":
		return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") || inDir("__tests__", "test", "tests")
	case "rust":
		return base == "tests.rs" || inDir("tests")
	}
	return strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test") || strings.HasSuffix(stem, "_tests") ||
		strings.HasSuffix(stem, "_unittest") || strings.HasSuffix(stem, "-test") || inDir("test", "tests")
}

// SourceKind returns the excerpt kind a path may carry as source: "test"
// for a test path, "code" for other source and "" for no source.
func SourceKind(p string) string {
	switch {
	case SourceEcosystem(p) == "":
		return ""
	case IsTestPath(p):
		return "test"
	}
	return "code"
}
