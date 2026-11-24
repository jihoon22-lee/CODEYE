"""Contract checks for the analyzer packs (Staticcheck, Python, C/C++, JS/TS, Rust, structure and secrets, pattern taint)."""

from __future__ import annotations

import copy
import hashlib
from pathlib import Path
import re

from .common import ROOT, ensure, expect_invalid, json_file, yaml_file
from .report import ABSOLUTE, check_analyzer, check_analyzer_observations


def go_string_constant(path: Path, name: str) -> str:
    match = re.search(r"^\s*(?:const )?" + name + r'\s*=\s*"([^"]*)"$', path.read_text(encoding="utf-8"), re.M)
    ensure(match is not None, "Missing Go constant " + name + " in " + str(path))
    return match.group(1)


def check_staticcheck_pack_contracts(validators: dict) -> int:
    """The staticcheck descriptor, the real envelope example and the analyzer_tools environment field.

    Returns the number of negative cases."""
    negatives = 0
    descriptor = yaml_file(ROOT / "examples/analyzer-staticcheck.yaml")
    validators["analyzer.schema.json"].validate(descriptor)
    check_analyzer(descriptor)
    ensure(descriptor["exit_codes"] == {"clean": [0], "findings": [1], "error": [2]}, "staticcheck exit-code contract changed")
    checks = go_string_constant(ROOT / "internal/worker/analysis/staticcheck/staticcheck.go", "Checks")
    argv = descriptor["execute"]["argv"]
    ensure(argv[argv.index("-checks") + 1] == checks, "Descriptor -checks differs from staticcheck.Checks")
    envelope_validator = validators["analyzer-observations.schema.json"]
    envelope = json_file(ROOT / "examples/observations-staticcheck.json")
    envelope_validator.validate(envelope)
    check_analyzer_observations(envelope)
    ensure(envelope["analyzer"] == {"id": "go.staticcheck", "ecosystem": "go", "required": False}, "staticcheck is extra depth (not required)")
    ensure(envelope["rule_pack"]["selection"][1] == checks, "Envelope rule selection differs from staticcheck.Checks")
    ensure("-show-ignored" in argv and "-show-ignored" in envelope["rule_pack"]["selection"], "staticcheck must report suppressed problems (-show-ignored)")
    ensure(envelope["rule_pack"]["repository_config"] == "overridden" and envelope["rule_pack"]["source_suppressions"] == "reported" and envelope["rule_pack"]["config_files"] == [],
           "staticcheck rule context: -checks overrides staticcheck.conf, suppressions are reported")
    ensure(envelope["rule_pack"]["unreported_suppression_rules"] == ["U1000"], "staticcheck: U1000 suppressions are not reported by -show-ignored (measured)")
    ensure(envelope["outcome"]["exit_class"] == "success_with_findings" and executions_exit(envelope, "staticcheck.check") == 1, "Example is the exit-1 findings run (TC-07)")
    go_vet = json_file(ROOT / "examples/observations-go-vet.json")
    expect_invalid("go vet document as envelope", envelope_validator.validate, go_vet)
    expect_invalid("envelope as go vet document", validators["go-vet-observations.schema.json"].validate, envelope)
    negatives += 2

    def both(doc):
        envelope_validator.validate(doc)
        check_analyzer_observations(doc)

    def case(name, edit):
        nonlocal negatives
        bad = copy.deepcopy(envelope)
        edit(bad)
        expect_invalid(name, both, bad)
        negatives += 1

    case("success with partial parse", lambda d: d["outcome"].update(partial_parse=True))
    case("success without processed files", lambda d: d["coverage"].update(processed_paths=None))
    case("not_run with observations", lambda d: d["outcome"].update(task_status="unsupported", reason="tool_not_configured", exit_class="not_run"))
    case("observation count drift", lambda d: d["observations"].pop())
    case("unbound raw stream", lambda d: d["observations"][0]["raw_ref"].update(stream_sha256="0" * 64))
    case("raw range outside stream", lambda d: d["observations"][0]["raw_ref"].update(byte_end=1 << 30))
    case("truncated success", lambda d: d["executions"][-1]["stdout"].update(truncated=True))
    case("node path in argv", lambda d: d["executions"][-1]["command"]["argv"].append("/home/user/x"))
    case("hyphen analyzer id", lambda d: d["analyzer"].update(id="go.static-check"))
    case("unknown category", lambda d: d["observations"][0].update(category="style"))
    case("unsorted limitations", lambda d: d.update(limitations=list(reversed(d["limitations"]))))
    case("absolute target property", lambda d: d["target"]["properties"].update(goroot="/usr/lib/go"))
    case("config files with overridden configuration", lambda d: d["rule_pack"].update(config_files=[{"path": "staticcheck.conf", "sha256": "a" * 64}]))
    case("unenumerated config files without honored", lambda d: d["rule_pack"].update(config_files=None))
    case("suppressed with honored suppressions", lambda d: (d["rule_pack"].update(source_suppressions="honored", unreported_suppression_rules=[]), d["observations"][0].update(suppressed=True)))
    case("only suppressed but with findings", lambda d: [o.update(suppressed=True) for o in d["observations"]])
    case("unknown suppression mode", lambda d: d["rule_pack"].update(source_suppressions="ignored"))
    case("unreported suppression rules under honored", lambda d: d["rule_pack"].update(source_suppressions="honored"))
    case("unsorted unreported suppression rules", lambda d: d["rule_pack"].update(unreported_suppression_rules=["U1000", "SA4006"]))
    case("missing unreported suppression rules", lambda d: d["rule_pack"].pop("unreported_suppression_rules"))
    not_run = copy.deepcopy(envelope)
    not_run.update(observations=[], executions=[], limitations=["tool_not_configured"])
    not_run["outcome"] = {"task_status": "unsupported", "reason": "tool_not_configured", "exit_class": "not_run", "partial_parse": False}
    not_run["coverage"]["processed_paths"] = []
    not_run["parse"].update(ok=False, observations=0)
    both(not_run)
    environment = copy.deepcopy(json_file(ROOT / "examples/environment.json"))
    environment["environment"]["analyzer_tools"] = [
        {"tool": "staticcheck", "analyzers": ["go.staticcheck"], "status": "observed", "version": "2026.2.2 (0.8.2)", "detail": None, "execution": "staticcheck.version"}]
    validators["environment.schema.json"].validate(environment)
    for name, edit in (("observed without version", {"version": None}), ("runtime tool as analyzer tool", {"tool": "go"}),
                       ("not_probed with execution", {"status": "not_probed", "version": None}), ("version with a path", {"version": "/opt/sc 1"})):
        bad = copy.deepcopy(environment)
        bad["environment"]["analyzer_tools"][0].update(edit)
        expect_invalid(name, validators["environment.schema.json"].validate, bad)
        negatives += 1
    return negatives


