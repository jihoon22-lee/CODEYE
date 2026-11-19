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

// roleExample decodes a role-pack example record (examples/review-r1, written
// for fixtures/modules/input) with a lookup built from the fixture itself.
func roleExample(t *testing.T, stage string) ([]byte, RoleRecord, Expect) {
	t.Helper()
	b, err := os.ReadFile("../../../../examples/review-r1/" + stage + ".json")
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
	var head RoleRecord
	if err := json.Unmarshal(b, &head); err != nil {
		t.Fatal(err)
	}
	want := Expect{Snapshot: head.Snapshot, RunID: head.RunID, AttemptID: head.AttemptID, Stage: stage, Lookup: func(p string) (string, bool) { s, ok := lookup[p]; return s, ok }}
	r, err := DecodeRole(compact.Bytes(), want)
	if err != nil {
		t.Fatal(err)
	}
	return compact.Bytes(), r, want
}

// asSlice turns a whole-run investigate or verify record into a record of
// slice s001 (schema 0.3, its own upstream names).
func asSlice(r RoleRecord) RoleRecord {
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
	raw, inv, want := roleExample(t, StageInvestigate)
	again, err := MarshalRole(inv, want)
	if err != nil || !bytes.Equal(again, raw) || bytes.Contains(raw, []byte(`"slice"`)) {
		t.Fatalf("0.2 record bytes changed: %v", err)
	}
	sliceWant := want
	sliceWant.Slice = "s001"
	good := asSlice(inv)
	b, err := MarshalRole(good, sliceWant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRole(b, sliceWant); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRole(b, want); err == nil {
		t.Fatal("slice record accepted as a whole-run record")
	}
	mutations := map[string]func(*RoleRecord){
		"no slice on 0.3":  func(r *RoleRecord) { r.Slice = nil },
		"slice on 0.2":     func(r *RoleRecord) { r.Schema = SchemaVersionRole },
		"key s000":         func(r *RoleRecord) { r.Slice.Key = "s000" },
		"key s033":         func(r *RoleRecord) { r.Slice.Key = "s033" },
		"other slice":      func(r *RoleRecord) { r.Slice.Key = "s002" },
		"upper case key":   func(r *RoleRecord) { r.Slice.Key = "S001" },
		"no modules":       func(r *RoleRecord) { r.Slice.Modules = []string{} },
		"nil modules":      func(r *RoleRecord) { r.Slice.Modules = nil },
		"unsorted modules": func(r *RoleRecord) { r.Slice.Modules = []string{"go:svc/b", "go:."} },
		"duplicate module": func(r *RoleRecord) { r.Slice.Modules = []string{"go:.", "go:."} },
		"invalid module":   func(r *RoleRecord) { r.Slice.Modules = []string{"go"} },
		"absolute module":  func(r *RoleRecord) { r.Slice.Modules = []string{"go:/etc"} },
		"too many modules": func(r *RoleRecord) {
			r.Slice.Modules = strings.Split("go:a go:b go:c go:d go:e go:f go:g go:h go:i", " ")
		},
		"interpret stage": func(r *RoleRecord) { r.Stage = StageInterpret },
		"unknown schema":  func(r *RoleRecord) { r.Schema = "0.4" },
		"interpret upstream": func(r *RoleRecord) {
			r.Upstream = append(r.Upstream, Upstream{Source: "review.interpret", State: UpstreamSkipped, Reason: "no_tool_observations"})
		},
		"other slice upstream": func(r *RoleRecord) {
			r.Upstream = append(r.Upstream, Upstream{Source: "review.investigate.s002", State: UpstreamSkipped, Reason: "x"})
		},
	}
	for name, mutate := range mutations {
		r := asSlice(inv)
		r.Slice = &SliceRef{Key: r.Slice.Key, Modules: append([]string(nil), r.Slice.Modules...)}
		mutate(&r)
		if _, err := MarshalRole(r, sliceWant); err == nil {
			t.Errorf("%s accepted", name)
		}
		if b, err := json.Marshal(r); err == nil {
			if _, err := DecodeRole(b, sliceWant); err == nil {
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
		if _, err := DecodeRole(b, sliceWant); err == nil {
			t.Error("unknown slice field decoded")
		}
	}

	// A slice verify carries its own slice's investigate candidates only.
	_, ver, verWant := roleExample(t, StageVerify)
	verWant.Slice = "s001"
	slice := asSlice(ver)
	if _, err := MarshalRole(slice, verWant); err == nil {
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
	if _, err := MarshalRole(slice, verWant); err != nil {
		t.Fatalf("slice verify: %v", err)
	}
	slice.Upstream = append(slice.Upstream, Upstream{Source: "review.investigate", State: UpstreamSkipped, Reason: "x"})
	if _, err := MarshalRole(slice, verWant); err == nil {
		t.Fatal("slice verify naming the overview investigate accepted")
	}
}

func TestSliceTaskKeys(t *testing.T) {
	for key, want := range map[string]string{"review.investigate.s001": "investigate s001", "review.verify.s032": "verify s032"} {
		stage, slice, ok := SliceTask(key)
		if !ok || stage+" "+slice != want {
			t.Fatalf("%s: %s %s %v", key, stage, slice, ok)
		}
	}
	for _, key := range []string{"review.investigate", "review.scope", "review.interpret.s001", "review.verify.s033", "review.verify.s1", "go-vet"} {
		if _, _, ok := SliceTask(key); ok {
			t.Fatalf("%s accepted", key)
		}
	}
	if !ValidMaxCalls(WholeRunMaxCalls) || !ValidMaxCalls(MaxMaxCalls) || ValidMaxCalls(14) || ValidMaxCalls(208) || MaxMaxCalls != 207 || !ValidMaxCalls(DefaultMaxCalls) {
		t.Fatal("call ceiling range")
	}
}

// validScope is a small consistent scope artifact over synthetic paths.
func validScope() (ScopeDoc, Expect) {
	sha := strings.Repeat("a", 64)
	lookup := map[string]string{"a/main.go": sha, "a/b.go": sha, "a/c.go": sha, "a/d_test.go": sha, "w/app.py": sha}
	want := Expect{Snapshot: strings.Repeat("7", 64), RunID: domain.NewID(), AttemptID: domain.NewID(), Lookup: func(p string) (string, bool) { s, ok := lookup[p]; return s, ok }}
	file := func(p, module, state string, lines int) ScopeFile {
		f := ScopeFile{Path: p, SHA256: sha, Module: module, BoundBytes: 2400, TotalLines: 60, State: state}
		if lines > 0 {
			f.PlannedLines, f.PlannedBytes = lines, 1200
		}
		return f
	}
	d := ScopeDoc{Schema: ScopeSchemaVersion, Kind: ScopeKind, Pack: PackRef{ID: "review-r1", Version: "2.0.0"}, Snapshot: want.Snapshot, RunID: want.RunID, AttemptID: want.AttemptID,
		Screen: ScreenApplied, Budget: ScopeBudget{MaxCalls: DefaultMaxCalls, MaxRequestBytes: 40000, WholeRunMaxCalls: WholeRunMaxCalls, SliceReserveCalls: SliceReserveCalls, CallsBeforeSlices: 7, Admission: AdmissionSequentialReserve},
		Limits:   ScopeLimits{MaxFiles: 8, MaxBytes: 12000, MaxFileBytes: 2400, MaxFileLines: 50, MaxSlices: MaxSlices},
		Upstream: []Upstream{{Source: "review.investigate", State: UpstreamAccepted, SHA256: strings.Repeat("b", 64)}},
		Modules:  []ScopeModule{{ID: "go:a", Discovered: true, FilesEligible: 4}, {ID: "python:w", FilesEligible: 1}},
		Overview: ScopeOverview{Files: []ScopeFile{file("a/main.go", "go:a", FileCoveredByOverview, 50), file("w/app.py", "python:w", FileUnreadable, 0)}, Offered: []string{"a/b.go", "a/main.go"}},
		Slices: []ScopeSlice{{Key: "s001", InvestigateTask: "review.investigate.s001", VerifyTask: "review.verify.s001", Modules: []string{"go:a"},
			Files: []ScopeFile{file("a/b.go", "go:a", FileCoveredByOverview, 50), file("a/c.go", "go:a", FilePlanned, 50), file("a/d_test.go", "go:a", FileWithheld, 0)}}},
		Limitations: []string{}}
	return d, want
}

// TC-164: the scope artifact binds the attempt and snapshot and partitions
// every eligible file once, with states allowed where they occur.
func TestScopeArtifactValidation(t *testing.T) {
	d, want := validScope()
	b, err := MarshalScope(d, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeScope(b, want); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*ScopeDoc){
		"other attempt":          func(d *ScopeDoc) { d.AttemptID = domain.NewID() },
		"screen not applied":     func(d *ScopeDoc) { d.Screen = "unscreened" },
		"ceiling too low":        func(d *ScopeDoc) { d.Budget.MaxCalls = 14 },
		"ceiling too high":       func(d *ScopeDoc) { d.Budget.MaxCalls = 208 },
		"other admission":        func(d *ScopeDoc) { d.Budget.Admission = "worst_case" },
		"file twice":             func(d *ScopeDoc) { d.Slices[0].Files[1].Path = "a/main.go" },
		"file not in snapshot":   func(d *ScopeDoc) { d.Slices[0].Files[1].Path = "a/x.go" },
		"not source":             func(d *ScopeDoc) { d.Slices[0].Files[1].Path = "README.md" },
		"planned in overview":    func(d *ScopeDoc) { d.Overview.Files[0].State = FilePlanned },
		"withheld but planned":   func(d *ScopeDoc) { d.Slices[0].Files[2].PlannedLines, d.Slices[0].Files[2].PlannedBytes = 5, 50 },
		"bytes over bound":       func(d *ScopeDoc) { d.Slices[0].Files[1].PlannedBytes = 2401 },
		"unknown module":         func(d *ScopeDoc) { d.Slices[0].Files[1].Module = "go:z" },
		"module count":           func(d *ScopeDoc) { d.Modules[0].FilesEligible = 5 },
		"slice key out of order": func(d *ScopeDoc) { d.Slices[0].Key = "s002" },
		"slice task":             func(d *ScopeDoc) { d.Slices[0].VerifyTask = "review.verify" },
		"offered not covered":    func(d *ScopeDoc) { d.Slices[0].Files[0].State = FilePlanned },
		"covered not offered":    func(d *ScopeDoc) { d.Overview.Offered = []string{"a/main.go"} },
		"offered unsorted":       func(d *ScopeDoc) { d.Overview.Offered = []string{"a/main.go", "a/b.go"} },
		"not planned below limit": func(d *ScopeDoc) {
			d.Modules[0].FilesNotPlanned = 1
			d.Modules[0].FilesEligible = 5
		},
		"upstream other source": func(d *ScopeDoc) { d.Upstream[0].Source = "review.verify" },
		"slice modules":         func(d *ScopeDoc) { d.Slices[0].Modules = []string{"go:a", "python:w"} },
	}
	for name, mutate := range mutations {
		d, want := validScope()
		mutate(&d)
		if _, err := MarshalScope(d, want); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
