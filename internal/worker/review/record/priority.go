package record

import (
	"bytes"
	"slices"
	"strings"
)

// Review priority (docs/REVIEW.md#검토-우선순위): the path rule that tells a
// project's own code from material that only supports it, and the order in
// which review.scope gives the slots to files. The worker uses it to fill
// the review slots, the report builder to classify ledger rows and the
// renderers to order findings, so the rule lives here once.

// File classes.
const (
	ClassPrimary   = "primary"
	ClassSecondary = "secondary"
)

// Priority ranks of a file, highest first. A file with tool diagnostics
// comes first (static analysis already points at it), then a file of a
// module whose required analyzer did not succeed (static analysis says
// nothing about it), then entry points, other source, and tests.
const (
	RankToolObservations     = "tool_observations"
	RankAnalyzerNotSucceeded = "analyzer_not_succeeded"
	RankEntryPoint           = "entry_point"
	RankSource               = "source"
	RankTest                 = "test"
)

// Ranks in priority order.
var Ranks = []string{RankToolObservations, RankAnalyzerNotSucceeded, RankEntryPoint, RankSource, RankTest}

// RankIndex is the position of a rank in Ranks (len(Ranks) when unknown).
func RankIndex(r string) int {
	for i, x := range Ranks {
		if x == r {
			return i
		}
	}
	return len(Ranks)
}

// Secondary reasons of a file.
const (
	// ReasonSecondaryPath prefixes the matched segment:
	// secondary_path:fixtures.
	ReasonSecondaryPath = "secondary_path"
	// ReasonGeneratedFile marks a file whose first lines say it is generated.
	ReasonGeneratedFile = "generated_file"
)

// SecondaryRule names the path segments of test input, examples,
// third-party and generated material. A path is secondary when one of its
// directories is such a segment (an entry with a slash, tests/data, matches
// consecutive directories). Segments in NotForGo do not apply to Go files:
// Go packages named build or dist are ordinary source (the review corpus
// already leaves non-Go files below them out).
type SecondaryRule struct {
	Segments []string `json:"segments"`
	NotForGo []string `json:"not_for_go"`
}

// DefaultSecondary is the rule the worker and the report use unless the
// node operator configures another (review.Handler.Secondary).
var DefaultSecondary = SecondaryRule{
	Segments: []string{"fixtures", "testdata", "test-data", "tests/data", "examples", "samples", "demo", "vendor", "third_party",
		"node_modules", "generated", "gen", "mocks", "__snapshots__", "dist", "build"},
	NotForGo: []string{"build", "dist"},
}

// Valid reports a rule the scope artifact may record: 1..64 segments of
// lower-case path names, NotForGo a subset of them.
func (r SecondaryRule) Valid() bool {
	if len(r.Segments) == 0 || len(r.Segments) > 64 || r.NotForGo == nil {
		return false
	}
	seen := map[string]bool{}
	for _, s := range r.Segments {
		if seen[s] || s == "" || len(s) > 64 || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") || strings.Contains(s, "//") || strings.ContainsAny(s, "\\\x00") || s != strings.ToLower(s) {
			return false
		}
		seen[s] = true
	}
	for _, s := range r.NotForGo {
		if !seen[s] {
			return false
		}
	}
	return true
}

// Match returns the first segment of the rule found among the directories
// of p (a snapshot-relative file path), or "" when p is primary. Matching
// ignores case.
func (r SecondaryRule) Match(p string) string {
	dirs := strings.Split(strings.ToLower(p), "/")
	dirs = dirs[:len(dirs)-1]
	goFile := strings.HasSuffix(strings.ToLower(p), ".go")
	for i := range dirs {
		for _, s := range r.Segments {
			if goFile && contains(r.NotForGo, s) {
				continue
			}
			parts := strings.Split(s, "/")
			if i+len(parts) <= len(dirs) && equalParts(dirs[i:i+len(parts)], parts) {
				return s
			}
		}
	}
	return ""
}

// ModuleClass is the class of a module (<ecosystem>:<dir>): secondary when
// its directory lies below a secondary segment, e.g. fixtures/rust/clean.
func (r SecondaryRule) ModuleClass(module string) string {
	eco, dir, ok := strings.Cut(module, ":")
	if !ok || dir == "." {
		return ClassPrimary
	}
	// A file directly in the module directory: Go files do not take the
	// NotForGo segments.
	probe := dir + "/x"
	if eco == "go" {
		probe += ".go"
	}
	if r.Match(probe) != "" {
		return ClassSecondary
	}
	return ClassPrimary
}

// Path classes of a file that is not shipped code (PathClass).
const (
	PathTest      = "test"
	PathExample   = "example"
	PathBenchmark = "benchmark"
	PathGenerated = "generated"
	PathVendored  = "vendored"
)

// PathClasses lists the classes PathClass returns, in the order a reader
// sees them.
var PathClasses = []string{PathTest, PathExample, PathBenchmark, PathGenerated, PathVendored}

