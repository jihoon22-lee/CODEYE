package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/docpath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Bounds of the review inputs, in JSON-encoded bytes of excerpt text
// (encoding/json escapes <, > and & to six bytes). Every request must also
// fit PromptBudget; roles trim excerpts to fit and record it.
const (
	roleMaxCodeFiles    = 8
	roleMaxCodeFile     = 2400
	roleMaxCodeLines    = 50
	roleMaxCodeBytes    = 12000
	roleMaxDocs         = 8
	roleMaxDocBytes     = 2000
	roleMaxDocLines     = 40
	roleMaxDocsBytes    = 10000
	roleMaxRequirements = 16
	maxObservations     = 8
	roleMaxTools        = 8
	roleMaxWindows      = 4
	roleWindowRadius    = 10
	roleMaxWindowBytes  = 1600
)

func normalizedScope(s domain.Scope) domain.Scope {
	if s.Kind == "" {
		return domain.Scope{Kind: "full"}
	}
	return s
}

func (s *roleStage) entries() map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range s.j.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil {
			out[e.Path] = e
		}
	}
	return out
}

// readSource returns a verified snapshot blob that is safe to use as model input.
func (s *roleStage) readSource(e source.Entry) (string, bool, error) {
	text, reason, err := s.reviewSource(e)
	return text, reason == "", err
}

func (s *roleStage) reviewSource(e source.Entry) (string, string, error) {
	if *e.Size > maxFileBytes {
		return "", record.ScopeReasonTooLarge, nil
	}
	if domain.ValidateEvidencePath(e.Path) != nil {
		return "", record.ScopeReasonUnreadable, nil
	}
	if s.secretFiles()[e.Path] {
		s.rec.Limitations = addLim(s.rec.Limitations, limSecretWithheld)
		return "", record.ScopeReasonWithheld, nil
	}
	root, err := os.OpenRoot(s.j.Snapshot.SnapshotDir)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	b, err := readVerified(root, e)
	if err != nil {
		return "", "", err
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 {
		return "", record.ScopeReasonUnreadable, nil
	}
	if record.Sensitive(b) {
		return "", record.ScopeReasonWithheld, nil
	}
	return strings.TrimSuffix(string(b), "\n"), "", nil
}

// readLines returns lines start..end (1-based, inclusive) of a verified
// snapshot blob, or ok=false when the blob is not safe model input.
func (s *roleStage) readLines(e source.Entry, start, end int) (string, int, bool, error) {
	lines, ok, err := s.sourceLines(e, nil)
	if err != nil || !ok {
		return "", 0, false, err
	}
	if start < 1 || start > len(lines) {
		return "", len(lines), false, nil
	}
	end = min(end, len(lines))
	return strings.Join(lines[start-1:end], "\n"), len(lines), true, nil
}

// sourceLines can reuse a checked blob across excerpts in one role. The
// caller resolves entries against that role's immutable snapshot index.
func (s *roleStage) sourceLines(e source.Entry, cached map[string][]string) ([]string, bool, error) {
	if lines, ok := cached[e.Path]; ok {
		return lines, true, nil
	}
	text, ok, err := s.readSource(e)
	if err != nil || !ok {
		return nil, ok, err
	}
	lines := strings.Split(text, "\n")
	if cached != nil {
		cached[e.Path] = lines
	}
	return lines, true, nil
}

// secretFiles are the files the secrets screen withholds from every model
// input of the stage (screen.go): code context, documents, observation
// windows and verification expansions.
func (s *roleStage) secretFiles() map[string]bool { return s.screen.withheld }

// excerpt builds a bounded excerpt of up to maxLines lines and maxBytes of
// JSON-encoded text from line start. total is the file's line count.
func (s *roleStage) excerpt(e source.Entry, kind, origin string, start, maxLines, maxBytes int) (x record.Excerpt, total int, ok bool, err error) {
	text, total, ok, err := s.readLines(e, start, start+maxLines-1)
	if err != nil || !ok {
		return record.Excerpt{}, total, false, err
	}
	x, ok = s.boundedExcerpt(e, kind, origin, start, strings.Split(text, "\n"), maxBytes)
	return x, total, ok, nil
}

func (s *roleStage) boundedExcerpt(e source.Entry, kind, origin string, start int, lines []string, maxBytes int) (record.Excerpt, bool) {
	var kept []string
	used := 0
	for _, line := range lines {
		cost := record.JSONLen(line)
		if used+cost > maxBytes {
			break
		}
		kept = append(kept, line)
		used += cost
	}
	if len(kept) == 0 || strings.TrimSpace(strings.Join(kept, "")) == "" {
		return record.Excerpt{}, false
	}
	end := start + len(kept) - 1
	return record.Excerpt{ID: record.ExcerptID(s.j.Snapshot.Digest, e.Path, e.SHA256, start, end), Path: e.Path, SHA256: e.SHA256, Start: start, End: end,
		Kind: kind, Origin: origin, Text: strings.Join(kept, "\n")}, true
}

func shortReviewSource(text string, maxBytes int) bool {
	return len(text) <= maxBytes && strings.Count(text, "\n") < roleMaxCodeLines && record.JSONLen(text) <= maxBytes
}

// withText re-reads the text of record excerpts from the snapshot, checking
// each excerpt's ID binding again.
func (s *roleStage) withText(list []record.Excerpt) ([]record.Excerpt, error) {
	files := s.entries()
	cached := map[string][]string{}
	out := make([]record.Excerpt, 0, len(list))
	for _, x := range list {
		e, ok := files[x.Path]
		if !ok || e.SHA256 != x.SHA256 || x.ID != record.ExcerptID(s.j.Snapshot.Digest, x.Path, x.SHA256, x.Start, x.End) {
			return nil, errSnapshotIntegrity
		}
		lines, ok, err := s.sourceLines(e, cached)
		if err != nil {
			return nil, err
		}
		if !ok || x.Start < 1 || x.Start > len(lines) {
			return nil, errSnapshotIntegrity
		}
		x.Text = strings.Join(lines[x.Start-1:min(x.End, len(lines))], "\n")
		out = append(out, x)
	}
	return out, nil
}

var errSnapshotIntegrity = os.ErrInvalid

// ---- discovery artifacts -------------------------------------------------

func (s *roleStage) discoveryDoc(kind string) (envdoc.Document, record.Upstream) {
	src := map[string]string{envdoc.KindEnvironment: envdoc.TaskEnvironment, envdoc.KindDocuments: envdoc.TaskDocuments}[kind]
	u := record.Upstream{Source: src}
	b, err := readBounded(discovery.ArtifactPath(s.j.Scratch, kind), envdoc.MaxBytes)
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordMissing
		return envdoc.Document{}, u
	}
	in := discovery.Input{Manifest: s.j.Snapshot.Manifest, Digest: s.j.Snapshot.Digest, Scope: s.j.Scope, RunID: s.j.RunID, AttemptID: s.j.AttemptID}
	d, err := envdoc.Decode(b, in.Expect(kind))
	if err != nil {
		u.State, u.Reason = record.UpstreamUnavailable, reasonRecordInvalid
		return envdoc.Document{}, u
	}
	u.State, u.SHA256 = record.UpstreamAccepted, digestOf(b)
	return d, u
}

