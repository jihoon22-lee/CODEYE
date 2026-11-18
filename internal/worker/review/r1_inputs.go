package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Bounds of the review-r1 inputs, in JSON-encoded bytes of excerpt text
// (encoding/json escapes <, > and & to six bytes). Every request must also
// fit PromptBudget; roles trim excerpts to fit and record it.
const (
	r1MaxCodeFiles    = 8
	r1MaxCodeFile     = 2400
	r1MaxCodeBytes    = 12000
	r1MaxDocs         = 8
	r1MaxDocBytes     = 2000
	r1MaxDocLines     = 40
	r1MaxDocsBytes    = 10000
	r1MaxRequirements = 16
	r1MaxObservations = 8
	r1MaxTools        = 8
	r1MaxWindows      = 4
	r1WindowRadius    = 10
	r1MaxWindowBytes  = 1600
)

func normalizedScope(s domain.Scope) domain.Scope {
	if s.Kind == "" {
		return domain.Scope{Kind: "full"}
	}
	return s
}

func (s *r1Stage) entries() map[string]source.Entry {
	out := map[string]source.Entry{}
	for _, e := range s.j.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil {
			out[e.Path] = e
		}
	}
	return out
}

// readLines returns lines start..end (1-based, inclusive) of a verified
// snapshot blob, or ok=false when the blob is not safe model input.
func (s *r1Stage) readLines(e source.Entry, start, end int) (string, int, bool, error) {
	if *e.Size > maxFileBytes || domain.ValidateEvidencePath(e.Path) != nil {
		return "", 0, false, nil
	}
	if s.secretFiles()[e.Path] {
		s.rec.Limitations = addLim(s.rec.Limitations, limSecretWithheld)
		return "", 0, false, nil
	}
	root, err := os.OpenRoot(s.j.Snapshot.SnapshotDir)
	if err != nil {
		return "", 0, false, err
	}
	defer root.Close()
	b, err := readVerified(root, e)
	if err != nil {
		return "", 0, false, err
	}
	if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 || record.Sensitive(b) {
		return "", 0, false, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if start < 1 || start > len(lines) {
		return "", len(lines), false, nil
	}
	end = min(end, len(lines))
	return strings.Join(lines[start-1:end], "\n"), len(lines), true, nil
}

// secretFiles are the files the secrets screen withholds from every model
// input of the stage (screen.go): code context, documents, observation
// windows and verification expansions.
func (s *r1Stage) secretFiles() map[string]bool { return s.screen.withheld }

// excerpt builds a bounded excerpt of up to maxLines lines and maxBytes of
// JSON-encoded text from line start. total is the file's line count.
func (s *r1Stage) excerpt(e source.Entry, kind, origin string, start, maxLines, maxBytes int) (x record.Excerpt, total int, ok bool, err error) {
	text, total, ok, err := s.readLines(e, start, start+maxLines-1)
	if err != nil || !ok {
		return record.Excerpt{}, total, false, err
	}
	var kept []string
	used := 0
	for _, line := range strings.Split(text, "\n") {
		cost := record.JSONLen(line)
		if used+cost > maxBytes {
			break
		}
		kept = append(kept, line)
		used += cost
	}
	if len(kept) == 0 || strings.TrimSpace(strings.Join(kept, "")) == "" {
		return record.Excerpt{}, total, false, nil
	}
	end := start + len(kept) - 1
	return record.Excerpt{ID: record.ExcerptID(s.j.Snapshot.Digest, e.Path, e.SHA256, start, end), Path: e.Path, SHA256: e.SHA256, Start: start, End: end,
		Kind: kind, Origin: origin, Text: strings.Join(kept, "\n")}, total, true, nil
}

// withText re-reads the text of record excerpts from the snapshot, checking
// each excerpt's ID binding again.
func (s *r1Stage) withText(list []record.Excerpt) ([]record.Excerpt, error) {
	files := s.entries()
	out := make([]record.Excerpt, 0, len(list))
	for _, x := range list {
		e, ok := files[x.Path]
		if !ok || e.SHA256 != x.SHA256 || x.ID != record.ExcerptID(s.j.Snapshot.Digest, x.Path, x.SHA256, x.Start, x.End) {
			return nil, errSnapshotIntegrity
		}
		text, _, ok, err := s.readLines(e, x.Start, x.End)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errSnapshotIntegrity
		}
		x.Text = text
		out = append(out, x)
	}
	return out, nil
}

var errSnapshotIntegrity = os.ErrInvalid

// ---- discovery artifacts -------------------------------------------------

