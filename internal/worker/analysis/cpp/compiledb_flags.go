package cpp

import (
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

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
