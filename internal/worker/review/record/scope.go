package record

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// The review scope artifact of the review.scope task
// (docs/REVIEW.md#모듈-단위-검토-분할): the fixed inputs of every review
// slot, written before any slot runs. It holds what the plan and the
// overview decided (the call ceiling, the overview prefix and the files the
// overview actually sent) and how review.scope filled the slots: every
// filled slot's files in priority order with their class and rank, the
// files it left out (withheld, unreadable) and, per module, the files no
// slot was left for. It does not hold the admitted slots: admission happens
// slot by slot afterwards and is read from the slice task states
// (budget_exhausted / review_budget_exhausted).
//
// Schema 0.3 records source line windows. Earlier artifacts retain their
// original file-only coverage and admission rules when decoded.
const (
	ScopeMediaType      = "application/vnd.codeye.review-scope+json"
	ScopeSchemaVersion  = "0.3"
	ScopeSchemaPriority = "0.2"
	// ScopeSchemaPlanned is the earlier schema of plan-time slices.
	ScopeSchemaPlanned = "0.1"
	ScopeKind          = "review_scope"
	ScopeMaxBytes      = 24 << 20

	// Call budget of the review (codeye agent --review-max-calls).
	DefaultMaxCalls = 99
	// DefaultParallel is how many independent review pairs of an attempt
	// run at once by default (codeye agent --review-parallel): a few
	// requests wait on the provider together; 1 is one at a time.
	DefaultParallel = 4
	MaxParallel     = 16
	// WholeRunMaxCalls is the most the five whole-run roles can call
	// (5 x RoleMaxCalls); they are never cut, so it is the ceiling's minimum.
	WholeRunMaxCalls = 5 * RoleMaxCalls
	// SliceReserveCalls is the worst case of one slice pair
	// (investigate and verify, RoleMaxCalls each).
	SliceReserveCalls = 2 * RoleMaxCalls
	// MaxSlices bounds the slots of a run. MaxMaxCalls is the largest call
	// ceiling: the whole-run roles plus two calls (one investigate, one
	// verify) for every slot, the usual cost of a slot whose replies need no
	// repair. A slot needing repairs costs up to SliceReserveCalls, so at
	// the largest ceiling fewer slots may be admitted.
	LegacyMaxSlices = 96
	LegacyMaxCalls  = WholeRunMaxCalls + LegacyMaxSlices*2
	MaxSlices       = 999
	MaxMaxCalls     = WholeRunMaxCalls + MaxSlices*2
	// planSlices bounds the slices of a schema 0.1 artifact.
	planSlices = 32

	// Admission rule recorded in the artifact: sequential admission of
	// slots in order, each needing SliceReserveCalls of the calls the
	// attempt has left.
	AdmissionSequentialReserve   = "sequential_reserve"
	AdmissionReserveVerification = "reserve_verification"
	SliceMinCalls                = 2
	// PriorityOrder names the order review.scope fills the slots in
	// (docs/REVIEW.md#검토-우선순위).
	PriorityOrder = "primary_first_observations_first"
	// PriorityPullRequestFirst is the order of a pull-request run: the
	// changed files first, then the files in their directories, each tier in
	// PriorityOrder.
	PriorityPullRequestFirst     = "pull_request_changed_first"
	PriorityCoverageFirst        = "coverage_first"
	PriorityChangedCoverageFirst = "pull_request_changed_coverage_first"
	// Pull-request labels of a scope file and slot.
	PullRequestChanged  = "changed"
	PullRequestNeighbor = "neighbor"
	// The secrets screen the scope and its slices ran with: applied (a
	// completed screen withheld its files) or not_screened (no secrets task
	// ran; the inputs went through the read filters only and every record
	// says model_input_not_secret_screened). A screen that ran but is
	// unusable runs no scope at all.
	ScreenApplied     = "applied"
	ScreenNotScreened = "not_screened"
)

// ValidMaxCalls reports a call ceiling the review accepts (0 is the
// default and is resolved before it is recorded).
func ValidMaxCalls(n int) bool { return n >= WholeRunMaxCalls && n <= MaxMaxCalls }

