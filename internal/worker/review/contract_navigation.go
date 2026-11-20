package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	navigationMaxLocations = 12
	navigationMaxFiles     = 32
	navigationScanBytes    = 512 << 10
)

const investigateProbeContract = ` When navigation_candidates is present, include "contract_probe" in the same result. Set it to {"contract_index":1,"candidate_ids":["L1"],"literals":["knownIdentifier"]} for one meaningful follow-up or to null if no such follow-up exists. Select an index already marked unknown in contract_comparisons, and copy one to three exact L IDs that include that index and lie in its applies_to directory. Optionally add up to three case-sensitive literals copied from the provided names or text (single ASCII identifier, 3–48 characters). Choose locations that might resolve the missing condition, call, or test assertion. These location IDs are navigation only, so never cite them as evidence.`

type contractCandidateInput struct {
	ID              string `json:"id"`
	Path            string `json:"path"`
	Start           int    `json:"start_line"`
	End             int    `json:"end_line"`
	Kind            string `json:"kind"`
	Symbol          string `json:"symbol,omitempty"`
	ContractIndices []int  `json:"contract_indices"`
}

type contractNavigationCandidate struct {
	location record.ContractLocation
	indices  []int
	score    int
}

func navigationAlias(i int) string { return "L" + strconv.Itoa(i+1) }

func (c contractNavigationCandidate) input(i int) contractCandidateInput {
	p := c.location
	return contractCandidateInput{ID: navigationAlias(i), Path: p.Path, Start: p.Start, End: p.End, Kind: p.Kind, Symbol: p.Symbol, ContractIndices: c.indices}
}

func (s *roleStage) fitNavigation(in *investigateInput, candidates []contractNavigationCandidate) []contractNavigationCandidate {
	in.NavigationCandidates = make([]contractCandidateInput, 0, len(candidates))
	for i, c := range candidates {
		in.NavigationCandidates = append(in.NavigationCandidates, c.input(i))
	}
	instructions := s.instructions(investigateContract + investigateProbeContract)
	for len(candidates) > 0 {
		b, err := marshal(in)
		if _, ok := s.fits(instructions, b, true); err == nil && ok {
			return candidates
		}
		candidates = candidates[:len(candidates)-1]
		in.NavigationCandidates = in.NavigationCandidates[:len(candidates)]
	}
	return nil
}

func (s *roleStage) navigationEligible(g codeGroups, path string) bool {
	_, eligible := g.rank[path]
	return eligible && g.tierOf(path) == tierChanged && g.secondary[path] == "" &&
		!minifiedSource(path) && !record.Sensitive([]byte(path)) && !s.secretFiles()[path]
}

func (s *roleStage) navigationFiles(g codeGroups, scopes []string) []source.Entry {
	return s.boundedNavigationFiles(g, scopes, false)
}

// Reserve file and byte budgets separately for code and tests. For the
// overview catalog, visit representatives of more directories before
// returning to siblings, so shallow source and test suites cannot use every
// read slot before a nested product directory is reached.
func (s *roleStage) boundedNavigationFiles(g codeGroups, scopes []string, spreadDirectories bool) []source.Entry {
	var lanes [2][]source.Entry
	var sizes [2]int64
	dirs := [2]map[string]bool{{}, {}}
	selected := map[string]bool{}
	passes := []bool{false}
	if spreadDirectories {
		passes = []bool{true, false}
	}
	for _, newDirectory := range passes {
		if len(lanes[0])+len(lanes[1]) == navigationMaxFiles {
			break
		}
		for e := range g.orderedFiles() {
			if s.ctx.Err() != nil || len(lanes[0])+len(lanes[1]) == navigationMaxFiles {
				break
			}
			if selected[e.Path] || !s.navigationEligible(g, e.Path) ||
				!slices.ContainsFunc(scopes, func(scope string) bool { return record.ScopeUnder(e.Path, scope) }) {
				continue
			}
			lane := 0
			if record.SourceKind(e.Path) == "test" {
				lane = 1
			}
			if len(lanes[lane]) >= navigationMaxFiles/2 || *e.Size > navigationScanBytes/2-sizes[lane] {
				continue
			}
			if newDirectory {
				dir := path.Dir(e.Path)
				if dirs[lane][dir] {
					continue
				}
				dirs[lane][dir] = true
			}
			selected[e.Path] = true
			lanes[lane] = append(lanes[lane], e)
			sizes[lane] += *e.Size
		}
	}
	files := make([]source.Entry, 0, len(lanes[0])+len(lanes[1]))
	for i := 0; i < max(len(lanes[0]), len(lanes[1])); i++ {
		for _, lane := range lanes {
			if i < len(lane) {
				files = append(files, lane[i])
			}
		}
	}
	return files
}

