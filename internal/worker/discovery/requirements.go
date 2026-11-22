package discovery

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Limitation codes recorded by requirement parsing.
const (
	LimManifestMalformed      = "manifest_malformed"
	LimManifestNotRead        = "manifest_not_read"
	LimWorkspaceInheritance   = "workspace_inheritance_not_resolved"
	LimExtraPythonVersions    = "additional_python_versions_ignored"
	LimConstraintNotCompared  = "constraint_not_compared"
	LimToolNotProbed          = "tool_not_probed"
	LimToolProbeFailed        = "tool_probe_failed"
	LimGoWorkIgnoredByGoVet   = "go_work_not_used_by_go_vet"
	maxManifestBytes          = 1 << 20
	maxConstraintBytes        = 256
	errMalformed              = parseError("malformed")
	inheritedRustVersionValue = "workspace"
)

type parseError string

func (e parseError) Error() string { return string(e) }

// declared is one requirement found in a manifest, before IDs are attached.
type declared struct {
	Kind, Constraint string
	Start, End       int
	Limitations      []string
}

// lines splits manifest text into lines without line terminators; a CR
// before LF (Windows checkouts) is dropped.
func lines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	out := strings.Split(s, "\n")
	for i := range out {
		out[i] = strings.TrimSuffix(out[i], "\r")
	}
	return out
}

// lineAt returns the 1-based line of byte offset off.
func lineAt(b []byte, off int) int {
	if off > len(b) {
		off = len(b)
	}
	return 1 + bytes.Count(b[:off], []byte{'\n'})
}

// parseManifest dispatches on the manifest base name. It never executes or
// evaluates manifest content. ok=false means no parser exists for the file.
func parseManifest(p string, b []byte) (reqs []declared, ok bool, err error) {
	switch path.Base(p) {
	case "go.mod":
		reqs, err = parseGoDirectives(b, "go_directive", "go_toolchain")
	case "go.work":
		reqs, err = parseGoDirectives(b, "go_work_directive", "go_work_toolchain")
	case "package.json":
		reqs, err = parsePackageJSON(b)
	case "pyproject.toml":
		reqs, err = tomlRequirement(b, "project", "requires-python", "requires_python")
	case "setup.cfg":
		reqs, err = parseSetupCfg(b)
	case ".python-version":
		reqs, err = parsePythonVersion(b)
	case "Cargo.toml":
		reqs, err = parseCargo(b)
	case "rust-toolchain", "rust-toolchain.toml":
		reqs, err = parseRustToolchain(b, path.Base(p) == "rust-toolchain.toml")
	case "CMakeLists.txt":
		reqs, err = parseCMake(b)
	case "pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts":
		declarations, parseErr := javaproject.Parse(path.Base(p), b)
		err = parseErr
		for _, req := range declarations {
			reqs = append(reqs, declared{Kind: req.Kind, Constraint: req.Value, Start: req.Line, End: req.Line})
		}
	default:
		return nil, false, nil
	}
	for _, r := range reqs {
		// The constraint is the only repository text kept in the artifact.
		if len(r.Constraint) > maxConstraintBytes || !textSafe(r.Constraint) || record.Sensitive([]byte(r.Constraint)) {
			return nil, true, errMalformed
		}
	}
	return reqs, true, err
}

func textSafe(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0xfffd {
			return false
		}
	}
	return true
}

