//go:build linux

package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/progress"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/workerproto"
)

// rustFixture reads fixtures/rust/<name> into snapshot files.
func rustFixture(t *testing.T, name string) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..", "fixtures", "rust", name)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil || len(out) == 0 {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return out
}

// runRustPack runs the production deep-review composition (discovery → the
// analyzer composite → review roles) through the real worker on a Rust
// snapshot. goExe is configured because go vet is the only other baseline
// analyzer; the snapshot has no Go, so go vet plans nothing. allow is the
// node operator's repository-code grant for the job's source.
func runRustPack(t *testing.T, files map[string]string, tools map[string]string, allow bool) packRun {
	t.Helper()
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goExe); err != nil {
		t.Skip("go toolchain not found at GOROOT/bin")
	}
	e := newEnv(t)
	if err := os.Remove(filepath.Join(e.src, "main.go")); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(e.src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := builtin.Config{GoExecutable: goExe, Tools: tools, ProbeDir: t.TempDir(), ToolPath: "/usr/bin:/bin"}
	c, _, err := builtin.Composite(context.Background(), cfg, &executor.Executor{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := discovery.Wrap(review.Handler{Static: c}, discovery.Config{Observer: discovery.ProbeObserver{GoExecutable: goExe, Analyzers: builtin.Probes(cfg)}})
	if err != nil {
		t.Fatal(err)
	}
	fa, in, done, _ := startWorker(t, h)
	defer func() {
		in.Close()
		if code := <-done; code != 0 {
			t.Errorf("worker exit %d", code)
		}
	}()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-fa.started:
			case <-stop:
				return
			}
		}
	}()
	fa.hello(e)
	job := localJob(e)
	job.AllowRepositoryCode = allow
	if err := fa.conn.Send(job); err != nil {
		t.Fatal(err)
	}
	var out packRun
	select {
	case out.complete = <-fa.complete:
	case <-time.After(180 * time.Second):
		t.Fatalf("no complete; messages %v", fa.kinds())
	}
	if out.complete.ResultManifestClientArtifact == nil || out.complete.CleanupState != "confirmed" {
		t.Fatalf("completion: %+v", out.complete)
	}
	fa.mu.Lock()
	defer fa.mu.Unlock()
	if err := json.Unmarshal(fa.artifacts[*out.complete.ResultManifestClientArtifact], &out.manifest); err != nil {
		t.Fatal(err)
	}
	out.stages = map[string]domain.Stage{}
	for _, m := range fa.msgs {
		if v, ok := m.(*workerproto.Event); ok && v.Kind == nodeproto.EventTaskCreated {
			var p struct {
				Key   string       `json:"key"`
				Stage domain.Stage `json:"stage"`
			}
			if err := json.Unmarshal(v.Payload, &p); err != nil {
				t.Fatal(err)
			}
			out.stages[p.Key] = p.Stage
		}
	}
	if out.report, err = report.Build(reportInput(t, fa, out.manifest, *out.complete.ResultManifestClientArtifact), report.DefaultRegistry()); err != nil {
		t.Fatal(err)
	}
	if err := report.ValidateReport(out.report); err != nil {
		t.Fatal(err)
	}
	for id, body := range fa.artifacts {
		if fa.roles[id] == "source_manifest" {
			if out.source, err = source.DecodeManifest(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	index := map[string]string{}
	for _, en := range out.source.Entries {
		if en.Kind == "file" && en.ExcludedReason == "" {
			index[en.Path] = en.SHA256
		}
	}
	lookup := func(p string) (string, bool) { s, ok := index[p]; return s, ok }
	out.docs = map[string]obsdoc.Document{}
	for _, task := range out.manifest.Tasks {
		for _, a := range task.Artifacts {
			body := fa.artifacts[clientID(fa, a.ArtifactID)]
			switch {
			case a.Role == analysis.RoleObservations:
				d, err := obsdoc.Decode(body, obsdoc.Expect{TaskKey: task.Key, Snapshot: out.manifest.Snapshot.ManifestDigest, Lookup: lookup})
				if err != nil {
					t.Fatalf("%s envelope: %v", task.Key, err)
				}
				out.docs[task.Key] = d
			case a.MediaType == envdoc.MediaType:
				var d envdoc.Document
				if json.Unmarshal(body, &d) == nil {
					out.envs = append(out.envs, d)
				}
			}
		}
	}
	return out
}

func taskLimitations(m analysis.ResultManifest, key string) []string {
	for _, task := range m.Tasks {
		if task.Key == key {
			return task.Limitations
		}
	}
	return nil
}

// fakeRustToolchain is a toolchain directory whose cargo, rustc and
// cargo-clippy record every invocation in log and answer version commands.
// Only the version probes may ever call them without a repository-code grant.
func fakeRustToolchain(t *testing.T, log string) (cargo, clippy string) {
	t.Helper()
	dir := t.TempDir()
	script := func(name, version string) {
		body := "#!/bin/sh\necho \"" + name + " $*\" >> '" + log + "'\ncase \"$1\" in\n--version) echo '" + version + "';;\n-vV) printf 'rustc 1.99.1 (58cf8f9c3 2026-10-13)\\nhost: x86_64-unknown-linux-gnu\\nrelease: 1.99.1\\n';;\nesac\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("cargo", "cargo 1.99.1 (5f94df478 2026-08-27)")
	script("rustc", "")
	script("cargo-clippy", "clippy 0.1.99 (58cf8f9c35 2026-10-13)")
	script("clippy-driver", "")
	return filepath.Join(dir, "cargo"), filepath.Join(dir, "cargo-clippy")
}

// TC-103/115/116 (docs/ANALYZERS.md#rust-환경): a node without the repository-code grant never
// runs cargo. The Rust baseline is unsupported / repository_code_not_allowed
// whether or not the tool is configured; the static_analysis stage and the
// report show it; the tool reason stays a limitation. With cargo configured, the
// only invocations are the version probes.
func TestRustPackWithoutRepositoryCodeGrant(t *testing.T) {
	files := rustFixture(t, "findings")
	log := filepath.Join(t.TempDir(), "calls.log")
	cargo, clippy := fakeRustToolchain(t, log)
	for _, tc := range []struct {
		name  string
		tools map[string]string
		also  string // the tool reason kept as a limitation
	}{
		{"tools not configured", nil, "tool_not_configured"},
		{"tools configured", map[string]string{"cargo": cargo, "clippy": clippy}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(log)
			r := runRustPack(t, files, tc.tools, false)
			rows := taskRows(r.manifest)
			if rows["cargo-check"] != "unsupported/repository_code_not_allowed" || rows["clippy"] != "unsupported/repository_code_not_allowed" {
				t.Fatalf("tasks %v", rows)
			}
			if _, ok := rows["go-vet"]; ok {
				t.Fatalf("go vet planned for a Rust-only snapshot: %v", rows)
			}
			for _, k := range []string{"cargo-check", "clippy"} {
				d := r.docs[k]
				if d.Outcome.ExitClass != obsdoc.ExitNotRun || len(d.Executions) != 0 || len(d.Observations) != 0 || d.Coverage.ProcessedPaths == nil || len(d.Coverage.ProcessedPaths) != 0 {
					t.Fatalf("%s envelope %+v", k, d)
				}
				if tc.also != "" && !slices.Contains(taskLimitations(r.manifest, k), tc.also) {
					t.Fatalf("%s limitations %v, want %s kept", k, taskLimitations(r.manifest, k), tc.also)
				}
				for _, task := range r.manifest.Tasks {
					if task.Key == k && (len(task.Executions) != 0 || task.Required != (k == "cargo-check")) {
						t.Fatalf("%s manifest task %+v", k, task)
					}
				}
			}
			calls, _ := os.ReadFile(log)
			for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
				if line != "" && !strings.HasSuffix(line, " --version") && !strings.HasSuffix(line, " -vV") {
					t.Fatalf("a tool ran something other than a version probe without the grant:\n%s", calls)
				}
			}
			// The stage and the report show the missing grant; nothing is a clean pass.
			var tasks []progress.Task
			for _, task := range r.manifest.Tasks {
				reason := ""
				if task.Reason != nil {
					reason = *task.Reason
				}
				tasks = append(tasks, progress.Task{Stage: r.stages[task.Key], Status: task.Status, Reason: reason})
			}
			view := progress.Compute(progress.Run{Status: domain.RunPartial, SnapshotFixed: true, Tasks: tasks})
			var stage progress.Stage
			for _, s := range view.Stages {
				if s.Key == string(domain.StageStaticAnalysis) {
					stage = s
				}
			}
			if stage.State == progress.Succeeded || !slices.Contains(stage.Reasons, "repository_code_not_allowed") {
				t.Fatalf("static_analysis stage %+v", stage)
			}
			rep := r.report
			if rep.Status == report.StatusCompleted || rep.QualityVerdict == report.VerdictNoActionable || len(rep.Findings) != 0 {
				t.Fatalf("report %s/%s with %d findings", rep.Status, rep.QualityVerdict, len(rep.Findings))
			}
			if !slices.Contains(rep.Limitations, report.LimRequiredTaskIncomplete) {
				t.Fatalf("limitations %v", rep.Limitations)
			}
			for _, row := range rep.Coverage.Tools {
				if strings.HasPrefix(row.Analyzer, "rust.") && (row.ExitClass != "not_run" || row.ProcessedFiles == nil || *row.ProcessedFiles != 0) {
					t.Fatalf("tool row %+v", row)
				}
			}
			t.Logf("report %s/%s limitations %v", rep.Status, rep.QualityVerdict, rep.Limitations)
		})
	}
}

// The real end to end (CODEYE_TEST_CARGO, CODEYE_TEST_CLIPPY): with the
// grant, cargo check and clippy run on the same workspace copy under the
// production composition; the report carries their findings as unreviewed tool
// findings and records that repository code ran.
func TestRustPackWithRealToolchain(t *testing.T) {
	cargo, clippy := os.Getenv("CODEYE_TEST_CARGO"), os.Getenv("CODEYE_TEST_CLIPPY")
	if cargo == "" || clippy == "" {
		t.Skip("set CODEYE_TEST_CARGO and CODEYE_TEST_CLIPPY to the toolchain's cargo and cargo-clippy")
	}
	r := runRustPack(t, rustFixture(t, "findings"), map[string]string{"cargo": cargo, "clippy": clippy}, true)
	rows := taskRows(r.manifest)
	if rows["cargo-check"] != "succeeded/" || rows["clippy"] != "succeeded/" {
		t.Fatalf("tasks %v", rows)
	}
	var names []string
	for _, task := range r.manifest.Tasks {
		if task.Key != "cargo-check" {
			continue
		}
		for _, e := range task.Executions {
			names = append(names, e.Name)
			if e.Name == "cargo.check" && (e.ExitCode == nil || *e.ExitCode != 0 || e.StdoutArtifactID == nil) {
				t.Fatalf("cargo.check %+v", e)
			}
		}
		if !slices.Contains(task.Limitations, "repository_code_executed") || !task.Required {
			t.Fatalf("cargo-check task %+v", task)
		}
	}
	if !slices.Equal(names, []string{"rustc.version", "cargo.version", "cargo.check"}) {
		t.Fatalf("executions %v", names)
	}
	check, lint := r.docs["cargo-check"], r.docs["clippy"]
	if check.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || lint.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings || !slices.Equal(check.Coverage.ProcessedPaths, []string{"src/lib.rs", "src/main.rs", "tests/it.rs"}) {
		t.Fatalf("envelopes %+v %+v", check.Outcome, lint.Outcome)
	}
	at := analyzerTools(r.envs)
	seen := map[string]bool{}
	for _, a := range at {
		seen[a.Tool] = a.Status == envdoc.ObsObserved && a.Version != nil
	}
	if !seen["cargo"] || !seen["clippy"] {
		t.Fatalf("analyzer tools %+v", at)
	}
	rep := r.report
	tools := map[string]bool{}
	for _, row := range rep.Coverage.Tools {
		tools[row.Analyzer] = row.ProcessedFiles != nil && *row.ProcessedFiles == 3
	}
	if !tools["rust.cargocheck"] || len(rep.Findings) == 0 {
		t.Fatalf("report tools %v findings %d", tools, len(rep.Findings))
	}
	for _, f := range rep.Findings {
		if f.Validation != "unreviewed" {
			t.Fatalf("finding %+v is not an unreviewed tool finding", f)
		}
	}
}
