package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	reviewpackr1 "github.com/jihoon22-lee/CODEYE/prompts/review-r1"
)

// exampleDir holds the five review-r1 stage records the worker writes for
// fixtures/w15-modules/input with the scripted caller (validScript) and one
// synthetic go vet observation. They are indented copies of the exact record
// bytes (json.Compact restores them), so an upstream sha256 of a review stage
// is the digest of its compacted sibling. CODEYE_UPDATE_EXAMPLES=1 rewrites
// them; otherwise a regenerated run must match byte for byte.
const exampleDir = "../../../examples/review-r1"

// Fixed identities make the run reproducible (source_fixed_at changes the
// real snapshot digest on every acquisition).
const (
	exampleSnapshot = "6666666666666666666666666666666666666666666666666666666666666666"
	exampleRun      = "0195f3a0-0000-7000-8000-000000000025"
	exampleAttempt  = "0195f3a0-0000-7000-8000-000000000026"
)

func TestR1ExampleRecords(t *testing.T) {
	j := w15Job(t, nil)
	j.Snapshot.Digest, j.RunID, j.AttemptID = exampleSnapshot, exampleRun, exampleAttempt
	f := &r1Caller{script: validScript(t)}
	plan, res := runR1(t, j, vetStub{obs: gatewayObservation}, f, "1.27.4")
	for _, r := range rows(plan, res) {
		if r.status != "succeeded" {
			t.Fatalf("%+v", rows(plan, res))
		}
	}
	update := os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1"
	stages := []string{"environment", "documents", "investigate", "interpret", "verify"}
	for _, stage := range stages {
		raw := mustRead(t, r1Path(j, stage))
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		path := filepath.Join(exampleDir, stage+".json")
		if update {
			if err := os.MkdirAll(exampleDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, pretty.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(have, pretty.Bytes()) {
			t.Fatalf("%s differs from the regenerated record; rerun with CODEYE_UPDATE_EXAMPLES=1 and review the diff", path)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, have); err != nil || !bytes.Equal(compact.Bytes(), raw) {
			t.Fatalf("%s does not compact to the record bytes", path)
		}
		rec := readR1(t, j, stage)
		sel, err := reviewpackr1.For(stage)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range rec.Calls {
			if c.PackDigest != sel.Digest || c.PackID != sel.ID || c.PackVersion != sel.Version || strings.Join(c.PackFiles, ",") != strings.Join(sel.Files, ",") {
				t.Fatalf("%s call pack %+v, pack %s %s", stage, c, sel.ID, sel.Digest)
			}
		}
		for _, u := range rec.Upstream {
			if p, ok := strings.CutPrefix(u.Source, "review."); ok && u.State == "accepted" && u.SHA256 != domain.Digest(mustRead(t, r1Path(j, p))) {
				t.Fatalf("%s upstream %s sha", stage, u.Source)
			}
		}
	}
}
