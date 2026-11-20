package review

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	witnessMaxFiles    = 192
	witnessScanBytes   = 4 << 20
	witnessMaxExcerpts = 4
	witnessMaxLines    = 24
	witnessMaxExcerpt  = 1500
	witnessMinScore    = 8
)

var (
	witnessWord        = regexp.MustCompile(`[\pL\pN_][\pL\pN_-]*`)
	witnessCode        = regexp.MustCompile("`([^`\n]+)`")
	witnessCamel       = regexp.MustCompile(`[\p{Lu}]?[\p{Ll}]+|[\p{Lu}]+|[\pN]+`)
	witnessDeclaration = regexp.MustCompile(`(^|[[:space:]])(func|function|def|fn|class|case)[[:space:]]`)
	witnessControl     = regexp.MustCompile(`(^|[[:space:]])(if|else|elif|case|match|switch|raise|throw|return|assert)([[:space:](]|$)`)
	witnessSeparators  = strings.NewReplacer("_", "", "-", "")
	witnessPath        = regexp.MustCompile(`[\pL\pN_./-]+\.[A-Za-z0-9]+`)
)

type witnessQuery struct {
	scope     string
	terms     map[string]int
	statement map[string]int
	paths     map[string]bool
}

type contractWitness struct {
	entry       source.Entry
	line, score int
	direct      bool
}

// contractWitnesses looks for readable implementation and test locations for
// accepted document contracts. Its lexical matches are input ordering hints.
func (s *roleStage) contractWitnesses(g codeGroups, docs record.Record) ([]contractWitness, error) {
	queries, err := s.witnessQueries(docs)
	if err != nil || len(queries) == 0 {
		return nil, err
	}
	type candidate struct {
		entry      source.Entry
		pathScore  int
		inOverview bool
	}
	var named, otherCode, otherTests []candidate
	firstNamed := make([]candidate, len(queries))
	natural := 0
	for e := range g.orderedFiles() {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		natural++
		// Keep unchanged PR files and secondary artifacts in their usual
		// phases instead of promoting them over the primary source.
		if (g.tier != nil && g.tierOf(e.Path) != tierChanged) || g.secondary[e.Path] != "" || minifiedSource(e.Path) {
			continue
		}
		if s.secretFiles()[e.Path] {
			s.rec.Limitations = addLim(s.rec.Limitations, limSecretWithheld)
			continue
		}
		c := candidate{entry: e, inOverview: natural <= roleMaxCodeFiles}
		var words []string
		for i, q := range queries {
			if record.ScopeUnder(e.Path, q.scope) {
				if words == nil {
					words = witnessTerms(path.Base(e.Path))
				}
				score := witnessMatch(words, q.terms)
				if q.names(e.Path) {
					score += 16
				}
				c.pathScore = max(c.pathScore, score)
				if score >= witnessMinScore && score > firstNamed[i].pathScore {
					firstNamed[i] = c
					firstNamed[i].pathScore = score
				}
			}
		}
		if words == nil {
			continue
		}
		switch {
		case c.pathScore >= 4:
			named = append(named, c)
		case windowKind(e.Path) == "test" && len(otherTests) < witnessMaxFiles:
			otherTests = append(otherTests, c)
		case windowKind(e.Path) == "code" && len(otherCode) < witnessMaxFiles:
			otherCode = append(otherCode, c)
		}
	}
	sort.SliceStable(named, func(i, j int) bool { return named[i].pathScore > named[j].pathScore })
	// Reserve room for unnamed implementations and test files. A command
	// spelling often appears only in a generic entry point or test suite.
	namedFirst := min(len(named), witnessMaxFiles/3)
	candidates := make([]candidate, 0, witnessMaxFiles)
	candidates = append(candidates, firstNamed...)
	candidates = append(candidates, named[:namedFirst]...)
	// Interleave fallback code and tests so a large corpus cannot spend
	// the scan budget on either kind exclusively.
	for i := 0; i < max(len(otherCode), len(otherTests)); i++ {
		if i < len(otherCode) {
			candidates = append(candidates, otherCode[i])
		}
		if i < len(otherTests) {
			candidates = append(candidates, otherTests[i])
		}
	}
	candidates = append(candidates, named[namedFirst:]...)

	matches := make([][2][]contractWitness, len(queries))
	remaining := witnessScanBytes
	scanned := map[string]bool{}
	for _, c := range candidates {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if len(scanned) == witnessMaxFiles || remaining == 0 {
			break
		}
		e := c.entry
		if e.Size == nil || scanned[e.Path] || *e.Size > int64(remaining) {
			continue
		}
		scanned[e.Path] = true
		remaining -= int(*e.Size)
		content, ok, err := s.readSource(e)
		if err != nil {
			return nil, err
		}
		if !ok || record.GeneratedHeader([]byte(content)) {
			continue
		}
		found := matchWitnessLines(content, e, queries)
		wholeFile := len(content) <= roleMaxCodeFile && strings.Count(content, "\n") < 50 && record.JSONLen(content) <= roleMaxCodeFile
		kind := 0
		if windowKind(e.Path) == "test" {
			kind = 1
		}
		for i, w := range found {
			// An anchor already visible in the ordinary report's leading
			// lines does not justify lifting the file out of breadth order.
			if w.line > 0 && (!c.inOverview || (w.line > 6 && (w.direct || !wholeFile))) {
				matches[i][kind] = append(matches[i][kind], w)
			}
		}
	}

	hasTests := false
	for i := range matches {
		hasTests = hasTests || len(matches[i][1]) > 0
		for _, list := range matches[i] {
			sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
		}
	}
	var out []contractWitness
	seen := map[string]bool{}
	take := func(i, kind int) bool {
		if len(out) >= witnessMaxExcerpts {
			return false
		}
		for _, w := range matches[i][kind] {
			if !seen[w.entry.Path] {
				seen[w.entry.Path] = true
				out = append(out, w)
				return true
			}
		}
		return false
	}
	codeLimit := witnessMaxExcerpts
	if hasTests {
		codeLimit--
	}
	hasCode := make([]bool, len(matches))
	for i := 0; i < len(matches) && len(out) < codeLimit; i++ {
		hasCode[i] = take(i, 0)
	}
	// Pair tests with a contract's implementation if possible. Use the
	// remaining places for other contracts with tests.
	for _, paired := range []bool{true, false} {
		for i, code := range hasCode {
			if code == paired {
				take(i, 1)
			}
		}
	}
	return out, nil
}

