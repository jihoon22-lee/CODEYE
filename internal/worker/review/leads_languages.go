package review

import (
	"path"
	"regexp"
	"strings"
)

// The languages a definition lead is resolved in, their token scans, and
// the names each language defines itself (docs/REVIEW.md#단서-해석).

// leadDefinitionLanguage is the language whose binding rules resolve a
// definition lead anchored in p: go, java, python, javascript (JavaScript
// and TypeScript, JSX included), rust, c or cpp; "" when no resolver reads
// p. A .c file is C; a header is read by the C++ rules, the stricter ones
// (overloads, namespaces, classes), since no translation unit decides its
// language.
func leadDefinitionLanguage(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".py":
		return "python"
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return "javascript"
	case ".rs":
		return "rust"
	case ".c":
		return "c"
	case ".cc", ".cpp", ".cxx", ".c++", ".h", ".hh", ".hpp", ".hxx", ".h++":
		return "cpp"
	}
	return ""
}

// typeScriptPath reports a TypeScript source (its overload signatures and
// type-only imports bind nothing at run time).
func typeScriptPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	}
	return false
}

// leadTokens scans text of language lang for a lead's binding. Python uses
// the shared scan; JavaScript and TypeScript the member scan (a template
// literal is one opaque token) that also reads "/" as division after an
// operand; Rust the Rust scan (raw strings, lifetimes, nested comments).
// C and C++ text is scanned with comments, strings, character literals and
// preprocessor lines blanked (nativeText), so "/" and "\" are operators. nil
// when the scan cannot separate the text safely.
func leadTokens(lang, text string) []callToken {
	switch lang {
	case "python":
		return callTokens(text, true)
	case "javascript":
		return lexTokens(text, false, true, false, true, callMaxTokens)
	case "rust":
		return lexTokens(text, false, false, true, false, callMaxTokens)
	case "c", "cpp":
		masked, ok := nativeText(text)
		if !ok {
			return nil
		}
		return lexTokens(masked, false, false, true, false, callMaxTokens)
	}
	return nil
}

// nativeUnsafe finds C++ syntax the blanking pass cannot read: a raw string
// literal (R"(...)") or a digit separator (1'000).
var nativeUnsafe = regexp.MustCompile(`(^|[^A-Za-z0-9_])(u8|u|U|L)?R"|[0-9]'[0-9A-Fa-f]`)

// nativeText is C or C++ text with comments, strings, character literals and
// preprocessor lines (with their continuation lines) blanked, every byte
// and line kept in place; false when nativeUnsafe syntax may hide code.
func nativeText(text string) (string, bool) {
	if nativeUnsafe.MatchString(text) {
		return "", false
	}
	masked := []byte(contractExecutableSource(text, "c"))
	offset := 0
	continued := false
	for _, line := range strings.SplitAfter(string(masked), "\n") {
		trimmed := strings.TrimSpace(line)
		directive := continued || strings.HasPrefix(trimmed, "#")
		continued = directive && strings.HasSuffix(trimmed, "\\")
		if directive {
			maskContractRange(masked[offset : offset+len(line)])
		}
		offset += len(line)
	}
	return string(masked), true
}

// Names a language defines itself. A refused file never names the reason of
// a lead whose call names one of them, unless an import or a declaration of
// the anchor's file binds that name (then it is a project name). The lists
// are fixed and hold the callable builtins, standard global objects and
// exceptions; a project definition of the same name is still resolved.
var leadBuiltins = map[string]map[string]bool{
	"go": leadNames(`append cap clear close complex copy delete imag len make max min new panic print println real recover
		bool byte complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune string uint uint8 uint16 uint32 uint64 uintptr any`),
	"python": leadNames(`abs aiter all anext any ascii bin bool breakpoint bytearray bytes callable chr classmethod compile complex
		delattr dict dir divmod enumerate eval exec filter float format frozenset getattr globals hasattr hash help hex id input int
		isinstance issubclass iter len list locals map max memoryview min next object oct open ord pow print property range repr
		reversed round set setattr slice sorted staticmethod str sum super tuple type vars zip __import__
		BaseException Exception ArithmeticError AssertionError AttributeError EOFError ImportError ModuleNotFoundError IndexError
		KeyError KeyboardInterrupt LookupError MemoryError NameError NotImplementedError OSError OverflowError RecursionError
		RuntimeError StopIteration SystemExit TypeError UnicodeError UnicodeDecodeError UnicodeEncodeError ValueError
		ZeroDivisionError ConnectionError TimeoutError FileExistsError FileNotFoundError PermissionError IOError`),
	"javascript": leadNames(`parseInt parseFloat isNaN isFinite encodeURI encodeURIComponent decodeURI decodeURIComponent
		setTimeout setInterval setImmediate clearTimeout clearInterval clearImmediate queueMicrotask structuredClone fetch require
		Number String Boolean Symbol BigInt Object Array Function Date RegExp Map Set WeakMap WeakSet Promise Proxy Reflect JSON Math
		Intl console process Buffer globalThis window document
		Error TypeError RangeError SyntaxError ReferenceError EvalError URIError AggregateError`),
	"java": leadNames(`System String Math Integer Long Double Float Short Byte Character Boolean Object Thread StringBuilder Runtime
		Exception RuntimeException IllegalArgumentException IllegalStateException NullPointerException`),
	"rust": leadNames(`drop Some None Ok Err Box Vec String Option Result Default`),
	"c": leadNames(`printf fprintf sprintf snprintf vprintf vfprintf vsnprintf puts fputs putchar fputc putc getchar fgetc getc fgets
		scanf sscanf fscanf fopen fclose fread fwrite fflush fseek ftell perror malloc calloc realloc free memcpy memmove memset
		memcmp memchr strlen strcmp strncmp strcpy strncpy strcat strncat strchr strrchr strstr strtok strdup strerror strtol strtoul
		strtod atoi atol atof abs labs qsort bsearch rand srand exit abort atexit getenv assert isalpha isdigit isspace isalnum
		isupper islower toupper tolower`),
}

func leadNames(list string) map[string]bool {
	out := map[string]bool{}
	for _, name := range strings.Fields(list) {
		out[name] = true
	}
	return out
}

// leadBuiltin reports a name the language of lang defines itself; C++ has
// the C list (its std names are qualified).
func leadBuiltin(lang, name string) bool {
	if lang == "cpp" {
		lang = "c"
	}
	return leadBuiltins[lang][name]
}
