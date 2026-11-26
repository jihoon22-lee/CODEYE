package review

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Resolving the leads of an investigation (docs/REVIEW.md#단서-후속-조사).
//
// A lead starts from sent code lines and a symbol. A definition lead names
// the callee of a call on those lines (the call's name, or for a function
// an import binds under another name the imported name); a callers lead names
// the function those lines declare. The platform finds what the lead needs
// in the pinned snapshot with the review's source resolvers: the call
// bindings of sourceCalls (Go package-qualified calls into project
// packages, Java, C and C++ headers, Rust modules, Python and JavaScript
// imports), a source-only type check of the anchor's Go package for its own
// functions and the methods of its concrete types (goCallees), and the Go
// caller expansion (expandWithin). The standard library, other modules,
// interface methods and values of a type the check cannot fix resolve to
// nothing. It reads only files the run may send: in scope, inside a pull
// request's changed files, not withheld by the secrets screen, not
// generated and not on a secondary path, each blob checked against the
// manifest (readVerified). Every lead gets one scan within maxScanFiles and
// maxScanBytes, the caller expansion's reads included. A location the
// investigate request already carried is named by that excerpt's ID and not
// sent again; two declarations the lead could mean are never chosen
// between. Each found location becomes one window of at most
// record.MaxLeadWindowLines lines and maxWindowBytes bytes, within the
// follow-up's excerpt and byte caps (limits.leadExcerpts, leadBytes): a
// definition's whole declaration when it fits and the investigation sent
// none of its lines, else a window marked partial.

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
		// A generated file is neither read for a lead nor sent.
		l.refused[e.Path] = record.LeadGenerated
		return "", false, nil
	}
	l.texts[e.Path], l.readable[e.Path] = text, true
	return text, true, nil
}

// missing is the reason of a lead the scan found nothing for: a file it
// was not allowed to read that the lead may point into (relevant), then its
// limit, then notFound. The scan reads every file the anchor's file calls
// into, so a refused file of another call never names the reason.
func (l *leadScan) missing(notFound string, relevant func(string) bool) string {
	for _, reason := range []string{record.LeadWithheld, record.LeadOutsideChange, record.LeadOutsideScope, record.LeadGenerated, record.LeadSecondary} {
		for p, r := range l.refused {
			if r == reason && relevant(p) {
				return reason
			}
		}
	}
	if l.limited {
		return record.LeadScanLimit
	}
	return notFound
}

var importLine = regexp.MustCompile(`^\s*(import|from|#\s*include|use|mod|require|export)\b|\brequire\s*\(`)