func minifiedSource(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".min.js") || strings.HasSuffix(lower, ".min.mjs") || strings.HasSuffix(lower, ".min.cjs")
}

func (s *roleStage) witnessQueries(docs record.Record) ([]witnessQuery, error) {
	if len(docs.Contracts) == 0 {
		return nil, nil
	}
	catalog := catalogOf(docs.Evidence)
	files := s.entries()
	cited := map[string]string{}
	var out []witnessQuery
	for _, c := range docs.Contracts {
		statement := map[string]int{}
		addWitnessWords(statement, c.Statement, 4)
		paths := map[string]bool{}
		addWitnessPaths(paths, c.Statement)
		terms := map[string]int{}
		for word, weight := range statement {
			terms[word] = weight
		}
		for _, id := range c.EvidenceIDs {
			raw, cached := cited[id]
			if !cached {
				x := catalog[id]
				e, found := files[x.Path]
				if !found || x.Kind != "document" || x.SHA256 != e.SHA256 {
					continue
				}
				var err error
				raw, _, _, err = s.readLines(e, x.Start, x.End)
				if err != nil {
					return nil, err
				}
				cited[id] = raw
			}
			passage := witnessPassage(statement, raw)
			addWitnessWords(terms, passage, 2)
			addWitnessPaths(paths, passage)
		}
		if len(terms) != 0 || len(paths) != 0 {
			out = append(out, witnessQuery{scope: c.AppliesTo, terms: terms, statement: statement, paths: paths})
		}
	}
	common := map[string]int{}
	for _, q := range out {
		for word := range q.statement {
			common[word]++
		}
	}
	for _, q := range out {
		for word, count := range common {
			if count > 1 && q.terms[word] > 0 && q.terms[word] < witnessMinScore {
				q.terms[word] = max(1, q.terms[word]/count)
			}
		}
	}
	return out, nil
}

// The same document excerpt can back several contracts. Use the passage
// overlapping this contract's own terms so that product-wide names and an
// unrelated feature row cannot dominate every search.
func witnessPassage(statement map[string]int, text string) string {
	lines := strings.Split(text, "\n")
	bestLine, bestScore := 0, 0
	for i, line := range lines {
		if score := witnessMatch(witnessTerms(line), statement); score > bestScore {
			bestLine, bestScore = i, score
		}
	}
	if bestScore > 0 {
		start, end := bestLine, bestLine+1
		// Take wrapped text alongside its selected line. A preceding or
		// following table row or list item can be a distinct feature.
		boundary := func(s string) bool {
			s = strings.TrimSpace(s)
			return s == "" || strings.HasPrefix(s, "|") || strings.HasPrefix(s, "#") ||
				strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ")
		}
		if start > 0 && !boundary(lines[start]) {
			start--
		}
		if end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[bestLine]), "|") && !boundary(lines[end]) {
			end++
		}
		return strings.Join(lines[start:end], "\n")
	} else if len(lines) <= 6 {
		// Short excerpts often use a translated contract. Keep only
		// explicitly marked names in that case.
		return strings.Join(witnessCode.FindAllString(text, -1), " ")
	}
	return ""
}

func addWitnessPaths(paths map[string]bool, text string) {
	for _, ref := range witnessPath.FindAllString(text, -1) {
		if p := path.Clean(ref); windowKind(p) != "" && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../") {
			paths[p] = true
		}
	}
}

func (q witnessQuery) names(p string) bool {
	return len(q.paths) != 0 && (q.paths[p] || q.paths[path.Base(p)] || q.paths[strings.TrimPrefix(p, q.scope+"/")])
}

