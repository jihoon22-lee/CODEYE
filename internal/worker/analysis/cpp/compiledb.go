package cpp

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
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
		if unsafeArg(a.Flag) || (a.Value != "" && (unsafeArg(a.Value) || strings.HasPrefix(a.Value, "-"))) || (a.Path != "" && domain.ValidateEvidencePath(a.Path) != nil) {
			return false
		}
	}
	return domain.ValidateEvidencePath(u.Rel) == nil
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

type dbEntry struct {
	Directory *string  `json:"directory"`
	File      *string  `json:"file"`
	Command   *string  `json:"command"`
	Arguments []string `json:"arguments"`
	Output    *string  `json:"output"`
}

// ErrCompileDB is returned for a compile DB that is not a JSON array of
// objects within the size limits.
var ErrCompileDB = errors.New("compile DB unreadable")

func parseDB(b []byte) ([]dbEntry, error) {
	if len(b) > maxCompileDBBytes {
		return nil, ErrCompileDB
	}
	var raw []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&raw); err != nil {
		return nil, ErrCompileDB
	}
	if dec.More() || len(raw) > maxCompileDBEntries {
		return nil, ErrCompileDB
	}
	out := make([]dbEntry, len(raw))
	for i, r := range raw {
		// Unknown keys are tolerated (generators add some); a value of the
		// wrong type makes the entry malformed, not the database.
		if err := json.Unmarshal(r, &out[i]); err != nil {
			out[i] = dbEntry{}
		}
	}
	return out, nil
}

// splitCommand splits a "command" string like clang's JSON compilation
// database on POSIX: whitespace separates, single quotes are literal, double
// quotes and backslashes escape the next character.
func splitCommand(s string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	inArg := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if inArg {
				out = append(out, cur.String())
				cur.Reset()
				inArg = false
			}
		case c == '\\':
			if i+1 >= len(s) {
				return nil, false
			}
			i++
			cur.WriteByte(s[i])
			inArg = true
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			i += j + 1
			inArg = true
		case c == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
			inArg = true
		default:
			cur.WriteByte(c)
			inArg = true
		}
	}
	if inArg {
		out = append(out, cur.String())
	}
	return out, true
}

