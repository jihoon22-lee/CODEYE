package record

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Review stage records (schema 0.2) are written by the review roles. A
// record is not cumulative: it holds only its own stage's calls and binds
// every upstream input by the SHA-256 of the artifact bytes it read.
const (
	SchemaVersion = "0.2"
	// SchemaVersionSlice is a role record of one review slice
	// (review.investigate.sNNN, review.verify.sNNN): 0.2 plus the required
	// slice reference. Every other role record stays 0.2, byte for byte.
	SchemaVersionSlice = "0.3"

	StageEnvironment = "environment"
	StageDocuments   = "documents"
	StageInvestigate = "investigate"
	StageInterpret   = "interpret"
	StageVerify      = "verify"

	// Upstream states. Only accepted inputs reach a downstream role.
	UpstreamAccepted    = "accepted"
	UpstreamSkipped     = "skipped"
	UpstreamFailed      = "failed"
	UpstreamUnavailable = "unavailable"

	// LimExecutionClaim marks model prose that claimed running tools or tests,
	// or reproducing a defect. The platform rejects it (heuristic check).
	LimExecutionClaim = "execution_claim_rejected"

	maxEvidence     = 40
	maxObservations = 16
	// maxText bounds the JSON-encoded size of one review model text
	// field, so a downstream request carrying it keeps within the request
	// budget.
	maxText = 400
	// RoleMaxCalls bounds the calls of one stage: the first request, one
	// repair of rejected output and one retry of a reply that was not JSON.
	RoleMaxCalls = 3
)

// Stages in plan order.
var Stages = []string{StageEnvironment, StageDocuments, StageInvestigate, StageInterpret, StageVerify}

// PackRef identifies the instruction pack a record was produced with.
type PackRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// Upstream is one input of a stage: a platform discovery or tool artifact or
// an earlier role record. SHA256 is the artifact bytes read (accepted).
type Upstream struct {
	Source string `json:"source"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// ObservationRef is an adapter-issued tool observation given to a stage.
type ObservationRef struct {
	ID        string `json:"id"`
	TaskKey   string `json:"task_key"`
	Tool      string `json:"tool"`
	RuleID    string `json:"rule_id"`
	Path      string `json:"path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
}

// EnvironmentNote is the environment role's per-module impact note. The
// platform verdicts are not part of it; the model cannot change them.
type EnvironmentNote struct {
	Module            string   `json:"module"`
	RequirementIDs    []string `json:"requirement_ids"`
	EvidenceIDs       []string `json:"evidence_ids"`
	AffectedChecks    string   `json:"affected_checks"`
	StillCheckable    string   `json:"still_checkable"`
	NeededObservation string   `json:"needed_observation"`
}

