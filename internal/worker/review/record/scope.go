package record

import (
	"encoding/json"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// The review scope artifact (schema 0.1) of the review.scope task
// (docs/REVIEW.md#모듈-단위-검토-분할): the fixed inputs of every review
// slice, written before any slice runs. It holds what the plan and the
// overview decided (the call ceiling, the overview prefix and the files the
// overview actually sent, every slice's files with their state) but not the
// admitted slices: admission happens slice by slice afterwards and is read
// from the slice task states (budget_exhausted / review_budget_exhausted).
const (
	ScopeMediaType     = "application/vnd.codeye.review-scope+json"
	ScopeSchemaVersion = "0.1"
	ScopeKind          = "review_scope"
	ScopeMaxBytes      = 1 << 20

	// Call budget of the role pack (codeye agent --review-max-calls).
	DefaultMaxCalls = 33
	// WholeRunMaxCalls is the most the five whole-run roles can call
	// (5 x RoleMaxCalls); they are never cut, so it is the ceiling's minimum.
	WholeRunMaxCalls = 5 * RoleMaxCalls
	// SliceReserveCalls is the worst case of one slice pair
	// (investigate and verify, RoleMaxCalls each).
	SliceReserveCalls = 2 * RoleMaxCalls
	// MaxSlices bounds the slices of a run; MaxMaxCalls is the ceiling that
	// admits all of them in the worst case.
	MaxSlices   = 32
	MaxMaxCalls = WholeRunMaxCalls + MaxSlices*SliceReserveCalls

	// Admission rule recorded in the artifact: sequential admission of
	// slices in plan order, each needing SliceReserveCalls of the calls the
	// attempt has left.
	AdmissionSequentialReserve = "sequential_reserve"
	// ScreenApplied is the only screen state slices run with.
	ScreenApplied = "applied"
)

// ValidMaxCalls reports a call ceiling the role pack accepts (0 is the
// default and is resolved before it is recorded).
func ValidMaxCalls(n int) bool { return n >= WholeRunMaxCalls && n <= MaxMaxCalls }

// Scope file states. Every eligible code or test file of the run is in
// exactly one: the overview prefix holds covered_by_overview, withheld,
// unreadable or overview_not_sent (the overview did not send it: its
// request was trimmed or failed before sending); a slice holds planned,
// covered_by_overview (the overview sent it after all), withheld or
// unreadable; the files beyond the slice limit are counted per module as
// not planned.
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

// ScopeLimits are the per-request caps of the overview and of every slice.
type ScopeLimits struct {
	MaxFiles     int `json:"max_files"`
	MaxBytes     int `json:"max_bytes"`
	MaxFileBytes int `json:"max_file_bytes"`
	MaxFileLines int `json:"max_file_lines"`
	MaxSlices    int `json:"max_slices"`
}

// ScopeModule is one module of the code corpus.
type ScopeModule struct {
	ID string `json:"id"`
	// Discovered says module discovery (env.discover) found the same ID;
	// a corpus group without a manifest of its ecosystem is not discovered.
	Discovered    bool `json:"discovered"`
	FilesEligible int  `json:"files_eligible"`
	// FilesNotPlanned are the module's files beyond the slice limit.
	FilesNotPlanned int `json:"files_not_planned"`
}

// ScopeFile is one eligible file of the overview prefix or a slice. Bound
// is the plan's byte bound; planned lines and bytes are the excerpt a
// review would send (0 when the file is withheld or unreadable).
type ScopeFile struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Module       string `json:"module"`
	BoundBytes   int    `json:"bound_bytes"`
	PlannedLines int    `json:"planned_lines"`
	PlannedBytes int    `json:"planned_bytes"`
	TotalLines   int    `json:"total_lines"`
	State        string `json:"state"`
}

// ScopeOverview is the overview prefix and the code and test files the
// overview investigate actually sent (its record's context excerpts).
// Files holds the prefix and, as covered_by_overview, a file the overview
// sent beyond it that no slice holds (one past the slice limit).
type ScopeOverview struct {
	Files   []ScopeFile `json:"files"`
	Offered []string    `json:"offered"`
}

// ScopeSlice is one planned slice.
type ScopeSlice struct {
	Key             string      `json:"key"`
	InvestigateTask string      `json:"investigate_task"`
	VerifyTask      string      `json:"verify_task"`
	Modules         []string    `json:"modules"`
	Files           []ScopeFile `json:"files"`
}

// ScopeDoc is the review scope artifact.
type ScopeDoc struct {
	Schema      string        `json:"schema_version"`
	Kind        string        `json:"kind"`
	Pack        PackRef       `json:"pack"`
	Snapshot    string        `json:"snapshot_manifest_digest"`
	RunID       string        `json:"run_id"`
	AttemptID   string        `json:"attempt_id"`
	Screen      string        `json:"screen"`
	Budget      ScopeBudget   `json:"budget"`
	Limits      ScopeLimits   `json:"limits"`
	Upstream    []Upstream    `json:"upstream"`
	Modules     []ScopeModule `json:"modules"`
	Overview    ScopeOverview `json:"overview"`
	Slices      []ScopeSlice  `json:"slices"`
	Limitations []string      `json:"limitations"`
}

// DecodeScope strictly parses and validates a scope artifact.
func DecodeScope(body []byte, want Expect) (ScopeDoc, error) {
	var d ScopeDoc
	if len(body) > ScopeMaxBytes {
		return ScopeDoc{}, invalid("scope artifact too large")
	}
	if err := domain.DecodeStrict(body, &d); err != nil {
		return ScopeDoc{}, invalid("not strict JSON: %v", err)
	}
	if err := ValidateScope(d, want); err != nil {
		return ScopeDoc{}, err
	}
	return d, nil
}

