package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const maxContractEntryPoints = 2

// contractNamedExcerpts finds uniquely defined routines whose names occur in
// the accepted contract statement and the cited document text. These exact
// names can supplement a probe that found a decision branch but missed the
// documented public entry point.
func (s *roleStage) contractNamedExcerpts(contract record.Contract, scope string, docs []record.Excerpt) ([]record.Excerpt, error) {
	var names []string
	for _, name := range documentedMembers(contract.Statement, true) {
		if record.ValidContractLiteral(name) && slices.ContainsFunc(docs, func(e record.Excerpt) bool { return containsContractLiteral(e.Text, name) }) {
			names = append(names, name)
			if len(names) == maxContractEntryPoints {
				break
			}
		}
	}
	if len(names) == 0 {
		return nil, nil
	}
	definitions := map[string]record.Excerpt{}
	ambiguous := map[string]bool{}
	groups := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	for _, e := range s.navigationFiles(groups, []string{scope}) {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if record.SourceKind(e.Path) != "code" || typeDeclarationPath(e.Path) || strings.HasSuffix(strings.ToLower(e.Path), ".pyi") {
			continue
		}
		text, ok, err := s.readSource(e)
		if err != nil {
			return nil, err
		}
		if !ok || record.GeneratedHeader([]byte(text)) ||
			!slices.ContainsFunc(names, func(name string) bool { return containsContractLiteral(text, name) }) {
			continue
		}
		lines := strings.Split(text, "\n")
		python := record.SourceEcosystem(e.Path) == "python"
		navigationSourceLines(lines, func(line int, raw string) bool {
			name, body := documentedDefinition(lines, line, raw, python)
			if !slices.Contains(names, name) || ambiguous[name] {
				return false
			}
			if _, found := definitions[name]; found {
				ambiguous[name] = true
				return false
			}
			location, ok := s.navigationLocation(e, lines, line, name)
			if !ok || location.End < body {
				// A definition is still ambiguous if a second definition is
				// found, even when this signature exceeds the input limit.
				definitions[name] = record.Excerpt{}
				return false
			}
			excerpt, ok := s.boundedExcerpt(e, "code", "context", location.Start, lines[location.Start-1:location.End], roleMaxWindowBytes)
			if ok {
				definitions[name] = excerpt
			}
			return false
		})
	}
	var out []record.Excerpt
	for _, name := range names {
		if e := definitions[name]; !ambiguous[name] && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

// documentedDefinition accepts a conventional routine signature with a
// visible body and ignores prototypes and calls. It is used for navigation
// only, without resolving overloads or proving that a caller reaches it.
func documentedDefinition(lines []string, line int, raw string, python bool) (string, int) {
	signature := strings.TrimSpace(raw)
	if strings.HasSuffix(signature, ";") || !strings.Contains(signature, "(") {
		return "", 0
	}
	for end := line; end <= min(line+3, len(lines)); end++ {
		if end != line {
			signature += " " + strings.TrimSpace(lines[end-1])
		}
		name := navigationSymbol(signature, false)
		switch {
		case python && name != "" && strings.HasSuffix(signature, ":"):
			return name, end + 1
		case !python && name != "" && strings.Contains(signature, "{"):
			if prefix, body, _ := strings.Cut(signature, "{"); !strings.Contains(prefix, ";") {
				if strings.TrimSpace(body) != "" {
					return name, end
				}
				return name, end + 1
			}
		}
		if strings.Contains(signature, ";") || strings.Contains(signature, "}") {
			break
		}
	}
	return "", 0
}
