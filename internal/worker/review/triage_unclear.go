package review

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"slices"
	"strings"
)

// What an unclear triage answer says is missing (docs/REVIEW.md#도구-진단-분류).
//
// An answer of unclear is only right when something that decides the
// diagnostic is absent from the request. The answer names what: one entry of
// missing per absent thing, with a fixed kind and, for a definition, the
// identifier. The platform keeps the structure out of the review record (the
// reason prose stays the record of the answer) and checks two things.
//
//   - A malformed or absent missing list is a fault of form (triage_missing_invalid).
//     After the repair the answer stands without it and the limitation
//     triage_missing_unstated says the missing thing was not named.
//   - A definition named as missing that the request holds is a fault of form
//     (triage_unclear_input_present): the definition is a top-level
//     declaration of a Go file in the same package whose unit is in the same
//     request, and it is declared exactly once there. The repair points at the
//     excerpt that holds it and lets the group cite it. After the repair the
//     answer stands as the model's (recorded unclear with the limitation
//     triage_unclear_definition_in_request): the guard sees only the
//     declarations of the request and may be wrong (a method named like a
//     function), and a fault of form never fails a stage.
//
// The check is exact and only for Go: Go's own parser gives the top-level
// declarations and the package of each file. For the other languages a unit
// index of declaration names would be a guess (the unit boundaries of those
// languages are heuristics), so an unclear answer that names a definition in
// such a file is not checked.

const (
	errTriageMissingInvalid  = "triage_missing_invalid"
	errTriageUnclearPresent  = "triage_unclear_input_present"
	limTriageMissingUnstated = "triage_missing_unstated"
	// limTriageUnclearDefinitionPresent marks an unclear answer that stands
	// although the platform found the definition it names in the request.
	limTriageUnclearDefinitionPresent = "triage_unclear_definition_in_request"
)

// triageMissing is one thing an unclear answer says the request lacks.
type triageMissing struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// missingKinds are the kinds of what can be absent from a triage request.
var missingKinds = []string{"definition", "caller", "external_behavior", "dependency_or_type", "build_condition", "other"}

