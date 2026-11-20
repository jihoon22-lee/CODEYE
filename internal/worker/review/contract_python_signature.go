package review

import (
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// pythonFreeCalls recognizes the same bare, complete call spelling in a
// contract and its cited wording. It does not resolve dotted names or aliases.
func pythonFreeCalls(text string) []string {
	var names []string
	for _, match := range documentedCall.FindAllStringSubmatchIndex(text, 12) {
		start, open := match[2], match[1]-1
		name := text[start:match[3]]
		if strings.HasPrefix(name, "_") || strings.Contains(name, "$") || slices.Contains(names, name) {
			continue
		}
		if r, _ := utf8.DecodeLastRuneInString(text[:start]); r == '.' || r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			continue
		}
		before := strings.TrimRight(text[:start], " \t")
		word := before[strings.LastIndexAny(before, " \t\r\n`(")+1:]
		if word == "def" || word == "class" || strings.HasSuffix(before, ".") {
			continue
		}
		for i := open + 1; i < min(len(text), start+witnessMaxExcerpt); i++ {
			if text[i] != ')' {
				continue
			}
			tokens := callTokens(text[start:i+1], true)
			if len(tokens) >= 3 && tokenIs(tokens, 0, name) && tokens[1].close == len(tokens)-1 {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

// pythonPublicSignature returns the exact header of the unique public module
// function containing a selected runtime window. A nested suite, decorator,
// or other use that can rebind the name defeats the relationship.
func pythonPublicSignature(text string, names []string, runtime record.Excerpt) (start, end int) {
	tokens := callTokens(text, true)
	if len(tokens) == 0 || pythonDynamicBindings(tokens) {
		return 0, 0
	}
	decls := pythonDocDeclarations(tokens)
	for _, d := range decls {
		if d.class || d.indent != 0 || !slices.Contains(names, d.name) || tokens[d.body].line > runtime.Start || runtime.End > pythonSuiteEnd(tokens, d) {
			continue
		}
		if !slices.ContainsFunc(tokens[d.body:], func(t callToken) bool {
			return t.name && t.depth == 0 && t.line >= runtime.Start && t.line <= runtime.End
		}) {
			return 0, 0
		}
		if !pythonUniqueFunction(tokens, d) {
			return 0, 0
		}
		for _, nested := range decls {
			if nested.indent > d.indent && nested.line > d.line && nested.line <= runtime.End && pythonSuiteEnd(tokens, nested) >= runtime.Start {
				return 0, 0
			}
		}
		return d.line, tokens[d.body-1].line
	}
	return 0, 0
}

func pythonSuiteEnd(tokens []callToken, d pythonDocDeclaration) int {
	for _, t := range tokens[d.body:] {
		if t.depth == 0 && t.column <= d.indent {
			return t.line - 1
		}
	}
	return tokens[len(tokens)-1].endLine
}

func pythonUniqueFunction(tokens []callToken, d pythonDocDeclaration) bool {
	// The source side of a verified from-import alias introduces only the
	// alias as a local name. An unaliased import still makes this ambiguous.
	aliased := map[int]bool{}
	for name, bindings := range importedBindings(tokens, true) {
		if name == d.name {
			continue
		}
		for _, b := range bindings {
			if b.symbol == d.name && tokenIs(tokens, b.token-1, "as") && tokenIs(tokens, b.token-2, d.name) {
				aliased[b.token-2] = true
			}
		}
	}
	decl := -1
	for i, t := range tokens {
		if !t.name {
			continue
		}
		if (t.text == "setattr" || t.text == "delattr") && tokenIs(tokens, i+1, "(") {
			return false
		}
		if t.text != d.name || aliased[i] {
			continue
		}
		if decl < 0 && t.line == d.line && tokenIs(tokens, i-1, "def") && tokenIs(tokens, i+1, "(") {
			decl = i - 1
			if tokenIs(tokens, decl-1, "async") && tokens[decl-1].line == d.line {
				decl--
			}
		} else if !isDirectCall(tokens, i) {
			return false
		}
	}
	if decl < 0 || tokens[decl].column != 0 {
		return false
	}
	for i := decl - 1; i >= 0; i-- {
		if tokens[i].depth == 0 && tokens[i].column == 0 {
			return !tokenIs(tokens, i, "@")
		}
	}
	return true
}

// pythonSignatureWitness uses one slot after the existing contract witnesses.
// The retained runtime window keeps its identity and can still support calls.
func (s *roleStage) pythonSignatureWitness(out []record.Excerpt, selection contractSelection, remain int) (record.Excerpt, bool) {
	if len(out) >= roleMaxCodeFiles || remain <= 2 {
		return record.Excerpt{}, false
	}
	for _, runtime := range out {
		content := selection.sources[runtime.Path]
		if content == "" || path.Ext(runtime.Path) != ".py" || !contractRuntimePath(runtime.Path, runtime.Kind) {
			continue
		}
		var names []string
		for _, q := range selection.queries {
			if record.ScopeUnder(runtime.Path, q.scope) {
				names = append(names, q.pythonCalls...)
			}
		}
		if len(names) == 0 {
			continue
		}
		start, end := pythonPublicSignature(content, names, runtime)
		if start == 0 {
			continue
		}
		lines := strings.Split(content, "\n")
		if !fitsWitness(lines, start, end) {
			continue
		}
		leftLines, leftBytes := roleMaxCodeLines, roleMaxCodeFile
		for _, x := range out {
			if x.Path == runtime.Path {
				if x.Start <= end && start <= x.End {
					leftLines = 0
					break
				}
				leftLines -= x.End - x.Start + 1
				leftBytes -= record.JSONLen(x.Text)
			}
		}
		if end-start+1 > leftLines {
			continue
		}
		text := strings.Join(lines[start-1:end], "\n")
		if record.JSONLen(text) > min(leftBytes, remain) {
			continue
		}
		return record.Excerpt{
			ID: record.ExcerptID(s.j.Snapshot.Digest, runtime.Path, runtime.SHA256, start, end), Path: runtime.Path,
			SHA256: runtime.SHA256, Start: start, End: end, Kind: runtime.Kind, Origin: runtime.Origin, Text: text,
		}, true
	}
	return record.Excerpt{}, false
}