def check_python_pack(validators: dict) -> None:
    """Python pack: the four descriptors, their selections against the Go constants and the real ruff envelope."""
    negatives = 0
    src = ROOT / "internal/worker/analysis/python"
    descriptors = {}
    for tool in ("ruff", "mypy", "pyright", "bandit"):
        descriptor = yaml_file(ROOT / f"examples/analyzer-{tool}.yaml")
        validators["analyzer.schema.json"].validate(descriptor)
        check_analyzer(descriptor)
        ensure(descriptor["id"] == "python." + tool and descriptor["executes_repository_code"] is False and descriptor["network_policy"] == "none",
               f"{tool}: Python tools run no repository code and no network")
        descriptors[tool] = descriptor
    ruff_rules = go_string_constant(src / "ruff.go", "RuffRules")
    ruff_ignore = go_string_constant(src / "ruff.go", "RuffIgnore")
    argv = descriptors["ruff"]["execute"]["argv"]
    ensure(argv[argv.index("--select") + 1] == ruff_rules and argv[argv.index("--ignore") + 1] == ruff_ignore, "ruff descriptor differs from RuffRules/RuffIgnore")
    ensure(not any(r.startswith("S") for r in ruff_rules.split(",")), "ruff S rules re-implement bandit (docs/ANALYZERS.md#python-엔진-중복)")
    ensure({"--isolated", "--unfixable", "--"} <= set(argv), "ruff: isolated configuration, no fix edits in stdout, -- before targets")
    ensure("--config-file=" in descriptors["mypy"]["execute"]["argv"], "mypy runs with the CODEYE configuration only (plugins execute code)")
    bandit_argv = descriptors["bandit"]["execute"]["argv"]
    ensure(bandit_argv[bandit_argv.index("--skip") + 1] == go_string_constant(src / "bandit.go", "BanditSkips") and "csv" in bandit_argv,
           "bandit descriptor differs from BanditSkips or streams JSON with source lines")
    envelope_validator = validators["analyzer-observations.schema.json"]
    envelope = json_file(ROOT / "examples/observations-ruff.json")
    envelope_validator.validate(envelope)
    check_analyzer_observations(envelope)
    rp = envelope["rule_pack"]
    ensure(envelope["analyzer"] == {"id": "python.ruff", "ecosystem": "python", "required": True}, "ruff is the required Python baseline")
    ensure(rp["selection"][rp["selection"].index("--select") + 1] == ruff_rules, "Envelope rule selection differs from RuffRules")
    ensure(rp["repository_config"] == "overridden" and rp["source_suppressions"] == "reported" and rp["config_files"] == [] and rp["unreported_suppression_rules"] == [],
           "ruff rule context: --isolated overrides configuration, --ignore-noqa reports every suppression form")
    ensure(envelope["outcome"]["exit_class"] == "success_with_findings" and executions_exit(envelope, "ruff.check-ignore-noqa") == 1, "Example is the exit-1 findings run")
    ensure(envelope["target"]["properties"].get("python_version_source") == "requires_python", "The target Python version and its source are recorded")

    def bad(name, edit):
        nonlocal negatives
        doc = copy.deepcopy(envelope)
        edit(doc)

        def both(d):
            envelope_validator.validate(d)
            check_analyzer_observations(d)
        expect_invalid(name, both, doc)
        negatives += 1

    bad("ruff success with partial parse", lambda d: d["outcome"].update(partial_parse=True))
    bad("ruff unbound raw stream", lambda d: d["observations"][0]["raw_ref"].update(stream_sha256="0" * 64))
    bad("ruff node path in argv", lambda d: d["executions"][-1]["command"]["argv"].append("/home/user/project"))
    bad("ruff suppressed under honored", lambda d: (d["rule_pack"].update(source_suppressions="honored"), d["observations"][0].update(suppressed=True)))
    print(f"PASS: Python pack (ruff, mypy, pyright, bandit descriptors; real ruff envelope; {negatives} negative cases)")


def go_string_slice(path: Path, name: str) -> list[str]:
    """The quoted elements of `var <name> = []string{...}` in a Go file."""
    match = re.search(r"^var " + name + r" = \[\]string\{(.*?)\}$", path.read_text(encoding="utf-8"), re.M | re.S)
    ensure(match is not None, "Missing Go []string " + name + " in " + str(path))
    return re.findall(r'"((?:[^"\\]|\\.)*)"', match.group(1))


CPP_SOURCE = (".c", ".cc", ".cpp", ".cxx", ".c++")


