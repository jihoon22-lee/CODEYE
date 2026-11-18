package rustpack

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Modules other than "." on captured bytes: the key, module directory, span
// mapping, dep-info mapping and the Cargo configuration of the module and of
// the snapshot root.
func TestReplaySiblingModules(t *testing.T) {
	for _, tc := range []struct {
		kind      Kind
		key, capN string
		dir       string
		rule      string
		processed string
		config    []string
	}{
		{KindCheck, "cargo-check.m01", "check-siblings-m01", "a", "unused_variables", "a/src/lib.rs", []string{".cargo/config.toml", "a/.cargo/config.toml"}},
		{KindCheck, "cargo-check.m02", "check-siblings-m02", "b", "unused_variables", "b/src/lib.rs", []string{".cargo/config.toml"}},
		{KindClippy, "clippy.m01", "clippy-siblings-m01", "a", "clippy::ptr_arg", "a/src/lib.rs", []string{".cargo/config.toml", "a/.cargo/config.toml"}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			a := replayAnalyzer(t, tc.kind)
			fx := newFixture(t, "", fixtureFiles(t, "siblings"), domain.Scope{}, true)
			r := newReplay(t, tc.capN)
			res := runTask(t, a, fx.Job, tc.key, r)
			doc, _ := readEnvelope(t, fx.Job, res)
			if res.Status != domain.TaskSucceeded || doc.Module.Dir != tc.dir {
				t.Fatalf("status %s/%s module %q errors %+v", res.Status, res.Reason, doc.Module.Dir, doc.ToolErrors)
			}
			if o := obsOf(doc, tc.rule); len(o) == 0 || *o[0].Path != tc.dir+"/src/lib.rs" || o[0].LocationState != obsdoc.LocationResolved {
				t.Fatalf("observations %+v", doc.Observations)
			}
			if !slices.Equal(doc.Coverage.ProcessedPaths, []string{tc.processed}) || *doc.Coverage.FilesInScope != 1 || doc.Coverage.FilesUnlisted != 0 {
				t.Fatalf("coverage %+v", doc.Coverage)
			}
			var cfg []string
			for _, f := range doc.RulePack.ConfigFiles {
				if !strings.HasSuffix(f.Path, "clippy.toml") {
					cfg = append(cfg, f.Path)
				}
			}
			if !slices.Equal(cfg, tc.config) {
				t.Fatalf("config files %v, want %v", cfg, tc.config)
			}
			// The process runs in the module directory of the workspace copy.
			if m := r.mains[0]; !strings.HasSuffix(m.Dir, "/"+tc.dir) {
				t.Fatalf("cwd %s", m.Dir)
			}
			if tc.key == "cargo-check.m01" {
				for _, x := range obsOf(doc, "unused_variables") {
					if strings.Contains(x.RawMessage, "tail_a") {
						line := strings.Split(fixtureFiles(t, "siblings")["a/src/lib.rs"], "\n")[x.Location.StartLine-1]
						if want := strings.Index(line, "tail_a") + 1; x.Location.StartCol != want {
							t.Fatalf("tail_a column %d, want %d", x.Location.StartCol, want)
						}
					}
				}
			}
		})
	}
}

// stderr is evidence only of a failed run and only through cargo's own
// unindented error lines. A build script can print anything: a clean run
// stays clean whatever it prints, and a failed build is not turned into a
// configuration error by text that only quotes one.
func TestReplayStderrIsNotEvidenceOfAnythingButAFailure(t *testing.T) {
	quote := "warning: app@0.1.0: no matching package named `x` found\nwarning: app@0.1.0: failed to download linker `cc` not found --> Cargo.toml:1:1\n" +
		"  error: no matching package named `serde` found\n  error: cannot create the lock file Cargo.lock because --locked was passed\n"
	// Exit 0 with a successful build-finished: even unindented error text is ignored.
	x := replayCase(t, KindCheck, "findings", "check-findings", func(r *executor.Result) {
		r.Stderr.Data = append([]byte(quote+"error: no matching package named `serde` found\n"), r.Stderr.Data...)
		r.Stderr.TotalBytes = int64(len(r.Stderr.Data))
	})
	if x.res.Status != domain.TaskSucceeded || x.doc.Outcome.ExitClass != obsdoc.ExitSuccessWithFindings {
		t.Fatalf("a quoted error flipped a clean run: %s/%s", x.res.Status, x.res.Reason)
	}
	// Exit 101 from a compile error: quoted (indented or warning) text does not
	// outrank the compile error in the JSON stream.
	x = replayCase(t, KindCheck, "typeerr", "check-typeerr", func(r *executor.Result) {
		r.Stderr.Data = append([]byte(quote), r.Stderr.Data...)
		r.Stderr.TotalBytes = int64(len(r.Stderr.Data))
	})
	if x.res.Status != domain.TaskFailed || x.res.Reason != ReasonPackageErrors || x.doc.Outcome.ExitClass != obsdoc.ExitExecutionError {
		t.Fatalf("quoted text changed the failure: %s/%s %+v", x.res.Status, x.res.Reason, x.doc.Outcome)
	}
	if f := ClassifyStderr([]byte(quote)); f.Dependency || f.LockMissing || f.Linker || f.ManifestInvalid {
		t.Fatalf("quoted text classified: %+v", f)
	}
}
