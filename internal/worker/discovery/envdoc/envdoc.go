// Package envdoc defines the W15 environment/documents discovery artifact
// (application/vnd.codeye.environment+json, schema 0.1). It performs no I/O
// and imports no process code, so a report decoder can validate an uploaded
// artifact against the snapshot manifest alone. The worker produces the
// documents in internal/worker/discovery; nothing here comes from a model.
package envdoc

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const (
	MediaType     = "application/vnd.codeye.environment+json"
	SchemaVersion = "0.1"
	MaxBytes      = 4 << 20

	// Kind selects the section a task wrote. env.discover writes an
	// "environment" document, docs.discover a "documents" document; both
	// carry the same module map.
	KindEnvironment = "environment"
	KindDocuments   = "documents"

	TaskEnvironment = "env.discover"
	TaskDocuments   = "docs.discover"

	MaxModules   = 256
	MaxDocuments = 1000
)

// Ecosystems a module may belong to.
const (
	EcoGo         = "go"
	EcoJavaScript = "javascript"
	EcoPython     = "python"
	EcoRust       = "rust"
	EcoCpp        = "cpp"
)

// Observation statuses (docs/37 §5).
const (
	ObsObserved    = "observed"
	ObsNotProbed   = "not_probed"
	ObsNotFound    = "not_found"
	ObsProbeFailed = "probe_failed"
)

// Verdict statuses and unknown reasons.
const (
	VerdictSatisfied = "satisfied"
	VerdictMismatch  = "mismatch"
	VerdictUnknown   = "unknown"

	ReasonNotDeclared           = "not_declared"
	ReasonNotObserved           = "not_observed"
	ReasonUnsupportedConstraint = "unsupported_constraint"
	// ReasonDeclarationUnreadable extends the docs/37 §5 reasons: no
	// requirement could be read because a manifest that may declare the tool
	// is malformed, not UTF-8 or too large. Reporting not_declared there
	// would be a false fact.
	ReasonDeclarationUnreadable = "declaration_unreadable"
)

// Requirement comparisons. preferred_minimum is the Go toolchain directive:
// with GOTOOLCHAIN=local it is a preference, not a gate for go vet.
const (
	CompareMinimum          = "minimum"
	ComparePreferredMinimum = "preferred_minimum"
	ComparePrefix           = "prefix"
	CompareUnsupported      = "unsupported"
)

// Manifest read statuses in an environment document.
const (
	ManifestParsed    = "parsed"
	ManifestNoParser  = "no_requirement_parser"
	ManifestMalformed = "malformed"
	ManifestNotUTF8   = "not_utf8"
	ManifestTooLarge  = "too_large"

	// Document statuses, reasons, overlap assessment and module states.
	DocSelected          = "selected"
	DocBudgetExceeded    = "budget_exceeded"
	DocExcluded          = "excluded"
	DocUnreadable        = "unreadable"
	ReasonOutOfScope     = "out_of_scope"
	ReasonCredentialLike = "credential_like_content"
	ReasonTooLarge       = "document_too_large"
	ReasonBudget         = "read_budget_exhausted"
	ReasonNotUTF8        = "not_utf8"
	ReasonNUL            = "contains_nul"
	ReasonLFS            = "lfs_object_not_acquired"
	ReasonNotRegular     = "not_a_regular_file"
	AssessmentNoModel    = "not_assessed_without_model"
	StateOwn             = "own"
	StateInherited       = "inherited"
	StateNone            = "none"
)

// Document classes. Guidance classes take part in module attachment and
// overlap grouping; "other" documents are inventoried only.
const (
	ClassReadme       = "readme"
	ClassAgent        = "agent_instructions"
	ClassDesign       = "design"
	ClassContributing = "contributing"
	ClassOther        = "other"
)

// Document is one discovery artifact.
type Document struct {
	SchemaVersion string       `json:"schema_version"`
	Kind          string       `json:"kind"`
	Snapshot      string       `json:"snapshot_manifest_digest"`
	RunID         string       `json:"run_id"`
	AttemptID     string       `json:"attempt_id"`
	Scope         Scope        `json:"scope"`
	Modules       []Module     `json:"modules"`
	Environment   *Environment `json:"environment,omitempty"`
	Documents     *Documents   `json:"documents,omitempty"`
	Limitations   []string     `json:"limitations"`
}

