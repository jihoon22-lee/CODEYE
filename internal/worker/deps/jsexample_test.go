package deps

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
)

// examples/dependencies-javascript.json is a record the decoder accepts, in
// the canonical form Marshal writes.
func TestJSRecordExampleDecodes(t *testing.T) {
	b, err := os.ReadFile("../../../examples/dependencies-javascript.json")
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Snapshot  string `json:"snapshot_manifest_digest"`
		RunID     string `json:"run_id"`
		AttemptID string `json:"attempt_id"`
		TaskKey   string `json:"task_key"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		t.Fatal(err)
	}
	want := depdoc.Expect{Snapshot: head.Snapshot, RunID: head.RunID, AttemptID: head.AttemptID, TaskKey: head.TaskKey}
	r, err := depdoc.Decode(b, want)
	if err != nil || !r.TreeUsable() {
		t.Fatalf("example: %v", err)
	}
	canon, err := depdoc.Marshal(r, want)
	if err != nil || strings.TrimSpace(string(b)) != string(canon) {
		t.Fatalf("example is not the canonical record: %v\n%s", err, canon)
	}
}