var (
	goAnchor        = regexp.MustCompile(`^func[ \t]+(?:\([^)]*\)[ \t]+)?([A-Za-z_][A-Za-z0-9_]*)[ \t]*[[(]`)
	namedAnchor     = regexp.MustCompile(`^(?:(?:export|default|async|pub|unsafe|static|constexpr)[ \t]+)*(?:def|function|fn)[ \t]+([A-Za-z_][A-Za-z0-9_]*)[ \t]*[<(]`)
	callAnchor      = regexp.MustCompile(`^(?:(?:await|return)[ \t]+)?(?:[A-Za-z_][A-Za-z0-9_]*\.)*([A-Za-z_][A-Za-z0-9_]*)[ \t]*\(`)
	typedAnchor     = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_:<>&*]*[ \t]+)+([A-Za-z_][A-Za-z0-9_]*)\([^;]*\)[^;]*\{`)
	anchorsInLine   = []*regexp.Regexp{goAnchor, namedAnchor, typedAnchor}
	navigationToken = regexp.MustCompile(`//.*|/\*.*|#.*|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\x60[^\x60]*\x60|[\pL\pN_$][\pL\pN_$-]*`)
)

func navigationSymbol(line string, includeCalls bool) string {
	line = strings.TrimSpace(line)
	for _, expr := range anchorsInLine {
		if m := expr.FindStringSubmatch(line); len(m) > 0 && record.ValidContractLiteral(m[1]) {
			return m[1]
		}
	}
	if m := callAnchor.FindStringSubmatch(line); includeCalls && len(m) > 0 &&
		slices.Contains([]string{"test", "describe", "get", "post", "route", "handle", "register", "expect", "assert"}, m[1]) {
		return m[1]
	}
	return ""
}

