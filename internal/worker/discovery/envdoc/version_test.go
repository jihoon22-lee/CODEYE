package envdoc

import "testing"

func TestClassifyAndDecide(t *testing.T) {
	cases := []struct {
		kind, constraint, comparison, observed, verdict string
	}{
		// Go: the go command's ordering, GOTOOLCHAIN=local.
		{"go_directive", "1.22", CompareMinimum, "1.27.4", VerdictSatisfied},
		{"go_directive", "1.22.3", CompareMinimum, "1.27.4", VerdictSatisfied},
		{"go_directive", "1.27.4", CompareMinimum, "1.27.4", VerdictSatisfied},
		{"go_directive", "1.27.5", CompareMinimum, "1.27.4", VerdictMismatch},
		{"go_directive", "1.30", CompareMinimum, "1.27.4", VerdictMismatch},
		{"go_directive", "1.22", CompareMinimum, "1.22rc1", VerdictSatisfied}, // 1.22 < 1.22rc1 < 1.22.0
		{"go_directive", "1.22.0", CompareMinimum, "1.22rc1", VerdictMismatch},
		{"go_work_directive", "1.21", CompareMinimum, "1.20.14", VerdictMismatch},
		{"go_directive", "1.22.x", CompareUnsupported, "1.27.4", VerdictUnknown},
		{"go_directive", "go1.22", CompareUnsupported, "1.27.4", VerdictUnknown},
		{"go_toolchain", "go1.30.0", ComparePreferredMinimum, "1.27.4", VerdictMismatch},
		{"go_toolchain", "go1.21.0", ComparePreferredMinimum, "1.27.4", VerdictSatisfied},
		{"go_toolchain", "go1.21.3-custom", ComparePreferredMinimum, "1.27.4", VerdictSatisfied},
		{"go_toolchain", "default", CompareUnsupported, "1.27.4", VerdictUnknown},
		{"go_toolchain", "go1.21.0+auto", CompareUnsupported, "1.27.4", VerdictUnknown},
		{"go_toolchain", "1.21.0", CompareUnsupported, "1.27.4", VerdictUnknown},
		// Node engines: ">=" minimum, bare version = same release line.
		{"engines_node", ">=18", CompareMinimum, "18.17.0", VerdictSatisfied},
		{"engines_node", ">= 18.18.1", CompareMinimum, "18.17.0", VerdictMismatch},
		{"engines_node", "18", ComparePrefix, "18.1.0", VerdictSatisfied},
		{"engines_node", "v18.2", ComparePrefix, "18.3.0", VerdictMismatch},
		{"engines_node", "18", ComparePrefix, "20.0.0", VerdictMismatch},
		{"engines_node", "^18", CompareUnsupported, "18.1.0", VerdictUnknown},
		{"engines_node", ">=18 <21", CompareUnsupported, "18.1.0", VerdictUnknown},
		{"engines_node", "18.x", CompareUnsupported, "18.1.0", VerdictUnknown},
		// Python.
		{"requires_python", ">=3.10", CompareMinimum, "3.11.4", VerdictSatisfied},
		{"python_requires", ">=3.10", CompareMinimum, "3.9.18", VerdictMismatch},
		{"requires_python", "~=3.10", CompareUnsupported, "3.11.4", VerdictUnknown},
		{"requires_python", "3.10", CompareUnsupported, "3.10.1", VerdictUnknown},
		{"requires_python", ">=3.8,<4", CompareUnsupported, "3.10.1", VerdictUnknown},
		{"python_version_file", "3.11", ComparePrefix, "3.11.4", VerdictSatisfied},
		{"python_version_file", "3.11.4", ComparePrefix, "3.11.5", VerdictMismatch},
		{"python_version_file", "pypy3.9-7.3.9", CompareUnsupported, "3.9.0", VerdictUnknown},
		// Rust.
		{"rust_version", "1.70", CompareMinimum, "1.75.0", VerdictSatisfied},
		{"rust_version", "1.70", CompareMinimum, "1.69.0", VerdictMismatch},
		{"rust_version", "workspace", CompareUnsupported, "1.75.0", VerdictUnknown},
		{"rust_toolchain_channel", "1.75", ComparePrefix, "1.75.0", VerdictSatisfied},
		{"rust_toolchain_channel", "stable", CompareUnsupported, "1.75.0", VerdictUnknown},
		// CMake: the minimum of "min...policy_max" gates.
		{"cmake_minimum_required", "3.20", CompareMinimum, "3.28.1", VerdictSatisfied},
		{"cmake_minimum_required", "3.10...3.28", CompareMinimum, "3.9.6", VerdictMismatch},
		{"cmake_minimum_required", "3.10...", CompareUnsupported, "3.28.1", VerdictUnknown},
		{"unknown_kind", "1", CompareUnsupported, "1", VerdictUnknown},
	}
	for _, c := range cases {
		if got := Classify(c.kind, c.constraint); got != c.comparison {
			t.Errorf("Classify(%s, %q) = %s, want %s", c.kind, c.constraint, got, c.comparison)
		}
		if got := Decide(c.kind, c.constraint, c.observed); got != c.verdict {
			t.Errorf("Decide(%s, %q, %q) = %s, want %s", c.kind, c.constraint, c.observed, got, c.verdict)
		}
	}
	for _, v := range []string{"devel", "1.27.4 linux", "go1.27.4", "", "1.21.0+auto"} {
		if ComparableObservation("go", v) {
			t.Errorf("go observation %q must not be comparable", v)
		}
	}
	for _, v := range []string{"18.17.0-nightly", "v18.17.0", ""} {
		if ComparableObservation("node", v) {
			t.Errorf("node observation %q must not be comparable", v)
		}
	}
	if Decide("go_directive", "1.22", "devel") != VerdictUnknown {
		t.Fatal("incomparable observation compared")
	}
}