// Scope is the run scope the document was derived under. An empty job scope
// kind is recorded as "full", like the go-vet planner treats it.
type Scope struct {
	Kind  string   `json:"kind"`
	Paths []string `json:"paths"`
}

// Module is a manifest directory of one ecosystem. Its scope is Path minus
// ExcludedPaths (nested modules of the same ecosystem).
type Module struct {
	ID            string   `json:"id"`
	Ecosystem     string   `json:"ecosystem"`
	Path          string   `json:"path"`
	Manifests     []string `json:"manifests"`
	ExcludedPaths []string `json:"excluded_paths"`
	InScope       bool     `json:"in_scope"`
}

// Environment holds declarations, node observations and platform verdicts.
type Environment struct {
	Manifests    []ManifestRead `json:"manifests"`
	Requirements []Requirement  `json:"requirements"`
	Observations []Observation  `json:"observations"`
	Verdicts     []Verdict      `json:"verdicts"`
	// AnalyzerTools are the node's analyzer tools (docs/39) for the
	// in-scope ecosystems: probed when the operator configured them,
	// not_probed otherwise. They are not compared with declarations.
	// Absent in documents written before W19.
	AnalyzerTools []AnalyzerTool `json:"analyzer_tools,omitempty"`
}

// AnalyzerTool is this attempt's node observation of one analyzer tool
// (staticcheck, ruff, ...). Version is the tool's own version text; the
// execution, when set, is named "<tool>.version".
type AnalyzerTool struct {
	Tool      string   `json:"tool"`
	Analyzers []string `json:"analyzers"`
	Status    string   `json:"status"`
	Version   *string  `json:"version"`
	Detail    *string  `json:"detail"`
	Execution *string  `json:"execution"`
}

// ManifestRead is how one manifest of an in-scope module was read.
type ManifestRead struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Status string `json:"status"`
}

// Location is a line range of one snapshot blob.
type Location struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// Requirement is one declared environment constraint with its source.
// EvidenceID follows record.ExcerptID so later roles can cite it.
type Requirement struct {
	ID         string   `json:"id"`
	Module     string   `json:"module"`
	Tool       string   `json:"tool"`
	Kind       string   `json:"kind"`
	Constraint string   `json:"constraint"`
	Comparison string   `json:"comparison"`
	Source     Location `json:"source"`
	EvidenceID string   `json:"evidence_id"`
}

// Observation is what this attempt's node reported for one tool. Execution
// names the probe execution in the task's result manifest.
type Observation struct {
	Tool      string  `json:"tool"`
	Status    string  `json:"status"`
	Version   *string `json:"version"`
	Detail    *string `json:"detail"`
	Execution *string `json:"execution"`
}

// Verdict compares one module requirement (or its absence) with the node
// observation of the same tool. The platform, not a model, sets it.
type Verdict struct {
	Module      string  `json:"module"`
	Tool        string  `json:"tool"`
	Requirement *string `json:"requirement"`
	Status      string  `json:"status"`
	Reason      *string `json:"reason"`
}

// Documents is the document inventory under an explicit read budget.
type Documents struct {
	Budget              Budget       `json:"budget"`
	Items               []Doc        `json:"items"`
	Modules             []ModuleDocs `json:"modules"`
	OverlappingGuidance []Overlap    `json:"overlapping_guidance"`
}

// Budget bounds the bytes discovery reads. ReadBytes counts the documents
// whose content was read (selected, contains_nul, credential_like_content).
type Budget struct {
	TotalBytes       int64 `json:"total_bytes"`
	PerDocumentBytes int64 `json:"per_document_bytes"`
	MaxDocuments     int   `json:"max_documents"`
	ReadBytes        int64 `json:"read_bytes"`
}