// Contract is a documented expectation scoped to a path.
type Contract struct {
	Statement   string   `json:"statement"`
	AppliesTo   string   `json:"applies_to"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// Conflict is two sides of guidance that disagree in one scope.
type Conflict struct {
	Scope          string   `json:"scope"`
	Summary        string   `json:"summary"`
	Impact         string   `json:"impact"`
	FirstEvidence  []string `json:"first_evidence_ids"`
	SecondEvidence []string `json:"second_evidence_ids"`
}

// Question is an open question handed to code review.
type Question struct {
	Question    string   `json:"question"`
	AppliesTo   string   `json:"applies_to"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// InstructionNote records instruction-like document content that was
// treated as data (for example a request to change permissions).
type InstructionNote struct {
	Note        string   `json:"note"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// InterpretationLimit is a tool observation the interpret role could not
// relate to code.
type InterpretationLimit struct {
	ObservationIDs []string `json:"observation_ids"`
	Statement      string   `json:"statement"`
}

// Candidate keeps the 0.1 candidate fields (so verification and report
// findings work unchanged) with its origin stage and, for interpret, the
// tool observations it is linked to.
type Candidate struct {
	CandidateText
	Origin         string   `json:"origin"`
	ObservationIDs []string `json:"observation_ids,omitempty"`
}

// Call is one model request of the stage. OutputErrors are the fixed
// platform codes the output of this call was rejected with.
type Call struct {
	Role          string   `json:"role"`
	Model         string   `json:"model"`
	Version       string   `json:"opencode_version"`
	PackID        string   `json:"pack_id"`
	PackVersion   string   `json:"pack_version"`
	PackDigest    string   `json:"pack_sha256"`
	PackFiles     []string `json:"pack_files"`
	RequestDigest string   `json:"request_sha256"`
	RequestBytes  int      `json:"request_bytes"`
	Usage         *Usage   `json:"usage"`
	Attempt       int      `json:"attempt"`
	Outcome       string   `json:"outcome"`
	OutputErrors  []string `json:"output_errors"`
}

// SliceRef names the review slice of a 0.3 record and the modules its
// files belong to.
type SliceRef struct {
	Key     string   `json:"key"`
	Modules []string `json:"modules"`
}

// Record is one stage artifact of the review.
type Record struct {
	Schema    string  `json:"schema_version"`
	Pack      PackRef `json:"pack"`
	Snapshot  string  `json:"snapshot_manifest_digest"`
	RunID     string  `json:"run_id"`
	AttemptID string  `json:"attempt_id"`
	Stage     string  `json:"stage"`
	// Slice is set on 0.3 records only (nil is omitted, so 0.2 bytes do not
	// change).
	Slice               *SliceRef             `json:"slice,omitempty"`
	Outcome             string                `json:"outcome"`
	FailureReason       string                `json:"failure_reason,omitempty"`
	Upstream            []Upstream            `json:"upstream"`
	Evidence            []Excerpt             `json:"evidence"`
	Observations        []ObservationRef      `json:"observations"`
	EnvironmentNotes    []EnvironmentNote     `json:"environment_notes,omitempty"`
	Contracts           []Contract            `json:"contracts,omitempty"`
	Conflicts           []Conflict            `json:"conflicts,omitempty"`
	Questions           []Question            `json:"questions,omitempty"`
	InstructionNotes    []InstructionNote     `json:"instruction_notes,omitempty"`
	DocumentQuality     []DocumentQuality     `json:"document_quality,omitempty"`
	Strengths           []Strength            `json:"strengths,omitempty"`
	ContractComparisons []ContractComparison  `json:"contract_comparisons,omitempty"`
	Candidates          []Candidate           `json:"candidates,omitempty"`
	Limits              []InterpretationLimit `json:"interpretation_limits,omitempty"`
	Verifications       []Verification        `json:"verifications,omitempty"`
	Limitations         []string              `json:"limitations"`
	Calls               []Call                `json:"calls"`
}

// Accepted reports whether the record carries an accepted stage result.
func (r Record) Accepted() bool { return r.Outcome == OutcomeAccepted }

var (
	candidateIDRule = regexp.MustCompile(`^(investigate|interpret)_[1-4]$`)
	moduleRef       = regexp.MustCompile(`^(cpp|go|javascript|python|rust):(\.|[^:\x00-\x1f]+)$`)
	requirementID   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}@[^@\x00-\x1f]+$`)
	observationID   = regexp.MustCompile(`^obs_[0-9a-f]{20}$`)
	taskKey         = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	packID          = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
	kinds           = map[string]bool{"code": true, "test": true, "document": true, "manifest": true}
	origins         = map[string]bool{"": true, "context": true, "caller": true, "test": true, "observation": true}
	// Upstream sources each stage may name. Interpret names analyzer tasks:
	// any task key outside the discovery and review namespaces (docs/ANALYZERS.md);
	// the report binds each to that task's accepted observations artifact.
	stageSources = map[string][]string{
		StageEnvironment: {"env.discover"},
		StageDocuments:   {"docs.discover"},
		StageInvestigate: {"review.environment", "review.documents"},
		StageInterpret:   {analyzerSource},
		StageVerify:      {"review.documents", "review.investigate", "review.interpret"},
	}
)

// Execution claims: affirmative past-tense statements of running, executing,
// passing tests or reproducing. Negations ("실행하지 않았다") and reported
// speech ("실행했다고 볼 수 없다") do not match. This is a heuristic guard on
// model prose, not a proof that a claim is absent.
var executionClaim = regexp.MustCompile(`(재현|실행|통과)(됨|된 것|되는 것)[을를] 확인(했|하였)|(재현|실행|구동|통과)(했|하였|됐|되었|해 ?봤|해 ?보았|해 ?본 결과)(다|고|으며|음|습니다|으나|지만|으므로|는데)?(?:[.,!\s)]|$)|(?i:\b(?:i|we)\s+(?:have\s+)?(?:ran|run|executed|reproduced|re-ran)\b)|(?i:\b(?:successfully|was|were|been)\s+reproduced\b)|(?i:\btests?\s+(?:passed|have\s+passed)\b)`)