// splitWindowsCommand splits a "command" string of a Windows compile DB
// like CommandLineToArgvW: white space separates outside double quotes,
// backslashes are literal unless they precede a double quote (2n
// backslashes + quote: n backslashes and a quote toggle; 2n+1: n
// backslashes and a literal quote).
func splitWindowsCommand(s string) ([]string, bool) {
	var out []string
	var cur strings.Builder
	inArg, quoted := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c == ' ' || c == '\t' || c == '\n' || c == '\r') && !quoted:
			if inArg {
				out = append(out, cur.String())
				cur.Reset()
				inArg = false
			}
		case c == '\\':
			n := 0
			for i < len(s) && s[i] == '\\' {
				n++
				i++
			}
			if i < len(s) && s[i] == '"' {
				cur.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					cur.WriteByte('"')
				} else {
					quoted = !quoted
				}
			} else {
				cur.WriteString(strings.Repeat(`\`, n))
				i--
			}
			inArg = true
		case c == '"':
			quoted = !quoted
			inArg = true
		default:
			cur.WriteByte(c)
			inArg = true
		}
	}
	if quoted {
		return nil, false
	}
	if inArg {
		out = append(out, cur.String())
	}
	return out, true
}

var drivePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// norm makes a build-machine path slash separated (Windows drive and UNC
// paths use backslashes).
func norm(p string) string {
	if drivePath.MatchString(p) || strings.HasPrefix(p, `\\`) {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return p
}

func isAbs(p string) bool { return strings.HasPrefix(p, "/") || drivePath.MatchString(p) }

// winDir reports a compile DB entry written on Windows (drive or UNC
// directory): its paths use backslashes, also relative ones.
func winDir(dir string) bool {
	return drivePath.MatchString(dir) || strings.HasPrefix(dir, `\\`) || strings.HasPrefix(dir, "//")
}

// absOn resolves a build-machine path against the entry directory.
func absOn(dir, p string) (string, bool) {
	if winDir(dir) {
		p = strings.ReplaceAll(p, `\`, "/")
	} else {
		p = norm(p)
	}
	if !isAbs(p) {
		if !isAbs(dir) {
			return "", false
		}
		p = dir + "/" + p
	}
	return path.Clean(p), true
}

// mapUnder maps an absolute build-machine path under root to a snapshot
// path ("." for the root itself).
func mapUnder(root, p string) (string, bool) {
	if root == "" {
		return "", false
	}
	var rel string
	switch {
	case p == root:
		return ".", true
	case root == "/":
		rel = strings.TrimPrefix(p, "/")
	case strings.HasPrefix(p, root+"/"):
		rel = p[len(root)+1:]
	default:
		return "", false
	}
	if domain.ValidateEvidencePath(rel) != nil {
		return "", false
	}
	return rel, true
}

// inferRoot finds the build-machine directory the snapshot root was: the
// prefix that maps the most entry files onto included snapshot files (ties:
// the longest matching suffix). It is never recorded (an absolute path of
// another machine).
func inferRoot(files []string, snapshot map[string]source.Entry) string {
	score := map[string]int{}
	for _, f := range files {
		seen := map[string]bool{}
		for i := 0; i < len(f); i++ {
			if f[i] != '/' {
				continue
			}
			suffix := f[i+1:]
			if _, ok := snapshot[suffix]; !ok || suffix == "" {
				continue
			}
			root := f[:i]
			if root == "" {
				root = "/"
			}
			if !seen[root] {
				seen[root] = true
				score[root]++
			}
		}
	}
	best, bestScore := "", 0
	for root, n := range score {
		if n > bestScore || n == bestScore && (len(root) < len(best) || len(root) == len(best) && root < best) {
			best, bestScore = root, n
		}
	}
	return best
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

// takesValue are options whose value is the next argument when not joined.
var takesValue = map[string]bool{
	"-o": true, "-MF": true, "-MT": true, "-MQ": true, "-x": true, "-target": true, "-Xclang": true, "-Xpreprocessor": true, "-Xassembler": true,
	"-Xlinker": true, "-Xanalyzer": true, "-mllvm": true, "-arch": true, "-isysroot": true, "--sysroot": true, "-B": true, "-include-pch": true,
	"-iprefix": true, "-iwithprefix": true, "-iwithprefixbefore": true, "-imultilib": true, "-iframework": true, "-F": true, "-L": true, "-u": true,
	"-z": true, "-T": true, "-e": true, "--param": true, "-resource-dir": true, "-load": true, "--config": true, "-Xopenmp-target": true, "-MJ": true,
	"-ivfsoverlay": true, "--serialize-diagnostics": true, "-dependency-file": true, "-D": true, "-U": true,
}

// pathFlags take an include directory or file, joined ("-Idir") or separate.
var pathFlags = []struct {
	name string
	file bool
}{{"-isystem", false}, {"-iquote", false}, {"-idirafter", false}, {"-imacros", true}, {"-include", true}, {"-I", false}}

// keepF are the -f options that change how the code parses (language
// dialect, character signedness, predefined macros); every other -f option
// is dropped.
var keepF = map[string]bool{
	"-fexceptions": true, "-fno-exceptions": true, "-fcxx-exceptions": true, "-fno-cxx-exceptions": true, "-frtti": true, "-fno-rtti": true,
	"-fsigned-char": true, "-funsigned-char": true, "-fno-signed-char": true, "-fno-unsigned-char": true, "-fshort-enums": true, "-fshort-wchar": true,
	"-fms-extensions": true, "-fms-compatibility": true, "-fno-builtin": true, "-ffreestanding": true, "-fopenmp": true, "-fPIC": true, "-fpic": true,
	"-fPIE": true, "-fpie": true, "-fno-PIC": true, "-fno-pic": true, "-fno-PIE": true, "-fno-pie": true, "-fchar8_t": true, "-fno-char8_t": true,
	"-fcoroutines": true, "-fconcepts": true, "-fgnu89-inline": true, "-fwrapv": true, "-fdeclspec": true, "-fblocks": true, "-fgnu-keywords": true,
	"-fno-gnu-keywords": true, "-fasm": true, "-fno-asm": true, "-fdollars-in-identifiers": true, "-fno-dollars-in-identifiers": true,
	"-fno-common": true, "-fcommon": true, "-fno-operator-names": true, "-fpermissive": true, "-fno-threadsafe-statics": true,
}

var keepM = regexp.MustCompile(`^-m(?:32|64|x32|16|thumb|arm|abi=[a-z0-9_.-]+|arch=[A-Za-z0-9_.+-]+|cpu=[A-Za-z0-9_.+-]+|tune=[A-Za-z0-9_.+-]+|fpu=[A-Za-z0-9_.+-]+|float-abi=[a-z]+|(?:no-)?(?:sse[0-9.]*|ssse3|avx[0-9a-z]*|fma|bmi2?|popcnt|aes|pclmul|lzcnt|f16c|crc32|neon|altivec|vsx))$`)

func classifyDash(a string) string {
	switch {
	case a == "-c" || a == "-S" || a == "-E" || a == "-o" || strings.HasPrefix(a, "-o") || a == "-pipe" || a == "-v" || a == "-###" ||
		a == "-MD" || a == "-MMD" || a == "-MP" || a == "-M" || a == "-MM" || a == "-MG" || a == "-MF" || a == "-MT" || a == "-MQ" || a == "-MJ" ||
		strings.HasPrefix(a, "-MF") || strings.HasPrefix(a, "-MT") || strings.HasPrefix(a, "-MQ") || strings.HasPrefix(a, "-g") ||
		a == "-w" || a == "-fsyntax-only" || strings.HasPrefix(a, "-save-temps") || strings.HasPrefix(a, "-fdiagnostics") ||
		strings.HasPrefix(a, "-fcolor-diagnostics") || strings.HasPrefix(a, "-fno-color-diagnostics") || strings.HasPrefix(a, "-fmessage-length") ||
		strings.HasPrefix(a, "-fdebug-prefix-map") || strings.HasPrefix(a, "-ffile-prefix-map") || strings.HasPrefix(a, "-fmacro-prefix-map") ||
		a == "--serialize-diagnostics" || a == "-dependency-file":
		return dropStructural
	case strings.HasPrefix(a, "-fplugin") || strings.HasPrefix(a, "-fpass-plugin") || a == "-Xclang" || a == "-Xanalyzer" || a == "-load":
		return DropCodeLoading
	case a == "-mllvm":
		return DropBackendOption
	case strings.HasPrefix(a, "-Wl,") || strings.HasPrefix(a, "-Wa,") || strings.HasPrefix(a, "-Wp,") || a == "-Xpreprocessor" || a == "-Xassembler" ||
		a == "-Xlinker" || strings.HasPrefix(a, "-Xarch_") || strings.HasPrefix(a, "-Xopenmp-target"):
		return DropPassthrough
	case strings.HasPrefix(a, "-W") || a == "-pedantic" || a == "-pedantic-errors" || a == "-Werror":
		return dropWarningOption
	case a == "--config" || strings.HasPrefix(a, "--config=") || strings.HasPrefix(a, "--config-") || a == "--no-default-config" || a == "-ivfsoverlay":
		return DropDriverConfig
	case strings.HasPrefix(a, "--driver-mode"):
		return DropDriverMode
	case a == "--sysroot" || strings.HasPrefix(a, "--sysroot=") || strings.HasPrefix(a, "-isysroot") || strings.HasPrefix(a, "--gcc-toolchain") ||
		strings.HasPrefix(a, "--gcc-install-dir") || strings.HasPrefix(a, "-B") || a == "-resource-dir" || strings.HasPrefix(a, "--cuda-path") ||
		strings.HasPrefix(a, "--rocm-path"):
		return DropToolchainPath
	case strings.HasPrefix(a, "-fmodule") || strings.HasPrefix(a, "-fprebuilt-module") || strings.HasPrefix(a, "-fimplicit-module") || a == "-fmodules":
		return DropModules
	case strings.HasPrefix(a, "-fsanitize") || strings.HasPrefix(a, "-fprofile") || strings.HasPrefix(a, "-fcoverage") || strings.HasPrefix(a, "-finstrument") ||
		strings.HasPrefix(a, "-fxray") || strings.HasPrefix(a, "-fno-sanitize") || strings.HasPrefix(a, "-fno-profile") || a == "--coverage":
		return DropInstrumentation
	case strings.HasPrefix(a, "-include-pch") || strings.HasPrefix(a, "-fpch"):
		return DropPrecompiled
	case strings.HasPrefix(a, "-l") || strings.HasPrefix(a, "-L") || a == "-shared" || strings.HasPrefix(a, "-static") || a == "-rdynamic" ||
		a == "-s" || a == "-r" || a == "-u" || a == "-z" || a == "-T" || a == "-e" || a == "-nostdlib" || a == "-nostartfiles" || a == "-nodefaultlibs" || a == "-pie" || a == "-no-pie":
		return dropLinker
	}
	return ""
}

// mapFlags keeps the allowlisted arguments of one entry, mapping include
// paths onto the workspace. The entry's input file is replaced by the mapped
// main file when the tools render the entry.
func mapFlags(args []string, dir, root, fileArg, fileAbs string, files map[string]source.Entry, dirs map[string]bool, u *Unit, m *Mapping) {
	isInput := func(a string) bool {
		if a == fileArg {
			return true
		}
		p, ok := absOn(dir, a)
		return ok && p == fileAbs
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "":
			continue
		case isInput(a):
			// The main file (also one named like an option, "-x.c"); the
			// tools receive it as an absolute path.
			continue
		case strings.HasPrefix(a, "@"):
			m.Dropped[DropResponseFile]++
			continue
		case !strings.HasPrefix(a, "-"):
			m.Dropped[DropPositional]++
			continue
		}
		matched := false
		for _, pf := range pathFlags {
			if !strings.HasPrefix(a, pf.name) {
				continue
			}
			if pf.name == "-include" && strings.HasPrefix(a, "-include-pch") {
				break
			}
			matched = true
			value := strings.TrimPrefix(a, pf.name)
			if value == "" {
				var ok bool
				if value, ok = next(); !ok {
					m.Dropped[DropPositional]++
					break
				}
			}
			value = strings.TrimPrefix(value, "=")
			p, ok := absOn(dir, value)
			rel := ""
			if ok {
				rel, ok = mapUnder(root, p)
			}
			if !ok {
				m.Dropped[DropIncludeOutside]++
				break
			}
			if pf.file {
				if _, exists := files[rel]; !exists {
					m.FilesMissing++
				}
				u.Args = append(u.Args, Arg{Flag: pf.name, Path: rel})
			} else {
				if !dirs[rel] {
					m.DirsMissing++
				}
				u.Args = append(u.Args, Arg{Flag: pf.name, Path: rel, Joined: true})
			}
			break
		}
		if matched {
			continue
		}
		keep := func(ok bool, arg Arg) {
			if ok {
				u.Args = append(u.Args, arg)
			} else {
				m.Dropped[DropNotAllowlisted]++
			}
		}
		switch {
		case a == "-D" || a == "-U":
			if v, ok := next(); ok && v != "" {
				pat := macroPattern
				if a == "-U" {
					pat = undefPattern
				}
				keep(pat.MatchString(v) && len(v) <= maxMacroArg, Arg{Flag: a + v})
			} else {
				m.Dropped[DropPositional]++
			}
			continue
		case strings.HasPrefix(a, "-D"):
			keep(macroPattern.MatchString(a[2:]) && len(a) <= maxMacroArg, Arg{Flag: a})
			continue
		case strings.HasPrefix(a, "-U"):
			keep(undefPattern.MatchString(a[2:]), Arg{Flag: a})
			continue
		case strings.HasPrefix(a, "-std=") || strings.HasPrefix(a, "--std="):
			v := a[strings.IndexByte(a, '=')+1:]
			keep(stdPattern.MatchString(v), Arg{Flag: "-std=" + v})
			continue
		case a == "-x":
			// The language is rendered as "-x <v>" from a fixed set only.
			if v, ok := next(); ok && v != "" {
				keep(keepLanguages[v], Arg{Flag: a, Value: v})
			} else {
				m.Dropped[DropPositional]++
			}
			continue
		case a == "-target" || strings.HasPrefix(a, "--target="):
			v := strings.TrimPrefix(a, "--target=")
			if a == "-target" {
				var ok bool
				if v, ok = next(); !ok {
					m.Dropped[DropPositional]++
					continue
				}
			}
			keep(targetPattern.MatchString(v), Arg{Flag: "--target=" + v})
			continue
		case strings.HasPrefix(a, "-stdlib="):
			keep(keepStdlib[strings.TrimPrefix(a, "-stdlib=")], Arg{Flag: a})
			continue
		case a == "-ansi" || a == "-pthread" || a == "-nostdinc" || a == "-nostdinc++" || keepF[a] || keepM.MatchString(a) || optPattern.MatchString(a):
			u.Args = append(u.Args, Arg{Flag: a})
			continue
		}
		reason := classifyDash(a)
		if reason == "" {
			reason = DropNotAllowlisted
		}
		m.Dropped[reason]++
		if takesValue[a] {
			next()
		}
	}
}

// RenderedDriver is the driver name the tools see: "clang++" for C++
// (a C++ driver name, a C++ extension or -x c++), otherwise "clang". The
// entry's own driver name is recorded but never rendered: its prefix or
// suffix would select a target, a driver mode or configuration files.
func (u Unit) RenderedDriver() string {
	lang := ""
	for _, a := range u.Args {
		if a.Flag == "-x" {
			lang = a.Value
		}
	}
	if strings.HasPrefix(lang, "c++") || (lang == "" && (cxxDriver(u.Driver) || cxxExt[strings.ToLower(path.Ext(u.Rel))])) {
		return "clang++"
	}
	return "clang"
}

// Render returns the argv of a unit for the workspace: driver, kept
// arguments with absolute workspace paths, then the main file. Every
// positional (the main file, separate include paths) is absolute, so no
// repository path can be read as an option.
func (u Unit) Render(workspace string) []string {
	abs := func(rel string) string {
		if rel == "." {
			return workspace
		}
		return filepath.Join(workspace, filepath.FromSlash(rel))
	}
	out := []string{u.RenderedDriver()}
	for _, a := range u.Args {
		switch {
		case a.Path != "" && a.Joined:
			out = append(out, a.Flag+abs(a.Path))
		case a.Path != "":
			out = append(out, a.Flag, abs(a.Path))
		case a.Value != "":
			out = append(out, a.Flag, a.Value)
		default:
			out = append(out, a.Flag)
		}
	}
	return append(out, abs(u.Rel))
}

// File is the absolute workspace path of the unit's main file.
func (u Unit) File(workspace string) string {
	return filepath.Join(workspace, filepath.FromSlash(u.Rel))
}

// RenderDB writes the CODEYE compile DB the tools read: one entry per unit,
// directory = workspace root, every path absolute in the workspace.
func RenderDB(workspace string, units []Unit) ([]byte, error) {
	type out struct {
		Directory string   `json:"directory"`
		File      string   `json:"file"`
		Arguments []string `json:"arguments"`
	}
	if !filepath.IsAbs(workspace) {
		return nil, errors.New("workspace must be absolute")
	}
	list := make([]out, 0, len(units))
	for _, u := range units {
		if !u.safe() {
			return nil, errors.New("unsafe compile DB unit")
		}
		argv := u.Render(workspace)
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "@") || (!strings.HasPrefix(a, "-") && !filepath.IsAbs(a) && !keepLanguages[a]) {
				return nil, errors.New("unsafe compile DB argument")
			}
		}
		list = append(list, out{Directory: workspace, File: u.File(workspace), Arguments: argv})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(list); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Std summarizes the -std values of the units ("c99,c++17"), for target
// properties.
func Std(units []Unit) string {
	set := map[string]bool{}
	for _, u := range units {
		for _, a := range u.Args {
			if strings.HasPrefix(a.Flag, "-std=") {
				set[a.Flag[5:]] = true
			}
		}
	}
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	s := strings.Join(out, ",")
	if len(s) > 256 || slices.ContainsFunc(out, func(v string) bool { return strings.ContainsAny(v, "/\\") }) {
		return ""
	}
	return s
}