// parseGoDirectives reads the go and toolchain directives of go.mod/go.work.
// A repeated directive or one without exactly one argument is malformed.
func parseGoDirectives(b []byte, goKind, toolchainKind string) ([]declared, error) {
	var out []declared
	seen := map[string]bool{}
	depth := 0
	for i, raw := range lines(b) {
		line := raw
		if j := strings.Index(line, "//"); j >= 0 {
			line = line[:j]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if depth > 0 {
			if f[0] == ")" {
				depth--
			}
			continue
		}
		if f[len(f)-1] == "(" {
			depth++
			continue
		}
		var kind string
		switch f[0] {
		case "go":
			kind = goKind
		case "toolchain":
			kind = toolchainKind
		default:
			continue
		}
		if len(f) != 2 || seen[kind] {
			return nil, errMalformed
		}
		seen[kind] = true
		out = append(out, declared{Kind: kind, Constraint: f[1], Start: i + 1, End: i + 1})
	}
	return out, nil
}

// parsePackageJSON finds engines.node. The JSON must be one strict object
// (duplicate keys are malformed); positions come from the token stream.
func parsePackageJSON(b []byte) ([]declared, error) {
	if _, err := domain.CanonicalJSON(b); err != nil {
		return nil, errMalformed
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errMalformed
	}
	var out []declared
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, errMalformed
		}
		if key != "engines" {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return nil, errMalformed
			}
			continue
		}
		tok, err := dec.Token()
		if err != nil {
			return nil, errMalformed
		}
		if tok != json.Delim('{') {
			if d, ok := tok.(json.Delim); ok && d == '[' {
				for dec.More() {
					var skip json.RawMessage
					if dec.Decode(&skip) != nil {
						return nil, errMalformed
					}
				}
				if _, err := dec.Token(); err != nil {
					return nil, errMalformed
				}
			}
			continue // engines is not an object: nothing declared
		}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, errMalformed
			}
			var raw json.RawMessage
			if dec.Decode(&raw) != nil {
				return nil, errMalformed
			}
			if k != "node" {
				continue
			}
			var c string
			if json.Unmarshal(raw, &c) != nil {
				return nil, errMalformed // npm accepts only a range string
			}
			end := int(dec.InputOffset())
			start := end - len(raw)
			out = append(out, declared{Kind: "engines_node", Constraint: strings.TrimSpace(c), Start: lineAt(b, start), End: lineAt(b, end-1)})
		}
		if _, err := dec.Token(); err != nil {
			return nil, errMalformed
		}
	}
	return out, nil
}

