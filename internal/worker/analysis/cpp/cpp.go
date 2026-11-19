// Package cpp is the W16 C/C++ analyzer pack (docs/ANALYZERS.md): the baseline
// cppcheck adapter and the extra-depth clang-tidy adapter, which also
// carries the Clang Static Analyzer (its clang-analyzer-* checks; clang
// --analyze is the same engine and is never run). Both tools run natively
// through the W05 runner in the attempt workspace copy, offline, with an
// explicit environment and argv, and record their results in the generic
// observations envelope (obsdoc).
//
// A compile_commands.json in the snapshot is data: its entries are mapped
// from the original build machine onto the attempt workspace and filtered
// through a flag allowlist into a CODEYE-written database in task scratch
// (compiledb.go). No entry's compiler is ever executed, no configure step or
// build system runs, and no repository configuration can load code into the
// tools, so neither analyzer's tasks execute repository code.
package cpp

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Ecosystem of the pack.
const Ecosystem = "cpp"

// CompileDBName is the file name of a compilation database.
const CompileDBName = "compile_commands.json"

// Shared task reasons (TaskResult.Reason and outcome.reason).
const (
	ReasonCleanupUncertain     = "cleanup_uncertain"
	ReasonCancelled            = "cancelled"
	ReasonTimeout              = "timeout"
	ReasonExecutionUnsupported = "execution_unsupported"
	ReasonToolStartFailed      = "tool_start_failed"
	ReasonExecutionRejected    = "execution_rejected"
	ReasonToolVersion          = "tool_version_unrecognized"
	ReasonSnapshotIntegrity    = "snapshot_integrity_failed"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonExitMismatch         = "exit_status_mismatch"
	// ReasonCompileDBMissing: the snapshot has no compile_commands.json, so
	// a tool that needs build flags cannot run. Generating one would execute
	// repository code (configure, cmake, bear) and is not implemented.
	ReasonCompileDBMissing = "compile_db_missing"
	// ReasonCompileDBUnreadable: a compile DB is not valid JSON of entries.
	ReasonCompileDBUnreadable = "compile_db_unreadable"
	// ReasonNoTranslationUnits: nothing in scope can be handed to the tool
	// (headers only, or no in-scope entry of the compile DB).
	ReasonNoTranslationUnits = "no_translation_units"
	// ReasonMissingInclude: a user header (often one a configure step would
	// generate, e.g. config.h) was not found; the translation unit was
	// analyzed partially or not at all. A coverage gap, never a defect.
	ReasonMissingInclude = "missing_include_files"
	// ReasonTUErrors: translation units failed to parse or compile.
	ReasonTUErrors = "translation_unit_errors"
	// ReasonTUNotProcessed: the tool output does not show that every given
	// translation unit was processed.
	ReasonTUNotProcessed = "translation_units_not_processed"
)

// Translation-unit states (coverage.units, unit "translation_unit").
const (
	TUAnalyzedClean          = "analyzed_clean"
	TUAnalyzedWithFindings   = "analyzed_with_findings"
	TUReducedClean           = "analyzed_clean_without_build_flags"
	TUReducedWithFindings    = "analyzed_with_findings_without_build_flags"
	TUPartialMissingInclude  = "partial_missing_include"
	TUPossiblyPartial        = "possibly_partial_missing_include"
	TUFailedMissingInclude   = "failed_missing_include"
	TUFailedCompile          = "failed_compile"
	TUFailedParse            = "failed_parse"
	TUNotProcessed           = "not_processed"
	TUUnknown                = "unknown"
	TUNotInCompileDB         = "not_in_compile_db"
	TUCompileDBEntryExcluded = "compile_db_entry_excluded"
)