// pathClassOfSegment maps the default secondary segments to a class: test
// input and doubles are test code, the rest keep their own name.
var pathClassOfSegment = map[string]string{
	"fixtures": PathTest, "testdata": PathTest, "test-data": PathTest, "tests/data": PathTest, "mocks": PathTest, "__snapshots__": PathTest,
	"examples": PathExample, "samples": PathExample, "demo": PathExample,
	"vendor": PathVendored, "third_party": PathVendored, "node_modules": PathVendored,
	"generated": PathGenerated, "gen": PathGenerated, "dist": PathGenerated, "build": PathGenerated,
}

// benchmarkDirs are the directories of benchmark code. They are not
// secondary segments: the review priority and its scope artifact keep
// ranking benchmark code as source.
var benchmarkDirs = []string{"benchmark", "benchmarks", "benches"}

// LegacyPathClass is the class rule of report builder 23: test directories,
// the default secondary segments and benchmark directories at any depth.
// It guesses (a directory named test, demo, gen or build inside a source tree
// is often shipped code), so only the validation of builder 23 reports reads
// it. The review uses PathClass.
func LegacyPathClass(p string) string {
	if IsTestPath(p) {
		return PathTest
	}
	if seg := DefaultSecondary.Match(p); seg != "" {
		return pathClassOfSegment[seg]
	}
	dirs := strings.Split(strings.ToLower(p), "/")
	for _, d := range dirs[:len(dirs)-1] {
		if contains(benchmarkDirs, d) {
			return PathBenchmark
		}
	}
	return ""
}

// sourceTreeMarkers are directories that make a test, example or benchmark
// directory below them a part of the source tree rather than a root of the
// project (a package named test inside src, lib or an application).
var sourceTreeMarkers = []string{"src", "lib", "main", "internal", "pkg", "app", "cmd", "include", "source", "sources"}

// PathClass is the class of a snapshot-relative file path that is certainly
// not shipped code: a test file by the naming convention of its language
// (the names IsTestPath knows), a file below a well-known root (src/test of
// a Java or Kotlin module, testdata, __tests__, and examples, benchmarks or
// a tests directory that is not inside a source tree), or a vendored
// directory. It is "" for shipped code and for every path that only might not
// be shipped: a directory named test, demo, gen or build somewhere inside a
// source tree is often code that ships. Only the path is read, so the worker
// and the report builder agree; this one rule marks the excerpts of a request,
// decides which candidates the verification does not take and lowers the
// default severity of a tool diagnostic (docs/ANALYZERS.md#기본-심각도).
func PathClass(p string) string {
	lower := strings.ToLower(p)
	segs := strings.Split(lower, "/")
	dirs := segs[:len(segs)-1]
	for _, d := range dirs {
		switch d {
		case "vendor", "third_party", "node_modules":
			return PathVendored
		case "testdata", "test-data", "__tests__", "__snapshots__":
			return PathTest
		}
	}
	if testFileName(p) {
		return PathTest
	}
	for i, d := range dirs {
		inTree := slices.ContainsFunc(dirs[:i], func(x string) bool { return contains(sourceTreeMarkers, x) })
		switch {
		case d == "src" && i+1 < len(dirs) && dirs[i+1] == "test" && SourceEcosystem(lower) == "java":
			return PathTest
		case inTree:
		case i == 0 && d == "test", d == "tests":
			return PathTest
		case d == "examples" || d == "example":
			return PathExample
		case d == "benchmarks" || d == "benches":
			return PathBenchmark
		}
	}
	return ""
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func equalParts(a, b []string) bool {
	for i := range b {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// GeneratedHeader reports whether a conventional generated-code marker
// occurs in the first ten lines. Both Go marker phrases must be on one line.
func GeneratedHeader(head []byte) bool {
	end := 0
	for range 10 {
		line := bytes.IndexByte(head[end:], '\n')
		if line < 0 {
			end = len(head)
			break
		}
		end += line + 1
	}
	head = head[:end]
	if bytes.Contains(head, []byte("@generated")) {
		return true
	}
	if !bytes.Contains(head, []byte("Code generated")) || !bytes.Contains(head, []byte("DO NOT EDIT")) {
		return false
	}
	for len(head) > 0 {
		line, rest, _ := bytes.Cut(head, []byte("\n"))
		if bytes.Contains(line, []byte("Code generated")) && bytes.Contains(line, []byte("DO NOT EDIT")) {
			return true
		}
		head = rest
	}
	return false
}

// SeverityRank orders the tools' raw severities, which use different words:
// 3 for error/high/critical, 2 for warning/medium, 1 for anything else that
// is reported (low, info, note, style, no severity), 0 for diagnostics the
// tool marks as ignored.
func SeverityRank(raw *string) int {
	if raw == nil {
		return 1
	}
	switch strings.ToLower(*raw) {
	case "critical", "fatal", "error", "high":
		return 3
	case "warning", "warn", "medium", "moderate":
		return 2
	case "ignored", "none", "off":
		return 0
	}
	return 1
}

// SeverityNames are the normalized severities of SeverityRank 0..3.
var SeverityNames = []string{"ignored", "low", "medium", "high"}
