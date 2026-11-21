package record

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// example decodes a review example record (examples/review, written
// for fixtures/modules/input) with a lookup built from the fixture itself.
func example(t *testing.T, stage string) ([]byte, Record, Expect) {
	t.Helper()
	b, err := os.ReadFile("../../../../examples/review/" + stage + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		t.Fatal(err)
	}
	lookup := map[string]string{}
	root := "../../../../fixtures/modules/input"
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		lookup[filepath.ToSlash(rel)] = domain.Digest(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var head Record
	if err := json.Unmarshal(b, &head); err != nil {
		t.Fatal(err)
	}
	want := Expect{Snapshot: head.Snapshot, RunID: head.RunID, AttemptID: head.AttemptID, Stage: stage, Lookup: func(p string) (string, bool) { s, ok := lookup[p]; return s, ok }}
	r, err := Decode(compact.Bytes(), want)
	if err != nil {
		t.Fatal(err)
	}
	return compact.Bytes(), r, want
}

// asSlice turns a whole-run investigate or verify record into a record of
// slice s001 (schema 0.3, its own upstream names).
func asSlice(r Record) Record {
	r.Schema = SchemaVersionSlice
	r.Slice = &SliceRef{Key: "s001", Modules: []string{"go:.", "go:svc/b"}}
	r.Upstream = append([]Upstream{}, r.Upstream...)
	for i := range r.Upstream {
		if r.Upstream[i].Source == "review.investigate" {
			r.Upstream[i].Source = "review.investigate.s001"
		}
	}
	r.Upstream = append(r.Upstream, Upstream{Source: TaskScope, State: UpstreamAccepted, SHA256: strings.Repeat("c", 64)})
	return r
}

// TC-164: slice records are schema 0.3: 0.2 plus a required slice
// reference. A 0.2 record keeps its bytes (no slice field) and rejects a
// slice; a 0.3 record needs its slice key, sorted module IDs, the slice
// upstream names and (verify) its own slice's investigate candidates only.
func TestSliceRecordSchema(t *testing.T) {
	raw, inv, want := example(t, StageInvestigate)
	again, err := Marshal(inv, want)
	if err != nil || !bytes.Equal(again, raw) || bytes.Contains(raw, []byte(`"slice"`)) {
		t.Fatalf("0.2 record bytes changed: %v", err)
	}
	sliceWant := want
	sliceWant.Slice = "s001"
	good := asSlice(inv)
	b, err := Marshal(good, sliceWant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(b, sliceWant); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(b, want); err == nil {
		t.Fatal("slice record accepted as a whole-run record")
	}
	mutations := map[string]func(*Record){
		"no slice on 0.3":  func(r *Record) { r.Slice = nil },
		"slice on 0.2":     func(r *Record) { r.Schema = SchemaVersion },
		"key s000":         func(r *Record) { r.Slice.Key = "s000" },
		"key s033":         func(r *Record) { r.Slice.Key = "s033" },
		"other slice":      func(r *Record) { r.Slice.Key = "s002" },
		"upper case key":   func(r *Record) { r.Slice.Key = "S001" },
		"no modules":       func(r *Record) { r.Slice.Modules = []string{} },
		"nil modules":      func(r *Record) { r.Slice.Modules = nil },
		"unsorted modules": func(r *Record) { r.Slice.Modules = []string{"go:svc/b", "go:."} },
		"duplicate module": func(r *Record) { r.Slice.Modules = []string{"go:.", "go:."} },
		"invalid module":   func(r *Record) { r.Slice.Modules = []string{"go"} },
		"absolute module":  func(r *Record) { r.Slice.Modules = []string{"go:/etc"} },
		"too many modules": func(r *Record) {
			r.Slice.Modules = strings.Split("go:a go:b go:c go:d go:e go:f go:g go:h go:i", " ")
		},
		"interpret stage": func(r *Record) { r.Stage = StageInterpret },
		"unknown schema":  func(r *Record) { r.Schema = "0.4" },
		"interpret upstream": func(r *Record) {
			r.Upstream = append(r.Upstream, Upstream{Source: "review.interpret", State: UpstreamSkipped, Reason: "no_tool_observations"})
		},
		"other slice upstream": func(r *Record) {
			r.Upstream = append(r.Upstream, Upstream{Source: "review.investigate.s002", State: UpstreamSkipped, Reason: "x"})
		},
	}
	for name, mutate := range mutations {
		r := asSlice(inv)
		r.Slice = &SliceRef{Key: r.Slice.Key, Modules: append([]string(nil), r.Slice.Modules...)}
		mutate(&r)
		if _, err := Marshal(r, sliceWant); err == nil {
			t.Errorf("%s accepted", name)
		}
		if b, err := json.Marshal(r); err == nil {
			if _, err := Decode(b, sliceWant); err == nil {
				t.Errorf("%s decoded", name)
			}
		}
	}
	var unknown map[string]any
	if err := json.Unmarshal(b, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["slice"].(map[string]any)["tasks"] = []string{"x"}
	if b, _ := json.Marshal(unknown); b != nil {
		if _, err := Decode(b, sliceWant); err == nil {
			t.Error("unknown slice field decoded")
		}
	}

	// A slice verify carries its own slice's investigate candidates only.
	_, ver, verWant := example(t, StageVerify)
	verWant.Slice = "s001"
	slice := asSlice(ver)
	if _, err := Marshal(slice, verWant); err == nil {
		t.Fatal("slice verify with interpret candidates accepted")
	}
	slice.Candidates, slice.Verifications = slice.Candidates[:1], slice.Verifications[:1]
	slice.Observations = []ObservationRef{}
	kept := slice.Upstream[:0]
	for _, u := range slice.Upstream {
		if u.Source != "review.interpret" {
			kept = append(kept, u)
		}
	}
	slice.Upstream = kept
	if _, err := Marshal(slice, verWant); err != nil {
		t.Fatalf("slice verify: %v", err)
	}
	slice.Upstream = append(slice.Upstream, Upstream{Source: "review.investigate", State: UpstreamSkipped, Reason: "x"})
	if _, err := Marshal(slice, verWant); err == nil {
		t.Fatal("slice verify naming the overview investigate accepted")
	}
}

func TestSliceTaskKeys(t *testing.T) {
	for key, want := range map[string]string{"review.investigate.s001": "investigate s001", "review.verify.s032": "verify s032", "review.verify.s096": "verify s096", "review.investigate.s090": "investigate s090"} {
		stage, slice, ok := SliceTask(key)
		if !ok || stage+" "+slice != want {
			t.Fatalf("%s: %s %s %v", key, stage, slice, ok)
		}
	}
	for _, key := range []string{"review.investigate", "review.scope", "review.interpret.s001", "review.verify.s097", "review.verify.s000", "review.verify.s100", "review.verify.s1", "go-vet"} {
		if _, _, ok := SliceTask(key); ok {
			t.Fatalf("%s accepted", key)
		}
	}
	if !ValidMaxCalls(WholeRunMaxCalls) || !ValidMaxCalls(MaxMaxCalls) || ValidMaxCalls(14) || ValidMaxCalls(208) || MaxMaxCalls != 207 || !ValidMaxCalls(DefaultMaxCalls) {
		t.Fatal("call ceiling range")
	}
}

// validScope is a small consistent scope artifact over synthetic paths:
// the overview prefix (main.go sent, app.py unreadable), b.go the overview
// sent beyond the prefix, one slot with c.go, d_test.go withheld and one
// secondary file (fixtures/e.go) no slot was left for.
func validScope() (ScopeDoc, Expect) {
	sha := strings.Repeat("a", 64)
	lookup := map[string]string{"a/main.go": sha, "a/b.go": sha, "a/c.go": sha, "a/d_test.go": sha, "w/app.py": sha, "a/fixtures/e.go": sha}
	want := Expect{Snapshot: strings.Repeat("7", 64), RunID: domain.NewID(), AttemptID: domain.NewID(), Lookup: func(p string) (string, bool) { s, ok := lookup[p]; return s, ok }}
	file := func(p, module, state, rank string, lines int) ScopeFile {
		f := ScopeFile{Path: p, SHA256: sha, Module: module, BoundBytes: 2400, TotalLines: 60, State: state, Class: ClassPrimary, Rank: rank}
		if lines > 0 {
			f.PlannedLines, f.PlannedBytes = lines, 1200
		}
		return f
	}
	observed := file("a/c.go", "go:a", FilePlanned, RankToolObservations, 50)
	observed.Observations, observed.Severity = 3, "high"
	d := ScopeDoc{Schema: ScopeSchemaPriority, Kind: ScopeKind, Pack: PackRef{ID: "review", Version: "2.0.0"}, Snapshot: want.Snapshot, RunID: want.RunID, AttemptID: want.AttemptID,
		Screen: ScreenApplied, Budget: ScopeBudget{MaxCalls: DefaultMaxCalls, MaxRequestBytes: 40000, WholeRunMaxCalls: WholeRunMaxCalls, SliceReserveCalls: SliceReserveCalls, CallsBeforeSlices: 7, Admission: AdmissionSequentialReserve},
		Limits:   ScopeLimits{MaxFiles: 8, MaxBytes: 12000, MaxFileBytes: 2400, MaxFileLines: 50, MaxSlices: MaxSlices, Slots: 1},
		Priority: ScopePriority{Order: PriorityOrder, Secondary: DefaultSecondary},
		Upstream: []Upstream{{Source: "review.investigate", State: UpstreamAccepted, SHA256: strings.Repeat("b", 64)}},
		Modules:  []ScopeModule{{ID: "go:a", Discovered: true, Class: ClassPrimary, FilesEligible: 5, FilesSecondary: 1, FilesNotPlanned: 1, FilesSecondaryNotPlanned: 1}, {ID: "python:w", Class: ClassPrimary, FilesEligible: 1}},
		Overview: ScopeOverview{Files: []ScopeFile{file("a/main.go", "go:a", FileCoveredByOverview, RankEntryPoint, 50), file("w/app.py", "python:w", FileUnreadable, RankEntryPoint, 0),
			file("a/b.go", "go:a", FileCoveredByOverview, RankSource, 50)}, Offered: []string{"a/b.go", "a/main.go"}},
		Slices: []ScopeSlice{{Key: "s001", InvestigateTask: "review.investigate.s001", VerifyTask: "review.verify.s001", Class: ClassPrimary, Rank: RankToolObservations, Modules: []string{"go:a"},
			Files: []ScopeFile{observed}}},
		Excluded:    []ScopeFile{file("a/d_test.go", "go:a", FileWithheld, RankTest, 0)},
		Limitations: []string{"review_slice_limit"}}
	return d, want
}

// TC-164: the scope artifact binds the attempt and snapshot and partitions
// every eligible file once, with states allowed where they occur and every
// file's class and rank consistent with the recorded rule.
func TestScopeArtifactValidation(t *testing.T) {
	d, want := validScope()
	b, err := MarshalScope(d, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeScope(b, want); err != nil {
		t.Fatal(err)
	}
	unscreened := d
	unscreened.Screen, unscreened.Limitations = ScreenNotScreened, []string{"model_input_not_secret_screened"}
	if _, err := MarshalScope(unscreened, want); err != nil {
		t.Fatalf("not_screened scope: %v", err)
	}
	mutations := map[string]func(*ScopeDoc){
		"other attempt":           func(d *ScopeDoc) { d.AttemptID = domain.NewID() },
		"screen outside the enum": func(d *ScopeDoc) { d.Screen = "unscreened" },
		"not_screened unmarked":   func(d *ScopeDoc) { d.Screen = ScreenNotScreened },
		"ceiling too low":         func(d *ScopeDoc) { d.Budget.MaxCalls = 14 },
		"ceiling too high":        func(d *ScopeDoc) { d.Budget.MaxCalls = 208 },
		"other admission":         func(d *ScopeDoc) { d.Budget.Admission = "worst_case" },
		"file twice":              func(d *ScopeDoc) { d.Slices[0].Files[0].Path = "a/main.go" },
		"file not in snapshot":    func(d *ScopeDoc) { d.Slices[0].Files[0].Path = "a/x.go" },
		"not source":              func(d *ScopeDoc) { d.Slices[0].Files[0].Path = "README.md" },
		"planned in overview":     func(d *ScopeDoc) { d.Overview.Files[0].State = FilePlanned },
		"withheld but planned":    func(d *ScopeDoc) { d.Excluded[0].PlannedLines, d.Excluded[0].PlannedBytes = 5, 50 },
		"planned in excluded":     func(d *ScopeDoc) { d.Excluded[0].State = FilePlanned },
		"withheld in a slot": func(d *ScopeDoc) {
			d.Slices[0].Files[0].State, d.Slices[0].Files[0].PlannedLines, d.Slices[0].Files[0].PlannedBytes = FileWithheld, 0, 0
		},
		"bytes over bound":         func(d *ScopeDoc) { d.Slices[0].Files[0].PlannedBytes = 2401 },
		"unknown module":           func(d *ScopeDoc) { d.Slices[0].Files[0].Module = "go:z" },
		"module count":             func(d *ScopeDoc) { d.Modules[0].FilesEligible = 6 },
		"secondary count":          func(d *ScopeDoc) { d.Modules[0].FilesSecondary = 2 },
		"module class":             func(d *ScopeDoc) { d.Modules[0].Class = ClassSecondary },
		"slice key out of order":   func(d *ScopeDoc) { d.Slices[0].Key = "s002" },
		"slice task":               func(d *ScopeDoc) { d.Slices[0].VerifyTask = "review.verify" },
		"slice rank":               func(d *ScopeDoc) { d.Slices[0].Rank = RankSource },
		"slice class":              func(d *ScopeDoc) { d.Slices[0].Class = ClassSecondary },
		"covered not offered":      func(d *ScopeDoc) { d.Overview.Offered = []string{"a/main.go"} },
		"offered unsorted":         func(d *ScopeDoc) { d.Overview.Offered = []string{"a/main.go", "a/b.go"} },
		"offered excluded":         func(d *ScopeDoc) { d.Overview.Offered = []string{"a/b.go", "a/d_test.go", "a/main.go"} },
		"not planned below slots":  func(d *ScopeDoc) { d.Limits.Slots = 2 },
		"no slots":                 func(d *ScopeDoc) { d.Limits.Slots = 0 },
		"slots above the limit":    func(d *ScopeDoc) { d.Limits.Slots = MaxSlices + 1 },
		"upstream other source":    func(d *ScopeDoc) { d.Upstream[0].Source = "review.verify" },
		"slice modules":            func(d *ScopeDoc) { d.Slices[0].Modules = []string{"go:a", "python:w"} },
		"rank unknown":             func(d *ScopeDoc) { d.Overview.Files[0].Rank = "core" },
		"observations without":     func(d *ScopeDoc) { d.Slices[0].Files[0].Observations = 0 },
		"observed rank without":    func(d *ScopeDoc) { d.Overview.Files[2].Observations, d.Overview.Files[2].Severity = 1, "low" },
		"severity unknown":         func(d *ScopeDoc) { d.Slices[0].Files[0].Severity = "error" },
		"secondary without reason": func(d *ScopeDoc) { d.Overview.Files[2].Class = ClassSecondary },
		"reason outside the rule":  func(d *ScopeDoc) { d.Overview.Files[2].Secondary = "secondary_path:lib" },
		"other priority order":     func(d *ScopeDoc) { d.Priority.Order = "breadth_first" },
		"empty rule":               func(d *ScopeDoc) { d.Priority.Secondary = SecondaryRule{Segments: []string{}, NotForGo: []string{}} },
		"schema 0.1 written":       func(d *ScopeDoc) { d.Schema = ScopeSchemaPlanned },
	}
	for name, mutate := range mutations {
		d, want := validScope()
		mutate(&d)
		if _, err := MarshalScope(d, want); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A secondary file listed with its reason is accepted.
	d, want = validScope()
	d.Overview.Files[2].Class, d.Overview.Files[2].Secondary = ClassSecondary, ReasonGeneratedFile
	d.Modules[0].FilesSecondary = 2
	if _, err := MarshalScope(d, want); err != nil {
		t.Fatalf("generated file: %v", err)
	}
}

func TestScopeRejectsOverlappingOrUnaccountedLines(t *testing.T) {
	d, want := validScope()
	d.Schema = ScopeSchemaVersion
	d.Budget.Admission, d.Budget.SliceReserveCalls = AdmissionReserveVerification, SliceMinCalls
	d.Priority.Order = PriorityCoverageFirst
	d.Modules = []ScopeModule{{ID: "go:a", Class: ClassPrimary, FilesEligible: 1}}
	d.Overview = ScopeOverview{Files: []ScopeFile{}, Offered: []string{}}
	d.Excluded = []ScopeFile{}
	f := &d.Slices[0].Files[0]
	f.StartLine, f.BoundBytes = 1, f.PlannedBytes
	d.Omitted = []ScopeOmission{{Path: f.Path, StartLine: 51, EndLine: 60, Reason: ScopeReasonSlotLimit}}
	b, err := MarshalScope(d, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeScope(b, want); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ start, end int }{{50, 60}, {52, 60}, {51, 61}} {
		d.Omitted[0].StartLine, d.Omitted[0].EndLine = tc.start, tc.end
		if _, err := MarshalScope(d, want); err == nil {
			t.Fatalf("invalid omitted range %d–%d was accepted", tc.start, tc.end)
		}
	}
}

// An artifact of the earlier schema (plan-time slices without priority) of an
// older worker still decodes, as the 0.2 shape without priority fields; it
// cannot carry them.
func TestScopeArtifactPlannedSchema(t *testing.T) {
	sha := strings.Repeat("a", 64)
	want := Expect{Snapshot: strings.Repeat("7", 64), RunID: domain.NewID(), AttemptID: domain.NewID(), Lookup: func(p string) (string, bool) { return sha, p == "a/b.go" || p == "a/main.go" }}
	body := `{"schema_version":"0.1","kind":"review_scope","pack":{"id":"review","version":"2.0.0"},"snapshot_manifest_digest":"` + want.Snapshot + `","run_id":"` + want.RunID + `","attempt_id":"` + want.AttemptID + `",` +
		`"screen":"applied","budget":{"max_calls":99,"max_request_bytes":40000,"whole_run_max_calls":15,"slice_reserve_calls":6,"calls_before_slices":5,"admission":"sequential_reserve"},` +
		`"limits":{"max_files":8,"max_bytes":12000,"max_file_bytes":2400,"max_file_lines":50,"max_slices":32},"upstream":[{"source":"review.investigate","state":"accepted","reason":"","sha256":"` + strings.Repeat("b", 64) + `"}],` +
		`"modules":[{"id":"go:a","discovered":true,"files_eligible":2,"files_not_planned":0}],` +
		`"overview":{"files":[{"path":"a/main.go","sha256":"` + sha + `","module":"go:a","bound_bytes":2400,"planned_lines":50,"planned_bytes":1200,"total_lines":60,"state":"covered_by_overview"}],"offered":["a/main.go"]},` +
		`"slices":[{"key":"s001","investigate_task":"review.investigate.s001","verify_task":"review.verify.s001","modules":["go:a"],"files":[{"path":"a/b.go","sha256":"` + sha + `","module":"go:a","bound_bytes":2400,"planned_lines":50,"planned_bytes":1200,"total_lines":60,"state":"planned"}]}],` +
		`"limitations":[]}`
	d, err := DecodeScope([]byte(body), want)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Planned() || d.Limits.Slots != 1 || len(d.Slices) != 1 || d.Slices[0].Files[0].Class != "" || d.Excluded == nil {
		t.Fatalf("%+v", d)
	}
	if _, err := DecodeScope([]byte(strings.Replace(body, `"state":"planned"}`, `"state":"planned","class":"primary"}`, 1)), want); err == nil {
		t.Fatal("schema 0.1 file with a class accepted")
	}
	if _, err := MarshalScope(d, want); err == nil {
		t.Fatal("schema 0.1 written")
	}
}
