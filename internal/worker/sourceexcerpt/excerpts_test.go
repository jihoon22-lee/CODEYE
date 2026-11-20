package sourceexcerpt

import (
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
	dotenv := write(".env.test", "ABC=xyz\n")
	if _, ok := Read(root, dotenv, 1); ok {
		t.Fatal("credential path returned an excerpt")
	}
	long := write("long.go", "package source\n// "+strings.Repeat("x", 2000)+"\n")
	if _, ok := Read(root, long, 2); ok {
		t.Fatal("oversized anchor returned an excerpt")
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
