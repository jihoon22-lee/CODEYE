package review

import (
	"path"
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func (s *roleStage) documentedPythonAPI(manifest, files map[string]source.Entry, page envdoc.Doc, target string, budget int) (record.Excerpt, bool, error) {
	f, object := pythonDocSource(manifest, page.AppliesTo, target)
	e, included := files[f.Path]
	if !included || !inScope(e.Path, normalizedScope(s.j.Scope)) || otherSource(e.Path) != "python" || record.IsTestPath(e.Path) {
		return record.Excerpt{}, false, nil
	}
	body, ok, err := s.readSource(e)
	if err != nil || !ok {
		return record.Excerpt{}, false, err
	}
	api, ok := s.pythonDocExcerpt(e, page.Path, body, object, budget)
	return api, ok && api.ValidDirectivePath() && api.DocumentScope() == page.AppliesTo, nil
}

const maxAPIDocExcerpts = 2

var mkdocstringsDirective = regexp.MustCompile(`^:::[ \t]+([A-Za-z][A-Za-z0-9_]*(?:\.[A-Za-z][A-Za-z0-9_]*){0,7})[ \t]*$`)
var apiPageHeading = regexp.MustCompile(`^#{1,6}[ \t]+[^ \t#]`)

// pythonDocTarget recognizes a Markdown API stub containing only headings
// and one literal directive. A fenced example, indented code or an options
// block leaves the original guidance excerpt as it was.
func pythonDocTarget(page, text string) string {
	if !record.MarkdownDirectivePath(page) {
		return ""
	}
	target := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimLeft(line, " ")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		if len(line)-len(trimmed) > 3 {
			return ""
		}
		if apiPageHeading.MatchString(trimmed) {
			continue
		}
		match := mkdocstringsDirective.FindStringSubmatch(trimmed)
		if match == nil || len(match[1]) > 200 || target != "" {
			return ""
		}
		target = match[1]
	}
	return target
}

// pythonDocSource searches the page's project/module root and its src
// directory. A longer literal module prefix takes precedence over an
// attribute of its package. Competing paths at the same prefix are ambiguous,
// including entries missing from the materialized snapshot.
func pythonDocSource(files map[string]source.Entry, root, target string) (source.Entry, []string) {
	parts := strings.Split(target, ".")
	for end := len(parts); end >= max(1, len(parts)-2); end-- {
		module := strings.Join(parts[:end], "/")
		var found source.Entry
		for _, dir := range []string{root, path.Join(root, "src")} {
			base := path.Join(dir, module)
			for _, p := range []string{base + ".py", path.Join(base, "__init__.py")} {
				if e, exists := files[p]; exists {
					if found.Path != "" {
						return source.Entry{}, nil
					}
					found = e
				}
			}
		}
		if found.Path != "" {
			return found, parts[end:]
		}
	}
	return source.Entry{}, nil
}

type pythonDocRange struct {
	declaration, start, end int // physical lines, 1-based
}

// pythonDocstring recognizes an initial standalone text literal. It keeps
// the bytes in the Python source, including any literal doctest prompts.
func pythonDocstring(tokens []callToken, first int) pythonDocRange {
	if first >= len(tokens) || tokens[first].depth != 0 {
		return pythonDocRange{}
	}
	t := tokens[first]
	start := t.line
	if t.name && (strings.EqualFold(t.text, "r") || strings.EqualFold(t.text, "u")) &&
		first+1 < len(tokens) && t.endLine == tokens[first+1].line && t.column+len(t.text) == tokens[first+1].column {
		first++
		t = tokens[first]
	}
	if !t.stringLiteral || (first+1 < len(tokens) && tokens[first+1].line == t.endLine) {
		return pythonDocRange{}
	}
	return pythonDocRange{declaration: start, start: start, end: t.endLine}
}

type pythonDocDeclaration struct {
	name       string
	line, body int
	indent     int
	class      bool
}

func pythonDocDeclarations(tokens []callToken) []pythonDocDeclaration {
	var declarations []pythonDocDeclaration
	for i, t := range tokens {
		if t.depth != 0 || !t.name || (t.text != "def" && t.text != "class") || !tokenName(tokens, i+1) {
			continue
		}
		indent := t.column
		if tokenIs(tokens, i-1, "async") && tokens[i-1].line == t.line {
			indent = tokens[i-1].column
		} else if i > 0 && tokens[i-1].endLine == t.line {
			continue
		}
		name := tokens[i+1].text
		cursor := i + 2
		for cursor < len(tokens) && tokens[cursor].column > indent {
			if tokenIs(tokens, cursor, ":") {
				body := cursor + 1
				if body < len(tokens) && tokens[body].line > tokens[cursor].line && tokens[body].column > indent {
					declarations = append(declarations, pythonDocDeclaration{name: name, line: t.line, body: body, indent: indent, class: t.text == "class"})
				}
				break
			}
			if next := tokens[cursor].close; next > cursor {
				cursor = next
			}
			cursor++
		}
	}
	return declarations
}

