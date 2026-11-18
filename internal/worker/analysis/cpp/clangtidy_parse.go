package cpp

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// Parser identity: the clang-tidy 23 diagnostic stream on stdout (header
// lines "file:line:col: level: message [check]" followed by source snippet
// lines, which clang-tidy prints with its own options: -fno-caret-diagnostics
// in the compile command does not remove them, measured) and the stderr
// progress/summary lines. --export-fixes YAML carries the same diagnostics
// but is a file, and an observation must be bound to a recorded stream.
const (
	ClangTidyParserID      = "clang-tidy-text-v1"
	ClangTidyParserVersion = "1"
)

var (
	ctLocated   = regexp.MustCompile(`^(.+?):([0-9]+):([0-9]+): (warning|error|note|fatal error|remark): (.*)$`)
	ctUnlocated = regexp.MustCompile(`^(warning|error|note|fatal error|remark): (.*)$`)
	ctCheck     = regexp.MustCompile(`^(.*) \[([A-Za-z0-9][A-Za-z0-9.,_+-]*)\]$`)
	ctSnippet   = regexp.MustCompile(`^ *[0-9]* \|(?: |$)`)
)

// ctNote is a note attached to the preceding diagnostic.
type ctNote struct {
	File      string
	Line, Col int
	Message   string
}

// ctDiag is one warning or error with its notes. ByteStart/ByteEnd cover the
// header line and its snippet lines in stdout.
type ctDiag struct {
	Index              int
	File               string
	Line, Col          int
	Level, Message     string
	Checks             []string
	Notes              []ctNote
	ByteStart, ByteEnd int64
}

// ctStream is the parse of stdout. ShapeErrors counts lines that are not of
// the pinned format (an orphan note, a warning without check, an unknown
// line); their bytes are UnparsedBytes. Neither is ever "no findings".
type ctStream struct {
	Diags         []ctDiag
	Values        int
	ShapeErrors   int
	UnparsedBytes int64
}

func (d *ctDiag) Rule() string {
	if len(d.Checks) == 0 {
		return ""
	}
	return d.Checks[0]
}

func parseClangTidyStdout(stdout []byte) ctStream {
	var out ctStream
	cur := -1 // index of the diagnostic notes and snippets attach to
	inNote := false
	off := int64(0)
	for off < int64(len(stdout)) {
		end := int64(bytes.IndexByte(stdout[off:], '\n'))
		next := off + end + 1
		if end < 0 {
			end, next = int64(len(stdout))-off, int64(len(stdout))
		}
		line := strings.TrimRight(string(stdout[off:off+end]), "\r")
		lineStart := off
		off = next
		if line == "" {
			continue
		}
		if ctSnippet.MatchString(line) {
			if cur < 0 {
				out.ShapeErrors++
				out.UnparsedBytes += next - lineStart
				continue
			}
			if !inNote {
				out.Diags[cur].ByteEnd = next
			}
			continue
		}
		var file, level, msg string
		ln, col := 0, 0
		if m := ctLocated.FindStringSubmatch(line); m != nil {
			file, level, msg = m[1], m[4], m[5]
			ln, _ = strconv.Atoi(m[2])
			col, _ = strconv.Atoi(m[3])
		} else if m := ctUnlocated.FindStringSubmatch(line); m != nil {
			level, msg = m[1], m[2]
		} else {
			out.ShapeErrors++
			out.UnparsedBytes += next - lineStart
			continue
		}
		if level == "note" {
			if cur < 0 {
				out.ShapeErrors++
				out.UnparsedBytes += next - lineStart
				continue
			}
			out.Diags[cur].Notes = append(out.Diags[cur].Notes, ctNote{File: file, Line: ln, Col: col, Message: msg})
			inNote = true
			continue
		}
		out.Values++
		d := ctDiag{Index: out.Values - 1, File: file, Line: ln, Col: col, Level: level, Message: msg, ByteStart: lineStart, ByteEnd: next}
		if m := ctCheck.FindStringSubmatch(msg); m != nil {
			d.Message = m[1]
			d.Checks = strings.Split(m[2], ",")
		}
		if level == "warning" && len(d.Checks) == 0 {
			// clang-tidy names the check of every warning it prints.
			out.ShapeErrors++
			out.UnparsedBytes += next - lineStart
			cur, inNote = -1, false
			continue
		}
		out.Diags = append(out.Diags, d)
		cur, inNote = len(out.Diags)-1, false
	}
	return out
}

// Stderr lines of clang-tidy 23.
var (
	ctProcessing = regexp.MustCompile(`^\[([0-9]+)/([0-9]+)\] Processing file (.+)\.$`)
	ctGenerated  = regexp.MustCompile(`^(?:([0-9]+) warnings?(?: and ([0-9]+) errors?)?|([0-9]+) errors?) generated\.$`)
	ctErrorWhile = regexp.MustCompile(`^Error while processing (.+)\.$`)
	ctSuppressed = regexp.MustCompile(`^Suppressed [0-9]+ warnings? \((.*)\)\.$`)
	ctNolint     = regexp.MustCompile(`([0-9]+) NOLINT`)
	ctSkipping   = regexp.MustCompile(`^Skipping (.+)\. Compile command not found\.$`)
)

const ctHeaderHint = "Use -header-filter=.* or leave it as default to display errors from all non-system headers. Use -system-headers to display errors from system headers as well."