// Doc is one inventoried document. SizeBytes/SHA256 are set only for included
// regular files; LineCount only for selected documents.
type Doc struct {
	Path      string  `json:"path"`
	AppliesTo string  `json:"applies_to"`
	Class     string  `json:"class"`
	Status    string  `json:"status"`
	Reason    *string `json:"reason"`
	SizeBytes *int64  `json:"size_bytes"`
	SHA256    *string `json:"sha256"`
	LineCount *int    `json:"line_count"`
}

// ModuleDocs attaches guidance documents to a module: own documents lie in
// the module scope, inherited ones apply from an ancestor directory.
type ModuleDocs struct {
	Module    string   `json:"module"`
	State     string   `json:"state"`
	Own       []string `json:"own"`
	Inherited []string `json:"inherited"`
}

// Overlap groups guidance documents that apply to the same path. Whether they
// conflict is a model (documents role) judgement, so without a model it stays
// not_assessed_without_model.
type Overlap struct {
	Path       string   `json:"path"`
	Documents  []string `json:"documents"`
	Assessment string   `json:"assessment"`
}

// ToolOf maps a requirement kind to its tool.
var ToolOf = map[string]string{
	"go_directive": "go", "go_toolchain": "go", "go_work_directive": "go", "go_work_toolchain": "go",
	"engines_node":    "node",
	"requires_python": "python", "python_requires": "python", "python_version_file": "python",
	"rust_version": "rust", "rust_toolchain_channel": "rust",
	"cmake_minimum_required": "cmake",
}

// PrimaryTools are the tools every module of an ecosystem gets a verdict for,
// declared or not.
var PrimaryTools = map[string][]string{
	EcoGo: {"go"}, EcoJavaScript: {"node"}, EcoPython: {"python"}, EcoRust: {"rust"}, EcoCpp: {"c_cpp_compiler"},
}

var (
	ecosystems = []string{EcoCpp, EcoGo, EcoJavaScript, EcoPython, EcoRust}
	tools      = []string{"c_cpp_compiler", "cmake", "go", "node", "python", "rust"}
	classes    = []string{ClassAgent, ClassContributing, ClassDesign, ClassOther, ClassReadme}
	manifestSt = []string{ManifestParsed, ManifestNoParser, ManifestMalformed, ManifestNotUTF8, ManifestTooLarge}
	obsStatus  = []string{ObsObserved, ObsNotProbed, ObsNotFound, ObsProbeFailed}
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	evidenceID = regexp.MustCompile(`^ev_[0-9a-f]{24}$`)
	execName   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	// analyzer tools: names, analyzer ids and version text (no path).
	analyzerToolName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	analyzerID       = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+$`)
	toolVersionText  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._+():-]{0,79}$`)
)

// AnalyzerToolVersionValid reports whether v is acceptable analyzer tool
// version text: short, printable, no path separator.
func AnalyzerToolVersionValid(v string) bool { return toolVersionText.MatchString(v) }

func checkAnalyzerTools(list []AnalyzerTool) error {
	for i, t := range list {
		observed := t.Status == ObsObserved
		switch {
		case i > 0 && list[i-1].Tool >= t.Tool, !analyzerToolName.MatchString(t.Tool), slices.Contains(tools, t.Tool), !slices.Contains(obsStatus, t.Status):
			return invalid("analyzer tool %q", t.Tool)
		case len(t.Analyzers) == 0 || !sortedUnique(t.Analyzers, analyzerID.MatchString):
			return invalid("analyzer tool %q analyzers", t.Tool)
		case observed != (t.Version != nil), t.Version != nil && !AnalyzerToolVersionValid(*t.Version):
			return invalid("analyzer tool %q version", t.Tool)
		case (observed || t.Status == ObsNotProbed || t.Status == ObsNotFound) && t.Detail != nil, t.Status == ObsProbeFailed && (t.Detail == nil || !record.ValidCode(*t.Detail)):
			return invalid("analyzer tool %q detail", t.Tool)
		case t.Execution != nil && *t.Execution != t.Tool+".version", t.Status == ObsNotProbed && t.Execution != nil, observed && t.Execution == nil:
			return invalid("analyzer tool %q execution", t.Tool)
		}
	}
	return nil
}

