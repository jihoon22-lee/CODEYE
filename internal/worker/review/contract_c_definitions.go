package review

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const maxCDocumentDefinitions = 2

type documentedCDefinitions []map[string]contractWitness

func (m documentedCDefinitions) observe(e source.Entry, text string, queries []witnessQuery) {
	if path.Ext(e.Path) != ".c" || windowKind(e.Path) != "code" {
		return
	}
	wanted := map[string]bool{}
	for _, q := range queries {
		if record.ScopeUnder(e.Path, q.scope) {
			for _, name := range q.navigationNames {
				wanted[name] = true
			}
		}
	}
	if len(wanted) == 0 {
		return
	}
	lines := strings.Split(text, "\n")
	for _, d := range cDefinitions(text, wanted) {
		for i, q := range queries {
			if !record.ScopeUnder(e.Path, q.scope) || !slices.Contains(q.navigationNames, d.name) {
				continue
			}
			if m[i] == nil {
				m[i] = map[string]contractWitness{}
			}
			if _, exists := m[i][d.name]; exists || !fitsWitness(lines, d.start, d.end) {
				m[i][d.name] = contractWitness{}
				continue
			}
			m[i][d.name] = contractWitness{entry: e, line: d.start, end: d.end, query: i + 1}
		}
	}
}

func (m documentedCDefinitions) choose(queries []witnessQuery) []contractWitness {
	var out []contractWitness
	for i, q := range queries {
		for _, name := range q.navigationNames {
			w := m[i][name]
			if w.line == 0 || slices.ContainsFunc(out, func(other contractWitness) bool {
				return other.entry.Path == w.entry.Path && other.line <= w.end && w.line <= other.end
			}) {
				continue
			}
			out = append(out, w)
			if len(out) == maxCDocumentDefinitions {
				return out
			}
		}
	}
	return out
}

// A C API may spell the return type through a visibility macro.
var cDefinitionSignature = regexp.MustCompile(`^[ \t]*(?:[A-Za-z_][A-Za-z0-9_]*(?:\([A-Za-z0-9_ \t*]+\))?[ \t*]+)+([A-Za-z_][A-Za-z0-9_]*)[ \t]*\(`)

type cDefinition struct {
	name, returnType, parameters, body string
	start, end, bodyLine               int
	conditional                        bool
}

// cDefinitions reads conventional externally visible definitions in a .c
// translation unit. Header prototypes and calls cannot open a function body.
// This lexical result only places source windows; it does not resolve builds.
func cDefinitions(text string, wanted map[string]bool) []cDefinition {
	return namedCDefinitions(text, wanted, false)
}

func namedCDefinitions(text string, wanted map[string]bool, includeStatic bool) []cDefinition {
	masked := []byte(contractExecutableSource(text, "c"))
	lines := strings.Split(string(masked), "\n")
	offset, depth := 0, 0
	conditions := make([]bool, len(lines))
	conditional := 0
	inDirective := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		directive := inDirective || strings.HasPrefix(trimmed, "#")
		if !inDirective && strings.HasPrefix(trimmed, "#") {
			command := strings.TrimLeft(trimmed[1:], " \t")
			if end := strings.IndexFunc(command, func(r rune) bool { return r < 'a' || r > 'z' }); end >= 0 {
				command = command[:end]
			}
			switch command {
			case "if", "ifdef", "ifndef":
				conditional++
			case "endif":
				conditional = max(0, conditional-1)
			}
		}
		conditions[i] = conditional > 0 || directive
		inDirective = directive && strings.HasSuffix(strings.TrimSpace(line), "\\")
		if directive {
			maskContractRange(masked[offset : offset+len(line)])
		}
		offset += len(line) + 1
	}

	var out []cDefinition
	offset = 0
	for i, line := range strings.Split(string(masked), "\n") {
		if depth == 0 {
			m := cDefinitionSignature.FindStringSubmatchIndex(line)
			if m != nil && wanted[line[m[2]:m[3]]] && (externalCReturnType(line[:m[2]]) ||
				includeStatic && cStatic(line[:m[2]])) {
				// The matched signature ends at the opening parenthesis.
				params := matchingCDelimiter(masked, offset+m[1]-1, '(', ')')
				if params != -1 && strings.Count(string(masked[offset:params]), "\n") < 5 {
					body := params
					for body < len(masked) && (masked[body] == ' ' || masked[body] == '\t' || masked[body] == '\r' || masked[body] == '\n') {
						body++
					}
					if body < len(masked) && masked[body] == '{' {
						end := matchingCDelimiter(masked, body, '{', '}')
						if end != -1 {
							last := i + strings.Count(string(masked[offset:end]), "\n")
							out = append(out, cDefinition{
								name: line[m[2]:m[3]], returnType: line[:m[2]], parameters: string(masked[offset+m[1] : params-1]),
								body: string(masked[body:end]), start: i + 1, end: last + 1,
								bodyLine:    i + 1 + strings.Count(string(masked[offset:body]), "\n"),
								conditional: slices.Contains(conditions[i:last+1], true),
							})
						}
					}
				}
			}
		}
		for _, c := range line {
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
		}
		offset += len(line) + 1
	}
	return out
}

func cStatic(prefix string) bool { return slices.Contains(strings.Fields(prefix), "static") }

func externalCReturnType(prefix string) bool {
	for _, word := range strings.Fields(prefix) {
		switch word {
		case "static", "typedef", "return":
			return false
		}
	}
	return true
}

// matchingCDelimiter returns the offset immediately after the closing byte.
func matchingCDelimiter(text []byte, start int, open, close byte) int {
	depth := 0
	for i, c := range text[start:] {
		switch c {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return start + i + 1
			}
		}
	}
	return -1
}
