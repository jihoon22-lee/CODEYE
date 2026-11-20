package sourceexcerpt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

func TestReadChecksSnapshotAndWithholdsSensitiveFiles(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	write := func(path string, data string) source.Entry {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		n := int64(len(data))
		return source.Entry{Path: path, Kind: "file", Size: &n, SHA256: domain.Digest([]byte(data))}
	}
	entry := write("safe.go", "package safe\n\nfunc ready() bool {\n\treturn true\n}\n")
	got, ok := Read(root, entry, 4)
	if !ok || got.StartLine != 1 || got.EndLine != 5 || !strings.Contains(got.Text, "\treturn true\n") {
		t.Fatalf("incorrect snapshot excerpt: %+v, %v", got, ok)
	}
	_ = write("safe.go", "changed after source snapshot")
	if _, ok := Read(root, entry, 4); ok {
		t.Fatal("snapshot drift returned an excerpt")
	}
	credential := "example" + "token0987654321"
	secret := write("secret.go", "package secret\n"+strings.Repeat("var a int\n", 12)+"var token = \""+credential+"\"\n")
	if _, ok := Read(root, secret, 1); ok {
		t.Fatal("file containing a credential outside the window returned an excerpt")
	}
	if _, ok := ReadRange(root, secret, 1, 6); ok {
		t.Fatal("model excerpt returned a file with a credential beyond its citation")
	}
	dotenv := write(".env.test", "ABC=xyz\n")
	if _, ok := Read(root, dotenv, 1); ok {
		t.Fatal("credential path returned an excerpt")
	}
	long := write("long.go", "package source\n// "+strings.Repeat("x", 2000)+"\n")
	if _, ok := Read(root, long, 2); ok {
		t.Fatal("oversized anchor returned an excerpt")
	}
}

func TestReadModelRangeKeepsExactWholeLinesWithinBudgets(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("// route line %d", i+1)
	}
	lines[12] = "  const request = await input.json();"
	lines[21] = "  return Response.json({ error: 'request failed' }, { status: 500 });"
	write := func() source.Entry {
		data := []byte(strings.Join(lines, "\n") + "\n")
		if err := os.WriteFile(filepath.Join(dir, "route.ts"), data, 0600); err != nil {
			t.Fatal(err)
		}
		n := int64(len(data))
		return source.Entry{Path: "route.ts", Kind: "file", Size: &n, SHA256: domain.Digest(data)}
	}
	entry := write()
	assertRange := func(citedEnd, expectedEnd int) {
		t.Helper()
		got, ok := ReadRange(root, entry, 1, citedEnd)
		if !ok || got.StartLine != 1 || got.EndLine != expectedEnd || got.Text != strings.Join(lines[:expectedEnd], "\n") {
			t.Fatalf("citations 1–%d: got %d–%d, want 1–%d with the original bytes", citedEnd, got.StartLine, got.EndLine, expectedEnd)
		}
	}
	assertRange(24, 24)
	assertRange(60, 50)
	diagnostic, ok := Read(root, entry, 13)
	if !ok || diagnostic.StartLine != 10 || diagnostic.EndLine != 18 || diagnostic.Text != strings.Join(lines[9:18], "\n") {
		t.Fatalf("the diagnostic should retain its narrow context: %+v", diagnostic)
	}
	lines[24] = strings.Repeat("<", 340) // Short raw text that exceeds the JSON-escaped budget.
	entry = write()
	assertRange(60, 24)
	if _, ok := ReadRange(root, entry, 25, 26); ok {
		t.Fatal("a too-large first line was skipped or cut")
	}
}

func TestDecodeAllowsSeparateModelAndDiagnosticWindowsAtOneAnchor(t *testing.T) {
	sha := strings.Repeat("b", 64)
	lookup := func(p string) (string, bool) { return sha, p == "route.ts" }
	diagnostic := Item{Path: "route.ts", SHA256: sha, Anchor: 4, Excerpt: Excerpt{StartLine: 1, EndLine: 9, Text: strings.Repeat("line\n", 8) + "line"}}
	model := Item{Path: "route.ts", SHA256: sha, Anchor: 4, Excerpt: Excerpt{StartLine: 4, EndLine: 24, Text: strings.Repeat("line\n", 20) + "line"}}
	for _, tc := range []struct {
		version string
		items   []Item
		accept  bool
	}{
		{LegacyVersion, []Item{diagnostic}, true},
		{LegacyVersion, []Item{model}, false},
		{Version, []Item{diagnostic, model}, true},
		{Version, []Item{model, model}, false},
	} {
		doc := Document{SchemaVersion: tc.version, RunID: "run", AttemptID: "attempt", Snapshot: "snapshot", Items: tc.items}
		body, err := Encode(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := Decode(body, doc.RunID, doc.AttemptID, doc.Snapshot, lookup); ok != tc.accept {
			t.Errorf("version %s with %d items accepted=%t, want %t", tc.version, len(tc.items), ok, tc.accept)
		}
	}
}

func TestDecodeChecksAnchorAndSnapshot(t *testing.T) {
	sha := strings.Repeat("0", 64)
	doc := Document{SchemaVersion: Version, RunID: "run", AttemptID: "attempt", Snapshot: "snapshot",
		Items: []Item{{Path: "main.go", SHA256: sha, Anchor: 3, Excerpt: Excerpt{StartLine: 3, EndLine: 4, Text: "package main\nfunc main() {}"}}}}
	lookup := func(p string) (string, bool) { return sha, p == "main.go" }
	body, _ := Encode(doc)
	if _, ok := Decode(body, doc.RunID, doc.AttemptID, doc.Snapshot, lookup); !ok {
		t.Fatal("snapshot-bound excerpt rejected")
	}
	if _, ok := Decode(body, doc.RunID, "other", doc.Snapshot, lookup); ok {
		t.Fatal("foreign attempt accepted")
	}
	doc.Items[0].Anchor = 2
	body, _ = Encode(doc)
	if _, ok := Decode(body, doc.RunID, doc.AttemptID, doc.Snapshot, lookup); ok {
		t.Fatal("excerpt with a missing anchor accepted")
	}
}