var declaredName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
var qualifiedName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,127}$`)

// checkMissing reads an unclear answer's missing list: the entries that
// stand and whether it has a fault of form (a field of the wrong type, a
// malformed entry, or no entry).
func (s *roleStage) checkMissing(raw json.RawMessage) (valid []triageMissing, fault bool) {
	if emptyJSON(raw) {
		return nil, true
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, true
	}
	for _, it := range items {
		var m triageMissing
		dec := json.NewDecoder(bytes.NewReader(it))
		dec.DisallowUnknownFields()
		// The name is an identifier, optionally qualified; only a definition
		// must have one.
		ok := dec.Decode(&m) == nil && slices.Contains(missingKinds, m.Kind) && (m.Name == "" && m.Kind != "definition" || qualifiedName.MatchString(m.Name))
		if !ok {
			fault = true
			continue
		}
		valid = append(valid, m)
	}
	return valid, fault || len(valid) == 0
}

// declaredAt is one top-level declaration of a Go file of the request.
type declaredAt struct {
	file       string
	line       int
	excerptIDs []string
}

// definitionIndex finds the top-level Go declarations of the files whose
// units a triage request carries, by package directory, package name and
// identifier.
type definitionIndex struct {
	// byName maps dir "\x00" package "\x00" name to the declarations found.
	byName map[string][]declaredAt
	// pkg is the directory and package name of each indexed file.
	pkg map[string]string
}

// newDefinitionIndex parses each Go file of the sent groups once (the
// worker holds the whole file) and records the top-level functions, types,
// variables and constants whose name line lies in a unit of the request.
func newDefinitionIndex(sent []triageSent) *definitionIndex {
	idx := &definitionIndex{byName: map[string][]declaredAt{}, pkg: map[string]string{}}
	parsed := map[string]*ast.File{}
	fset := token.NewFileSet()
	for _, g := range sent {
		p := g.group.unit.entry.Path
		if path.Ext(p) != ".go" {
			continue
		}
		f, ok := parsed[p]
		if !ok {
			f, _ = parser.ParseFile(fset, p, strings.Join(g.group.unit.text, "\n"), parser.SkipObjectResolution|parser.ParseComments)
			parsed[p] = f
		}
		if f == nil || f.Name == nil || buildConstrained(p, f) {
			continue
		}
		idx.pkg[p] = path.Dir(p) + "\x00" + f.Name.Name
	}
	for p, f := range parsed {
		if _, indexed := idx.pkg[p]; !indexed {
			continue
		}
		for _, name := range topLevelNames(fset, f) {
			var ids []string
			for _, g := range sent {
				u := g.group.unit
				// Several groups of one unit share its excerpt.
				// The whole declaration travels: a large one split over units is
				// not claimed as present.
				if u.entry.Path == p && u.span.start <= name.line && name.last <= u.span.end && !slices.Contains(ids, g.context[0]) {
					ids = append(ids, g.context[0])
				}
			}
			if len(ids) == 0 {
				continue
			}
			key := idx.pkg[p] + "\x00" + name.name
			idx.byName[key] = append(idx.byName[key], declaredAt{file: p, line: name.line, excerptIDs: ids})
		}
	}
	return idx
}

type namedLine struct {
	name       string
	line, last int // the name's line and the last line of the declaration
}

// topLevelNames are the package-level names a Go file declares, with the
// line of the name and the declaration's last line. Methods are not
// package-level names.
func topLevelNames(fset *token.FileSet, f *ast.File) []namedLine {
	var out []namedLine
	add := func(id *ast.Ident, end token.Pos) {
		if id != nil && id.Name != "_" && id.Name != "init" {
			out = append(out, namedLine{id.Name, fset.Position(id.Pos()).Line, fset.Position(end).Line})
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				add(d.Name, d.End())
			}
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					add(sp.Name, sp.End())
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						add(n, sp.End())
					}
				}
			}
		}
	}
	return out
}

// buildConstrained reports a Go file that is not built everywhere: a build
// constraint before the package clause, or a GOOS or GOARCH file-name suffix.
// Its declarations may not be the ones the package has, so it is not indexed.
func buildConstrained(p string, f *ast.File) bool {
	for _, g := range f.Comments {
		if g.Pos() >= f.Package {
			break
		}
		for _, c := range g.List {
			if strings.HasPrefix(c.Text, "//go:build") || strings.HasPrefix(c.Text, "// +build") {
				return true
			}
		}
	}
	stem := strings.TrimSuffix(strings.TrimSuffix(path.Base(p), ".go"), "_test")
	parts := strings.Split(stem, "_")
	for _, tail := range parts[max(1, len(parts)-2):] {
		if slices.Contains(knownGOOS, tail) || slices.Contains(knownGOARCH, tail) {
			return true
		}
	}
	return false
}

var knownGOOS = []string{"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos"}
var knownGOARCH = []string{"386", "amd64", "amd64p32", "arm", "arm64", "arm64be", "armbe", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc", "ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm"}

// holder returns the excerpt that holds the definition of name for a group
// on file p: the one unit of the request whose file is in the same package
// and declares it, or "" when none does or the name is not exact (declared
// more than once in the request, qualified, or the file is not Go).
func (x *definitionIndex) holder(p, name string) string {
	pkg, ok := x.pkg[p]
	if !ok || !declaredName.MatchString(name) {
		return ""
	}
	found := x.byName[pkg+"\x00"+name]
	if len(found) != 1 || len(found[0].excerptIDs) != 1 {
		return ""
	}
	return found[0].excerptIDs[0]
}

// presentDefinition is the excerpt of the request that holds a definition an
// unclear answer says is missing, or "".
func (x *definitionIndex) presentDefinition(g triageSent, missing []triageMissing) string {
	p := g.group.unit.entry.Path
	for _, m := range missing {
		if m.Kind != "definition" {
			continue
		}
		if id := x.holder(p, m.Name); id != "" {
			return id
		}
	}
	return ""
}

// contractUnclear is the part of the triage contract that tells what an
// unclear entry carries.
const contractUnclear = ` An unclear entry names in missing what the request lacks, one object per absent thing: its kind (definition, caller, external_behavior, dependency_or_type, build_condition or other) and, for a definition, the identifier; use unclear only for a thing that is absent from the request, and decide from every excerpt of the request, which may hold the definition a group uses.`