// MarshalScope validates before encoding.
func MarshalScope(d ScopeDoc, want Expect) ([]byte, error) {
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
// listed modules) and the slice keys in plan order.
func ValidateScope(d ScopeDoc, want Expect) error {
	switch {
	case d.Schema != ScopeSchemaVersion || d.Kind != ScopeKind:
		return invalid("scope schema")
	case !packID.MatchString(d.Pack.ID) || !version.MatchString(d.Pack.Version):
		return invalid("pack identity")
	case !hex64.MatchString(d.Snapshot) || !domain.ValidID(d.RunID) || !domain.ValidID(d.AttemptID):
		return invalid("snapshot or attempt identifiers")
	case want.Lookup == nil || d.Snapshot != want.Snapshot || d.RunID != want.RunID || d.AttemptID != want.AttemptID:
		return invalid("scope belongs to another snapshot or attempt")
	case d.Screen != ScreenApplied:
		return invalid("screen")
	case d.Upstream == nil || d.Modules == nil || d.Overview.Files == nil || d.Overview.Offered == nil || d.Slices == nil || d.Limitations == nil:
		return invalid("missing list")
	case !codes(d.Limitations):
		return invalid("limitations")
	}
	b := d.Budget
	if !ValidMaxCalls(b.MaxCalls) || b.WholeRunMaxCalls != WholeRunMaxCalls || b.SliceReserveCalls != SliceReserveCalls || b.Admission != AdmissionSequentialReserve ||
		b.MaxRequestBytes <= 0 || b.CallsBeforeSlices < 0 || b.CallsBeforeSlices > WholeRunMaxCalls {
		return invalid("budget")
	}
	l := d.Limits
	if l.MaxFiles <= 0 || l.MaxBytes <= 0 || l.MaxFileBytes <= 0 || l.MaxFileBytes > l.MaxBytes || l.MaxFileLines <= 0 || l.MaxSlices != MaxSlices {
		return invalid("limits")
	}
	if len(d.Upstream) != 1 || d.Upstream[0].Source != "review.investigate" {
		return invalid("upstream source")
	}
	if err := checkUpstreamState(d.Upstream[0]); err != nil {
		return err
	}
	modules := map[string]bool{}
	notPlanned := 0
	for _, m := range d.Modules {
		if !ValidModuleRef(m.ID) || modules[m.ID] || m.FilesEligible < 1 || m.FilesNotPlanned < 0 || m.FilesNotPlanned > m.FilesEligible {
			return invalid("scope module")
		}
		modules[m.ID] = true
		notPlanned += m.FilesNotPlanned
	}
	files := map[string]bool{}
	perModule := map[string]int{}
	checkFile := func(f ScopeFile, states ...string) error {
		switch {
		case domain.ValidateEvidencePath(f.Path) != nil || !hex64.MatchString(f.SHA256) || files[f.Path]:
			return invalid("scope file")
		case SourceKind(f.Path) == "" || !modules[f.Module] || !slices.Contains(states, f.State):
			return invalid("scope file attributes")
		case f.BoundBytes <= 0 || f.BoundBytes > l.MaxFileBytes || f.PlannedBytes < 0 || f.PlannedBytes > f.BoundBytes ||
			f.PlannedLines < 0 || f.PlannedLines > l.MaxFileLines || f.TotalLines < f.PlannedLines || (f.PlannedLines == 0) != (f.PlannedBytes == 0):
			return invalid("scope file size")
		case (f.State == FileWithheld || f.State == FileUnreadable) && f.PlannedLines != 0:
			return invalid("scope file planned while not readable")
		}
		if sha, ok := want.Lookup(f.Path); !ok || sha != f.SHA256 {
			return invalid("scope file not in snapshot")
		}
		files[f.Path] = true
		perModule[f.Module]++
		return nil
	}
	for _, f := range d.Overview.Files {
		if err := checkFile(f, FileCoveredByOverview, FileWithheld, FileUnreadable, FileOverviewNotSent); err != nil {
			return err
		}
	}
	// The prefix (at most MaxFiles) plus any file the overview sent beyond
	// it that no slice holds (at most MaxFiles more).
	if len(d.Overview.Files) > 2*l.MaxFiles || len(d.Overview.Offered) > l.MaxFiles {
		return invalid("overview files")
	}
	if len(d.Slices) > MaxSlices || (notPlanned > 0 && len(d.Slices) != MaxSlices) {
		return invalid("slice count")
	}
	for i, s := range d.Slices {
		key := sliceKeyOf(i)
		if s.Key != key || s.InvestigateTask != "review.investigate."+key || s.VerifyTask != "review.verify."+key || len(s.Files) == 0 || len(s.Files) > l.MaxFiles ||
			len(s.Modules) == 0 || len(s.Modules) > MaxSliceModules || !slices.IsSorted(s.Modules) {
			return invalid("slice %d", i+1)
		}
		used := map[string]bool{}
		bytes := 0
		for _, f := range s.Files {
			if err := checkFile(f, FilePlanned, FileCoveredByOverview, FileWithheld, FileUnreadable); err != nil {
				return err
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
	for _, m := range d.Modules {
		if perModule[m.ID]+m.FilesNotPlanned != m.FilesEligible {
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
