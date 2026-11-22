package review

import (
	"go/scanner"
	"go/token"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// contractAnchorInput refers only to windows actually included in the request.
// It is an ordering aid, never a stored comparison or a source of verdicts.
type contractAnchorInput struct {
	ContractIndex int      `json:"contract_index"`
	EvidenceIDs   []string `json:"evidence_ids"`
}

type contractAllocation struct {
	windows []record.Excerpt
	anchors []contractAnchorInput
	keep    int
}

func (a contractAllocation) submitted(list []record.Excerpt) []contractAnchorInput {
	var out []contractAnchorInput
	for _, anchor := range a.anchors {
		complete := true
		for _, id := range anchor.EvidenceIDs {
			if !slices.ContainsFunc(list, func(e record.Excerpt) bool { return e.ID == id }) {
				complete = false
				break
			}
		}
		if complete {
			out = append(out, anchor)
		}
	}
	return out
}

func contractClaimCandidates(matches [][2][]contractWitness) [][]contractWitness {
	const maxAlternatives = 4
	out := make([][]contractWitness, len(matches))
	for i, list := range matches {
		for _, w := range list[0] {
			if w.documented && w.direct && !w.compileBranch && contractRuntimePath(w.entry.Path, "code") {
				out[i] = append(out[i], w)
				if len(out[i]) == maxAlternatives {
					break
				}
			}
		}
	}
	return out
}

// contractClaimAllocation tries complete groups before adding ordinary review
// context. It reuses the witness scan, and only replaces the ordinary order
// when at least two different document contracts have admissible code.
func (s *roleStage) contractClaimAllocation(base []record.Excerpt, selected contractSelection, workflow []workflowLink, doc record.Record) contractAllocation {
	if len(doc.Contracts) < 2 || len(selected.queries) == 0 {
		return contractAllocation{}
	}
	const (
		typedField = iota
		typedGuard
		boundRoute
		exactName
	)
	type alternative struct {
		windows []record.Excerpt
		source  int
	}
	groups := make([][]alternative, len(doc.Contracts))
	add := func(index int, group []record.Excerpt, source int) {
		if index < 1 || index > len(groups) || len(group) == 0 {
			return
		}
		for _, e := range group {
			if e.ID == "" || !record.ScopeUnder(e.Path, doc.Contracts[index-1].AppliesTo) {
				return
			}
		}
		groups[index-1] = append(groups[index-1], alternative{windows: group, source: source})
	}
	for _, trace := range selected.goJSONFields {
		kind := typedField
		if trace.guard {
			kind = typedGuard
		}
		windows := trace.core
		if len(windows) == 0 {
			windows = trace.excerpts
		}
		add(trace.contractIndex, windows, kind)
	}
	for _, trace := range selected.jsOptions {
		add(trace.index, trace.windows(s, selected.sources), typedGuard)
	}
	for _, link := range workflow {
		for _, index := range link.indices {
			// Keep the route's independent origin and implementation together.
			add(index, []record.Excerpt{link.route.Registration, link.route.Handler}, boundRoute)
		}
	}
	for i, q := range selected.queries {
		if i >= len(selected.claimWitnesses) || len(q.navigationNames) == 0 {
			continue
		}
		for _, w := range selected.claimWitnesses[i] {
			sourceText := selected.sources[w.entry.Path]
			lines := strings.Split(sourceText, "\n")
			if w.line < 1 || w.line > len(lines) {
				continue
			}
			end := min(len(lines), max(w.end, w.line+witnessMaxLines-1))
			x, ok := s.boundedExcerpt(w.entry, "code", "context", w.line, lines[w.line-1:end], witnessMaxExcerpt)
			if !ok || !strengthImplementation(x) || !containsClaimName(x, sourceText, q.navigationNames) {
				continue
			}
			add(q.contractIndex, []record.Excerpt{x}, exactName)
		}
	}
	var allocation contractAllocation
	// Structural links get a chance before lexical windows, and each
	// claim can admit only one group.
	for _, source := range []int{typedField, typedGuard, boundRoute, exactName} {
		for i, alternatives := range groups {
			if slices.ContainsFunc(allocation.anchors, func(x contractAnchorInput) bool { return x.ContractIndex == i+1 }) {
				continue
			}
			for _, group := range alternatives {
				if group.source != source {
					continue
				}
				windows, ids, ok := admitClaimWindows(allocation.windows, group.windows, roleMaxCodeFiles-1)
				if ok {
					allocation.windows = windows
					allocation.anchors = append(allocation.anchors, contractAnchorInput{ContractIndex: i + 1, EvidenceIDs: ids})
					break
				}
			}
		}
	}
	if len(allocation.anchors) < 2 {
		return contractAllocation{}
	}
	allocation.keep = len(allocation.windows)
	for _, e := range base {
		if windows, _, ok := admitClaimWindows(allocation.windows, []record.Excerpt{e}, roleMaxCodeFiles); ok {
			allocation.windows = windows
		}
	}
	return allocation
}

func containsClaimName(e record.Excerpt, sourceText string, names []string) bool {
	switch record.SourceEcosystem(e.Path) {
	case "go":
		// Start at the file boundary so a window that starts in a raw
		// string or a block comment cannot be mistaken for Go syntax.
		file := token.NewFileSet().AddFile(e.Path, -1, len(sourceText))
		var scan scanner.Scanner
		scan.Init(file, []byte(sourceText), nil, 0)
		for pos, kind, literal := scan.Scan(); kind != token.EOF; pos, kind, literal = scan.Scan() {
			line := file.Line(pos)
			if line > e.End {
				break
			}
			if line >= e.Start && kind == token.IDENT && slices.Contains(names, literal) {
				return true
			}
		}
	case "python", "javascript":
		return slices.ContainsFunc(callTokens(e.Text, callLanguage(e.Path) == "python"), func(t callToken) bool {
			return t.name && slices.Contains(names, t.text)
		})
	default:
		// Comments and literals in C and Rust are not name anchors.
		ecosystem := record.SourceEcosystem(e.Path)
		if ecosystem == "cpp" {
			ecosystem = "c"
		}
		code := contractExecutableSource(sourceText, ecosystem)
		lines := strings.Split(code, "\n")
		return slices.ContainsFunc(names, func(name string) bool {
			return containsContractLiteral(strings.Join(lines[e.Start-1:e.End], "\n"), name)
		})
	}
	return false
}

// admitClaimWindows preserves overlapping sources without double-sending an
// identical or contained range. A partially overlapping range cannot replace
// already assigned evidence. A group is applied atomically if it all fits.
func admitClaimWindows(existing, group []record.Excerpt, limit int) ([]record.Excerpt, []string, bool) {
	windows := slices.Clone(existing)
	var ids []string
	for _, x := range group {
		id := x.ID
		if at := slices.IndexFunc(windows, func(old record.Excerpt) bool {
			return old.Path == x.Path && old.SHA256 == x.SHA256 && old.Start <= x.Start && x.End <= old.End
		}); at >= 0 {
			id = windows[at].ID
		} else {
			remain := roleMaxCodeBytes
			for _, e := range windows {
				remain -= record.JSONLen(e.Text)
			}
			additional, ok := wholeTraceWindows(windows, []record.Excerpt{x}, remain, limit)
			if !ok {
				return nil, nil, false
			}
			windows = append(windows, additional...)
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return windows, ids, true
}

// Fit the complete anchored prefix using the real serialized request budget.
// The caller keeps the existing excerpt order if this alternative cannot fit.
func (s *roleStage) fitContractAllocation(in investigateInput, links corpusLinks, documents []record.Excerpt) ([]record.Excerpt, bool) {
	allocation := links.claims
	build := func(list []record.Excerpt) ([]byte, error) {
		in.Scope = in.Scope.withExcerpts(list, links)
		in.Scope.ContractAnchors = allocation.submitted(list)
		return marshal(in)
	}
	for _, x := range allocation.windows {
		if slices.ContainsFunc(documents, func(d record.Excerpt) bool { return d.ID == x.ID }) {
			return nil, false
		}
	}
	windows, _, err := s.fitExcerpts(s.instructions(investigateContract), allocation.keep, allocation.windows, build)
	if err != nil {
		return nil, false
	}
	return windows, true
}