// ValidParallel reports a degree of parallelism of the review pairs the
// agent accepts.
func ValidParallel(n int) bool { return n >= 1 && n <= MaxParallel }

// Scope file states. Every eligible code or test file of the run is in
// exactly one place: the overview list holds covered_by_overview, withheld,
// unreadable or overview_not_sent (the overview did not send it: its
// request was trimmed or failed before sending) for the prefix, and
// covered_by_overview for a file the overview sent beyond it; a slot holds
// planned files; the excluded list holds withheld and unreadable files; the
// files no slot was left for are counted per module as not planned. (A
// schema 0.1 slice may also hold covered_by_overview, withheld and
// unreadable files.)
const (
	FilePlanned           = "planned"
	FileWithheld          = "withheld"
	FileUnreadable        = "unreadable"
	FileCoveredByOverview = "covered_by_overview"
	FileOverviewNotSent   = "overview_not_sent"
	FileNotPlanned        = "not_planned"
)

// ScopeBudget is the call budget the attempt runs the slices under.
type ScopeBudget struct {
	MaxCalls          int    `json:"max_calls"`
	MaxRequestBytes   int    `json:"max_request_bytes"`
	WholeRunMaxCalls  int    `json:"whole_run_max_calls"`
	SliceReserveCalls int    `json:"slice_reserve_calls"`
	CallsBeforeSlices int    `json:"calls_before_slices"`
	Admission         string `json:"admission"`
}

// ScopeLimits are the per-request caps of the overview and of every slot,
// the slot limit and the slots the plan made (Slots, schema 0.2).
type ScopeLimits struct {
	MaxFiles     int `json:"max_files"`
	MaxBytes     int `json:"max_bytes"`
	MaxFileBytes int `json:"max_file_bytes"`
	MaxFileLines int `json:"max_file_lines"`
	MaxSlices    int `json:"max_slices"`
	Slots        int `json:"slots"`
}

// ScopePriority is the rule review.scope ordered the files by.
type ScopePriority struct {
	Order     string        `json:"order"`
	Secondary SecondaryRule `json:"secondary"`
}

// ScopeModule is one module of the code corpus.
type ScopeModule struct {
	ID string `json:"id"`
	// Discovered says module discovery (env.discover) found the same ID;
	// a corpus group without a manifest of its ecosystem is not discovered.
	Discovered bool `json:"discovered"`
	// Class is the module's class (secondary when its directory is below a
	// secondary path).
	Class         string `json:"class"`
	FilesEligible int    `json:"files_eligible"`
	// FilesSecondary counts the module's eligible files of class secondary.
	FilesSecondary int `json:"files_secondary"`
	// FilesNotPlanned are the module's files no slot was left for, of which
	// FilesSecondaryNotPlanned are secondary.
	FilesNotPlanned          int `json:"files_not_planned"`
	FilesSecondaryNotPlanned int `json:"files_secondary_not_planned"`
}