// ctUnit is the stderr block of one translation unit: from its
// "[i/N] Processing file F." line (multi-file runs; a single-file run has
// one block with File "") to the next. The error count of the
// "W warnings and E errors generated." line is cumulative over the run
// (measured, clang-tidy 23.1.5), and "Error while processing F." is printed
// for every unit once that count is non-zero: a unit failed iff its count
// grew.
type ctUnit struct {
	File      string
	Index     int // i of [i/N]; 0 in single-file runs
	Total     int // N of [i/N]
	Errors    int // cumulative errors after the unit (carried when no line)
	ErrorLine bool
	// Foreign counts "Error while processing" lines naming another file.
	Foreign int
}

// ctStderr is the classified stderr of one check execution.
type ctStderr struct {
	Units   []ctUnit
	Skipped []string // "Skipping F. Compile command not found."
	Nolint  int      // NOLINT count of "Suppressed N warnings (...)"
	// Fatal lines: the compile DB was not loaded ("Running without flags."
	// is a silent fallback to no flags), usage errors, crashes.
	Fatal []string
	Other int
}

func parseClangTidyStderr(stderr []byte) ctStderr {
	var out ctStderr
	inDBError := false
	cur := -1
	block := func() *ctUnit {
		if cur < 0 {
			// Single-file run: one implicit block.
			out.Units = append(out.Units, ctUnit{})
			cur = 0
		}
		return &out.Units[cur]
	}
	for _, raw := range strings.Split(string(stderr), "\n") {
		l := strings.TrimRight(raw, "\r")
		switch {
		case strings.TrimSpace(l) == "":
		case ctProcessing.MatchString(l):
			m := ctProcessing.FindStringSubmatch(l)
			i, _ := strconv.Atoi(m[1])
			n, _ := strconv.Atoi(m[2])
			prev := 0
			if cur >= 0 {
				prev = out.Units[cur].Errors
			}
			out.Units = append(out.Units, ctUnit{File: m[3], Index: i, Total: n, Errors: prev})
			cur = len(out.Units) - 1
			inDBError = false
		case ctGenerated.MatchString(l):
			m := ctGenerated.FindStringSubmatch(l)
			e := 0
			if m[2] != "" {
				e, _ = strconv.Atoi(m[2])
			} else if m[3] != "" {
				e, _ = strconv.Atoi(m[3])
			}
			block().Errors = e
		case l == "Found compiler error(s).", l == ctHeaderHint:
		case ctErrorWhile.MatchString(l):
			u := block()
			if f := ctErrorWhile.FindStringSubmatch(l)[1]; u.File == "" && u.Index == 0 && !u.ErrorLine {
				u.File, u.ErrorLine = f, true
			} else if f == u.File && !u.ErrorLine {
				u.ErrorLine = true
			} else {
				u.Foreign++
			}
		case ctSuppressed.MatchString(l):
			for _, m := range ctNolint.FindAllStringSubmatch(ctSuppressed.FindStringSubmatch(l)[1], -1) {
				n, _ := strconv.Atoi(m[1])
				out.Nolint += n
			}
		case ctSkipping.MatchString(l):
			out.Skipped = append(out.Skipped, ctSkipping.FindStringSubmatch(l)[1])
		case strings.HasPrefix(l, "Error while trying to load a compilation database"):
			inDBError = true
			out.Fatal = append(out.Fatal, l)
		case l == "Running without flags.":
			out.Fatal = append(out.Fatal, l)
			inDBError = false
		case inDBError:
			out.Fatal = append(out.Fatal, l)
		case strings.HasPrefix(l, "Error: "), strings.HasPrefix(l, "USAGE: "), strings.HasPrefix(l, "LLVM ERROR"), strings.HasPrefix(l, "PLEASE submit a bug report"),
			strings.HasPrefix(l, "Stack dump"), strings.Contains(l, "Segmentation fault"), strings.HasPrefix(l, "clang-tidy: error"):
			out.Fatal = append(out.Fatal, l)
		default:
			out.Other++
		}
	}
	return out
}

// clang-tidy --version prints three lines: "LLVM (http://llvm.org/):",
// "  LLVM version 23.1.5", "  Optimized build." (vendor builds prefix the
// product name). Exactly one version line must be present.
var ctVersionLine = regexp.MustCompile(`^\s*(?:[A-Za-z][A-Za-z0-9_.+-]* )?LLVM version ([0-9]+\.[0-9]+\.[0-9]+)\s*$`)

// ParseClangTidyVersion returns "23.1.5". Development builds ("23.1.5git")
// and other output are not recognized.
func ParseClangTidyVersion(stdout []byte) (string, bool) {
	found := ""
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
		if m := ctVersionLine.FindStringSubmatch(strings.TrimRight(l, "\r")); m != nil {
			found = m[1]
			n++
		}
	}
	if n != 1 || !strings.Contains(string(stdout), "LLVM (http://llvm.org/)") {
		return "", false
	}
	return found, true
}

// ClangTidyTestedRelease is the LLVM series whose output the parser fixtures
// pin (captured from 23.1.5).
const ClangTidyTestedRelease = "23.1"

// ClangTidyVersionTested reports a version of the tested series.
func ClangTidyVersionTested(v string) bool { return strings.HasPrefix(v, ClangTidyTestedRelease+".") }
