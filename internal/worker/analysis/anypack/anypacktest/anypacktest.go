// Package anypacktest holds the fixtures the adapter tests of the any-ecosystem analyzers share
// (docs/ANALYZERS.md): a snapshot with an attempt workspace, one task run through
// a given runner, the envelope read back, tool executions replayed from
// captured streams, and the generated fake secret with its leak checks. Only
// tests import it.
package anypacktest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Fixture is a snapshot of files with a cloned attempt workspace.
type Fixture struct {
	SourceRoot string
	Job        analysis.Job
}

// New acquires a snapshot of files under base (a temp dir when "") and
// clones an attempt workspace like the worker.
func New(t testing.TB, base string, files map[string][]byte, scope domain.Scope) Fixture {
	t.Helper()
	if base == "" {
		base = t.TempDir()
	}
	src, data, scratch := filepath.Join(base, "source"), filepath.Join(base, "data"), filepath.Join(base, "scratch")
	for rel, body := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{src, data, scratch} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	snap, err := source.AcquireLocal(ctx, src, ".", data, source.DefaultLimits())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	loaded, err := source.Load(ctx, snap.Root, snap.Digest)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ws := filepath.Join(data, "attempt-workspace")
	if err := source.CloneWorkspace(ctx, loaded, ws); err != nil {
		t.Fatalf("clone: %v", err)
	}
	loaded.WorkspaceDir = ws
	if scope.Kind == "" {
		scope.Kind = "full"
	}
	return Fixture{SourceRoot: src, Job: analysis.Job{RunID: "run-1", AttemptID: "attempt-1", Epoch: 1, Profile: "deep-review", Scope: scope, Snapshot: loaded, Scratch: scratch}}
}

