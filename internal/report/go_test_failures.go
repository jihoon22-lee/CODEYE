package report

import (
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/faildoc"
)

const (
	goTestFailureDecoder = faildoc.MediaType + ";schema_version=" + faildoc.Version
	goTestFailureReason  = "test_command_failed"
)

type goTestFailureSource struct {
	task         string
	sidecar, log ArtifactRef
	want         faildoc.Expect
}

type goTestFailureEvidence struct {
	sidecar string
	rows    []GoTestFailure
}

// ReadGoTestFailureBodies loads only streams referenced by a valid sidecar of
// an accepted test.run. This optional read uses a shared byte limit, and a
// failed read never blocks report publication. Build rechecks the bytes.
func (in *Input) ReadGoTestFailureBodies(read func(ArtifactRef) ([]byte, bool)) {
	if !in.mayHaveGoTestFailures() {
		return
	}
	if domain.Digest(in.SourceManifest) != in.Snapshot.ManifestDigest {
		return
	}
	manifest, err := source.DecodeManifest(in.SourceManifest)
	if err != nil {
		return
	}
	arts := make(map[string]ArtifactRef, len(in.Artifacts))
	for _, a := range in.Artifacts {
		arts[a.ID] = a
	}
	states := make(map[string]*taskState, len(in.Tasks))
	for _, t := range in.Tasks {
		states[t.Key] = &taskState{facts: t}
	}
	rm, reason := acceptResultManifest(*in, arts, states)
	if reason != "" {
		return
	}
	var remaining int64 = MaxDecodedBytes
	for _, c := range in.goTestFailureSources(manifest, rm, arts, states) {
		size := c.sidecar.SizeBytes + c.log.SizeBytes
		if size > remaining {
			continue
		}
		remaining -= c.sidecar.SizeBytes
		sidecar, ok := read(c.sidecar)
		if !ok || !matchesTestArtifact(c.sidecar, sidecar) {
			continue
		}
		if _, err := faildoc.Decode(sidecar, c.want); err != nil {
			continue
		}
		remaining -= c.log.SizeBytes
		stdout, ok := read(c.log)
		if !ok || !matchesTestArtifact(c.log, stdout) {
			continue
		}
		in.Bodies[c.sidecar.ID], in.Bodies[c.log.ID] = sidecar, stdout
	}
}

func (in Input) mayHaveGoTestFailures() bool {
	return in.Run.Profile == "with-tests" && in.Attempt.Cleanup == "confirmed" && in.ResultManifest != nil &&
		matchesTestArtifact(*in.ResultManifest, in.Bodies[in.ResultManifest.ID]) && slices.ContainsFunc(in.Artifacts, func(a ArtifactRef) bool {
		return a.Role == "other" && a.MediaType == faildoc.MediaType
	})
}

func matchesTestArtifact(a ArtifactRef, bytes []byte) bool {
	return len(bytes) > 0 && int64(len(bytes)) == a.SizeBytes && domain.Digest(bytes) == a.SHA256
}

func (in Input) goTestFailureSources(manifest source.Manifest, rm analysis.ResultManifest, arts map[string]ArtifactRef, states map[string]*taskState) []goTestFailureSource {
	if !in.mayHaveGoTestFailures() || rm.CleanupState != "confirmed" {
		return nil
	}
	modules, err := govet.PlanModules(manifest, domain.Scope{Kind: in.Run.ScopeKind, Paths: in.Run.ScopePaths}, "test.go", "Go tests")
	if err != nil || len(modules) == 0 {
		return nil
	}
	plans := make(map[string]govet.ModuleTask, len(modules))
	for _, m := range modules {
		plans[m.Key] = m
	}
	uses := map[string]int{}
	for _, t := range rm.Tasks {
		for _, a := range t.Artifacts {
			uses[a.ArtifactID]++
		}
		for _, e := range t.Executions {
			if e.StdoutArtifactID != nil {
				uses[*e.StdoutArtifactID]++
			}
		}
	}
	files := govet.IncludedFiles(manifest)
	dirs := govet.ModuleDirs(files)
	var out []goTestFailureSource
	for _, t := range rm.Tasks {
		plan, planned := plans[t.Key]
		fact := states[t.Key].facts // acceptResultManifest matched this task row.
		if !planned || fact.Stage != string(domain.StageTest) || !failedGoTestTask(t.Key, fact.Status, deref(fact.Reason)) ||
			t.Status != domain.TaskFailed || deref(t.Reason) != deref(fact.Reason) {
			continue
		}
		sidecar := uniqueTestSidecar(t.Artifacts, arts)
		if sidecar.ID == "" || uses[sidecar.ID] != 1 {
			continue
		}
		e := uniqueTestRun(t.Executions)
		if e == nil || e.Command.Dir != plan.ModuleDir || !matchesGoTestCommand(e.Command.Argv, plan.Patterns) ||
			e.Status != executor.StatusExited || e.Cleanup != executor.CleanupConfirmed || e.ExitCode == nil || *e.ExitCode <= 0 ||
			e.StdoutTruncated || e.StderrTruncated || e.StartedAt == "" || e.EndedAt == "" || e.StdoutArtifactID == nil {
			continue
		}
		log := arts[*e.StdoutArtifactID]
		if log.Role != analysis.RoleStdout || log.MediaType != "text/plain" || uses[log.ID] != 1 || log.SizeBytes <= 0 || log.SizeBytes > faildoc.MaxStdoutBytes {
			continue
		}
		allowed := map[string]string{}
		for _, p := range govet.GoFilesInScope(files, dirs, plan.ModuleDir, plan.Patterns) {
			if strings.HasSuffix(p, "_test.go") && sourceexcerpt.SafePath(p) {
				allowed[p] = files[p].SHA256
			}
		}
		want := faildoc.Expect{RunID: in.Run.ID, AttemptID: in.Attempt.ID, Snapshot: in.Snapshot.ManifestDigest, TaskKey: t.Key,
			ModuleDir: plan.ModuleDir, StdoutSHA256: log.SHA256, Lookup: func(p string) (string, bool) { sha, ok := allowed[p]; return sha, ok }}
		out = append(out, goTestFailureSource{task: t.Key, sidecar: sidecar, log: log, want: want})
	}
	return out
}

