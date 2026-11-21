package cpp

import (
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// A compile_commands.json is data written on the original build machine. It
// is mapped onto the attempt workspace here and never executed: the entry's
// compiler is only a driver name that tells the tool the language mode, and
// every flag passes an allowlist (docs/ANALYZERS.md#compile-db-매핑).

// Entry exclusion reasons (counted per compile DB entry).
const (
	ExclMalformed        = "entry_malformed"
	ExclDirectory        = "directory_not_absolute"
	ExclOutsideRoot      = "file_outside_snapshot_root"
	ExclNotInSnapshot    = "file_not_in_snapshot"
	ExclDataDir          = "data_directory"
	ExclHeader           = "header_entry"
	ExclNotCpp           = "not_cpp_source"
	ExclOutOfScope       = "out_of_scope"
	ExclDuplicate        = "duplicate_entry"
	ExclMSVCDriver       = "msvc_driver_unsupported"
	ExclUnsupportedDrive = "unsupported_driver"
	// ExclUnsafeArgument: a kept argument would still be read as a response
	// file or as a code-loading or toolchain-redirecting option (defense in
	// depth behind the allowlist; never expected).
	ExclUnsafeArgument = "unsafe_argument"
)

// Dropped-flag reasons (counted per argument).
const (
	DropCodeLoading      = "code_loading"
	DropPassthrough      = "passthrough"
	DropResponseFile     = "response_file"
	DropDriverConfig     = "driver_config"
	DropDriverMode       = "driver_mode"
	DropToolchainPath    = "toolchain_path"
	DropIncludeOutside   = "include_outside_snapshot"
	DropModules          = "modules"
	DropInstrumentation  = "instrumentation"
	DropPrecompiled      = "precompiled_header"
	DropBackendOption    = "backend_option"
	DropPositional       = "positional_argument"
	DropNotAllowlisted   = "not_allowlisted"
	dropStructural       = "structural"
	dropWarningOption    = "warning_option"
	dropLinker           = "linker"
	maxCompileDBBytes    = 64 << 20
	maxCompileDBEntries  = 100000
	maxArgumentsPerEntry = 4096
	maxMacroArg          = 1024
)

// Limitation codes of the mapping.
const (
	LimCodeLoadingDropped   = "code_loading_flags_dropped"
	LimFlagsDropped         = "compile_flags_dropped"
	LimResponseFiles        = "response_files_dropped"
	LimIncludeOutside       = "include_paths_outside_snapshot"
	LimIncludeDirsMissing   = "include_dirs_not_in_snapshot"
	LimIncludeFilesMissing  = "forced_include_files_not_in_snapshot"
	LimWrappersStripped     = "compiler_wrappers_stripped"
	LimEntriesExcluded      = "compile_db_entries_excluded"
	LimMultipleDBs          = "multiple_compile_dbs"
	LimSourcesNotInDB       = "sources_not_in_compile_db"
	LimDuplicateEntries     = "duplicate_compile_db_entries"
	LimNoRootInferred       = "compile_db_root_not_inferred"
	LimCompileDBFlagsMapped = "compile_db_flags_mapped"
)

// wrappers are compiler launchers stripped from argv[0]; they would run the
// real compiler, which CODEYE never does.
var wrappers = map[string]bool{"ccache": true, "sccache": true, "distcc": true, "icecc": true, "buildcache": true, "colorgcc": true}

var driverPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// Values the allowlist keeps from an entry are validated: clang expands any
// argv element that starts with "@" as a response file, so a value such as
// "-x @/x.rsp" would inject arguments. Every kept value is from a fixed set
// or a strict pattern, and values are joined to their flag where clang
// accepts that.
var (
	keepLanguages = map[string]bool{"c": true, "c++": true, "c-header": true, "c++-header": true}
	keepStdlib    = map[string]bool{"libc++": true, "libstdc++": true, "platform": true}
	targetPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	stdPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+]{0,15}$`)
	optPattern    = regexp.MustCompile(`^-O(?:[0-3sgz]|fast)?$`)
	macroPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}(?:\([A-Za-z0-9_, .]*\))?(?:=[^\x00\n\r]*)?$`)
	undefPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// unsafeArg reports an argument that clang would expand as a response file
// or that loads code, redirects the toolchain or reads driver configuration.
// The allowlist never keeps one; Unit validation rejects the entry if it
// ever did.
func unsafeArg(a string) bool {
	if strings.HasPrefix(a, "@") {
		return true
	}
	for _, p := range []string{"-fplugin", "-fpass-plugin", "-load", "-Xclang", "-Xanalyzer", "-mllvm", "-B", "--gcc-toolchain", "--gcc-install-dir", "-specs", "--specs",
		"--sysroot", "-isysroot", "--config", "--no-default-config", "-resource-dir", "-ivfsoverlay", "-include-pch", "-fmodule", "--driver-mode", "-Wl,", "-Wp,", "-Xlinker",
		"-Xpreprocessor", "-Xassembler", "-fprofile", "-fsanitize", "-fcoverage", "-Xopenmp-target", "-cc1", "-fdepfile", "-MF", "-o"} {
		if strings.HasPrefix(a, p) {
			return true
		}
	}
	return false
}

