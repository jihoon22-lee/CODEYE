package rustpack

import (
	"bytes"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Task failure reasons (TaskResult.Reason and outcome.reason).
const (
	ReasonCleanupUncertain     = pack.ReasonCleanupUncertain
	ReasonCancelled            = pack.ReasonCancelled
	ReasonTimeout              = pack.ReasonTimeout
	ReasonExecutionUnsupported = pack.ReasonExecutionUnsupported
	ReasonToolStartFailed      = pack.ReasonToolStartFailed
	ReasonExecutionRejected    = pack.ReasonExecutionRejected
	ReasonToolVersion          = "tool_version_unrecognized"
	ReasonSnapshotIntegrity    = "snapshot_integrity_failed"
	ReasonNotRun               = "cargo_not_run"
	ReasonWorkspaceDrift       = "workspace_drift"
	ReasonToolchain            = "toolchain_unavailable"
	ReasonLockMissing          = "lockfile_missing"
	ReasonLockOutdated         = "lockfile_outdated"
	ReasonDependency           = "dependency_unavailable"
	ReasonLinker               = "linker_unavailable"
	ReasonManifestInvalid      = "manifest_invalid"
	ReasonBuildScript          = "build_script_failed"
	ReasonOutputTruncated      = "output_truncated"
	ReasonOutputUnparsed       = "output_unparsed"
	ReasonExitMismatch         = "build_exit_mismatch"
	ReasonPackageErrors        = "package_errors"
	ReasonLintsDenied          = "lints_denied"
	ReasonFailed               = "cargo_failed"
	ReasonNoCrates             = "no_analyzable_crates"
)

// Crate (unit) states.
const (
	CrateAnalyzedClean        = "analyzed_clean"
	CrateAnalyzedWithFindings = "analyzed_with_findings"
	CrateFailed               = "failed_compile"
)

// Kind selects the analyzer: the cargo check baseline or the clippy extra.
type Kind int

const (
	KindCheck Kind = iota
	KindClippy
	KindDoc
)

func (k Kind) main() string {
	if k == KindDoc {
		return execDoc
	}
	if k == KindClippy {
		return execClippy
	}
	return execCheck
}

// Execution names inside one task, in run order.
const (
	execRustc   = "rustc.version"
	execCargo   = "cargo.version"
	execClippyV = "clippy.version"
	execDoc     = "cargo.doc"
	execCheck   = "cargo.check"
	execClippy  = "clippy.check"
)

// assembleInput is everything the pure document builder needs. Results are
// nil for executions that were not run.
type assembleInput struct {
	Kind                               Kind
	TaskKey, ManifestDigest, ModuleDir string
	Patterns                           []string
	Files                              map[string]source.Entry
	ScopeFiles                         []string
	// Source returns the snapshot bytes of a manifest file.
	Source                      func(rel string) ([]byte, bool)
	Drift                       map[string]bool
	Rustc, Cargo, ClippyV, Main *executor.Result
	Executions                  []analysis.Execution
	Mask                        func(string) string
	Cancelled, Integrity        bool
	ExtraLimitations            []string
	WorkspaceRoots              []string
	DepInfo                     []string // snapshot-relative .rs files rustc listed
	DepInfoOK                   bool
	ConfigFiles                 []obsdoc.ConfigFile
	ConfigKnown                 bool
	Linker                      bool
	OutOfScope                  func(rel string) bool
	// Dependencies is the limitation of a run that read the crates this
	// attempt fetched (CARGO_HOME of deps.rust); "" for a scratch home.
	Dependencies string
}

func ptr[T any](v T) *T { return &v }

// ---- category policy --------------------------------------------------------

var errorCode = regexp.MustCompile(`^E[0-9]{4}$`)

// Lints by subject. The table describes what a lint is about, not its impact;
// a lint that is not listed is maintainability (style, complexity, unused
// code), and a rustc error code is correctness. Clippy's JSON does not carry
// the lint group, so the table is CODEYE's own and versioned with the rule
// pack (RulePackVersion).
var categoryOf = map[string]string{
	// correctness
	"arithmetic_overflow": "correctness", "unconditional_panic": "correctness", "unconditional_recursion": "correctness", "overflowing_literals": "correctness",
	"invalid_value": "correctness", "invalid_atomic_ordering": "correctness", "deref_nullptr": "correctness", "unused_must_use": "error_handling",
	"clippy::eq_op": "correctness", "clippy::never_loop": "correctness", "clippy::absurd_extreme_comparisons": "correctness", "clippy::approx_constant": "correctness",
	"clippy::bad_bit_mask": "correctness", "clippy::ifs_same_cond": "correctness", "clippy::out_of_bounds_indexing": "correctness", "clippy::erasing_op": "correctness",
	"clippy::while_immutable_condition": "correctness", "clippy::self_assignment": "correctness", "clippy::almost_swapped": "correctness", "clippy::infinite_iter": "correctness",
	"clippy::iter_next_loop": "correctness", "clippy::overly_complex_bool_expr": "correctness", "clippy::logic_bug": "correctness", "clippy::min_max": "correctness",
	"clippy::reversed_empty_ranges": "correctness", "clippy::modulo_one": "correctness", "clippy::zero_divided_by_zero": "correctness", "clippy::no_effect": "correctness",
	"clippy::unit_cmp": "correctness", "clippy::nonsensical_open_options": "correctness", "clippy::not_unsafe_ptr_arg_deref": "correctness", "clippy::invalid_regex": "correctness",
	"clippy::derive_ord_xor_partial_ord": "correctness", "clippy::derived_hash_with_manual_eq": "correctness", "clippy::wrong_transmute": "correctness",
	"clippy::transmuting_null": "correctness", "clippy::mem_replace_with_uninit": "correctness", "clippy::uninit_assumed_init": "correctness", "clippy::cast_slice_different_sizes": "correctness",
	// unsafe review
	"unsafe_code": "security", "unsafe_op_in_unsafe_fn": "security", "clippy::undocumented_unsafe_blocks": "security", "clippy::multiple_unsafe_ops_per_block": "security",
	"clippy::transmute_ptr_to_ref": "security", "clippy::cast_ptr_alignment": "security",
	// panic review and error handling
	"clippy::panic_in_result_fn": "error_handling", "clippy::unwrap_in_result": "error_handling", "clippy::todo": "error_handling", "clippy::unimplemented": "error_handling",
	"clippy::unwrap_used": "error_handling", "clippy::expect_used": "error_handling", "clippy::panic": "error_handling", "clippy::unreachable": "error_handling",
	"clippy::indexing_slicing": "error_handling", "clippy::result_unit_err": "error_handling", "clippy::let_underscore_must_use": "error_handling",
	"clippy::unnecessary_literal_unwrap": "error_handling", "clippy::question_mark": "error_handling",
	// concurrency
	"clippy::await_holding_lock": "concurrency", "clippy::await_holding_refcell_ref": "concurrency", "clippy::mut_mutex_lock": "concurrency", "clippy::if_let_mutex": "concurrency",
	"clippy::arc_with_non_send_sync": "concurrency", "clippy::rc_mutex": "concurrency", "clippy::mutex_atomic": "concurrency", "clippy::mutex_integer": "concurrency",
	// performance
	"clippy::redundant_clone": "performance", "clippy::large_enum_variant": "performance", "clippy::box_collection": "performance", "clippy::vec_box": "performance",
	"clippy::inefficient_to_string": "performance", "clippy::iter_nth": "performance", "clippy::needless_collect": "performance", "clippy::manual_memcpy": "performance",
	"clippy::unnecessary_to_owned": "performance", "clippy::expect_fun_call": "performance", "clippy::or_fun_call": "performance", "clippy::useless_vec": "performance",
	"clippy::map_entry": "performance", "clippy::single_char_pattern": "performance", "clippy::redundant_allocation": "performance", "clippy::boxed_local": "performance",
	"clippy::large_stack_arrays": "performance", "clippy::extend_with_drain": "performance",
	// resources
	"clippy::mem_forget": "resource_lifecycle", "clippy::forget_non_drop": "resource_lifecycle", "clippy::drop_non_drop": "resource_lifecycle",
	// tests
	"clippy::tests_outside_test_module": "test_gap", "clippy::bool_assert_comparison": "test_gap",
}

func category(code string) string {
	if c, ok := categoryOf[code]; ok {
		return c
	}
	if errorCode.MatchString(code) {
		return "correctness"
	}
	return "maintainability"
}

// ---- position helpers -------------------------------------------------------

type fileText struct {
	lines [][]byte
	sha   string
}

type resolver struct {
	in    assembleInput
	cache map[string]*fileText
}

func splitLines(b []byte) [][]byte {
	lines := bytes.Split(b, []byte{'\n'})
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	return lines
}

func (rs *resolver) text(rel string) *fileText {
	if t, ok := rs.cache[rel]; ok {
		return t
	}
	var t *fileText
	if rs.in.Source != nil {
		if b, ok := rs.in.Source(rel); ok {
			t = &fileText{lines: splitLines(b), sha: obsdoc.SHA256Hex(b)}
		}
	}
	rs.cache[rel] = t
	return t
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// byteColumn converts rustc's 1-based character column (a count of Unicode
// scalar values, no byte offset; a tab counts 1) on line to a 1-based byte
// column. A column one past the last character is the end of the line.
func byteColumn(line []byte, col int, first bool) (int, bool) {
	prefix := 0
	if first && bytes.HasPrefix(line, utf8BOM) {
		prefix, line = len(utf8BOM), line[len(utf8BOM):]
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})
	off := 0
	for i := 1; i < col; i++ {
		if off >= len(line) {
			return 0, false
		}
		_, size := utf8.DecodeRune(line[off:])
		off += size
	}
	return prefix + off + 1, true
}

// snapshotPath maps a rustc span file name to a snapshot-relative path.
// Relative names are relative to the module directory (cargo runs rustc from
// the workspace root); absolute workspace names arrive redacted. A redacted
// name that is not a manifest file points at the scratch, the toolchain or a
// generated file, none of which is the snapshot.
func (rs *resolver) snapshotPath(file string) (rel string, redacted, ok bool) {
	redacted = strings.HasPrefix(file, analysis.RedactedMarker)
	rel, ok = govet.StripWorkspace(file, rs.in.ModuleDir, rs.in.WorkspaceRoots, true)
	if !ok || rel == "." {
		return "", redacted, false
	}
	return rel, redacted, true
}

func (rs *resolver) resolve(sp Span) (*string, *obsdoc.Location, string) {
	rel, redacted, ok := rs.snapshotPath(sp.File)
	if !ok {
		return nil, nil, obsdoc.LocationOutsideSnapshot
	}
	relp := rel
	entry, ok := rs.in.Files[rel]
	if !ok {
		if redacted {
			return nil, nil, obsdoc.LocationOutsideSnapshot
		}
		return &relp, nil, obsdoc.LocationMissingFile
	}
	if rs.in.Drift[rel] {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	t := rs.text(rel)
	if t == nil || t.sha != entry.SHA256 {
		return &relp, nil, obsdoc.LocationSourceDrift
	}
	n := len(t.lines)
	if sp.LineStart > max(n, 1) || sp.LineEnd > max(n, 1) {
		return &relp, nil, obsdoc.LocationLineOutOfRange
	}
	line := func(l int) []byte {
		if l-1 < n {
			return t.lines[l-1]
		}
		return nil
	}
	sc, ok1 := byteColumn(line(sp.LineStart), sp.ColStart, sp.LineStart == 1)
	ec, ok2 := byteColumn(line(sp.LineEnd), sp.ColEnd, sp.LineEnd == 1)
	if !ok1 || !ok2 || (sp.LineEnd == sp.LineStart && ec < sc) {
		return &relp, nil, obsdoc.LocationLineOutOfRange
	}
	return &relp, &obsdoc.Location{Path: rel, StartLine: sp.LineStart, StartCol: sc, EndLine: sp.LineEnd, EndCol: ec, ColumnUnit: "byte", BlobSHA256: entry.SHA256}, obsdoc.LocationResolved
}

func spanText(sp Span) string {
	return sp.File + ":" + itoa(sp.LineStart) + ":" + itoa(sp.ColStart) + "-" + itoa(sp.LineEnd) + ":" + itoa(sp.ColEnd)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func primarySpan(d Diag) (Span, bool) {
	for _, s := range d.Spans {
		if s.Primary {
			return s, true
		}
	}
	return Span{}, false
}

// unitName is the Cargo package name of a package id. Redaction can erase the
// directory name a path-style id ends with; the unit is then named after the
// manifest directory ("root" for the module root).
func unitName(pkgID, manifestPath string) string {
	n := PackageName(pkgID)
	if n != "" && !strings.Contains(n, analysis.RedactedMarker) {
		return n
	}
	dir := path.Base(path.Dir(strings.ReplaceAll(manifestPath, `\`, "/")))
	if dir == "" || dir == "." || strings.Contains(dir, analysis.RedactedMarker) {
		return "root"
	}
	return dir
}

// unitOf is the Cargo package name of a record.
func unitOf(m MsgRecord) string { return unitName(m.PackageID, m.ManifestPath) }

// ---- rule pack --------------------------------------------------------------

// Rule pack identities. Version 1 of each: bump it when the selection or its
// meaning changes (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제).
const (
	CheckRulePackID  = "codeye-rust-cargo-check"
	ClippyRulePackID = "codeye-rust-clippy"
	RulePackVersion  = "1"
)

// CheckSelection is the cargo check scope: every workspace member and every
// target kind (library, binaries, tests, examples, benches), default features,
// the host target. No lint level is changed.
var CheckSelection = []string{"--workspace", "--all-targets"}

// ClippyReviewLints are the allow-by-default clippy lints the pack enables for
// the unsafe and panic review (docs/ANALYZERS.md#unsafepanic-lint). They are the only lint levels
// CODEYE sets; `#[allow]` in the source still hides them.
var ClippyReviewLints = []string{"clippy::undocumented_unsafe_blocks", "clippy::multiple_unsafe_ops_per_block", "clippy::panic_in_result_fn", "clippy::unwrap_in_result", "clippy::todo", "clippy::unimplemented"}

// ClippySelection is the clippy scope: the check scope, lint levels capped at
// warn (a denied lint stays an observation instead of failing the build and
// leaving dependents unchecked), the review lints, and the kept diagnostics.
func ClippySelection() []string {
	sel := append([]string{}, CheckSelection...)
	sel = append(sel, "--", "--cap-lints", "warn")
	for _, l := range ClippyReviewLints {
		sel = append(sel, "-W", l)
	}
	return append(sel, "keep:clippy::*")
}

// ClippyArgs is the clippy argv after the cargo-clippy executable.
func ClippyArgs() []string {
	args := []string{"clippy", "--offline", "--locked", "--message-format=json", "--workspace", "--all-targets", "--", "--cap-lints", "warn"}
	for _, l := range ClippyReviewLints {
		args = append(args, "-W", l)
	}
	return args
}

// CheckArgs is the cargo argv after the cargo executable.
func CheckArgs() []string {
	return []string{"check", "--offline", "--locked", "--message-format=json", "--workspace", "--all-targets"}
}

func DocArgs() []string {
	return []string{"doc", "--offline", "--locked", "--workspace", "--no-deps", "--message-format=json"}
}

func keepCode(k Kind, code string) bool {
	if k == KindDoc {
		return strings.HasPrefix(code, "rustdoc::")
	}
	if k == KindClippy {
		return strings.HasPrefix(code, "clippy::")
	}
	return !strings.HasPrefix(code, "clippy::")
}

// ---- assemble ---------------------------------------------------------------

type crateState struct {
	name     string
	findings bool
	failed   bool
	seen     bool
}

// assemble builds the envelope and decides the task outcome. Success is one
// explicit conjunction; everything else keeps what was parsed.
func assemble(in assembleInput) obsdoc.Document {
	mask := in.Mask
	if mask == nil {
		mask = func(s string) string { return s }
	}
	id, tool, rulePack, sel, adapter := CheckAnalyzerID, "cargo", CheckRulePackID, CheckSelection, "cargo-check"
	required := true
	if in.Kind == KindClippy {
		id, tool, rulePack, sel, adapter = ClippyAnalyzerID, "clippy", ClippyRulePackID, ClippySelection(), "clippy"
		required = false
	}
	if in.Kind == KindDoc {
		id, tool, rulePack, sel, adapter = "rust.rustdoc", "cargo", "codeye-rust-doc", append(DocArgs(), "-W rustdoc::broken_intra_doc_links", "--cap-lints warn", "keep:rustdoc::*"), "cargo-doc"
		required = false
	}
	doc := obsdoc.New(obsdoc.Analyzer{ID: id, Ecosystem: "rust", Required: required}, obsdoc.Component{ID: adapter, Version: AdapterVersion},
		obsdoc.Component{ID: ParserID, Version: ParserVersion}, in.TaskKey, in.ManifestDigest)
	doc.Module.Dir = in.ModuleDir
	doc.Patterns = append([]string{}, in.Patterns...)
	doc.Tool.Name = tool
	// The repository's Cargo configuration, [lints] tables and clippy.toml are
	// honored (docs/ANALYZERS.md#rust-규칙-문맥과-억제); in-source #[allow]/#[expect] hide diagnostics and
	// nothing reports them (measured), so suppressions are honored too.
	doc.RulePack = obsdoc.RulePack{ID: rulePack, Version: RulePackVersion, Selection: append([]string{}, sel...), RepositoryConfig: obsdoc.RepoConfigHonored,
		SourceSuppressions: obsdoc.SuppressHonored, UnreportedSuppressionRules: []string{}}
	if in.ConfigKnown {
		doc.RulePack.ConfigFiles = append([]obsdoc.ConfigFile{}, in.ConfigFiles...)
	} else {
		doc.RulePack.ConfigFiles = nil
	}
	doc.Target.Properties = map[string]string{"features": "default", "targets": "all_targets", "workspace": "true", "host_only": "true", "profile": "dev"}
	if in.Kind == KindDoc {
		doc.Target.Properties["targets"] = "documentation"
	}
	doc.Coverage.Unit = "crate"
	doc.Coverage.FilesInScope = ptr(len(in.ScopeFiles))
	home := "offline_scratch_cargo_home"
	if in.Dependencies != "" {
		home = in.Dependencies
	}
	limitations := []string{"default_features_only", "host_target_only", home, "rustc_wrapper_overridden"}
	if in.Kind == KindClippy {
		limitations = append(limitations, "cap_lints_warn")
	}
	limitations = append(limitations, in.ExtraLimitations...)
	for _, e := range in.Executions {
		doc.Executions = append(doc.Executions, obsdoc.ExecutionOf(e.Name, e.Result))
	}
	mainName := in.Kind.main()
	if in.Integrity {
		doc.Outcome = obsdoc.Outcome{TaskStatus: domain.TaskFailed, Reason: ptr(ReasonSnapshotIntegrity), ExitClass: obsdoc.ExitNotRun}
		doc.Coverage.ProcessedPaths = []string{}
		doc.Limitations = limitations
		return doc
	}
	for p := range in.Drift {
		doc.WorkspaceDrift = append(doc.WorkspaceDrift, p)
	}
	sort.Strings(doc.WorkspaceDrift)
	if len(doc.WorkspaceDrift) > 0 {
		limitations = append(limitations, "workspace_drift")
	}

	// Identity.
	rustcOK, cargoOK, clippyOK := false, false, in.Kind != KindClippy
	if pack.ExitedZero(in.Rustc) {
		if r, ok := ParseRustcVV(in.Rustc.Stdout.Data); ok && pack.StreamsComplete(in.Rustc) {
			rustcOK = true
			doc.Target.Platform = ptr(r.Host)
			doc.Target.Properties["rustc"] = r.Release
			if !RustcVersionTested(r.Release) {
				limitations = append(limitations, "rustc_version_untested")
			}
		}
	}
	cargoVersion := ""
	if pack.ExitedZero(in.Cargo) {
		if v, ok := ParseCargoVersion(in.Cargo.Stdout.Data); ok && pack.StreamsComplete(in.Cargo) {
			cargoOK, cargoVersion = true, v
			rel, _, _ := strings.Cut(v, " ")
			doc.Target.Properties["cargo"] = rel
			if in.Kind != KindClippy {
				doc.Tool.Version, doc.Tool.VersionTested = ptr(v), CargoVersionTested(v)
				if !doc.Tool.VersionTested {
					limitations = append(limitations, "tool_version_untested")
				}
			}
		}
	}
	if in.Kind == KindClippy && pack.ExitedZero(in.ClippyV) {
		if v, ok := ParseClippyVersion(in.ClippyV.Stdout.Data); ok && pack.StreamsComplete(in.ClippyV) {
			clippyOK = true
			doc.Tool.Version, doc.Tool.VersionTested = ptr(v), ClippyVersionTested(v)
			if !doc.Tool.VersionTested {
				limitations = append(limitations, "tool_version_untested")
			}
			if !CargoVersionTested(cargoVersion) && cargoOK {
				limitations = append(limitations, "cargo_version_untested")
			}
		}
	}

	// Main stdout and stderr.
	var st Stream
	var facts StderrFacts
	parsed := false
	stdoutSHA := ""
	if in.Main != nil {
		st = ParseStream(in.Main.Stdout.Data)
		parsed = st.UnparsedBytes == 0 && st.ShapeErrors == 0 && st.DecodeError == ""
		stdoutSHA = obsdoc.SHA256Hex(in.Main.Stdout.Data)
		// stderr explains a failed run; a run that exited 0 with a successful
		// build-finished is decided by those two alone (a build script can
		// print any text).
		if in.Main.Status == executor.StatusExited && in.Main.ExitCode != nil && *in.Main.ExitCode != 0 {
			facts = ClassifyStderr(in.Main.Stderr.Data)
		}
	}
	doc.Parse.Records, doc.Parse.UnparsedBytes, doc.Parse.ShapeErrors, doc.Parse.DecodeError = st.Values, st.UnparsedBytes, st.ShapeErrors, st.DecodeError

	crates := map[string]*crateState{}
	pathResolver := &resolver{in: in}
	crateOf := func(manifestPath, pkgID string) *crateState {
		rel, _, ok := pathResolver.snapshotPath(manifestPath)
		if !ok {
			return nil
		}
		if _, listed := in.Files[rel]; !listed {
			return nil
		}
		c := crates[rel]
		if c == nil {
			c = &crateState{name: unitName(pkgID, manifestPath)}
			crates[rel] = c
		}
		return c
	}
	for _, a := range st.Artifacts {
		if c := crateOf(a.ManifestPath, a.PackageID); c != nil {
			c.seen = true
		}
	}
	for _, kind := range st.Artifacts {
		for _, k := range kind.TargetKinds {
			if k == "proc-macro" {
				limitations = append(limitations, "proc_macro_executed")
			}
		}
	}
	if len(st.BuildScripts) > 0 {
		limitations = append(limitations, "build_script_executed")
	}

	rs := &resolver{in: in, cache: map[string]*fileText{}}
	byKey := map[string]int{}
	ordinals := map[string]int{}
	compile, denied, linker := false, false, facts.Linker
	omitted, merged, excluded, unattributed := 0, 0, 0, 0
	for _, m := range st.Messages {
		d := m.Diag
		c := crateOf(m.ManifestPath, m.PackageID)
		switch d.Level {
		case "warning", "error":
		case "error: internal compiler error":
			compile = true
			doc.ToolErrors = appendToolError(doc.ToolErrors, obsdoc.ToolError{Execution: mainName, Kind: "internal_error", Unit: unitOf(m), Message: mask(d.Message)})
			if c != nil {
				c.failed = true
			}
			continue
		default:
			continue // note, help, failure-note carry no diagnostic of their own
		}
		prim, hasPrim := primarySpan(d)
		lintCoded := d.Code != "" && !errorCode.MatchString(d.Code) && hasPrim
		if d.Level == "error" && !lintCoded {
			if d.Code == "" && len(d.Spans) == 0 && (strings.HasPrefix(d.Message, "aborting due to") || strings.HasPrefix(d.Message, "could not compile")) {
				compile = true
				continue
			}
			compile = true
			kind := "compile"
			if strings.Contains(d.Message, "linker `") && strings.Contains(d.Message, "not found") {
				kind, linker = "linker", true
			}
			te := obsdoc.ToolError{Execution: mainName, Kind: kind, Unit: unitOf(m), Message: mask(d.Message)}
			if hasPrim {
				if rel, _, ok := rs.snapshotPath(prim.File); ok {
					if _, listed := in.Files[rel]; listed {
						te.Path, te.Line, te.Column = ptr(rel), prim.LineStart, 0
					}
				}
			}
			doc.ToolErrors = appendToolError(doc.ToolErrors, te)
			if c != nil {
				c.failed = true
			}
			continue
		}
		if d.Code == "" || len(d.Spans) == 0 {
			unattributed++
			continue
		}
		if !keepCode(in.Kind, d.Code) {
			excluded++
			continue
		}
		if !hasPrim {
			prim = d.Spans[0]
		}
		if d.Level == "error" {
			denied = true
		}
		pathPtr, loc, state := rs.resolve(prim)
		if in.OutOfScope != nil && pathPtr != nil && in.OutOfScope(*pathPtr) {
			omitted++
			continue
		}
		msg := mask(d.Message)
		if msg == "" {
			msg = "(empty message)"
		}
		key := strings.Join([]string{d.Code, d.Level, mask(spanText(prim)), msg}, "\x00")
		if _, dup := byKey[key]; dup {
			merged++
			continue
		}
		unit := unitOf(m)
		normStart := mask(itoa(prim.LineStart) + ":" + itoa(prim.ColStart))
		normEnd := mask(itoa(prim.LineEnd) + ":" + itoa(prim.ColEnd))
		file := ""
		if pathPtr != nil {
			file = *pathPtr
		} else {
			file = mask(prim.File)
		}
		tuple := strings.Join([]string{unit, d.Code, file, normStart, normEnd, msg}, "\x00")
		ordinal := ordinals[tuple]
		ordinals[tuple]++
		level := d.Level
		o := obsdoc.Observation{ID: obsdoc.ObservationID(id, in.ModuleDir, unit, d.Code, file+":"+normStart, normEnd, msg, ordinal), RuleID: d.Code, Category: category(d.Code),
			RawSeverity: &level, RawMessage: msg, Path: pathPtr, Location: loc, LocationState: state, Related: []obsdoc.Related{},
			RawRef: obsdoc.RawRef{Execution: mainName, Stream: "stdout", StreamSHA256: stdoutSHA, RecordIndex: m.Index, JSONPointer: "/message", ByteStart: m.ByteStart, ByteEnd: m.ByteEnd}, Unit: unit}
		addRelated := func(text string, sp Span) {
			if len(o.Related) >= 64 {
				return
			}
			rp, rl, rst := rs.resolve(sp)
			if text == "" {
				text = "(related location)"
			}
			o.Related = append(o.Related, obsdoc.Related{RawMessage: mask(text), Path: rp, Location: rl, LocationState: rst})
		}
		for _, sp := range d.Spans {
			if sp != prim {
				addRelated(sp.Label, sp)
			}
			if sp.HasReplaceFix {
				o.SuggestedFixes++
			}
		}
		for _, ch := range d.Children {
			for _, sp := range ch.Spans {
				addRelated(ch.Message, sp)
				if sp.HasReplaceFix {
					o.SuggestedFixes++
				}
			}
		}
		byKey[key] = len(doc.Observations)
		doc.Observations = append(doc.Observations, o)
		if c != nil && !o.Suppressed {
			c.findings = true
		}
	}
	if merged > 0 {
		limitations = append(limitations, "duplicate_diagnostics_merged")
	}
	if omitted > 0 {
		limitations = append(limitations, "observations_outside_scope_omitted")
	}
	if excluded > 0 && in.Kind != KindCheck {
		limitations = append(limitations, "rustc_diagnostics_excluded")
	}
	if unattributed > 0 {
		limitations = append(limitations, "diagnostics_without_location_omitted")
	}
	if facts.Other > 0 {
		limitations = append(limitations, "tool_stderr_errors")
	}
	if in.Linker {
		limitations = append(limitations, "linker_from_toolchain_path")
	}
	if !in.ConfigKnown {
		limitations = append(limitations, "repository_config_not_enumerated")
	}

	// Crates.
	for _, c := range crates {
		switch {
		case c.failed:
			doc.Coverage.Units[CrateFailed]++
		case c.findings:
			doc.Coverage.Units[CrateAnalyzedWithFindings]++
		case c.seen:
			doc.Coverage.Units[CrateAnalyzedClean]++
		}
	}

	exit := -1
	if in.Main != nil && in.Main.Status == executor.StatusExited && in.Main.ExitCode != nil {
		exit = *in.Main.ExitCode
	}
	finished := len(st.Finished) == 1
	finishedOK := finished && st.Finished[0]

	// Outcome: precedence from cleanup to parser state.
	results := []*executor.Result{in.Rustc, in.Cargo, in.ClippyV, in.Main}
	status, reason, class := pack.ExecutionOutcome(results, in.Cancelled)
	fail := func(s domain.TaskStatus, r, c string) {
		if reason == "" {
			status, reason, class = s, r, c
		}
	}
	if (in.Rustc != nil && !rustcOK) || (in.Cargo != nil && !cargoOK) || (in.ClippyV != nil && !clippyOK) {
		fail(domain.TaskFailed, ReasonToolVersion, obsdoc.ExitConfigurationError)
	}
	incomplete := in.Rustc == nil || in.Cargo == nil || in.Main == nil || (in.Kind == KindClippy && in.ClippyV == nil)
	if incomplete {
		fail(domain.TaskFailed, ReasonNotRun, obsdoc.ExitNotRun)
	}
	if len(doc.WorkspaceDrift) > 0 {
		fail(domain.TaskFailed, ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
	}
	switch {
	case facts.Toolchain:
		fail(domain.TaskFailed, ReasonToolchain, obsdoc.ExitConfigurationError)
	case facts.LockMissing:
		fail(domain.TaskFailed, ReasonLockMissing, obsdoc.ExitConfigurationError)
	case facts.LockOutdated:
		fail(domain.TaskFailed, ReasonLockOutdated, obsdoc.ExitConfigurationError)
	case facts.Dependency:
		fail(domain.TaskFailed, ReasonDependency, obsdoc.ExitConfigurationError)
	case linker:
		fail(domain.TaskFailed, ReasonLinker, obsdoc.ExitConfigurationError)
	case facts.ManifestInvalid:
		fail(domain.TaskFailed, ReasonManifestInvalid, obsdoc.ExitConfigurationError)
	case facts.BuildScript:
		fail(domain.TaskFailed, ReasonBuildScript, obsdoc.ExitExecutionError)
	}
	if in.Main != nil && !pack.StreamsComplete(in.Main) {
		fail(domain.TaskFailed, ReasonOutputTruncated, obsdoc.ExitExecutionError)
	}
	if in.Main != nil && !parsed {
		fail(domain.TaskFailed, ReasonOutputUnparsed, obsdoc.ExitExecutionError)
	}
	switch {
	case in.Main == nil:
	case exit != 0 && exit != 101:
		fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
	case exit == 0 && (!finishedOK || compile || denied):
		fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
	case exit == 101 && finishedOK:
		fail(domain.TaskFailed, ReasonExitMismatch, obsdoc.ExitExecutionError)
	case exit == 101 && (compile || len(doc.ToolErrors) > 0):
		fail(domain.TaskFailed, ReasonPackageErrors, obsdoc.ExitExecutionError)
	case exit == 101 && denied:
		fail(domain.TaskFailed, ReasonLintsDenied, obsdoc.ExitExecutionError)
	case exit == 101:
		// A failed build nothing explained in the record stream.
		fail(domain.TaskFailed, ReasonFailed, obsdoc.ExitExecutionError)
	}

	// Processed files: the sources rustc read, from the dep-info files.
	scope := map[string]bool{}
	for _, f := range in.ScopeFiles {
		scope[f] = true
	}
	processed := []string{}
	if in.Kind == KindDoc {
		// Cargo's documentation artifact names the crate's entry file.
		// Other modules are reported only when rustdoc located a diagnostic in them.
		verified := map[string]bool{}
		for _, art := range st.Artifacts {
			if !art.Documentation || art.SourcePath == "" {
				continue
			}
			if rel, _, ok := pathResolver.snapshotPath(art.SourcePath); ok && scope[rel] && !in.Drift[rel] {
				verified[rel] = true
			}
		}
		for _, o := range doc.Observations {
			if o.Path != nil && o.Location != nil && scope[*o.Path] {
				verified[*o.Path] = true
			}
		}
		for file := range verified {
			processed = append(processed, file)
		}
		sort.Strings(processed)
		if reason == "" && (len(crates) == 0 || len(processed) == 0) {
			fail(domain.TaskUnsupported, ReasonNoCrates, obsdoc.ExitConfigurationError)
		}
		if reason == "" {
			doc.Coverage.ProcessedPaths = processed
		}
		limitations = append(limitations, "rustdoc_module_coverage_partial")
	} else {
		if in.DepInfoOK {
			seen := map[string]bool{}
			for _, f := range in.DepInfo {
				if scope[f] && !seen[f] {
					seen[f] = true
					processed = append(processed, f)
				}
			}
			sort.Strings(processed)
			if reason == "" {
				doc.Coverage.FilesUnlisted = len(in.ScopeFiles) - len(processed)
				if doc.Coverage.FilesUnlisted > 0 {
					limitations = append(limitations, "rust_files_not_compiled")
				}
			}
		}
		if reason == "" && (!in.DepInfoOK || len(processed) == 0) {
			fail(domain.TaskUnsupported, ReasonNoCrates, obsdoc.ExitConfigurationError)
		}
		if reason == "" {
			doc.Coverage.ProcessedPaths = processed
		}

	}

	partial := false
	if reason == "" {
		class = obsdoc.ExitSuccessWithoutFindings
		if doc.Active() > 0 {
			class = obsdoc.ExitSuccessWithFindings
		}
	} else {
		partial = in.Main != nil && (st.Values > 0 || st.UnparsedBytes > 0 || in.Main.Stdout.Truncated || in.Main.Stdout.Incomplete)
	}
	switch {
	case class == obsdoc.ExitNotRun:
		doc.Observations, partial = []obsdoc.Observation{}, false
		doc.ToolErrors = []obsdoc.ToolError{}
		doc.Coverage.ProcessedPaths = []string{}
	case reason != "":
		// A failed run never claims files as processed.
		doc.Coverage.ProcessedPaths = nil
	}
	doc.Outcome = obsdoc.Outcome{TaskStatus: status, ExitClass: class, PartialParse: partial}
	if reason != "" {
		doc.Outcome.Reason = ptr(reason)
	}
	doc.Parse.OK = reason == "" && parsed
	doc.Limitations = limitations
	return doc
}

func appendToolError(list []obsdoc.ToolError, e obsdoc.ToolError) []obsdoc.ToolError {
	if len(list) >= 200 {
		return list
	}
	return append(list, e)
}
