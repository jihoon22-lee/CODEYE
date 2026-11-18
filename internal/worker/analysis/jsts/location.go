package jsts

import (
	"bytes"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Resolver maps tool positions to snapshot locations. tsc and ESLint count
// columns in UTF-16 code units of the line (JavaScript strings) after a
// leading byte order mark; the resolver converts them to the byte columns
// the report uses, reading the snapshot file it verifies against the
// manifest. A column it cannot convert keeps the utf16 unit.
type Resolver struct {
	SnapshotDir string
	ModuleDir   string
	Roots       []string
	Files       map[string]source.Entry
	Drift       map[string]bool
	cache       map[string]*fileLines
}

type fileLines struct {
	ok    bool
	sha   string
	lines [][]byte
	bom   bool
}

func (r *Resolver) file(rel string) *fileLines {
	if r.cache == nil {
		r.cache = map[string]*fileLines{}
	}
	if f, ok := r.cache[rel]; ok {
		return f
	}
	f := &fileLines{}
	if b, err := govet.ReadSnapshotFile(r.SnapshotDir, rel); err == nil {
		f.ok, f.sha = true, obsdoc.SHA256Hex(b)
		f.bom = bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF})
		if f.bom {
			b = b[3:]
		}
		f.lines = bytes.Split(b, []byte("\n"))
		if len(f.lines) > 1 && len(f.lines[len(f.lines)-1]) == 0 {
			f.lines = f.lines[:len(f.lines)-1]
		}
	}
	r.cache[rel] = f
	return f
}

// utf16ToByte converts a 1-based UTF-16 column of line to a 1-based byte
// column; ok is false past the end of the line or inside a surrogate pair.
func utf16ToByte(line []byte, col int) (int, bool) {
	if col < 1 {
		return 0, false
	}
	units, i := 0, 0
	for units < col-1 {
		if i >= len(line) {
			return 0, false
		}
		r, n := utf8.DecodeRune(line[i:])
		w := 1
		if r >= 0x10000 {
			w = 2
		}
		if units+w > col-1 {
			return 0, false
		}
		units += w
		i += n
	}
	return i + 1, true
}

// Resolve maps a position. path is the tool's file path (absolute workspace,
// redacted or module-relative); line and the columns are 1-based (column 0 =
// unknown); endLine 0 means a single point.
func (r *Resolver) Resolve(toolPath string, line, col, endLine, endCol int) (*string, *obsdoc.Location, string) {
	if toolPath == "" {
		return nil, nil, obsdoc.LocationNoPosition
	}
	rel, ok := StripWorkspace(toolPath, r.ModuleDir, r.Roots)
	if !ok || rel == "." {
		return nil, nil, obsdoc.LocationOutsideSnapshot
	}
	p := rel
	entry, ok := r.Files[rel]
	if !ok {
		return &p, nil, obsdoc.LocationMissingFile
	}
	if line < 1 {
		return nil, nil, obsdoc.LocationNoPosition
	}
	if r.Drift[rel] {
		return &p, nil, obsdoc.LocationSourceDrift
	}
	f := r.file(rel)
	if !f.ok || f.sha != entry.SHA256 {
		return &p, nil, obsdoc.LocationSourceDrift
	}
	if endLine < 1 {
		endLine, endCol = line, col
	}
	n := max(len(f.lines), 1)
	if line > n || endLine < line || endLine > n {
		return &p, nil, obsdoc.LocationLineOutOfRange
	}
	unit := "byte"
	sc, ec := col, endCol
	text := func(l int) []byte {
		if l-1 < len(f.lines) {
			return f.lines[l-1]
		}
		return nil
	}
	if col > 0 {
		b, ok1 := utf16ToByte(text(line), col)
		e, ok2 := b, true
		if endCol > 0 {
			e, ok2 = utf16ToByte(text(endLine), endCol)
		}
		if ok1 && ok2 {
			sc, ec = b, e
		} else {
			unit, sc, ec = "utf16", col, endCol
		}
	}
	if endLine == line && ec < sc {
		ec = sc
	}
	return &p, &obsdoc.Location{Path: rel, StartLine: line, StartCol: sc, EndLine: endLine, EndCol: ec, ColumnUnit: unit, BlobSHA256: entry.SHA256}, obsdoc.LocationResolved
}

// Lines reports a snapshot file's line count and SHA-256 as read.
func (r *Resolver) Lines(rel string) (int, string, bool) {
	f := r.file(rel)
	return len(f.lines), f.sha, f.ok
}
