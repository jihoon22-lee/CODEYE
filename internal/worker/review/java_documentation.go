package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const maxJavaAPIDocs = 4

type javaAPIExcerpt struct {
	excerpt   record.Excerpt
	signature string
}

func (s *roleStage) javaAPIScope(path string) bool {
	return inScope(path, normalizedScope(s.j.Scope)) &&
		(s.j.PullRequest == nil || slices.Contains(s.j.PullRequest.Changed, path)) &&
		s.h.secondary().Match(path) == ""
}

func (s *roleStage) javaAPIExcerpts(entry source.Entry, content string, remaining, limit int) []javaAPIExcerpt {
	if limit <= 0 || remaining <= 0 {
		return nil
	}
	lines := strings.Split(content, "\n")
	type candidate struct {
		javaproject.APIDocument
		score int
	}
	var options []candidate
	for _, api := range javaproject.APIDocuments(entry.Path, content) {
		if api.End-api.Start+1 > roleMaxDocLines {
			continue
		}
		score := 0
		for _, line := range lines[api.Start-1 : api.End] {
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
			score += documentationLineWeight(text)
			switch {
			case strings.HasPrefix(text, "@return"), strings.HasPrefix(text, "@throws"), strings.HasPrefix(text, "@exception"):
				score += 6
			case strings.HasPrefix(text, "@param"):
				score += 2
			}
		}
		options = append(options, candidate{api, score})
	}
	slices.SortStableFunc(options, func(a, b candidate) int { return b.score - a.score })
	var out []javaAPIExcerpt
	for _, api := range options {
		if len(out) == limit {
			break
		}
		x, ok := s.boundedExcerpt(entry, "document", "", api.Start, lines[api.Start-1:api.End], min(remaining, roleMaxDocBytes))
		if !ok || x.End != api.End {
			continue
		}
		out = append(out, javaAPIExcerpt{excerpt: x, signature: api.Signature})
		remaining -= record.JSONLen(x.Text)
	}
	return out
}

// Re-read the declaration that owns the exact cited span. Method names alone
// cannot choose an overload or a class. An abstract declaration has no
// implementation to include.
func documentedJavaImplementation(e source.Entry, content string, q witnessQuery) contractWitness {
	cited := q.javaDocs[e.Path]
	if len(cited) == 0 || !record.ScopeUnder(e.Path, q.scope) {
		return contractWitness{}
	}
	lines := strings.Split(content, "\n")
	var picked *javaproject.APIDocument
	for _, doc := range javaproject.APIDocuments(e.Path, content) {
		if !slices.ContainsFunc(cited, func(x record.Excerpt) bool { return x.Start == doc.Start && x.End == doc.End && x.SHA256 == e.SHA256 }) {
			continue
		}
		if picked != nil {
			return contractWitness{}
		}
		picked = &doc
	}
	if picked == nil || picked.BodyStart <= picked.End || picked.BodyEnd < picked.BodyStart {
		return contractWitness{}
	}
	end := min(picked.BodyEnd, picked.BodyStart+witnessMaxLines-1)
	for end >= picked.BodyStart && !fitsWitness(lines, picked.BodyStart, end) {
		end--
	}
	if end < picked.BodyStart {
		return contractWitness{}
	}
	return contractWitness{entry: e, line: picked.BodyStart, end: end, score: 4 * witnessMinScore, direct: true}
}

// Use only same-class implementations reached by a call inside the exact
// cited declaration. Later windows elsewhere in the file have no such link.
func documentedJavaCalls(e source.Entry, text string, w contractWitness, terms map[string]int) []contractCall {
	lines := strings.Split(text, "\n")
	var calls []contractCall
	local := javaLocalCalls(e.Path, lines, javaTokens(text))
	rankJavaCalls(local, lines, terms)
	for _, call := range local {
		if call.caller.start < w.line || call.caller.end > w.end {
			continue
		}
		calls = append(calls, contractCall{
			caller:    contractWitness{entry: e, line: call.caller.start},
			callee:    contractWitness{entry: e, line: call.callee.start},
			callerEnd: call.caller.end, calleeEnd: call.callee.end, symbol: call.symbol,
		})
	}
	return calls
}

func rankJavaCalls(calls []sourceCall, lines []string, terms map[string]int) {
	ranks := map[sourceCall]int{}
	for _, call := range calls {
		caller := strings.Join(lines[call.caller.start-1:call.caller.end], " ")
		callee := strings.Join(lines[call.callee.start-1:call.callee.end], " ")
		ranks[call] = 2*witnessMatch(witnessTerms(caller), terms) + witnessMatch(witnessTerms(callee), terms)
	}
	slices.SortStableFunc(calls, func(a, b sourceCall) int { return ranks[b] - ranks[a] })
}

type javaAPIBinding struct {
	path        string
	first, last int
	calls       []sourceCall
}

