package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review-v1"
)

// W15 phase 2 keeps review-v1 byte-identical: the default pack, its
// per-role digests and texts, the corpus it selects and every request it
// sends are pinned to the values captured at 5ff38e6 before the R1 candidate
// pack was added. A change here is a change of the active pack and needs the
// promotion evidence of docs/16 §8, not a test update.
func TestReviewV1IsPinned(t *testing.T) {
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	packs := map[string][2]string{
		"context":     {"89d35b04633d2dca52d22e89f52b6187dc43001396622616622f1fdfaec7b43b", "568d07f905398919a3c43d68a0fd4ab81bd99f7a55d57f1341e500821934cb39"},
		"investigate": {"dffe2f5da7168be7b279c6d70a15164b336746c0ca6dc91e93e5bf018c59ec31", "6cabd32592534ba5bf17c4ad36306f77a56bdf54470a9b221f947d9c5e33014a"},
		"verify":      {"91fae3032a783078cdcbef985e4612b631eeec1a86e25020f27335a82a37f0ec", "27d3ae0c94228b93988467c6fef42774d718c41d130b89d752b50e5ea31ebaa9"},
	}
	for role, want := range packs {
		s, err := reviewpack.For(role)
		if err != nil || s.Digest != want[0] || sum([]byte(s.Text)) != want[1] {
			t.Fatalf("review-v1 %s pack changed: %s", role, s.Digest)
		}
		if !slices.Equal(s.Files, []string{"00-core.md", map[string]string{"context": "01-context.md", "investigate": "02-investigate.md", "verify": "03-verify.md"}[role], "domains.md", "languages/go.md", "pack.yaml"}) {
			t.Fatalf("review-v1 %s files %v", role, s.Files)
		}
	}

	// The default handler plans the three R0 roles.
	plan, err := Handler{Static: stubStatic{}}.Plan(t.Context(), analysis.Job{})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, p := range plan {
		keys = append(keys, p.Key)
	}
	if !slices.Equal(keys, []string{"static", "review.context", "review.investigate", "review.verify"}) {
		t.Fatal(keys)
	}

	// Corpus selection on the labelled verify fixture (with README).
	j := verifyJob(t, true)
	c, err := buildCorpus(j.Snapshot, j.Scope)
	if err != nil {
		t.Fatal(err)
	}
	type sel struct {
		path, kind string
		start, end int
	}
	var got []sel
	for _, e := range c.Excerpts {
		got = append(got, sel{e.Path, e.Kind, e.Start, e.End})
	}
	wantSel := []sel{{"README.md", "document", 1, 1}, {"load.go", "code", 1, 50}, {"store.go", "code", 1, 9}, {"go.mod", "config", 1, 3}, {"store_test.go", "test", 1, 50}}
	if !slices.Equal(got, wantSel) || c.Eligible != 5 || c.Documents != 1 || !slices.Equal(c.Limitations, []string{"excerpt_only"}) {
		t.Fatalf("v1 corpus selection changed: %+v eligible=%d docs=%d lims=%v", got, c.Eligible, c.Documents, c.Limitations)
	}

	// Every request of the three roles: role, instructions and input bytes.
	f := script(t, 1, func(k int, in sentInput) opencode.Reply {
		return ok(map[string]any{"verifications": []any{verdict("candidate_1", "inconclusive", func(m map[string]any) {
			m["unresolved_assumptions"] = []string{"호출자의 정리 경로를 확인하지 못했다."}
		})}})
	})
	if _, r := runStages(t, j, f); r.Status != domain.TaskSucceeded {
		t.Fatal(r)
	}
	// The snapshot digest (and so every evidence ID) differs per acquisition;
	// normalize both before hashing the exact input bytes.
	names := map[string]string{j.Snapshot.Digest: "SNAPSHOT"}
	var sent []pinnedRequest
	for _, q := range f.requests {
		for _, e := range decodeInput(t, q).Scope.Excerpts {
			names[e.ID] = fmt.Sprintf("ev:%s:%d-%d", e.Path, e.Start, e.End)
		}
		input := string(q.Input)
		for from, to := range names {
			input = strings.ReplaceAll(input, from, to)
		}
		sent = append(sent, pinnedRequest{q.Role, sum([]byte(q.Instructions)), sum([]byte(input))})
	}
	if !slices.Equal(sent, pinnedV1Requests) {
		for _, s := range sent {
			t.Logf("%s instructions=%s input=%s", s.role, s.instructions, s.input)
		}
		t.Fatalf("review-v1 requests changed")
	}
}

type pinnedRequest struct{ role, instructions, input string }

var pinnedV1Requests = []pinnedRequest{
	{"context", "355003a09ceebfb77f7d7ebaaa0b27b398bf5fb41c5e24312e500f48006c6a7f", "c4f238509f331b3b0f8d718615010678e6c65a6a9abece75da90ca03e215289a"},
	{"investigate", "5320a19e77e14cdf757a23c85833e0d0d223045d07dfcffe5d0031a91210557e", "9cf48984575970c8b6fc75eda1c5758c5113fd591e6d3943be40e5cec59ba7ae"},
	{"verify", "bdd78ed463a1e7f88ac48f9c1852d228e5a23c51693b9f518b3c08388b79f51f", "8faef9e813c42d578f7ddea6241c430a4632738c66b7d29004ee595e0f624913"},
}
