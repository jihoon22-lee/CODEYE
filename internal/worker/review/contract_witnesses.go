package review

import (
	"path"
	"regexp"
	"slices"
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
	compileCondition   = regexp.MustCompile(`^#[ \t]*(?:if|ifdef|ifndef|elif)[ \t(]`)
	compileBranchEnd   = regexp.MustCompile(`^#[ \t]*(?:else|elif|endif)(?:[ \t(/]|$)`)
	cExecutableLine    = regexp.MustCompile(`^(?:(?:if|for|while|switch)[ \t]*\(|(?:return|goto|break|continue)(?:[ \t(;]|$)|[A-Za-z_]\w*[ \t]*[+*/%&|^-]?=[^=]|[A-Za-z_]\w*[ \t]*\([^;{}]*\)[ \t]*;)`)
	cRawString         = regexp.MustCompile(`R"([^ \t\r\x5c()]{0,16})\(`)
)

type witnessQuery struct {
	contractIndex        int
	scope                string
	terms                map[string]int
	statement            map[string]int
	paths                map[string]bool
	product              bool
	operation            bool
	hasRuntimeJavaScript bool
	typeContract         bool
	performance          bool
	members              []string
	navigationNames      []string
	jsAPINames           []string
	jsOptionNames        []string
	javaDocs             map[string][]record.Excerpt
	pythonDocs           map[string][]record.Excerpt
	rustDocs             map[string][]record.Excerpt
	pythonCalls          []string
	formula              pythonFormulaQuery
	cFamilies            []cAPIFamily
	packageEntry         string
	goExamples           []goContractExample
	goCommands           []goContractCommand
	hasGoCommandRoute    bool
	goDefaults           []string
	goJSONFields         []string
}

type contractWitness struct {
	entry            source.Entry
	line, end, score int
	direct           bool
	documented       bool
	compileBranch    bool
	query            int
}

type contractCall struct {
	caller, callee       contractWitness
	callerEnd, calleeEnd int
	symbol               string
}

type contractSelection struct {
	witnesses, additional []contractWitness
	rustAPIs              []rustContractWitness
	calls, javaCalls      []contractCall
	member                memberPair
	sources               map[string]string
	goTraces              []goWrapperTrace
	goOption              goOptionTrace
	goJSONFields          []goJSONTrace
	claimWitnesses        [][]contractWitness
	jsOptions             []jsOptionTrace
	cDefinitions          []contractWitness
	cFamily               []contractWitness
	queries               []witnessQuery
}

