package python

import (
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// The Python version declaration is read with the env.discover rules
// (docs/37, internal/worker/discovery requirements.go) so the version the
// pack passes to the tools is the one the environment document reports.
// Analyzer packs sit below discovery in the import graph (discovery's tests
// build the analyzer catalog), so this is a rule-for-rule copy of the three
// Python readers; TestDeclarationMatchesDiscovery pins it against
// discovery.PythonRequirement on a generated corpus.

const (
	maxDeclarationFileBytes = 1 << 20 // discovery maxManifestBytes
	maxConstraintBytes      = 256     // discovery maxConstraintBytes
)

var errDeclarationMalformed = errors.New("malformed")

// readDeclaration returns the first Python version declaration of a module
// manifest: pyproject.toml [project] requires-python, setup.cfg [options]
// python_requires or the first version of .python-version. kind "" with a
// nil error means the file declares none.
func readDeclaration(p string, b []byte) (kind, constraint string, err error) {
	var reqs []declaration
	switch path.Base(p) {
	case "pyproject.toml":
		if len(b) > maxDeclarationFileBytes {
			return "", "", errDeclarationMalformed
		}
		reqs, err = tomlRequirement(b, "project", "requires-python", SourceRequiresPython)
	case "setup.cfg":
		if len(b) > maxDeclarationFileBytes {
			return "", "", errDeclarationMalformed
		}
		reqs, err = parseSetupCfg(b)
	case ".python-version":
		if len(b) > maxDeclarationFileBytes {
			return "", "", errDeclarationMalformed
		}
		reqs = parsePythonVersionFile(b)
	default:
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	for _, r := range reqs {
		// discovery keeps the constraint as the only repository text of its
		// artifact and rejects one that is long, has control characters or
		// looks like a secret; the pack rejects the same ones.
		if len(r.constraint) > maxConstraintBytes || !textSafe(r.constraint) || record.Sensitive([]byte(r.constraint)) {
			return "", "", errDeclarationMalformed
		}
	}
	if len(reqs) == 0 {
		return "", "", nil
	}
	return reqs[0].kind, reqs[0].constraint, nil
}

type declaration struct{ kind, constraint string }

func textSafe(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0xfffd {
			return false
		}
	}
	return true
}

// declLines splits manifest text into lines; a CR before LF is dropped.
func declLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	out := strings.Split(s, "\n")
	for i := range out {
		out[i] = strings.TrimSuffix(out[i], "\r")
	}
	return out
}

// tomlValue decodes a single-line TOML value: a basic or literal string, or
// the raw text of any other value; multi-line strings are malformed.
func tomlValue(v string) (string, error) {
	v = strings.TrimSpace(v)
	switch {
	case strings.HasPrefix(v, `"""`), strings.HasPrefix(v, `'''`):
		return "", errDeclarationMalformed
	case strings.HasPrefix(v, `"`):
		end := closingQuote(v)
		if end < 0 {
			return "", errDeclarationMalformed
		}
		s, err := strconv.Unquote(v[:end+1])
		if err != nil || !restIsComment(v[end+1:]) {
			return "", errDeclarationMalformed
		}
		return s, nil
	case strings.HasPrefix(v, `'`):
		end := strings.IndexByte(v[1:], '\'')
		if end < 0 || !restIsComment(v[end+2:]) {
			return "", errDeclarationMalformed
		}
		return v[1 : end+1], nil
	}
	if i := strings.Index(v, "#"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if v == "" {
		return "", errDeclarationMalformed
	}
	return v, nil
}

func closingQuote(v string) int {
	for i := 1; i < len(v); i++ {
		switch v[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}

func restIsComment(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || strings.HasPrefix(s, "#")
}

// tomlKeys yields (table, key, value) for simple "key = value" lines, with
// discovery's permissive headers and multi-line array skipping.
func tomlKeys(b []byte, visit func(table, key, value string) error) error {
	table, depth := "", 0
	for _, raw := range declLines(b) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if depth > 0 {
			depth += strings.Count(line, "[") - strings.Count(line, "]")
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.LastIndex(line, "]")
			if end < 0 {
				return errDeclarationMalformed
			}
			if !restIsComment(line[end+1:]) {
				continue
			}
			inner := strings.TrimSuffix(strings.TrimPrefix(line[:end+1], "["), "]")
			inner = strings.TrimSuffix(strings.TrimPrefix(inner, "["), "]")
			table = strings.NewReplacer(`"`, "", "'", "", " ", "", "\t", "").Replace(inner)
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			depth = strings.Count(v, "[") - strings.Count(v, "]")
		}
		key := strings.Trim(strings.TrimSpace(k), `"'`)
		if err := visit(table, key, v); err != nil {
			return err
		}
	}
	return nil
}

func tomlRequirement(b []byte, table, key, kind string) ([]declaration, error) {
	var out []declaration
	err := tomlKeys(b, func(t, k, v string) error {
		if t != table || k != key {
			return nil
		}
		if len(out) > 0 {
			return errDeclarationMalformed
		}
		val, err := tomlValue(v)
		if err != nil {
			return err
		}
		out = append(out, declaration{kind: kind, constraint: val})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseSetupCfg reads [options] python_requires (INI, single-line value).
func parseSetupCfg(b []byte) ([]declaration, error) {
	section := ""
	var out []declaration
	for _, raw := range declLines(b) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, errDeclarationMalformed
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		sep := strings.IndexAny(line, "=:")
		if sep < 0 || section != "options" {
			continue
		}
		k, v := line[:sep], line[sep+1:]
		if strings.TrimSpace(k) != "python_requires" {
			continue
		}
		if len(out) > 0 || strings.TrimSpace(v) == "" {
			return nil, errDeclarationMalformed
		}
		out = append(out, declaration{kind: SourcePythonRequires, constraint: strings.TrimSpace(v)})
	}
	return out, nil
}

// parsePythonVersionFile reads the first version of a .python-version file.
func parsePythonVersionFile(b []byte) []declaration {
	for _, l := range declLines(b) {
		if j := strings.Index(l, "#"); j >= 0 {
			l = l[:j]
		}
		if f := strings.Fields(l); len(f) > 0 {
			return []declaration{{kind: SourceVersionFile, constraint: f[0]}}
		}
	}
	return nil
}