func pythonDocRanges(content string, object []string) []pythonDocRange {
	tokens := callTokens(content, true)
	if len(tokens) == 0 {
		return nil
	}
	decls := pythonDocDeclarations(tokens)
	objectDecl := func(parent *pythonDocDeclaration, name string) *pythonDocDeclaration {
		indent, first, last := 0, 0, len(tokens)
		if parent != nil {
			if !parent.class {
				return nil
			}
			first = parent.body
			indent = tokens[first].column
			for i := first; i < len(tokens); i++ {
				if tokens[i].depth == 0 && tokens[i].column <= parent.indent {
					last = i
					break
				}
			}
		}
		var found *pythonDocDeclaration
		for i := range decls {
			d := &decls[i]
			if d.indent == indent && d.body >= first && d.body < last && d.name == name {
				if found != nil {
					return nil
				}
				found = d
			}
		}
		return found
	}
	read := func(d *pythonDocDeclaration) pythonDocRange {
		r := pythonDocstring(tokens, d.body)
		r.declaration = d.line
		return r
	}
	if len(object) != 0 {
		var decl *pythonDocDeclaration
		for _, name := range object {
			decl = objectDecl(decl, name)
			if decl == nil {
				return nil
			}
		}
		return []pythonDocRange{read(decl)}
	}
	docs := []pythonDocRange{pythonDocstring(tokens, 0)}
	names := map[string]int{}
	for _, d := range decls {
		if d.indent == 0 {
			names[d.name]++
		}
	}
	for i := range decls {
		d := &decls[i]
		if d.indent == 0 && !strings.HasPrefix(d.name, "_") && names[d.name] == 1 {
			docs = append(docs, read(d))
		}
	}
	return docs
}

// pythonDocExcerpt gives observable examples and options precedence over a
// module synopsis. Its range ends inside the literal, before executable code.
func (s *roleStage) pythonDocExcerpt(e source.Entry, page, content string, obj []string, budget int) (record.Excerpt, bool) {
	lines := strings.Split(content, "\n")
	var best record.Excerpt
	bestScore := 0
	for _, r := range pythonDocRanges(content, obj) {
		if r.start == 0 {
			continue
		}
		for start := r.declaration; start <= r.end; start++ {
			end, used, score := start, 0, 0
			for end <= min(r.end, start+roleMaxDocLines-1) {
				if len(lines[end-1]) > budget {
					break
				}
				used += record.JSONLen(lines[end-1])
				if used > budget {
					break
				}
				if end >= r.start {
					line := strings.TrimSpace(lines[end-1])
					score += documentationLineWeight(line)
					if strings.HasPrefix(line, ">>> ") && end < r.end && end < start+roleMaxDocLines-1 &&
						used+record.JSONLen(lines[end]) <= budget && strings.TrimSpace(lines[end]) != "" {
						score += 4
					}
					if documentationOptions(line) {
						score += 3
					}
				}
				end++
			}
			if end <= r.start || end <= start {
				continue
			}
			if start <= r.start {
				score++ // keep the introduction on a tie
			}
			if score > bestScore {
				if x, ok := s.boundedExcerpt(e, "document", "", start, lines[start-1:end-1], budget); ok {
					best, bestScore = x, score
				}
			}
			if end > r.end {
				break
			}
		}
	}
	best.DirectivePath = page
	return best, best.ID != ""
}

func documentationOptions(line string) bool {
	switch strings.TrimSuffix(strings.ToLower(line), ":") {
	case "args", "arguments", "parameters", "keyword args", "keyword arguments", "options", "returns", "raises", "examples":
		return true
	}
	return false
}

func addPythonDocExampleTerms(terms map[string]int, content string) {
	previousPrompt := false
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if previousPrompt && len(line) >= 2 && len(line) <= 120 &&
			(line[0] == '\'' || line[0] == '"') && line[0] == line[len(line)-1] {
			addWitnessWords(terms, line[1:len(line)-1], 2)
		}
		previousPrompt = strings.HasPrefix(line, ">>> ")
	}
}

