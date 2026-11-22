package report

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

// TestCaseResults describes terminal cases reported by the test runner.
// Counts include cases outside the bounded sample. Names appear only in
// code-included reports. A test process may also fail outside a test case.
type TestCaseResults struct {
	Runner string           `json:"runner"`
	Counts resultdoc.Counts `json:"counts"`
	Cases  []resultdoc.Case `json:"cases,omitempty"`
}

const testResultDecoder = resultdoc.MediaType + ";schema_version=0.1"

func (in Input) testCases(task *taskState, e Execution, arts map[string]ArtifactRef, sources *SourceIndex) (*TestCaseResults, string) {
	if in.Run.Profile != "with-tests" || in.Attempt.Cleanup != "confirmed" || task.facts.Stage != string(domain.StageTest) ||
		!testTaskKey(e.TaskKey) || e.Name != "test.run" || !confirmedTestExit(e) || e.StdoutTruncated || e.StderrTruncated ||
		task.facts.Status != string(task.manifest.Status) || uniqueTestRun(task.manifest.Executions) == nil {
		return nil, ""
	}
	var sidecar ArtifactRef
	for _, ref := range task.manifest.Artifacts {
		a := arts[ref.ArtifactID]
		if a.Role == "other" && a.MediaType == resultdoc.MediaType {
			if sidecar.ID != "" {
				return nil, ""
			}
			sidecar = a
		}
	}
	if sidecar.ID == "" || sidecar.SizeBytes > resultdoc.MaxBytes {
		return nil, ""
	}
	raw := in.Bodies[sidecar.ID]
	out, errout := arts[deref(e.StdoutArtifactID)], arts[deref(e.StderrArtifactID)]
	if !matchesTestArtifact(sidecar, raw) || out.Role != analysis.RoleStdout || errout.Role != analysis.RoleStderr {
		return nil, ""
	}
	doc, err := resultdoc.Decode(raw, resultdoc.Expect{
		RunID: in.Run.ID, AttemptID: in.Attempt.ID, Snapshot: in.Snapshot.ManifestDigest, TaskKey: e.TaskKey,
		StdoutSHA256: out.SHA256, StderrSHA256: errout.SHA256,
		Lookup: func(path string) bool { _, found := sources.Lookup(path); return found },
	})
	if err != nil {
		return nil, ""
	}
	return &TestCaseResults{Runner: doc.Runner, Counts: doc.Counts, Cases: doc.Cases}, sidecar.ID
}

func validateTestCases(r Report) error {
	accepted := map[string]int{}
	for _, a := range r.Artifacts {
		if deref(a.Decoder) == testResultDecoder {
			if a.MediaType != resultdoc.MediaType || a.Role != "other" || !testTaskKey(deref(a.TaskKey)) {
				return invalidf("invalid test results artifact")
			}
			accepted[deref(a.TaskKey)]++
		}
	}
	for _, e := range r.Executions {
		cases := e.TestCases
		if cases == nil {
			continue
		}
		if !builderAtLeast(r.Builder.Version, TestCaseResultsSince) || r.Inputs.Profile != "with-tests" || r.Attempt.CleanupState != "confirmed" ||
			e.Name != "test.run" || !testTaskKey(e.TaskKey) || !confirmedTestExit(e) || e.StdoutTruncated || e.StderrTruncated || accepted[e.TaskKey] != 1 ||
			(!r.Export.IncludeCode && len(cases.Cases) > 0) || resultdoc.ValidateCases(cases.Runner, cases.Counts, cases.Cases, nil) != nil {
			return invalidf("invalid test case results")
		}
		delete(accepted, e.TaskKey)
	}
	if len(accepted) > 0 {
		return invalidf("test results artifact lacks execution")
	}
	return nil
}

// Each observed case counts once even when two overlapping citations refer to it.
func (v secView) citedTestCounts(refs []string, evidence []SectionEvidence) resultdoc.Counts {
	var out resultdoc.Counts
	if len(v.testEvidence) == 0 {
		return out
	}
	ranges := map[string][]SectionEvidence{}
	for _, e := range evidence {
		if e.Kind == "test" && slices.Contains(refs, e.ID) {
			ranges[e.Path] = append(ranges[e.Path], e)
		}
	}
	for path, spans := range ranges {
		for _, item := range v.testEvidence[path] {
			if !slices.ContainsFunc(spans, func(e SectionEvidence) bool { return e.StartLine <= item.line && item.line <= e.EndLine }) {
				continue
			}
			switch item.status {
			case resultdoc.Passed:
				out.Passed++
			case resultdoc.Failed:
				out.Failed++
			case resultdoc.Skipped:
				out.Skipped++
			}
		}
	}
	return out
}

type testLine struct {
	line   int
	status string
}

func testLines(executions []Execution) map[string][]testLine {
	out := map[string][]testLine{}
	for _, e := range executions {
		if e.TestCases == nil {
			continue
		}
		for _, c := range e.TestCases.Cases {
			if c.Path != "" {
				out[c.Path] = append(out[c.Path], testLine{c.Line, c.Status})
			}
		}
	}
	return out
}

func locatedFailure(c *TestCaseResults) bool {
	return c != nil && slices.ContainsFunc(c.Cases, func(item resultdoc.Case) bool { return item.Status == resultdoc.Failed && item.Path != "" })
}
