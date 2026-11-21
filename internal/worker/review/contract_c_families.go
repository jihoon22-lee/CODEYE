package review

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/docpath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	cFamilyGuideBytes = 64 << 10
	cFamilyGuideLimit = 2
	cFamilyNamesLimit = 8
)

type cAPIFamily struct{ qualified, base string }

type cFamilyGuideCache struct {
	selection *docpath.Selection
	calls     map[string][]string
}

var (
	cQualifier    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{2,31}$`)
	cExampleCall  = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)[ \t]*\(`)
	cSimpleReturn = regexp.MustCompile(`^\{\s*return\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(([A-Za-z0-9_,\s]+)\)\s*;\s*\}$`)
	cSimpleParam  = regexp.MustCompile(`^([ A-Za-z_0-9*\t\n]+[\s*])([A-Za-z_][A-Za-z0-9_]*)$`)
	cElseBlock    = regexp.MustCompile(`^\s*else\s*\{`)
)

// Read only the same eligible product guide already cited for the qualifier.
// A fenced C usage supplies exact candidate names without guessing a prefix.
func (s *roleStage) documentedCFamilies(
	cache *cFamilyGuideCache, file source.Entry, raw string, names []string, prior []cAPIFamily,
) ([]cAPIFamily, error) {
	var suffixes []string
	marked := witnessCode.FindAllStringSubmatch(raw, -1)
	for _, name := range names {
		if cQualifier.MatchString(name) && slices.ContainsFunc(marked, func(match []string) bool { return match[1] == name }) {
			suffixes = append(suffixes, name)
		}
	}
	if len(suffixes) == 0 || len(prior) >= cFamilyNamesLimit || file.Size == nil || *file.Size > cFamilyGuideBytes ||
		!inScope(file.Path, normalizedScope(s.j.Scope)) || s.h.secondary().Match(file.Path) != "" ||
		s.j.PullRequest != nil && !slices.Contains(s.j.PullRequest.Changed, file.Path) {
		return prior, nil
	}
	if cache.selection == nil {
		paths := make([]string, 0, len(s.j.Snapshot.Manifest.Entries))
		for _, e := range s.j.Snapshot.Manifest.Entries {
			if e.Kind == "file" && e.ExcludedReason == "" && docpath.Classify(e.Path) != "" {
				paths = append(paths, e.Path)
			}
		}
		cache.selection = docpath.NewSelection(paths)
		cache.calls = map[string][]string{}
	}
	if !cache.selection.Product(file.Path, docpath.Classify(file.Path)) {
		return prior, nil
	}
	calls, ok := cache.calls[file.Path]
	if !ok {
		if len(cache.calls) == cFamilyGuideLimit {
			return prior, nil
		}
		text, usable, err := s.readSource(file)
		if err != nil {
			return nil, err
		}
		if usable {
			calls = cGuideCalls(text)
		}
		cache.calls[file.Path] = calls
	}
	for _, call := range calls {
		for _, suffix := range suffixes {
			base, ok := strings.CutSuffix(call, suffix)
			candidate := cAPIFamily{call, strings.TrimSuffix(base, "_")}
			if ok && candidate.base != "" && !slices.Contains(prior, candidate) {
				prior = append(prior, candidate)
				if len(prior) == cFamilyNamesLimit {
					return prior, nil
				}
			}
		}
	}
	return prior, nil
}

func cGuideCalls(text string) []string {
	var fence string
	var block strings.Builder
	var out []string
	seen := map[string]bool{}
	c := false
	for raw := range strings.Lines(text) {
		line := strings.TrimSpace(raw)
		switch {
		case fence != "" && strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == "":
			if c {
				for masked := range strings.Lines(contractExecutableSource(block.String(), "c")) {
					for _, match := range cExampleCall.FindAllStringSubmatchIndex(masked, -1) {
						name := masked[match[2]:match[3]]
						prefix := strings.TrimSpace(masked[:match[0]])
						if !strings.HasPrefix(prefix, "#") && (prefix == "" || strings.ContainsAny(prefix, "=([!,") || prefix == "return") &&
							record.ValidContractLiteral(name) && !seen[name] {
							out = append(out, name)
							seen[name] = true
						}
					}
				}
			}
			fence, c = "", false
			block.Reset()
		case fence != "":
			if c {
				block.WriteString(raw)
			}
		case strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~"):
			fence = line[:3]
			c = strings.EqualFold(strings.TrimSpace(line[3:]), "c")
		}
	}
	return out
}

type cFamilyWitness struct {
	family  cAPIFamily
	windows []contractWitness
}

type cFamilyCatalog struct {
	definitions map[string]map[string][]cDefinition
	matches     [][]cFamilyWitness
}

func (m *cFamilyCatalog) observe(e source.Entry, text string, queries []witnessQuery) {
	if path.Ext(e.Path) != ".c" || windowKind(e.Path) != "code" {
		return
	}
	wanted := map[string]bool{}
	for _, q := range queries {
		if record.ScopeUnder(e.Path, q.scope) {
			for _, f := range q.cFamilies {
				wanted[f.base], wanted[f.qualified] = true, true
			}
		}
	}
	if len(wanted) == 0 {
		return
	}
	if m.definitions == nil {
		m.definitions = map[string]map[string][]cDefinition{}
		m.matches = make([][]cFamilyWitness, len(queries))
	}
	defs := map[string][]cDefinition{}
	for _, d := range cDefinitions(text, wanted) {
		defs[d.name] = append(defs[d.name], d)
	}
	m.definitions[e.Path] = defs
	for i, q := range queries {
		if !record.ScopeUnder(e.Path, q.scope) {
			continue
		}
		for _, family := range q.cFamilies {
			base, qualified := defs[family.base], defs[family.qualified]
			if len(base) != 1 || len(qualified) != 1 {
				continue
			}
			if windows := cFamilyWindows(e, text, base[0], qualified[0]); len(windows) > 0 {
				m.matches[i] = append(m.matches[i], cFamilyWitness{family, windows})
			}
		}
	}
}

