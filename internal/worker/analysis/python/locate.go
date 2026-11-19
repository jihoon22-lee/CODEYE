package python

import (
	"bytes"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Column units the tools report (measured, docs/ANALYZERS.md#python-규칙-팩): ruff 1-based code
// points, pyright 0-based UTF-16 code units, mypy (legacy parser) and
// bandit 0-based UTF-8 bytes. The adapters convert every column to 1-based
// bytes of the snapshot line, which is the report's unit.
const (
	unitByte  = "byte"
	unitRune  = "rune"
	unitUTF16 = "utf16"
)

// fileText is one snapshot file split into lines (without terminators).
type fileText struct {
	ok    bool
	sha   string
	lines [][]byte
}

type texts struct {
	snapshotDir string
	cache       map[string]*fileText
}

func (t *texts) get(rel string) *fileText {
	if f, ok := t.cache[rel]; ok {
		return f
	}
	f := &fileText{}
	if b, err := govet.ReadSnapshotFile(t.snapshotDir, rel); err == nil {
		f.ok, f.sha = true, obsdoc.SHA256Hex(b)
		for _, l := range bytes.SplitAfter(b, []byte{'\n'}) {
			if len(l) == 0 {
				continue
			}
			l = bytes.TrimSuffix(l, []byte{'\n'})
			l = bytes.TrimSuffix(l, []byte{'\r'})
			f.lines = append(f.lines, l)
		}
	}
	t.cache[rel] = f
	return f
}

// byteColumn converts a 1-based column in unit on line to a 1-based byte
// column. A column one past the end of the line (an exclusive end) is
// allowed; anything further, or invalid UTF-8 before it, is not.
func byteColumn(line []byte, unit string, col int) (int, bool) {
	if col < 1 {
		return 0, false
	}
	if unit == unitByte {
		return col, col <= len(line)+1
	}
	units, i := 1, 0
	for i < len(line) && units < col {
		r, size := utf8.DecodeRune(line[i:])
		if r == utf8.RuneError && size <= 1 {
			return 0, false
		}
		step := 1
		if unit == unitUTF16 && r >= 0x10000 {
			step = 2
		}
		units += step
		i += size
	}
	if units != col {
		return 0, false
	}
	return i + 1, true
}

// place is a tool position: 1-based lines, 1-based columns in unit (end
// exclusive). Zero lines mean no position.
type place struct {
	StartLine, StartCol, EndLine, EndCol int
	Unit                                 string
}

// locate maps a snapshot-relative path and tool place to an observation
// location bound to the manifest and the snapshot bytes.
func locate(rel string, ok bool, pl place, files map[string]source.Entry, drift map[string]bool, t *texts) (*string, *obsdoc.Location, string) {
	if !ok {
		return nil, nil, obsdoc.LocationOutsideSnapshot
	}
	if rel == "" || rel == "." || pl.StartLine < 1 {
		return nil, nil, obsdoc.LocationNoPosition
	}
	relp := rel
	entry, inManifest := files[rel]
	if !inManifest {
		return &relp, nil, obsdoc.LocationMissingFile
	}
	if drift[rel] {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	f := t.get(rel)
	if !f.ok || f.sha != entry.SHA256 {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	endLine, endCol := pl.EndLine, pl.EndCol
	if endLine < pl.StartLine || (endLine == pl.StartLine && endCol < pl.StartCol) {
		// A tool end before its start (bandit end_col_offset 0) is dropped:
		// the location is the start point.
		endLine, endCol = pl.StartLine, pl.StartCol
	}
	n := len(f.lines)
	lineAt := func(l int, end bool) ([]byte, bool) {
		switch {
		case l >= 1 && l <= n:
			return f.lines[l-1], true
		case (end && l == n+1) || (n == 0 && l == 1):
			// End positions may point at the start of the line after the
			// last one (an exclusive end after a final newline).
			return nil, true
		}
		return nil, false
	}
	sl, ok1 := lineAt(pl.StartLine, false)
	el, ok2 := lineAt(endLine, true)
	if !ok1 || !ok2 {
		return &relp, nil, obsdoc.LocationLineOutOfRange
	}
	sc, ok1 := byteColumn(sl, pl.Unit, max(pl.StartCol, 1))
	ec, ok2 := byteColumn(el, pl.Unit, max(endCol, 1))
	if !ok1 || !ok2 {
		return &relp, nil, obsdoc.LocationLineOutOfRange
	}
	if endLine == pl.StartLine && ec < sc {
		ec = sc
	}
	if endLine > n {
		// Keep the end inside the file: the end of the last line.
		endLine = max(n, 1)
		ec = len(lineOr(f.lines, endLine)) + 1
	}
	return &relp, &obsdoc.Location{Path: rel, StartLine: pl.StartLine, StartCol: sc, EndLine: endLine, EndCol: ec, ColumnUnit: unitByte, BlobSHA256: entry.SHA256}, obsdoc.LocationResolved
}

func lineOr(lines [][]byte, l int) []byte {
	if l >= 1 && l <= len(lines) {
		return lines[l-1]
	}
	return nil
}