// Canary writes a file outside any source tree holding value and returns
// its path: the target of the hostile links of the security fixtures.
func Canary(t testing.TB, value string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "outside-canary.py")
	if err := os.WriteFile(p, []byte("TOKEN = \""+value+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// NewLinked is New with symbolic links in the source tree: links maps a
// relative path to its (absolute, outside) target. The snapshot records a
// link as link_not_followed and never copies it into the workspace.
func NewLinked(t testing.TB, files map[string][]byte, links map[string]string, scope domain.Scope) Fixture {
	t.Helper()
	base := t.TempDir()
	src := filepath.Join(base, "source")
	for rel, target := range links {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	return New(t, base, files, scope)
}

// LinkInWorkspace plants a link to target at rel in the attempt workspace
// (a changed copy): the adapters must refuse to run.
func LinkInWorkspace(t testing.TB, fx Fixture, rel, target string) {
	t.Helper()
	p := filepath.Join(fx.Job.Snapshot.WorkspaceDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(p)
	if err := os.Symlink(target, p); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// AssertNoSnippet fails when a captured stream of res holds one of the
// source lines (the executor stores the streams as artifacts).
func AssertNoSnippet(t testing.TB, res analysis.TaskResult, lines ...string) {
	t.Helper()
	for _, e := range res.Executions {
		for _, l := range lines {
			if bytes.Contains(e.Result.Stdout.Data, []byte(l)) || bytes.Contains(e.Result.Stderr.Data, []byte(l)) {
				t.Fatalf("%s streams hold source text %q", e.Name, l)
			}
		}
	}
}

// Text converts string sources to bytes.
func Text(m map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(m))
	for k, v := range m {
		out[k] = []byte(v)
	}
	return out
}

// RunTask plans the job with a and runs the task key through runner.
func RunTask(t testing.TB, a pack.Analyzer, job analysis.Job, key string, runner executor.Runner) analysis.TaskResult {
	t.Helper()
	tasks, err := a.Plan(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Spec.Key == key {
			return a.Run(context.Background(), job, task.Spec, runner)
		}
	}
	t.Fatalf("task %s not planned: %+v", key, tasks)
	return analysis.TaskResult{}
}

// ReadEnvelope reads and validates the task's observations artifact.
func ReadEnvelope(t testing.TB, job analysis.Job, res analysis.TaskResult) (obsdoc.Document, []byte) {
	t.Helper()
	if len(res.Artifacts) != 1 || res.Artifacts[0].Role != analysis.RoleObservations || res.Artifacts[0].MediaType != analysis.MediaObservations {
		t.Fatalf("observations artifact missing: %+v", res)
	}
	raw, err := os.ReadFile(res.Artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Base(filepath.Dir(res.Artifacts[0].Path))
	doc, err := obsdoc.Decode(raw, pack.Expect(job, key))
	if err != nil {
		t.Fatalf("envelope invalid: %v\n%s", err, raw)
	}
	if dump := os.Getenv("CODEYE_STRUCTURE_DUMP"); dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "-" + key + ".json"
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return doc, raw
}

// TreeHash digests the paths, modes and contents below root.
func TreeHash(t testing.TB, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := filepath.ToSlash(rel) + "|" + info.Mode().String()
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			line += "|" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Recorded is one captured tool execution.
type Recorded struct {
	Stdout, Stderr []byte
	Exit           int
}

// LoadCapture reads testdata/<name>.stdout|stderr|exit.
func LoadCapture(t testing.TB, name string) Recorded {
	t.Helper()
	read := func(suffix string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", name+suffix))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(read(".exit"))))
	if err != nil {
		t.Fatal(err)
	}
	return Recorded{Stdout: read(".stdout"), Stderr: read(".stderr"), Exit: code}
}

// Capture writes the streams and exit code of execution exec of res to
// $CODEYE_STRUCTURE_CAPTURE/<name>.* when that variable is set: how the
// testdata/ captures of the replay tests were taken (docs/ANALYZERS.md). A capture
// holding a node path or secret is refused.
func Capture(t testing.TB, name, exec string, res analysis.TaskResult, secrets ...string) {
	t.Helper()
	dir := os.Getenv("CODEYE_STRUCTURE_CAPTURE")
	if dir == "" {
		return
	}
	for _, e := range res.Executions {
		if e.Name != exec {
			continue
		}
		code := "none"
		if e.Result.ExitCode != nil {
			code = strconv.Itoa(*e.Result.ExitCode)
		}
		for suffix, data := range map[string][]byte{".stdout": e.Result.Stdout.Data, ".stderr": e.Result.Stderr.Data, ".exit": []byte(code + "\n")} {
			if bytes.Contains(data, []byte("/home/")) || bytes.Contains(data, []byte("/tmp/")) || bytes.Contains(data, []byte(`\Users\`)) {
				t.Fatalf("capture %s%s holds a node path", name, suffix)
			}
			for _, s := range secrets {
				AssertAbsent(t, s, "capture "+name+suffix, data)
			}
			if err := os.WriteFile(filepath.Join(dir, name+suffix), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Replay is a runner for one fake tool: an execution whose executable is
// Fake is answered by Answer (from captured streams) and never started.
// Every spec is recorded.
type Replay struct {
	Fake   string
	Answer func(spec executor.Spec) Recorded
	Mutate func(spec executor.Spec, r *executor.Result)
	Specs  []executor.Spec
}

// Run implements executor.Runner.
func (r *Replay) Run(_ context.Context, spec executor.Spec) executor.Result {
	r.Specs = append(r.Specs, spec)
	if spec.Executable != r.Fake {
		return executor.Result{Status: executor.StatusRejected, Reason: "unexpected executable", Cleanup: executor.CleanupConfirmed}
	}
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rec := r.Answer(spec)
	code := rec.Exit
	res := executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed, Containment: executor.ContainmentNone,
		Command: executor.CommandRecord{Executable: spec.Executable, Argv: append([]string{spec.Executable}, spec.Args...), Dir: analysis.RedactedMarker, EnvKeys: keys},
		Stdout:  executor.Stream{Data: append([]byte{}, rec.Stdout...), TotalBytes: int64(len(rec.Stdout))},
		Stderr:  executor.Stream{Data: append([]byte{}, rec.Stderr...), TotalBytes: int64(len(rec.Stderr))}}
	if r.Mutate != nil {
		r.Mutate(spec, &res)
	}
	return res
}

// FakeExecutable writes a file that stands for a tool path in replay tests
// (never executed).
func FakeExecutable(t testing.TB, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("not executed: replayed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// secretAlphabet is the base32 alphabet of the generated key body.
const secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// Secret generates a fake credential at test time, never committed: the
// shape of an AWS access key ID ("AKIA" and 16 base32 characters, which
// gitleaks' aws-access-token rule matches) with a random body. Its length
// is fixed (20) so the replay captures keep their positions.
func Secret(t testing.TB) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	out := []byte("AKIA")
	for _, c := range b {
		out = append(out, secretAlphabet[int(c)%len(secretAlphabet)])
	}
	return string(out)
}

// Placeholder is the stand-in of Secret in replay fixtures: the same length
// and no secret shape gitleaks matches.
const Placeholder = "placeholder-not-key0"

// AssertAbsent fails when data holds the secret or its body (the part
// after the fixed prefix) in any common encoding.
func AssertAbsent(t testing.TB, secret, label string, data []byte) {
	t.Helper()
	if secret == "" {
		return
	}
	body := secret[4:]
	for _, needle := range []string{secret, body, strings.ToLower(body), hex.EncodeToString([]byte(secret))} {
		if bytes.Contains(data, []byte(needle)) {
			t.Fatalf("%s holds the generated secret", label)
		}
	}
}

// AssertTreeAbsent checks every regular file below root.
func AssertTreeAbsent(t testing.TB, secret, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			AssertAbsent(t, secret, "scratch file "+filepath.ToSlash(strings.TrimPrefix(p, root)), b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// AssertResultAbsent checks the streams the executor captured, the recorded
// commands and the task limitations and reason.
func AssertResultAbsent(t testing.TB, secret string, res analysis.TaskResult) {
	t.Helper()
	for _, e := range res.Executions {
		AssertAbsent(t, secret, e.Name+" stdout", e.Result.Stdout.Data)
		AssertAbsent(t, secret, e.Name+" stderr", e.Result.Stderr.Data)
		AssertAbsent(t, secret, e.Name+" argv", []byte(strings.Join(e.Result.Command.Argv, "\x00")))
	}
	AssertAbsent(t, secret, "task reason", []byte(res.Reason+strings.Join(res.Limitations, ",")))
}