// documentedPythonWitness finds executable lines of the declaration which
// supplied the cited docstring. A declaration or its examples must not win
// over the implementation when the two have the same names and options.
func documentedPythonWitness(e source.Entry, content string, q witnessQuery) contractWitness {
	cited := q.pythonDocs[e.Path]
	if len(cited) == 0 {
		return contractWitness{}
	}
	tokens := callTokens(content, true)
	lines := strings.Split(content, "\n")
	var best contractWitness
	for _, d := range pythonDocDeclarations(tokens) {
		r := pythonDocstring(tokens, d.body)
		if r.start == 0 || !citesPythonDocRange(cited, r) {
			continue
		}
		first := d.body
		for first < len(tokens) && tokens[first].line <= r.end {
			first++
		}
		last := first
		for last < len(tokens) && (tokens[last].depth > 0 || tokens[last].column > d.indent) {
			last++
		}
		if last == first {
			continue
		}
		for start := tokens[first].line; start <= tokens[last-1].endLine; start++ {
			used, score, result := 0, 0, 0
			end := start
			for end <= min(tokens[last-1].endLine, start+witnessMaxLines-1) {
				raw := lines[end-1]
				if len(raw) > witnessMaxExcerpt {
					break
				}
				used += record.JSONLen(raw)
				if used > witnessMaxExcerpt {
					break
				}
				line := strings.TrimSpace(raw)
				if line != "" && !strings.HasPrefix(line, "#") {
					match := witnessMatch(witnessTerms(line), q.terms)
					score += min(witnessMinScore, match)
					if word, _, _ := strings.Cut(line, " "); match > 0 && (word == "if" || word == "elif") &&
						strings.HasSuffix(line, ":") && end < min(tokens[last-1].endLine, start+witnessMaxLines-1) &&
						used+record.JSONLen(lines[end]) <= witnessMaxExcerpt &&
						len(lines[end])-len(strings.TrimLeft(lines[end], " \t")) > len(raw)-len(strings.TrimLeft(raw, " \t")) {
						score += 2 * witnessMinScore
					}
					if word, _, _ := strings.Cut(line, " "); word == "return" || word == "yield" || word == "raise" {
						result++
						if end == tokens[last-1].line {
							result += 2
						}
					}
				}
				end++
			}
			if score > 0 && result > 0 && score+8*result > best.score {
				best = contractWitness{entry: e, line: start, end: end - 1, score: score + 8*result, direct: true}
			}
			if end > tokens[last-1].endLine {
				break
			}
		}
	}
	if best.line > 0 {
		best.score += 2 * witnessMinScore
	}
	return best
}

func citesPythonDocRange(cited []record.Excerpt, r pythonDocRange) bool {
	for _, e := range cited {
		if e.Start <= r.end && r.start <= e.End {
			return true
		}
	}
	return false
}

var pythonTestRow = regexp.MustCompile(`^\s*\(\[([0-9][0-9_*. +/\-]*)(?:, *(True|False))?\], *['"]([^'"\r\n]+)['"]\),?\s*$`)

// Rows for the same numeric input make an option's result easier to compare.
// These are static pytest parameters, with no claim of an executed test.
func documentedPythonTestRows(e source.Entry, content string, q witnessQuery) contractWitness {
	if path.Ext(e.Path) != ".py" || !record.IsTestPath(e.Path) || !record.ScopeUnder(e.Path, q.scope) {
		return contractWitness{}
	}
	stem := strings.TrimSuffix(strings.TrimPrefix(path.Base(e.Path), "test_"), ".py")
	stem = strings.TrimSuffix(stem, "_test")
	for p := range q.pythonDocs {
		if path.Base(p) == stem+".py" {
			return findPythonTestRows(e, content, q)
		}
	}
	return contractWitness{}
}

func findPythonTestRows(e source.Entry, content string, q witnessQuery) contractWitness {
	lines := strings.Split(content, "\n")
	type expectation struct {
		line   int
		output string
	}
	var defaults map[string]expectation
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "@pytest.mark.parametrize(") {
			defaults = map[string]expectation{}
		} else if strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "async def ") {
			defaults = nil
		}
		if defaults == nil || len(raw) > witnessMaxExcerpt {
			continue
		}
		row := pythonTestRow.FindStringSubmatch(raw)
		if row == nil {
			continue
		}
		input := strings.ReplaceAll(row[1], " ", "")
		if row[2] == "" || row[2] == "False" {
			defaults[input] = expectation{i, row[3]}
			continue
		}
		base, ok := defaults[input]
		if !ok || i-base.line >= witnessMaxLines || row[3] == base.output ||
			witnessMatch(witnessTerms(base.output+" "+row[3]), q.terms) == 0 || !fitsWitness(lines, base.line+1, i+1) {
			continue
		}
		end := i + 1
		for next := i + 1; next < min(len(lines), base.line+witnessMaxLines); next++ {
			line := strings.TrimSpace(lines[next])
			if strings.HasPrefix(line, "@") {
				break
			}
			if strings.HasPrefix(line, "assert ") && fitsWitness(lines, base.line+1, next+1) {
				end = next + 1
				break
			}
		}
		return contractWitness{entry: e, line: base.line + 1, end: end, score: 3 * witnessMinScore, direct: true}
	}
	return contractWitness{}
}