type envFacts struct {
	doc      envdoc.Document
	upstream record.Upstream
	gaps     []string // modules with a verdict other than satisfied
}

// environmentFacts loads env.discover's artifact and the skip rule: no model
// call when every declared requirement is satisfied.
func (s *roleStage) environmentFacts() (envFacts, string) {
	d, u := s.discoveryDoc(envdoc.KindEnvironment)
	f := envFacts{doc: d, upstream: u}
	if u.State != record.UpstreamAccepted || d.Environment == nil {
		return f, reasonDependency
	}
	gap, declaredGap := map[string]bool{}, false
	for _, v := range d.Environment.Verdicts {
		if v.Status != envdoc.VerdictSatisfied {
			gap[v.Module] = true
			declaredGap = declaredGap || v.Requirement != nil
		}
	}
	if !declaredGap {
		return f, ReasonNoEnvironmentGap
	}
	f.gaps = sortedKeys(gap)
	return f, ""
}

type docFacts struct {
	doc      envdoc.Document
	upstream record.Upstream
	selected []envdoc.Doc
}

// documentFacts applies the documents role's skip rules: no guidance
// document in scope (no_documents), discovery unavailable (dependency_failed)
// or none of them selected under the read budget (documents_not_selected).
func (s *roleStage) documentFacts() (docFacts, string) {
	if len(discovery.EligibleDocuments(s.j.Snapshot.Manifest, s.j.Scope)) == 0 {
		return docFacts{}, ReasonNoDocuments
	}
	d, u := s.discoveryDoc(envdoc.KindDocuments)
	f := docFacts{doc: d, upstream: u}
	if u.State != record.UpstreamAccepted || d.Documents == nil {
		return f, reasonDependency
	}
	for _, it := range d.Documents.Items {
		if it.Status == envdoc.DocSelected && envdoc.IsGuidance(it.Class) && it.SHA256 != nil && it.LineCount != nil && *it.LineCount > 0 {
			f.selected = append(f.selected, it)
		}
	}
	if len(f.selected) == 0 {
		return f, ReasonDocsNotSelected
	}
	return f, ""
}

// ---- analyzer observations -------------------------------------------------

// toolLocation and toolObservation are the analyzer-independent view of one
// observation; toolSummary of one analyzer task (go vet v0.1 or the generic
// envelope of docs/ANALYZERS.md).
type toolLocation struct {
	Path               string
	StartLine, EndLine int
}

type toolObservation struct {
	ID, Tool, RuleID, RawMessage, LocationState string
	Location                                    *toolLocation
	Related                                     []obsdoc.Related
	// Severity is the tool's raw severity; it orders review.scope's slots
	// and is never part of a request.
	Severity *string
}

type toolSummary struct {
	Tool, Module, TaskStatus, ExitClass string
	// Ecosystem is the analyzer's ecosystem (go for a go vet document).
	Ecosystem             string
	ToolVersion           *string
	PartialParse, ParseOK bool
	Limitations           []string
	Observations          []toolObservation
}

type toolDoc struct {
	key      string
	doc      toolSummary
	upstream record.Upstream
}