def check_cpp_pack_contracts(validators: dict) -> int:
    """C/C++ pack (docs/ANALYZERS.md): the cppcheck and clang-tidy descriptors, the real envelope examples bound to the
    committed fixture bytes, the rule context, the single required baseline and the expected fixture outcomes.

    Returns the number of negative cases."""
    negatives = 0
    package = ROOT / "internal/worker/analysis/cpp"
    fixture = ROOT / "fixtures/cpp"
    selection = go_string_slice(package / "cppcheck.go", "CppcheckSelection")
    checks = go_string_slice(package / "clangtidy.go", "ClangTidyChecks")
    cc = yaml_file(ROOT / "examples/analyzer-cppcheck.yaml")
    ct = yaml_file(ROOT / "examples/analyzer-clang-tidy.yaml")
    for descriptor in (cc, ct):
        validators["analyzer.schema.json"].validate(descriptor)
        check_analyzer(descriptor)
        ensure(descriptor["executes_repository_code"] is False and descriptor["requires_build"] is False and descriptor["network_policy"] == "none",
               descriptor["id"] + ": the C/C++ pack never executes repository code (compile DB read as data)")
    ensure(cc["exit_codes"] == {"clean": [0], "findings": [2], "error": [1]}, "cppcheck --error-exitcode=2 contract changed")
    ensure(ct["exit_codes"] == {"clean": [0], "findings": [], "error": [1]}, "clang-tidy warnings exit 0, compiler errors exit 1")
    argv = cc["execute"]["argv"]
    ensure(argv[1:1 + len(selection)] == selection and "--error-exitcode=2" in argv and argv[-1].startswith("--project="),
           "cppcheck descriptor argv differs from cpp.CppcheckArgs")
    ensure(not any(a.startswith("--inline-suppr") or a.startswith("--suppress") or a.startswith("--cppcheck-build-dir") for a in argv),
           "cppcheck: inline suppressions stay disabled and no build dir outside scratch")
    argv = ct["execute"]["argv"]
    ensure(argv[:2] == ["-p", "${scratch}/work"] and "--extra-arg-before=--no-default-config" in argv and "--" not in argv and not any(a.startswith("--fix") for a in argv),
           "clang-tidy descriptor argv differs from cpp.ClangTidyArgs (no fixed-DB '--', no fixes)")
    ensure(checks[:2] == ["-*", "clang-analyzer-*"], "clang-tidy selection starts from -* and carries the Clang Static Analyzer")
    ensure(not any(c.lstrip("-").startswith(("cert-", "hicpp-", "cppcoreguidelines-")) for c in checks),
           "clang-tidy alias families report the same diagnostic twice")
    ensure("bugprone-*" in checks and "-clang-analyzer-security.FloatLoopCounter" in checks, "CSA FloatLoopCounter duplicates bugprone-float-loop-counter (measured)")

    gcc = yaml_file(ROOT / "examples/analyzer-gcc-analyzer.yaml")
    validators["analyzer.schema.json"].validate(gcc)
    check_analyzer(gcc)
    ensure(gcc["languages"] == ["c"] and gcc["network_policy"] == "none" and not gcc["executes_repository_code"] and not gcc["requires_build"],
           "GCC file descriptor task covers C translation units and uses only the configured compiler")
    gcc_argv = gcc["execute"]["argv"]
    ensure(gcc_argv[1:-2] == go_string_slice(package / "gcc_analyzer.go", "gccAnalyzerArgs"), "GCC descriptor argv differs from the adapter")
    gcc_doc = json_file(ROOT / "examples/observations-gcc-analyzer.json")
    validators["analyzer-observations.schema.json"].validate(gcc_doc)
    check_analyzer_observations(gcc_doc)
    ensure(gcc_doc["analyzer"] == {"id": "cpp.gcc_analyzer", "ecosystem": "cpp", "required": False}, "GCC is optional")
    ensure(gcc_doc["rule_pack"]["selection"] == go_string_slice(package / "gcc_analyzer.go", "GCCAnalyzerSelection"),
           "GCC envelope differs from the file-descriptor rule selection")
    gcc_input = fixture / "gcc-analyzer/input"
    gcc_digest = hashlib.sha256((gcc_input / "compile_commands.json").read_bytes()).hexdigest()
    ensure(gcc_doc["rule_pack"]["config_files"] == [{"path": "compile_commands.json", "sha256": gcc_digest}], "GCC compile DB digest differs")
    ensure(gcc_doc["coverage"]["processed_paths"] == ["src/clean.c", "src/descriptor.c"] and
           len(gcc_doc["observations"]) == 1 and gcc_doc["observations"][0]["rule_id"] == "analyzer-fd-use-after-close",
           "GCC example should show the file descriptor error and the healthy control")
    for obs in gcc_doc["observations"]:
        for loc in [obs["location"], *[r["location"] for r in obs["related"]]]:
            source = (gcc_input / loc["path"]).read_bytes()
            ensure(loc["blob_sha256"] == hashlib.sha256(source).hexdigest() and loc["end_line"] <= len(source.splitlines()),
                   "GCC diagnostic location not bound to the captured source")

    envelope_validator = validators["analyzer-observations.schema.json"]
    db_sha = hashlib.sha256((fixture / "input/compile_commands.json").read_bytes()).hexdigest()
    expected = json_file(fixture / "expected.json")
    ensure(not (fixture / "input/expected.json").exists(), "Expected outcomes must stay outside the fixture input")
    case = expected["cases"]["with_generated_header"]

    def cpp_envelope(doc: dict, tool: str) -> None:
        envelope_validator.validate(doc)
        check_analyzer_observations(doc)
        rule_pack, coverage, outcome = doc["rule_pack"], doc["coverage"], doc["outcome"]
        ensure(doc["analyzer"] == ({"id": "cpp.cppcheck", "ecosystem": "cpp", "required": True} if tool == "cppcheck" else
                                   {"id": "cpp.clang_tidy", "ecosystem": "cpp", "required": False}), tool + ": cppcheck is the one required C/C++ baseline")
        ensure(rule_pack["repository_config"] == "honored" and rule_pack["config_files"] == [{"path": "compile_commands.json", "sha256": db_sha}],
               tool + ": the compile DB is honored repository build configuration bound to its manifest digest")
        ensure(rule_pack["source_suppressions"] == ("disabled" if tool == "cppcheck" else "honored") and rule_pack["unreported_suppression_rules"] == [],
               tool + ": cppcheck runs without --inline-suppr; clang-tidy NOLINT cannot be reported (measured)")
        ensure(rule_pack["selection"][:len(selection if tool == "cppcheck" else checks)] == (selection if tool == "cppcheck" else checks), tool + ": envelope selection differs from the Go source")
        if tool == "cppcheck":
            ensure(rule_pack["version"] == go_string_constant(package / "cppcheck.go", "CppcheckRulePackVersion"), "cppcheck rule pack version differs from the Go source")
            style_rule = go_string_constant(package / "cppcheck.go", "cppcheckRedundantAssignment")
            ensure(doc["target"]["properties"].get("style_observation_rule") == style_rule
                   and all(o["rule_id"] == style_rule for o in doc["observations"] if o.get("raw_severity") == "style"),
                   "Only redundantAssignment is published from cppcheck's style group")
        ensure(all(p.endswith(CPP_SOURCE) for p in coverage["processed_paths"] or []), tool + ": processed paths are translation units only (no header claims)")
        ensure(coverage["unit"] == "translation_unit" and doc["target"]["properties"]["mode"] == "compile_db", tool + ": compile DB mode example")
        ensure(not any(ABSOLUTE.match(v) or "/home/" in v or "/tmp/" in v for v in doc["target"]["properties"].values()), tool + ": node path in a target property")
        for o in doc["observations"]:
            source = fixture / "input" / o["location"]["path"]
            ensure(source.is_file() and o["location"]["blob_sha256"] == hashlib.sha256(source.read_bytes()).hexdigest(), tool + ": observation not bound to the committed fixture bytes")
        want = case["cppcheck" if tool == "cppcheck" else "clang_tidy"]
        ensure(outcome["task_status"] == want["task_status"] and outcome["exit_class"] == want["exit_class"] and coverage["processed_paths"] == want["processed_paths"] and
               coverage["units"] == want["units"] and doc["limitations"] == want["limitations"], tool + ": example differs from expected.json with_generated_header")
        got = sorted(o["rule_id"] + " " + o["path"] + ":" + str(o["location"]["start_line"]) for o in doc["observations"])
        ensure(got == want["observations"], tool + ": example observations differ from expected.json")

    examples = {"cppcheck": json_file(ROOT / "examples/observations-cppcheck.json"), "clang-tidy": json_file(ROOT / "examples/observations-clang-tidy.json")}
    for tool, doc in examples.items():
        cpp_envelope(doc, tool)
    ensure(executions_exit(examples["cppcheck"], "cppcheck.check") == 2 and executions_exit(examples["clang-tidy"], "clang-tidy.check.1") == 0,
           "Examples are the measured exits: cppcheck 2 with records, clang-tidy 0 with warnings")

    def bad(name, tool, edit):
        nonlocal negatives
        doc = copy.deepcopy(examples[tool])
        edit(doc)
        expect_invalid(name, lambda d: cpp_envelope(d, tool), doc)
        negatives += 1

    bad("clang-tidy as a second required baseline", "clang-tidy", lambda d: d["analyzer"].update(required=True))
    bad("cppcheck not required", "cppcheck", lambda d: d["analyzer"].update(required=False))
    bad("cppcheck inline suppressions honored", "cppcheck", lambda d: d["rule_pack"].update(source_suppressions="honored"))
    bad("clang-tidy NOLINT claimed reported", "clang-tidy", lambda d: d["rule_pack"].update(source_suppressions="reported"))
    bad("compile DB digest drift", "cppcheck", lambda d: d["rule_pack"]["config_files"][0].update(sha256="0" * 64))
    bad("compile DB not recorded", "clang-tidy", lambda d: d["rule_pack"].update(config_files=[]))
    bad("header claimed as processed", "clang-tidy", lambda d: d["coverage"].update(processed_paths=sorted(d["coverage"]["processed_paths"] + ["include/ringbuf/ring.h"])))
    bad("observation bound to other bytes", "cppcheck", lambda d: d["observations"][0]["location"].update(blob_sha256="0" * 64))
    bad("node path in a property", "clang-tidy", lambda d: d["target"]["properties"].update(drivers="/usr/bin/cc"))
    bad("alias family selected", "clang-tidy", lambda d: d["rule_pack"]["selection"].__setitem__(1, "cert-*"))
    bad("limitation dropped", "cppcheck", lambda d: d["limitations"].remove("code_loading_flags_dropped"))

    # Expected outcomes: a missing or generated header is a coverage gap (never a
    # pass, never a defect), reduced mode says so, and only succeeded tasks claim
    # processed files.
    gaps = {"partial_missing_include", "possibly_partial_missing_include", "failed_missing_include", "failed_compile", "failed_parse", "not_processed", "unknown"}
    for name, c in expected["cases"].items():
        for tool in ("cppcheck", "clang_tidy"):
            want = c.get(tool)
            if want is None:
                continue
            units = set(want["units"])
            ensure(want["task_status"] != "succeeded" or not units & gaps, name + " " + tool + ": success with a translation-unit gap")
            ensure((want["processed_paths"] is not None) == (want["task_status"] in ("succeeded", "unsupported")), name + " " + tool + ": processed paths only for succeeded or not-run tasks")
            ensure(all(p.endswith(CPP_SOURCE) for p in want["processed_paths"] or []), name + " " + tool + ": header in processed paths")
            if want["properties"].get("mode") == "reduced":
                ensure({"compile_db_missing", "reduced_mode_no_build_flags"} <= set(want["limitations"]) and not units & {"analyzed_clean", "analyzed_with_findings"},
                       name + ": reduced mode must say it ran without build flags")
            if tool == "clang_tidy" and "compile_commands.json" in c.get("remove", []):
                ensure(want["task_status"] == "unsupported" and want["reason"] == "compile_db_missing" and want["executions"] == [],
                       name + ": clang-tidy without a compile DB is not run (generation would execute repository code)")
    ensure(any(c.get("cppcheck", {}).get("reason") == "missing_include_files" for c in expected["cases"].values()), "A generated-header case must fail as missing_include_files")
    return negatives


