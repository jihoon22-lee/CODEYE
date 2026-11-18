package python

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// DefaultPythonMinor is the Python 3 minor version passed to the tools when
// the module declares none that the pack understands (source
// codeye_default, limitation python_version_unknown). It is the newest
// release every pinned tool accepts as a target, because an old default
// turns newer syntax into parse errors while a new one only misses
// version-specific diagnostics (docs/42 §4).
const DefaultPythonMinor = 14

// Target version sources (target.properties python_version_source).
const (
	SourceRequiresPython = "requires_python"
	SourcePythonRequires = "python_requires"
	SourceVersionFile    = "python_version_file"
	SourceDefault        = "codeye_default"
)

// Limitation codes of the target resolution.
const (
	LimVersionUnknown       = "python_version_unknown"
	LimVersionUnreadable    = "python_version_declaration_unreadable"
	LimVersionNotUnderstood = "python_version_constraint_not_understood"
	LimVersionBelowTool     = "python_version_below_tool_minimum"
	LimVersionAboveTool     = "python_version_above_tool_maximum"
)

// pyTarget is the Python version a module declares, as the pack reads it.
type pyTarget struct {
	Minor       int    // Python 3.<Minor>
	Source      string // one of the Source* constants
	Declared    string // the declaration text ("" for the default)
	Limitations []string
}

func (t pyTarget) version() string { return "3." + strconv.Itoa(t.Minor) }

var (
	lowerBound  = regexp.MustCompile(`^(>=|~=|==|>)\s*3\.([0-9]{1,2})(?:\.(?:[0-9]+|\*))*$`)
	versionFile = regexp.MustCompile(`^3\.([0-9]{1,2})(?:\.[0-9]+)?$`)
)

// minimumMinor reads the lowest Python 3 minor a declaration allows: the
// largest lower bound (>=, ~=, ==, >) of a PEP 440 specifier list, or the
// version of .python-version. Anything else is not understood.
func minimumMinor(kind, constraint string) (int, bool) {
	c := strings.TrimSpace(constraint)
	if kind == SourceVersionFile {
		m := versionFile.FindStringSubmatch(c)
		if m == nil {
			return 0, false
		}
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	best, found := 0, false
	for _, spec := range strings.Split(c, ",") {
		m := lowerBound.FindStringSubmatch(strings.TrimSpace(spec))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if !found || n > best {
			best, found = n, true
		}
	}
	return best, found
}

// resolveTarget reads the module's declarations from the snapshot (bound to
// the manifest SHA-256), in this order: pyproject.toml requires-python,
// setup.cfg python_requires, .python-version. The first one understood
// wins; a missing or unreadable one is recorded, never guessed.
func resolveTarget(snapshotDir string, files map[string]source.Entry, moduleDir string) pyTarget {
	var lims []string
	for _, name := range []string{"pyproject.toml", "setup.cfg", ".python-version"} {
		rel := joinRel(moduleDir, name)
		e, ok := files[rel]
		if !ok {
			continue
		}
		b, err := govet.ReadSnapshotFile(snapshotDir, rel)
		if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
			lims = append(lims, LimVersionUnreadable)
			continue
		}
		kind, constraint, err := readDeclaration(rel, b)
		switch {
		case err != nil:
			lims = append(lims, LimVersionUnreadable)
			continue
		case kind == "":
			continue
		}
		if n, ok := minimumMinor(kind, constraint); ok {
			return pyTarget{Minor: n, Source: kind, Declared: constraint, Limitations: lims}
		}
		lims = append(lims, LimVersionNotUnderstood)
	}
	return pyTarget{Minor: DefaultPythonMinor, Source: SourceDefault, Limitations: append(lims, LimVersionUnknown)}
}

// clampMinor applies a tool's supported target range. A declaration
// outside it is passed as the nearest supported version and recorded.
func clampMinor(n, lo, hi int) (int, string) {
	switch {
	case n < lo:
		return lo, LimVersionBelowTool
	case n > hi:
		return hi, LimVersionAboveTool
	}
	return n, ""
}