// safe reports a unit whose kept arguments and driver are all safe.
func (u Unit) safe() bool {
	if !driverPattern.MatchString(u.Driver) || unsafeArg(u.Driver) {
		return false
	}
	for _, a := range u.Args {
		if unsafeArg(a.Flag) || (a.Value != "" && (unsafeArg(a.Value) || strings.HasPrefix(a.Value, "-"))) || !a.safePath() {
			return false
		}
	}
	return domain.ValidateEvidencePath(u.Rel) == nil
}

func (a Arg) safePath() bool {
	if a.Path != "." {
		return a.Path == "" || domain.ValidateEvidencePath(a.Path) == nil
	}
	// The project root can be a search directory. mapFlags always renders
	// directory options joined to the absolute path in the working copy.
	return a.Joined && a.Value == "" && slices.ContainsFunc(pathFlags, func(pf includePathFlag) bool {
		return pf.name == a.Flag && !pf.file
	})
}

// cxxDriver reports a C++ driver name (g++, clang++-18, c++).
func cxxDriver(name string) bool { return strings.Contains(name, "++") }

var cxxExt = map[string]bool{".cc": true, ".cpp": true, ".cxx": true, ".c++": true}

// Arg is one kept compiler argument. Path, when set, is snapshot-relative and
// is rendered as the absolute workspace path, joined to Flag or as the next
// element; Value is a separate literal element.
type Arg struct {
	Flag   string
	Value  string
	Path   string
	Joined bool
}

// Unit is one translation unit from a compile DB, mapped onto the snapshot.
type Unit struct {
	Rel    string // snapshot path of the main file
	DB     string // compile DB path it came from
	Driver string // basename of the entry's compiler (never executed)
	Args   []Arg
}

// Mapping is the result of mapping the snapshot's compile DBs.
type Mapping struct {
	Units    []Unit
	Total    int
	Excluded map[string]int
	Dropped  map[string]int
	// ExcludedSources are in-scope sources whose every entry was excluded.
	ExcludedSources map[string]string
	Drivers         []string
	Wrappers        int
	DirsMissing     int
	FilesMissing    int
	RootUnknown     bool
	// Roots counts DBs whose original root was inferred.
	Roots int
}

// Limitations derived from the mapping.
func (m Mapping) Limitations() []string {
	var out []string
	add := func(ok bool, code string) {
		if ok {
			out = append(out, code)
		}
	}
	add(m.Dropped[DropCodeLoading] > 0, LimCodeLoadingDropped)
	add(m.Dropped[DropResponseFile] > 0, LimResponseFiles)
	add(m.Dropped[DropIncludeOutside] > 0, LimIncludeOutside)
	for _, r := range []string{DropPassthrough, DropDriverConfig, DropDriverMode, DropToolchainPath, DropModules, DropInstrumentation, DropPrecompiled, DropBackendOption, DropPositional, DropNotAllowlisted} {
		if m.Dropped[r] > 0 {
			out = append(out, LimFlagsDropped)
			break
		}
	}
	add(m.DirsMissing > 0, LimIncludeDirsMissing)
	add(m.FilesMissing > 0, LimIncludeFilesMissing)
	add(m.Wrappers > 0, LimWrappersStripped)
	excluded := 0
	for r, n := range m.Excluded {
		if r != ExclOutOfScope && r != ExclDuplicate {
			excluded += n
		}
	}
	add(excluded > 0, LimEntriesExcluded)
	add(m.Excluded[ExclDuplicate] > 0, LimDuplicateEntries)
	add(m.RootUnknown, LimNoRootInferred)
	return out
}

// DroppedText is the dropped-flag summary without the structural flags that
// do not affect what the tools analyze (output, dependency files, debug info,
// warning options, linker arguments).
func (m Mapping) DroppedText() string {
	c := map[string]int{}
	for k, v := range m.Dropped {
		if k != dropStructural && k != dropWarningOption && k != dropLinker {
			c[k] = v
		}
	}
	return countText(c)
}

