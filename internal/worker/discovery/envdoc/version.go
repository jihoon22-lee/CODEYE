package envdoc

import (
	"go/version"
	"regexp"
	"strconv"
	"strings"
)

// Version comparison is deliberately narrow. Go declarations use the go
// command's own ordering (go/version: 1.22 < 1.22rc1 < 1.22.0). Other tools
// support only ">=X[.Y[.Z]]" and bare numeric versions whose meaning depends
// on the declaration kind; every other range expression is "unsupported" and
// yields an unknown verdict instead of a guess.

var (
	goLang  = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,2}((rc|beta)[0-9]+)?$`)
	numeric = regexp.MustCompile(`^[0-9]+(\.[0-9]+){0,3}$`)
)

func isGoKind(kind string) bool { return ToolOf[kind] == "go" }

// goBound returns the go/version form of a Go declaration.
func goBound(kind, c string) (string, bool) {
	switch kind {
	case "go_directive", "go_work_directive":
		if goLang.MatchString(c) && version.IsValid("go"+c) {
			return "go" + c, true
		}
	case "go_toolchain", "go_work_toolchain":
		// "go1.30.0" or "go1.21.3-suffix"; "default", "+auto" forms and
		// anything go/version rejects are not compared.
		if strings.HasPrefix(c, "go") && !strings.ContainsAny(c, "+ ") && version.IsValid(c) {
			return c, true
		}
	}
	return "", false
}

func parts(v string, max int) ([]int, bool) {
	if !numeric.MatchString(v) {
		return nil, false
	}
	fields := strings.Split(v, ".")
	if len(fields) > max {
		return nil, false
	}
	out := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n > 1<<20 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// bound classifies a non-Go constraint into a comparison and its numeric bound.
func bound(kind, c string) (string, []int) {
	c = strings.TrimSpace(c)
	switch kind {
	case "engines_node":
		if rest, ok := strings.CutPrefix(c, ">="); ok {
			if p, ok := parts(strings.TrimSpace(rest), 3); ok {
				return CompareMinimum, p
			}
			return CompareUnsupported, nil
		}
		if p, ok := parts(strings.TrimPrefix(c, "v"), 3); ok {
			return ComparePrefix, p
		}
	case "requires_python", "python_requires":
		if rest, ok := strings.CutPrefix(c, ">="); ok {
			if p, ok := parts(strings.TrimSpace(rest), 3); ok {
				return CompareMinimum, p
			}
		}
	case "python_version_file", "rust_toolchain_channel":
		if p, ok := parts(c, 3); ok {
			return ComparePrefix, p
		}
	case "rust_version":
		if p, ok := parts(c, 3); ok {
			return CompareMinimum, p
		}
	case "cmake_minimum_required":
		// "3.20" or "3.10...3.28" (minimum...policy_max): the minimum gates.
		lo, _, ranged := strings.Cut(c, "...")
		if p, ok := parts(lo, 4); ok && (!ranged || numeric.MatchString(c[len(lo)+3:])) {
			return CompareMinimum, p
		}
	}
	return CompareUnsupported, nil
}

// Classify returns the comparison a declaration supports.
func Classify(kind, constraint string) string {
	if isGoKind(kind) {
		if _, ok := goBound(kind, constraint); !ok {
			return CompareUnsupported
		}
		if kind == "go_toolchain" || kind == "go_work_toolchain" {
			return ComparePreferredMinimum
		}
		return CompareMinimum
	}
	cmp, _ := bound(kind, constraint)
	return cmp
}

// ComparableObservation reports whether an observed tool version can be
// compared: Go versions in go/version form without the "go" prefix, other
// tools as 1-4 numeric components.
func ComparableObservation(tool, v string) bool {
	if tool == "go" {
		return goLang.MatchString(v) && version.IsValid("go"+v)
	}
	_, ok := parts(v, 4)
	return ok
}

// Decide compares a supported declaration with a comparable observation and
// returns satisfied or mismatch; otherwise unknown.
func Decide(kind, constraint, observed string) string {
	if !ComparableObservation(ToolOf[kind], observed) {
		return VerdictUnknown
	}
	if isGoKind(kind) {
		b, ok := goBound(kind, constraint)
		if !ok {
			return VerdictUnknown
		}
		if version.Compare("go"+observed, b) >= 0 {
			return VerdictSatisfied
		}
		return VerdictMismatch
	}
	cmp, want := bound(kind, constraint)
	got, _ := parts(observed, 4)
	switch cmp {
	case CompareMinimum:
		for i := 0; i < max(len(want), len(got)); i++ {
			a, b := at(got, i), at(want, i)
			if a != b {
				if a > b {
					return VerdictSatisfied
				}
				return VerdictMismatch
			}
		}
		return VerdictSatisfied
	case ComparePrefix:
		if len(got) < len(want) {
			return VerdictMismatch
		}
		for i := range want {
			if got[i] != want[i] {
				return VerdictMismatch
			}
		}
		return VerdictSatisfied
	}
	return VerdictUnknown
}

func at(p []int, i int) int {
	if i < len(p) {
		return p[i]
	}
	return 0
}
