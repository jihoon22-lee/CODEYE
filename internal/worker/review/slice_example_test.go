package review

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// sliceExampleDir holds the review.scope artifact and the first slice's two
// records the worker writes for fixtures/modules/slices/input behind a
// clean secrets scan with the one-call script (coverageScript), indented
// like the stage examples. CODEYE_UPDATE_EXAMPLES=1 rewrites them;
// otherwise a regenerated run must match byte for byte.
const sliceExampleDir = "../../../examples/review/slices"

const (
	sliceExampleSnapshot = "5555555555555555555555555555555555555555555555555555555555555555"
	sliceExampleRun      = "0195f3a0-0000-7000-8000-000000000041"
	sliceExampleAttempt  = "0195f3a0-0000-7000-8000-000000000042"
)

func TestSliceExampleRecords(t *testing.T) {
	j := coverageJob(t)
	j.Snapshot.Digest, j.RunID, j.AttemptID = sliceExampleSnapshot, sliceExampleRun, sliceExampleAttempt
	f := &scriptedCaller{script: coverageScript(t)}
	_, res := runRoleWith(t, j, Handler{Static: secretsStub{}, Caller: f, MaxCalls: DefaultMaxCalls}, "1.27.4")
	update := os.Getenv("CODEYE_UPDATE_EXAMPLES") == "1"
	files := map[string]string{
		"scope.json":            scopePath(j),
		"investigate.s001.json": recordPath(j, "investigate.s001"),
		"verify.s001.json":      recordPath(j, "verify.s001"),
	}
	for _, k := range []string{record.TaskScope, "review.investigate.s001", "review.verify.s001"} {
		if res[k].Status != domain.TaskSucceeded {
			t.Fatalf("%s %+v", k, res[k])
		}
	}
	for name, src := range files {
		raw := mustRead(t, src)
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err != nil {
			t.Fatal(err)
		}
		pretty.WriteByte('\n')
		path := filepath.Join(sliceExampleDir, name)
		if update {
			if err := os.MkdirAll(sliceExampleDir, 0o755); err != nil {
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
	}
}