def check_js_ts_pack_contracts(validators: dict) -> int:
    """JS/TS pack: the ESLint and tsc descriptors and their real envelope examples.

    Returns the number of negative cases."""
    negatives = 0
    envelope_validator = validators["analyzer-observations.schema.json"]

    def both(doc):
        envelope_validator.validate(doc)
        check_analyzer_observations(doc)

    eslint = yaml_file(ROOT / "examples/analyzer-eslint.yaml")
    tsc = yaml_file(ROOT / "examples/analyzer-tsc.yaml")
    for descriptor in (eslint, tsc):
        validators["analyzer.schema.json"].validate(descriptor)
        check_analyzer(descriptor)
        ensure(descriptor["executes_repository_code"] is False and descriptor["network_policy"] == "none", "tasks never execute repository code")
        ensure(descriptor["execute"]["executable"] == "${node}", "tools run through the shared node toolchain, never their #! line")
        ensure(descriptor["execute"]["env_allowlist"] == ["PATH", "LANG", "SystemRoot"], "environment allowlist changed")
    ensure(eslint["exit_codes"] == {"clean": [0], "findings": [1], "error": [2]}, "eslint exit-code contract changed")
    ensure(tsc["exit_codes"] == {"clean": [0], "findings": [2], "error": [1]}, "tsc exit-code contract changed")
    argv = eslint["execute"]["argv"]
    for flag in ("--no-config-lookup", "--no-inline-config", "--suppressions-location", "--"):
        ensure(flag in argv, "eslint argv must hold " + flag)
    ensure(argv.index("--") == len(argv) - 2, "eslint positionals follow --")
    flags = go_string_slice(ROOT / "internal/worker/analysis/tsc/tsc.go", "Flags")
    ensure(tsc["execute"]["argv"][1:-2] == flags, "Descriptor argv differs from tsc.Flags")
    ensure("--build" not in flags and "-b" not in flags and tsc["execute"]["argv"][-1].startswith("./"), "tsc: --noEmit, never --build, ./ project path")

    base = json_file(ROOT / "examples/observations-eslint.json")
    both(base)
    ensure(base["analyzer"] == {"id": "javascript.eslint", "ecosystem": "javascript", "required": True}, "javascript.eslint is the ecosystem baseline")
    rp = base["rule_pack"]
    ensure(rp["repository_config"] == "overridden" and rp["config_files"] == [] and rp["source_suppressions"] == "disabled" and rp["unreported_suppression_rules"] == [],
           "eslint rule context: managed config overrides the repository's, directives are disabled")
    ensure("--no-inline-config" in rp["selection"] and "repository_eslint_config_not_loaded" in base["limitations"], "eslint selection records the disabled directives")
    ensure(base["outcome"]["exit_class"] == "success_with_findings" and executions_exit(base, "eslint.check") == 1, "ESLint example is the exit-1 findings run")
    ensure(all(o["raw_ref"]["stream"] == "stdout" and o["raw_ref"]["json_pointer"] for o in base["observations"]), "ESLint observations point into stdout JSON")
    checker = json_file(ROOT / "examples/observations-tsc.json")
    both(checker)
    ensure(checker["analyzer"] == {"id": "javascript.tsc", "ecosystem": "javascript", "required": False}, "javascript.tsc is extra depth")
    rp = checker["rule_pack"]
    ensure(rp["repository_config"] == "honored" and rp["config_files"] and rp["source_suppressions"] == "honored" and rp["unreported_suppression_rules"] == [],
           "tsc rule context: tsconfig chain honored and enumerated, @ts-ignore/@ts-expect-error honored")
    ensure(checker["outcome"]["exit_class"] == "success_with_findings" and executions_exit(checker, "tsc.check") == 2, "tsc example is the exit-2 findings run")
    ensure([e["name"] for e in checker["executions"]] == ["tsc.version", "tsc.config", "tsc.check"], "tsc shows the effective config before the check")

    def case(name, doc, edit):
        nonlocal negatives
        bad = copy.deepcopy(doc)
        edit(bad)
        expect_invalid(name, both, bad)
        negatives += 1

    case("eslint suppressed under disabled directives", base, lambda d: d["observations"][0].update(suppressed=True))
    case("eslint config files under overridden configuration", base, lambda d: d["rule_pack"].update(config_files=[{"path": "eslint.config.js", "sha256": "a" * 64}]))
    case("eslint observation outside stdout", base, lambda d: d["observations"][0]["raw_ref"].update(stream="stderr"))
    case("tsc unreported suppression rules under honored", checker, lambda d: d["rule_pack"].update(unreported_suppression_rules=["TS2322"]))
    case("tsc success without processed files", checker, lambda d: d["coverage"].update(processed_paths=[]))
    case("tsc host path in target", checker, lambda d: d["target"]["properties"].update(tsconfig="/home/u/tsconfig.json"))
    return negatives


