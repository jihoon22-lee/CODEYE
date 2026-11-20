package review

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const MediaType = record.MediaType

type Caller interface {
	Review(context.Context, executor.Runner, opencode.Request) opencode.Reply
}

// Record types live in the I/O-free record package so report decoders can
// validate artifacts without importing model or process code.
type (
	CandidateText = record.CandidateText
	Excerpt       = record.Excerpt
	Verification  = record.Verification
)

func result(status domain.TaskStatus, reason string) analysis.TaskResult {
	return analysis.TaskResult{Status: status, Reason: reason}
}
func failure(reply opencode.Reply) analysis.TaskResult {
	switch reply.Reason {
	case "cancelled":
		return result(domain.TaskCancelled, reply.Reason)
	case "model_timeout":
		return result(domain.TaskTimedOut, reply.Reason)
	case "model_not_configured", "opencode_version_unsupported", opencode.ReasonCredentialUnavailable:
		return result(domain.TaskUnsupported, reply.Reason)
	case "policy_blocked":
		return result(domain.TaskSkippedPolicy, reply.Reason)
	}
	return result(domain.TaskFailed, reply.Reason)
}

// noteVersion adds opencode_version_untested when reply came through an
// OpenCode release other than the tested one.
func noteVersion(lims []string, reply opencode.Reply) []string {
	if opencode.VersionUntested(reply.Version) {
		return addLim(lims, opencode.LimVersionUntested)
	}
	return lims
}

// expect binds record validation to this attempt and its immutable snapshot
// manifest; prior stage records in scratch are not trusted on their own.
func expect(j analysis.Job, stage string) record.Expect {
	index := map[string]string{}
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.SHA256 != "" && e.ExcludedReason == "" && inScope(e.Path, j.Scope) {
			index[e.Path] = e.SHA256
		}
	}
	return record.Expect{Snapshot: j.Snapshot.Digest, RunID: j.RunID, AttemptID: j.AttemptID, Stage: stage,
		Lookup: func(p string) (string, bool) { sha, ok := index[p]; return sha, ok }}
}

func usage(u *opencode.Usage) *record.Usage {
	if u == nil {
		return nil
	}
	return &record.Usage{Input: u.Input, Output: u.Output}
}