// navigationSourceLines walks plausible code lines, preserving the original
// positions. Its positions are search starting points, not parsed control flow.
func navigationSourceLines(lines []string, visit func(int, string) bool) {
	var multiline string
	for i, line := range lines {
		stripped := strings.TrimSpace(line)
		if multiline != "" {
			if strings.Contains(stripped, multiline) {
				multiline = ""
			}
			continue
		}
		if stripped == "" || strings.HasPrefix(stripped, "//") || strings.HasPrefix(stripped, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(stripped, "/*"); ok {
			if !strings.Contains(rest, "*/") {
				multiline = "*/"
			}
			continue
		}
		for _, quote := range []string{`"""`, `'''`} {
			if strings.Count(stripped, quote)%2 != 0 {
				multiline = quote
				break
			}
		}
		if multiline != "" {
			continue
		}
		if visit(i+1, line) {
			return
		}
	}
}

func (s *roleStage) navigationLocation(e source.Entry, lines []string, line int, symbol string) (record.ContractLocation, bool) {
	if line < 1 || line > len(lines) {
		return record.ContractLocation{}, false
	}
	start := max(1, line-1)
	end := min(len(lines), start+record.MaxContractLocationLines-1)
	excerpt, ok := s.boundedExcerpt(e, record.SourceKind(e.Path), "context", start, lines[start-1:end], roleMaxWindowBytes)
	if !ok || excerpt.End < line {
		return record.ContractLocation{}, false
	}
	if symbol != "" && !containsContractLiteral(excerpt.Text, symbol) {
		symbol = ""
	}
	location := record.ContractLocation{Path: e.Path, SHA256: e.SHA256, Start: excerpt.Start, End: excerpt.End, Kind: excerpt.Kind, Symbol: symbol}
	location.ID = record.ContractLocationID(s.j.Snapshot.Digest, location.Path, location.SHA256, location.Start, location.End)
	return location, location.Valid(s.j.Snapshot.Digest, ".")
}

func navigationIdentifiers(statement string) []string {
	const maxNames = 12
	var names []string
	add := func(name string) {
		if len(names) < maxNames && record.ValidContractLiteral(name) && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	for _, name := range documentedMembers(statement, true) {
		add(name)
	}
	for _, code := range witnessCode.FindAllStringSubmatch(statement, maxNames) {
		for _, name := range witnessWord.FindAllString(code[1], maxNames) {
			add(name)
		}
	}
	for _, name := range witnessWord.FindAllString(statement, -1) {
		if strings.ContainsRune(name, '_') || name != strings.ToLower(name) {
			add(name)
		}
	}
	return names
}

type navigationChoices struct {
	code, extra, test, structuralCode, structuralTest contractNavigationCandidate
	ambiguousStructuralCode                           bool
}

const (
	navigationExact = 100
	navigationNamed = 80
)

func (q witnessQuery) navigationScore(p, line, symbol string) int {
	exact := 0
	for _, name := range q.navigationNames {
		if name == symbol || containsContractLiteral(line, name) {
			exact++
		}
	}
	lexical := min(24, witnessMatch(witnessTerms(line), q.terms))
	switch {
	case exact > 0:
		return exact*navigationExact + lexical
	case q.names(p):
		return navigationNamed + lexical
	case len(q.navigationNames) == 0 && lexical >= witnessMinScore:
		return lexical
	}
	return 0
}

func (q witnessQuery) allowsNavigation(p string, maintenance map[string]bool) bool {
	return record.ScopeUnder(p, q.scope) &&
		(!maintenance[p] || !q.product || q.namesMaintenance(p)) &&
		(!benchmarkPath(p) || q.performance || q.names(p) || !q.product && !q.hasRuntimeJavaScript)
}

func navigationOverlap(a, b record.ContractLocation) bool {
	return a.Path == b.Path && a.Start <= b.End && b.Start <= a.End
}

func (c *navigationChoices) addCode(candidate contractNavigationCandidate) {
	if candidate.score <= 2 {
		if c.structuralCode.location.ID != "" && candidate.location.Path != c.structuralCode.location.Path {
			c.ambiguousStructuralCode = true
		} else if candidate.score > c.structuralCode.score {
			c.structuralCode = candidate
		}
		return
	}
	if candidate.score > c.code.score {
		candidate, c.code = c.code, candidate
		if navigationOverlap(c.extra.location, c.code.location) {
			c.extra = contractNavigationCandidate{}
		}
	}
	if candidate.score > c.extra.score && !navigationOverlap(candidate.location, c.code.location) {
		c.extra = candidate
	}
}

// navigationCatalog keeps the ordinary code windows fixed. It uses the
// bounded witness reads already available to the overview and reserves a
// separate bounded pass for directories the witness scan did not reach.
func (s *roleStage) navigationCatalog(docs record.Record, sent []record.Excerpt, cached map[string]string) []contractNavigationCandidate {
	queries, err := s.witnessQueries(docs)
	if err != nil || len(queries) == 0 {
		return nil
	}
	g := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	maintenance, err := s.maintenanceWitnesses(g, queries)
	if err != nil {
		return nil
	}
	var scopes []string
	for _, q := range queries {
		scopes = append(scopes, q.scope)
	}
	choices := make([]navigationChoices, len(queries))
	visited := map[string]bool{}
	add := func(e source.Entry, content string, structural bool) {
		visited[e.Path] = true
		lines := strings.Split(content, "\n")
		if record.SourceKind(e.Path) == "test" {
			s.rankNavigationTests(e, content, lines, sent, queries, choices, maintenance, structural)
		} else if contractRuntimePath(e.Path, record.SourceKind(e.Path)) {
			s.rankNavigationCode(e, content, lines, sent, queries, choices, maintenance, structural)
		}
	}
	for _, e := range s.boundedNavigationFiles(g, scopes, true) {
		if s.ctx.Err() != nil {
			return nil
		}
		content, ok := cached[e.Path]
		if !ok {
			var err error
			content, ok, err = s.readSource(e)
			if err != nil || !ok || record.GeneratedHeader([]byte(content)) {
				continue
			}
		}
		add(e, content, true)
	}
	if len(cached) != 0 {
		for e := range g.orderedFiles() {
			if s.ctx.Err() != nil {
				return nil
			}
			if content, ok := cached[e.Path]; ok && !visited[e.Path] && s.navigationEligible(g, e.Path) {
				add(e, content, false)
			}
		}
	}

	var out []contractNavigationCandidate
	appendRanked := func(list []contractNavigationCandidate) {
		slices.SortStableFunc(list, func(a, b contractNavigationCandidate) int { return b.score - a.score })
		for _, c := range list {
			if c.location.ID == "" {
				continue
			}
			i := slices.IndexFunc(out, func(old contractNavigationCandidate) bool { return old.location.ID == c.location.ID })
			if i >= 0 {
				out[i].indices = append(out[i].indices, c.indices...)
				slices.Sort(out[i].indices)
				out[i].indices = slices.Compact(out[i].indices)
			} else if len(out) < navigationMaxLocations {
				out = append(out, c)
			}
		}
	}
	var qualified, tests, extra, structuralCode, structuralTests []contractNavigationCandidate
	for _, choice := range choices {
		if choice.code.location.ID == "" && (choice.ambiguousStructuralCode || choice.structuralCode.location.ID == "") {
			continue
		}
		if choice.code.location.ID != "" {
			qualified = append(qualified, choice.code)
			tests = append(tests, choice.test)
		} else {
			structuralCode = append(structuralCode, choice.structuralCode)
			structuralTests = append(structuralTests, choice.test)
		}
		if choice.test.location.ID == "" {
			structuralTests = append(structuralTests, choice.structuralTest)
		}
		if choice.extra.score > 2 {
			extra = append(extra, choice.extra)
		}
	}
	appendRanked(qualified)
	appendRanked(tests)
	// Keep a code option for each applicable contract before reserving
	// secondary decisions or generic tests.
	appendRanked(structuralCode)
	appendRanked(extra)
	appendRanked(structuralTests)
	return out
}

func (s *roleStage) rankNavigationCode(e source.Entry, content string, lines []string, sent []record.Excerpt, queries []witnessQuery, choices []navigationChoices, maintenance map[string]bool, structural bool) {
	pathEvidence := record.Excerpt{Path: e.Path, SHA256: e.SHA256, Kind: "code"}
	type activity struct {
		line     int
		decision bool
	}
	var actions []activity
	freshText := make([]string, len(lines))
	masked := contractExecutableSource(content, record.SourceEcosystem(e.Path))
	navigationSourceLines(strings.Split(masked, "\n"), func(line int, raw string) bool {
		if record.NewContractEvidenceLine(pathEvidence, line, sent) && len(lines[line-1]) <= roleMaxWindowBytes {
			freshText[line-1] = navigationCodeText(lines[line-1])
			if action, decision := contractLineActivity(raw); action {
				actions = append(actions, activity{line, decision})
			}
		}
		return false
	})
	for _, action := range actions {
		line := action.line
		start, symbol := max(1, line-3), navigationSymbol(lines[line-1], true)
		for previous := line - 1; previous >= max(1, line-4); previous-- {
			if strings.Contains(lines[previous-1], "}") {
				break
			}
			if name := navigationSymbol(lines[previous-1], false); name != "" {
				start, symbol = previous, name
				break
			}
		}
		var location record.ContractLocation
		// Related names can occur on adjacent actions or on the nearby
		// continuation of a declaration. Use only unread source here.
		first, last := max(0, start-2, line-5), min(len(lines), line+4)
		context := strings.Join(freshText[first:last], " ")
		for i, q := range queries {
			if !q.allowsNavigation(e.Path, maintenance) {
				continue
			}
			score := q.navigationScore(e.Path, context, symbol)
			directDecision := action.decision && q.navigationScore(e.Path, freshText[line-1], symbol) > 0
			if score > 0 && directDecision {
				score += 12
			} else if score == 0 && structural && len(q.navigationNames) == 0 && symbol != "" {
				score = 1
				if action.decision {
					score++
				}
			}
			if score <= choices[i].extra.score {
				continue
			}
			if location.ID == "" {
				var ok bool
				location, ok = s.navigationLocation(e, lines, start, symbol)
				if !ok || location.End < line {
					location, ok = s.navigationLocation(e, lines, line, symbol)
					if !ok {
						break
					}
				}
				first, last = max(first, location.Start-1), min(last, location.End)
				context = strings.Join(freshText[first:last], " ")
				if score > 2 {
					score = q.navigationScore(e.Path, context, location.Symbol)
					if score > 0 && directDecision {
						score += 12
					}
				}
			}
			if score > 0 {
				choices[i].addCode(contractNavigationCandidate{location: location, indices: []int{q.contractIndex}, score: score})
			}
		}
	}
}

func navigationCodeText(line string) string {
	var words []string
	for _, token := range navigationToken.FindAllString(line, -1) {
		switch token[0] {
		case '/', '#':
			return strings.Join(words, " ")
		case '"', '\'', '`':
			value := token[1 : len(token)-1]
			if record.ValidContractLiteral(value) {
				words = append(words, value)
			}
		default:
			words = append(words, token)
		}
	}
	return strings.Join(words, " ")
}

func (s *roleStage) rankNavigationTests(e source.Entry, content string, lines []string, sent []record.Excerpt, queries []witnessQuery, choices []navigationChoices, maintenance map[string]bool, structural bool) {
	hits := matchWitnessLines(content, e, queries)
	var fallback record.ContractLocation
	if structural {
		fallback, _, _ = s.structuralNavigationLocation(e, lines, sent)
	}
	for i, q := range queries {
		if !q.allowsNavigation(e.Path, maintenance) {
			continue
		}
		hit := hits[i]
		if hit.line > 0 {
			location, ok := s.navigationLocation(e, lines, hit.line, navigationSymbol(lines[hit.line-1], true))
			if ok {
				window := strings.Join(lines[location.Start-1:location.End], "\n")
				if score := q.navigationScore(e.Path, window, location.Symbol); score > choices[i].test.score {
					choices[i].test = contractNavigationCandidate{location: location, indices: []int{q.contractIndex}, score: score}
				}
			}
		}
		if fallback.ID != "" && len(q.navigationNames) == 0 {
			score := min(4, fallback.End-fallback.Start+1)
			if !slices.ContainsFunc(sent, func(old record.Excerpt) bool {
				return old.Path == e.Path && old.SHA256 == e.SHA256 && old.Start <= fallback.Start && old.End >= fallback.End
			}) {
				score += 4
			}
			if score > choices[i].structuralTest.score {
				choices[i].structuralTest = contractNavigationCandidate{location: fallback, indices: []int{q.contractIndex}, score: score}
			}
		}
	}
}

func (s *roleStage) structuralNavigationLocation(e source.Entry, lines []string, sent []record.Excerpt) (record.ContractLocation, int, bool) {
	if location, ok := s.testNavigationLocation(e, lines); ok {
		score := 0
		if !slices.ContainsFunc(sent, func(x record.Excerpt) bool {
			return e.Path == x.Path && x.Start <= location.Start && x.End >= location.End
		}) {
			score = 1
		}
		return location, score, true
	}
	var first, unoffered int
	var firstName, unofferedName string
	navigationSourceLines(lines, func(line int, raw string) bool {
		name := navigationSymbol(raw, true)
		if name == "" {
			return false
		}
		if first == 0 {
			first, firstName = line, name
		}
		if !slices.ContainsFunc(sent, func(x record.Excerpt) bool { return e.Path == x.Path && x.Start <= line && x.End >= line }) {
			unoffered, unofferedName = line, name
			return true
		}
		return false
	})
	score := 0
	if unoffered != 0 {
		first, firstName, score = unoffered, unofferedName, 1
	}
	location, ok := s.navigationLocation(e, lines, first, firstName)
	return location, score, ok
}

func selectContractProbe(raw json.RawMessage, navigation []contractNavigationCandidate, comparisons []record.ContractComparison, in investigateInput) (*record.ContractProbe, string) {
	if !slices.ContainsFunc(comparisons, func(comparison record.ContractComparison) bool {
		return comparison.Status == "unknown" && slices.ContainsFunc(navigation, func(c contractNavigationCandidate) bool {
			return slices.Contains(c.indices, comparison.ContractIndex) && record.ScopeUnder(c.location.Path, comparison.AppliesTo)
		})
	}) {
		return nil, ""
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, record.LimNavigationNoChoice
	}
	probe := decodeContractProbe(raw, navigation, comparisons, in)
	if probe == nil {
		return nil, record.LimNavigationChoiceInvalid
	}
	return probe, ""
}

func decodeContractProbe(raw json.RawMessage, navigation []contractNavigationCandidate, comparisons []record.ContractComparison, in investigateInput) *record.ContractProbe {
	var choice struct {
		Index    int      `json:"contract_index"`
		IDs      []string `json:"candidate_ids"`
		Literals []string `json:"literals"`
	}
	if domain.DecodeStrict(raw, &choice) != nil || choice.Index < 1 || choice.Index > len(in.Documents.Contracts) ||
		len(choice.IDs) == 0 || len(choice.IDs) > record.MaxContractProbeCandidates ||
		len(choice.Literals) > record.MaxContractProbeLiterals {
		return nil
	}
	i := slices.IndexFunc(comparisons, func(c record.ContractComparison) bool {
		return c.ContractIndex == choice.Index && c.Status == "unknown"
	})
	if i < 0 {
		return nil
	}
	probe := record.ContractProbe{ContractIndex: choice.Index, Literals: choice.Literals}
	for _, id := range choice.IDs {
		var selected record.ContractLocation
		for i, c := range navigation {
			if (id == navigationAlias(i) || id == c.location.ID) && slices.Contains(c.indices, choice.Index) {
				selected = c.location
				break
			}
		}
		if selected.ID == "" {
			return nil
		}
		probe.Candidates = append(probe.Candidates, selected)
	}
	if !probe.Valid(in.Scope.Snapshot, comparisons[i]) ||
		!slices.ContainsFunc(probe.Candidates, func(c record.ContractLocation) bool { return c.Kind == "code" }) {
		return nil
	}
	for _, name := range probe.Literals {
		if !literalWasOffered(name, navigation, in, comparisons[i].AppliesTo, choice.Index) {
			return nil
		}
	}
	return &probe
}

func literalWasOffered(name string, navigation []contractNavigationCandidate, in investigateInput, scope string, index int) bool {
	if slices.ContainsFunc(navigation, func(c contractNavigationCandidate) bool {
		return c.location.Symbol == name && slices.Contains(c.indices, index) && record.ScopeUnder(c.location.Path, scope)
	}) || containsContractLiteral(in.Documents.Contracts[index-1].Statement, name) {
		return true
	}
	for _, q := range in.Documents.Questions {
		if record.ScopeUnder(scope, q.AppliesTo) && containsContractLiteral(q.Question, name) {
			return true
		}
	}
	return slices.ContainsFunc(in.Scope.Excerpts, func(x record.Excerpt) bool {
		return record.ScopeUnder(x.Path, scope) && containsContractLiteral(x.Text, name)
	})
}

func containsContractLiteral(text, name string) bool {
	if name == "" {
		return false
	}
	identifierRune := func(r rune) bool {
		return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
	}
	for offset := 0; offset < len(text); {
		nextMatch := strings.Index(text[offset:], name)
		if nextMatch < 0 {
			return false
		}
		idx := offset + nextMatch
		after := idx + len(name)
		prev, _ := utf8.DecodeLastRuneInString(text[:idx])
		next, _ := utf8.DecodeRuneInString(text[after:])
		if !identifierRune(prev) && !identifierRune(next) {
			return true
		}
		offset = after
	}
	return false
}

var errContractLocation = errors.New("contract probe location unavailable")

func (s *roleStage) contractProbeExcerpts(probe record.ContractProbe, comparison record.ContractComparison) ([]record.Excerpt, error) {
	if !probe.Valid(s.j.Snapshot.Digest, comparison) {
		return nil, errContractLocation
	}
	g := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	entries := s.entries()
	var out []record.Excerpt
	for _, p := range probe.Candidates {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		e, exists := entries[p.Path]
		if !exists || e.SHA256 != p.SHA256 || !s.navigationEligible(g, p.Path) {
			return nil, errContractLocation
		}
		content, ok, err := s.readSource(e)
		if err != nil {
			return nil, err
		}
		if !ok || record.GeneratedHeader([]byte(content)) {
			return nil, errContractLocation
		}
		lines := strings.Split(content, "\n")
		if p.End > len(lines) {
			return nil, errContractLocation
		}
		excerpt, ok := s.boundedExcerpt(e, p.Kind, "context", p.Start, lines[p.Start-1:p.End], roleMaxWindowBytes)
		if !ok || excerpt.End != p.End || (p.Symbol != "" && !containsContractLiteral(excerpt.Text, p.Symbol)) {
			return nil, errContractLocation
		}
		out = append(out, excerpt)
	}
	if len(probe.Literals) == 0 {
		return out, nil
	}
	remaining := record.MaxContractProbeLiterals
	for _, e := range s.navigationFiles(g, []string{comparison.AppliesTo}) {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		content, ok, err := s.readSource(e)
		if err != nil {
			return nil, err
		}
		if !ok || record.GeneratedHeader([]byte(content)) {
			continue
		}
		lines := strings.Split(content, "\n")
		navigationSourceLines(lines, func(line int, raw string) bool {
			if !slices.ContainsFunc(probe.Literals, func(literal string) bool { return containsContractLiteral(raw, literal) }) {
				return false
			}
			location, ok := s.navigationLocation(e, lines, line, "")
			if !ok || slices.ContainsFunc(out, func(x record.Excerpt) bool {
				return x.Path == location.Path && x.Start <= location.End && location.Start <= x.End
			}) {
				return false
			}
			// The matching line itself must remain in the byte-bounded window.
			excerpt, ok := s.boundedExcerpt(e, location.Kind, "context", location.Start, lines[location.Start-1:location.End], roleMaxWindowBytes)
			if !ok {
				return false
			}
			out = append(out, excerpt)
			remaining--
			return true
		})
		if remaining == 0 {
			break
		}
	}
	return out, nil
}
