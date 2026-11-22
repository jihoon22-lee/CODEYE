package envdoc

import (
	"regexp"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const (
	MaxDocumentationGaps = 12
	MaxGapDocuments      = 24
	DocGapOption         = "cli_option"
	DocGapEnvironment    = "cli_environment_variable"
)

// DocumentationGap records a declaration whose exact public spelling was not
// found in the listed fully read guidance files. The claim ends at that list.
type DocumentationGap struct {
	Kind     string   `json:"kind"`
	Symbol   string   `json:"symbol"`
	Default  string   `json:"default,omitempty"`
	Scope    string   `json:"scope"`
	Source   Location `json:"source"`
	Searched []string `json:"searched"`
}

var optionName = regexp.MustCompile(`^--[a-zA-Z][a-zA-Z0-9-]{1,63}$`)
var environmentName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)
var simpleDefault = regexp.MustCompile(`^[a-zA-Z0-9_./-]{1,80}$`)

func ValidDocumentationSymbol(kind, symbol string) bool {
	return kind == DocGapOption && optionName.MatchString(symbol) || kind == DocGapEnvironment && environmentName.MatchString(symbol)
}

func ValidDocumentationDefault(value string) bool {
	return value == "" || simpleDefault.MatchString(value) && !record.Sensitive([]byte(value))
}

// Read the user guidance inventory. Agent directives and contribution
// instructions are not user help for a public command-line interface.
func UserGuide(doc Doc, scope string) bool {
	return (doc.Class == ClassReadme || doc.Class == ClassDesign || doc.Class == ClassOther) &&
		(Under(doc.AppliesTo, scope) || Under(scope, doc.AppliesTo))
}

func DocumentationScope(source string, modules []Module) string {
	root := "."
	ecosystem := record.SourceEcosystem(source)
	for _, module := range modules {
		if module.Ecosystem == ecosystem && Under(source, module.Path) && len(module.Path) > len(root) {
			root = module.Path
		}
	}
	return root
}

func checkDocumentationGaps(gaps []DocumentationGap, docs map[string]Doc, modules []Module, want Expect) error {
	if len(gaps) > MaxDocumentationGaps {
		return invalid("documentation gaps")
	}
	seen := map[[3]string]bool{}
	for _, g := range gaps {
		s := g.Source
		sha, exists := want.Lookup(s.Path)
		key := [3]string{g.Scope, g.Kind, g.Symbol}
		if seen[key] || !ValidDocumentationSymbol(g.Kind, g.Symbol) || !ValidDocumentationDefault(g.Default) ||
			!ValidPath(g.Scope) || domain.ValidateEvidencePath(s.Path) != nil || g.Scope != DocumentationScope(s.Path, modules) ||
			!exists || sha != s.SHA256 || s.StartLine < 1 || s.EndLine < s.StartLine || s.EndLine-s.StartLine >= 25 ||
			len(g.Searched) == 0 || len(g.Searched) > MaxGapDocuments || !slices.IsSorted(g.Searched) {
			return invalid("documentation gap %q", g.Symbol)
		}
		seen[key] = true
		var required []string
		for p, doc := range docs {
			if !UserGuide(doc, g.Scope) {
				continue
			}
			if doc.Status != DocSelected || doc.LineCount == nil {
				return invalid("unread guidance for documentation gap %q", g.Symbol)
			}
			required = append(required, p)
		}
		slices.Sort(required)
		if !slices.Equal(g.Searched, required) {
			return invalid("incomplete search for documentation gap %q", g.Symbol)
		}
	}
	return nil
}