// ClaimsExecution reports model prose that claims an execution or a
// reproduction the platform never performed.
func ClaimsExecution(s string) bool { return executionClaim.MatchString(s) }

// ModelText bounds one review model text field (JSON-encoded size, credentials,
// execution claims).
func ModelText(s string) bool {
	return SafeText(s) && JSONLen(s) <= maxText && !ClaimsExecution(s)
}

// JSONLen is the size of s encoded as a JSON string by encoding/json
// (HTML-escaped, so <, > and & take six bytes each).
func JSONLen(s string) int {
	b, _ := json.Marshal(s)
	return len(b)
}

// ValidScopePath accepts "." or a snapshot-relative path.
func ValidScopePath(p string) bool { return p == "." || domain.ValidateEvidencePath(p) == nil }

// ScopeUnder reports whether p is dir or lies below it.
func ScopeUnder(p, dir string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

// DocumentDir returns the containing directory of a snapshot-relative path.
func DocumentDir(p string) string {
	d := path.Dir(p)
	if d == "" {
		return "."
	}
	return d
}

// Decode strictly parses and validates a role record.
func Decode(body []byte, want Expect) (Record, error) {
	var r Record
	if len(body) > MaxBytes {
		return Record{}, invalid("record too large")
	}
	if err := domain.DecodeStrict(body, &r); err != nil {
		return Record{}, invalid("not strict JSON: %v", err)
	}
	if err := Validate(r, want); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Marshal validates before encoding.
func Marshal(r Record, want Expect) ([]byte, error) {
	if err := Validate(r, want); err != nil {
		return nil, err
	}
	b, err := json.Marshal(r)
	if err != nil || len(b) > MaxBytes {
		return nil, invalid("record encoding")
	}
	return b, nil
}

// Validate checks a role record against the attempt and snapshot manifest.
// Model content is checked again with the worker's rules, so a decoder never
// accepts what the worker would have rejected.
func Validate(r Record, want Expect) error {
	switch r.Schema {
	case SchemaVersion:
		if r.Slice != nil || want.Slice != "" {
			return invalid("slice reference on a whole-run record")
		}
	case SchemaVersionSlice:
		if err := checkSlice(r, want); err != nil {
			return err
		}
	default:
		return invalid("schema_version")
	}
	switch {
	case !slices.Contains(Stages, r.Stage) || (want.Stage != "" && want.Stage != r.Stage):
		return invalid("stage")
	case !packID.MatchString(r.Pack.ID) || !version.MatchString(r.Pack.Version):
		return invalid("pack identity")
	case !hex64.MatchString(r.Snapshot) || !domain.ValidID(r.RunID) || !domain.ValidID(r.AttemptID):
		return invalid("snapshot or attempt identifiers")
	case want.Lookup == nil || r.Snapshot != want.Snapshot || r.RunID != want.RunID || r.AttemptID != want.AttemptID:
		return invalid("record belongs to another snapshot or attempt")
	case r.Upstream == nil || r.Evidence == nil || r.Observations == nil || r.Limitations == nil || r.Calls == nil:
		return invalid("missing list")
	case len(r.Evidence) > maxEvidence || len(r.Observations) > maxObservations:
		return invalid("input size")
	case !codes(r.Limitations):
		return invalid("limitations")
	}
	switch r.Outcome {
	case OutcomeAccepted:
		if r.FailureReason != "" {
			return invalid("accepted record with failure_reason")
		}
	case OutcomeFailed:
		if !ValidCode(r.FailureReason) {
			return invalid("failed record without failure_reason")
		}
	default:
		return invalid("outcome")
	}
	if err := checkUpstream(r); err != nil {
		return err
	}
	catalog := map[string]Excerpt{}
	for _, e := range r.Evidence {
		if err := checkExcerpt(e, r.Snapshot, want.Lookup); err != nil {
			return err
		}
		if _, dup := catalog[e.ID]; dup {
			return invalid("duplicate evidence id")
		}
		catalog[e.ID] = e
	}
	observations := map[string]ObservationRef{}
	for _, o := range r.Observations {
		if !observationID.MatchString(o.ID) || !taskKey.MatchString(o.TaskKey) || !ValidCode(strings.ReplaceAll(o.Tool, ".", "_")) || o.RuleID == "" || len(o.RuleID) > 64 ||
			(o.Path != "" && domain.ValidateEvidencePath(o.Path) != nil) || o.StartLine < 0 || (o.Path == "") != (o.StartLine == 0) {
			return invalid("observation reference")
		}
		if _, dup := observations[o.ID]; dup {
			return invalid("duplicate observation")
		}
		observations[o.ID] = o
	}
	if err := checkStageContent(r, catalog, observations); err != nil {
		return err
	}
	if err := checkInsights(r, catalog); err != nil {
		return err
	}
	return checkCalls(r)
}

// analyzerSource marks the interpret stage's upstream rule in stageSources.
const analyzerSource = "(analyzer task)"

// Review slices (docs/REVIEW.md#모듈-단위-검토-분할).
const (
	// TaskScope is the review.scope task: it fixes the slice inputs and
	// makes no model call.
	TaskScope = "review.scope"
	// MaxSliceModules bounds the modules of one slice (a slice holds at most
	// eight files).
	MaxSliceModules = 8
)

var sliceKey = regexp.MustCompile(`^s(00[1-9]|0[1-8][0-9]|09[0-6])$`)

// ValidSliceKey accepts s001..s096 (MaxSlices slots).
func ValidSliceKey(k string) bool { return sliceKey.MatchString(k) }

// SliceTask splits a slice task key (review.investigate.s001,
// review.verify.s001) into its stage and slice key.
func SliceTask(key string) (stage, slice string, ok bool) {
	rest, found := strings.CutPrefix(key, "review.")
	if !found {
		return "", "", false
	}
	stage, slice, found = strings.Cut(rest, ".")
	if !found || (stage != StageInvestigate && stage != StageVerify) || !ValidSliceKey(slice) {
		return "", "", false
	}
	return stage, slice, true
}

// ValidModuleRef accepts a module ID <ecosystem>:<dir>.
func ValidModuleRef(id string) bool {
	_, dir, _ := strings.Cut(id, ":")
	return moduleRef.MatchString(id) && (dir == "." || domain.ValidateEvidencePath(dir) == nil)
}

// checkSlice validates the slice reference of a 0.3 record: an investigate
// or verify stage of the expected slice, with one to MaxSliceModules sorted
// distinct module IDs.
func checkSlice(r Record, want Expect) error {
	s := r.Slice
	switch {
	case s == nil || s.Modules == nil:
		return invalid("slice record without its slice")
	case r.Stage != StageInvestigate && r.Stage != StageVerify:
		return invalid("slice stage")
	case !ValidSliceKey(s.Key) || s.Key != want.Slice:
		return invalid("slice key")
	case len(s.Modules) == 0 || len(s.Modules) > MaxSliceModules || !slices.IsSorted(s.Modules):
		return invalid("slice modules")
	}
	for i, m := range s.Modules {
		if !ValidModuleRef(m) || (i > 0 && s.Modules[i-1] == m) {
			return invalid("slice module")
		}
	}
	return nil
}

// upstreamSources are the sources a record's stage may name: a slice
// investigate also reads review.scope; a slice verify reads the documents
// record, its own slice's investigate record and review.scope.
func upstreamSources(r Record) []string {
	if r.Slice == nil {
		return stageSources[r.Stage]
	}
	if r.Stage == StageInvestigate {
		return []string{"review.environment", "review.documents", TaskScope}
	}
	return []string{"review.documents", "review.investigate." + r.Slice.Key, TaskScope}
}

// AnalyzerTaskKey reports whether key can name an analyzer task: a valid
// task key outside the discovery tasks, the dependency tasks (deps.*) and
// the review.* roles.
func AnalyzerTaskKey(key string) bool {
	return taskKey.MatchString(key) && key != "env.discover" && key != "docs.discover" && !strings.HasPrefix(key, "review.") && !strings.HasPrefix(key, "deps.")
}

func checkUpstream(r Record) error {
	seen := map[string]bool{}
	for _, u := range r.Upstream {
		allowed := false
		for _, s := range upstreamSources(r) {
			allowed = allowed || u.Source == s || (s == analyzerSource && AnalyzerTaskKey(u.Source))
		}
		if !allowed || seen[u.Source] {
			return invalid("upstream source")
		}
		seen[u.Source] = true
		switch u.State {
		case UpstreamAccepted:
			if !hex64.MatchString(u.SHA256) || u.Reason != "" {
				return invalid("accepted upstream without its artifact digest")
			}
		case UpstreamSkipped, UpstreamFailed, UpstreamUnavailable:
			if u.SHA256 != "" || !ValidCode(u.Reason) {
				return invalid("upstream state")
			}
		default:
			return invalid("upstream state")
		}
	}
	return nil
}

func checkExcerpt(e Excerpt, snapshot string, lookup func(string) (string, bool)) error {
	switch {
	case domain.ValidateEvidencePath(e.Path) != nil || !hex64.MatchString(e.SHA256) || e.Start < 1 || e.End < e.Start || e.End-e.Start+1 > 80:
		return invalid("evidence location")
	case !kinds[e.Kind] || !origins[e.Origin] || e.Text != "" || (e.Symbol != "" && !symbol.MatchString(e.Symbol)):
		return invalid("evidence attributes")
	case (e.Kind == "code" || e.Kind == "test") && !sourceKindMatches(e.Path, e.Kind):
		return invalid("evidence kind")
	case (e.Origin == "caller" || e.Origin == "test") != (e.Symbol != "") || (e.Origin == "test" && e.Kind != "test") || (e.Origin == "caller" && e.Kind != "code"):
		return invalid("evidence origin")
	case (e.Kind == "document" || e.Kind == "manifest") && e.Origin != "":
		return invalid("evidence origin")
	case e.ID != ExcerptID(snapshot, e.Path, e.SHA256, e.Start, e.End):
		return invalid("evidence id is not bound to its location")
	}
	if sha, ok := lookup(e.Path); !ok || sha != e.SHA256 {
		return invalid("evidence path or blob not in snapshot")
	}
	if !e.ValidDirectivePath() {
		return invalid("evidence directive path")
	}
	if e.DirectivePath != "" {
		if _, ok := lookup(e.DirectivePath); !ok {
			return invalid("directive path not in snapshot")
		}
	}
	return nil
}

// refs checks 1..max unique IDs of the catalog whose kinds are in want.
func refs(ids []string, catalog map[string]Excerpt, max int, want ...string) bool {
	if len(ids) == 0 || len(ids) > max || !idsOK(ids, catalog, max) {
		return false
	}
	for _, id := range ids {
		if !slices.Contains(want, catalog[id].Kind) {
			return false
		}
	}
	return true
}

// docsCover reports whether every cited document applies to scope: a rule
// of a sub-directory is never generalized to a parent or the repository.
func docsCover(ids []string, catalog map[string]Excerpt, scope string) bool {
	for _, id := range ids {
		if !ScopeUnder(scope, catalog[id].DocumentScope()) {
			return false
		}
	}
	return true
}

func checkStageContent(r Record, catalog map[string]Excerpt, observations map[string]ObservationRef) error {
	empty := func(n ...int) bool {
		for _, v := range n {
			if v != 0 {
				return false
			}
		}
		return true
	}
	notes, docs := len(r.EnvironmentNotes), len(r.Contracts)+len(r.Conflicts)+len(r.Questions)+len(r.InstructionNotes)
	failed := r.Outcome == OutcomeFailed
	switch r.Stage {
	case StageEnvironment:
		if !empty(docs, len(r.Candidates), len(r.Limits), len(r.Verifications), len(r.Observations)) || notes > 4 || (failed && notes != 0) || (!failed && notes == 0) {
			return invalid("environment content")
		}
	case StageDocuments:
		if !empty(notes, len(r.Candidates), len(r.Limits), len(r.Verifications), len(r.Observations)) || (failed && docs != 0) ||
			len(r.Contracts) > 6 || len(r.Conflicts) > 3 || len(r.Questions) > 4 || len(r.InstructionNotes) > 3 {
			return invalid("documents content")
		}
	case StageInvestigate:
		if !empty(notes, docs, len(r.Limits), len(r.Verifications), len(r.Observations)) || len(r.Candidates) > 4 || (failed && len(r.Candidates) != 0) {
			return invalid("investigate content")
		}
	case StageInterpret:
		if !empty(notes, docs, len(r.Verifications)) || len(r.Candidates) > 2 || len(r.Limits) > 3 || len(r.Observations) == 0 ||
			(failed && len(r.Candidates)+len(r.Limits) != 0) || (!failed && len(r.Candidates)+len(r.Limits) == 0) {
			return invalid("interpret content")
		}
	case StageVerify:
		if !empty(notes, docs, len(r.Limits)) || len(r.Candidates) > 6 || len(r.Verifications) != len(r.Candidates) {
			return invalid("verify content")
		}
	}
	for _, n := range r.EnvironmentNotes {
		if !moduleRef.MatchString(n.Module) || n.RequirementIDs == nil || n.EvidenceIDs == nil || len(n.RequirementIDs) > 4 ||
			!ModelText(n.AffectedChecks) || !ModelText(n.StillCheckable) || !ModelText(n.NeededObservation) {
			return invalid("environment note")
		}
		_, dir, _ := strings.Cut(n.Module, ":")
		for i, id := range n.RequirementIDs {
			_, p, _ := strings.Cut(id, "@")
			if !requirementID.MatchString(id) || DocumentDir(p) != dir || slices.Contains(n.RequirementIDs[:i], id) {
				return invalid("environment note requirement of another module or duplicate")
			}
		}
		if (len(n.EvidenceIDs) > 0 && !refs(n.EvidenceIDs, catalog, 4, "manifest")) || (len(n.RequirementIDs) > 0) != (len(n.EvidenceIDs) > 0) {
			return invalid("environment note evidence")
		}
		for _, id := range n.EvidenceIDs {
			if DocumentDir(catalog[id].Path) != dir {
				return invalid("environment note evidence of another module")
			}
		}
	}
	for _, c := range r.Contracts {
		if !ModelText(c.Statement) || !ValidScopePath(c.AppliesTo) || !refs(c.EvidenceIDs, catalog, 4, "document") || !docsCover(c.EvidenceIDs, catalog, c.AppliesTo) {
			return invalid("contract")
		}
	}
	for _, c := range r.Conflicts {
		if !ModelText(c.Summary) || !ModelText(c.Impact) || !ValidScopePath(c.Scope) || !refs(c.FirstEvidence, catalog, 3, "document") || !refs(c.SecondEvidence, catalog, 3, "document") ||
			!docsCover(c.FirstEvidence, catalog, c.Scope) || !docsCover(c.SecondEvidence, catalog, c.Scope) {
			return invalid("conflict")
		}
		for _, a := range c.FirstEvidence {
			for _, b := range c.SecondEvidence {
				if catalog[a].DocumentPath() == catalog[b].DocumentPath() {
					return invalid("conflict sides cite the same document")
				}
			}
		}
	}
	for _, q := range r.Questions {
		if !ModelText(q.Question) || !ValidScopePath(q.AppliesTo) || !refs(q.EvidenceIDs, catalog, 4, "document") || !docsCover(q.EvidenceIDs, catalog, q.AppliesTo) {
			return invalid("question")
		}
	}
	for _, n := range r.InstructionNotes {
		if !ModelText(n.Note) || !refs(n.EvidenceIDs, catalog, 3, "document") {
			return invalid("instruction note")
		}
	}
	counts := map[string]int{}
	for _, c := range r.Candidates {
		counts[c.Origin]++
		if c.ID != fmt.Sprintf("%s_%d", c.Origin, counts[c.Origin]) || !candidateIDRule.MatchString(c.ID) {
			return invalid("candidate id")
		}
		if (r.Stage == StageInvestigate || r.Stage == StageInterpret) && c.Origin != r.Stage {
			return invalid("candidate origin")
		}
		if !CandidateTextOK(c.CandidateText) || !refs(c.Evidence, catalog, 6, "code", "test", "document") || !hasKind(c.Evidence, catalog, "code") {
			return invalid("candidate %s", c.ID)
		}
		if (c.Origin == StageInterpret) != (len(c.ObservationIDs) > 0) || len(c.ObservationIDs) > 4 {
			return invalid("candidate observations")
		}
		for i, id := range c.ObservationIDs {
			if _, ok := observations[id]; !ok || slices.Contains(c.ObservationIDs[:i], id) {
				return invalid("candidate observation reference")
			}
		}
	}
	if r.Stage == StageVerify {
		// A slice verify checks its own slice's investigate candidates only.
		if counts[StageInterpret] > 2 || counts[StageInvestigate] > 4 || (r.Slice != nil && counts[StageInterpret] > 0) {
			return invalid("verify candidates")
		}
		reviewed := 0
		for i, v := range r.Verifications {
			if v.CandidateID != r.Candidates[i].ID {
				return invalid("verification order")
			}
			if err := CheckVerification(v, catalog); err != nil {
				return err
			}
			for _, s := range append(append(append([]string{v.SeverityReason, v.Impact}, v.ValidConditions...), v.InvalidConditions...), append(v.UnresolvedAssumptions, v.NextVerification...)...) {
				if ClaimsExecution(s) {
					return invalid("verification claims an execution")
				}
			}
			if v.Verdict != VerdictUnreviewed {
				reviewed++
			}
		}
		if (failed && reviewed != 0) || (!failed && len(r.Candidates) > 0 && reviewed == 0) {
			return invalid("verification outcomes do not match stage")
		}
	}
	for _, l := range r.Limits {
		if !ModelText(l.Statement) || len(l.ObservationIDs) == 0 || len(l.ObservationIDs) > 4 {
			return invalid("interpretation limit")
		}
		for i, id := range l.ObservationIDs {
			if _, ok := observations[id]; !ok || slices.Contains(l.ObservationIDs[:i], id) {
				return invalid("interpretation limit observation")
			}
		}
	}
	return nil
}

// CandidateTextOK bounds the prose of a review candidate.
func CandidateTextOK(c CandidateText) bool {
	return ModelText(c.Title) && ModelText(c.Condition) && ModelText(c.Impact) && ModelText(c.Improvement) && ModelText(c.Counterpoint)
}

func checkCalls(r Record) error {
	if len(r.Calls) > RoleMaxCalls {
		return invalid("too many calls")
	}
	accepted := false
	for i, c := range r.Calls {
		if c.Role != r.Stage || c.Attempt != i+1 || !ValidCode(c.Outcome) || c.PackID != r.Pack.ID || c.PackVersion != r.Pack.Version ||
			!hex64.MatchString(c.PackDigest) || len(c.PackFiles) == 0 || len(c.PackFiles) > 24 || c.RequestBytes < 0 || c.OutputErrors == nil || !codes(c.OutputErrors) ||
			(c.Usage != nil && (c.Usage.Input < 0 || c.Usage.Output < 0)) {
			return invalid("call provenance")
		}
		for j, f := range c.PackFiles {
			if domain.ValidateEvidencePath(f) != nil || Sensitive([]byte(f)) || slices.Contains(c.PackFiles[:j], f) {
				return invalid("pack file")
			}
		}
		ok := c.Outcome == OutcomeAccepted
		// A verifier accepts candidates per item, so both of its calls may
		// carry accepted items; any other stage accepts one whole output.
		if ok && r.Stage != StageVerify && (accepted || len(c.OutputErrors) != 0 || i != len(r.Calls)-1) {
			return invalid("call sequence")
		}
		accepted = accepted || ok
		if (ok || c.Model != "") && (len(c.Model) > 160 || !modelID.MatchString(c.Model) || Sensitive([]byte(c.Model))) {
			return invalid("model identity")
		}
		if (ok || c.Version != "") && !version.MatchString(c.Version) {
			return invalid("model client version")
		}
		if (ok || c.RequestDigest != "" || c.Usage != nil) && !hex64.MatchString(c.RequestDigest) {
			return invalid("request digest")
		}
	}
	noCall := r.Stage == StageVerify && len(r.Candidates) == 0
	if noCall && len(r.Calls) != 0 {
		return invalid("empty verify with a call")
	}
	if r.Accepted() && !noCall && !accepted {
		return invalid("accepted record without an accepted call")
	}
	if !r.Accepted() && accepted {
		return invalid("failed record keeps an accepted call")
	}
	return nil
}