// Expect is what a consumer already trusts: the attempt identity and the
// immutable snapshot manifest. Lookup returns the manifest sha256 of an
// included file entry. Kind, when set, is the kind the task must have written.
type Expect struct {
	Snapshot, RunID, AttemptID string
	Kind                       string
	Lookup                     func(path string) (sha256 string, ok bool)
}

var errInvalid = errors.New("environment document invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", errInvalid, fmt.Sprintf(format, a...))
}

// ValidPath accepts "." or a snapshot-relative evidence path.
func ValidPath(p string) bool { return p == "." || domain.ValidateEvidencePath(p) == nil }

// Dir is the directory of a snapshot-relative path ("." for the root).
func Dir(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

// Under reports whether p is dir or lies below it.
func Under(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

// ModuleID is "<ecosystem>:<path>".
func ModuleID(eco, dir string) string { return eco + ":" + dir }

// RequirementID is "<kind>@<path>"; a manifest declares each kind once.
func RequirementID(kind, p string) string { return kind + "@" + p }

// Decode strictly parses and validates a discovery artifact.
func Decode(body []byte, want Expect) (Document, error) {
	var d Document
	if len(body) > MaxBytes {
		return Document{}, invalid("document too large")
	}
	if err := domain.DecodeStrict(body, &d); err != nil {
		return Document{}, invalid("not strict JSON: %v", err)
	}
	if err := Validate(d, want); err != nil {
		return Document{}, err
	}
	return d, nil
}

// Marshal validates before encoding canonical JSON, so the worker never
// writes a document the decoder would reject.
func Marshal(d Document, want Expect) ([]byte, error) {
	if err := Validate(d, want); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, invalid("encoding: %v", err)
	}
	b, err := domain.CanonicalJSON(raw)
	if err != nil || len(b) > MaxBytes {
		return nil, invalid("encoding")
	}
	return b, nil
}

func codes(list []string) bool {
	if list == nil {
		return false
	}
	for i, s := range list {
		if !record.ValidCode(s) || (i > 0 && list[i-1] >= s) {
			return false
		}
	}
	return true
}

func sortedUnique(list []string, valid func(string) bool) bool {
	if list == nil {
		return false
	}
	for i, s := range list {
		if !valid(s) || (i > 0 && list[i-1] >= s) {
			return false
		}
	}
	return true
}

// Validate applies Decode's rules to an in-memory document.
func Validate(d Document, want Expect) error {
	switch {
	case d.SchemaVersion != SchemaVersion:
		return invalid("schema_version")
	case d.Kind != KindEnvironment && d.Kind != KindDocuments, want.Kind != "" && want.Kind != d.Kind:
		return invalid("kind")
	case !hex64.MatchString(d.Snapshot) || !domain.ValidID(d.RunID) || !domain.ValidID(d.AttemptID):
		return invalid("snapshot or attempt identifiers")
	case want.Lookup == nil || d.Snapshot != want.Snapshot || d.RunID != want.RunID || d.AttemptID != want.AttemptID:
		return invalid("document belongs to another snapshot or attempt")
	case d.Modules == nil || len(d.Modules) > MaxModules || !codes(d.Limitations):
		return invalid("modules or limitations")
	case (d.Kind == KindEnvironment) != (d.Environment != nil) || (d.Kind == KindDocuments) != (d.Documents != nil):
		return invalid("section does not match kind")
	}
	if err := checkScope(d.Scope); err != nil {
		return err
	}
	mods := map[string]Module{}
	for i, m := range d.Modules {
		if i > 0 {
			p := d.Modules[i-1]
			if p.Path > m.Path || (p.Path == m.Path && p.Ecosystem >= m.Ecosystem) {
				return invalid("modules are not sorted")
			}
		}
		if !slices.Contains(ecosystems, m.Ecosystem) || !ValidPath(m.Path) || m.ID != ModuleID(m.Ecosystem, m.Path) || len(m.Manifests) == 0 {
			return invalid("module %q", m.ID)
		}
		if !sortedUnique(m.Manifests, func(s string) bool {
			_, ok := want.Lookup(s)
			return domain.ValidateEvidencePath(s) == nil && ok && Under(s, m.Path) && (m.Ecosystem == EcoCpp || Dir(s) == m.Path)
		}) {
			return invalid("module %q manifests", m.ID)
		}
		if !sortedUnique(m.ExcludedPaths, func(s string) bool { return domain.ValidateEvidencePath(s) == nil && s != m.Path && Under(s, m.Path) }) {
			return invalid("module %q excluded paths", m.ID)
		}
		mods[m.ID] = m
	}
	if d.Environment != nil {
		return checkEnvironment(d, *d.Environment, mods, want)
	}
	return checkDocuments(*d.Documents, d.Modules, want)
}

func checkScope(s Scope) error {
	switch s.Kind {
	case "full":
		if s.Paths == nil || len(s.Paths) != 0 {
			return invalid("full scope has paths")
		}
	case "paths":
		if len(s.Paths) == 0 || len(s.Paths) > 100 || !sortedUnique(s.Paths, func(p string) bool { return domain.ValidateEvidencePath(p) == nil }) {
			return invalid("scope paths")
		}
	default:
		return invalid("scope kind")
	}
	return nil
}

func checkEnvironment(d Document, e Environment, mods map[string]Module, want Expect) error {
	if e.Manifests == nil || e.Requirements == nil || e.Observations == nil || e.Verdicts == nil {
		return invalid("environment lists")
	}
	manifestOf := map[string]string{} // manifest path -> module id
	for _, m := range d.Modules {
		for _, p := range m.Manifests {
			manifestOf[p] = m.ID
		}
	}
	read := map[string]ManifestRead{}
	for i, r := range e.Manifests {
		sha, ok := want.Lookup(r.Path)
		mod, known := manifestOf[r.Path]
		if !ok || sha != r.SHA256 || !known || !mods[mod].InScope || !slices.Contains(manifestSt, r.Status) || (i > 0 && e.Manifests[i-1].Path >= r.Path) {
			return invalid("manifest %q", r.Path)
		}
		read[r.Path] = r
	}
	reqs := map[string]Requirement{}
	for i, r := range e.Requirements {
		m, ok := mods[r.Module]
		src := r.Source
		switch {
		case i > 0 && e.Requirements[i-1].ID >= r.ID:
			return invalid("requirements are not sorted")
		case !ok || !m.InScope || ToolOf[r.Kind] == "" || ToolOf[r.Kind] != r.Tool || r.ID != RequirementID(r.Kind, src.Path):
			return invalid("requirement %q", r.ID)
		case read[src.Path].Status != ManifestParsed || manifestOf[src.Path] != r.Module || read[src.Path].SHA256 != src.SHA256:
			return invalid("requirement %q source", r.ID)
		case src.StartLine < 1 || src.EndLine < src.StartLine || len(r.Constraint) > 256 || !utf8Safe(r.Constraint) || r.Comparison != Classify(r.Kind, r.Constraint):
			return invalid("requirement %q attributes", r.ID)
		case !evidenceID.MatchString(r.EvidenceID) || r.EvidenceID != record.ExcerptID(d.Snapshot, src.Path, src.SHA256, src.StartLine, src.EndLine):
			return invalid("requirement %q evidence id is not bound to its location", r.ID)
		}
		reqs[r.ID] = r
	}
	obs := map[string]Observation{}
	for i, o := range e.Observations {
		observed := o.Status == ObsObserved
		switch {
		case i > 0 && e.Observations[i-1].Tool >= o.Tool, !slices.Contains(tools, o.Tool), !slices.Contains(obsStatus, o.Status):
			return invalid("observation %q", o.Tool)
		case observed != (o.Version != nil), o.Version != nil && !ComparableObservation(o.Tool, *o.Version):
			return invalid("observation %q version", o.Tool)
		case (observed || o.Status == ObsNotProbed || o.Status == ObsNotFound) && o.Detail != nil, o.Status == ObsProbeFailed && (o.Detail == nil || !record.ValidCode(*o.Detail)):
			return invalid("observation %q detail", o.Tool)
		case o.Execution != nil && !execName.MatchString(*o.Execution), o.Status == ObsNotProbed && o.Execution != nil:
			return invalid("observation %q execution", o.Tool)
		}
		obs[o.Tool] = o
	}
	if err := checkAnalyzerTools(e.AnalyzerTools); err != nil {
		return err
	}
	for i, v := range e.Verdicts {
		if i > 0 {
			p := e.Verdicts[i-1]
			if p.Module > v.Module || (p.Module == v.Module && (p.Tool > v.Tool || (p.Tool == v.Tool && deref(p.Requirement) >= deref(v.Requirement)))) {
				return invalid("verdicts are not sorted")
			}
		}
		m, ok := mods[v.Module]
		o, seen := obs[v.Tool]
		if !ok || !m.InScope || !seen {
			return invalid("verdict for %q/%q", v.Module, v.Tool)
		}
		var r *Requirement
		if v.Requirement != nil {
			req, ok := reqs[*v.Requirement]
			if !ok || req.Module != v.Module || req.Tool != v.Tool {
				return invalid("verdict requirement %q", *v.Requirement)
			}
			r = &req
		}
		exp := expectedVerdict(r, o)
		if exp == ReasonNotDeclared && unreadableDeclaration(m, v.Tool, read) {
			exp = ReasonDeclarationUnreadable
		}
		switch {
		case exp != "compare":
			if v.Status != VerdictUnknown || v.Reason == nil || *v.Reason != exp {
				return invalid("verdict %q/%q must be unknown with reason %s", v.Module, v.Tool, exp)
			}
		case v.Reason != nil || v.Status != Decide(r.Kind, r.Constraint, *o.Version):
			return invalid("verdict %q/%q does not follow the comparison", v.Module, v.Tool)
		}
	}
	return nil
}

// DeclaringTool maps a manifest base name to the tool it may declare a
// requirement for ("" when it declares none).
func DeclaringTool(base string) string {
	switch base {
	case "go.mod", "go.work":
		return "go"
	case "package.json":
		return "node"
	case "pyproject.toml", "setup.cfg", ".python-version":
		return "python"
	case "Cargo.toml", "rust-toolchain", "rust-toolchain.toml":
		return "rust"
	case "CMakeLists.txt":
		return "cmake"
	}
	return ""
}

// unreadableDeclaration reports whether a manifest of m that may declare tool
// could not be read or parsed.
func unreadableDeclaration(m Module, tool string, read map[string]ManifestRead) bool {
	for _, p := range m.Manifests {
		st := read[p].Status
		if DeclaringTool(path.Base(p)) == tool && (st == ManifestMalformed || st == ManifestNotUTF8 || st == ManifestTooLarge) {
			return true
		}
	}
	return false
}

// expectedVerdict returns the unknown reason the inputs force, or "compare"
// when a satisfied/mismatch comparison is required.
func expectedVerdict(r *Requirement, o Observation) string {
	switch {
	case r == nil:
		return ReasonNotDeclared
	case r.Comparison == CompareUnsupported:
		return ReasonUnsupportedConstraint
	case o.Status != ObsObserved:
		return ReasonNotObserved
	}
	return "compare"
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func utf8Safe(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == 0xfffd {
			return false
		}
	}
	return true
}

// IsGuidance reports whether a class takes part in attachment and overlaps.
func IsGuidance(class string) bool { return class != ClassOther && slices.Contains(classes, class) }

func checkDocuments(ds Documents, modules []Module, want Expect) error {
	b := ds.Budget
	if b.TotalBytes <= 0 || b.PerDocumentBytes <= 0 || b.PerDocumentBytes > b.TotalBytes || b.MaxDocuments <= 0 || b.MaxDocuments > MaxDocuments || b.ReadBytes < 0 || b.ReadBytes > b.TotalBytes {
		return invalid("budget")
	}
	if ds.Items == nil || ds.Modules == nil || ds.OverlappingGuidance == nil || len(ds.Items) > b.MaxDocuments {
		return invalid("document lists")
	}
	docs := map[string]Doc{}
	var read int64
	for i, doc := range ds.Items {
		if i > 0 && ds.Items[i-1].Path >= doc.Path {
			return invalid("documents are not sorted")
		}
		if domain.ValidateEvidencePath(doc.Path) != nil || doc.AppliesTo != Dir(doc.Path) || !slices.Contains(classes, doc.Class) {
			return invalid("document %q", doc.Path)
		}
		sha, included := want.Lookup(doc.Path)
		if (doc.SHA256 != nil) != (doc.SizeBytes != nil) || (doc.SHA256 != nil && (!included || sha != *doc.SHA256)) || (doc.SizeBytes != nil && *doc.SizeBytes < 0) {
			return invalid("document %q blob", doc.Path)
		}
		reason := deref(doc.Reason)
		switch doc.Status {
		case DocSelected:
			if doc.Reason != nil || doc.SHA256 == nil || doc.LineCount == nil || *doc.LineCount < 0 || *doc.SizeBytes > b.PerDocumentBytes {
				return invalid("selected document %q", doc.Path)
			}
		case DocBudgetExceeded:
			if (reason != ReasonTooLarge && reason != ReasonBudget) || doc.SHA256 == nil || (reason == ReasonTooLarge) != (*doc.SizeBytes > b.PerDocumentBytes) {
				return invalid("budget_exceeded document %q", doc.Path)
			}
		case DocExcluded:
			if !record.ValidCode(reason) || ((reason == ReasonOutOfScope || reason == ReasonCredentialLike) && doc.SHA256 == nil) {
				return invalid("excluded document %q", doc.Path)
			}
		case DocUnreadable:
			if (reason != ReasonNotUTF8 && reason != ReasonNUL) || doc.SHA256 == nil {
				return invalid("unreadable document %q", doc.Path)
			}
		default:
			return invalid("document status")
		}
		if doc.LineCount != nil && doc.Status != DocSelected {
			return invalid("line_count outside selected document %q", doc.Path)
		}
		if doc.Status == DocSelected || reason == ReasonNUL || reason == ReasonCredentialLike {
			read += *doc.SizeBytes
		}
		docs[doc.Path] = doc
	}
	if read != b.ReadBytes {
		return invalid("read_bytes does not match the read documents")
	}
	guidance := func(p string) bool { d, ok := docs[p]; return ok && IsGuidance(d.Class) }
	if len(ds.Modules) != len(modules) {
		return invalid("module documents")
	}
	for i, md := range ds.Modules {
		m := modules[i]
		if md.Module != m.ID || !sortedUnique(md.Own, guidance) || !sortedUnique(md.Inherited, guidance) {
			return invalid("module documents %q", md.Module)
		}
		for _, p := range md.Own {
			if !inModule(p, m) {
				return invalid("module documents %q own", md.Module)
			}
		}
		for _, p := range md.Inherited {
			a := docs[p].AppliesTo
			if a == m.Path || !Under(m.Path, a) {
				return invalid("module documents %q inherited", md.Module)
			}
		}
		state := StateNone
		switch {
		case len(md.Own) > 0:
			state = StateOwn
		case len(md.Inherited) > 0:
			state = StateInherited
		}
		if md.State != state {
			return invalid("module documents %q state", md.Module)
		}
	}
	for i, o := range ds.OverlappingGuidance {
		if (i > 0 && ds.OverlappingGuidance[i-1].Path >= o.Path) || !ValidPath(o.Path) || len(o.Documents) < 2 || !sortedUnique(o.Documents, guidance) || o.Assessment != AssessmentNoModel {
			return invalid("overlapping guidance %q", o.Path)
		}
		for _, p := range o.Documents {
			if !Under(o.Path, docs[p].AppliesTo) {
				return invalid("overlapping guidance %q does not apply", o.Path)
			}
		}
	}
	return nil
}

func inModule(p string, m Module) bool {
	if !Under(p, m.Path) {
		return false
	}
	for _, x := range m.ExcludedPaths {
		if Under(p, x) {
			return false
		}
	}
	return true
}