// tomlValue decodes a single-line TOML value: a basic or literal string, or
// the raw text of any other value. Multi-line strings are malformed for the
// purpose of this conservative reader.
func tomlValue(v string) (string, error) {
	v = strings.TrimSpace(v)
	switch {
	case strings.HasPrefix(v, `"""`), strings.HasPrefix(v, `'''`):
		return "", errMalformed
	case strings.HasPrefix(v, `"`):
		end := closingQuote(v)
		if end < 0 {
			return "", errMalformed
		}
		s, err := strconv.Unquote(v[:end+1])
		if err != nil || !restIsComment(v[end+1:]) {
			return "", errMalformed
		}
		return s, nil
	case strings.HasPrefix(v, `'`):
		end := strings.IndexByte(v[1:], '\'')
		if end < 0 || !restIsComment(v[end+2:]) {
			return "", errMalformed
		}
		return v[1 : end+1], nil
	}
	if i := strings.Index(v, "#"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if v == "" {
		return "", errMalformed
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

// tomlKeys yields (table, key, value, line) for simple "key = value" lines.
// Headers are read permissively ([target.'cfg(unix)'.dependencies],
// [profile.dev.package."*"]); a line opening a header without any "]" is
// malformed. Rows of a multi-line array are skipped by bracket depth.
func tomlKeys(b []byte, visit func(table, key, value string, line int) error) error {
	table, depth := "", 0
	for i, raw := range lines(b) {
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
				return errMalformed
			}
			if !restIsComment(line[end+1:]) {
				continue // not a header line
			}
			inner := strings.TrimSuffix(strings.TrimPrefix(line[:end+1], "["), "]")
			inner = strings.TrimSuffix(strings.TrimPrefix(inner, "["), "]")
			table = strings.NewReplacer(`"`, "", "'", "", " ", "", "\t", "").Replace(inner)
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue // continuation of another multi-line value
		}
		if strings.HasPrefix(strings.TrimSpace(v), "[") {
			depth = strings.Count(v, "[") - strings.Count(v, "]")
		}
		key := strings.Trim(strings.TrimSpace(k), `"'`)
		if err := visit(table, key, v, i+1); err != nil {
			return err
		}
	}
	return nil
}

func tomlRequirement(b []byte, table, key, kind string) ([]declared, error) {
	var out []declared
	err := tomlKeys(b, func(t, k, v string, line int) error {
		if t != table || k != key {
			return nil
		}
		if len(out) > 0 {
			return errMalformed // duplicate key in one table
		}
		val, err := tomlValue(v)
		if err != nil {
			return err
		}
		out = append(out, declared{Kind: kind, Constraint: val, Start: line, End: line})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseCargo reads [package] rust-version. Workspace inheritance
// (rust-version.workspace = true) is recorded but not resolved.
func parseCargo(b []byte) ([]declared, error) {
	var out []declared
	err := tomlKeys(b, func(t, k, v string, line int) error {
		if t != "package" || (k != "rust-version" && k != "rust-version.workspace") {
			return nil
		}
		if len(out) > 0 {
			return errMalformed
		}
		val, err := tomlValue(v)
		if err != nil {
			return err
		}
		d := declared{Kind: "rust_version", Constraint: val, Start: line, End: line}
		if k == "rust-version.workspace" || strings.Contains(val, "workspace") {
			d.Constraint, d.Limitations = inheritedRustVersionValue, []string{LimWorkspaceInheritance}
		}
		out = append(out, d)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseRustToolchain reads the legacy one-line channel file or the TOML form.
func parseRustToolchain(b []byte, isTOML bool) ([]declared, error) {
	if !isTOML && !bytes.Contains(b, []byte("[toolchain]")) {
		for i, l := range lines(b) {
			if t := strings.TrimSpace(l); t != "" && !strings.HasPrefix(t, "#") {
				return []declared{{Kind: "rust_toolchain_channel", Constraint: t, Start: i + 1, End: i + 1}}, nil
			}
		}
		return nil, nil
	}
	return tomlRequirement(b, "toolchain", "channel", "rust_toolchain_channel")
}

// parseSetupCfg reads [options] python_requires (INI, single-line value).
func parseSetupCfg(b []byte) ([]declared, error) {
	section := ""
	var out []declared
	for i, raw := range lines(b) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, errMalformed
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		// INI accepts "=" or ":"; the first one separates key and value
		// (">=" inside the value must not split it).
		sep := strings.IndexAny(line, "=:")
		if sep < 0 || section != "options" {
			continue
		}
		k, v := line[:sep], line[sep+1:]
		if strings.TrimSpace(k) != "python_requires" {
			continue
		}
		if len(out) > 0 || strings.TrimSpace(v) == "" {
			return nil, errMalformed
		}
		out = append(out, declared{Kind: "python_requires", Constraint: strings.TrimSpace(v), Start: i + 1, End: i + 1})
	}
	return out, nil
}

// parsePythonVersion reads the first version of a .python-version file.
func parsePythonVersion(b []byte) ([]declared, error) {
	var out []declared
	for i, l := range lines(b) {
		if j := strings.Index(l, "#"); j >= 0 {
			l = l[:j]
		}
		for _, f := range strings.Fields(l) {
			if len(out) == 0 {
				out = append(out, declared{Kind: "python_version_file", Constraint: f, Start: i + 1, End: i + 1})
			} else if len(out[0].Limitations) == 0 {
				out[0].Limitations = []string{LimExtraPythonVersions}
			}
		}
	}
	return out, nil
}

var cmakeCall = regexp.MustCompile(`(?i)\bcmake_minimum_required\s*\(`)

// parseCMake reads the first cmake_minimum_required(VERSION ...) call. Line
// comments are ignored; the call may span lines.
func parseCMake(b []byte) ([]declared, error) {
	ls := lines(b)
	for i, raw := range ls {
		line := stripCMakeComment(raw)
		loc := cmakeCall.FindStringIndex(line)
		if loc == nil {
			continue
		}
		args := line[loc[1]:]
		end := i
		for !strings.Contains(args, ")") {
			end++
			if end >= len(ls) {
				return nil, errMalformed
			}
			args += " " + stripCMakeComment(ls[end])
		}
		args = args[:strings.Index(args, ")")]
		f := strings.Fields(args)
		for j := 0; j+1 < len(f); j++ {
			if strings.EqualFold(f[j], "VERSION") {
				return []declared{{Kind: "cmake_minimum_required", Constraint: f[j+1], Start: i + 1, End: end + 1}}, nil
			}
		}
		return nil, errMalformed
	}
	return nil, nil
}

func stripCMakeComment(l string) string {
	if j := strings.IndexByte(l, '#'); j >= 0 {
		return l[:j]
	}
	return l
}