func failedGoTestTask(key, status, reason string) bool {
	return faildoc.GoTask(key) && status == string(domain.TaskFailed) &&
		(reason == goTestFailureReason || reason == "test_dependencies_unavailable")
}

func matchesGoTestCommand(argv, patterns []string) bool {
	if len(argv) < len(patterns)+3 {
		return false
	}
	end := len(argv) - len(patterns)
	command := slices.Index(argv[:end-1], "test")
	return command >= 0 && argv[command+1] == "-json" && slices.Equal(argv[end:], patterns)
}

func uniqueTestSidecar(list []analysis.ManifestArtifact, arts map[string]ArtifactRef) ArtifactRef {
	var sidecar ArtifactRef
	for _, a := range list {
		if a.MediaType != faildoc.MediaType {
			continue
		}
		if sidecar.ID != "" || a.Role != "other" || a.SizeBytes <= 0 || a.SizeBytes > faildoc.MaxBytes {
			return ArtifactRef{}
		}
		sidecar = arts[a.ArtifactID]
	}
	return sidecar
}

func uniqueTestRun(list []analysis.ManifestExecution) *analysis.ManifestExecution {
	var result *analysis.ManifestExecution
	for i := range list {
		if list[i].Name == "test.run" {
			if result != nil {
				return nil
			}
			result = &list[i]
		}
	}
	return result
}

func (in Input) decodeGoTestFailureEvidence(manifest source.Manifest, rm analysis.ResultManifest, arts map[string]ArtifactRef, states map[string]*taskState) map[string]goTestFailureEvidence {
	out := map[string]goTestFailureEvidence{}
	for _, c := range in.goTestFailureSources(manifest, rm, arts, states) {
		body, stdout := in.Bodies[c.sidecar.ID], in.Bodies[c.log.ID]
		if !matchesTestArtifact(c.sidecar, body) || !matchesTestArtifact(c.log, stdout) {
			continue
		}
		doc, err := faildoc.Decode(body, c.want)
		if err != nil || faildoc.ValidateEvents(doc, stdout) != nil {
			continue
		}
		rows := make([]GoTestFailure, 0, len(doc.Failures))
		for _, f := range doc.Failures {
			rows = append(rows, GoTestFailure{Name: f.Test, Path: f.Path, Line: f.Line})
		}
		out[c.task] = goTestFailureEvidence{sidecar: c.sidecar.ID, rows: rows}
	}
	return out
}

func validateGoTestFailureEvidence(r Report) error {
	sidecars := map[string]int{}
	streams := map[string]Artifact{}
	for _, a := range r.Artifacts {
		if a.Role == analysis.RoleStdout && a.MediaType == "text/plain" {
			streams[a.ID] = a
		}
		if deref(a.Decoder) != goTestFailureDecoder {
			continue
		}
		key := deref(a.TaskKey)
		if a.Role != "other" || a.MediaType != faildoc.MediaType || !builderAtLeast(r.Builder.Version, GoTestFailureEvidenceSince) ||
			!faildoc.GoTask(key) || a.SizeBytes <= 0 || a.SizeBytes > faildoc.MaxBytes {
			return invalidf("Go test failure artifact requires its task and builder")
		}
		sidecars[key]++
	}
	tasks := make(map[string]Task, len(r.Tasks))
	for _, t := range r.Tasks {
		tasks[t.Key] = t
	}
	for _, e := range r.Executions {
		if e.GoTestFailures == nil {
			continue
		}
		t := tasks[e.TaskKey]
		log := streams[deref(e.StdoutArtifactID)]
		if !builderAtLeast(r.Builder.Version, GoTestFailureEvidenceSince) || r.Inputs.Profile != "with-tests" || r.Attempt.CleanupState != "confirmed" ||
			!failedGoTestTask(t.Key, t.Status, deref(t.Reason)) || e.Name != "test.run" || e.ID != t.Key+"/test.run" ||
			!confirmedTestExit(e) || *e.ExitCode == 0 || e.StdoutTruncated || e.StderrTruncated || e.StartedAt == nil || e.EndedAt == nil ||
			deref(log.TaskKey) != t.Key || log.SizeBytes <= 0 || log.SizeBytes > faildoc.MaxStdoutBytes || sidecars[t.Key] != 1 ||
			len(e.GoTestFailures) == 0 || len(e.GoTestFailures) > faildoc.MaxFailures || e.Cwd == analysis.RedactedMarker {
			return invalidf("Go test failure evidence requires a confirmed failed Go test execution")
		}
		seen := map[GoTestFailure]bool{}
		for _, row := range e.GoTestFailures {
			if r.Export.IncludeCode && (!faildoc.TestName(row.Name) || seen[row]) || !r.Export.IncludeCode && row.Name != "" ||
				!sourceexcerpt.SafePath(row.Path) || !strings.HasSuffix(row.Path, "_test.go") || row.Line < 1 ||
				e.Cwd != "." && !strings.HasPrefix(row.Path, e.Cwd+"/") {
				return invalidf("invalid Go test failure evidence location or name")
			}
			seen[row] = true
		}
		delete(sidecars, t.Key)
	}
	if len(sidecars) != 0 {
		return invalidf("Go test failure artifact has no associated execution evidence")
	}
	return nil
}
