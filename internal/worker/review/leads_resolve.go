package review

import (
	"errors"
	"go/ast"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Resolving the leads of an investigation (docs/REVIEW.md#단서-후속-조사).
//
// A lead starts from sent code lines and a symbol. The platform finds what
// the lead needs in the pinned snapshot with the review's source resolvers:
// the call bindings of sourceCalls (Go across packages, Java, C and C++
// headers, Rust modules, Python and JavaScript imports), the Go package's
// own declarations and the Go caller expansion (expandWithin). It reads only
// files the run may send: in scope, inside a pull request's changed files,
// not withheld by the secrets screen, not generated and not on a secondary
// path, each blob checked against the manifest (readVerified). Every lead
// gets its own scan within maxScanFiles and maxScanBytes. A location the
// investigate request already carried is named by that excerpt's ID and not
// sent again; two declarations the lead could mean are never chosen
// between. Each found location becomes one window of at most
// record.MaxLeadWindowLines lines and maxWindowBytes bytes, within the
// follow-up's excerpt and byte caps (limits.leadExcerpts, leadBytes).

// leadResolver resolves the leads of one investigation.
type leadResolver struct {
	s       *roleStage
	g       codeGroups
	catalog *sourceCallCatalog
	// sent are the code and test excerpts the investigate request carried,
	// with their text.
	sent      []record.Excerpt
	sentPaths map[string]bool
	// excerpts and bytes are what the follow-up's caps leave.
	excerpts, bytes int
}

func (s *roleStage) newLeadResolver(sent []record.Excerpt) *leadResolver {
	r := &leadResolver{s: s, g: planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j)),
		catalog: s.callCatalog(), sentPaths: map[string]bool{}, excerpts: s.limit().leadExcerpts, bytes: s.limit().leadBytes}
	for _, e := range sent {
		if e.Text != "" && (e.Kind == "code" || e.Kind == "test") {
			r.sent = append(r.sent, e)
			r.sentPaths[e.Path] = true
		}
	}
	return r
}

// blocked is why a file may not be read for a lead or sent as one of its
// excerpts; "" when it may.
func (r *leadResolver) blocked(p string) string {
	switch {
	case r.s.secretFiles()[p] || record.Sensitive([]byte(p)):
		return record.LeadWithheld
	case !inScope(p, normalizedScope(r.s.j.Scope)):
		return record.LeadOutsideScope
	}
	if _, ok := r.g.rank[p]; !ok {
		return record.LeadOutsideScope
	}
	switch {
	case r.g.tierOf(p) != tierChanged:
		return record.LeadOutsideChange
	case r.g.secondary[p] != "":
		return record.LeadSecondary
	case minifiedSource(p):
		return record.LeadGenerated
	}
	return ""
}

// leadScan is the bounded read of one lead's resolution.
type leadScan struct {
	r        *leadResolver
	files    int
	bytes    int64
	texts    map[string]string
	readable map[string]bool
	// refused holds the files the scan did not read and why; limited is set
	// once the scan reached its file or byte limit.
	refused map[string]string
	limited bool
	index   *sourceCalls
}

func (r *leadResolver) scan() *leadScan {
	l := &leadScan{r: r, texts: map[string]string{}, readable: map[string]bool{}, refused: map[string]string{}}
	l.index = r.catalog.calls(l.read)
	return l
}

func (l *leadScan) read(e source.Entry) (string, bool, error) {
	if err := l.r.s.ctx.Err(); err != nil {
		return "", false, err
	}
	if ok, seen := l.readable[e.Path]; seen {
		return l.texts[e.Path], ok, nil
	}
	// A file the investigation sent may be read again to find what its
	// lines call or define; go.mod and Cargo.toml give module identity.
	if !callPathMetadata(e.Path) && !l.r.sentPaths[e.Path] {
		if reason := l.r.blocked(e.Path); reason != "" {
			l.refused[e.Path] = reason
			return "", false, nil
		}
	}
	if l.files >= maxScanFiles || *e.Size > maxScanBytes-l.bytes {
		l.limited = true
		return "", false, nil
	}
	l.files++
	l.bytes += *e.Size
	l.readable[e.Path] = false
	text, reason, err := l.r.s.reviewSource(e)
	if err != nil {
		return "", false, err
	}
	switch {
	case reason == record.ScopeReasonWithheld:
		l.refused[e.Path] = record.LeadWithheld
		return "", false, nil
	case reason == record.ScopeReasonTooLarge:
		l.limited = true
		return "", false, nil
	case reason != "":
		return "", false, nil
	case record.GeneratedHeader([]byte(text)) && !callPathMetadata(e.Path):
		l.refused[e.Path] = record.LeadGenerated
	}
	l.texts[e.Path], l.readable[e.Path] = text, true
	return text, true, nil
}