def executions_exit(doc: dict, name: str):
    return next(e["exit_code"] for e in doc["executions"] if e["name"] == name)


def check_rust_pack(validators: dict) -> int:
    """The cargo check and clippy descriptors and their real envelope examples (docs/ANALYZERS.md).

    Returns the number of negative cases."""
    negatives = 0
    envelope_validator = validators["analyzer-observations.schema.json"]
    source = ROOT / "internal/worker/analysis/rustpack/assemble.go"

    version = go_string_constant(source, "RulePackVersion")
    for name, analyzer_id, required, rule_pack_id_const, tool in (("cargo-check", "rust.cargocheck", True, "CheckRulePackID", "cargo"), ("clippy", "rust.clippy", False, "ClippyRulePackID", "clippy")):
        descriptor = yaml_file(ROOT / f"examples/analyzer-{name}.yaml")
        validators["analyzer.schema.json"].validate(descriptor)
        check_analyzer(descriptor)
        ensure(descriptor["id"] == analyzer_id and descriptor["languages"] == ["rust"], f"{name}: analyzer identity")
        ensure(descriptor["executes_repository_code"] is True and descriptor["requires_build"] is True, f"{name}: cargo runs build scripts, proc macros and repository Cargo configuration")
        ensure(descriptor["network_policy"] == "none", f"{name}: offline")
        argv = descriptor["execute"]["argv"]
        ensure({"--offline", "--locked", "--message-format=json", "--workspace", "--all-targets"} <= set(argv), f"{name}: offline, locked, JSON, whole workspace")
        ensure(descriptor["exit_codes"] == {"clean": [0], "findings": [], "error": [101]}, f"{name}: warnings exit 0, build failures exit 101")
        ensure(descriptor["execute"]["env_allowlist"].count("PATH") == 1 and "RUSTC_WRAPPER" in descriptor["execute"]["env_allowlist"], f"{name}: explicit environment")
        envelope = json_file(ROOT / f"examples/observations-{name}.json")
        envelope_validator.validate(envelope)
        check_analyzer_observations(envelope)
        ensure(envelope["analyzer"] == {"id": analyzer_id, "ecosystem": "rust", "required": required}, f"{name}: required follows the baseline rule")
        rule_pack = envelope["rule_pack"]
        ensure(rule_pack["id"] == go_string_constant(source, rule_pack_id_const) and rule_pack["version"] == version, f"{name}: rule pack identity differs from the Go constants")
        ensure(rule_pack["repository_config"] == "honored" and rule_pack["source_suppressions"] == "honored" and rule_pack["unreported_suppression_rules"] == [],
               f"{name}: repository configuration and in-source suppressions are honored (nothing reports #[allow])")
        ensure(isinstance(rule_pack["config_files"], list), f"{name}: the example enumerates the configuration it read")
        selection = rule_pack["selection"]
        ensure(selection[:2] == ["--workspace", "--all-targets"], f"{name}: selection starts with the scope")
        lints = [argv[i + 1] for i, a in enumerate(argv) if a == "-W"]
        ensure([s for i, s in enumerate(selection) if i > 0 and selection[i - 1] == "-W"] == lints, f"{name}: -W lints of the descriptor differ from the envelope selection")
        if name == "clippy":
            ensure(len(lints) == 6 and "--cap-lints" in argv and argv[argv.index("--cap-lints") + 1] == "warn" and argv[0] == "clippy", "clippy: review lints and capped lint levels, run as cargo-clippy")
            ensure(all(o["rule_id"].startswith("clippy::") for o in envelope["observations"]), "clippy keeps only clippy:: diagnostics")
            ensure(envelope["tool"]["name"] == "clippy" and "rustc_diagnostics_excluded" in envelope["limitations"], "clippy overlap with cargo check is excluded")
        else:
            ensure(argv[0] == "check" and not any(o["rule_id"].startswith("clippy::") for o in envelope["observations"]), "cargo check keeps rustc diagnostics")
        ensure(envelope["tool"]["name"] == tool and envelope["target"]["platform"] and envelope["target"]["properties"]["host_only"] == "true", f"{name}: tool and host target")
        ensure(envelope["outcome"]["exit_class"] == "success_with_findings" and executions_exit(envelope, f"{'cargo' if name == 'cargo-check' else 'clippy'}.check") == 0, f"{name}: warnings exit 0 and are findings")
        ensure(envelope["coverage"]["unit"] == "crate" and envelope["analyzer"]["ecosystem"] == "rust", f"{name}: crate coverage")

        def both(doc):
            envelope_validator.validate(doc)
            check_analyzer_observations(doc)

        def case(label, edit):
            nonlocal negatives
            bad = copy.deepcopy(envelope)
            edit(bad)
            expect_invalid(f"{name}: {label}", both, bad)
            negatives += 1

        case("success with partial parse", lambda d: d["outcome"].update(partial_parse=True))
        case("success without processed files", lambda d: d["coverage"].update(processed_paths=None))
        case("observation count drift", lambda d: d["observations"].pop())
        case("unbound raw stream", lambda d: d["observations"][0]["raw_ref"].update(stream_sha256="0" * 64))
        case("truncated success", lambda d: d["executions"][-1]["stdout"].update(truncated=True))
        case("hyphen analyzer id", lambda d: d["analyzer"].update(id="rust.cargo-check"))
        case("config files with overridden configuration", lambda d: d["rule_pack"].update(repository_config="overridden", config_files=[{"path": "Cargo.toml", "sha256": "a" * 64}]))
        case("suppressed observation with honored suppressions", lambda d: d["observations"][0].update(suppressed=True))
        case("unreported rules under honored suppressions", lambda d: d["rule_pack"].update(unreported_suppression_rules=["clippy::todo"]))
        case("node path in a target property", lambda d: d["target"]["properties"].update(cargo_home="/home/user/.cargo"))
    return negatives