// readToolDoc reads and validates one analyzer task's observations artifact:
// the generic envelope at obsdoc.ArtifactPath, else a go vet v0.1 document.
func (s *roleStage) readToolDoc(key string, files map[string]source.Entry) (toolSummary, []byte, string) {
	if b, err := readBounded(obsdoc.ArtifactPath(s.j.Scratch, key), obsdoc.MaxBytes); err == nil {
		lookup := func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }
		d, err := obsdoc.Decode(b, obsdoc.Expect{TaskKey: key, Snapshot: s.j.Snapshot.Digest, Lookup: lookup})
		if err != nil {
			return toolSummary{}, nil, reasonRecordInvalid
		}
		sum := toolSummary{Tool: d.Analyzer.ID, ToolVersion: d.Tool.Version, Module: d.Module.Dir, TaskStatus: string(d.Outcome.TaskStatus), ExitClass: d.Outcome.ExitClass,
			PartialParse: d.Outcome.PartialParse, ParseOK: d.Parse.OK, Limitations: d.Limitations, Ecosystem: d.Analyzer.Ecosystem}
		for _, o := range d.Observations {
			// A diagnostic the tool reported as suppressed in source is the
			// project's decision, not a candidate: it never reaches the model
			// (the envelope's source_suppressed_observations limitation says
			// some were left out) and never becomes a finding (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제).
			if o.Suppressed {
				continue
			}
			// A structure metric is no defect candidate: the envelope's
			// metrics_not_defects limitation is all the model sees of it.
			if o.Metric != nil {
				continue
			}
			to := toolObservation{ID: o.ID, Tool: d.Analyzer.ID, RuleID: o.RuleID, RawMessage: o.RawMessage, LocationState: o.LocationState, Severity: o.RawSeverity}
			if o.Location != nil {
				to.Location = &toolLocation{Path: o.Location.Path, StartLine: o.Location.StartLine, EndLine: o.Location.EndLine}
			}
			if d.Analyzer.ID == "cpp.cppcheck" || d.Analyzer.ID == "cpp.clang_tidy" {
				for _, r := range o.Related {
					if r.LocationState == obsdoc.LocationResolved && r.Location != nil && r.RawSpan != nil &&
						obsdoc.ValidRelatedKind(r.Kind) && !slices.Contains(d.WorkspaceDrift, r.Location.Path) {
						to.Related = append(to.Related, r)
					}
				}
			}
			sum.Observations = append(sum.Observations, to)
		}
		return sum, b, ""
	}
	if !pack.Owns(govet.HandlerName, key) {
		return toolSummary{}, nil, reasonRecordMissing
	}
	b, err := readBounded(govet.ObservationsPath(s.j.Scratch, key), 8<<20)
	if err != nil {
		return toolSummary{}, nil, reasonRecordMissing
	}
	var d govet.Document
	if domain.DecodeStrict(b, &d) != nil || d.SchemaVersion != govet.DocumentSchemaVersion || d.Kind != govet.DocumentKind || d.AnalyzerID != govet.AnalyzerID ||
		d.TaskKey != key || d.ManifestDigest != s.j.Snapshot.Digest || d.Observations == nil || len(d.Observations) != d.Parse.Observations {
		return toolSummary{}, nil, reasonRecordInvalid
	}
	sum := toolSummary{Tool: d.AnalyzerID, ToolVersion: d.ToolVersion, Module: d.Module.Dir, TaskStatus: string(d.Outcome.TaskStatus), ExitClass: d.Outcome.ExitClass,
		PartialParse: d.Outcome.PartialParse, ParseOK: d.Parse.OK, Limitations: d.Limitations, Ecosystem: "go"}
	for _, o := range d.Observations {
		to := toolObservation{ID: o.ID, Tool: o.Tool, RuleID: o.RuleID, RawMessage: o.RawMessage, LocationState: o.LocationState}
		if o.Location != nil {
			e, ok := files[o.Location.Path]
			if !ok || e.SHA256 != o.Location.BlobSHA256 || o.Location.StartLine < 1 {
				return toolSummary{}, nil, reasonRecordInvalid
			}
			to.Location = &toolLocation{Path: o.Location.Path, StartLine: o.Location.StartLine, EndLine: o.Location.EndLine}
		}
		sum.Observations = append(sum.Observations, to)
	}
	return sum, b, ""
}

// toolFacts reads every planned analyzer task's observations artifact.
func (s *roleStage) toolFacts() ([]toolDoc, int, string) {
	plan, err := s.h.Static.Plan(s.ctx, s.j)
	if err != nil {
		return nil, 0, reasonDependency
	}
	files := s.entries()
	var docs []toolDoc
	total := 0
	for _, t := range plan {
		if !record.AnalyzerTaskKey(t.Key) {
			continue
		}
		td := toolDoc{key: t.Key, upstream: record.Upstream{Source: t.Key}}
		sum, b, reason := s.readToolDoc(t.Key, files)
		if reason != "" {
			td.upstream.State, td.upstream.Reason = record.UpstreamUnavailable, reason
			docs = append(docs, td)
			continue
		}
		td.doc, td.upstream.State, td.upstream.SHA256 = sum, record.UpstreamAccepted, digestOf(b)
		total += len(sum.Observations)
		docs = append(docs, td)
	}
	if total == 0 {
		return docs, 0, ReasonNoToolObservation
	}
	return docs, total, ""
}

// ---- module-aware code corpus --------------------------------------------

func goIgnored(p string) bool {
	segs := strings.Split(p, "/")
	for _, seg := range segs[:len(segs)-1] {
		if seg == "vendor" || seg == "testdata" || strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "_") {
			return true
		}
	}
	return false
}

