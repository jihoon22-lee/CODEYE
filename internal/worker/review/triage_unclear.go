package review

import (
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
//     answer is left out like any other fault of form that the repair did not
//     resolve (triage_item_omitted): the group is not triaged.
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
// stand and whether it has a fault of form (malformed entry or no entry).
func (s *roleStage) checkMissing(list []triageMissing) (valid []triageMissing, fault bool) {
	for _, m := range list {
		// The name is an identifier, optionally qualified; only a definition
		// must have one.
		ok := slices.Contains(missingKinds, m.Kind) && (m.Name == "" && m.Kind != "definition" || qualifiedName.MatchString(m.Name))
		if !ok {
			fault = true
			continue
		}
		valid = append(valid, m)
	}
	return valid, fault || len(list) == 0
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
			f, _ = parser.ParseFile(fset, p, strings.Join(g.group.unit.text, "\n"), parser.SkipObjectResolution)
			parsed[p] = f
		}
		if f == nil || f.Name == nil {
			continue
		}
		idx.pkg[p] = path.Dir(p) + "\x00" + f.Name.Name
	}
	for p, f := range parsed {
		if f == nil || f.Name == nil {
			continue
		}
		for _, name := range topLevelNames(fset, f) {
			var ids []string
			for _, g := range sent {
				u := g.group.unit
				// Several groups of one unit share its excerpt.
				if u.entry.Path == p && u.span.start <= name.line && name.line <= u.span.end && !slices.Contains(ids, g.context[0]) {
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
	name string
	line int
}

// topLevelNames are the package-level names a Go file declares, with the
// line of the name. Methods are not package-level names.
func topLevelNames(fset *token.FileSet, f *ast.File) []namedLine {
	var out []namedLine
	add := func(id *ast.Ident) {
		if id != nil && id.Name != "_" && id.Name != "init" {
			out = append(out, namedLine{id.Name, fset.Position(id.Pos()).Line})
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				add(d.Name)
			}
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				switch sp := sp.(type) {
				case *ast.TypeSpec:
					add(sp.Name)
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						add(n)
					}
				}
			}
		}
	}
	return out
}

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