func addWitnessWords(terms map[string]int, text string, weight int) {
	for _, quoted := range witnessCode.FindAllStringSubmatch(text, -1) {
		for _, word := range witnessWord.FindAllString(quoted[1], -1) {
			addWitnessWord(terms, word, witnessMinScore)
		}
	}
	for _, word := range witnessWord.FindAllString(text, -1) {
		score := weight
		if strings.ContainsAny(word, "_-") || (len(witnessCamel.FindAllString(word, 2)) > 1 && word != strings.ToLower(word)) {
			score += 5
		}
		addWitnessWord(terms, word, score)
	}
}

func addWitnessWord(terms map[string]int, word string, score int) {
	for i, term := range witnessTerms(word) {
		if i > 0 {
			score = min(score, 3)
		}
		if len(term) < 2 || len(term) > 80 || (len(term) == 2 && (term[0] != 'v' || term[1] < '0' || term[1] > '9')) {
			continue
		}
		switch term {
		case "the", "and", "that", "this", "with", "from", "for", "when", "else", "then", "must", "should", "returns", "return", "function", "input", "output", "code", "test", "tests", "file", "files", "example", "true", "false":
			continue
		}
		terms[term] = max(terms[term], score)
	}
}

func witnessTerms(text string) []string {
	var out []string
	for _, word := range witnessWord.FindAllString(text, -1) {
		lower := strings.ToLower(word)
		out = append(out, witnessSeparators.Replace(lower))
		if word != lower || strings.ContainsAny(word, "_-") {
			for _, part := range witnessCamel.FindAllString(word, -1) {
				p := strings.ToLower(part)
				if p != lower {
					out = append(out, p)
				}
			}
		}
		// Match Korean nouns used as bare identifiers or message text to
		// the same nouns with particles attached in prose.
		if first, _ := utf8.DecodeRuneInString(word); unicode.Is(unicode.Hangul, first) {
			for _, suffix := range []string{"에서", "에는", "으로", "에게", "까지", "을", "를", "은", "는", "의", "에", "와", "과", "로"} {
				if stem, found := strings.CutSuffix(word, suffix); found && utf8.RuneCountInString(stem) >= 2 {
					out = append(out, stem)
					break
				}
			}
		}
	}
	return out
}

func witnessMatch(words []string, terms map[string]int) int {
	score := 0
	for i, word := range words {
		weight := terms[word]
		if weight == 0 {
			continue
		}
		// Repeated occurrences on a line do not strengthen the match.
		duplicate := false
		for _, prior := range words[:i] {
			if word == prior {
				duplicate = true
				break
			}
		}
		if !duplicate {
			score += weight
		}
	}
	return score
}

func matchWitnessLines(text string, e source.Entry, queries []witnessQuery) []contractWitness {
	best := make([]contractWitness, len(queries))
	pathWords := witnessTerms(path.Base(e.Path))
	pathScores := make([]int, len(queries))
	namedPaths := make([]bool, len(queries))
	for i, q := range queries {
		if record.ScopeUnder(e.Path, q.scope) {
			pathScores[i] = min(8, witnessMatch(pathWords, q.terms))
			namedPaths[i] = q.names(e.Path)
		} else {
			pathScores[i] = -1
		}
	}
	lines := strings.Split(text, "\n")
	var multiline string
	for line, raw := range lines {
		s := strings.TrimSpace(raw)
		if multiline != "" {
			if strings.Contains(s, multiline) {
				multiline = ""
			}
			continue
		}
		if strings.HasPrefix(s, "/*") && !strings.Contains(s, "*/") {
			multiline = "*/"
			continue
		}
		for _, quote := range []string{`"""`, "'''"} {
			if strings.Count(s, quote)%2 != 0 {
				multiline = quote
				break
			}
		}
		if multiline != "" {
			continue
		}
		if s == "" || len(raw) > witnessMaxExcerpt || strings.HasPrefix(s, "import ") ||
			strings.HasPrefix(s, "package ") || strings.HasPrefix(s, "from ") ||
			strings.HasPrefix(s, "use ") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/*") ||
			strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "#") {
			continue
		}
		words := witnessTerms(s)
		structure := 0
		if witnessDeclaration.MatchString(s) || witnessControl.MatchString(s) {
			structure = 4
		}
		for i, q := range queries {
			if pathScores[i] < 0 {
				continue
			}
			score := witnessMatch(words, q.terms)
			named := namedPaths[i]
			if score == 0 && (!named || structure == 0) {
				continue
			}
			score += structure + pathScores[i]
			if named {
				score += witnessMinScore
			}
			anchor := line
			// Keep a neighboring guard with the matching error or branch
			// action. The short report excerpt uses this same anchor.
			if line > 0 && witnessControl.MatchString(strings.TrimSpace(lines[line-1])) {
				anchor--
				score += 3
			}
			if score < witnessMinScore || score <= best[i].score || record.JSONLen(raw) > witnessMaxExcerpt {
				continue
			}
			if anchor != line && record.JSONLen(lines[anchor])+record.JSONLen(raw) > witnessMaxExcerpt {
				anchor = line
			}
			best[i] = contractWitness{entry: e, line: anchor + 1, score: score, direct: named || witnessMatch(words, q.statement) >= witnessMinScore}
		}
	}
	return best
}