// ScopeFile is one eligible file of the overview list, a slot or the
// excluded list. Bound is the plan's byte bound; planned lines and bytes are
// the excerpt a review would send (0 when the file is withheld or
// unreadable). Class, Rank, Secondary, Observations and Severity say why the
// file sits where it does (schema 0.2).
type ScopeFile struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Module       string `json:"module"`
	BoundBytes   int    `json:"bound_bytes"`
	PlannedLines int    `json:"planned_lines"`
	PlannedBytes int    `json:"planned_bytes"`
	TotalLines   int    `json:"total_lines"`
	State        string `json:"state"`
	Class        string `json:"class"`
	Rank         string `json:"rank"`
	// Secondary is the reason of a secondary file: secondary_path:<segment>
	// or generated_file.
	Secondary string `json:"secondary,omitempty"`
	// Observations counts the non-metric tool diagnostics located in the
	// file; Severity is the highest of them (SeverityNames).
	Observations int    `json:"observations,omitempty"`
	Severity     string `json:"severity,omitempty"`
	// PullRequest is changed or neighbor for a file of a pull-request run
	// the pull request changes or that sits beside one (order
	// pull_request_changed_first); absent otherwise.
	PullRequest string `json:"pull_request,omitempty"`
	// StartLine identifies the first line of a planned slice window in schema 0.3.
	StartLine int    `json:"start_line,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ScopeOmission identifies a line range with no request the worker can schedule.
type ScopeOmission struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Reason    string `json:"reason"`
}

// ScopeOverview is the overview prefix and the code and test files the
// overview investigate actually sent (its record's context excerpts).
// Files holds the prefix and, as covered_by_overview, every file the
// overview sent beyond it.
type ScopeOverview struct {
	Files   []ScopeFile `json:"files"`
	Offered []string    `json:"offered"`
}

// ScopeSlice is one filled slot, in slot order. Class and Rank are those of
// its first file.
type ScopeSlice struct {
	Key             string `json:"key"`
	InvestigateTask string `json:"investigate_task"`
	VerifyTask      string `json:"verify_task"`
	Class           string `json:"class"`
	Rank            string `json:"rank"`
	// PullRequest is the pull-request label of its files (one per slot).
	PullRequest string      `json:"pull_request,omitempty"`
	Modules     []string    `json:"modules"`
	Files       []ScopeFile `json:"files"`
}

// ScopeDoc is the review scope artifact (schema 0.2 shape).
type ScopeDoc struct {
	Schema      string          `json:"schema_version"`
	Kind        string          `json:"kind"`
	Pack        PackRef         `json:"pack"`
	Snapshot    string          `json:"snapshot_manifest_digest"`
	RunID       string          `json:"run_id"`
	AttemptID   string          `json:"attempt_id"`
	Screen      string          `json:"screen"`
	Budget      ScopeBudget     `json:"budget"`
	Limits      ScopeLimits     `json:"limits"`
	Priority    ScopePriority   `json:"priority"`
	Upstream    []Upstream      `json:"upstream"`
	Modules     []ScopeModule   `json:"modules"`
	Overview    ScopeOverview   `json:"overview"`
	Slices      []ScopeSlice    `json:"slices"`
	Excluded    []ScopeFile     `json:"excluded"`
	Unplanned   []ScopeFile     `json:"unplanned,omitempty"`
	Omitted     []ScopeOmission `json:"omitted,omitempty"`
	Limitations []string        `json:"limitations"`
}

// Planned reports an artifact of the earlier schema (plan-time slices, no
// priority).
func (d ScopeDoc) Planned() bool { return d.Schema == ScopeSchemaPlanned }

func (d ScopeDoc) LineCoverage() bool { return d.Schema == ScopeSchemaVersion }

// The schema 0.1 shape, decoded strictly and converted.
type scopeLimitsV01 struct {
	MaxFiles     int `json:"max_files"`
	MaxBytes     int `json:"max_bytes"`
	MaxFileBytes int `json:"max_file_bytes"`
	MaxFileLines int `json:"max_file_lines"`
	MaxSlices    int `json:"max_slices"`
}

type scopeModuleV01 struct {
	ID              string `json:"id"`
	Discovered      bool   `json:"discovered"`
	FilesEligible   int    `json:"files_eligible"`
	FilesNotPlanned int    `json:"files_not_planned"`
}

type scopeFileV01 struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Module       string `json:"module"`
	BoundBytes   int    `json:"bound_bytes"`
	PlannedLines int    `json:"planned_lines"`
	PlannedBytes int    `json:"planned_bytes"`
	TotalLines   int    `json:"total_lines"`
	State        string `json:"state"`
}

type scopeSliceV01 struct {
	Key             string         `json:"key"`
	InvestigateTask string         `json:"investigate_task"`
	VerifyTask      string         `json:"verify_task"`
	Modules         []string       `json:"modules"`
	Files           []scopeFileV01 `json:"files"`
}

type scopeDocV01 struct {
	Schema    string           `json:"schema_version"`
	Kind      string           `json:"kind"`
	Pack      PackRef          `json:"pack"`
	Snapshot  string           `json:"snapshot_manifest_digest"`
	RunID     string           `json:"run_id"`
	AttemptID string           `json:"attempt_id"`
	Screen    string           `json:"screen"`
	Budget    ScopeBudget      `json:"budget"`
	Limits    scopeLimitsV01   `json:"limits"`
	Upstream  []Upstream       `json:"upstream"`
	Modules   []scopeModuleV01 `json:"modules"`
	Overview  struct {
		Files   []scopeFileV01 `json:"files"`
		Offered []string       `json:"offered"`
	} `json:"overview"`
	Slices      []scopeSliceV01 `json:"slices"`
	Limitations []string        `json:"limitations"`
}

func (f scopeFileV01) file() ScopeFile {
	return ScopeFile{Path: f.Path, SHA256: f.SHA256, Module: f.Module, BoundBytes: f.BoundBytes, PlannedLines: f.PlannedLines, PlannedBytes: f.PlannedBytes, TotalLines: f.TotalLines, State: f.State}
}

func filesV01(list []scopeFileV01) []ScopeFile {
	if list == nil {
		return nil
	}
	out := make([]ScopeFile, 0, len(list))
	for _, f := range list {
		out = append(out, f.file())
	}
	return out
}

func (v scopeDocV01) doc() ScopeDoc {
	d := ScopeDoc{Schema: v.Schema, Kind: v.Kind, Pack: v.Pack, Snapshot: v.Snapshot, RunID: v.RunID, AttemptID: v.AttemptID, Screen: v.Screen, Budget: v.Budget,
		Limits:   ScopeLimits{MaxFiles: v.Limits.MaxFiles, MaxBytes: v.Limits.MaxBytes, MaxFileBytes: v.Limits.MaxFileBytes, MaxFileLines: v.Limits.MaxFileLines, MaxSlices: v.Limits.MaxSlices, Slots: len(v.Slices)},
		Upstream: v.Upstream, Overview: ScopeOverview{Files: filesV01(v.Overview.Files), Offered: v.Overview.Offered}, Excluded: []ScopeFile{}, Limitations: v.Limitations}
	if v.Modules != nil {
		d.Modules = []ScopeModule{}
		for _, m := range v.Modules {
			d.Modules = append(d.Modules, ScopeModule{ID: m.ID, Discovered: m.Discovered, FilesEligible: m.FilesEligible, FilesNotPlanned: m.FilesNotPlanned})
		}
	}
	if v.Slices != nil {
		d.Slices = []ScopeSlice{}
		for _, s := range v.Slices {
			d.Slices = append(d.Slices, ScopeSlice{Key: s.Key, InvestigateTask: s.InvestigateTask, VerifyTask: s.VerifyTask, Modules: s.Modules, Files: filesV01(s.Files)})
		}
	}
	return d
}

// DecodeScope strictly parses and validates a scope artifact of either
// schema.
func DecodeScope(body []byte, want Expect) (ScopeDoc, error) {
	if len(body) > ScopeMaxBytes {
		return ScopeDoc{}, invalid("scope artifact too large")
	}
	var head struct {
		Schema string `json:"schema_version"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&head); err != nil {
		return ScopeDoc{}, invalid("not JSON: %v", err)
	}
	var d ScopeDoc
	switch head.Schema {
	case ScopeSchemaPlanned:
		var v scopeDocV01
		if err := domain.DecodeStrict(body, &v); err != nil {
			return ScopeDoc{}, invalid("not strict JSON: %v", err)
		}
		d = v.doc()
	default:
		if err := domain.DecodeStrict(body, &d); err != nil {
			return ScopeDoc{}, invalid("not strict JSON: %v", err)
		}
	}
	if err := ValidateScope(d, want); err != nil {
		return ScopeDoc{}, err
	}
	return d, nil
}