type corpusLinks struct {
	directCalls   []directCall
	member        memberPair
	witnessSource map[string]string
	wholeWitness  map[string]record.Excerpt
	partial       map[string]bool
	workflow      []workflowLink
	claims        contractAllocation
}

// codeCorpus selects code and test excerpts round-robin over the modules of
// the snapshot, so every module in scope is represented before any module
// gets a second file. A Go file belongs to its deepest go.mod directory; a
// file of another ecosystem to its deepest build manifest directory of that
// ecosystem, and modules are visited ecosystem by ecosystem (sources.go,
// groupOrder). Accepted document contracts can add targeted windows before
// the ordinary file prefixes.
func (s *roleStage) codeCorpus(documents record.Record) ([]record.Excerpt, corpusLinks, []string, error) {
	g := planCodeGroupsFor(s.j.Snapshot.Manifest, s.j.Scope, s.h.secondary(), changedPaths(s.j))
	lims := append([]string{}, g.lims...)
	var out []record.Excerpt
	remain := roleMaxCodeBytes
	offered := map[string]string{}
	links := corpusLinks{partial: map[string]bool{}}
	keep := func(x record.Excerpt, total int) {
		if x.Start > 1 || x.End < total {
			lims = addLim(lims, "excerpt_only")
			links.partial[x.ID] = true
		}
		out = append(out, x)
		offered[x.Path] = x.ID
		remain -= record.JSONLen(x.Text)
	}
	add := func(e source.Entry, start, lines, budget int) error {
		x, total, ok, err := s.excerpt(e, windowKind(e.Path), "context", start, lines, min(budget, remain))
		if ok {
			keep(x, total)
		}
		return err
	}
	workflow, missingWorkflow, workflowErr := s.selectedWorkflowRoutes(documents)
	if s.ctx.Err() != nil {
		return nil, corpusLinks{}, nil, s.ctx.Err()
	}
	if missingWorkflow || workflowErr != nil {
		lims = addLim(lims, limWorkflowUnavailable)
	}
	links.workflow = workflow
	selected, err := s.contractWitnesses(g, documents)
	if err != nil {
		return nil, corpusLinks{}, nil, err
	}
	links.member, links.witnessSource = selected.member, selected.sources
	keepOption := func(trace goOptionTrace) {
		if windows, ok := wholeTraceWindows(out, trace.windows, remain, roleMaxCodeFiles-1); ok {
			for _, x := range windows {
				keep(x, x.End+1)
			}
			links.directCalls = append(links.directCalls, directCall{
				CallerEvidenceID: trace.windows[1].ID, CalleeEvidenceID: trace.windows[2].ID, Symbol: trace.symbol,
			})
		}
	}
	trace := selected.goOption
	if trace.delivers {
		keepOption(trace)
	}
	for _, x := range workflowOverviewWindows(workflow) {
		if !trace.delivers {
			keep(x, x.End+1)
			continue
		}
		if windows, ok := wholeTraceWindows(out, []record.Excerpt{x}, remain, roleMaxCodeFiles-1); ok && len(windows) > 0 {
			keep(x, x.End+1)
		}
	}
	if !trace.delivers && len(trace.windows) != 0 {
		keepOption(trace)
	}
	for _, trace := range selected.goTraces {
		windows, ok := wholeTraceWindows(out, trace.excerpts, remain, maxGoWrapperWindows)
		if !ok {
			continue
		}
		for _, x := range windows {
			keep(x, x.End) // Go declarations start after the package clause.
		}
		for i, name := range trace.callees {
			caller, callee := trace.excerpts[i], trace.excerpts[i+1]
			links.directCalls = append(links.directCalls, directCall{CallerEvidenceID: caller.ID, CalleeEvidenceID: callee.ID, Symbol: name})
		}
	}
	// Keep one fully bound value path in the initial investigation when it
	// fits alongside established links and ordinary breadth windows.
	for _, trace := range selected.goJSONFields {
		if windows, ok := wholeTraceWindows(out, trace.excerpts, remain, roleMaxCodeFiles-2); ok && len(windows) > 0 {
			for _, x := range windows {
				keep(x, x.End+1)
			}
			break
		}
	}
	if len(selected.cFamily) > 0 {
		var trace []record.Excerpt
		for _, w := range selected.cFamily {
			lines := strings.Split(selected.sources[w.entry.Path], "\n")
			if window, ok := s.boundedExcerpt(w.entry, "code", "context", w.line, lines[w.line-1:w.end], witnessMaxExcerpt); ok && window.End == w.end {
				trace = append(trace, window)
			}
		}
		if len(trace) == len(selected.cFamily) {
			if windows, ok := wholeTraceWindows(out, trace, remain, roleMaxCodeFiles-2); ok {
				for _, x := range windows {
					keep(x, x.End+1)
				}
			}
		}
	}
	for _, w := range selected.cDefinitions {
		if len(out) >= roleMaxCodeFiles-2 {
			break
		}
		if slices.ContainsFunc(selected.witnesses, func(prior contractWitness) bool {
			return prior.compileBranch && prior.entry.Path == w.entry.Path
		}) {
			continue
		}
		bytes, availableLines := roleMaxCodeFile, roleMaxCodeLines
		for _, prior := range out {
			if prior.Path != w.entry.Path {
				continue
			}
			if prior.Start <= w.end && w.line <= prior.End {
				availableLines = 0
				break
			}
			bytes -= record.JSONLen(prior.Text)
			availableLines -= prior.End - prior.Start + 1
		}
		if w.end-w.line+1 > availableLines {
			continue
		}
		lines := strings.Split(selected.sources[w.entry.Path], "\n")
		window, ok := s.boundedExcerpt(w.entry, "code", "context", w.line, lines[w.line-1:w.end], min(witnessMaxExcerpt, remain, bytes))
		if ok && window.End == w.end {
			keep(window, len(lines))
		}
	}
	// Preserve a slot for a selected test and at least two ordinary files
	// when exact Go traces consume more than the usual witness allowance.
	witnessLimit := max(witnessMaxExcerpts, len(out)+1)
	if len(workflow) > 0 {
		witnessLimit = min(witnessLimit, roleMaxCodeFiles-2)
	}
	if len(out) > 0 {
		var tests []contractWitness
		for _, w := range selected.witnesses {
			if windowKind(w.entry.Path) == "test" {
				tests = append(tests, w)
			}
		}
		if len(tests) > 0 {
			selected.witnesses = append(tests[:1], selected.witnesses...)
		}
	}
	for _, w := range selected.witnesses {
		if len(out) >= witnessLimit {
			break
		}
		if offered[w.entry.Path] != "" {
			continue
		}
		lines := witnessMaxLines
		if w.end > 0 {
			lines = w.end - w.line + 1
		}
		before := len(out)
		if err := add(w.entry, w.line, lines, witnessMaxExcerpt); err != nil {
			return nil, corpusLinks{}, nil, err
		}
		if len(out) > before && w.documented && links.partial[out[before].ID] {
			links.addWholeWitness(s.j.Snapshot.Digest, out[before], selected.sources[w.entry.Path])
		}
	}
	for _, call := range selected.calls {
		caller, callee := offered[call.caller.entry.Path], offered[call.callee.entry.Path]
		if caller != "" && callee != "" {
			links.directCalls = append(links.directCalls, directCall{CallerEvidenceID: caller, CalleeEvidenceID: callee, Symbol: call.symbol})
		}
	}
	if signature, ok := s.pythonSignatureWitness(out, selected, remain); ok {
		out = append(out, signature)
		links.partial[signature.ID] = true
		remain -= record.JSONLen(signature.Text)
	}
	for e := range g.orderedFiles() {
		if len(out) >= roleMaxCodeFiles || remain < 128 {
			break
		}
		if offered[e.Path] == "" {
			if err := add(e, 1, roleMaxCodeLines, roleMaxCodeFile); err != nil {
				return nil, corpusLinks{}, nil, err
			}
		}
	}
	if g.eligible > len(offered) {
		lims = append(lims, "files_outside_review_context")
	} else if len(out) < roleMaxCodeFiles && len(selected.additional) > 0 {
		before := len(out)
		out, err = s.additionalWitnesses(out, selected.additional, remain)
		if err != nil {
			return nil, corpusLinks{}, nil, err
		}
		for _, x := range out[before:] {
			links.partial[x.ID] = true
		}
	}
	links.claims = s.contractClaimAllocation(out, selected, workflow, documents)
	return out, links, addLim(nil, lims...), nil
}