// contractWitnesses looks for readable implementation and test locations for
// accepted document contracts, including bounded direct calls from lexical
// matches. Both kinds of matches are input ordering hints.
func (s *roleStage) contractWitnesses(g codeGroups, docs record.Record) (contractSelection, error) {
	queries, err := s.witnessQueries(docs)
	if err != nil || len(queries) == 0 {
		return contractSelection{}, err
	}
	type candidate struct {
		entry      source.Entry
		pathScore  int
		inOverview bool
		penalties  []int
	}
	maintenance, err := s.maintenanceWitnesses(g, queries)
	if err != nil {
		return contractSelection{}, err
	}
	var named, formulaCode, otherCode, otherTests []candidate
	firstNamed := make([]candidate, len(queries))
	eligible := map[string]candidate{}
	natural := 0
	for e := range g.orderedFiles() {
		if err := s.ctx.Err(); err != nil {
			return contractSelection{}, err
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
		benchmark := benchmarkPath(e.Path)
		declaration := typeDeclarationPath(e.Path)
		if maintenance[e.Path] || benchmark || declaration {
			c.penalties = make([]int, len(queries))
		}
		var words []string
		formulaScope := false
		for i, q := range queries {
			if record.ScopeUnder(e.Path, q.scope) {
				formulaScope = formulaScope || q.formula.anchored()
				if words == nil {
					words = witnessTerms(path.Base(e.Path))
				}
				score := witnessMatch(words, q.terms)
				if q.names(e.Path) {
					score += 16
				}
				if c.penalties != nil {
					helper := maintenance[e.Path] && q.product && !q.namesMaintenance(e.Path)
					typeOnly := declaration && q.hasRuntimeJavaScript && !q.typeContract
					auxiliaryBenchmark := benchmark && (q.product || q.hasRuntimeJavaScript) && !q.performance
					if !q.names(e.Path) && (helper || typeOnly || auxiliaryBenchmark) {
						c.penalties[i] = 2 * witnessMinScore
					}
					score = max(0, score-c.penalties[i])
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
		eligible[e.Path] = c
		if formulaScope && !maintenance[e.Path] && !benchmark && len(formulaCode) < witnessMaxFiles/3 &&
			callLanguage(e.Path) == "python" && windowKind(e.Path) == "code" {
			formulaCode = append(formulaCode, c)
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
	// An accepted formula can name its subject in natural-language
	// docstrings while file names and runtime identifiers use another language.
	candidates = append(candidates, formulaCode...)
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
	javaRuntime := make([]contractWitness, len(queries))
	var javaCalls []contractCall
	pythonRuntime := make([]contractWitness, len(queries))
	pythonTests := make([]contractWitness, len(queries))
	formulas := make([]contractWitness, len(queries))
	defaults := make([]contractWitness, len(queries))
	formulaTies := make([]bool, len(queries))
	cRuntime := make(documentedCDefinitions, len(queries))
	var cFamilies cFamilyCatalog
	additional := make([][]contractWitness, len(queries))
	calls := make([][]contractCall, len(queries))
	members := memberWitnesses{sources: map[memberKey][]contractWitness{}, tests: map[string][]memberTest{}}
	catalog := s.callCatalog()
	manifest := catalog.files
	remaining := witnessScanBytes
	scanned := map[string]bool{}
	contents := map[string]string{}
	safe := map[string]string{}
	readSafe := func(e source.Entry) (string, bool, error) {
		if err := s.ctx.Err(); err != nil {
			return "", false, err
		}
		if scanned[e.Path] {
			content, ok := safe[e.Path]
			return content, ok, nil
		}
		if len(scanned) >= witnessMaxFiles || e.Size == nil || *e.Size > int64(remaining) {
			return "", false, nil
		}
		scanned[e.Path] = true
		remaining -= int(*e.Size)
		content, ok, err := s.readSource(e)
		if ok {
			safe[e.Path] = content
		}
		return content, ok, err
	}
	goTraces, err := s.goCommandTraces(g, queries, readSafe)
	if err != nil {
		return contractSelection{}, err
	}
	wrappers, err := s.goWrapperTraces(g, queries, readSafe)
	if err != nil {
		return contractSelection{}, err
	}
	goTraces = append(goTraces, wrappers...)
	read := func(e source.Entry) (string, bool, error) {
		content, ok, err := readSafe(e)
		if !ok || record.GeneratedHeader([]byte(content)) {
			return "", false, err
		}
		contents[e.Path] = content
		return content, true, err
	}
	bindings := catalog.calls(func(e source.Entry) (string, bool, error) {
		if callPathMetadata(e.Path) {
			return readSafe(e)
		}
		if _, ok := eligible[e.Path]; !ok {
			return "", false, nil
		}
		return read(e)
	})
	rustAPIs := make([][]rustContractWitness, len(queries))
	for i, q := range queries {
		bound, err := rustDocumentedBindings(bindings, q)
		if err != nil {
			return contractSelection{}, err
		}
		for _, b := range bound {
			rustAPIs[i] = append(rustAPIs[i], rustContractWitness{
				binding: b, contractIndex: q.contractIndex, implementation: rustImplementationWitness(b, q),
			})
		}
	}
	processed := map[string]bool{}
	for _, c := range candidates {
		e := c.entry
		if e.Size == nil || processed[e.Path] {
			continue
		}
		processed[e.Path] = true
		content, ok, err := read(e)
		if err != nil {
			return contractSelection{}, err
		}
		if !ok {
			continue
		}
		found := matchWitnessLines(content, e, queries)
		defaultSites := goDefaultWitnesses(e, content, queries)
		for i, q := range queries {
			if len(q.goDefaults) == 0 || !strings.HasSuffix(e.Path, ".go") {
				continue
			}
			found[i] = contractWitness{}
			if i < len(defaultSites) && defaultSites[i].score > defaults[i].score && !maintenance[e.Path] && !benchmarkPath(e.Path) {
				defaults[i] = defaultSites[i]
			}
		}
		for i, query := range queries {
			if len(query.javaDocs) > 0 {
				found[i] = contractWitness{}
				if w := documentedJavaImplementation(e, content, query); w.line != 0 {
					w.query = i + 1
					javaRuntime[i] = w
					javaCalls = append(javaCalls, documentedJavaCalls(e, content, w, query.terms)...)
				}
				continue
			}
			if len(query.rustDocs) > 0 {
				found[i] = contractWitness{}
				for j := range rustAPIs[i] {
					trace := &rustAPIs[i][j]
					w, err := trace.binding.linkedAssertion(bindings, e, content, query)
					if err != nil {
						return contractSelection{}, err
					}
					if w.score > trace.test.score {
						trace.test = w
					}
				}
				continue
			}
			if w := documentedPythonWitness(e, content, query); w.line != 0 {
				w.query = i + 1
				if w.score > pythonRuntime[i].score {
					pythonRuntime[i] = w
				}
				found[i] = w
			} else if len(query.pythonDocs) > 0 {
				w := documentedPythonTestRows(e, content, query)
				if w.line == 0 {
					var err error
					w, err = linkedPythonAssertion(bindings, e, content, query)
					if err != nil {
						return contractSelection{}, err
					}
				}
				if w.line != 0 {
					w.query = i + 1
					if w.score > pythonTests[i].score {
						pythonTests[i] = w
					}
					found[i] = w
				}
			}
		}
		wholeFile := shortReviewSource(content, roleMaxCodeFile)
		kind := 0
		if windowKind(e.Path) == "test" {
			kind = 1
		}
		if !maintenance[e.Path] && !benchmarkPath(e.Path) && !typeDeclarationPath(e.Path) {
			members.observe(e, content, queries, kind == 1)
			cRuntime.observe(e, content, queries)
			if s.j.PullRequest == nil || slices.Contains(s.j.PullRequest.Changed, e.Path) {
				cFamilies.observe(e, content, queries)
			}
			for i, query := range queries {
				if w, tied := pythonFormulaWitness(e, content, query); w.line > 0 {
					w.query = i + 1
					if w.score > formulas[i].score {
						formulas[i], formulaTies[i] = w, tied
					} else if w.score == formulas[i].score {
						formulaTies[i] = true
					}
				}
			}
		}
		for i, w := range found {
			w.query = i + 1
			q := queries[i]
			if q.hasGoCommandRoute && !q.names(e.Path) &&
				(maintenance[e.Path] && !q.namesMaintenance(e.Path) || benchmarkPath(e.Path) && !q.performance) {
				continue
			}
			if c.penalties != nil {
				w.score -= c.penalties[i]
			}
			if g.eligible < roleMaxCodeFiles && w.score >= witnessMinScore && w.direct && kind == 0 &&
				!maintenance[e.Path] && !benchmarkPath(e.Path) && !typeDeclarationPath(e.Path) {
				additional[i] = append(additional[i], w)
			}
			// An anchor already visible in the ordinary report's leading
			// lines does not justify lifting the file out of breadth order.
			if w.score >= witnessMinScore && (!c.inOverview || (w.line > 6 && (w.direct || !wholeFile))) {
				matches[i][kind] = append(matches[i][kind], w)
			}
		}
		checked := map[string]bool{}
		for i, w := range found {
			if scope := queries[i].scope; w.line != 0 && !checked[scope] {
				checked[scope] = true
				bound, err := bindings.from(e.Path, scope)
				if err != nil {
					return contractSelection{}, err
				}
				for _, trace := range contractCalls(content, e, bound, queries, found, c.penalties, scope) {
					i, q := trace.query, queries[trace.query]
					if q.hasGoCommandRoute || (found[i].end > 0 && len(q.pythonDocs[e.Path]) != 0) || len(q.pythonDocs[trace.site.callee.path]) > 0 {
						continue
					}
					target, allowed := eligible[trace.site.callee.path]
					if !allowed || windowKind(target.entry.Path) != "code" {
						continue
					}
					if c.inOverview && target.inOverview && inWitnessPrefix(content, trace.site.caller.end) &&
						inWitnessPrefix(contents[target.entry.Path], trace.site.callee.end) {
						continue
					}
					calls[i] = append(calls[i], contractCall{
						caller:    contractWitness{entry: e, line: trace.start, score: trace.score, query: i + 1},
						callee:    contractWitness{entry: target.entry, line: trace.site.callee.start, query: i + 1},
						callerEnd: trace.site.caller.end, calleeEnd: trace.site.callee.end, symbol: trace.site.symbol,
					})
				}
			}
		}
	}

	jsOptions := jsOptionTraces(queries, contents, manifest, maintenance)
	goOption, err := s.goOptionWitness(queries, contents, readSafe)
	if err != nil {
		return contractSelection{}, err
	}
	goJSONFields, err := s.goJSONFieldTraces(queries, contents)
	if err != nil {
		return contractSelection{}, err
	}
	hasTests := false
	for i := range matches {
		hasTests = hasTests || len(matches[i][1]) > 0
		for _, list := range matches[i] {
			sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
		}
	}
	var out []contractWitness
	var linked []contractCall
	seen := map[string]bool{}
	hasCode := make([]bool, len(matches))
	for i, w := range javaRuntime {
		if len(out) < witnessMaxExcerpts && w.line > 0 && !seen[w.entry.Path] {
			out = append(out, w)
			seen[w.entry.Path], hasCode[i] = true, true
		}
	}
	member, paired := members.choose(queries, manifest)
	if paired {
		out = append(out, member.source, member.test)
		seen[member.source.entry.Path], seen[member.test.entry.Path] = true, true
		hasCode[member.source.query-1] = true
	}
	for i, w := range defaults {
		if w.line == 0 {
			continue
		}
		if seen[w.entry.Path] {
			for j, old := range out {
				if old.entry.Path == w.entry.Path && old.end > 0 {
					first, last := min(old.line, w.line), max(old.end, w.end)
					if fitsWitness(strings.Split(contents[w.entry.Path], "\n"), first, last) {
						out[j].line, out[j].end = first, last
						hasCode[i] = true
					}
				}
			}
			continue
		}
		if len(out) < witnessMaxExcerpts {
			out = append(out, w)
			seen[w.entry.Path], hasCode[i] = true, true
		}
	}
	// A directly cited API and its numeric fixture deserve the same slots
	// before a broad project description claims one of their paths.
	preferredPython := map[string]contractWitness{}
	for _, w := range pythonRuntime {
		if w.score > preferredPython[w.entry.Path].score {
			preferredPython[w.entry.Path] = w
		}
	}
	for i, w := range pythonRuntime {
		test := pythonTests[i]
		if len(out)+2 > witnessMaxExcerpts {
			break
		}
		if w.line > 0 && test.line > 0 && !seen[w.entry.Path] && !seen[test.entry.Path] {
			out = append(out, preferredPython[w.entry.Path], test)
			seen[w.entry.Path], seen[test.entry.Path] = true, true
		}
	}
	for i, w := range pythonRuntime {
		if w.line == 0 {
			continue
		}
		if len(out) < witnessMaxExcerpts && !seen[w.entry.Path] {
			out = append(out, preferredPython[w.entry.Path])
			seen[w.entry.Path] = true
		}
		hasCode[i] = seen[w.entry.Path]
	}
	for i, w := range formulas {
		if len(out) < witnessMaxExcerpts && w.line > 0 && !formulaTies[i] && !seen[w.entry.Path] {
			out = append(out, w)
			seen[w.entry.Path], hasCode[i] = true, true
			break
		}
	}
	for i, list := range calls {
		if len(out)+2 > witnessMaxExcerpts {
			break
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].caller.score > list[j].caller.score })
		for _, call := range list {
			caller, callee := call.caller, call.callee
			if seen[caller.entry.Path] || seen[callee.entry.Path] {
				continue
			}
			out = append(out, caller, callee)
			linked = append(linked, call)
			seen[caller.entry.Path], seen[callee.entry.Path] = true, true
			hasCode[i] = true
			break
		}
	}
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
	if hasTests && !paired {
		codeLimit--
	}
	if len(out) < codeLimit {
		var guard contractWitness
		for _, list := range matches {
			for _, w := range list[0] {
				if w.compileBranch && !seen[w.entry.Path] && w.score > guard.score {
					guard = w
				}
			}
		}
		if guard.line > 0 {
			// One complete compile-time branch keeps an explicitly named
			// setting visible when earlier contracts share the same file.
			out = append(out, guard)
			seen[guard.entry.Path] = true
			hasCode[guard.query-1] = true
		}
	}
	for i := 0; i < len(matches) && len(out) < codeLimit; i++ {
		if !hasCode[i] {
			hasCode[i] = take(i, 0)
		}
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
	firstQuery := map[string]int{}
	for _, w := range out {
		firstQuery[w.entry.Path] = w.query
	}
	var extras []contractWitness
	for _, list := range additional {
		sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
		for _, w := range list {
			if firstQuery[w.entry.Path] != w.query {
				extras = append(extras, w)
			}
		}
	}

	return contractSelection{witnesses: out, additional: extras, rustAPIs: slices.Concat(rustAPIs...), calls: linked, javaCalls: javaCalls, member: member, sources: contents,
		goTraces: goTraces, goOption: goOption, goJSONFields: goJSONFields, cDefinitions: cRuntime.choose(queries), cFamily: cFamilies.choose(queries), queries: queries, claimWitnesses: contractClaimCandidates(matches), jsOptions: jsOptions}, nil
}

func minifiedSource(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".min.js") || strings.HasSuffix(lower, ".min.mjs") || strings.HasSuffix(lower, ".min.cjs")
}

type contractCallChoice struct {
	site                sourceCall
	start, score, query int
}

// contractCalls ties resolved calls to a lexical contract anchor. Equally
// relevant distinct calls leave the ordinary source selection intact.
func contractCalls(content string, e source.Entry, sites []sourceCall, queries []witnessQuery, found []contractWitness,
	penalties []int, scope string) []contractCallChoice {
	lines := strings.Split(content, "\n")
	choices := make([]contractCallChoice, len(queries))
	tied := make([]bool, len(queries))
	for _, site := range sites {
		from, to := site.caller.start, site.caller.end
		if !fitsWitness(lines, from, to) {
			continue
		}
		anchor := lines[from-1]
		if from > 1 {
			previous := strings.TrimSpace(lines[from-2])
			if len(previous) <= witnessMaxExcerpt && witnessControl.MatchString(previous) && fitsWitness(lines, from-1, to) {
				anchor = previous + " " + anchor
				from--
			}
		}
		words := witnessTerms(anchor)
		for i, q := range queries {
			if found[i].line == 0 || q.scope != scope {
				continue
			}
			score := witnessMatch(words, q.terms)
			if q.names(e.Path) {
				score += witnessMinScore
			}
			if penalties != nil {
				score -= penalties[i]
			}
			if score < witnessMinScore {
				continue
			}
			switch {
			case score > choices[i].score:
				choices[i] = contractCallChoice{site: site, start: from, score: score, query: i}
				tied[i] = false
			case score == choices[i].score && choices[i].site != site:
				tied[i] = true
			}
		}
	}
	var out []contractCallChoice
	for i, match := range choices {
		if !tied[i] && match.start > 0 {
			out = append(out, match)
		}
	}
	return out
}

func fitsWitness(lines []string, start, end int) bool {
	if end-start >= witnessMaxLines {
		return false
	}
	used := 0
	for _, line := range lines[start-1 : end] {
		if len(line) > witnessMaxExcerpt {
			return false
		}
		used += record.JSONLen(line)
		if used > witnessMaxExcerpt {
			return false
		}
	}
	return true
}

func inWitnessPrefix(content string, line int) bool {
	if line > 50 {
		return false
	}
	used := 0
	for i, raw := range strings.SplitN(content, "\n", line+1) {
		if i == line {
			break
		}
		if len(raw) > roleMaxCodeFile {
			return false
		}
		used += record.JSONLen(raw)
		if used > roleMaxCodeFile {
			return false
		}
	}
	return true
}

func (s *roleStage) witnessQueries(docs record.Record) ([]witnessQuery, error) {
	if len(docs.Contracts) == 0 {
		return nil, nil
	}
	catalog := catalogOf(docs.Evidence)
	files := s.entries()
	cited := map[string]string{}
	languages := languagesInScope(s.j)
	hasC, hasGo := slices.Contains(languages, "cpp"), slices.Contains(languages, "go")
	var cGuideCache cFamilyGuideCache
	var out []witnessQuery
	for i, c := range docs.Contracts {
		statement := map[string]int{}
		addWitnessWords(statement, c.Statement, 4)
		paths := map[string]bool{}
		var pythonDocs map[string][]record.Excerpt
		var rustDocs map[string][]record.Excerpt
		var javaDocs map[string][]record.Excerpt
		var pythonCalls []string
		var formula pythonFormulaQuery
		var cFamilies []cAPIFamily
		statementCalls := pythonFreeCalls(c.Statement)
		var packageEntry string
		var goExamples []goContractExample
		var goCommands []goContractCommand
		var goDefaults, goJSONFields []string
		addWitnessPaths(paths, c.Statement)
		members := documentedMembers(c.Statement, true)
		names := navigationIdentifiers(c.Statement)
		var exactNames []string
		terms := map[string]int{}
		for word, weight := range statement {
			terms[word] = weight
		}
		for _, id := range c.EvidenceIDs {
			x := catalog[id]
			raw, cached := cited[id]
			if !cached {
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
			if len(statementCalls) > 0 && c.AppliesTo == x.DocumentScope() {
				for _, name := range pythonFreeCalls(raw) {
					if slices.Contains(statementCalls, name) && !slices.Contains(pythonCalls, name) {
						pythonCalls = append(pythonCalls, name)
					}
				}
			}
			if x.DirectivePath == "" && c.AppliesTo == x.DocumentScope() {
				goExamples = goExamplesInContract(goExamples, c.Statement, raw)
				goCommands = goCommandsInContract(goCommands, c.Statement, raw)
				if hasGo {
					goDefaults = append(goDefaults, goDefaultFields(c.Statement, raw)...)
					goJSONFields = documentedGoJSONFields(goJSONFields, c.Statement, raw)
				}
				formula.fromPassage(c.Statement, passage)
				if hasC {
					var err error
					cFamilies, err = s.documentedCFamilies(&cGuideCache, files[x.Path], raw, names, cFamilies)
					if err != nil {
						return nil, err
					}
				}
				if command, ok := standaloneGoCommand(x.Path, c.Statement, raw); ok && !slices.Contains(goCommands, command) {
					goCommands = append(goCommands, command)
				}
			}
			addWitnessWords(terms, passage, 2)
			addWitnessPaths(paths, passage)
			for _, name := range names {
				if !slices.Contains(exactNames, name) && containsContractLiteral(raw, name) {
					exactNames = append(exactNames, name)
				}
			}
			if x.IsRustAPIDocument() {
				if rustDocs == nil {
					rustDocs = map[string][]record.Excerpt{}
				}
				rustDocs[x.Path] = append(rustDocs[x.Path], x)
				paths[x.Path] = true
			} else if x.DirectivePath != "" {
				if pythonDocs == nil {
					pythonDocs = map[string][]record.Excerpt{}
				}
				pythonDocs[x.Path] = append(pythonDocs[x.Path], x)
				paths[x.Path] = true
				addWitnessWords(terms, strings.TrimSuffix(path.Base(x.Path), ".py"), 4)
				addPythonDocExampleTerms(terms, raw)
			}
			if x.IsJavaAPIDocument() {
				if javaDocs == nil {
					javaDocs = map[string][]record.Excerpt{}
				}
				javaDocs[x.Path] = append(javaDocs[x.Path], x)
				paths[x.Path] = true
			}
			if ref := x.PackageReference; !ref.IsZero() && ref.Scope() == c.AppliesTo && ref.Matches(c.Statement) {
				paths[ref.EntryPath] = true
				packageEntry = ref.EntryPath
				if ref.Export != "default" && !slices.Contains(exactNames, ref.Export) {
					exactNames = append(exactNames, ref.Export)
					addWitnessWords(terms, ref.Export, witnessMinScore)
				}
			}
			if len(members) == 0 {
				members = documentedMembers(passage, false)
			}
		}
		if len(javaDocs) > 0 {
			members = nil
		}
		if len(terms) != 0 || len(paths) != 0 {
			var jsAPINames []string
			for _, m := range documentedCall.FindAllStringSubmatch(c.Statement, len(exactNames)) {
				if slices.Contains(exactNames, m[1]) && !slices.Contains(jsAPINames, m[1]) {
					jsAPINames = append(jsAPINames, m[1])
				}
			}
			out = append(out, witnessQuery{contractIndex: i + 1, scope: c.AppliesTo, terms: terms, statement: statement, paths: paths,
				operation: maintenanceOperation(c.Statement), typeContract: mentionsJavaScriptTypes(c.Statement),
				performance: performanceContract(c.Statement), members: members, navigationNames: exactNames, jsAPINames: jsAPINames, jsOptionNames: jsDocumentedOptions(c.Statement, exactNames), javaDocs: javaDocs, pythonDocs: pythonDocs, rustDocs: rustDocs, pythonCalls: pythonCalls,
				formula: formula, cFamilies: cFamilies, packageEntry: packageEntry, goExamples: goExamples, goCommands: goCommands,
				goDefaults: goDefaults, goJSONFields: goJSONFields})
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
			if q.packageEntry == e.Path {
				best[i] = contractWitness{entry: e, line: 1, score: witnessMinScore}
			}
		} else {
			pathScores[i] = -1
		}
	}
	lines := strings.Split(text, "\n")
	var multiline string
	cFamily := record.SourceEcosystem(e.Path) == "cpp"
	continued := false
	for line, raw := range lines {
		s := strings.TrimSpace(raw)
		inContinuation := continued
		continued = cFamily && strings.HasSuffix(s, "\\")
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
		if cFamily {
			if multiline = cRawStringEnd(s); multiline != "" {
				continue
			}
			if inContinuation {
				continue
			}
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
		guard := cFamily && compileCondition.MatchString(s) && compileBranchVisible(lines, line)
		if s == "" || len(raw) > witnessMaxExcerpt || strings.HasPrefix(s, "import ") ||
			strings.HasPrefix(s, "package ") || strings.HasPrefix(s, "from ") ||
			strings.HasPrefix(s, "use ") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/*") ||
			strings.HasPrefix(s, "* ") || (strings.HasPrefix(s, "#") && !guard) {
			continue
		}
		words := witnessTerms(s)
		structure := 0
		if guard || witnessDeclaration.MatchString(s) || witnessControl.MatchString(s) {
			structure = 4
		}
		for i, q := range queries {
			if pathScores[i] < 0 {
				continue
			}
			lineMatch := witnessMatch(words, q.terms)
			score := lineMatch
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
			if !guard && line > 0 && witnessControl.MatchString(strings.TrimSpace(lines[line-1])) {
				anchor--
				score += 3
			}
			if score < witnessMinScore || score <= best[i].score || record.JSONLen(raw) > witnessMaxExcerpt {
				continue
			}
			if anchor != line && record.JSONLen(lines[anchor])+record.JSONLen(raw) > witnessMaxExcerpt {
				anchor = line
			}
			best[i] = contractWitness{entry: e, line: anchor + 1, score: score, compileBranch: guard && lineMatch >= 4,
				direct:     named || witnessMatch(words, q.statement) >= witnessMinScore,
				documented: named || slices.ContainsFunc(q.navigationNames, func(name string) bool { return containsContractLiteral(s, name) })}
		}
	}
	return best
}

// A preprocessor guard is useful evidence if its own branch contains a
// statement within the same bounded excerpt. A macro declaration or an
// include alone contributes no executable branch.
func compileBranchVisible(lines []string, guard int) bool {
	var multiline string
	continued := strings.HasSuffix(strings.TrimSpace(lines[guard]), "\\")
	for i := guard + 1; i < min(len(lines), guard+witnessMaxLines); i++ {
		raw := strings.TrimSpace(lines[i])
		inContinuation := continued
		continued = strings.HasSuffix(raw, "\\")
		if multiline != "" {
			if strings.Contains(raw, multiline) {
				multiline = ""
			}
			continue
		}
		if strings.HasPrefix(raw, "/*") {
			if !strings.Contains(raw, "*/") {
				multiline = "*/"
			}
			continue
		}
		if multiline = cRawStringEnd(raw); multiline != "" || inContinuation {
			continue
		}
		if compileBranchEnd.MatchString(raw) {
			return false
		}
		if cExecutableLine.MatchString(raw) {
			return fitsWitness(lines, guard+1, i+1)
		}
	}
	return false
}

func cRawStringEnd(line string) string {
	m := cRawString.FindStringSubmatchIndex(line)
	if m == nil {
		return ""
	}
	close := ")" + line[m[2]:m[3]] + `"`
	if strings.Contains(line[m[1]:], close) {
		return ""
	}
	return close
}