// missing is the reason of a lead the scan found nothing for: a file it
// was not allowed to read, then its limit, then notFound.
func (l *leadScan) missing(notFound string) string {
	for _, reason := range []string{record.LeadWithheld, record.LeadOutsideChange, record.LeadOutsideScope, record.LeadGenerated, record.LeadSecondary} {
		for _, r := range l.refused {
			if r == reason {
				return reason
			}
		}
	}
	if l.limited {
		return record.LeadScanLimit
	}
	return notFound
}

// leadLanguage reports a source file whose leads a resolver reads.
func leadLanguage(p string) bool {
	switch path.Ext(p) {
	case ".go", ".java", ".c", ".cc", ".cpp", ".cxx", ".c++", ".rs":
		return true
	}
	return callLanguage(p) != ""
}

// symbolAt reports an identifier token equal to symbol in text.
func symbolAt(text, symbol string) bool {
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_$])` + regexp.QuoteMeta(symbol) + `($|[^A-Za-z0-9_$])`)
	return re.MatchString(text)
}

// leadHit is one location a resolution found.
type leadHit struct {
	loc    sourceCallLocation
	origin string
	symbol string
	// excerpt is the window when the resolver already built it (the Go
	// caller expansion).
	excerpt *record.Excerpt
}

// resolve resolves one lead from its anchor (the cited lines with text).
func (r *leadResolver) resolve(l record.Lead, anchor record.Excerpt) (record.Lead, error) {
	unresolved := func(reason string) (record.Lead, error) {
		l.Resolution, l.Reason, l.Targets = record.LeadUnresolved, reason, nil
		return l, nil
	}
	if !leadLanguage(anchor.Path) {
		return unresolved(record.LeadLanguage)
	}
	if !symbolAt(anchor.Text, l.Symbol) {
		return unresolved(record.LeadSymbolNotAtAnchor)
	}
	scan := r.scan()
	var hits []leadHit
	var reason string
	var err error
	if l.Need == record.LeadNeedDefinition {
		hits, reason, err = r.definitions(scan, l.Symbol, anchor)
	} else {
		hits, reason, err = r.callers(scan, l.Symbol, anchor)
	}
	if err != nil {
		if r.s.ctx.Err() != nil {
			return l, err
		}
		return unresolved(record.LeadReadFailed)
	}
	if reason != "" {
		return unresolved(reason)
	}
	var outside []leadHit
	var inRequest []string
	for _, h := range hits {
		if i := slices.IndexFunc(r.sent, func(x record.Excerpt) bool {
			return x.Path == h.loc.path && x.Start <= h.loc.start && h.loc.end <= x.End
		}); i >= 0 {
			if len(inRequest) < record.MaxLeadTargets {
				inRequest = addLim(inRequest, r.sent[i].ID)
			}
			continue
		}
		outside = append(outside, h)
	}
	if len(outside) == 0 {
		l.InRequest = inRequest
		return unresolved(record.LeadInRequest)
	}
	blockedBy, overCap := "", false
	for _, h := range outside {
		if len(l.Targets) == record.MaxLeadTargets {
			break
		}
		if why := r.blocked(h.loc.path); why != "" {
			blockedBy = cmpReason(blockedBy, why)
			continue
		}
		x, ok := r.window(scan, h)
		if !ok {
			continue
		}
		if r.excerpts <= 0 || record.JSONLen(x.Text) > r.bytes {
			overCap = true
			continue
		}
		if slices.ContainsFunc(l.Targets, func(t record.LeadTarget) bool { return t.ID == x.ID }) {
			continue
		}
		r.excerpts--
		r.bytes -= record.JSONLen(x.Text)
		l.Targets = append(l.Targets, record.LeadTarget{ID: x.ID, Path: x.Path, SHA256: x.SHA256, Start: x.Start, End: x.End, Kind: x.Kind, Origin: x.Origin, Symbol: x.Symbol})
	}
	switch {
	case len(l.Targets) > 0:
		l.Resolution, l.Reason, l.InRequest = record.LeadResolved, "", nil
		return l, nil
	case overCap:
		return unresolved(record.LeadOverCap)
	case blockedBy != "":
		return unresolved(blockedBy)
	case l.Need == record.LeadNeedDefinition:
		return unresolved(record.LeadDefinitionMissing)
	}
	return unresolved(record.LeadCallersMissing)
}

// cmpReason keeps the first blocking reason found.
func cmpReason(have, next string) string {
	if have != "" {
		return have
	}
	return next
}

// definitions finds the declaration of the symbol a call in the anchor
// lines binds to: a bound call of sourceCalls first, then for Go a function
// or method of the anchor's own package. More than one distinct
// declaration is ambiguous.
func (r *leadResolver) definitions(scan *leadScan, symbol string, anchor record.Excerpt) ([]leadHit, string, error) {
	links, err := scan.index.from(anchor.Path, ".")
	if err != nil {
		return nil, "", err
	}
	var hits []leadHit
	add := func(loc sourceCallLocation) {
		if !slices.ContainsFunc(hits, func(h leadHit) bool { return h.loc == loc }) {
			hits = append(hits, leadHit{loc: loc, origin: "context"})
		}
	}
	for _, link := range links {
		if link.symbol == symbol && rangesContain([]record.Excerpt{anchor}, link.caller) {
			add(link.callee)
		}
	}
	if len(hits) == 0 && strings.HasSuffix(anchor.Path, ".go") {
		locs, err := r.goPackageDeclarations(scan, anchor.Path, symbol)
		if err != nil {
			return nil, "", err
		}
		for _, loc := range locs {
			add(loc)
		}
	}
	switch {
	case len(hits) == 0:
		return nil, scan.missing(record.LeadDefinitionMissing), nil
	case len(hits) > 1:
		return nil, record.LeadAmbiguous, nil
	}
	return hits, "", nil
}

// goPackageDeclarations lists the functions and methods named symbol in the
// package of the Go file p (its test files too when p is a test).
func (r *leadResolver) goPackageDeclarations(scan *leadScan, p, symbol string) ([]sourceCallLocation, error) {
	own, err := scan.index.goFile(p)
	if err != nil || own.parsed == nil {
		return nil, err
	}
	pkg, dir := own.parsed.Name.Name, path.Dir(p)
	files := slices.Clone(r.catalog.dirs[dir])
	if strings.HasSuffix(p, "_test.go") {
		for _, q := range r.catalog.ordered {
			if path.Dir(q) == dir && strings.HasSuffix(q, "_test.go") {
				files = append(files, q)
			}
		}
	}
	var out []sourceCallLocation
	for _, q := range files {
		if goIgnored(q) {
			continue
		}
		f, err := scan.index.goFile(q)
		if err != nil {
			return nil, err
		}
		if f.parsed == nil || f.parsed.Name.Name != pkg {
			continue
		}
		for _, decl := range f.parsed.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == symbol && fn.Body != nil {
				out = append(out, sourceCallLocation{q, f.line(fn.Pos()), f.line(fn.End())})
			}
		}
	}
	return out, nil
}

// goDeclarationAt is the line range of the Go function or method named
// symbol whose declaration intersects the anchor lines.
func (r *leadResolver) goDeclarationAt(scan *leadScan, anchor record.Excerpt, symbol string) (sourceCallLocation, bool, error) {
	f, err := scan.index.goFile(anchor.Path)
	if err != nil || f.parsed == nil {
		return sourceCallLocation{}, false, err
	}
	for _, decl := range f.parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != symbol || fn.Body == nil {
			continue
		}
		first, last := f.line(fn.Pos()), f.line(fn.End())
		if first <= anchor.End && anchor.Start <= last {
			return sourceCallLocation{anchor.Path, first, last}, true, nil
		}
	}
	return sourceCallLocation{}, false, nil
}

// callers finds the call sites of the function the anchor lines define:
// the Go caller expansion over the anchor's package, then the bound calls
// of sourceCalls in the anchor's file and the other files of its language
// the run may read, in review order.
func (r *leadResolver) callers(scan *leadScan, symbol string, anchor record.Excerpt) ([]leadHit, string, error) {
	definition := sourceCallLocation{anchor.Path, anchor.Start, anchor.End}
	var hits []leadHit
	if strings.HasSuffix(anchor.Path, ".go") {
		decl, ok, err := r.goDeclarationAt(scan, anchor, symbol)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, record.LeadSymbolNotAtAnchor, nil
		}
		definition = decl
		// The caller expansion selects the functions whose declarations the
		// cited lines intersect: only this one.
		start, end := max(anchor.Start, decl.start), min(anchor.End, decl.end)
		lines := strings.Split(anchor.Text, "\n")
		narrow := record.Narrow(anchor, r.s.j.Snapshot.Digest, start, end)
		if narrow.Text == "" && start-anchor.Start < len(lines) {
			narrow.Text = strings.Join(lines[start-anchor.Start:min(len(lines), end-anchor.Start+1)], "\n")
		}
		var changed map[string]bool
		if r.s.j.PullRequest != nil {
			changed = map[string]bool{}
			for _, p := range r.s.j.PullRequest.Changed {
				changed[p] = true
			}
		}
		found, lims, err := expandWithin(r.s.ctx, r.s.j.Snapshot, normalizedScope(r.s.j.Scope), []record.Excerpt{narrow},
			[]record.CandidateText{{Evidence: []string{narrow.ID}}}, r.s.secretFiles(), changed, expansionCaps{2 * record.MaxLeadTargets, 2 * record.MaxLeadTargets * maxWindowBytes})
		if err != nil {
			return nil, "", err
		}
		if slices.Contains(lims, limCallersOutside) {
			scan.limited = true
		}
		for i := range found {
			if found[i].Symbol == symbol {
				x := found[i]
				hits = append(hits, leadHit{loc: sourceCallLocation{x.Path, x.Start, x.End}, origin: x.Origin, symbol: symbol, excerpt: &x})
			}
		}
	}
	order := []string{anchor.Path}
	lang := record.SourceEcosystem(anchor.Path)
	for e := range r.g.orderedFiles() {
		if e.Path != anchor.Path && record.SourceEcosystem(e.Path) == lang && windowKind(e.Path) != "" && r.blocked(e.Path) == "" {
			order = append(order, e.Path)
		}
	}
	for _, p := range order {
		if len(hits) >= 2*record.MaxLeadTargets || scan.files >= maxScanFiles {
			if scan.files >= maxScanFiles {
				scan.limited = true
			}
			break
		}
		links, err := scan.index.from(p, ".")
		if err != nil {
			return nil, "", err
		}
		for _, link := range links {
			callee := link.calleeExtent
			if callee.start == 0 {
				callee = link.callee
			}
			if link.symbol != symbol || callee.path != definition.path || callee.end < definition.start || definition.end < callee.start {
				continue
			}
			if slices.ContainsFunc(hits, func(h leadHit) bool {
				return h.loc.path == link.caller.path && h.loc.start <= link.caller.start && link.caller.end <= h.loc.end
			}) {
				continue
			}
			hits = append(hits, leadHit{loc: link.caller, origin: "caller", symbol: symbol})
		}
	}
	if len(hits) == 0 {
		return nil, scan.missing(record.LeadCallersMissing), nil
	}
	return hits, "", nil
}

// window is the excerpt of one hit: a definition from its first line, a
// call site with three lines around it, at most record.MaxLeadWindowLines
// lines and maxWindowBytes bytes, bound to the snapshot blob.
func (r *leadResolver) window(scan *leadScan, h leadHit) (record.Excerpt, bool) {
	kind := windowKind(h.loc.path)
	if kind != "code" && kind != "test" {
		return record.Excerpt{}, false
	}
	if h.excerpt != nil {
		x := *h.excerpt
		if x.End-x.Start+1 > record.MaxLeadWindowLines || record.JSONLen(x.Text) > maxWindowBytes {
			return record.Excerpt{}, false
		}
		return x, true
	}
	e, ok := r.catalog.files[h.loc.path]
	if !ok || e.Kind != "file" || e.Size == nil || e.SHA256 == "" || e.ExcludedReason != "" {
		return record.Excerpt{}, false
	}
	text, readable, err := scan.read(e)
	if err != nil || !readable {
		return record.Excerpt{}, false
	}
	lines := strings.Split(text, "\n")
	start, end := h.loc.start, h.loc.end
	origin := h.origin
	if origin == "caller" {
		start, end = max(1, start-3), min(len(lines), end+3)
	}
	if kind == "test" && origin == "caller" {
		origin = "test"
	}
	end = min(end, start+record.MaxLeadWindowLines-1, len(lines))
	if start < 1 || end < start {
		return record.Excerpt{}, false
	}
	x, ok := r.s.boundedExcerpt(e, kind, origin, start, lines[start-1:end], maxWindowBytes)
	if !ok || (origin != "context" && x.End < h.loc.end) {
		return record.Excerpt{}, false
	}
	if origin != "context" && record.ValidEvidenceSymbol(h.symbol) {
		x.Symbol = h.symbol
	}
	return x, true
}

// errLeadsCancelled: the attempt ended while the leads were resolved.
var errLeadsCancelled = errors.New("review_leads_cancelled")

// resolveLeads resolves an investigation's leads in order; anchors maps
// each lead's evidence ID to its cited lines with text.
func (s *roleStage) resolveLeads(leads []record.Lead, anchors map[string]record.Excerpt, sent []record.Excerpt) ([]record.Lead, error) {
	r := s.newLeadResolver(sent)
	out := make([]record.Lead, len(leads))
	for i, l := range leads {
		anchor := anchors[l.EvidenceID]
		if anchor.Text == "" {
			withText, err := s.withText([]record.Excerpt{anchor})
			if err != nil {
				if s.ctx.Err() != nil {
					return nil, errLeadsCancelled
				}
				l.Resolution, l.Reason = record.LeadUnresolved, record.LeadReadFailed
				out[i] = l
				continue
			}
			anchor = withText[0]
		}
		resolved, err := r.resolve(l, anchor)
		if err != nil {
			return nil, errLeadsCancelled
		}
		out[i] = resolved
	}
	return out, nil
}
