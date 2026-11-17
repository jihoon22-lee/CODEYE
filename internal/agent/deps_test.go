package agent

import (
	"os/exec"
	"strings"
	"testing"
)

// Invariant 1: node-side packages never reach the database.
func TestNodePackagesDoNotImportStorage(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go command not available")
	}
	for _, tags := range []string{"", "codeye_fixture"} {
		args := []string{"list", "-deps"}
		if tags != "" {
			args = append(args, "-tags", tags)
		}
		args = append(args, "github.com/jihoon22-lee/CODEYE/internal/agent/...", "github.com/jihoon22-lee/CODEYE/internal/worker/...", "github.com/jihoon22-lee/CODEYE/internal/workerproto", "github.com/jihoon22-lee/CODEYE/internal/nodeproto")
		out, err := exec.Command("go", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("go list: %v\n%s", err, out)
		}
		for _, dep := range strings.Fields(string(out)) {
			if strings.Contains(dep, "internal/storage") || strings.Contains(dep, "CODEYE/migrations") || strings.HasPrefix(dep, "github.com/jackc/") || strings.Contains(dep, "internal/httpapi") {
				t.Errorf("tags=%q: node package depends on %s", tags, dep)
			}
		}
	}
}
