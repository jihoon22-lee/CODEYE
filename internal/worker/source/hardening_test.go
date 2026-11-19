package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitBin(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	bin, _ = filepath.Abs(bin)
	return bin
}

func acquire(t *testing.T, root, relative, data string) Snapshot {
	t.Helper()
	s, err := AcquireLocal(context.Background(), root, relative, data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	return s
}

func TestCommonCredentialFilesAreExcludedButSourcePackagesKept(t *testing.T) {
	root, data := fixture(t)
	secret := []string{
		".npmrc", ".netrc", "_netrc", ".git-credentials", ".pypirc", ".pgpass", ".htpasswd", "credentials", "credentials.json",
		"secrets.yaml", "secrets.json", "service-account-prod.json", "serviceAccount.json", "id_ecdsa", "id_dsa", "server.p12",
		"store.pfx", "app.jks", "release.keystore", "vault.kdbx", "infra/terraform.tfstate", "infra/prod.tfvars", "nested/.NPMRC",
		".docker/config.json", ".kube/config", "tools/.azure/profile.json", "kubeconfig", ".env", ".env.production", "a/b/private.pem",
	}
	kept := []string{
		"internal/credentials/store.go", "pkg/secrets/handler.go", ".env.example", ".env.sample", "docs/credentials.md",
		"src/service-account.go", "config/secretsmanager.yaml",
	}
	for _, p := range append(append([]string{}, secret...), kept...) {
		put(t, root, p, "body\n")
	}
	s := acquire(t, root, ".", data)
	for _, p := range secret {
		if reason := excludedReason(s, p); reason == "" {
			t.Errorf("%s was copied", p)
		}
		if _, err := os.Stat(filepath.Join(s.SnapshotDir, filepath.FromSlash(p))); err == nil {
			t.Errorf("%s exists in the snapshot", p)
		}
	}
	for _, p := range kept {
		if e := find(s, p); e.ExcludedReason != "" || e.SHA256 == "" {
			t.Errorf("%s was dropped: %+v", p, e)
		}
		if read(t, s.WorkspaceDir, p) != "body\n" {
			t.Errorf("%s missing from workspace", p)
		}
	}
}

// excludedReason is the reason for p or its nearest excluded ancestor directory.
func excludedReason(s Snapshot, p string) string {
	for cut := p; cut != "."; cut = filepath.ToSlash(filepath.Dir(cut)) {
		if reason := find(s, cut).ExcludedReason; reason != "" {
			return reason
		}
	}
	return ""
}

func TestGeneratedDirectoryExclusionIsTopLevelOrMarked(t *testing.T) {
	root, data := fixture(t)
	keep := []string{"internal/build/build.go", "src/dist/lib.c", "cmd/target/main.go", "pkg/venv/venv.go", "svc/venv/app.py"}
	drop := []string{"build/out.o", "dist/app.js", "target/debug/x", "venv/bin/python", ".venv/pyvenv.cfg", "svc/.venv/lib/x.py", "a/node_modules/m.js", "a/b/.git/HEAD", "a/__pycache__/x.pyc"}
	for _, p := range append(append([]string{}, keep...), drop...) {
		put(t, root, p, "x")
	}
	put(t, root, "svc/.venv/pyvenv.cfg", "home = /usr")
	s := acquire(t, root, ".", data)
	for _, p := range keep {
		if e := find(s, p); e.ExcludedReason != "" || e.SHA256 == "" {
			t.Errorf("%s dropped: %+v", p, e)
		}
	}
	for _, p := range drop {
		reason := excludedReason(s, p)
		top := strings.SplitN(p, "/", 2)[0]
		if reason != "generated_or_cache" {
			t.Errorf("%s (under %s) not excluded: %q", p, top, reason)
		}
	}
	if find(s, "svc/venv").ExcludedReason != "" {
		t.Error("a venv-named source directory without pyvenv.cfg was dropped")
	}
}

func TestGitAppliesSameExclusionPolicyAndRecordsRequest(t *testing.T) {
	root, data := fixture(t)
	git(t, root, "init", "-b", "main")
	for _, p := range []string{"internal/build/x.go", "build/out.o", "svc/.venv/pyvenv.cfg", "svc/.venv/lib.py", "svc/venv/app.py", ".npmrc", "pkg/credentials/c.go"} {
		put(t, root, p, "x")
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "x")
	before := time.Now().UTC().Add(-time.Second)
	s, err := AcquireGit(context.Background(), fileURL(root), "main", data, DefaultLimits(), GitOptions{Executable: gitBin(t), AllowedLocalRoot: root, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	for p, want := range map[string]string{"internal/build/x.go": "", "build/out.o": "generated_or_cache", "svc/.venv/lib.py": "generated_or_cache", "svc/venv/app.py": "", ".npmrc": "secret_file_policy", "pkg/credentials/c.go": ""} {
		if got := find(s, p).ExcludedReason; got != want {
			t.Errorf("%s excluded=%q want %q", p, got, want)
		}
	}
	m := s.Manifest
	if m.SourceBranch != "main" || m.RequestedAt.Before(before) || m.RequestedAt.After(m.FixedAt) {
		t.Fatalf("request metadata wrong: %q %v %v", m.SourceBranch, m.RequestedAt, m.FixedAt)
	}
	raw, err := os.ReadFile(s.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"file://", root, "originalfile", "http"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("manifest contains %q", leak)
		}
	}
	if _, err = Load(context.Background(), s.Root, s.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestApprovedRootConfinementHoldsWhenPathIsSwappedAfterValidation(t *testing.T) {
	base := t.TempDir()
	approved := filepath.Join(base, "approved")
	outside := filepath.Join(base, "outside")
	data := filepath.Join(base, "data")
	put(t, approved, "proj/inside.txt", "inside")
	put(t, outside, "secret.txt", "outside secret")
	_, err := AcquireLocal(context.Background(), approved, "proj", data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	cleanupAll(t, data)
	swap := localHooks{afterResolve: func() {
		if err := os.Rename(filepath.Join(approved, "proj"), filepath.Join(approved, "proj.old")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(approved, "proj")); err != nil {
			t.Skip("symlink creation unavailable")
		}
	}}
	s, err := acquireLocalHooks(context.Background(), approved, "proj", data, DefaultLimits(), swap)
	if err == nil {
		defer Remove(s.Root)
		t.Fatalf("acquired through a swapped symlink: %+v", s.Manifest.Entries)
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(data, "*", "snapshot", "secret.txt")); len(matches) != 0 {
		t.Fatal("outside file reached the data root")
	}
}

func cleanupAll(t *testing.T, data string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(data, "acquire-*"))
	for _, m := range matches {
		_ = Remove(m)
	}
}

func TestSelectedSubdirectoryAndDotBothOpenThroughTheApprovedRoot(t *testing.T) {
	base := t.TempDir()
	approved := filepath.Join(base, "approved")
	put(t, approved, "a/b/c.txt", "c")
	put(t, approved, "top.txt", "t")
	sub := acquire(t, approved, "a/b", filepath.Join(base, "data"))
	if find(sub, "c.txt").SHA256 == "" || find(sub, "top.txt").Path != "" {
		t.Fatal(sub.Manifest.Entries)
	}
	whole := acquire(t, approved, ".", filepath.Join(base, "data"))
	if find(whole, "a/b/c.txt").SHA256 == "" || find(whole, "top.txt").SHA256 == "" {
		t.Fatal(whole.Manifest.Entries)
	}
}

func TestDataRootMustBeOutsideTheProject(t *testing.T) {
	base := t.TempDir()
	approved := filepath.Join(base, "approved")
	put(t, approved, "proj/a.txt", "a")
	for name, data := range map[string]string{
		"inside the project":   filepath.Join(approved, "proj", "data"),
		"the project itself":   filepath.Join(approved, "proj"),
		"the root itself":      approved,
		"a parent of the root": base,
		"relative":             "data",
		"empty":                "",
	} {
		_, err := AcquireLocal(context.Background(), approved, "proj", data, DefaultLimits())
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(approved, "proj", "data")); !os.IsNotExist(err) {
		t.Error("data directory created inside the project")
	}
	// Beside the project under the same root (a personal state directory
	// under a $HOME root): the copy holds the project only.
	beside := filepath.Join(approved, ".state", "data")
	s, err := AcquireLocal(context.Background(), approved, "proj", beside, DefaultLimits())
	if err != nil {
		t.Fatalf("data root beside the project: %v", err)
	}
	cleanupSnapshot(t, s)
	if find(s, "a.txt").SHA256 == "" || len(s.Manifest.Entries) != 1 {
		t.Fatal(s.Manifest.Entries)
	}
	// The whole root as the project still refuses a data root inside it.
	if _, err := AcquireLocal(context.Background(), approved, ".", beside, DefaultLimits()); !errors.Is(err, ErrInvalid) {
		t.Errorf("data root inside a root-level project: %v", err)
	}
	for _, rel := range []string{".", "./proj"} {
		if _, err := AcquireLocal(context.Background(), ".", rel, filepath.Join(base, "data"), DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Errorf("relative approved root with %q: %v", rel, err)
		}
	}
	// A symlink alias of the project is resolved before the comparison.
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(approved, "proj"), alias); err == nil {
		if _, err := AcquireLocal(context.Background(), approved, "proj", filepath.Join(alias, "data"), DefaultLimits()); !errors.Is(err, ErrInvalid) {
			t.Errorf("symlinked data root: %v", err)
		}
	}
}

func TestGitDataRootMustBeOutsideAllowedLocalRoot(t *testing.T) {
	root, _ := fixture(t)
	git(t, root, "init", "-b", "main")
	put(t, root, "a.txt", "a")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "x")
	bin := gitBin(t)
	for name, tc := range map[string]struct{ allowed, data string }{
		"sibling of the repository inside the allowed parent": {filepath.Dir(root), filepath.Join(filepath.Dir(root), "data")},
		"inside the repository":                               {root, filepath.Join(root, "data")},
		"parent of the allowed root":                          {root, filepath.Dir(root)},
		"relative allowed root":                               {".", filepath.Join(t.TempDir(), "data")},
		"relative data root":                                  {root, "data"},
	} {
		_, err := AcquireGit(context.Background(), fileURL(root), "main", tc.data, DefaultLimits(), GitOptions{Executable: bin, AllowedLocalRoot: tc.allowed, Timeout: 10 * time.Second})
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	// Even a public-HTTPS request must not create platform storage inside a configured local root.
	_, err := AcquireGit(context.Background(), "https://example.invalid/r", "main", filepath.Join(root, "data"), DefaultLimits(), GitOptions{Executable: bin, AllowedHost: "example.invalid", AllowedLocalRoot: root, Timeout: time.Second})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("https with data inside local root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Error("data created inside the repository")
	}
}

func TestManifestDigestCoversTheWholeStoredManifest(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "one")
	s := acquire(t, root, ".", data)
	raw, err := os.ReadFile(s.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); s.Digest != hex.EncodeToString(sum[:]) {
		t.Fatal("digest is not the SHA-256 of manifest.json")
	}
	if len(raw) == 0 || raw[len(raw)-1] == '\n' || bytes.ContainsAny(raw, "\n ") && !bytes.Contains(raw, []byte(`"path":"a.txt"`)) {
		t.Fatal("manifest.json is not in canonical compact form")
	}
	restore := func() {
		if err := os.WriteFile(s.ManifestPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	mutations := map[string]func(m map[string]any){
		"limitations emptied": func(m map[string]any) { m["limitations"] = []any{} },
		"limitation added": func(m map[string]any) {
			m["limitations"] = append(m["limitations"].([]any), "lfs_objects_not_acquired")
		},
		"fixed time rewritten": func(m map[string]any) { m["source_fixed_at"] = "1999-01-01T00:00:00Z" },
		"entry size changed":   func(m map[string]any) { m["entries"].([]any)[0].(map[string]any)["size_bytes"] = 99 },
		"entry hash changed": func(m map[string]any) {
			m["entries"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
		},
		"unknown field added": func(m map[string]any) { m["origin"] = "/home/user/project" },
	}
	for name, mutate := range mutations {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		mutate(m)
		changed, _ := json.Marshal(m)
		if err := os.WriteFile(s.ManifestPath, changed, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(context.Background(), s.Root, s.Digest); !errors.Is(err, ErrIntegrity) {
			t.Errorf("%s: Load error %v", name, err)
		}
		// Even if the control plane's digest were recomputed over the tampered bytes,
		// fields outside the schema/semantics are still rejected.
		sum := sha256.Sum256(changed)
		if _, err := Load(context.Background(), s.Root, hex.EncodeToString(sum[:])); name == "unknown field added" && !errors.Is(err, ErrIntegrity) {
			t.Errorf("%s accepted with a matching digest", name)
		}
	}
	// Pretty-printing the same content changes the bytes, hence the digest check fails first.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.ManifestPath, pretty.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), s.Root, s.Digest); !errors.Is(err, ErrIntegrity) {
		t.Errorf("reformatted manifest accepted: %v", err)
	}
	sum := sha256.Sum256(pretty.Bytes())
	if _, err := Load(context.Background(), s.Root, hex.EncodeToString(sum[:])); !errors.Is(err, ErrIntegrity) {
		t.Errorf("non-canonical manifest accepted with a matching digest: %v", err)
	}
	restore()
	loaded, err := Load(context.Background(), s.Root, s.Digest)
	if err != nil || loaded.Digest != s.Digest || loaded.Manifest.FixedAt != s.Manifest.FixedAt {
		t.Fatal(err)
	}
}

func TestRetainedRetryKeepsDigestAndNewAcquisitionGetsANewOne(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "same bytes")
	first := acquire(t, root, ".", data)
	loaded, err := Load(context.Background(), first.Root, first.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err = CloneWorkspace(context.Background(), loaded, filepath.Join(data, "retry-1")); err != nil {
		t.Fatal(err)
	}
	again, err := Load(context.Background(), first.Root, first.Digest)
	if err != nil || again.Digest != first.Digest {
		t.Fatal("retry changed the digest", err)
	}
	time.Sleep(5 * time.Millisecond)
	second := acquire(t, root, ".", data)
	if second.Digest == first.Digest {
		t.Fatal("a new acquisition reused the digest although its fixed time differs")
	}
	if first.Manifest.Entries[0].SHA256 != second.Manifest.Entries[0].SHA256 {
		t.Fatal("same input bytes must keep the same file hashes")
	}
}

func TestCloneWorkspaceRejectsSymlinkedDestinationParents(t *testing.T) {
	root, data := fixture(t)
	put(t, root, "a.txt", "x")
	s := acquire(t, root, ".", data)
	victim := t.TempDir()
	link := filepath.Join(filepath.Dir(data), "link-out")
	if err := os.Symlink(victim, link); err != nil {
		t.Skip("symlink creation unavailable")
	}
	if err := CloneWorkspace(context.Background(), s, filepath.Join(link, "ws")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("clone through a symlink to elsewhere: %v", err)
	}
	if entries, _ := os.ReadDir(victim); len(entries) != 0 {
		t.Fatal("workspace written through the symlink")
	}
	// A symlink into the original tree is rejected as well.
	inOriginal := filepath.Join(filepath.Dir(data), "link-original")
	if err := os.Symlink(root, inOriginal); err != nil {
		t.Fatal(err)
	}
	if err := CloneWorkspace(context.Background(), s, filepath.Join(inOriginal, "ws")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("clone into the original tree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ws")); !os.IsNotExist(err) {
		t.Fatal("workspace written into the original tree")
	}
	// Existing destinations and relative paths are refused too.
	if err := CloneWorkspace(context.Background(), s, s.WorkspaceDir); err == nil {
		t.Fatal("overwrote an existing workspace")
	}
	if err := CloneWorkspace(context.Background(), s, "relative-ws"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := CloneWorkspace(context.Background(), s, filepath.Join(data, "fine")); err != nil {
		t.Fatal(err)
	}
}

// chunkReader returns small chunks forever and counts reads.
type chunkReader struct {
	reads  int
	onRead func(n int)
}

func (c *chunkReader) Read(p []byte) (int, error) {
	c.reads++
	if c.onRead != nil {
		c.onRead(c.reads)
	}
	return len(p), nil
}

func TestReadLimitedChecksContextBetweenChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &chunkReader{onRead: func(n int) {
		if n == 3 {
			cancel()
		}
	}}
	_, err := readLimited(ctx, r, 1<<30)
	if !errors.Is(err, context.Canceled) || r.reads != 3 {
		t.Fatalf("err=%v reads=%d", err, r.reads)
	}
	if _, err = readLimited(context.Background(), &chunkReader{}, 100<<10); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	b, err := readLimited(context.Background(), strings.NewReader(strings.Repeat("x", 3*readChunk+7)), 4*readChunk)
	if err != nil || len(b) != 3*readChunk+7 {
		t.Fatalf("exact read: %d %v", len(b), err)
	}
	if b, err = readLimited(context.Background(), io.LimitReader(strings.NewReader("abc"), 3), 3); err != nil || string(b) != "abc" {
		t.Fatalf("boundary read: %q %v", b, err)
	}
}

func TestGitFsckRejectsMalformedObjects(t *testing.T) {
	root, data := fixture(t)
	git(t, root, "init", "-b", "main")
	put(t, root, "x.txt", "x")
	blob := git(t, root, "hash-object", "-w", "x.txt")
	oid, err := hex.DecodeString(blob)
	if err != nil {
		t.Fatal(err)
	}
	// A tree with an entry named ".git": git add refuses to build it, but a hostile remote can serve it.
	tree := append([]byte("100644 .git\x00"), oid...)
	if err = os.WriteFile(filepath.Join(root, "tree.bin"), tree, 0600); err != nil {
		t.Fatal(err)
	}
	treeID := git(t, root, "hash-object", "-w", "-t", "tree", "--literally", "tree.bin")
	commit := git(t, root, "commit-tree", treeID, "-m", "hostile")
	git(t, root, "update-ref", "refs/heads/main", commit)
	_, err = AcquireGit(context.Background(), fileURL(root), "main", data, DefaultLimits(), GitOptions{Executable: gitBin(t), AllowedLocalRoot: root, Timeout: 20 * time.Second})
	if err == nil {
		t.Fatal("malformed tree accepted")
	}
	if errors.Is(err, ErrRecoveryRequired) {
		t.Fatalf("fsck rejection must be a clean failure: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(data, "acquire-*")); len(matches) != 0 {
		t.Fatal("rejected acquisition left a directory behind")
	}
}

func TestSnapshotManifestExamplesAndKeysMatchTheSchema(t *testing.T) {
	schemaBytes, err := os.ReadFile("../../../schemas/snapshot-manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err = json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatal(err)
	}
	examples, err := filepath.Glob("../../../examples/snapshot-manifest*.json")
	if err != nil || len(examples) < 2 {
		t.Fatalf("expected local and git examples, got %v", examples)
	}
	for _, path := range examples {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var m Manifest
		if err = decoder.Decode(&m); err != nil || validateManifest(m) != nil {
			t.Errorf("%s does not satisfy the manifest contract: %v / %v", path, err, validateManifest(m))
		}
		var keys map[string]any
		_ = json.Unmarshal(raw, &keys)
		for k := range keys {
			if _, ok := schema.Properties[k]; !ok && k != "$schema" {
				t.Errorf("%s: key %q is not in the schema", path, k)
			}
		}
		for _, k := range schema.Required {
			if _, ok := keys[k]; !ok {
				t.Errorf("%s: missing required key %q", path, k)
			}
		}
	}
	// Keys written by a real Git acquisition are all declared by the schema.
	root, data := fixture(t)
	git(t, root, "init", "-b", "main")
	put(t, root, "a.txt", "a")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "x")
	s, err := AcquireGit(context.Background(), fileURL(root), "main", data, DefaultLimits(), GitOptions{Executable: gitBin(t), AllowedLocalRoot: root, Timeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cleanupSnapshot(t, s)
	raw, _ := os.ReadFile(s.ManifestPath)
	var keys map[string]any
	_ = json.Unmarshal(raw, &keys)
	for k := range keys {
		if _, ok := schema.Properties[k]; !ok {
			t.Errorf("acquired manifest key %q is not in the schema", k)
		}
	}
}