func (s *r1Stage) discoveryDoc(kind string) (envdoc.Document, record.Upstream) {
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
func (s *r1Stage) environmentFacts() (envFacts, string) {
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

var docOrder = map[string]int{envdoc.ClassAgent: 0, envdoc.ClassReadme: 1, envdoc.ClassContributing: 2, envdoc.ClassDesign: 3}

// documentFacts applies the documents role's skip rules: no guidance
// document in scope (no_documents), discovery unavailable (dependency_failed)
// or none of them selected under the read budget (documents_not_selected).
func (s *r1Stage) documentFacts() (docFacts, string) {
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
	sort.SliceStable(f.selected, func(a, b int) bool {
		x, y := f.selected[a], f.selected[b]
		if docOrder[x.Class] != docOrder[y.Class] {
			return docOrder[x.Class] < docOrder[y.Class]
		}
		if strings.Count(x.Path, "/") != strings.Count(y.Path, "/") {
			return strings.Count(x.Path, "/") < strings.Count(y.Path, "/")
		}
		return x.Path < y.Path
	})
	if len(f.selected) == 0 {
		return f, ReasonDocsNotSelected
	}
	return f, ""
}

// ---- analyzer observations -------------------------------------------------

// toolLocation and toolObservation are the analyzer-independent view of one
// observation; toolSummary of one analyzer task (go vet v0.1 or the generic
// envelope of docs/39).
type toolLocation struct {
	Path               string
	StartLine, EndLine int
}

type toolObservation struct {
	ID, Tool, RuleID, RawMessage, LocationState string
	Location                                    *toolLocation
}

type toolSummary struct {
	Tool, Module, TaskStatus, ExitClass string
	ToolVersion                         *string
	PartialParse, ParseOK               bool
	Limitations                         []string
	Observations                        []toolObservation
}

type toolDoc struct {
	key      string
	doc      toolSummary
	upstream record.Upstream
}

// readToolDoc reads and validates one analyzer task's observations artifact:
// the generic envelope at obsdoc.ArtifactPath, else a go vet v0.1 document.
func (s *r1Stage) readToolDoc(key string, files map[string]source.Entry) (toolSummary, []byte, string) {
	if b, err := readBounded(obsdoc.ArtifactPath(s.j.Scratch, key), obsdoc.MaxBytes); err == nil {
		lookup := func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }
		d, err := obsdoc.Decode(b, obsdoc.Expect{TaskKey: key, Snapshot: s.j.Snapshot.Digest, Lookup: lookup})
		if err != nil {
			return toolSummary{}, nil, reasonRecordInvalid
		}
		sum := toolSummary{Tool: d.Analyzer.ID, ToolVersion: d.Tool.Version, Module: d.Module.Dir, TaskStatus: string(d.Outcome.TaskStatus), ExitClass: d.Outcome.ExitClass,
			PartialParse: d.Outcome.PartialParse, ParseOK: d.Parse.OK, Limitations: d.Limitations}
		for _, o := range d.Observations {
			// A diagnostic the tool reported as suppressed in source is the
			// project's decision, not a candidate: it never reaches the model
			// (the envelope's source_suppressed_observations limitation says
			// some were left out) and never becomes a finding (docs/39 §5.1).
			if o.Suppressed {
				continue
			}
			// A structure metric (W21) is no defect candidate: the envelope's
			// metrics_not_defects limitation is all the model sees of it.
			if o.Metric != nil {
				continue
			}
			to := toolObservation{ID: o.ID, Tool: d.Analyzer.ID, RuleID: o.RuleID, RawMessage: o.RawMessage, LocationState: o.LocationState}
			if o.Location != nil {
				to.Location = &toolLocation{Path: o.Location.Path, StartLine: o.Location.StartLine, EndLine: o.Location.EndLine}
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
		PartialParse: d.Outcome.PartialParse, ParseOK: d.Parse.OK, Limitations: d.Limitations}
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
func (s *r1Stage) toolFacts() ([]toolDoc, int, string) {
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

// codeCorpus selects code and test excerpts round-robin over the modules of
// the snapshot, so every module in scope is represented before any module
// gets a second file. A Go file belongs to its deepest go.mod directory; a
// file of another ecosystem to its deepest build manifest directory of that
// ecosystem, and modules are visited ecosystem by ecosystem (sources.go,
// groupOrder). Every excerpt is the first lines of its file.
func (s *r1Stage) codeCorpus() ([]record.Excerpt, []string, error) {
	files := s.entries()
	scope := normalizedScope(s.j.Scope)
	var modules []string
	for p := range files {
		if path.Base(p) == "go.mod" && !goIgnored(p) {
			modules = append(modules, record.DocumentDir(p))
		}
	}
	sort.Strings(modules)
	owner := func(p string) string {
		best := "."
		for _, m := range modules {
			if record.ScopeUnder(p, m) && len(m) >= len(best) {
				best = m
			}
		}
		return best
	}
	byModule := map[string][]source.Entry{}
	others := otherModuleRoots(files)
	eligible := 0
	for _, p := range sortedKeys(files) {
		e := files[p]
		if !inScope(p, scope) || *e.Size > maxFileBytes {
			continue
		}
		m := ""
		switch eco := otherSource(p); {
		case strings.HasSuffix(p, ".go") && !goIgnored(p):
			m = owner(p)
		case !strings.HasSuffix(p, ".go") && eco != "":
			m = groupKey(others.owner(eco, p), eco)
		default:
			continue
		}
		eligible++
		byModule[m] = append(byModule[m], e)
	}
	rank := func(p string) int {
		switch {
		case !strings.HasSuffix(p, ".go"):
			return otherRank(p)
		case path.Base(p) == "main.go":
			return 0
		case strings.HasSuffix(p, "_test.go"):
			return 2
		}
		return 1
	}
	order := groupOrder(byModule)
	for _, m := range order {
		list := byModule[m]
		sort.SliceStable(list, func(a, b int) bool {
			x, y := list[a].Path, list[b].Path
			if rank(x) != rank(y) {
				return rank(x) < rank(y)
			}
			if strings.Count(x, "/") != strings.Count(y, "/") {
				return strings.Count(x, "/") < strings.Count(y, "/")
			}
			return x < y
		})
	}
	var out []record.Excerpt
	var lims []string
	remain := r1MaxCodeBytes
	for round := 0; len(out) < r1MaxCodeFiles && remain >= 128; round++ {
		progressed := false
		for _, m := range order {
			if round >= len(byModule[m]) || len(out) >= r1MaxCodeFiles || remain < 128 {
				continue
			}
			progressed = true
			e := byModule[m][round]
			kind := windowKind(e.Path)
			x, total, ok, err := s.excerpt(e, kind, "context", 1, 50, min(r1MaxCodeFile, remain))
			if err != nil {
				return nil, nil, err
			}
			if !ok {
				continue
			}
			if x.End < total {
				lims = append(lims, "excerpt_only")
			}
			out = append(out, x)
			remain -= record.JSONLen(x.Text)
		}
		if !progressed {
			break
		}
	}
	if eligible > len(out) {
		lims = append(lims, "files_outside_review_context")
	}
	return out, addLim(nil, lims...), nil
}

// observation input of the interpret role
type obsInput struct {
	ID            string   `json:"id"`
	TaskKey       string   `json:"task_key"`
	RuleID        string   `json:"rule_id"`
	Message       string   `json:"message"`
	Path          string   `json:"path,omitempty"`
	StartLine     int      `json:"start_line,omitempty"`
	EndLine       int      `json:"end_line,omitempty"`
	LocationState string   `json:"location_state"`
	EvidenceIDs   []string `json:"evidence_ids"`
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
// r1MaxObservations observations and code windows around their locations.
func (s *r1Stage) observationWindows(docs []toolDoc) ([]toolInput, []obsInput, []record.ObservationRef, []record.Excerpt, []string, error) {
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
		if len(tools) >= r1MaxTools {
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
	if len(all) > r1MaxObservations {
		all = all[:r1MaxObservations]
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
		if o.Location != nil && s.secretFiles()[o.Location.Path] && in.Message != "(omitted by the platform)" {
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
			case len(windows) >= r1MaxWindows:
				lims = append(lims, "observation_context_outside_input")
			case windowKind(l.Path) != "" && files[l.Path].Size != nil:
				// A line window around the location in any source language
				// (no parser); other files (manifests, configuration) get none.
				kind := windowKind(l.Path)
				x, _, ok, err := s.excerpt(files[l.Path], kind, "observation", max(1, l.StartLine-r1WindowRadius), 2*r1WindowRadius+1, r1MaxWindowBytes)
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
	return tools, obs, refs, windows, addLim(nil, lims...), nil
}

// documentExcerpts reads the selected guidance documents within the bounds.
func (s *r1Stage) documentExcerpts(selected []envdoc.Doc) ([]record.Excerpt, []string, error) {
	files := s.entries()
	var out []record.Excerpt
	var lims []string
	remain := r1MaxDocsBytes
	for _, d := range selected {
		e, ok := files[d.Path]
		if !ok || e.SHA256 != *d.SHA256 {
			return nil, nil, errSnapshotIntegrity
		}
		if len(out) >= r1MaxDocs || remain < 128 {
			lims = append(lims, "documents_outside_review_input")
			continue
		}
		x, _, ok, err := s.excerpt(e, "document", "", 1, r1MaxDocLines, min(r1MaxDocBytes, remain))
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			lims = append(lims, "documents_outside_review_input")
			continue
		}
		if x.End < *d.LineCount {
			lims = append(lims, "document_excerpt_only")
		}
		out = append(out, x)
		remain -= record.JSONLen(x.Text)
	}
	return out, addLim(nil, lims...), nil
}

// existingDir reports whether p is "." or a directory holding an included
// file of the snapshot (a path a contract can apply to).
func (s *r1Stage) existingDir(p string) bool {
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