// additionalWitnesses fills at most two spare overview slots after all
// eligible files have a window. Multiple windows of a single file still
// fit the per-file byte and line limits and must be disjoint.
func (s *roleStage) additionalWitnesses(out []record.Excerpt, candidates []contractWitness, remain int) ([]record.Excerpt, error) {
	limit := min(roleMaxCodeFiles, len(out)+2)
	const extraWindowLines = (roleMaxCodeLines - witnessMaxLines) / 2
	selectedQuery := map[int]bool{}
	for _, w := range candidates {
		if err := s.ctx.Err(); err != nil {
			return nil, err
		}
		if len(out) >= limit || remain < 128 {
			break
		}
		if selectedQuery[w.query] {
			continue
		}
		maxEnd := w.line + extraWindowLines - 1
		lines, budget := roleMaxCodeLines, roleMaxCodeFile
		for _, e := range out {
			if e.Path != w.entry.Path {
				continue
			}
			if e.Start <= w.line && w.line <= e.End {
				maxEnd = w.line - 1
				break
			}
			if e.Start > w.line {
				maxEnd = min(maxEnd, e.Start-1)
			}
			lines -= e.End - e.Start + 1
			budget -= record.JSONLen(e.Text)
		}
		budget = min(budget, witnessMaxExcerpt, remain)
		lines = min(lines, maxEnd-w.line+1)
		if lines < 6 || budget < 128 {
			continue
		}
		x, _, ok, err := s.excerpt(w.entry, "code", "context", w.line, lines, budget)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, x)
			remain -= record.JSONLen(x.Text)
			selectedQuery[w.query] = true
		}
	}
	return out, nil
}

// observation input of the interpret role
type obsInput struct {
	ID            string            `json:"id"`
	TaskKey       string            `json:"task_key"`
	RuleID        string            `json:"rule_id"`
	Message       string            `json:"message"`
	Path          string            `json:"path,omitempty"`
	StartLine     int               `json:"start_line,omitempty"`
	EndLine       int               `json:"end_line,omitempty"`
	LocationState string            `json:"location_state"`
	EvidenceIDs   []string          `json:"evidence_ids"`
	Related       []obsRelatedInput `json:"related,omitempty"`
}

