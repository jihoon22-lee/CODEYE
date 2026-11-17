package agent

import (
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"testing"
)

func TestModelSourceGrantIsSeparateFromAcquisition(t *testing.T) {
	approved, readOnly := domain.NewID(), domain.NewID()
	ag := Agent{cfg: Config{SourceRoots: map[string]string{approved: t.TempDir(), readOnly: t.TempDir()}, ModelSourceRoots: map[string]bool{approved: true}}}
	for _, tc := range []struct {
		kind, id string
		allowed  bool
	}{
		{"local", approved, true}, {"local", readOnly, false}, {"git", approved, false},
	} {
		a := activeAttempt{}
		a.job.Source = nodeproto.JobSource{Kind: tc.kind, SourceRootID: tc.id}
		if j := ag.buildJob(&a); j.AllowModelReview != tc.allowed {
			t.Fatalf("%s: unexpected model source grant", tc.kind)
		}
	}
}
