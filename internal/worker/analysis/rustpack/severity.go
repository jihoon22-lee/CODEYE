package rustpack

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis/severity"

// The default-severity tables of the Rust pack (docs/ANALYZERS.md#기본-심각도).
// A rustc error code (E0000) is a compile error; lints follow the subject
// table of the rule pack (categoryOf).
var rustRules = []severity.Rule{
	{Pattern: "deref_nullptr", Family: "null_dereference"}, {Pattern: "clippy::transmuting_null", Family: "null_dereference"},
	{Pattern: "clippy::not_unsafe_ptr_arg_deref", Family: "memory_safety"}, {Pattern: "invalid_value", Family: "memory_safety"},
	{Pattern: "clippy::uninit_assumed_init", Family: "memory_safety"}, {Pattern: "clippy::mem_replace_with_uninit", Family: "memory_safety"},
	{Pattern: "clippy::out_of_bounds_indexing", Family: "memory_safety"}, {Pattern: "clippy::wrong_transmute", Family: "memory_safety"},
	{Pattern: "clippy::cast_slice_different_sizes", Family: "memory_safety"}, {Pattern: "clippy::transmute_ptr_to_ref", Family: "memory_safety"},
	{Pattern: "clippy::cast_ptr_alignment", Family: "memory_safety"},
	{Pattern: "arithmetic_overflow", Family: "undefined_behavior"}, {Pattern: "unconditional_panic", Family: "panic_path"},
	{Pattern: "unconditional_recursion", Family: "logic_error"}, {Pattern: "overflowing_literals", Family: "logic_error"},
	{Pattern: "invalid_atomic_ordering", Family: "concurrency"},
	{Pattern: "clippy::await_holding_*", Family: "concurrency"}, {Pattern: "clippy::mut_mutex_lock", Family: "concurrency"},
	{Pattern: "clippy::if_let_mutex", Family: "concurrency"}, {Pattern: "clippy::arc_with_non_send_sync", Family: "concurrency"},
	{Pattern: "clippy::rc_mutex", Family: "concurrency"}, {Pattern: "clippy::mutex_*", Family: "performance"},
	{Pattern: "unused_must_use", Family: "unchecked_error"}, {Pattern: "clippy::let_underscore_must_use", Family: "unchecked_error"},
	{Pattern: "clippy::result_unit_err", Family: "api_misuse"},
	{Pattern: "clippy::unwrap_used", Family: "panic_path"}, {Pattern: "clippy::expect_used", Family: "panic_path"},
	{Pattern: "clippy::panic", Family: "panic_path"}, {Pattern: "clippy::panic_in_result_fn", Family: "panic_path"},
	{Pattern: "clippy::unwrap_in_result", Family: "panic_path"}, {Pattern: "clippy::todo", Family: "panic_path"},
	{Pattern: "clippy::unimplemented", Family: "panic_path"}, {Pattern: "clippy::unreachable", Family: "panic_path"},
	{Pattern: "clippy::indexing_slicing", Family: "panic_path"}, {Pattern: "clippy::unnecessary_literal_unwrap", Family: "style"},
	{Pattern: "unsafe_code", Family: "security_weakness"}, {Pattern: "unsafe_op_in_unsafe_fn", Family: "security_weakness"},
	{Pattern: "clippy::undocumented_unsafe_blocks", Family: "documentation"}, {Pattern: "clippy::multiple_unsafe_ops_per_block", Family: "maintainability"},
	{Pattern: "clippy::mem_forget", Family: "resource_leak"}, {Pattern: "clippy::forget_non_drop", Family: "logic_error"},
	{Pattern: "clippy::drop_non_drop", Family: "logic_error"},
	{Pattern: "clippy::eq_op", Family: "logic_error"}, {Pattern: "clippy::never_loop", Family: "logic_error"},
	{Pattern: "clippy::absurd_extreme_comparisons", Family: "logic_error"}, {Pattern: "clippy::approx_constant", Family: "logic_error"},
	{Pattern: "clippy::bad_bit_mask", Family: "logic_error"}, {Pattern: "clippy::ifs_same_cond", Family: "logic_error"},
	{Pattern: "clippy::erasing_op", Family: "logic_error"}, {Pattern: "clippy::while_immutable_condition", Family: "logic_error"},
	{Pattern: "clippy::self_assignment", Family: "logic_error"}, {Pattern: "clippy::almost_swapped", Family: "logic_error"},
	{Pattern: "clippy::infinite_iter", Family: "logic_error"}, {Pattern: "clippy::iter_next_loop", Family: "logic_error"},
	{Pattern: "clippy::overly_complex_bool_expr", Family: "logic_error"}, {Pattern: "clippy::logic_bug", Family: "logic_error"},
	{Pattern: "clippy::min_max", Family: "logic_error"}, {Pattern: "clippy::reversed_empty_ranges", Family: "logic_error"},
	{Pattern: "clippy::modulo_one", Family: "logic_error"}, {Pattern: "clippy::zero_divided_by_zero", Family: "logic_error"},
	{Pattern: "clippy::no_effect", Family: "unused_code"}, {Pattern: "clippy::unit_cmp", Family: "logic_error"},
	{Pattern: "clippy::nonsensical_open_options", Family: "api_misuse"}, {Pattern: "clippy::invalid_regex", Family: "api_misuse"},
	{Pattern: "clippy::derive_ord_xor_partial_ord", Family: "api_misuse"}, {Pattern: "clippy::derived_hash_with_manual_eq", Family: "api_misuse"},
	{Pattern: "clippy::tests_outside_test_module", Family: "maintainability"}, {Pattern: "clippy::bool_assert_comparison", Family: "style"},
	{Pattern: "dead_code", Family: "unused_code"}, {Pattern: "unused_*", Family: "unused_code"},
	{Pattern: "deprecated", Family: "deprecated_api"},
	{Pattern: "E*", Family: "type_error"},
	{Pattern: "clippy::redundant_clone", Family: "performance"}, {Pattern: "clippy::large_enum_variant", Family: "performance"},
	{Pattern: "clippy::box_collection", Family: "performance"}, {Pattern: "clippy::vec_box", Family: "performance"},
	{Pattern: "clippy::inefficient_to_string", Family: "performance"}, {Pattern: "clippy::iter_nth", Family: "performance"},
	{Pattern: "clippy::needless_collect", Family: "performance"}, {Pattern: "clippy::manual_memcpy", Family: "performance"},
	{Pattern: "clippy::unnecessary_to_owned", Family: "performance"}, {Pattern: "clippy::expect_fun_call", Family: "performance"},
	{Pattern: "clippy::or_fun_call", Family: "performance"}, {Pattern: "clippy::useless_vec", Family: "performance"},
	{Pattern: "clippy::map_entry", Family: "performance"}, {Pattern: "clippy::single_char_pattern", Family: "performance"},
	{Pattern: "clippy::redundant_allocation", Family: "performance"}, {Pattern: "clippy::boxed_local", Family: "performance"},
	{Pattern: "clippy::large_stack_arrays", Family: "performance"}, {Pattern: "clippy::extend_with_drain", Family: "performance"},
	{Pattern: "clippy::*", Family: "style"},
	{Pattern: "rustdoc::*", Family: "documentation"},
}

var (
	CheckSeverity = severity.Table{
		Analyzer: CheckAnalyzerID, RulePack: CheckRulePackID, RulePackVersions: []string{RulePackVersion}, Version: "1",
		Rules: rustRules, Grades: map[string]string{"error": "type_error", "warning": "maintainability"},
	}
	ClippySeverity = severity.Table{
		Analyzer: ClippyAnalyzerID, RulePack: ClippyRulePackID, RulePackVersions: []string{RulePackVersion}, Version: "1",
		Rules: rustRules, Grades: map[string]string{"error": "type_error", "warning": "maintainability"},
	}
	DocSeverity = severity.Table{
		Analyzer: "rust.rustdoc", RulePack: "codeye-rust-doc", RulePackVersions: []string{"1"}, Version: "1",
		Rules: rustRules, Default: "documentation",
	}
)