type obsRelatedInput struct {
	Kind       string `json:"kind"`
	EvidenceID string `json:"evidence_id"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
}

type toolInput struct {
	TaskKey      string   `json:"task_key"`
	Tool         string   `json:"tool"`
	ToolVersion  *string  `json:"tool_version"`
	Module       string   `json:"module"`
	TaskStatus   string   `json:"task_status"`
	ExitClass    string   `json:"exit_class"`
	PartialParse bool     `json:"partial_parse"`
	ParseOK      bool     `json:"parse_ok"`
	Observations int      `json:"observations"`
	Limitations  []string `json:"limitations"`
}

// observationWindows builds the interpret inputs: tool states, up to
// maxObservations observations and code windows around their locations.
func (s *roleStage) observationWindows(docs []toolDoc) ([]toolInput, []obsInput, []record.ObservationRef, []record.Excerpt, []string, error) {
	files := s.entries()
	var tools []toolInput
	var all []toolObservation
	keyOf := map[string]string{}
	var lims []string
	for _, d := range docs {
		if d.upstream.State != record.UpstreamAccepted {
			continue
		}
		toolLims := []string{}
		for _, l := range d.doc.Limitations {
			if record.ValidCode(l) && len(toolLims) < 8 {
				toolLims = append(toolLims, l)
			}
		}
		if len(tools) >= roleMaxTools {
			lims = append(lims, "tools_outside_interpret_input")
			continue
		}
		tools = append(tools, toolInput{TaskKey: d.key, Tool: d.doc.Tool, ToolVersion: d.doc.ToolVersion, Module: d.doc.Module, TaskStatus: d.doc.TaskStatus,
			ExitClass: d.doc.ExitClass, PartialParse: d.doc.PartialParse, ParseOK: d.doc.ParseOK, Observations: len(d.doc.Observations), Limitations: toolLims})
		for _, o := range d.doc.Observations {
			all = append(all, o)
			keyOf[o.ID] = d.key
		}
	}
	sort.SliceStable(all, func(a, b int) bool {
		x, y := all[a], all[b]
		px, py := "\xff", "\xff"
		lx, ly := 0, 0
		if x.Location != nil {
			px, lx = x.Location.Path, x.Location.StartLine
		}
		if y.Location != nil {
			py, ly = y.Location.Path, y.Location.StartLine
		}
		if px != py {
			return px < py
		}
		if lx != ly {
			return lx < ly
		}
		return x.ID < y.ID
	})
	if len(all) > maxObservations {
		all = all[:maxObservations]
		lims = append(lims, "observations_outside_interpret_input")
	}
	var obs []obsInput
	var refs []record.ObservationRef
	var windows []record.Excerpt
	for _, o := range all {
		in := obsInput{ID: o.ID, TaskKey: keyOf[o.ID], RuleID: o.RuleID, Message: o.RawMessage, LocationState: o.LocationState, EvidenceIDs: []string{}}
		ref := record.ObservationRef{ID: o.ID, TaskKey: keyOf[o.ID], Tool: o.Tool, RuleID: o.RuleID}
		if !utf8.ValidString(in.Message) || record.Sensitive([]byte(in.Message)) || len(in.Message) > 600 {
			in.Message = "(omitted by the platform)"
			lims = append(lims, "observation_message_omitted")
		}
		// A tool message about a file the secrets screen withholds may quote
		// the value (e.g. a hardcoded-password rule): only its rule and place
		// reach the model.
		withheld := o.Location != nil && s.secretFiles()[o.Location.Path]
		withheld = withheld || slices.ContainsFunc(o.Related, func(r obsdoc.Related) bool {
			return s.secretFiles()[r.Location.Path]
		})
		if withheld && in.Message != "(omitted by the platform)" {
			in.Message = "(omitted by the platform)"
			lims = append(lims, limSecretWithheld)
		}
		if o.Location != nil {
			l := o.Location
			in.Path, in.StartLine, in.EndLine = l.Path, l.StartLine, l.EndLine
			ref.Path, ref.StartLine = l.Path, l.StartLine
			covered := ""
			for _, w := range windows {
				if w.Path == l.Path && w.Start <= l.StartLine && l.StartLine <= w.End {
					covered = w.ID
				}
			}
			switch {
			case covered != "":
				in.EvidenceIDs = []string{covered}
			case len(windows) >= roleMaxWindows:
				lims = append(lims, "observation_context_outside_input")
			case windowKind(l.Path) != "" && files[l.Path].Size != nil:
				// A line window around the location in any source language
				// (no parser); other files (manifests, configuration) get none.
				kind := windowKind(l.Path)
				x, _, ok, err := s.excerpt(files[l.Path], kind, "observation", max(1, l.StartLine-roleWindowRadius), 2*roleWindowRadius+1, roleMaxWindowBytes)
				if err != nil {
					return nil, nil, nil, nil, nil, err
				}
				if ok {
					windows = append(windows, x)
					in.EvidenceIDs = []string{x.ID}
				}
			}
		}
		obs = append(obs, in)
		refs = append(refs, ref)
	}
	windows, lims = s.relatedObservationWindows(all, obs, windows, files, lims)
	return tools, obs, refs, windows, addLim(nil, lims...), nil
}

type documentEvidence struct {
	excerpts    []record.Excerpt
	lists       map[string][]documentRange
	limitations []string
}

// documentExcerpts reads the selected guidance documents within the bounds.
func (s *roleStage) documentExcerpts(selected []envdoc.Doc) (documentEvidence, error) {
	files := s.entries()
	packages := documentPackageResolver{stage: s, files: files}
	documentSources := map[string][]string{}
	paths := make([]string, 0, len(selected))
	bound := 0
	for _, d := range selected {
		e, ok := files[d.Path]
		if !ok || e.SHA256 != *d.SHA256 {
			return documentEvidence{}, errSnapshotIntegrity
		}
		if s.secretFiles()[d.Path] {
			s.rec.Limitations = addLim(s.rec.Limitations, limSecretWithheld)
			continue
		}
		paths = append(paths, d.Path)
		bound += min(roleMaxDocBytes, excerptBound(*e.Size))
	}
	selection := docpath.NewSelection(paths)
	file := func(d envdoc.Doc) (string, string) { return d.Path, d.Class }
	constrained := len(paths) > roleMaxDocs || bound > roleMaxDocsBytes
	fullReview := normalizedScope(s.j.Scope).Kind == "full" && s.j.PullRequest == nil
	spreadReadme := constrained && fullReview && slices.ContainsFunc(selected, func(d envdoc.Doc) bool {
		return selection.Primary(d.Path) && (*d.LineCount > roleMaxDocLines || *files[d.Path].Size > roleMaxDocBytes)
	})
	secondaryRule := s.h.secondary()
	bundled := func(p string) bool {
		if !spreadReadme || selection.Primary(p) || selection.Linked(p) {
			return false
		}
		switch secondaryRule.Match(p) {
		case "fixtures", "testdata", "test-data", "tests/data", "vendor", "third_party", "node_modules":
			return true
		}
		// Keep a project's own test guide at the test directory root.
		// Extra documents in nested test suites compete with the README.
		dirs := strings.Split(strings.ToLower(p), "/")
		return slices.Contains(dirs[:max(0, len(dirs)-2)], "test") || slices.Contains(dirs[:max(0, len(dirs)-2)], "tests")
	}
	secondary := func(p string) bool { return !selection.Linked(p) && secondaryRule.Match(p) != "" }
	order := func(list []envdoc.Doc) {
		docpath.Sort(list, selection, file)
		sort.SliceStable(list, func(i, j int) bool { return !secondary(list[i].Path) && secondary(list[j].Path) })
		if spreadReadme {
			firstParty := func(d envdoc.Doc) bool {
				return !secondary(d.Path) && !bundled(d.Path) && (d.Class == envdoc.ClassAgent || selection.Product(d.Path, d.Class))
			}
			sort.SliceStable(list, func(i, j int) bool { return firstParty(list[i]) && !firstParty(list[j]) })
		}
	}
	if constrained {
		order(selected)
	} else {
		docpath.Sort(selected, nil, file)
	}
	var manifest map[string]source.Entry
	var out []record.Excerpt
	var lims []string
	lists := map[string][]documentRange{}
	remain := roleMaxDocsBytes
	apiExcerpts := 0
	packageExcerpts := 0
	apiEvidence := map[string]bool{}
	var later [2][]userDocument
	var productContinuations []userDocument
	var primary string
	continuedProduct := false
	spreadProduct := false
	for i := range selected {
		d := selected[i]
		e := files[d.Path]
		product := selection.Product(d.Path, d.Class)
		// Give the product text one more window before unlinked test inputs
		// and general guidance compete for the remaining slots.
		if constrained && !continuedProduct && len(productContinuations) > 0 &&
			(secondary(d.Path) || bundled(d.Path) || (!product && d.Class != envdoc.ClassAgent)) {
			continuedProduct = true
			before := len(out)
			out = s.fillDocumentCoverage(productContinuations, primary, out, remain, min(roleMaxDocs, before+1))
			if len(out) > before {
				remain -= record.JSONLen(out[before].Text)
			}
		}
		if !spreadProduct && bundled(d.Path) && primary != "" {
			spreadProduct = true
			if i := slices.IndexFunc(productContinuations, func(doc userDocument) bool { return doc.entry.Path == primary }); i >= 0 {
				before := len(out)
				out = s.compactDocumentCoverage(productContinuations[i], out, remain)
				for _, x := range out[before:] {
					remain -= record.JSONLen(x.Text)
				}
			}
		}
		if len(out) >= roleMaxDocs || remain < 128 {
			lims = append(lims, "documents_outside_review_input")
			continue
		}
		content, ok, err := s.readSource(e)
		if err != nil {
			return documentEvidence{}, err
		}
		if !ok {
			lims = append(lims, "documents_outside_review_input")
			continue
		}
		if selection.WithReadmeLinks(d.Path, content) && constrained {
			order(selected[i+1:])
		}
		lines := strings.Split(content, "\n")
		documentSources[e.Path] = lines
		x, ok := s.boundedExcerpt(e, "document", "", 1, lines[:min(len(lines), roleMaxDocLines)], min(roleMaxDocBytes, remain))
		if !ok {
			lims = append(lims, "documents_outside_review_input")
			continue
		}
		var example record.Excerpt
		if len(out)+1 < roleMaxDocs && packageExcerpts < 2 && product {
			x, example = packages.keepCrossingExample(e, lines, x, remain)
		}
		// Under pressure, a long guide can devote its representative window
		// to a headed user workflow. Keep a README's introduction and any
		// import-and-call examples already in the leading window.
		if constrained && product && d.Class != envdoc.ClassReadme && example.ID == "" && x.End < *d.LineCount {
			budget := min(roleMaxDocBytes, remain)
			start, end := bestDocumentationSection(lines, x.End, path.Ext(d.Path), budget, true)
			if start >= 0 && (!record.PackageGuidePath(d.Path) || len(documentedPackageUses(lines, x.Start, x.End)) == 0) {
				if workflow, ok := s.boundedExcerpt(e, "document", "", start+1, lines[start:end], budget); ok {
					x = workflow
				}
			}
		}
		last := max(x.End, example.End)
		partial := x.Start > 1 || last < *d.LineCount
		if partial {
			switch strings.ToLower(path.Ext(d.Path)) {
			case ".md", ".mdx", ".markdown", "":
				lists[d.Path] = markdownListRanges(lines)
			}
		}
		if partial && product {
			doc := userDocument{entry: e, lines: lines, after: last}
			if !secondary(d.Path) {
				productContinuations = append(productContinuations, doc)
			}
			rank := 1
			if d.Class == envdoc.ClassReadme {
				rank = 0
			}
			later[rank] = append(later[rank], doc)
			if selection.Primary(d.Path) {
				primary = d.Path
			}
		}
		if apiExcerpts < maxAPIDocExcerpts && x.Start == 1 && x.End >= *d.LineCount {
			if target := pythonDocTarget(d.Path, x.Text); target != "" {
				if manifest == nil {
					manifest = make(map[string]source.Entry, len(s.j.Snapshot.Manifest.Entries))
					for _, e := range s.j.Snapshot.Manifest.Entries {
						manifest[e.Path] = e
					}
				}
				api, ok, err := s.documentedPythonAPI(manifest, files, d, target, min(roleMaxDocBytes, remain))
				if err != nil {
					return documentEvidence{}, err
				}
				if ok && !apiEvidence[api.ID] {
					apiEvidence[api.ID] = true
					apiExcerpts++
					x = api
				}
			}
		}
		out = append(out, x)
		remain -= record.JSONLen(x.Text)
		if example.ID != "" {
			out = append(out, example)
			remain -= record.JSONLen(example.Text)
			packageExcerpts++
		}
		if constrained && fullReview && partial && selection.Primary(d.Path) {
			// Put concrete product behavior before unlinked documents that
			// could use the remaining request slots and bytes.
			covered := []documentRange{{0, last}}
			for range 2 {
				budget := min(roleMaxDocBytes, remain)
				start, end := representativeDocumentSection(lines, path.Ext(d.Path), budget, covered)
				if start < 0 || len(out) >= roleMaxDocs {
					break
				}
				excerpt, ok := s.boundedExcerpt(e, "document", "", start+1, lines[start:end], budget)
				if !ok {
					break
				}
				out = append(out, excerpt)
				covered = append(covered, documentRange{start, excerpt.End})
				remain -= record.JSONLen(excerpt.Text)
			}
		}
	}
	if len(out) < roleMaxDocs && remain >= 128 {
		budget := min(roleMaxDocBytes, remain)
	preferred:
		for _, ranked := range later {
			for _, d := range ranked {
				after := d.after
				for _, x := range out {
					if x.DirectivePath == "" && x.Path == d.entry.Path {
						after = max(after, x.End)
					}
				}
				start, end := documentationContinuation(d.lines, after, path.Ext(d.entry.Path), budget)
				if start >= 0 {
					for _, r := range documentationRanges(d.lines, path.Ext(d.entry.Path), start, end, roleMaxDocs-len(out)) {
						if x, ok := s.boundedExcerpt(d.entry, "document", "", r.start+1, d.lines[r.start:r.end], budget); ok {
							out = append(out, x)
							cost := record.JSONLen(x.Text)
							budget -= cost
							remain -= cost
						}
					}
					break preferred
				}
			}
		}
	}
	out = s.fillDocumentCoverage(slices.Concat(later[0], later[1]), primary, out, remain, roleMaxDocs)
	if err := packages.attach(out, documentSources); err != nil {
		return documentEvidence{}, err
	}
	return documentEvidence{excerpts: out, lists: lists, limitations: addLim(nil, lims...)}, nil
}

// Excerpts from a document do not overlap. A Python docstring replaces a
// complete directive page, so its source line count is irrelevant here.
func partialDocumentInput(docs []envdoc.Doc, excerpts []record.Excerpt) bool {
	covered := map[string]int{}
	for _, e := range excerpts {
		if e.DirectivePath == "" {
			covered[e.Path] += e.End - e.Start + 1
		}
	}
	for _, d := range docs {
		if n := covered[d.Path]; n > 0 && n < *d.LineCount {
			return true
		}
	}
	return false
}

// existingDir reports whether p is "." or a directory holding an included
// file of the snapshot (a path a contract can apply to).
func (s *roleStage) existingDir(p string) bool {
	if p == "." {
		return true
	}
	if !record.ValidScopePath(p) {
		return false
	}
	for q := range s.entries() {
		if strings.HasPrefix(q, p+"/") {
			return true
		}
	}
	return false
}

func marshal(v any) ([]byte, error) { return json.Marshal(v) }