// MarshalScope validates before encoding. Workers write schema 0.3.
func MarshalScope(d ScopeDoc, want Expect) ([]byte, error) {
	if d.Schema != ScopeSchemaVersion && d.Schema != ScopeSchemaPriority {
		return nil, invalid("scope schema")
	}
	if err := ValidateScope(d, want); err != nil {
		return nil, err
	}
	b, err := json.Marshal(d)
	if err != nil || len(b) > ScopeMaxBytes {
		return nil, invalid("scope encoding")
	}
	return b, nil
}

// ValidateScope checks a scope artifact against the attempt and the
// snapshot manifest: identities, the budget rule, the caps, the file
// partition (no file twice; states allowed where they occur; files of the
// listed modules), the slot keys in order and, for schema 0.2, the priority
// fields of every file and slot.
func ValidateScope(d ScopeDoc, want Expect) error {
	planned := d.Planned()
	coverage := d.LineCoverage()
	switch {
	case (!coverage && d.Schema != ScopeSchemaPriority && !planned) || d.Kind != ScopeKind:
		return invalid("scope schema")
	case !packID.MatchString(d.Pack.ID) || !version.MatchString(d.Pack.Version):
		return invalid("pack identity")
	case !hex64.MatchString(d.Snapshot) || !domain.ValidID(d.RunID) || !domain.ValidID(d.AttemptID):
		return invalid("snapshot or attempt identifiers")
	case want.Lookup == nil || d.Snapshot != want.Snapshot || d.RunID != want.RunID || d.AttemptID != want.AttemptID:
		return invalid("scope belongs to another snapshot or attempt")
	case d.Screen != ScreenApplied && d.Screen != ScreenNotScreened:
		return invalid("screen")
	case d.Screen == ScreenNotScreened && !slices.Contains(d.Limitations, "model_input_not_secret_screened"):
		return invalid("unscreened scope without its limitation")
	case d.Upstream == nil || d.Modules == nil || d.Overview.Files == nil || d.Overview.Offered == nil || d.Slices == nil || d.Excluded == nil || d.Limitations == nil:
		return invalid("missing list")
	case !codes(d.Limitations):
		return invalid("limitations")
	}
	b := d.Budget
	reserve, admission := SliceReserveCalls, AdmissionSequentialReserve
	if coverage {
		reserve, admission = SliceMinCalls, AdmissionReserveVerification
	}
	// A schema 0.1 artifact came from a worker whose largest ceiling was 207
	// as well.
	if !ValidMaxCalls(b.MaxCalls) || (!coverage && b.MaxCalls > LegacyMaxCalls) ||
		b.WholeRunMaxCalls != WholeRunMaxCalls || b.SliceReserveCalls != reserve || b.Admission != admission ||
		b.MaxRequestBytes <= 0 || b.CallsBeforeSlices < 0 || b.CallsBeforeSlices > WholeRunMaxCalls {
		return invalid("budget")
	}
	l := d.Limits
	maxSlices := LegacyMaxSlices
	if planned {
		maxSlices = planSlices
	} else if coverage && l.MaxSlices == MaxSlices {
		maxSlices = MaxSlices
	}
	if l.MaxFiles <= 0 || l.MaxBytes <= 0 || l.MaxFileBytes <= 0 || l.MaxFileBytes > l.MaxBytes || l.MaxFileLines <= 0 || l.MaxSlices != maxSlices ||
		(!planned && (l.Slots < 0 || l.Slots > maxSlices)) || (!coverage && !planned && l.Slots == 0) ||
		(l.MaxSlices != MaxSlices && b.MaxCalls > LegacyMaxCalls) {
		return invalid("limits")
	}
	pullRequest := d.Priority.Order == PriorityPullRequestFirst
	ordinary := PriorityOrder
	if coverage {
		pullRequest = d.Priority.Order == PriorityChangedCoverageFirst
		ordinary = PriorityCoverageFirst
	}
	if !planned && ((d.Priority.Order != ordinary && !pullRequest) || !d.Priority.Secondary.Valid()) {
		return invalid("priority rule")
	}
	if planned && (d.Priority.Order != "" || len(d.Excluded) != 0) {
		return invalid("schema 0.1 with priority fields")
	}
	if len(d.Upstream) != 1 || d.Upstream[0].Source != "review.investigate" {
		return invalid("upstream source")
	}
	if err := checkUpstreamState(d.Upstream[0]); err != nil {
		return err
	}
	if coverage {
		return validateCoverageScope(d, want, pullRequest)
	}
	if d.Unplanned != nil || d.Omitted != nil {
		return invalid("line coverage fields on older scope")
	}
	modules := map[string]ScopeModule{}
	notPlanned := 0
	for _, m := range d.Modules {
		_, dup := modules[m.ID]
		if !ValidModuleRef(m.ID) || dup || m.FilesEligible < 1 || m.FilesNotPlanned < 0 || m.FilesNotPlanned > m.FilesEligible {
			return invalid("scope module")
		}
		if planned && (m.Class != "" || m.FilesSecondary != 0 || m.FilesSecondaryNotPlanned != 0) {
			return invalid("schema 0.1 module with priority fields")
		}
		if !planned && ((m.Class != ClassPrimary && m.Class != ClassSecondary) || m.Class != d.Priority.Secondary.ModuleClass(m.ID) || m.FilesSecondary < 0 ||
			m.FilesSecondary > m.FilesEligible || m.FilesSecondaryNotPlanned < 0 || m.FilesSecondaryNotPlanned > min(m.FilesNotPlanned, m.FilesSecondary) ||
			(m.Class == ClassSecondary && m.FilesSecondary != m.FilesEligible)) {
			return invalid("scope module class")
		}
		modules[m.ID] = m
		notPlanned += m.FilesNotPlanned
	}
	files := map[string]bool{}
	perModule := map[string]int{}
	secondary := map[string]int{}
	checkFile := func(f ScopeFile, states ...string) error {
		_, known := modules[f.Module]
		switch {
		case f.StartLine != 0 || f.Reason != "":
			return invalid("line coverage fields on older scope")
		case domain.ValidateEvidencePath(f.Path) != nil || !hex64.MatchString(f.SHA256) || files[f.Path]:
			return invalid("scope file")
		case SourceKind(f.Path) == "" || !known || !slices.Contains(states, f.State):
			return invalid("scope file attributes")
		case f.BoundBytes <= 0 || f.BoundBytes > l.MaxFileBytes || f.PlannedBytes < 0 || f.PlannedBytes > f.BoundBytes ||
			f.PlannedLines < 0 || f.PlannedLines > l.MaxFileLines || f.TotalLines < f.PlannedLines || (f.PlannedLines == 0) != (f.PlannedBytes == 0):
			return invalid("scope file size")
		case (f.State == FileWithheld || f.State == FileUnreadable) && f.PlannedLines != 0:
			return invalid("scope file planned while not readable")
		}
		if err := checkPriority(f, planned, d.Priority.Secondary); err != nil {
			return err
		}
		if f.PullRequest != "" && (!pullRequest || (f.PullRequest != PullRequestChanged && f.PullRequest != PullRequestNeighbor)) {
			return invalid("scope file pull_request")
		}
		if sha, ok := want.Lookup(f.Path); !ok || sha != f.SHA256 {
			return invalid("scope file not in snapshot")
		}
		files[f.Path] = true
		perModule[f.Module]++
		if f.Class == ClassSecondary {
			secondary[f.Module]++
		}
		return nil
	}
	for _, f := range d.Overview.Files {
		if err := checkFile(f, FileCoveredByOverview, FileWithheld, FileUnreadable, FileOverviewNotSent); err != nil {
			return err
		}
	}
	// The prefix (at most MaxFiles) plus any file the overview sent beyond
	// it (at most MaxFiles more).
	if len(d.Overview.Files) > 2*l.MaxFiles || len(d.Overview.Offered) > l.MaxFiles {
		return invalid("overview files")
	}
	slots := l.Slots
	if planned {
		slots = maxSlices
	}
	if len(d.Slices) > slots || (notPlanned > 0 && len(d.Slices) != slots) {
		return invalid("slice count")
	}
	sliceStates := []string{FilePlanned}
	if planned {
		sliceStates = []string{FilePlanned, FileCoveredByOverview, FileWithheld, FileUnreadable}
	}
	for i, s := range d.Slices {
		key := sliceKeyOf(i)
		if s.Key != key || s.InvestigateTask != "review.investigate."+key || s.VerifyTask != "review.verify."+key || len(s.Files) == 0 || len(s.Files) > l.MaxFiles ||
			len(s.Modules) == 0 || len(s.Modules) > MaxSliceModules || !slices.IsSorted(s.Modules) {
			return invalid("slice %d", i+1)
		}
		if planned != (s.Class == "" && s.Rank == "") || (!planned && (s.Class != s.Files[0].Class || s.Rank != s.Files[0].Rank || s.PullRequest != s.Files[0].PullRequest)) {
			return invalid("slice %d priority", i+1)
		}
		used := map[string]bool{}
		bytes := 0
		for _, f := range s.Files {
			if err := checkFile(f, sliceStates...); err != nil {
				return err
			}
			if !planned && f.Class != s.Class {
				return invalid("slice %d mixes classes", i+1)
			}
			if f.PullRequest != s.PullRequest {
				return invalid("slice %d mixes pull-request tiers", i+1)
			}
			used[f.Module] = true
			bytes += f.BoundBytes
		}
		if bytes > l.MaxBytes || len(used) != len(s.Modules) {
			return invalid("slice %d bounds", i+1)
		}
		for j, m := range s.Modules {
			if !used[m] || (j > 0 && s.Modules[j-1] == m) {
				return invalid("slice %d modules", i+1)
			}
		}
	}
	for _, f := range d.Excluded {
		if err := checkFile(f, FileWithheld, FileUnreadable); err != nil {
			return err
		}
	}
	for _, m := range d.Modules {
		if perModule[m.ID]+m.FilesNotPlanned != m.FilesEligible || (!planned && secondary[m.ID]+m.FilesSecondaryNotPlanned != m.FilesSecondary) {
			return invalid("module %s file count", m.ID)
		}
	}
	sent := map[string]bool{}
	for i, p := range d.Overview.Offered {
		if !files[p] || (i > 0 && d.Overview.Offered[i-1] >= p) {
			return invalid("overview offered file")
		}
		sent[p] = true
	}
	for _, f := range d.Overview.Files {
		if (f.State == FileCoveredByOverview) != sent[f.Path] {
			return invalid("overview file state")
		}
	}
	for _, s := range d.Slices {
		for _, f := range s.Files {
			if (f.State == FileCoveredByOverview) != sent[f.Path] {
				return invalid("slice file state")
			}
		}
	}
	for _, f := range d.Excluded {
		if sent[f.Path] {
			return invalid("excluded file the overview sent")
		}
	}
	return nil
}