def check_structure_pack_contracts(validators: dict) -> int:
    """(docs/ANALYZERS.md): lizard, jscpd and gitleaks descriptors and real envelopes.

    Metric observations are measurements, never active observations or
    findings; secret detections carry the fixed message and no value or
    column. Returns the number of negative cases."""
    negatives = 0
    envelope_validator = validators["analyzer-observations.schema.json"]
    adapters = ROOT / "internal/worker/analysis"
    secrets_name = "gitl" + "eaks"

    def go_int(path: Path, name: str) -> int:
        match = re.search(r"^\s*(?:const )?" + name + r"\s*=\s*(\d+)$", path.read_text(encoding="utf-8"), re.M)
        ensure(match is not None, "Missing Go constant " + name + " in " + str(path))
        return int(match.group(1))

    def check_envelope(doc: dict, metric: dict | None) -> None:
        """The generic rules on the diagnostics, the metric rules on the rest."""
        envelope_validator.validate(doc)
        ensure(doc["analyzer"].get("purpose") == ("metrics" if metric is not None else "secrets"), "Structure and secrets envelopes record their analyzer purpose")
        rest = copy.deepcopy(doc)
        metrics = [o for o in rest["observations"] if "metric" in o]
        rest["observations"] = [o for o in rest["observations"] if "metric" not in o]
        rest["parse"]["observations"] -= len(metrics)
        rest["parse"]["unresolved_observations"] -= sum(1 for o in metrics if o["location_state"] != "resolved")
        check_analyzer_observations(rest)
        ensure(doc["parse"]["observations"] == len(doc["observations"]), "Envelope observation count mismatch")
        executions = {e["name"]: e for e in doc["executions"]}
        for o in metrics:
            ensure(metric is not None, "Metric observation from a diagnostic analyzer")
            m = o["metric"]
            ensure(m["value"] >= m["threshold"] and not o["suppressed"], "Metric under its threshold or suppressed")
            ensure((m["name"], m["unit"], m["threshold"]) == (metric["name"], metric["unit"], metric["threshold"]), "Metric differs from the rule pack")
            ensure(o["category"] == "maintainability" and o["rule_id"] == metric["rule"], "Metric rule or category")
            ref = o["raw_ref"]
            stream = executions[ref["execution"]][ref["stream"]]
            ensure(ref["stream_sha256"] == stream["sha256"] and ref["byte_start"] < ref["byte_end"] <= stream["captured_bytes"], "Metric not bound to its stream")
            for place in [o] + o["related"]:
                ensure(place["location"] is None or place["location"]["path"] == place["path"], "Location/path mismatch")
        if metric is not None:
            ensure(len(metrics) == len(doc["observations"]) > 0, "Structure envelope with a diagnostic")
            ensure("metrics_not_defects" in doc["limitations"] and "data_directories_excluded" in doc["limitations"], "Structure envelope limitations")
            ensure(doc["rule_pack"]["repository_config"] == "overridden" and doc["rule_pack"]["config_files"] == [], "Structure rule pack reads no repository configuration")

    def same_argv(descriptor: dict, doc: dict, run: str) -> None:
        recorded = next(e for e in doc["executions"] if e["name"] == run)["command"]["argv"]
        argv = [descriptor["execute"]["executable"], *descriptor["execute"]["argv"]]
        for key in ("${scratch}", "${lizard}", "${node}", "${jscpd}", "${" + secrets_name + "}"):
            argv = [a.replace(key, "[REDACTED]") for a in argv]
        argv = [a.replace("${scan_root}", ".") for a in argv]
        ensure(argv == recorded, "Descriptor argv differs from the recorded " + run + " command")
        ensure(descriptor["executes_repository_code"] is False and descriptor["network_policy"] == "none", run + ": no repository code, no network")

    envelopes = {}
    for name in ("lizard", "jscpd", secrets_name):
        descriptor = yaml_file(ROOT / ("examples/analyzer-" + name + ".yaml"))
        validators["analyzer.schema.json"].validate(descriptor)
        check_analyzer(descriptor)
        envelopes[name] = (descriptor, json_file(ROOT / ("examples/observations-" + name + ".json")))
        ensure(descriptor.get("purpose") == envelopes[name][1]["analyzer"].get("purpose"), name + ": descriptor and envelope purpose differ")
    lizard_metric = {"name": "cyclomatic_complexity", "unit": "count", "rule": "cyclomatic_complexity", "threshold": go_int(adapters / "lizard/lizard.go", "CCNThreshold")}
    jscpd_metric = {"name": "duplicated_lines", "unit": "lines", "rule": "duplicated_code", "threshold": go_int(adapters / "jscpd/jscpd.go", "MinLines")}
    descriptor, lizard = envelopes["lizard"]
    check_envelope(lizard, lizard_metric)
    same_argv(descriptor, lizard, "lizard.run")
    ensure(not any(a.startswith("-E") for a in descriptor["execute"]["argv"]), "lizard extensions (-Eduplicate) belong to jscpd")
    descriptor, jscpd = envelopes["jscpd"]
    check_envelope(jscpd, jscpd_metric)
    same_argv(descriptor, jscpd, "jscpd.run")
    descriptor, secrets = envelopes[secrets_name]

    def check_secrets(doc: dict) -> None:
        check_envelope(doc, None)
        for o in doc["observations"]:
            ensure(o["category"] == "security" and o["raw_message"] == "secret detected by " + secrets_name + " rule " + o["rule_id"] + " (value withheld)", "Secret observation text is the fixed template")
            ensure(o["location"]["start_col"] == o["location"]["end_col"] == 0, "Secret columns are not recorded")
        ensure({"history_not_scanned", "secret_values_withheld", "secret_columns_not_recorded"} <= set(doc["limitations"]), "gitleaks limitations")

    check_secrets(secrets)
    same_argv(descriptor, secrets, secrets_name + ".scan")
    ensure("--redact=100" in descriptor["execute"]["argv"] and descriptor["execute"]["argv"][-2] == "--", "gitleaks: full redaction, roots after --")
    template = re.search(r"^const Template = `([^`]*)`$", (adapters / secrets_name / (secrets_name + ".go")).read_text(encoding="utf-8"), re.M)
    ensure(template is not None and not re.search(r"\.(?:Secret|Match|Line|Fingerprint|Commit|Author|Email|Message)\b", template.group(1)), "gitleaks template must not read secret material")

    def case(name, doc, metric, edit):
        nonlocal negatives
        bad = copy.deepcopy(doc)
        edit(bad)
        expect_invalid(name, check_secrets if doc is secrets else lambda d: check_envelope(d, metric), bad)
        negatives += 1

    case("metric under threshold", lizard, lizard_metric, lambda d: d["observations"][0]["metric"].update(value=lizard_metric["threshold"] - 1))
    case("metric unknown unit", lizard, lizard_metric, lambda d: d["observations"][0]["metric"].update(unit="percent"))
    case("metric extra field", jscpd, jscpd_metric, lambda d: d["observations"][0]["metric"].update(severity="high"))
    case("metric-only success with findings", jscpd, jscpd_metric, lambda d: d["outcome"].update(exit_class="success_with_findings"))
    case("suppressed metric", lizard, lizard_metric, lambda d: d["observations"][0].update(suppressed=True))
    case("metric threshold not the rule pack", jscpd, jscpd_metric, lambda d: d["observations"][0]["metric"].update(threshold=5))
    case("metric in the secrets envelope", secrets, None, lambda d: d["observations"][0].update(metric={"name": "entropy", "value": 4, "threshold": 3, "unit": "count"}))
    case("structure envelope reading repository config", lizard, lizard_metric, lambda d: d["rule_pack"].update(repository_config="honored"))
    case("secret columns recorded", secrets, None, lambda d: d["observations"][0]["location"].update(start_col=13, end_col=32))
    case("secret message with more than the rule", secrets, None, lambda d: d["observations"][0].update(raw_message=d["observations"][0]["raw_message"] + ": AKIA"))
    case("secret limitations dropped", secrets, None, lambda d: d.update(limitations=[x for x in d["limitations"] if x != "secret_values_withheld"]))
    case("secrets envelope without its purpose", secrets, None, lambda d: d["analyzer"].pop("purpose"))
    case("metrics envelope marked secrets", lizard, lizard_metric, lambda d: d["analyzer"].update(purpose="secrets"))
    case("unknown analyzer purpose", jscpd, jscpd_metric, lambda d: d["analyzer"].update(purpose="diagnostics"))
    report = json_file(ROOT / "examples/report-go-vet.json")
    validators["report.schema.json"].validate(report)
    bad = copy.deepcopy(report)
    bad["observations"][0]["metric"] = {"name": "cyclomatic_complexity", "value": 17, "threshold": 16, "unit": "percent"}
    expect_invalid("report metric unit", validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(report)
    bad["summary"]["metric_observations"] = 0
    expect_invalid("report zero metric count (omitted when 0)", validators["report.schema.json"].validate, bad)
    return negatives + 2


def check_opengrep_pack(validators: dict) -> int:
    """(docs/ANALYZERS.md#opengrep-패턴taint-팩) the Opengrep descriptor and the real envelope.

    The rule pack is CODEYE's own embedded files only (no registry, URL or
    interfile switch), the rule list matches the embedded rule IDs with one
    CWE each, flow steps are tool_flow_step notes with fixed texts inside the
    result's raw range, and in-source suppressions are reported. Returns the
    number of negative cases."""
    negatives = 0
    envelope_validator = validators["analyzer-observations.schema.json"]
    package = ROOT / "internal/worker/analysis/opengrep"
    descriptor = yaml_file(ROOT / "examples/analyzer-opengrep.yaml")
    validators["analyzer.schema.json"].validate(descriptor)
    check_analyzer(descriptor)
    argv = descriptor["execute"]["argv"]
    ensure(descriptor["id"] == go_string_constant(package / "opengrep.go", "AnalyzerID") and descriptor["network_policy"] == "none"
           and not descriptor["executes_repository_code"] and not descriptor["requires_build"], "opengrep: identity, offline, no repository code")
    ensure({"--taint-intrafile", "--sarif", "--dataflow-traces", "--no-rewrite-rule-ids"} <= set(argv) and argv[-2] == "--", "opengrep: intra-file taint, SARIF with traces, files after --")
    ensure(not any(a in ("--enable-interfile", "auto") or a.startswith(("p/", "r/", "git+")) or "://" in a for a in argv), "opengrep: no interfile analysis, registry, URL or git rules")
    ensure(descriptor["exit_codes"] == {"clean": [0], "findings": [], "error": [2, 4, 5, 7, 8]}, "opengrep exit-code contract changed")
    ids = set()
    cwe = {}
    for rules_file in sorted((package / "rules").glob("*.yaml")):
        text = rules_file.read_text(encoding="utf-8")
        for block in re.split(r"(?m)^  - id: ", text)[1:]:
            rule = block.split()[0]
            match = re.search(r'cwe: \["(CWE-\d+)"\]', block)
            ensure(rule.startswith("codeye." + rules_file.stem + ".") and match is not None, f"{rule}: CODEYE rule ID with a CWE")
            ids.add(rule)
            cwe[rule] = match.group(1)
    envelope = json_file(ROOT / "examples/observations-opengrep.json")

    def check(doc: dict) -> None:
        envelope_validator.validate(doc)
        check_analyzer_observations(doc)
        ensure(doc["analyzer"] == {"id": descriptor["id"], "ecosystem": "any", "required": False}, "opengrep is an optional depth analyzer")
        pack = doc["rule_pack"]
        ensure(pack["id"] == go_string_constant(package / "opengrep.go", "RulePackID") and pack["version"] == go_string_constant(package / "opengrep.go", "RulePackVersion"),
               "opengrep: rule pack identity differs from the Go constants")
        ensure(pack["repository_config"] == "overridden" and pack["config_files"] == [] and pack["source_suppressions"] == "reported", "opengrep rule context")
        ensure([r for r in pack["selection"] if r.startswith("rules/")] == sorted(f"rules/{p.stem}.yaml@" + hashlib.sha256(p.read_bytes()).hexdigest()[:12] for p in (package / "rules").glob("*.yaml")),
               "opengrep: selection digests differ from the embedded rule files")
        listed = {r["id"]: r for r in doc["rules"]}
        ensure(set(listed) <= ids and all(listed[i]["cwe"] == [cwe[i]] for i in listed), "opengrep: envelope rules are embedded rules with their CWE")
        ensure({"intra_file_flows_only", "request_input_sources_only", "tool_output_holds_source_text"} <= set(doc["limitations"]), "opengrep limitations")
        scan = next(e for e in doc["executions"] if e["name"] == "opengrep.scan")["stdout"]
        for o in doc["observations"]:
            ensure(o["rule_id"] in listed and o["category"] == "security", "opengrep observation rule and category")
            ensure(o["raw_ref"]["stream_sha256"] == scan["sha256"] and o["raw_ref"]["json_pointer"].startswith("/runs/0/results/"), "opengrep raw reference")
            ensure(not re.search(r"\.(?:py|js|ts|go|java|c|rs)\b|[(){};=]", o["raw_message"]), "opengrep observation text quotes no code")
            for n, step in enumerate(o["related"], 1):
                ensure(step["kind"] == "tool_flow_step" and re.fullmatch(r"taint flow step \d+ of \d+: (source|sink|call|callee parameter|callee return|propagation)", step["raw_message"]) is not None,
                       "opengrep flow step kind and fixed text")
        ensure(any(o["suppressed"] for o in doc["observations"]) and any(len(o["related"]) > 2 for o in doc["observations"]), "opengrep example shows suppression and a flow through a call")

    check(envelope)

    def without_configs(args: list[str]) -> list[str]:
        """The argv without its --config pairs (one per rule language) and the files after --."""
        out, i = [], 0
        while i < len(args) and args[i] != "--":
            if args[i] == "--config":
                i += 2
                continue
            out.append(args[i].replace("${scratch}", "[REDACTED]"))
            i += 1
        return out

    recorded = next(e for e in envelope["executions"] if e["name"] == "opengrep.scan")["command"]["argv"]
    ensure(recorded[0] == "[REDACTED]" and without_configs(recorded[1:]) == without_configs(argv), "Descriptor argv differs from the recorded opengrep.scan command")
    ensure(all(recorded[i + 1].startswith("[REDACTED]/analyzers/opengrep/rules/") for i, a in enumerate(recorded) if a == "--config"), "opengrep reads only the rule files CODEYE wrote")

    def case(label, edit):
        nonlocal negatives
        bad = copy.deepcopy(envelope)
        edit(bad)
        expect_invalid("opengrep: " + label, check, bad)
        negatives += 1

    case("registry configuration recorded", lambda d: d["rule_pack"].update(repository_config="honored"))
    case("rule outside the pack", lambda d: d["rules"].append({"id": "codeye.python.zzz", "short_description": "x", "cwe": ["CWE-1"]}))
    case("rule with another CWE", lambda d: d["rules"][0].update(cwe=["CWE-1"]))
    case("flow step quoting code", lambda d: next(o for o in d["observations"] if o["related"])["related"][0].update(raw_message="taint flow step 1 of 3: source request.args"))
    case("flow step of another kind", lambda d: next(o for o in d["observations"] if o["related"])["related"][0].update(kind="note"))
    case("suppressions honored", lambda d: d["rule_pack"].update(source_suppressions="honored"))
    case("required analyzer", lambda d: d["analyzer"].update(required=True))
    return negatives
