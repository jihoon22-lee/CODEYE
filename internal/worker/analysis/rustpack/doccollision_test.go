package rustpack

import (
	"slices"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

func docResult(code int, stderr string) *executor.Result {
	return &executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Stderr: executor.Stream{Data: []byte(stderr), TotalBytes: int64(len(stderr))}}
}

// TC-327: the deeper package of each colliding pair is left out; anything
// else in stderr, another exit code or an unreadable pair is no retry.
func TestDocCollisionExcludes(t *testing.T) {
	pair := "error: document output filename collision\n" +
		"The lib `dup` in package `dup-ffi v0.1.0 ([REDACTED]/crates/ffi)` has the same name as the lib `dup` in package `dup v0.1.0 ([REDACTED])`.\n" +
		"Only one may be documented at once since they output to the same path.\n"
	for _, c := range []struct {
		name string
		r    *executor.Result
		want []string
	}{
		{"member under the root", docResult(101, pair), []string{"dup-ffi"}},
		{"root named second", docResult(101, "error: document output filename collision\nThe lib `dup` in package `dup v0.1.0 ([REDACTED])` has the same name as the lib `dup` in package `dup-ffi v0.1.0 ([REDACTED]/crates/ffi)`.\n"), []string{"dup-ffi"}},
		{"two siblings", docResult(101, "error: document output filename collision\nThe lib `x` in package `x-b v1.0.0 ([REDACTED]/b)` has the same name as the lib `x` in package `x-a v1.0.0 ([REDACTED]/a)`.\n"), []string{"x-b"}},
		{"exit 0", docResult(0, pair), nil},
		{"also a build script failure", docResult(101, pair+"error: failed to run custom build command for `y v0.1.0`\n"), nil},
		{"no pair", docResult(101, "error: document output filename collision\n"), nil},
		{"no collision", docResult(101, "error: could not compile `dup`\n"), nil},
	} {
		if got := DocCollisionExcludes(c.r); !slices.Equal(got, c.want) {
			t.Fatalf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if args := DocRetryArgs([]string{"dup-ffi"}); !slices.Equal(args[len(args)-2:], []string{"--exclude", "dup-ffi"}) || !slices.Contains(args, "--workspace") {
		t.Fatalf("retry args %v", args)
	}
}

// TC-327 with the real toolchain: a workspace whose member documents a
// library of the root package's name is documented without that member.
func TestRealRustdocCollidingLibraryNames(t *testing.T) {
	files := map[string]string{
		"Cargo.toml":            "[package]\nname=\"dup\"\nversion=\"0.1.0\"\nedition=\"2021\"\n\n[workspace]\nmembers=[\"crates/ffi\"]\n",
		"Cargo.lock":            "version = 3\n\n[[package]]\nname = \"dup\"\nversion = \"0.1.0\"\n\n[[package]]\nname = \"dup-ffi\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":            "//! See [`Missing`].\npub fn f() {}\n",
		"crates/ffi/Cargo.toml": "[package]\nname=\"dup-ffi\"\nversion=\"0.1.0\"\nedition=\"2021\"\n\n[lib]\nname=\"dup\"\npath=\"lib.rs\"\n",
		"crates/ffi/lib.rs":     "pub fn g() {}\n",
	}
	r, d, raw, _, _ := realRun(t, KindDoc, "", "cargo-doc", "", domain.Scope{Kind: "full"}, files)
	if r.Status != domain.TaskSucceeded || len(obsOf(d, "rustdoc::broken_intra_doc_links")) != 1 {
		t.Fatalf("task %v reason %s\n%s", r.Status, r.Reason, raw)
	}
	if !slices.Contains(d.Limitations, LimDocPackagesExcluded) || d.Target.Properties["excluded_packages"] != "dup-ffi" ||
		!slices.Equal(execNames(d), []string{execRustc, execCargo, execDoc, execDoc + ".packages"}) || !slices.Equal(d.Coverage.ProcessedPaths, []string{"src/lib.rs"}) {
		t.Fatalf("limitations %v properties %v executions %v processed %v", d.Limitations, d.Target.Properties, execNames(d), d.Coverage.ProcessedPaths)
	}
}
