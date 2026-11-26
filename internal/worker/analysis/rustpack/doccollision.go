package rustpack

import (
	"regexp"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// LimDocPackagesExcluded: cargo doc refused the workspace because two
// packages document a library of the same name; the result is the run
// without the deeper package of each pair (decision A71).
const LimDocPackagesExcluded = "doc_colliding_packages_excluded"

// docCollision is cargo's explanation of a document output filename
// collision (cargo 1.99): "The lib `x` in package `a v1 (path)` has the same
// name as the lib `x` in package `b v2 (path)`."
var docCollision = regexp.MustCompile("in package `([A-Za-z0-9_-]+) v[^ `]+ \\(([^)`]*)\\)` has the same name as the [a-z]+ `[^`]+` in package `([A-Za-z0-9_-]+) v[^ `]+ \\(([^)`]*)\\)`")

// DocCollisionExcludes returns the packages to leave out of a cargo doc run
// that failed (exit 101, complete streams) only because of output filename
// collisions: of each colliding pair the package whose directory is deeper
// (the workspace root's package stays), by name on a tie. nil when the run
// failed for anything else or no pair could be read.
func DocCollisionExcludes(r *executor.Result) []string {
	if r == nil || r.Status != executor.StatusExited || r.ExitCode == nil || *r.ExitCode != 101 || !pack.StreamsComplete(r) {
		return nil
	}
	f := ClassifyStderr(r.Stderr.Data)
	if !f.DocCollision || f.Toolchain || f.LockMissing || f.LockOutdated || f.Dependency || f.Linker || f.ManifestInvalid || f.BuildScript || f.CompileError {
		return nil
	}
	depth := func(p string) int {
		p = strings.TrimSuffix(strings.TrimPrefix(p, "[REDACTED]"), "/")
		return strings.Count(p, "/")
	}
	excluded := map[string]bool{}
	for _, m := range docCollision.FindAllStringSubmatch(string(r.Stderr.Data), -1) {
		a, ap, b, bp := m[1], m[2], m[3], m[4]
		if a == b {
			return nil
		}
		drop := a
		if depth(bp) > depth(ap) || (depth(bp) == depth(ap) && b > a) {
			drop = b
		}
		excluded[drop] = true
	}
	out := make([]string, 0, len(excluded))
	for p := range excluded {
		out = append(out, p)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// DocRetryArgs is the cargo doc argv without the excluded packages.
func DocRetryArgs(excluded []string) []string {
	args := DocArgs()
	for _, p := range excluded {
		args = append(args, "--exclude", p)
	}
	return args
}
