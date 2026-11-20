package run

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

func (w *worker) limits() source.Limits {
	l := w.hello.Limits
	if l.MaxFiles <= 0 || l.MaxFileBytes <= 0 || l.MaxTotalBytes < l.MaxFileBytes || l.Retries < 0 || l.Retries > 5 {
		return source.DefaultLimits()
	}
	return source.Limits{MaxFiles: l.MaxFiles, MaxFileBytes: l.MaxFileBytes, MaxTotalBytes: l.MaxTotalBytes, Retries: l.Retries}
}

// acquire fixes the attempt input: a pinned retry only Loads the cached
// snapshot (never re-acquires); otherwise source acquisition reads local or Git
// source. The intent is journaled before any acquisition call (Git runs subprocesses that are
// not individually journaled). It returns a reason code on failure.
func (w *worker) acquire(a *attempt) string {
	job := a.job
	if err := w.intent(a, workerproto.IntentSourceAcquireStarted, "", 0, ""); err != nil {
		return nodeproto.ReasonSourceUnavailable
	}
	var (
		s      source.Snapshot
		err    error
		reason string
	)
	switch {
	case job.Snapshot != nil:
		reason = nodeproto.ReasonSnapshotUnavailable
		if job.Snapshot.Root == "" {
			err = source.ErrUnavailable
			break
		}
		s, err = source.Load(a.ctx, job.Snapshot.Root, job.Snapshot.ManifestDigest)
		if err == nil {
			dest := filepath.Join(w.hello.DataRoot, "ws-"+job.Attempt.ID)
			if err = source.CloneWorkspace(a.ctx, s, dest); err == nil {
				s.WorkspaceDir = dest
			}
		}
		if errors.Is(err, source.ErrIntegrity) {
			reason = nodeproto.ReasonSnapshotIntegrityFailed
		}
	case job.Source.Kind == "local":
		if err = source.CheckProtected(job.Source.ApprovedRoot, job.Source.RelativePath, job.Source.ProtectedDirs); err == nil {
			s, err = source.AcquireLocal(a.ctx, job.Source.ApprovedRoot, job.Source.RelativePath, w.hello.DataRoot, w.limits())
		}
		reason = sourceReason(err)
	case job.Source.Kind == "git":
		if w.hello.GitExecutable == "" || job.Source.AllowedHost == "" {
			err, reason = source.ErrUnsupported, nodeproto.ReasonSourceUnsupported
			break
		}
		a.anyProc.Store(true)
		credential := job.GitCredentialFile
		if credential == "" {
			credential = w.hello.GitCredentialFile
		}
		opts := source.GitOptions{Executable: w.hello.GitExecutable, AllowedHost: job.Source.AllowedHost, AllowedLocalRoot: job.Source.AllowedLocalRoot, Timeout: 10 * time.Minute, CABundle: w.hello.CABundle,
			Token: source.ReadHostToken(credential, job.Source.AllowedHost)}
		branch := job.Source.Branch
		if pr := job.PullRequest; pr != nil {
			// A pull request names its own head; the project's branch is not fetched.
			opts.PullRequest, opts.ExpectSHA, branch = pr.Number, pr.HeadSHA, ""
		}
		s, err = source.AcquireGit(a.ctx, job.Source.RepositoryURL, branch, w.hello.DataRoot, w.limits(), opts)
		reason = sourceReason(err)
	default:
		err, reason = source.ErrUnsupported, nodeproto.ReasonSourceUnsupported
	}
	result := "ok"
	if err != nil {
		result = reason
		if errors.Is(err, source.ErrRecoveryRequired) {
			a.uncertain.Store(true)
			result = "recovery_required"
		}
	}
	if ierr := w.intent(a, workerproto.IntentSourceAcquireDone, "", 0, result); ierr != nil {
		a.uncertain.Store(true)
	}
	if err != nil {
		return reason
	}
	a.snapshot = s
	if a.workspace == "" {
		a.workspace = s.WorkspaceDir
	}
	return ""
}
