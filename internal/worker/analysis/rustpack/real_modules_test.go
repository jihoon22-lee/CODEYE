package rustpack

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
)

// A registry dependency served by a vendor directory (source replacement in
// the repository's own .cargo/config.toml) resolves offline with the scratch
// CARGO_HOME: the one route to registry dependencies (docs/ANALYZERS.md#오프라인-의존성). cargo
// still writes its lock and cache files into CARGO_HOME while offline, which
// is why a read-only operator cache cannot be given to it.
func TestRealVendoredDependencies(t *testing.T) {
	res, doc, _, fx, _ := realRun(t, KindCheck, "vendored", "cargo-check", "check-vendored", domain.Scope{}, nil)
	if res.Status != domain.TaskSucceeded || doc.Outcome.ExitClass != obsdoc.ExitSuccessWithoutFindings || !slices.Equal(doc.Coverage.ProcessedPaths, []string{"src/lib.rs"}) || *doc.Coverage.FilesInScope != 1 {
		t.Fatalf("status %s/%s %+v coverage %+v", res.Status, res.Reason, doc.Outcome, doc.Coverage)
	}
	if got := doc.RulePack.ConfigFiles; len(got) != 1 || got[0].Path != ".cargo/config.toml" {
		t.Fatalf("config files %+v", got)
	}
	home := filepath.Join(fx.Job.Scratch, "analyzers", "cargo-check", "cargo-home")
	if _, err := os.Stat(filepath.Join(home, ".package-cache")); err != nil {
		t.Fatalf("cargo wrote nothing into its home while offline: %v", err)
	}
}

// Modules other than "." (two sibling crates, no root manifest): spans,
// dep-info and the Cargo configuration of the module and of the snapshot
// root are mapped relative to the module directory.
func TestRealSiblingModules(t *testing.T) {
	for _, tc := range []struct {
		kind      Kind
		key, capN string
		dir       string
		rule      string
		path      string
		processed []string
		config    []string
	}{
		{KindCheck, "cargo-check.m01", "check-siblings-m01", "a", "unused_variables", "a/src/lib.rs", []string{"a/src/lib.rs"}, []string{".cargo/config.toml", "a/.cargo/config.toml"}},
		{KindCheck, "cargo-check.m02", "check-siblings-m02", "b", "unused_variables", "b/src/lib.rs", []string{"b/src/lib.rs"}, []string{".cargo/config.toml"}},
		{KindClippy, "clippy.m01", "clippy-siblings-m01", "a", "clippy::ptr_arg", "a/src/lib.rs", []string{"a/src/lib.rs"}, []string{".cargo/config.toml", "a/.cargo/config.toml"}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			res, doc, _, _, _ := realRun(t, tc.kind, "siblings", tc.key, tc.capN, domain.Scope{}, nil)
			if res.Status != domain.TaskSucceeded || doc.Module.Dir != tc.dir || doc.TaskKey != tc.key {
				t.Fatalf("status %s/%s module %q", res.Status, res.Reason, doc.Module.Dir)
			}
			o := obsOf(doc, tc.rule)
			if len(o) == 0 || *o[0].Path != tc.path || o[0].LocationState != obsdoc.LocationResolved {
				t.Fatalf("observations %+v", doc.Observations)
			}
			for _, x := range doc.Observations {
				if !strings.HasPrefix(*x.Path, tc.dir+"/") {
					t.Fatalf("observation outside the module: %+v", x)
				}
			}
			if !slices.Equal(doc.Coverage.ProcessedPaths, tc.processed) || *doc.Coverage.FilesInScope != 1 {
				t.Fatalf("coverage %+v", doc.Coverage)
			}
			var cfg []string
			for _, f := range doc.RulePack.ConfigFiles {
				cfg = append(cfg, f.Path)
			}
			if tc.kind == KindClippy {
				cfg = slices.DeleteFunc(cfg, func(p string) bool { return strings.HasSuffix(p, "clippy.toml") })
			}
			if !slices.Equal(cfg, tc.config) {
				t.Fatalf("config files %v, want %v", cfg, tc.config)
			}
			if tc.key == "cargo-check.m01" {
				// The byte column after the Korean text on the module's line.
				for _, x := range obsOf(doc, "unused_variables") {
					if strings.Contains(x.RawMessage, "tail_a") && (x.Location.StartCol == 0 || x.Location.ColumnUnit != "byte") {
						t.Fatalf("tail_a %+v", x.Location)
					}
				}
			}
		})
	}
}

// docs/ANALYZERS.md#실행-규칙: no detached child may write after the cleanup was confirmed.
// The scratch tree is identical right after the tasks and half a second
// later, for cargo check, clippy and a run with a build script and proc macro.
func TestRealNoWritesAfterCleanup(t *testing.T) {
	testClippy(t)
	cases := []struct {
		kind    Kind
		fixture string
		key     string
	}{{KindCheck, "findings", "cargo-check"}, {KindClippy, "findings", "clippy"}, {KindCheck, "buildscript", "cargo-check"}}
	for _, tc := range cases {
		if tc.fixture == "buildscript" && FindLinker(testToolPath()) == "" {
			continue
		}
		_, _, _, fx, _ := realRun(t, tc.kind, tc.fixture, tc.key, "", domain.Scope{}, nil)
		before := treeHash(t, fx.Job.Scratch)
		time.Sleep(500 * time.Millisecond)
		if after := treeHash(t, fx.Job.Scratch); after != before {
			t.Fatalf("%s %s: the scratch changed after the task ended", tc.fixture, tc.key)
		}
		if entries, _ := os.ReadDir(filepath.Join(fx.Job.Scratch, "analyzers", tc.key, "tmp")); len(entries) != 0 {
			t.Fatalf("%s %s: executor temp not cleaned: %v", tc.fixture, tc.key, entries)
		}
	}
}
