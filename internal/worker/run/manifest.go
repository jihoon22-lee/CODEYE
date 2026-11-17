package run

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// ResultManifestMediaType is the media type of the result_manifest artifact.
const ResultManifestMediaType = analysis.MediaResultManifest

// redactor rewrites node paths into placeholders.
type redactor struct {
	pairs      [][2]string
	workspaces []string
}

func (w *worker) redactor(a *attempt) redactor {
	var r redactor
	add := func(p, ph string) {
		if p == "" {
			return
		}
		r.pairs = append(r.pairs, [2]string{p, ph})
		if rp, err := filepath.EvalSymlinks(p); err == nil && rp != p {
			r.pairs = append(r.pairs, [2]string{rp, ph})
		}
	}
	for _, ws := range []string{a.workspace, a.snapshot.WorkspaceDir} {
		if ws == "" {
			continue
		}
		r.workspaces = append(r.workspaces, ws)
		if rp, err := filepath.EvalSymlinks(ws); err == nil && rp != ws {
			r.workspaces = append(r.workspaces, rp)
		}
	}
	add(a.scratch, "<scratch>")
	add(a.snapshot.Root, "<snapshot>")
	add(w.hello.DataRoot, "<data>")
	add(w.hello.ScratchRoot, "<scratch-root>")
	add(a.job.Source.ApprovedRoot, "<source>")
	// Longest first so nested roots are replaced by the most specific name.
	sort.SliceStable(r.pairs, func(i, j int) bool { return len(r.pairs[i][0]) > len(r.pairs[j][0]) })
	return r
}

