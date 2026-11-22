package review

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// sourceCallContext adds the other end of a bound call when an accepted
// excerpt contains the call site or intersects the implementation. It never
// widens the contract or PR scope to complete the connection.
func (s *roleStage) sourceCallContext(scope string, anchors, already []record.Excerpt, limit, budget int) ([]record.Excerpt, error) {
	if limit <= 0 || budget <= 0 {
		return nil, nil
	}
	g := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	catalog := s.callCatalog()
	files := catalog.files
	checked := map[string]bool{}
	eligible := func(path string) bool {
		if ok, present := checked[path]; present {
			return ok
		}
		ok := record.ScopeUnder(path, scope) && s.navigationEligible(g, path)
		checked[path] = ok
		return ok
	}
	anchorFiles := map[string][]record.Excerpt{}
	langs := map[string]bool{}
	var order []string
	for _, x := range anchors {
		if (x.Kind != "code" && x.Kind != "test") || !eligible(x.Path) ||
			files[x.Path].SHA256 != x.SHA256 {
			continue
		}
		if len(anchorFiles[x.Path]) == 0 {
			order = append(order, x.Path)
		}
		anchorFiles[x.Path] = append(anchorFiles[x.Path], x)
		langs[record.SourceEcosystem(x.Path)] = true
	}
	if len(order) == 0 {
		return nil, nil
	}
	for e := range g.orderedFiles() {
		if len(anchorFiles[e.Path]) == 0 && langs[record.SourceEcosystem(e.Path)] && windowKind(e.Path) != "" &&
			eligible(e.Path) {
			order = append(order, e.Path)
		}
	}
	var scannedBytes int64
	scanned := map[string]bool{}
	readable := map[string]string{}
	index := catalog.calls(func(e source.Entry) (string, bool, error) {
		if err := s.ctx.Err(); err != nil {
			return "", false, err
		}
		if scanned[e.Path] {
			t, ok := readable[e.Path]
			return t, ok, nil
		}
		if !callPathMetadata(e.Path) && !eligible(e.Path) {
			return "", false, nil
		}
		if len(scanned) >= maxScanFiles || *e.Size > maxScanBytes-scannedBytes {
			return "", false, nil
		}
		scanned[e.Path] = true
		scannedBytes += *e.Size
		t, ok, err := s.readSource(e)
		if ok {
			readable[e.Path] = t
		}
		return t, ok, err
	})
	var out []record.Excerpt
	for _, p := range order {
		if len(out) >= limit || budget <= 0 || len(scanned) >= maxScanFiles {
			break
		}
		calls, err := index.from(p, scope)
		if err != nil {
			return nil, err
		}
		for _, link := range calls {
			var target sourceCallLocation
			origin := "context"
			definition := link.calleeExtent
			if definition.start == 0 {
				definition = link.callee
			}
			switch {
			case rangesContain(anchorFiles[link.caller.path], link.caller):
				target = link.callee
			case slices.ContainsFunc(anchorFiles[definition.path], func(x record.Excerpt) bool {
				return x.Start <= definition.end && definition.start <= x.End
			}):
				target, origin = link.caller, "caller"
			default:
				continue
			}
			if rangesContain(already, target) || rangesContain(out, target) {
				continue
			}
			lines := strings.Split(readable[target.path], "\n")
			if target.end > len(lines) || target.start < 1 || target.end < target.start {
				continue
			}
			start, end := target.start, target.end
			if origin == "caller" {
				start, end = max(1, start-3), min(len(lines), end+3)
			}
			kind := windowKind(target.path)
			if kind == "test" {
				origin = "test"
			}
			if kind != "code" && kind != "test" {
				continue
			}
			x, ok := s.boundedExcerpt(files[target.path], kind, origin, start, lines[start-1:end], min(roleMaxWindowBytes, budget))
			if !ok || x.End < target.end {
				x, ok = s.boundedExcerpt(files[target.path], kind, origin, target.start,
					lines[target.start-1:target.end], min(roleMaxWindowBytes, budget))
			}
			if !ok || x.End < target.end || record.JSONLen(x.Text) > budget {
				continue
			}
			if record.ValidEvidenceSymbol(link.symbol) && (origin == "caller" || origin == "test") {
				x.Symbol = link.symbol
			}
			out = append(out, x)
			budget -= record.JSONLen(x.Text)
			if len(out) == limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func rangesContain(excerpts []record.Excerpt, loc sourceCallLocation) bool {
	return slices.ContainsFunc(excerpts, func(x record.Excerpt) bool {
		return x.Path == loc.path && x.Start <= loc.start && loc.end <= x.End
	})
}