func (m *cFamilyCatalog) choose(queries []witnessQuery) []contractWitness {
	for i, candidates := range m.matches {
		var possible [][]contractWitness
		for _, candidate := range candidates {
			base, qualified := 0, 0
			for p, defs := range m.definitions {
				if record.ScopeUnder(p, queries[i].scope) {
					base += len(defs[candidate.family.base])
					qualified += len(defs[candidate.family.qualified])
				}
			}
			if base == 1 && qualified == 1 {
				possible = append(possible, candidate.windows)
			}
		}
		if len(possible) == 1 {
			return possible[0]
		}
	}
	return nil
}

type cParameter struct{ name, layout string }

func cParameters(text string) ([]cParameter, bool) {
	if strings.TrimSpace(text) == "void" {
		return nil, true
	}
	var out []cParameter
	for arg := range strings.SplitSeq(text, ",") {
		parts := cSimpleParam.FindStringSubmatch(strings.TrimSpace(arg))
		if parts == nil || strings.TrimSpace(parts[1]) == "" {
			return nil, false
		}
		out = append(out, cParameter{parts[2], cTypeLayout(parts[1])})
	}
	return out, true
}

func cTypeLayout(text string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(text, "*", " * ")), " ")
}

func cForwardedMode(call []string, params []cParameter) (position int, enabled, ok bool) {
	if len(call) != len(params)+1 {
		return 0, false, false
	}
	position = -1
	index := 0
	for i, raw := range call {
		arg := strings.TrimSpace(raw)
		if index < len(params) && arg == params[index].name {
			index++
			continue
		}
		if position >= 0 {
			return 0, false, false
		}
		position = i
		switch arg {
		case "true", "false":
			enabled = arg == "true"
		default:
			n, err := strconv.ParseInt(arg, 0, 64)
			if err != nil {
				return 0, false, false
			}
			enabled = n != 0
		}
	}
	return position, enabled, position >= 0 && index == len(params)
}

func cFamilyWindows(e source.Entry, text string, a, b cDefinition) []contractWitness {
	first, last := min(a.start, b.start), max(a.end, b.end)
	if a.conditional || b.conditional || first == last || cTypeLayout(a.returnType) != cTypeLayout(b.returnType) {
		return nil
	}
	lines := strings.Split(text, "\n")
	if a.start > b.start {
		a, b = b, a
	}
	if a.end >= b.start || !fitsWitness(lines, first, last) ||
		strings.TrimSpace(contractExecutableSource(strings.Join(lines[a.end:b.start-1], "\n"), "c")) != "" {
		return nil
	}
	ap, apOK := cParameters(a.parameters)
	bp, bpOK := cParameters(b.parameters)
	ac, bc := cSimpleReturn.FindStringSubmatch(a.body), cSimpleReturn.FindStringSubmatch(b.body)
	if !apOK || !bpOK || !slices.EqualFunc(ap, bp, func(a, b cParameter) bool { return a.layout == b.layout }) ||
		ac == nil || bc == nil || ac[1] != bc[1] {
		return nil
	}
	m, mode, aOK := cForwardedMode(strings.Split(ac[2], ","), ap)
	n, otherMode, bOK := cForwardedMode(strings.Split(bc[2], ","), bp)
	if !aOK || !bOK || m != n || mode == otherMode {
		return nil
	}
	helpers := namedCDefinitions(text, map[string]bool{ac[1]: true}, true)
	if len(helpers) != 1 || !cStatic(helpers[0].returnType) || helpers[0].conditional {
		return nil
	}
	helper := helpers[0]
	hp, hpOK := cParameters(helper.parameters)
	if !hpOK || len(hp) != len(ap)+1 {
		return nil
	}
	for i, param := range ap {
		j := i
		if j >= m {
			j++
		}
		if param.layout != hp[j].layout {
			return nil
		}
	}
	branchStart, branchEnd := cModeBranch(helper, hp[m].name)
	if branchStart == 0 || !fitsWitness(lines, branchStart, branchEnd) {
		return nil
	}
	return []contractWitness{{entry: e, line: first, end: last}, {entry: e, line: branchStart, end: branchEnd}}
}

func cModeBranch(d cDefinition, name string) (first, last int) {
	pattern := regexp.MustCompile(`(?m)^[ \t]*if[ \t]*\([ \t]*!?[ \t]*` + regexp.QuoteMeta(name) + `[ \t]*\)\s*\{`)
	for _, found := range pattern.FindAllStringIndex(d.body, -1) {
		before := d.body[:found[0]]
		if strings.Count(before, "{")-strings.Count(before, "}") != 1 {
			continue
		}
		if containsContractLiteral(before, name) {
			return 0, 0
		}
		body := []byte(d.body)
		then := matchingCDelimiter(body, found[1]-1, '{', '}')
		if then < 0 || first != 0 {
			return 0, 0
		}
		other := cElseBlock.FindIndex(body[then:])
		if other == nil {
			return 0, 0
		}
		end := matchingCDelimiter(body, then+other[1]-1, '{', '}')
		if end < 0 {
			return 0, 0
		}
		first, last = d.bodyLine+strings.Count(before, "\n"), d.bodyLine+strings.Count(d.body[:end], "\n")
	}
	return first, last
}