// sourceExt are the translation-unit extensions; headerExt are included
// files that are never handed to a tool as a translation unit.
var (
	sourceExt = map[string]bool{".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".c++": true}
	headerExt = map[string]bool{".h": true, ".hh": true, ".hpp": true, ".hxx": true, ".h++": true}
)

// IsSource reports a translation-unit candidate by extension.
func IsSource(p string) bool { return sourceExt[strings.ToLower(path.Ext(p))] }

// IsCppFile reports any C/C++ source or header by extension.
func IsCppFile(p string) bool {
	e := strings.ToLower(path.Ext(p))
	return sourceExt[e] || headerExt[e]
}

// dataDir mirrors pack.SourceFiles: hidden, testdata, vendor and
// node_modules directories are data or vendored code.
func dataDir(seg string) bool {
	return strings.HasPrefix(seg, ".") || seg == "testdata" || seg == "vendor" || seg == "node_modules"
}

func underDataDir(p string) bool {
	segs := strings.Split(p, "/")
	return slices.ContainsFunc(segs[:len(segs)-1], dataDir)
}

func inScope(p string, scope domain.Scope) bool {
	if scope.Kind == "" || scope.Kind == "full" {
		return true
	}
	for _, s := range scope.Paths {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "./"), "/")
		if s == "" || s == "." || p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

// includedFiles indexes the included regular files of a manifest.
func includedFiles(m source.Manifest) map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range m.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			out[e.Path] = e
		}
	}
	return out
}

// layout is what both analyzers plan from: the manifest and the scope only.
type layout struct {
	// Sources are the in-scope translation-unit candidates, sorted.
	Sources []string
	// CppFiles counts every in-scope C/C++ file (pack.SourceFiles).
	CppFiles int
	// DBs are the compile DBs of the snapshot (scope does not apply: a DB is
	// build configuration, not an analysis target), shallowest first.
	DBs   []string
	Files map[string]source.Entry
}

func layoutOf(job analysis.Job) (layout, error) {
	switch job.Scope.Kind {
	case "", "full", "paths":
	default:
		return layout{}, fmt.Errorf("scope kind %q is not supported", job.Scope.Kind)
	}
	l := layout{Files: includedFiles(job.Snapshot.Manifest), CppFiles: pack.SourceFiles(job.Snapshot.Manifest, job.Scope)[Ecosystem]}
	for p := range l.Files {
		switch {
		case underDataDir(p):
		case path.Base(p) == CompileDBName:
			l.DBs = append(l.DBs, p)
		case IsSource(p) && inScope(p, job.Scope):
			l.Sources = append(l.Sources, p)
		}
	}
	sort.Strings(l.Sources)
	sort.Slice(l.DBs, func(a, b int) bool {
		da, db := strings.Count(l.DBs[a], "/"), strings.Count(l.DBs[b], "/")
		if da != db {
			return da < db
		}
		return l.DBs[a] < l.DBs[b]
	})
	return l, nil
}

// patterns are the scope arguments recorded with a task.
func patterns(scope domain.Scope) []string {
	if scope.Kind == "paths" && len(scope.Paths) > 0 {
		out := append([]string{}, scope.Paths...)
		sort.Strings(out)
		return out
	}
	return []string{"."}
}

// planOne plans the single task of an analyzer: nothing when no C/C++ file
// is in scope, otherwise one visible task even for headers only (the
// composite no longer plans pack-gap.cpp once this pack is a member).
func planOne(job analysis.Job, name string, required bool, title func(layout) string) ([]pack.Task, error) {
	l, err := layoutOf(job)
	if err != nil {
		return nil, err
	}
	if l.CppFiles == 0 {
		return nil, nil
	}
	n := len(l.Sources)
	return []pack.Task{{Spec: analysis.TaskSpec{Key: name, Required: required, Title: title(l), Stage: domain.StageStaticAnalysis},
		Module: ".", Patterns: patterns(job.Scope), FilesInScope: &n, RepositoryCode: false}}, nil
}

func ptr[T any](v T) *T { return &v }

// countText renders reason counts as "a:1,b:2" (sorted), for target
// properties.
func countText(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, m[k]))
	}
	s := strings.Join(parts, ",")
	if len(s) > 256 {
		s = s[:256]
	}
	return s
}
