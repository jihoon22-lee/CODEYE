package review

import (
	"context"
	"sync"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

type sourceIndexKey struct{}

type sourceIndexIdentity struct {
	run, attempt, manifest, root string
	epoch                        int64
}

func sourceIndexFor(j analysis.Job) sourceIndexIdentity {
	return sourceIndexIdentity{j.RunID, j.AttemptID, j.Snapshot.Digest, j.Snapshot.SnapshotDir, j.Epoch}
}

// WithAttemptSourceIndex shares immutable source metadata between tasks of
// one worker attempt. Individual tasks still verify the snapshot bytes they
// read and apply the current secrets screen and their own search budgets.
func WithAttemptSourceIndex(ctx context.Context, j analysis.Job) context.Context {
	if !j.AllowModelReview || j.Snapshot.Digest == "" {
		return ctx
	}
	return context.WithValue(ctx, sourceIndexKey{}, &attemptSourceIndex{
		identity: sourceIndexFor(j),
		manifest: j.Snapshot.Manifest,
	})
}

type attemptSourceIndex struct {
	identity sourceIndexIdentity
	manifest source.Manifest
	once     sync.Once
	catalog  *sourceCallCatalog
	mu       sync.Mutex
	checks   map[string]bool
}

func (s *roleStage) sourceIndex() *attemptSourceIndex {
	if s.ctx == nil {
		return nil
	}
	index, _ := s.ctx.Value(sourceIndexKey{}).(*attemptSourceIndex)
	if index != nil && index.identity == sourceIndexFor(s.j) {
		return index
	}
	return nil
}

func (i *attemptSourceIndex) callCatalog() *sourceCallCatalog {
	i.once.Do(func() { i.catalog = manifestCallCatalog(i.manifest) })
	return i.catalog
}

const maxCachedSourceChecks = 4096

// Content checks are memoized by verified blob hash. They hold no source text,
// eligibility decisions, errors, or scan-budget dependent results.
func (i *attemptSourceIndex) sensitive(sha string, verified []byte) bool {
	i.mu.Lock()
	answer, present := i.checks[sha]
	i.mu.Unlock()
	if present {
		return answer
	}
	answer = record.Sensitive(verified)
	i.mu.Lock()
	if len(i.checks) < maxCachedSourceChecks {
		if i.checks == nil {
			i.checks = make(map[string]bool)
		}
		i.checks[sha] = answer
	}
	i.mu.Unlock()
	return answer
}