// definitionFiles reports the files a definition lead may point into, by
// path and text alone (they could not be read): for a qualified call
// (q.symbol, q::symbol, q->symbol) a file whose name or directory is q or
// that an import line naming q points at; for an unqualified call a file of
// the anchor's directory or one that an import line naming the symbol
// points at.
func (r *leadResolver) definitionFiles(scan *leadScan, anchor record.Excerpt, symbol string) func(string) bool {
	qualified := regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*(?:\.|::|->)\s*` + regexp.QuoteMeta(symbol) + `\s*\(`)
	var names []string
	for _, m := range qualified.FindAllStringSubmatch(anchor.Text, -1) {
		names = addLim(names, m[1])
	}
	var imports []string
	if f, err := scan.index.goFile(anchor.Path); err == nil && f.parsed != nil {
		for _, spec := range f.parsed.Imports {
			line := spec.Path.Value
			if spec.Name != nil {
				line = spec.Name.Name + " " + line
			}
			imports = append(imports, line)
		}
	} else if e, ok := r.catalog.files[anchor.Path]; ok {
		if text, readable, err := scan.read(e); err == nil && readable {
			for _, line := range strings.Split(text, "\n") {
				if importLine.MatchString(line) {
					imports = append(imports, line)
				}
			}
		}
	}
	return func(p string) bool {
		own := []string{strings.TrimSuffix(path.Base(p), path.Ext(p)), path.Base(path.Dir(p))}
		points := func(token string) bool {
			for _, line := range imports {
				if symbolAt(line, token) && slices.ContainsFunc(own, func(n string) bool { return symbolAt(line, n) }) {
					return true
				}
			}
			return false
		}
		if len(names) == 0 {
			return path.Dir(p) == path.Dir(anchor.Path) || points(symbol)
		}
		for _, q := range names {
			if slices.Contains(own, q) || points(q) {
				return true
			}
		}
		return false
	}
}

// callAt reports a call of symbol in text: the identifier followed by "(".
func callAt(text, symbol string) bool {
	return regexp.MustCompile(`(^|[^A-Za-z0-9_$])` + regexp.QuoteMeta(symbol) + `\s*\(`).MatchString(text)
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
	// extent is the whole declaration of a definition whose bound location
	// (loc) may be only part of it (a Go function's closing return, a Python
	// or JavaScript function up to its first return): the window holds the
	// whole declaration when it fits one window and the investigate request
	// sent none of its lines (they are not sent twice).
	extent sourceCallLocation
	// excerpt is the window when the resolver already built it (the Go
	// caller expansion).
	excerpt *record.Excerpt
}

// declaration is the hit's whole declaration when known, else its location.
func (h leadHit) declaration() sourceCallLocation {
	if h.extent.start != 0 {
		return h.extent
	}
	return h.loc
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
	// A definition lead's call is checked where it binds (definitions).
	if l.Need == record.LeadNeedCallers && !symbolAt(anchor.Text, l.Symbol) {
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
		why := r.blocked(h.loc.path)
		if scan.refused[h.loc.path] == record.LeadGenerated {
			why = record.LeadGenerated
		}
		if why != "" {
			blockedBy = cmpReason(blockedBy, why)
			continue
		}
		x, partial, ok := r.window(scan, h)
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
		l.Targets = append(l.Targets, record.LeadTarget{ID: x.ID, Path: x.Path, SHA256: x.SHA256, Start: x.Start, End: x.End, Kind: x.Kind, Origin: x.Origin, Symbol: x.Symbol, Partial: partial})
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

// definitions finds the declaration a call of the symbol on the anchor
// lines binds to: by goCallees for Go, by the bound calls of sourceCalls for
// the other languages. The symbol names the callee itself, never another
// token of the line: no such call is lead_symbol_not_at_anchor. More than
// one distinct declaration is ambiguous.
func (r *leadResolver) definitions(scan *leadScan, symbol string, anchor record.Excerpt) ([]leadHit, string, error) {
	links, err := scan.index.from(anchor.Path, ".")
	if err != nil {
		return nil, "", err
	}
	var hits []leadHit
	add := func(h leadHit) {
		if !slices.ContainsFunc(hits, func(o leadHit) bool { return o.declaration() == h.declaration() }) {
			hits = append(hits, h)
		}
	}
	called := false
	if strings.HasSuffix(anchor.Path, ".go") {
		found, ok, err := r.goCallees(scan, anchor, symbol, links)
		if err != nil {
			return nil, "", err
		}
		called = ok
		for _, h := range found {
			add(h)
		}
	} else {
		called = callAt(anchor.Text, symbol)
		for _, link := range links {
			// The call's name, or the imported name an alias binds it to.
			if link.caller.path != anchor.Path || link.caller.start < anchor.Start || anchor.End < link.caller.start ||
				(symbol != link.symbol && symbol != link.local) {
				continue
			}
			called = true
			extent := link.calleeExtent
			if extent.start == 0 && callLanguage(link.callee.path) != "" {
				if extent, err = r.scriptExtent(scan, link); err != nil {
					return nil, "", err
				}
			}
			add(leadHit{loc: link.callee, origin: "context", extent: extent})
		}
	}
	switch {
	case !called:
		return nil, record.LeadSymbolNotAtAnchor, nil
	case len(hits) == 0:
		return nil, scan.missing(record.LeadDefinitionMissing, r.definitionFiles(scan, anchor, symbol)), nil
	case len(hits) > 1:
		return nil, record.LeadAmbiguous, nil
	}
	return hits, "", nil
}

// scriptExtent is the whole Python or JavaScript declaration whose window
// (up to its first return) a bound call names.
func (r *leadResolver) scriptExtent(scan *leadScan, link sourceCall) (sourceCallLocation, error) {
	text, ok, err := scan.index.text(link.callee.path)
	if err != nil || !ok {
		return sourceCallLocation{}, err
	}
	python := callLanguage(link.callee.path) == "python"
	start, end := directDeclarationExtent(callTokens(text, python), link.symbol, python)
	if start != link.callee.start || end < link.callee.end {
		return sourceCallLocation{}, nil
	}
	return sourceCallLocation{link.callee.path, start, end}, nil
}

// goCallees finds what the Go calls of symbol on the anchor lines bind to;
// called reports such a call. A package-qualified call binds only into the
// imported project package, through the bound calls of sourceCalls: a call
// into the standard library or another module binds nothing, whatever the
// anchor's package declares under the same name. A call of a package
// function, or of a method, binds through a type check of the anchor's
// package from its source alone (as the method caller expansion does): with
// no importer, a method binds only on a concrete type the package itself
// declares and fixes for the receiver. Interface methods, function values
// and values of a type the check cannot fix bind nothing; no method is
// matched by name across receivers. A function or method the package
// declares more than once (build variants) binds to each declaration, which
// definitions reports as ambiguous.
func (r *leadResolver) goCallees(scan *leadScan, anchor record.Excerpt, symbol string, links []sourceCall) ([]leadHit, bool, error) {
	own, err := scan.index.goFile(anchor.Path)
	if err != nil {
		return nil, false, err
	}
	if own.parsed == nil {
		return nil, callAt(anchor.Text, symbol), nil
	}
	imports := map[string]bool{}
	for _, spec := range own.parsed.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		name := path.Base(imported)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = true
	}
	var hits []leadHit
	called, typed := false, false
	for call, name := range goCallsNamed(own.parsed, own.line, symbol, anchor) {
		called = true
		selector, ok := ast.Unparen(goCallee(call)).(*ast.SelectorExpr)
		qualifier, qualified := ast.Expr(nil), false
		if ok {
			qualifier = selector.X
		}
		if x, ok := qualifier.(*ast.Ident); ok && x.Obj == nil {
			at := sourceCallLocation{anchor.Path, own.line(call.Pos()), own.line(call.End())}
			for _, link := range links {
				if link.symbol == name.Name && link.caller == at {
					qualified = true
					hits = append(hits, leadHit{loc: link.callee, origin: "context", extent: link.calleeExtent})
				}
			}
			qualified = qualified || imports[x.Name]
		}
		typed = typed || !qualified
	}
	if !typed {
		return hits, called, nil
	}
	pkg, err := r.goTypedPackage(scan, anchor.Path)
	if err != nil || pkg == nil {
		return hits, called, err
	}
	for call := range goCallsNamed(pkg.anchor, pkg.line, symbol, anchor) {
		var obj types.Object
		switch fun := ast.Unparen(goCallee(call)).(type) {
		case *ast.Ident:
			obj = pkg.info.Uses[fun]
		case *ast.SelectorExpr:
			if s := pkg.info.Selections[fun]; s != nil && s.Kind() != types.FieldVal && !types.IsInterface(s.Recv()) {
				obj = s.Obj()
			}
		}
		fn, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		key, ok := pkg.keys[fn.Origin()]
		if !ok {
			continue
		}
		for _, loc := range pkg.declarations[key] {
			hits = append(hits, leadHit{loc: loc, origin: "context", extent: loc})
		}
	}
	return hits, called, nil
}

// goCallee is a call's callee without its type arguments.
func goCallee(call *ast.CallExpr) ast.Expr {
	fun := ast.Unparen(call.Fun)
	switch indexed := fun.(type) {
	case *ast.IndexExpr:
		return indexed.X
	case *ast.IndexListExpr:
		return indexed.X
	}
	return fun
}

// goCallsNamed yields the calls in f whose callee name (a function name or
// a selector's last name) is symbol on a line of the anchor.
func goCallsNamed(f *ast.File, line func(token.Pos) int, symbol string, anchor record.Excerpt) func(func(*ast.CallExpr, *ast.Ident) bool) {
	return func(yield func(*ast.CallExpr, *ast.Ident) bool) {
		for n := range ast.Preorder(f) {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				continue
			}
			var name *ast.Ident
			switch fun := ast.Unparen(goCallee(call)).(type) {
			case *ast.Ident:
				name = fun
			case *ast.SelectorExpr:
				name = fun.Sel
			}
			if name == nil || name.Name != symbol {
				continue
			}
			if at := line(name.Pos()); at < anchor.Start || anchor.End < at {
				continue
			}
			if !yield(call, name) {
				return
			}
		}
	}
}

// goTypedPackage is the type check of a Go file's package from source.
type goTypedPackage struct {
	anchor *ast.File
	line   func(token.Pos) int
	info   types.Info
	// keys name each checked function or method (receiver base type, dot,
	// name); declarations list every declaration of a key in the package,
	// the files a build constraint excludes from the check included.
	keys         map[*types.Func]string
	declarations map[string][]sourceCallLocation
}

// goTypedPackage checks the package of the Go file p: its directory's
// files of the same package (its test files too when p is a test),
// unconditional and not generated, plus p itself, read through the lead's
// scan. No importer, build or project code runs; nil when p is not part of
// the check.
func (r *leadResolver) goTypedPackage(scan *leadScan, p string) (*goTypedPackage, error) {
	own, err := scan.index.goFile(p)
	if err != nil || own.parsed == nil {
		return nil, err
	}
	name, dir := own.parsed.Name.Name, path.Dir(p)
	files := slices.Clone(r.catalog.dirs[dir])
	if strings.HasSuffix(p, "_test.go") {
		for _, q := range r.catalog.ordered {
			if path.Dir(q) == dir && strings.HasSuffix(q, "_test.go") {
				files = append(files, q)
			}
		}
	}
	positions := token.NewFileSet()
	pkg := &goTypedPackage{line: func(pos token.Pos) int { return positions.PositionFor(pos, false).Line },
		keys: map[*types.Func]string{}, declarations: map[string][]sourceCallLocation{}}
	var inputs []*ast.File
	paths := map[*ast.File]string{}
	for _, q := range files {
		if goIgnored(q) {
			continue
		}
		text, ok, err := scan.index.text(q)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		f, err := parser.ParseFile(positions, q, text, parser.ParseComments)
		if err != nil || f.Name.Name != name || ast.IsGenerated(f) {
			continue
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				key := methodReceiverName(fn) + "." + fn.Name.Name
				pkg.declarations[key] = append(pkg.declarations[key], sourceCallLocation{q, pkg.line(fn.Pos()), pkg.line(fn.End())})
			}
		}
		if q == p || (contractGoFile{entry: r.catalog.files[q], parsed: f}).unconditional() {
			inputs, paths[f] = append(inputs, f), q
			if q == p {
				pkg.anchor = f
			}
		}
	}
	if pkg.anchor == nil {
		return nil, nil
	}
	pkg.info = types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	config := types.Config{Error: func(error) {}}
	_, _ = config.Check("review/"+dir, positions, inputs, &pkg.info)
	for _, f := range inputs {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				if obj, ok := pkg.info.Defs[fn.Name].(*types.Func); ok {
					pkg.keys[obj] = methodReceiverName(fn) + "." + fn.Name.Name
				}
			}
		}
	}
	return pkg, nil
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
// the run may read, in review order. Both read through the lead's scan.
func (r *leadResolver) callers(scan *leadScan, symbol string, anchor record.Excerpt) ([]leadHit, string, error) {
	definition := sourceCallLocation{anchor.Path, anchor.Start, anchor.End}
	var hits []leadHit
	// callerFiles are the files read as places a caller may be: one the
	// scan refused names the reason when no caller is found.
	callerFiles := map[string]bool{}
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
		// The expansion reads through the lead's scan: one file and byte limit
		// for the lead, and no file the lead may not read.
		read := func(e source.Entry) (string, bool, error) {
			callerFiles[e.Path] = true
			return scan.read(e)
		}
		found, lims, err := expandWithin(r.s.ctx, r.s.j.Snapshot, normalizedScope(r.s.j.Scope), []record.Excerpt{narrow},
			[]record.CandidateText{{Evidence: []string{narrow.ID}}}, r.s.secretFiles(), changed,
			expansionCaps{count: 2 * record.MaxLeadTargets, bytes: 2 * record.MaxLeadTargets * maxWindowBytes, read: read})
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
		callerFiles[p] = true
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
	// What the scan refused while reading the callees of those files never
	// names the reason.
	if len(hits) == 0 {
		return nil, scan.missing(record.LeadCallersMissing, func(p string) bool { return callerFiles[p] }), nil
	}
	return hits, "", nil
}

// window is the excerpt of one hit: a definition's whole declaration when
// it fits one window and the investigate request sent none of its lines,
// else the definition from its bound location; a call site with three
// lines around it. A window holds at most record.MaxLeadWindowLines lines
// and maxWindowBytes bytes, bound to the snapshot blob. partial reports a
// definition window that is not its whole declaration.
func (r *leadResolver) window(scan *leadScan, h leadHit) (x record.Excerpt, partial, ok bool) {
	kind := windowKind(h.loc.path)
	if kind != "code" && kind != "test" {
		return record.Excerpt{}, false, false
	}
	if h.excerpt != nil {
		x := *h.excerpt
		if x.End-x.Start+1 > record.MaxLeadWindowLines || record.JSONLen(x.Text) > maxWindowBytes {
			return record.Excerpt{}, false, false
		}
		return x, false, true
	}
	e, ok := r.catalog.files[h.loc.path]
	if !ok || e.Kind != "file" || e.Size == nil || e.SHA256 == "" || e.ExcludedReason != "" {
		return record.Excerpt{}, false, false
	}
	text, readable, err := scan.read(e)
	if err != nil || !readable {
		return record.Excerpt{}, false, false
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
		return record.Excerpt{}, false, false
	}
	whole := h.declaration()
	if origin == "context" && whole.path == h.loc.path && whole.start >= 1 && whole.start <= h.loc.start && h.loc.end <= whole.end &&
		whole.end <= len(lines) && whole.end-whole.start+1 <= record.MaxLeadWindowLines &&
		!slices.ContainsFunc(r.sent, func(x record.Excerpt) bool {
			return x.Path == whole.path && x.Start <= whole.end && whole.start <= x.End
		}) {
		if x, ok := r.s.boundedExcerpt(e, kind, origin, whole.start, lines[whole.start-1:whole.end], maxWindowBytes); ok && x.End == whole.end {
			return x, false, true
		}
	}
	x, ok = r.s.boundedExcerpt(e, kind, origin, start, lines[start-1:end], maxWindowBytes)
	if !ok || (origin != "context" && x.End < h.loc.end) {
		return record.Excerpt{}, false, false
	}
	if origin != "context" && record.ValidEvidenceSymbol(h.symbol) {
		x.Symbol = h.symbol
	}
	return x, origin == "context" && (x.Start > whole.start || x.End < whole.end), true
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
