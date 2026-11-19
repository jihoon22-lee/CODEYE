package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/reportsvc"
)

// Comparison and finding reviews over real HTTP and PostgreSQL with the
// real report builder and publisher (go vet observations from the
// go vet example). Skips (NOT executed) without CODEYE_TEST_DATABASE_URL.

func (e *env) publishRun(a attemptRun) {
	e.t.Helper()
	if out, err := e.srv.publisher.PublishRun(context.Background(), a.runID); err != nil || out != reportsvc.OutcomePublished {
		e.t.Fatalf("publish %s: %s %v", a.runID, out, err)
	}
}

// observation edits of the go vet example (5 diagnostics in bad/).
func dropObservation(rule, path string, line int) func(map[string]any) {
	return func(doc map[string]any) {
		var kept []any
		for _, o := range doc["observations"].([]any) {
			m := o.(map[string]any)
			loc, _ := m["location"].(map[string]any)
			if m["rule_id"] == rule && loc != nil && loc["path"] == path && loc["start_line"] == float64(line) {
				continue
			}
			kept = append(kept, o)
		}
		doc["observations"] = kept
		doc["parse"].(map[string]any)["observations"] = len(kept)
	}
}

func shiftLines(by int) func(map[string]any) {
	return func(doc map[string]any) {
		for _, o := range doc["observations"].([]any) {
			if loc, ok := o.(map[string]any)["location"].(map[string]any); ok {
				loc["start_line"], loc["end_line"] = loc["start_line"].(float64)+float64(by), loc["end_line"].(float64)+float64(by)
			}
		}
	}
}

func toolFailed(doc map[string]any) {
	doc["observations"] = []any{}
	doc["parse"].(map[string]any)["observations"] = 0
	doc["outcome"] = map[string]any{"task_status": "failed", "reason": "package_errors", "exit_class": "execution_error", "partial_parse": false}
}

type comparison struct {
	Baseline *struct {
		RunID     string `json:"run_id"`
		Revision  int    `json:"revision"`
		Selection string `json:"selection"`
	} `json:"baseline"`
	Comparable          bool           `json:"comparable"`
	NotComparableReason *string        `json:"not_comparable_reason"`
	Counts              map[string]int `json:"counts"`
	Differences         []struct {
		Key string `json:"key"`
	} `json:"differences"`
	Entries []struct {
		Fingerprint string   `json:"fingerprint"`
		State       string   `json:"state"`
		Reason      *string  `json:"reason"`
		Rule        string   `json:"rule"`
		Paths       []string `json:"paths"`
		Changes     []string `json:"changes"`
		Current     []struct {
			FindingID string `json:"finding_id"`
		} `json:"current"`
	} `json:"entries"`
	Findings []struct {
		FindingID              string  `json:"finding_id"`
		Fingerprint            *string `json:"fingerprint"`
		State                  *string `json:"state"`
		PublicationDisposition string  `json:"publication_disposition"`
		CurrentDisposition     string  `json:"current_disposition"`
		ReviewMatch            string  `json:"review_match"`
	} `json:"findings"`
	Summary struct {
		AtPublication map[string]int `json:"at_publication"`
		Current       map[string]int `json:"current"`
	} `json:"summary"`
	CanReview bool `json:"can_review"`
}

func (e *env) comparison(u user, path string) comparison {
	e.t.Helper()
	r := e.do("GET", path, nil, u.o()).want(e.t, 200, "")
	var c comparison
	if err := json.Unmarshal(r.raw, &c); err != nil {
		e.t.Fatal(err)
	}
	if strings.Contains(string(r.raw), "raw_message") || strings.Contains(string(r.raw), `"title"`) {
		e.t.Fatalf("comparison carries finding text: %s", r.raw)
	}
	return c
}

