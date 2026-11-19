package cpp

import (
	"bytes"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Stream sanitization before anything is stored. A repository can make the
// tools read files outside the workspace copy (#include "/abs/path", "..",
// #embed), and both tools quote what they read: clang-tidy prints the source
// line of every diagnostic and note (measured: -fno-caret-diagnostics and
// -fno-diagnostics-show-line-numbers do not remove clang-tidy's snippets),
// cppcheck's messages and <symbol> elements name tokens of the file. The
// executor result is rewritten before the execution is written, so neither
// the stored streams nor the envelope hold that text:
//
//   - clang-tidy: every source snippet line is removed; a diagnostic whose
//     file is outside the workspace is removed with its notes and counted,
//     and a note outside the workspace is removed from a kept diagnostic.
//   - cppcheck: an <error> record with any location outside the workspace is
//     removed and kept only as id, severity and file0 (the unit) for the
//     outcome.
//
// The tool process can still read any file the node account can read; only
// persistence is prevented, and a diagnostic located in a workspace file can
// still carry a value computed from such a file (docs/ANALYZERS.md#작업-사본-밖-파일-내용의-저장-차단).

// LimOutsideDropped: diagnostics located outside the workspace copy were
// removed from the stored streams and the envelope.
const LimOutsideDropped = "outside_workspace_diagnostics_dropped"

// ctDropped counts what sanitizeClangTidy removed from one stdout.
type ctDropped struct {
	Diagnostics int // warnings, errors and remarks outside the workspace
	Errors      int // of which compiler errors
	Notes       int // notes outside the workspace (also of kept diagnostics)
	Snippets    int // source snippet lines
}

// sanitizeClangTidy rewrites r.Stdout. inside reports a tool path in the
// workspace copy (or a clang pseudo file such as "<command line>").
func sanitizeClangTidy(r *executor.Result, inside func(string) bool) ctDropped {
	var d ctDropped
	if r == nil {
		return d
	}
	data := r.Stdout.Data
	var out bytes.Buffer
	dropping := false
	for off := 0; off < len(data); {
		end := bytes.IndexByte(data[off:], '\n')
		next := off + end + 1
		if end < 0 {
			end, next = len(data)-off, len(data)
		}
		raw := data[off:next]
		line := strings.TrimRight(string(data[off:off+end]), "\r")
		off = next
		if ctSnippet.MatchString(line) {
			d.Snippets++
			continue
		}
		m := ctLocated.FindStringSubmatch(line)
		switch {
		case m == nil:
			// Unlocated diagnostics and lines of another shape are kept for
			// the parser (which fails on an unknown shape).
			if ctUnlocated.MatchString(line) {
				dropping = false
			}
		case m[4] == "note":
			if dropping {
				d.Notes++
				continue
			}
			if !inside(m[1]) {
				d.Notes++
				continue
			}
		default:
			dropping = !inside(m[1])
			if dropping {
				d.Diagnostics++
				if m[4] == "error" || m[4] == "fatal error" {
					d.Errors++
				}
				continue
			}
		}
		out.Write(raw)
	}
	replaceStream(&r.Stdout, out.Bytes())
	return d
}

// sanitizeCppcheck rewrites r.Stderr (the XML document) and returns the
// removed records without their text.
func sanitizeCppcheck(r *executor.Result, inside func(string) bool) []ccRecord {
	if r == nil {
		return nil
	}
	data := r.Stderr.Data
	st := parseCppcheckXML(data)
	var dropped []ccRecord
	var out bytes.Buffer
	last := 0
	for _, rec := range st.Records {
		outside := false
		for _, l := range rec.Locations {
			outside = outside || !insideOrPseudo(l.File, inside)
		}
		if !outside {
			continue
		}
		start, end := int(rec.ByteStart), int(rec.ByteEnd)
		// Remove the record's whole lines when it is alone on them.
		for start > last && (data[start-1] == ' ' || data[start-1] == '\t') {
			start--
		}
		if end < len(data) && data[end] == '\n' {
			end++
		}
		out.Write(data[last:start])
		last = end
		dropped = append(dropped, ccRecord{Index: rec.Index, ID: rec.ID, Severity: rec.Severity, File0: rec.File0})
	}
	if len(dropped) == 0 {
		return nil
	}
	out.Write(data[last:])
	replaceStream(&r.Stderr, out.Bytes())
	return dropped
}

func insideOrPseudo(file string, inside func(string) bool) bool {
	return strings.HasPrefix(file, "<") || file == "nofile" || inside(file)
}

func replaceStream(s *executor.Stream, data []byte) {
	removed := int64(len(s.Data) - len(data))
	s.Data = data
	s.TotalBytes -= removed
	if s.TotalBytes < int64(len(data)) {
		s.TotalBytes = int64(len(data))
	}
}