func (r redactor) str(s string) string {
	for _, p := range r.pairs {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	return s
}

// workspaceMarked replaces the attempt workspace prefix by the W05 marker.
func (r redactor) workspaceMarked(s string) string {
	for _, ws := range r.workspaces {
		if s == ws {
			return analysis.RedactedMarker
		}
		if strings.HasPrefix(s, ws+string(filepath.Separator)) {
			return analysis.RedactedMarker + "/" + filepath.ToSlash(strings.TrimPrefix(s, ws+string(filepath.Separator)))
		}
	}
	return s
}

// manifestExec builds a path-free execution record. Workspace paths become
// the W05 redaction marker (so RelativeDir yields workspace-relative dirs),
// other node roots become placeholders; analysis.CommandFor then drops any
// remaining absolute value.
func (w *worker) manifestExec(a *attempt, red redactor, name string, res executor.Result) analysis.ManifestExecution {
	rec := res.Command
	rec.Dir = red.workspaceMarked(rec.Dir)
	argv := make([]string, len(rec.Argv))
	for i, arg := range rec.Argv {
		argv[i] = red.str(red.workspaceMarked(arg))
	}
	rec.Argv = argv
	cmd := analysis.CommandFor(rec)
	if cmd.EnvKeys == nil {
		cmd.EnvKeys = []string{}
	}
	return analysis.ManifestExecution{Name: name, Status: res.Status, ExitCode: res.ExitCode, Reason: res.Reason, Cleanup: res.Cleanup, Command: cmd, StdoutTruncated: res.Stdout.Truncated, StderrTruncated: res.Stderr.Truncated, StartedAt: analysis.FormatTime(res.StartedAt), EndedAt: analysis.FormatTime(res.EndedAt)}
}

// manifestForTask uploads a task's stream files and artifacts and builds its
// manifest entry. Executions come from the worker's own records; the handler
// supplies names and stream files matched by process identity.
func (w *worker) manifestForTask(a *attempt, t *taskRun, status domain.TaskStatus, reason string, res analysis.TaskResult) (analysis.ManifestTask, domain.TaskStatus, string) {
	red := w.redactor(a)
	mt := analysis.ManifestTask{TaskID: t.taskID, Key: t.spec.Key, Required: t.spec.Required, Executions: []analysis.ManifestExecution{}, Artifacts: []analysis.ManifestArtifact{}, Limitations: []string{}}
	for _, l := range res.Limitations {
		if nodeproto.ValidReason(l) {
			mt.Limitations = append(mt.Limitations, l)
		}
	}
	uploadFailed := false
	upload := func(path, role, media string) (analysis.ManifestArtifact, bool) {
		id, err := w.upload(a, path, role, media)
		if err != nil {
			uploadFailed = true
			return analysis.ManifestArtifact{}, false
		}
		sum, size := fileDigest(path)
		return analysis.ManifestArtifact{ArtifactID: id, Role: role, MediaType: media, SHA256: sum, SizeBytes: size}, true
	}
	a.recMu.Lock()
	recorded := append([]executor.Result(nil), a.recorded[t.spec.Key]...)
	a.recMu.Unlock()
	used := make([]bool, len(res.Executions))
	for _, rec := range recorded {
		var ex *analysis.Execution
		for i := range res.Executions {
			h := res.Executions[i].Result.Process
			if !used[i] && h.PID == rec.Process.PID && h.StartTime == rec.Process.StartTime {
				used[i], ex = true, &res.Executions[i]
				break
			}
		}
		name := "unreported"
		if ex != nil && ex.Name != "" {
			name = ex.Name
		}
		// Status/cleanup always come from the worker's own record. Command
		// metadata and streams are published only for executions the handler
		// returned explicitly (it may have redacted them); an unreported run
		// is listed without argv or output.
		me := w.manifestExec(a, red, name, rec)
		if ex == nil {
			me.Command = analysis.ManifestCommand{Argv: []string{}, Dir: analysis.RedactedMarker, EnvKeys: []string{}}
			me.StdoutTruncated, me.StderrTruncated = false, false
		} else {
			hc := w.manifestExec(a, red, name, ex.Result)
			me.Command = hc.Command
		}
		if ex != nil {
			if ex.StdoutPath != "" {
				if art, ok := upload(ex.StdoutPath, nodeproto.RoleStdout, "text/plain"); ok {
					me.StdoutArtifactID = &art.ArtifactID
				}
			}
			if ex.StderrPath != "" {
				if art, ok := upload(ex.StderrPath, nodeproto.RoleStderr, "text/plain"); ok {
					me.StderrArtifactID = &art.ArtifactID
				}
			}
		}
		mt.Executions = append(mt.Executions, me)
	}
	for _, u := range used {
		if !u {
			mt.Limitations = append(mt.Limitations, "unrecorded_execution_ignored")
			break
		}
	}
	for _, f := range res.Artifacts {
		role := f.Role
		if !nodeproto.ValidArtifactRole(role) || role == nodeproto.RoleSourceManifest || role == nodeproto.RoleResultManifest {
			role = nodeproto.RoleOther
		}
		if art, ok := upload(f.Path, role, f.MediaType); ok {
			mt.Artifacts = append(mt.Artifacts, art)
		}
	}
	if uploadFailed && status == domain.TaskSucceeded {
		status, reason = domain.TaskFailed, nodeproto.ReasonArtifactUploadFailed
	}
	mt.Status = status
	if reason != "" {
		r := reason
		mt.Reason = &r
	}
	return mt, status, reason
}

// writeManifest writes the validated canonical result manifest into scratch.
func (w *worker) writeManifest(a *attempt, h analysis.Handler, tasks []*taskRun, cleanup string) (string, error) {
	info := h.Info()
	m := analysis.ResultManifest{SchemaVersion: analysis.ResultManifestSchemaVersion, RunID: a.job.Attempt.RunID, AttemptID: a.job.Attempt.ID, LeaseEpoch: a.job.Attempt.Epoch, Snapshot: analysis.ManifestSnapshot{ID: a.snapshotID, ManifestDigest: a.snapshot.Digest}, Handlers: []analysis.ManifestHandler{{Name: info.Name, Version: info.Version, Capability: info.Capability}}, Tasks: []analysis.ManifestTask{}, CleanupState: cleanup, Limitations: []string{}}
	for _, t := range tasks {
		mt := t.manifest
		if !t.started {
			// Not run (stop_new_tasks): recorded as it is, never as success.
			mt = analysis.ManifestTask{TaskID: t.taskID, Key: t.spec.Key, Required: t.spec.Required, Status: domain.TaskSkippedPolicy, Executions: []analysis.ManifestExecution{}, Artifacts: []analysis.ManifestArtifact{}, Limitations: []string{"task_not_started"}}
		} else {
			mt.Status = t.status
		}
		m.Tasks = append(m.Tasks, mt)
	}
	canonical, err := m.MarshalCanonical()
	if err != nil {
		return "", err
	}
	path := filepath.Join(a.scratch, "result-manifest.json")
	return path, os.WriteFile(path, canonical, 0600)
}

func (w *worker) uploadManifest(a *attempt, path string) (string, error) {
	a.manifestClientID = domain.NewID()
	return w.uploadID(a, a.manifestClientID, path, nodeproto.RoleResultManifest, ResultManifestMediaType)
}
