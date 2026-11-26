package parser

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/core/v2/logger"
	"github.com/sirupsen/logrus"
)

// namedT is a minimal TestingT whose Name() is a subtest path.
type namedT struct{ name string }

func (namedT) Fail()                     {}
func (namedT) FailNow()                  {}
func (namedT) Fatal(...any)              {}
func (namedT) Fatalf(string, ...any)     {}
func (namedT) Error(...any)              {}
func (namedT) Errorf(string, ...any)     {}
func (n namedT) Name() string            { return n.name }
func (namedT) Helper()                   {}

// A line logged by a subtest must end up in that subtest's own log file when the output is split per test.
func TestSubtestLogLineReachesSubtestFile(t *testing.T) {
	var out bytes.Buffer
	logger.DoLog(namedT{name: "TestParent/child_case"}, 2, &out, "message-from-child-case")
	logger.DoLog(namedT{name: "TestParent"}, 2, &out, "message-from-parent")

	dir := t.TempDir()
	SpawnParsers(logrus.New(), &out, dir)

	child, err := os.ReadFile(filepath.Join(dir, "TestParent", "child_case.log"))
	if err != nil {
		t.Fatalf("subtest log file missing (%v); split output was:\n%s", err, listLogs(dir))
	}
	if !strings.Contains(string(child), "message-from-child-case") {
		t.Fatalf("subtest log file lacks the subtest's line:\n%s", child)
	}
	parent, err := os.ReadFile(filepath.Join(dir, "TestParent.log"))
	if err != nil {
		t.Fatalf("parent log file missing: %v", err)
	}
	if strings.Contains(string(parent), "message-from-child-case") {
		t.Fatalf("parent log file holds the subtest's line:\n%s", parent)
	}
}

func listLogs(dir string) string {
	var b strings.Builder
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			data, _ := os.ReadFile(path)
			rel, _ := filepath.Rel(dir, path)
			b.WriteString(rel + ":\n" + string(data))
		}
		return nil
	})
	return b.String()
}