// Record the method body that owns each cited Javadoc from the same verified
// snapshot. The caller can then reject a comparison citing another overload
// even when both methods have identical names in the same file.
func (s *roleStage) javaAPIBindings(documents []record.Excerpt) (map[string]javaAPIBinding, error) {
	var out map[string]javaAPIBinding
	parsed := map[string][]javaproject.APIDocument{}
	local := map[string][]sourceCall{}
	var files map[string]source.Entry
	for _, doc := range documents {
		if !doc.IsJavaAPIDocument() {
			continue
		}
		if out == nil {
			out = map[string]javaAPIBinding{}
			files = s.entries()
		}
		out[doc.ID] = javaAPIBinding{}
		if !s.javaAPIScope(doc.Path) {
			continue
		}
		if _, cached := parsed[doc.Path]; !cached {
			entry, present := files[doc.Path]
			if !present || entry.SHA256 != doc.SHA256 {
				continue
			}
			content, ok, err := s.readSource(entry)
			if err != nil {
				return nil, err
			}
			parsed[doc.Path] = nil
			if ok {
				parsed[doc.Path] = javaproject.APIDocuments(doc.Path, content)
				if len(parsed[doc.Path]) > 0 {
					local[doc.Path] = javaLocalCalls(doc.Path, strings.Split(content, "\n"), javaTokens(content))
				}
			}
		}
		for _, api := range parsed[doc.Path] {
			if api.Start == doc.Start && api.End == doc.End {
				binding := javaAPIBinding{path: doc.Path, first: api.BodyStart, last: api.BodyEnd}
				for _, call := range local[doc.Path] {
					if call.caller.start >= binding.first && call.caller.end <= binding.last {
						binding.calls = append(binding.calls, call)
					}
				}
				out[doc.ID] = binding
				break
			}
		}
	}
	return out, nil
}

func (b javaAPIBinding) contains(excerpt record.Excerpt) bool {
	return b.first > 0 && excerpt.Kind == "code" && excerpt.Path == b.path && excerpt.Start >= b.first && excerpt.End <= b.last
}

// A follow-up can add a helper reached from the bound method. The final
// comparison still cites the documented method's implementation itself.
func (b javaAPIBinding) supports(excerpt record.Excerpt) bool {
	return b.contains(excerpt) || excerpt.Kind == "code" && slices.ContainsFunc(b.calls, func(call sourceCall) bool {
		return excerpt.Path == call.callee.path && excerpt.Start >= call.callee.start && excerpt.End <= call.callee.end
	})
}

func (s *roleStage) javaContractWindows(scope, statement string, documents []record.Excerpt, bindings map[string]javaAPIBinding,
	files map[string]source.Entry) ([]record.Excerpt, error) {
	var out []record.Excerpt
	for _, doc := range documents {
		binding := bindings[doc.ID]
		if binding.first <= 0 || !record.ScopeUnder(binding.path, scope) || len(binding.calls) == 0 {
			continue
		}
		entry := files[binding.path]
		content, ok, err := s.readSource(entry)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		lines := strings.Split(content, "\n")
		add := func(first, last int) {
			x, ok := s.boundedExcerpt(entry, "code", "context", first, lines[first-1:last], witnessMaxExcerpt)
			if ok && !slices.ContainsFunc(out, func(e record.Excerpt) bool { return e.ID == x.ID }) {
				out = append(out, x)
			}
		}
		add(binding.first, min(binding.last, binding.first+witnessMaxLines-1))
		terms := map[string]int{}
		addWitnessWords(terms, statement, 4)
		addWitnessWords(terms, doc.Text, 2)
		calls := slices.Clone(binding.calls)
		rankJavaCalls(calls, lines, terms)
		for _, call := range calls[:min(len(calls), maxContractEntryPoints)] {
			if !rangesContain(out, call.caller) {
				add(call.caller.start, call.caller.end)
			}
			add(call.callee.start, call.callee.end)
		}
	}
	return out, nil
}

func javaContractDirectCalls(bindings map[string]javaAPIBinding, documents, excerpts []record.Excerpt) []directCall {
	find := func(loc sourceCallLocation) string {
		for _, e := range excerpts {
			if e.Kind == "code" && e.Path == loc.path && e.Start <= loc.start && loc.end <= e.End {
				return e.ID
			}
		}
		return ""
	}
	var links []directCall
	for _, doc := range documents {
		for _, call := range bindings[doc.ID].calls {
			caller, callee := find(call.caller), find(call.callee)
			if caller == "" || callee == "" || caller == callee {
				continue
			}
			link := directCall{CallerEvidenceID: caller, CalleeEvidenceID: callee, Symbol: call.symbol}
			if !slices.Contains(links, link) {
				links = append(links, link)
			}
		}
	}
	return links
}

func javaBindingSupports(bindings map[string]javaAPIBinding, excerpt record.Excerpt) bool {
	for _, binding := range bindings {
		if binding.supports(excerpt) {
			return true
		}
	}
	return false
}

func javaAPIComparisonValid(comparison record.ContractComparison, bindings map[string]javaAPIBinding, catalog map[string]record.Excerpt) bool {
	if comparison.Status == "unknown" || len(bindings) == 0 {
		return true
	}
	for _, id := range comparison.DocumentEvidenceIDs {
		binding, ok := bindings[id]
		if !ok {
			continue
		}
		if binding.first == 0 || !slices.ContainsFunc(comparison.CodeEvidenceIDs, func(codeID string) bool {
			return binding.contains(catalog[codeID])
		}) {
			return false
		}
	}
	return true
}