// MapDBs maps the compile DBs (path → bytes, in plan order) onto the
// snapshot. inScope selects the translation units the task covers. A DB that
// is not valid JSON fails the whole mapping: nothing is guessed from it.
func MapDBs(dbs []string, data map[string][]byte, files map[string]source.Entry, inScope func(rel string) bool) (Mapping, error) {
	m := Mapping{Excluded: map[string]int{}, Dropped: map[string]int{}, ExcludedSources: map[string]string{}}
	dirs := map[string]bool{".": true}
	for p := range files {
		for d := path.Dir(p); d != "." && !dirs[d]; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	seen := map[string]bool{}
	drivers := map[string]bool{}
	for _, db := range dbs {
		entries, err := parseDB(data[db])
		if err != nil {
			return Mapping{}, err
		}
		m.Total += len(entries)
		var abs []string
		for _, e := range entries {
			if e.Directory == nil || e.File == nil {
				continue
			}
			if f, ok := absOn(norm(*e.Directory), *e.File); ok {
				abs = append(abs, f)
			}
		}
		root := inferRoot(abs, files)
		if root == "" {
			m.RootUnknown = true
		} else {
			m.Roots++
		}
		for _, e := range entries {
			u, reason, rel := mapEntry(e, root, files, dirs, inScope, &m)
			if reason != "" {
				m.Excluded[reason]++
				if rel != "" && inScope(rel) && IsSource(rel) && !pack.UnderDataDir(rel) && !seen[rel] && reason != ExclDuplicate {
					if _, already := m.ExcludedSources[rel]; !already {
						m.ExcludedSources[rel] = reason
					}
				}
				continue
			}
			if seen[u.Rel] {
				m.Excluded[ExclDuplicate]++
				continue
			}
			seen[u.Rel] = true
			delete(m.ExcludedSources, u.Rel)
			u.DB = db
			drivers[u.Driver] = true
			m.Units = append(m.Units, u)
		}
	}
	sort.Slice(m.Units, func(a, b int) bool { return m.Units[a].Rel < m.Units[b].Rel })
	for d := range drivers {
		m.Drivers = append(m.Drivers, d)
	}
	sort.Strings(m.Drivers)
	return m, nil
}

// mapEntry maps one entry; reason != "" excludes it (rel is its snapshot
// file when known).
func mapEntry(e dbEntry, root string, files map[string]source.Entry, dirs map[string]bool, inScope func(string) bool, m *Mapping) (Unit, string, string) {
	if e.Directory == nil || e.File == nil || *e.File == "" || (e.Command == nil && len(e.Arguments) == 0) {
		return Unit{}, ExclMalformed, ""
	}
	dir := path.Clean(norm(*e.Directory))
	if !isAbs(dir) {
		return Unit{}, ExclDirectory, ""
	}
	argv := e.Arguments
	if len(argv) == 0 {
		split := splitCommand
		if winDir(dir) {
			split = splitWindowsCommand
		}
		var ok bool
		if argv, ok = split(*e.Command); !ok || len(argv) == 0 {
			return Unit{}, ExclMalformed, ""
		}
	}
	if len(argv) > maxArgumentsPerEntry {
		return Unit{}, ExclMalformed, ""
	}
	wrapped := 0
	for len(argv) > 0 && wrappers[strings.TrimSuffix(strings.ToLower(path.Base(norm(argv[0]))), ".exe")] {
		argv = argv[1:]
		wrapped++
	}
	if len(argv) == 0 {
		return Unit{}, ExclMalformed, ""
	}
	file, ok := absOn(dir, *e.File)
	if !ok {
		return Unit{}, ExclMalformed, ""
	}
	rel, ok := mapUnder(root, file)
	if !ok || rel == "." {
		return Unit{}, ExclOutsideRoot, ""
	}
	if _, ok := files[rel]; !ok {
		return Unit{}, ExclNotInSnapshot, ""
	}
	switch {
	case pack.UnderDataDir(rel):
		return Unit{}, ExclDataDir, rel
	case !IsCppFile(rel):
		return Unit{}, ExclNotCpp, rel
	case !IsSource(rel):
		return Unit{}, ExclHeader, rel
	case !inScope(rel):
		return Unit{}, ExclOutOfScope, rel
	}
	driver := path.Base(norm(argv[0]))
	lower := strings.TrimSuffix(strings.ToLower(driver), ".exe")
	switch {
	case lower == "cl" || strings.HasSuffix(lower, "clang-cl"):
		return Unit{}, ExclMSVCDriver, rel
	case lower == "nvcc" || !driverPattern.MatchString(driver):
		return Unit{}, ExclUnsupportedDrive, rel
	}
	u := Unit{Rel: rel, Driver: driver}
	mapFlags(argv[1:], dir, root, *e.File, file, files, dirs, &u, m)
	if !u.safe() {
		return Unit{}, ExclUnsafeArgument, rel
	}
	m.Wrappers += wrapped
	return u, "", rel
}