// checkPriority checks the priority fields of a file: none in schema 0.1;
// in 0.2 a class and rank, a secondary reason exactly for a secondary file
// (a segment of the recorded rule or generated_file), and a count and
// severity exactly for a file ranked by its tool diagnostics.
func checkPriority(f ScopeFile, planned bool, rule SecondaryRule) error {
	if planned {
		if f.Class != "" || f.Rank != "" || f.Secondary != "" || f.Observations != 0 || f.Severity != "" {
			return invalid("schema 0.1 file with priority fields")
		}
		return nil
	}
	seg, isPath := strings.CutPrefix(f.Secondary, ReasonSecondaryPath+":")
	switch {
	case f.Class != ClassPrimary && f.Class != ClassSecondary, RankIndex(f.Rank) == len(Ranks):
		return invalid("scope file priority")
	case (f.Class == ClassSecondary) != (f.Secondary != ""):
		return invalid("scope file secondary reason")
	case f.Secondary != "" && f.Secondary != ReasonGeneratedFile && (!isPath || !slices.Contains(rule.Segments, seg)):
		return invalid("scope file secondary reason")
	case (f.Rank == RankToolObservations) != (f.Observations > 0), f.Observations < 0:
		return invalid("scope file observations")
	case (f.Observations > 0) != (f.Severity != ""), f.Severity != "" && !slices.Contains(SeverityNames, f.Severity):
		return invalid("scope file severity")
	}
	return nil
}

func checkUpstreamState(u Upstream) error {
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
	return nil
}

func sliceKeyOf(i int) string {
	b := []byte("s000")
	n := i + 1
	for j := 3; j >= 1; j-- {
		b[j] = byte('0' + n%10)
		n /= 10
	}
	return string(b)
}