func TestComparisonAndReviewsOverHTTP(t *testing.T) {
	e := newEnv(t)
	base := e.drive(nil, driveOpts{taskStatus: "succeeded"})
	e.publishRun(base)
	f := base.f
	owner := f.user
	viewer, stranger := e.newUser(), e.newUser()
	if err := e.store.SetMember(context.Background(), owner.id, f.projectID, viewer.id, domain.RoleViewer); err != nil {
		t.Fatal(err)
	}
	c := e.comparison(owner, "/api/v1/runs/"+base.runID+"/comparison")
	if c.Baseline != nil || c.Comparable || *c.NotComparableReason != "no_baseline" || !c.CanReview || len(c.Findings) != 5 || c.Findings[0].Fingerprint == nil || c.Findings[0].State != nil {
		t.Fatalf("first run comparison %+v", c)
	}
	if r := e.do("GET", "/api/v1/runs/"+base.runID+"/reports", nil, owner.o()).want(t, 200, ""); r.body["default_baseline"] != nil {
		t.Fatalf("default baseline of the first run %s", r.raw)
	}
	// Review the copylocks diagnostic whose fingerprint is unique in the run.
	full := e.do("GET", "/api/v1/runs/"+base.runID+"/report?include_code=true", nil, owner.o()).want(t, 200, "")
	var target map[string]any
	counts := map[string]int{}
	for _, x := range full.body["findings"].([]any) {
		counts[x.(map[string]any)["fingerprint"].(string)]++
	}
	for _, x := range full.body["findings"].([]any) {
		m := x.(map[string]any)
		if strings.HasPrefix(m["title"].(string), "go vet copylocks: Copy passes") && counts[m["fingerprint"].(string)] == 1 {
			target = m
		}
	}
	if target == nil {
		t.Fatalf("no unique copylocks finding in %s", full.raw)
	}
	fp := target["fingerprint"].(string)
	review := func(disposition string) map[string]any {
		return map[string]any{"fingerprint": fp, "disposition": disposition, "reason": "테스트 환경 전용 코드다", "run_id": base.runID, "revision": 1, "finding_id": target["id"]}
	}
	post := func(u user, body any, o opts) resp {
		o.cookie, o.csrf = u.cookie, u.csrf
		return e.do("POST", "/api/v1/projects/"+f.projectID+"/finding-reviews", body, o)
	}
	post(viewer, review("false_positive"), opts{idem: "v1", ifNoneMatch: "*"}).want(t, 403, "FORBIDDEN")
	post(stranger, review("false_positive"), opts{idem: "s1", ifNoneMatch: "*"}).want(t, 404, "NOT_FOUND")
	post(owner, review("false_positive"), opts{idem: "o0"}).want(t, 400, "INVALID_REQUEST")
	bad := review("false_positive")
	bad["reason"] = "  "
	post(owner, bad, opts{idem: "o0", ifNoneMatch: "*"}).want(t, 422, "VALIDATION_FAILED")
	bad = review("false_positive")
	bad["finding_id"] = "fnd_unknown"
	post(owner, bad, opts{idem: "o0b", ifNoneMatch: "*"}).want(t, 422, "VALIDATION_FAILED")
	created := post(owner, review("false_positive"), opts{idem: "o1", ifNoneMatch: "*"}).want(t, 201, "")
	if created.header.Get("ETag") != `"1"` || created.body["version"] != float64(1) || created.body["actor_id"] != owner.id {
		t.Fatalf("created %s", created.raw)
	}
	if replay := post(owner, review("false_positive"), opts{idem: "o1", ifNoneMatch: "*"}).want(t, 200, ""); replay.body["id"] != created.body["id"] {
		t.Fatalf("replay %s", replay.raw)
	}
	post(owner, review("suppressed"), opts{idem: "o1", ifNoneMatch: "*"}).want(t, 409, "IDEMPOTENCY_CONFLICT")
	post(owner, review("suppressed"), opts{idem: "o2", ifNoneMatch: "*"}).want(t, 409, "CONFLICT")
	post(owner, review("suppressed"), opts{idem: "o3", ifMatch: `"7"`}).want(t, 409, "CONFLICT")
	// Viewers read the trail.
	list := e.do("GET", "/api/v1/projects/"+f.projectID+"/finding-reviews?fingerprint="+fp, nil, viewer.o()).want(t, 200, "")
	if list.body["can_review"] != false || len(list.body["items"].([]any)) != 1 {
		t.Fatalf("viewer list %s", list.raw)
	}
	e.do("GET", "/api/v1/projects/"+f.projectID+"/finding-reviews", nil, stranger.o()).want(t, 404, "NOT_FOUND")

	// Run 2: lines shifted, the printf diagnostic of bad/bad.go fixed.
	fixed := e.drive(&f, driveOpts{taskStatus: "succeeded", edit: func(doc map[string]any) {
		dropObservation("printf", "bad/bad.go", 12)(doc)
		shiftLines(2)(doc)
	}})
	e.publishRun(fixed)
	if r := e.do("GET", "/api/v1/runs/"+fixed.runID+"/reports", nil, viewer.o()).want(t, 200, ""); r.body["default_baseline"].(map[string]any)["run_id"] != base.runID {
		t.Fatalf("default baseline %s", r.raw)
	}
	c = e.comparison(viewer, "/api/v1/runs/"+fixed.runID+"/comparison")
	if c.Baseline == nil || c.Baseline.RunID != base.runID || c.Baseline.Selection != "default" || !c.Comparable || c.CanReview {
		t.Fatalf("baseline %+v", c)
	}
	if c.Counts["resolved_observed"] != 1 || c.Counts["persisting"] != 4 || c.Counts["new"]+c.Counts["changed"]+c.Counts["absent_unverified"] != 0 {
		t.Fatalf("counts %v entries %+v", c.Counts, c.Entries)
	}
	for _, x := range c.Entries {
		if x.State == "resolved_observed" && (x.Rule != "printf" || x.Paths[0] != "bad/bad.go" || x.Reason != nil) {
			t.Fatalf("resolved %+v", x)
		}
		if x.State == "persisting" && (len(x.Changes) != 1 || x.Changes[0] != "lines") {
			t.Fatalf("persisting %+v", x)
		}
	}
	// The disposition carried at publication: stamped in the stored revision.
	stamped := false
	for _, x := range c.Findings {
		if x.Fingerprint != nil && *x.Fingerprint == fp {
			stamped = x.PublicationDisposition == "false_positive" && x.CurrentDisposition == "false_positive" && x.ReviewMatch == "applied"
		}
	}
	if !stamped || c.Summary.AtPublication["hidden_findings"] != 1 || c.Summary.Current["hidden_findings"] != 1 || c.Summary.Current["total_findings"] != 4 {
		t.Fatalf("publication disposition %+v %+v", c.Findings, c.Summary)
	}
	stored := e.do("GET", "/api/v1/runs/"+fixed.runID+"/report?include_code=true", nil, owner.o()).want(t, 200, "")
	if stored.body["summary"].(map[string]any)["hidden_findings"] != float64(1) {
		t.Fatalf("stored summary %s", stored.body["summary"])
	}
	// Reverting changes the current view only; the published revision keeps
	// the disposition it was published with.
	post(owner, review("open"), opts{idem: "o4", ifMatch: `"1"`}).want(t, 201, "")
	c = e.comparison(owner, "/api/v1/runs/"+fixed.runID+"/comparison")
	for _, x := range c.Findings {
		if x.Fingerprint != nil && *x.Fingerprint == fp && (x.PublicationDisposition != "false_positive" || x.CurrentDisposition != "open" || x.ReviewMatch != "reverted") {
			t.Fatalf("after revert %+v", x)
		}
	}
	if c.Summary.AtPublication["hidden_findings"] != 1 || c.Summary.Current["hidden_findings"] != 0 {
		t.Fatalf("summary after revert %+v", c.Summary)
	}

	// TC-14: run 3's go vet failed; nothing vanished is resolved.
	failed := e.drive(&f, driveOpts{taskStatus: "failed", edit: toolFailed})
	e.publishRun(failed)
	c = e.comparison(owner, "/api/v1/runs/"+failed.runID+"/comparison?baseline="+fixed.runID)
	if c.Baseline.Selection != "explicit" || c.Counts["absent_unverified"] != 4 || c.Counts["resolved_observed"] != 0 {
		t.Fatalf("tool failure %v %+v", c.Counts, c.Entries)
	}
	for _, x := range c.Entries {
		if x.Reason == nil || *x.Reason != "check_failed" {
			t.Fatalf("absent reason %+v", x)
		}
	}
	diff := map[string]bool{}
	for _, d := range c.Differences {
		diff[d.Key] = true
	}
	if !diff["tool:go.vet:result"] || !diff["tool:go.vet:processed_files"] {
		t.Fatalf("differences %+v", c.Differences)
	}
	// A failed run's report is never a default baseline.
	again := e.drive(&f, driveOpts{taskStatus: "succeeded"})
	e.publishRun(again)
	if c := e.comparison(owner, "/api/v1/runs/"+again.runID+"/comparison"); c.Baseline.RunID != fixed.runID {
		t.Fatalf("default baseline after a failed run %+v", c.Baseline)
	}
	// Scope reduction: a paths run that does not cover bad/.
	narrow := e.drive(&f, driveOpts{taskStatus: "succeeded", scope: map[string]any{"kind": "paths", "paths": []string{"cmd"}}, edit: func(doc map[string]any) {
		doc["observations"] = []any{}
		doc["parse"].(map[string]any)["observations"] = 0
		doc["outcome"].(map[string]any)["exit_class"] = "success_without_findings"
	}})
	e.publishRun(narrow)
	c = e.comparison(owner, "/api/v1/runs/"+narrow.runID+"/comparison?baseline="+again.runID)
	if c.Counts["resolved_observed"] != 0 || c.Counts["absent_unverified"] == 0 {
		t.Fatalf("scope reduction %v", c.Counts)
	}
	for _, x := range c.Entries {
		if *x.Reason != "scope_reduced" {
			t.Fatalf("scope reason %+v", x)
		}
	}
	// Baselines are limited to the same project.
	second := e.do("POST", "/api/v1/projects", map[string]any{"name": "second", "source": map[string]any{"kind": "local", "source_root_id": f.root.ID, "relative_path": "."}}, owner.o()).want(t, 201, "")
	otherFixture := nodeFixture{user: owner, root: f.root, projectID: second.body["id"].(string)}
	other := e.drive(&otherFixture, driveOpts{taskStatus: "succeeded"})
	e.publishRun(other)
	e.do("GET", "/api/v1/runs/"+fixed.runID+"/comparison?baseline="+other.runID, nil, owner.o()).want(t, 404, "NOT_FOUND")
	e.do("GET", "/api/v1/runs/"+fixed.runID+"/comparison?baseline="+fixed.runID, nil, owner.o()).want(t, 422, "VALIDATION_FAILED")
	e.do("GET", "/api/v1/runs/"+fixed.runID+"/comparison?baseline_revision=1", nil, owner.o()).want(t, 400, "INVALID_REQUEST")
	e.do("GET", "/api/v1/runs/"+fixed.runID+"/comparison", nil, stranger.o()).want(t, 404, "NOT_FOUND")
	// The code-free export carries no fingerprint.
	if free := e.do("GET", "/api/v1/runs/"+fixed.runID+"/report", nil, owner.o()).want(t, 200, ""); strings.Contains(string(free.raw), "fp2:") || strings.Contains(string(free.raw), "fingerprint") {
		t.Fatalf("code-free export carries identity")
	}
}
