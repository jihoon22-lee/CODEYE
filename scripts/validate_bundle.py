#!/usr/bin/env python3
"""Validate CODEYE contracts and examples, independently of runtime tests.

Checks local Markdown file links (not rendered anchors), JSON Schemas,
examples, evidence locations/hashes, counts, DAGs, prompt references,
OpenAPI references/path parameters, and a small set of negative cases.
It is not a complete OpenAPI semantic validator or a product E2E test.
"""
from __future__ import annotations

import copy
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import sys
from urllib.parse import unquote, urldefrag

try:
    import yaml
    from jsonschema import Draft202012Validator, FormatChecker, ValidationError
    from referencing import Registry, Resource
except ImportError as exc:
    raise SystemExit("Install the approved packages in scripts/requirements.txt: " + str(exc))

ROOT = Path(__file__).resolve().parents[1]


def ensure(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def json_file(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def yaml_file(path: Path):
    return yaml.safe_load(path.read_text(encoding="utf-8"))


def within_root(path: Path) -> Path:
    resolved = path.resolve()
    ensure(resolved.is_relative_to(ROOT), "Path leaves repository: " + str(path))
    return resolved


def source_location(location: dict, root: Path = ROOT) -> Path:
    name = location["path"]
    relative = PurePosixPath(name)
    ensure(not relative.is_absolute() and ".." not in relative.parts, "Unsafe source path")
    ensure("\\" not in name and not re.match(r"^[A-Za-z]:", name), "Non-canonical source path")
    path = (root / name).resolve()
    ensure(path.is_relative_to(root.resolve()), "Evidence leaves source root: " + name)
    ensure(path.is_file(), "Missing example source: " + name)
    lines = path.read_text(encoding="utf-8").splitlines()
    ensure(1 <= location["start_line"] <= location["end_line"] <= len(lines), "Invalid line range: " + name)
    return path


def check_plan(plan: dict) -> None:
    tasks = plan["tasks"]
    ids = [task["id"] for task in tasks]
    ensure(len(ids) == len(set(ids)), "Duplicate task ID")
    graph = {task["id"]: task["depends_on"] for task in tasks}
    active: set[str] = set()
    visited: set[str] = set()
    def visit(node: str) -> None:
        ensure(node in graph, "Unknown dependency: " + node)
        ensure(node not in active, "Task graph contains a cycle")
        if node in visited:
            return
        active.add(node)
        for dependency in graph[node]:
            visit(dependency)
        active.remove(node)
        visited.add(node)
    for task_id in graph:
        visit(task_id)


def check_report(report: dict, source_root: Path = ROOT) -> None:
    findings = report["findings"]
    ids = [finding["id"] for finding in findings]
    ensure(len(ids) == len(set(ids)), "Duplicate finding ID")
    summary = report["summary"]
    ensure(summary["total_findings"] == len(findings), "Finding total mismatch")
    actionable = sum(f["validation"] in ("supported", "reproduced") and f["disposition"] in ("open", "acknowledged") for f in findings)
    pending = sum(f["validation"] in ("unreviewed", "inconclusive") for f in findings)
    refuted = sum(f["validation"] == "refuted" for f in findings)
    ensure(summary["actionable_findings"] == actionable, "Actionable count mismatch")
    ensure(summary["pending_candidates"] == pending, "Candidate count mismatch")
    ensure(summary["refuted_findings"] == refuted, "Refuted count mismatch")
    ensure(set(summary["priorities"]).issubset(ids), "Unknown summary priority")
    task_ids = [task["id"] for task in report["tasks"]]
    ensure(len(task_ids) == len(set(task_ids)), "Duplicate report task")
    missing_required = any(task["required_for_verdict"] and task["status"] != "succeeded" for task in report["tasks"])
    ensure(not (report["status"] == "completed" and missing_required), "Completed report has incomplete required work")
    ensure(not (report["quality_verdict"] == "no_actionable_findings" and (missing_required or actionable)), "Unsupported clean verdict")
    if "observations" in report:
        observations = report["observations"]
        ensure(summary["raw_observations"] == len(observations), "Raw observation count mismatch")
        ensure(summary["unresolved_observations"] == sum(o["evidence_state"] != "verified_against_source_manifest" for o in observations), "Unresolved observation count mismatch")
        rows = {a["id"]: a for a in report["artifacts"]}
        for obs in observations:
            raw = obs["raw_ref"]
            ensure(raw["artifact_id"] in rows, "Unbound raw reference")
            row = rows[raw["artifact_id"]]
            ensure(row["role"] == "stdout" and row["sha256"] == raw["sha256"] and row["task_key"] == obs["task_key"], "Incorrect raw stream")
            ensure(0 <= raw["byte_start"] <= raw["byte_end"] <= row["size_bytes"], "Raw byte range invalid")
            ensure((obs["raw_message"] is not None) == report["export"]["include_code"], "Export leaked or omitted diagnostic text")
        # W19: a diagnostic the tool reported as suppressed in source never becomes a finding.
        suppressed = {o["id"] for o in observations if o.get("suppressed")}
        ensure(not any(suppressed & set(f.get("observation_ids", [])) for f in findings), "Finding built from a suppressed observation")
        if report["coverage"]["ai_review"]["status"] != "performed":
            ensure(report["status"] != "completed" and report["quality_verdict"] != "no_actionable_findings", "Incomplete AI review marked clean")
    for row in report["coverage"].get("tools", []):
        context = row.get("rule_context")
        if context is None:
            continue
        recorded, include = "config_paths" in context or "config_digest" in context, report.get("export", {}).get("include_code")
        ensure(context["config"] == "recorded" or not recorded, "Rule context records configuration it did not read")
        ensure(include is not False or not recorded, "Code-free export carries rule-context configuration")
        ensure(not (include and context["config"] == "recorded") or ("config_paths" in context and "config_digest" in context),
               "Recorded rule context without paths and digest")
    for finding in findings:
        evidence = finding["evidence"]
        evidence_ids = {item["id"] for item in evidence}
        ensure(len(evidence_ids) == len(evidence), "Duplicate evidence ID")
        for claim in finding["claims"]:
            ensure(set(claim["evidence_ids"]).issubset(evidence_ids), "Unknown claim evidence")
        for condition in finding["conditions"]:
            ensure(set(condition["evidence_ids"]).issubset(evidence_ids), "Unknown condition evidence")
            if condition["status"] == "verified":
                ensure(bool(condition["evidence_ids"]), "Verified condition needs evidence")
        ensure(set(finding["counterevidence_ids"]).issubset(evidence_ids), "Unknown counterevidence")
        for location in finding["locations"]:
            ensure(location["snapshot_id"] == report["inputs"]["snapshot_id"], "Snapshot mismatch")
            source_location(location, source_root)
        for item in evidence:
            if item["kind"] in ("code", "document"):
                ensure(item["location"]["snapshot_id"] == report["inputs"]["snapshot_id"], "Evidence snapshot mismatch")
                path = source_location(item["location"], source_root)
                digest = hashlib.sha256(path.read_bytes()).hexdigest()
                ensure(digest == item["blob_sha256"], "Evidence hash mismatch: " + str(path))


def check_analyzer(descriptor: dict) -> None:
    codes = descriptor["exit_codes"]
    merged = codes["clean"] + codes["findings"] + codes["error"]
    ensure(len(merged) == len(set(merged)), "Overlapping exit-code categories")


ABSOLUTE = re.compile(r"^(?:/|\\|[A-Za-z]:[\\/])")


def check_observations(doc: dict) -> None:
    """Cross-field rules of the Go vet observations artifact (W08)."""
    observations = doc["observations"]
    ensure(doc["parse"]["observations"] == len(observations), "Observation count mismatch")
    unresolved = sum(1 for o in observations if o["location_state"] != "resolved")
    ensure(doc["parse"]["unresolved_observations"] == unresolved, "Unresolved observation count mismatch")
    ensure(len({o["id"] for o in observations}) == len(observations), "Duplicate observation id")
    vet = [e for e in doc["executions"] if e["name"] == "go.vet"]
    for o in observations:
        ensure(bool(vet) and o["raw_ref"]["stream_sha256"] == vet[0]["stdout"]["sha256"], "Observation not bound to go.vet stdout")
        ensure(o["raw_ref"]["byte_start"] < o["raw_ref"]["byte_end"] <= vet[0]["stdout"]["captured_bytes"], "Raw byte range outside stdout")
        if o["location"] is not None:
            ensure(o["location"]["path"] == o["path"], "Location/path mismatch")
            ensure(o["location"]["start_line"] <= o["location"]["end_line"], "Inverted observation range")
            ensure(o["path"] not in doc["workspace_drift"], "Drifted file linked to code")
    outcome = doc["outcome"]
    if outcome["task_status"] == "succeeded":
        ensure(all(e["status"] == "exited" and e["exit_code"] == 0 and e["cleanup"] == "confirmed" and not e["stdout"]["truncated"] and not e["stdout"]["incomplete"] for e in doc["executions"]), "Succeeded with an unclean execution")
        ensure([e["name"] for e in doc["executions"]] == ["go.version", "go.modedit", "go.list", "go.vet"], "Succeeded without all executions")
        ensure(doc["parse"]["ok"] and doc["parse"]["unparsed_bytes"] == 0, "Succeeded without a complete parse")
        ensure(not doc["workspace_drift"], "Succeeded although the workspace drifted from the snapshot")
        ensure(sum(doc["coverage"]["packages"].get(s, 0) for s in ("analyzed_clean", "analyzed_with_findings")) > 0, "Succeeded without any analyzed package")
        ensure((outcome["exit_class"] == "success_with_findings") == bool(observations), "Exit class does not follow parsed observations")
        ensure(all(p["state"] not in ("unknown", "failed_typecheck", "load_error", "dependency_unavailable") for p in doc["inventory"]), "Succeeded with unanalyzed listed packages")
    if any(p["state"] == "unknown" for p in doc["inventory"]):
        ensure(doc["coverage"]["processed_go_files"] is None, "Processed file count claimed with unknown packages")
    states = {}
    for p in doc["inventory"]:
        states[p["state"]] = states.get(p["state"], 0) + 1
    ensure(states == doc["coverage"]["packages"], "Package state counts mismatch")
    for e in doc["executions"]:
        ensure(not any(ABSOLUTE.match(a) for a in e["command"]["argv"]) and "/" not in e["command"]["argv"][0], "Node path in command")


def check_analyzer_observations(doc: dict) -> None:
    """Cross-field rules of the generic analyzer envelope (W19, docs/39; obsdoc.Validate)."""
    observations = doc["observations"]
    parse, outcome, coverage = doc["parse"], doc["outcome"], doc["coverage"]
    ensure(parse["observations"] == len(observations), "Envelope observation count mismatch")
    ensure(parse["tool_errors"] == len(doc["tool_errors"]), "Envelope tool error count mismatch")
    ensure(parse["unresolved_observations"] == sum(1 for o in observations if o["location_state"] != "resolved"), "Envelope unresolved count mismatch")
    ensure(len({o["id"] for o in observations}) == len(observations), "Duplicate envelope observation id")
    ensure(doc["limitations"] == sorted(set(doc["limitations"])), "Envelope limitations must be sorted unique codes")
    executions = {e["name"]: e for e in doc["executions"]}
    ensure(len(executions) == len(doc["executions"]), "Duplicate envelope execution")
    for o in observations:
        ref = o["raw_ref"]
        ensure(ref["execution"] in executions, "Observation names an unrecorded execution")
        stream = executions[ref["execution"]][ref["stream"]]
        ensure(ref["stream_sha256"] == stream["sha256"], "Observation not bound to its execution stream")
        ensure(ref["byte_start"] < ref["byte_end"] <= stream["captured_bytes"], "Raw byte range outside the captured stream")
        for place in [o] + o["related"]:
            if place["location"] is not None:
                ensure(place["location"]["path"] == place["path"], "Location/path mismatch")
                ensure(place["location"]["start_line"] <= place["location"]["end_line"], "Inverted observation range")
        if o["location"] is not None:
            ensure(o["path"] not in doc["workspace_drift"], "Drifted file linked to code")
    processed = coverage["processed_paths"]
    if processed is not None:
        ensure(processed == sorted(set(processed)), "Processed paths must be sorted unique")
    # Rule context (docs/39 §5.1): config files only when the tool honors
    # repository configuration; suppressed observations only when reported.
    rule_pack = doc["rule_pack"]
    config = rule_pack["config_files"]
    ensure(rule_pack["repository_config"] == "honored" or config == [], "Config files without honored repository configuration")
    if config:
        paths = [f["path"] for f in config]
        ensure(paths == sorted(set(paths)), "Config files must be sorted unique paths")
    ensure(rule_pack["source_suppressions"] == "reported" or not any(o["suppressed"] for o in observations), "Suppressed observation without reported source suppressions")
    unreported = rule_pack["unreported_suppression_rules"]
    ensure(unreported == sorted(set(unreported)) and (rule_pack["source_suppressions"] == "reported" or unreported == []), "Unreported suppression rules must be sorted and need reported suppressions")
    active = [o for o in observations if not o["suppressed"]]
    if outcome["task_status"] == "succeeded":
        ensure((outcome["exit_class"] == "success_with_findings") == bool(active), "Exit class does not follow the active (unsuppressed) observations")
        ensure(all(e["status"] == "exited" and e["exit_code"] is not None and e["cleanup"] == "confirmed" and not any(e[s]["truncated"] or e[s]["incomplete"] for s in ("stdout", "stderr")) for e in doc["executions"]), "Succeeded with an unclean execution")
        ensure(parse["ok"] and not doc["tool_errors"] and not doc["workspace_drift"] and processed, "Succeeded without a complete parse or processed files")
    if outcome["exit_class"] == "not_run":
        ensure(not observations and processed == [] and not outcome["partial_parse"], "not_run claims processed files or observations")
    for e in doc["executions"]:
        ensure(not any(ABSOLUTE.match(a) for a in e["command"]["argv"]) and "/" not in e["command"]["argv"][0], "Node path in command")


def go_string_constant(path: Path, name: str) -> str:
    match = re.search(r"^const " + name + r' = "([^"]*)"$', path.read_text(encoding="utf-8"), re.M)
    ensure(match is not None, "Missing Go constant " + name + " in " + str(path))
    return match.group(1)


def check_w19_contracts(validators: dict) -> int:
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


def executions_exit(doc: dict, name: str):
    return next(e["exit_code"] for e in doc["executions"] if e["name"] == name)


def check_w20_rust_pack(validators: dict) -> int:
    """The cargo check and clippy descriptors and their real envelope examples (W20, docs/44).

    Returns the number of negative cases."""
    negatives = 0
    envelope_validator = validators["analyzer-observations.schema.json"]
    source = ROOT / "internal/worker/analysis/rustpack/assemble.go"

    def go_const(path: Path, name: str) -> str:
        match = re.search(r"^\s*" + name + r'\s*=\s*"([^"]*)"$', path.read_text(encoding="utf-8"), re.M)
        ensure(match is not None, "Missing Go constant " + name + " in " + str(path))
        return match.group(1)

    version = go_const(source, "RulePackVersion")
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
        ensure(rule_pack["id"] == go_const(source, rule_pack_id_const) and rule_pack["version"] == version, f"{name}: rule pack identity differs from the Go constants")
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


def check_environment_example(doc: dict) -> None:
    """Bind the W15 discovery examples to the fixture input bytes and evidence IDs."""
    root = ROOT / "fixtures/w15-modules/input"
    for r in doc.get("environment", {}).get("requirements", []):
        src = r["source"]
        sha = hashlib.sha256((root / src["path"]).read_bytes()).hexdigest()
        ensure(sha == src["sha256"], "Environment requirement blob mismatch: " + src["path"])
        binding = "\0".join([doc["snapshot_manifest_digest"], src["path"], sha, f'{src["start_line"]}:{src["end_line"]}'])
        ensure(r["evidence_id"] == "ev_" + hashlib.sha256(binding.encode()).hexdigest()[:24], "Environment evidence ID mismatch")
    for d in doc.get("documents", {}).get("items", []):
        if d["sha256"] is not None:
            body = (root / d["path"]).read_bytes()
            ensure(hashlib.sha256(body).hexdigest() == d["sha256"] and len(body) == d["size_bytes"], "Document blob mismatch: " + d["path"])


def check_review_example(doc: dict) -> None:
    """Bind the verifier example to its labelled, synthetic source fixture."""
    root = ROOT / "internal/worker/review/testdata/verify"
    catalog = {}
    for e in doc["evidence"]:
        path = PurePosixPath(e["path"])
        ensure(not path.is_absolute() and ".." not in path.parts, "Invalid review example path")
        file = (root / path).resolve()
        ensure(file.is_relative_to(root) and file.is_file(), "Review evidence file is missing")
        body = file.read_bytes()
        start, end = e["start_line"], e["end_line"]
        ensure(1 <= start <= end <= len(body.splitlines()), "Review evidence range outside file")
        sha = hashlib.sha256(body).hexdigest()
        ensure(sha == e["sha256"], "Review evidence blob mismatch")
        binding = "\0".join([doc["snapshot_manifest_digest"], e["path"], sha, f"{start}:{end}"])
        expected_id = "ev_" + hashlib.sha256(binding.encode()).hexdigest()[:24]
        ensure(e["id"] == expected_id and expected_id not in catalog, "Review evidence ID mismatch")
        catalog[expected_id] = e
    candidates = [c["id"] for c in doc["candidates"]]
    ensure(candidates == [v["candidate_id"] for v in doc["verifications"]], "Verification coverage mismatch")
    for item in doc["facts"] + doc["candidates"] + doc["verifications"]:
        for key in ("evidence_ids", "supporting_evidence_ids", "counterevidence_ids"):
            ensure(all(ref in catalog for ref in item.get(key, [])), "Unknown review evidence")
    verdict = doc["verifications"][0]
    callers = [catalog[ref] for ref in verdict["counterevidence_ids"]]
    ensure(verdict["verdict"] == "refuted" and any(e["path"] == "load.go" and e.get("origin") == "caller" for e in callers), "Missing fixture counterexample")


R1_STAGES = ("environment", "documents", "investigate", "interpret", "verify")


def compact_json(text: str) -> bytes:
    """Remove the whitespace json.Indent added outside strings; the result is
    the exact record bytes the worker wrote (escapes are kept as written)."""
    out, in_string, escaped = [], False, False
    for ch in text:
        if in_string:
            out.append(ch)
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
        elif ch == '"':
            in_string = True
            out.append(ch)
        elif ch not in " \t\r\n":
            out.append(ch)
    return "".join(out).encode("utf-8")


def r1_pack() -> tuple[dict, dict]:
    """The review-r1 candidate pack: identity, files and per-role digests
    (review-v1 algorithm over pack.yaml and the role's selected files)."""
    pack_file = ROOT / "prompts/review-r1/pack.yaml"
    pack = yaml_file(pack_file)
    ensure(pack["id"] == "review-r1" and pack["status"] == "candidate-not-promoted" and pack["schema_version"] == "0.2", "review-r1 pack identity")
    ensure(tuple(pack["roles"]) == R1_STAGES, "review-r1 roles")
    roles = {}
    for role, file in pack["roles"].items():
        names = sorted({"pack.yaml", pack["core"], file, pack["domains"], *pack["languages"].values()})
        digest = hashlib.sha256()
        for relative in names:
            path = within_root(pack_file.parent / relative)
            ensure(path.is_file(), "Missing review-r1 prompt: " + relative)
            digest.update(relative.encode("utf-8") + b"\0" + path.read_bytes() + b"\0")
        roles[role] = {"files": names, "digest": digest.hexdigest()}
    v1 = ROOT / "prompts/review-v1"
    for shared in [pack["domains"], *pack["languages"].values()]:
        ensure((pack_file.parent / shared).read_bytes() == (v1 / shared).read_bytes(), "review-r1 copy differs from review-v1: " + shared)
    return pack, roles


def check_review_r1_examples(docs: dict, raw: dict, pack: dict, roles: dict) -> None:
    """Bind the review-r1 stage examples to the w15 fixture bytes, the pack
    files, each other (upstream SHA-256, candidates) and the scope rules."""
    root = ROOT / "fixtures/w15-modules/input"
    first = docs["environment"]
    for stage in R1_STAGES:
        doc = docs[stage]
        ensure(doc["stage"] == stage and doc["pack"] == {"id": pack["id"], "version": pack["version"]}, "review-r1 stage or pack: " + stage)
        ensure(all(doc[k] == first[k] for k in ("snapshot_manifest_digest", "run_id", "attempt_id")), "review-r1 examples of different attempts")
        catalog = {}
        for e in doc["evidence"]:
            body = (root / e["path"]).read_bytes()
            sha = hashlib.sha256(body).hexdigest()
            ensure(sha == e["sha256"] and 1 <= e["start_line"] <= e["end_line"] <= len(body.splitlines()), "review-r1 evidence blob or range: " + e["path"])
            binding = "\0".join([doc["snapshot_manifest_digest"], e["path"], sha, f'{e["start_line"]}:{e["end_line"]}'])
            ensure(e["id"] == "ev_" + hashlib.sha256(binding.encode()).hexdigest()[:24] and e["id"] not in catalog, "review-r1 evidence ID")
            catalog[e["id"]] = e
        cited = []
        for key in ("environment_notes", "contracts", "conflicts", "questions", "instruction_notes", "candidates", "verifications"):
            for item in doc.get(key, []):
                for field in ("evidence_ids", "first_evidence_ids", "second_evidence_ids", "supporting_evidence_ids", "counterevidence_ids"):
                    cited.extend(item.get(field, []))
        ensure(all(ref in catalog for ref in cited), "review-r1 cites evidence it was not given: " + stage)
        # A rule of a sub-directory is never generalized to a parent scope.
        def covers(ids, scope):
            dirs = [str(PurePosixPath(catalog[i]["path"]).parent) for i in ids]
            return all(d == "." or scope == d or scope.startswith(d + "/") for d in dirs)
        for c in doc.get("contracts", []) + doc.get("questions", []):
            ensure(covers(c["evidence_ids"], c["applies_to"]), "review-r1 scope generalized: " + c["applies_to"])
        for c in doc.get("conflicts", []):
            ensure(covers(c["first_evidence_ids"] + c["second_evidence_ids"], c["scope"]), "review-r1 conflict scope generalized")
        observations = {o["id"] for o in doc["observations"]}
        for c in doc.get("candidates", []):
            ensure(set(c.get("observation_ids", [])) <= observations, "review-r1 candidate cites an unknown observation")
        for call in doc["calls"]:
            ensure(call["role"] == stage and call["pack_files"] == roles[stage]["files"] and call["pack_sha256"] == roles[stage]["digest"], "review-r1 call pack digest: " + stage)
            ensure(call["pack_id"] == pack["id"] and call["pack_version"] == pack["version"], "review-r1 call pack identity")
        for u in doc["upstream"]:
            if u["state"] == "accepted" and u["source"].startswith("review."):
                sibling = u["source"][len("review."):]
                ensure(hashlib.sha256(compact_json(raw[sibling])).hexdigest() == u["sha256"], "review-r1 upstream digest: " + u["source"])
    verify = docs["verify"]
    upstream = docs["investigate"].get("candidates", []) + docs["interpret"].get("candidates", [])
    ensure(verify.get("candidates", []) == upstream, "verify candidates differ from the accepted upstream candidates")
    ensure([c["id"] for c in verify.get("candidates", [])] == [v["candidate_id"] for v in verify.get("verifications", [])], "review-r1 verification coverage")


SERVER_STAGES = ("source", "report")


def stage_vocabulary() -> tuple[list[str], list[str], list[str]]:
    """Task stage keys in domain.TaskStages order, the migration 0006 CHECK
    values, and the progress.State values (W15, docs/37 §2)."""
    go = (ROOT / "internal/domain/stage.go").read_text(encoding="utf-8")
    consts = dict(re.findall(r'^\s*(Stage\w+)\s+Stage\s*=\s*"([a-z_]+)"', go, re.M))
    listed = re.search(r"var TaskStages = \[\]Stage\{([^}]*)\}", go)
    ensure(bool(consts) and listed is not None, "domain stage constants not found")
    order = [consts[name.strip()] for name in listed.group(1).split(",") if name.strip()]
    ensure(sorted(order) == sorted(consts.values()) and len(order) == len(set(order)), "TaskStages must list every stage once")
    migrations = sorted((ROOT / "migrations/sql").glob("0006_*.sql"))
    ensure(len(migrations) == 1, "Missing migration 0006")
    check = re.search(r"stage IN \(([^)]*)\)", migrations[0].read_text(encoding="utf-8"))
    ensure(check is not None, "Migration 0006 has no stage CHECK")
    stored = re.findall(r"'([a-z_]+)'", check.group(1))
    progress = (ROOT / "internal/progress/progress.go").read_text(encoding="utf-8")
    states = re.findall(r'^\s*\w+\s+State\s*=\s*"([a-z_]+)"', progress, re.M)
    return order, stored, states


def check_stage_enums(api: dict, order: list[str], stored: list[str], states: list[str]) -> None:
    """The OpenAPI stage vocabulary equals the Go domain, the 0006 CHECK and
    the progress states; StageKey is source, task stages in order, report."""
    ensure(sorted(api["TaskStage"]["enum"]) == sorted(order), "Stage enum mismatch: OpenAPI/domain")
    ensure(sorted(stored) == sorted(order), "Stage enum mismatch: migration 0006/domain")
    ensure(api["StageKey"]["enum"] == [SERVER_STAGES[0], *order, SERVER_STAGES[1]], "StageKey enum/order mismatch")
    ensure(sorted(api["StageState"]["enum"]) == sorted(states) and len(states) == len(set(states)), "Stage state enum mismatch: OpenAPI/progress")
    ensure(api["StageDetail"]["enum"] == ["recorded", "not_recorded"], "Stage detail enum mismatch")


def check_stages(detail, stages: list, order: list[str]) -> None:
    """Cross-field rules of a W15 stage list (project activity or run progress)."""
    if detail != "recorded":
        ensure(stages == [], "Unrecorded run lists stages")
        return
    keys = [s["key"] for s in stages]
    ensure(len(keys) >= 2 and keys[0] == SERVER_STAGES[0] and keys[-1] == SERVER_STAGES[1], "Stages must start with source and end with report")
    middle = keys[1:-1]
    ensure(middle == [k for k in order if k in middle], "Task stages out of order or repeated")
    ensure(all(k in middle for k in order if k not in ("build", "test")), "Missing task stage")
    for s in stages:
        ensure(s["reasons"] == sorted(set(s["reasons"])), "Stage reasons must be sorted and distinct")
        ensure(s["finished"] == s["succeeded"] + sum(s["counts"].values()), "Stage finished count mismatch")
        if s["key"] in SERVER_STAGES:
            ensure(s["planned"] is None and s["finished"] == 0, "Server-derived stage carries task counts")
            continue
        planned = s["planned"]
        if s["state"] == "plan_pending":
            ensure(planned is None and s["finished"] == 0, "Pending plan with a denominator")
        if s["state"] == "no_tasks":
            ensure(planned == 0, "Empty stage with planned tasks")
        if planned is not None:
            ensure(s["finished"] <= planned, "More finished than planned")
        if s["state"] in ("waiting", "running", "plan_pending", "no_tasks"):
            ensure(s["finished_at"] is None, "Open stage has an end time")
        if s["state"] == "succeeded":
            ensure(not s["counts"] and (planned is None or s["succeeded"] == planned), "Succeeded stage with failures")


def check_activity(doc: dict, order: list[str]) -> None:
    """Cross-field rules of a project activity (W15, docs/37 §4)."""
    ensure((doc["run"] is None) == (doc["detail"] is None), "Activity detail must be null exactly without a run")
    if doc["run"] is None:
        ensure(doc["stages"] == [] and doc["other_active_runs"] == 0 and doc["waiting_reason"] is None and doc["last_event_at"] is None, "Activity data without a run")
        return
    ensure(doc["waiting_reason"] is None or doc["run"]["status"] == "queued", "Waiting reason for a run that is not queued")
    check_stages(doc["detail"], doc["stages"], order)


def api_validator(name: str) -> Draft202012Validator:
    """A JSON Schema validator for components/schemas/<name> of api/openapi.yaml."""
    document = yaml_file(ROOT / "api/openapi.yaml")
    root = {"$schema": "https://json-schema.org/draft/2020-12/schema", "$ref": "#/components/schemas/" + name, "components": document["components"]}
    Draft202012Validator.check_schema(root)
    return Draft202012Validator(root, format_checker=FormatChecker())


def check_w22_contracts() -> None:
    """W22 schedules, plan revision/weighted progress and the run event stream.

    Examples validate against OpenAPI; enums and bounds agree across OpenAPI,
    migration 0007 and the Go constants; derived progress fields are
    recomputed; the SSE example has contiguous ids, allowlisted payload fields
    and ends with the RunView example. Plus negative cases.
    """
    api = yaml_file(ROOT / "api/openapi.yaml")["components"]["schemas"]
    migration = (ROOT / "migrations/sql/0007_schedules_and_plan_progress.sql").read_text(encoding="utf-8")
    go = lambda name: (ROOT / name).read_text(encoding="utf-8")
    consts = lambda text, prefix: {v for _, v in re.findall(r"\b" + prefix + r"([A-Z]\w*)\s*=\s*\"([a-z_]+)\"", text)}
    stored = lambda column: set(re.findall(r"'([a-z_]+)'", re.search(column + r"\s+text\s+NOT NULL CHECK \(" + column + r" IN \(([^)]*)\)\)", migration).group(1)))
    cron, schedules = go("internal/schedule/cron.go"), go("internal/storage/postgres/schedules.go")
    sse, progress = go("internal/httpapi/sse.go"), go("internal/progress/progress.go")
    for column, name, prefix in (("overlap", "ScheduleOverlap", "Overlap"), ("misfire", "ScheduleMisfire", "Misfire")):
        ensure(set(api[name]["enum"]) == stored(column) == consts(cron, prefix), "Schedule " + column + " enum mismatch: OpenAPI/migration 0007/internal/schedule")
    states = set(api["ScheduleOccurrence"]["properties"]["state"]["enum"]) | {"pending"}
    ensure(states == stored("state") == consts(schedules, "Occurrence"), "Occurrence state enum mismatch: OpenAPI/migration 0007/store")
    reasons = api["ScheduleOccurrence"]["properties"]["reason"]["oneOf"][1]["enum"]
    ensure(set(reasons) == consts(schedules, "Reason"), "Occurrence reason enum mismatch: OpenAPI/store")
    ensure(set(api["StreamControl"]["properties"]["reason"]["enum"]) == consts(sse, "Resync") | consts(sse, "Closed"), "Stream reason enum mismatch: OpenAPI/sse.go")
    ensure(api["RunView"]["properties"]["progress"]["properties"]["weight_basis"]["oneOf"][1]["enum"] == sorted(consts(progress, "Basis"), key=["task_count", "declared", "mixed"].index), "Weight basis enum mismatch: OpenAPI/progress")
    allowed = set(re.findall(r'"(\w+)": true', re.search(r"var eventFields = map\[string\]bool\{(.*?)\n\}", sse, re.S).group(1)))
    ensure(allowed == set(api["RunEventData"]["x-codeye-payload-fields"]), "SSE payload allowlist mismatch: OpenAPI/sse.go")
    events = yaml_file(ROOT / "api/openapi.yaml")["paths"]["/api/v1/attempts/{attempt_id}/events"]["post"]
    payloads = events["requestBody"]["content"]["application/json"]["schema"]["properties"]["payload"]["oneOf"]
    declared = [p["properties"]["weight"] for p in payloads if "weight" in p.get("properties", {})]
    bounds = re.search(r"weight IS NULL OR weight BETWEEN (\d+) AND (\d+)", migration)
    go_bounds = re.search(r"func validWeight\(w int\) bool \{ return w >= (\d+) && w <= (\d+) \}", go("internal/storage/postgres/evidence.go"))
    ensure(len(declared) == 1 and bounds is not None and go_bounds is not None, "Task weight bound definitions not found")
    low, high = int(bounds.group(1)), int(bounds.group(2))
    ensure((low, high) == (int(go_bounds.group(1)), int(go_bounds.group(2))) == (declared[0]["minimum"], declared[0]["maximum"])
           == (api["PlanChange"]["properties"]["weight"]["minimum"], api["PlanChange"]["properties"]["weight"]["maximum"]), "Task weight bounds mismatch")

    schedule_validator, run_validator = api_validator("Schedule"), api_validator("RunView")
    row_validator, control_validator = api_validator("RunEventData"), api_validator("StreamControl")
    listing = json_file(ROOT / "examples/schedules.json")
    run = json_file(ROOT / "examples/run-progress.json")

    def check_schedules(doc: dict) -> None:
        ensure(set(doc) == {"items", "can_manage"} and isinstance(doc["can_manage"], bool), "Schedule list shape")
        for item in doc["items"]:
            schedule_validator.validate(item)
            ensure(item["enabled"] or item["next_run_at"] is None, "Disabled schedule with a next occurrence")
            last = item["last_occurrence"]
            if last is not None:
                ensure((last["state"] == "enqueued") == (last["run_id"] is not None) == (last["reason"] is None), "Occurrence run/reason mismatch")
                ensure(last["missed_from"] is None or last["missed_from"] < last["scheduled_for"], "missed_from must precede the occurrence")
                ensure(item["next_run_at"] is None or item["next_run_at"] > last["scheduled_for"], "Next occurrence is not after the last one")
        ensure(len({i["id"] for i in doc["items"]}) == len(doc["items"]), "Duplicate schedule")

    def check_progress(doc: dict) -> None:
        run_validator.validate(doc)
        p = doc["progress"]
        derived = (p["estimated_percent"], p["planned_weight"], p["finished_weight"], p["weight_basis"])
        if p["plan_revision"] is None:
            ensure(derived == (None, None, None, None) and p["plan_changes"] == [], "Plan fields before the plan is fixed")
            return
        ensure(p["plan_revision"] == 1 + len(p["plan_changes"]), "Plan revision does not match plan changes")
        tasks = [s for s in p["stages"] if s["planned"] is not None]
        ensure(p["total_tasks"] == sum(s["planned"] for s in tasks) and p["completed_tasks"] == sum(s["finished"] for s in tasks), "Task counts mismatch: progress/stages")
        planned = p["planned_weight"] - sum(c["weight"] for c in p["plan_changes"])
        for revision, change in enumerate(p["plan_changes"], start=2):
            planned += change["weight"]
            ensure(change["revision"] == revision and change["planned_weight"] == planned, "Plan change sequence drift")
        ensure(planned == p["planned_weight"] and 0 <= p["finished_weight"] <= planned, "Planned weight drift")
        ensure(p["weight_basis"] != "task_count" or planned == p["total_tasks"], "task_count basis with declared weights")
        expected = None if planned == 0 else int(p["finished_weight"] * 1000 / planned + 0.5) / 10
        ensure(p["estimated_percent"] == expected, "Estimated percent is not finished/planned weight")

    def check_stream(text: str) -> None:
        messages = []
        for block in text.split("\n\n"):
            fields = dict(line.split(": ", 1) for line in block.strip("\n").split("\n") if line)
            if "data" in fields:
                messages.append((fields.get("id"), fields.get("event"), json.loads(fields["data"])))
        ensure(text.startswith("retry: ") and messages, "Stream example shape")
        cursor, version, view = None, 0, None
        for seq, event, data in messages:
            ensure(data.get("id" if event == "run.view" else "run_id") == run["id"], "Stream message for another run")
            if event is None:
                row_validator.validate(data)
                ensure(seq is not None and int(seq) == data["sequence"] and (cursor is None or data["sequence"] == cursor + 1), "Event ids not contiguous")
                ensure(set(data) - {"run_id", "sequence", "version", "type", "at"} <= allowed, "Event row carries a field outside the allowlist")
                ensure(data["version"] >= version, "Run version decreased")
                cursor, version = data["sequence"], data["version"]
                continue
            ensure(seq is None, "Control message moves the cursor")
            if event == "run.view":
                check_progress(data)
                ensure(cursor is not None and data["event_sequence"] >= cursor and data["version"] >= version, "Run view older than the delivered events")
                view = data
            else:
                ensure(event in ("heartbeat", "state.resync", "closed"), "Unknown stream event: " + event)
                control_validator.validate(data)
                ensure((event == "heartbeat") == ("reason" not in data), "Heartbeat/reason mismatch")
        ensure(view == run, "Stream run.view differs from examples/run-progress.json")

    stream = (ROOT / "examples/run-events.txt").read_text(encoding="utf-8")
    check_schedules(listing)
    check_progress(run)
    check_stream(stream)
    trigger = run["trigger"]
    linked = [i for i in listing["items"] if i["id"] == trigger.get("schedule_id")]
    ensure(trigger["kind"] == "schedule" and len(linked) == 1 and linked[0]["last_occurrence"]["run_id"] == run["id"]
           and linked[0]["last_occurrence"]["scheduled_for"] == trigger["scheduled_for"], "Scheduled run is not the schedule's last occurrence")

    negatives = 0

    def reject(name: str, function, data) -> None:
        nonlocal negatives
        expect_invalid(name, function, data)
        negatives += 1

    bad = copy.deepcopy(run)
    bad["progress"]["plan_revision"] = 3
    reject("plan revision drift", check_progress, bad)
    bad = copy.deepcopy(run)
    bad["progress"]["estimated_percent"] = 30.0
    reject("percent not from weights", check_progress, bad)
    bad = copy.deepcopy(run)
    bad["progress"]["plan_changes"][0]["reason"] = "heartbeat"
    reject("plan change from a heartbeat", check_progress, bad)
    bad = copy.deepcopy(listing)
    bad["items"][0]["misfire"] = "catch_up"
    reject("unbounded catch-up policy", check_schedules, bad)
    bad = copy.deepcopy(listing)
    bad["items"][1]["next_run_at"] = "2026-11-19T07:30:00Z"
    reject("disabled schedule with next occurrence", check_schedules, bad)
    reject("gap in event ids", check_stream, stream.replace("id: 22\n", "id: 23\n").replace('"sequence":22,', '"sequence":23,'))
    reject("node identity in event row", check_stream, stream.replace('"status":"failed",', '"status":"failed","node_id":"n1",'))
    reject("control message with id", check_stream, stream.replace("event: heartbeat\n", "id: 23\nevent: heartbeat\n"))
    print(f"PASS: W22 schedule/progress/stream contracts (3 examples; overlap/misfire/occurrence/reason/stream/weight enums and bounds; {negatives} negative cases)")


def check_w23_contracts(published: dict) -> None:
    """W23 run comparison and human finding reviews (docs/40).

    Examples validate against OpenAPI; disposition/state/reason/match enums
    agree across OpenAPI, migration 0008, the report schema and the Go
    constants; counts are recomputed from entries, summaries from findings;
    a resolved entry never comes from a model and an absent one always has a
    reason. Plus negative cases.
    """
    api = yaml_file(ROOT / "api/openapi.yaml")["components"]["schemas"]
    migration = (ROOT / "migrations/sql/0008_finding_reviews.sql").read_text(encoding="utf-8")
    go = lambda name: (ROOT / name).read_text(encoding="utf-8")
    consts = lambda text, prefix: {v for _, v in re.findall(r"\b" + prefix + r"(\w*)\s*=\s*\"([a-z_]+)\"", text)}
    compare, reviews = go("internal/history/compare.go"), go("internal/history/reviews.go")
    stored = set(re.findall(r"'([a-z_]+)'", re.search(r"disposition\s+text\s+NOT NULL CHECK \(disposition IN \(([^)]*)\)\)", migration).group(1)))
    finding_schema = json_file(ROOT / "schemas/finding.schema.json")
    validate_go = go("internal/report/validate.go")
    go_dispositions = set(re.findall(r'"([a-z_]+)": true', re.search(r"dispositions\s*=\s*map\[string\]bool\{([^}]*)\}", validate_go).group(1)))
    ensure(set(api["Disposition"]["enum"]) == stored == set(finding_schema["properties"]["disposition"]["enum"]) == go_dispositions,
           "Disposition enum mismatch: OpenAPI/migration 0008/finding schema/report")
    ensure(api["ComparisonState"]["enum"] == [v for v in re.findall(r"State\w+\s*=\s*\"([a-z_]+)\"", compare)], "Comparison state enum mismatch: OpenAPI/history")
    reasons = api["ComparisonEntry"]["properties"]["reason"]["oneOf"][1]["enum"]
    ensure(set(reasons) == consts(compare, "Reason"), "Comparison reason enum mismatch: OpenAPI/history")
    ensure(set(api["RunComparison"]["properties"]["not_comparable_reason"]["oneOf"][1]["enum"]) == consts(compare, "NotComparable"), "Not-comparable reason mismatch: OpenAPI/history")
    matches = api["RunComparison"]["properties"]["findings"]["items"]["properties"]["review_match"]["enum"]
    ensure(set(matches) == consts(reviews, "Match"), "Review match enum mismatch: OpenAPI/history")
    ensure(set(api["RunComparison"]["properties"]["counts"]["required"]) == set(api["ComparisonState"]["enum"]), "Counts keys are not the comparison states")

    comparison_validator, review_validator = api_validator("RunComparison"), api_validator("FindingReview")
    comparison = json_file(ROOT / "examples/comparison.json")
    listing = json_file(ROOT / "examples/finding-reviews.json")
    hidden = {"accepted_risk", "false_positive", "suppressed"}

    def check_comparison(doc: dict) -> None:
        comparison_validator.validate(doc)
        ensure((doc["baseline"] is None) == (doc["not_comparable_reason"] == "no_baseline"), "Baseline/no_baseline mismatch")
        ensure(doc["comparable"] == (doc["not_comparable_reason"] is None), "comparable/not_comparable_reason mismatch")
        # Differences stay visible when the schemes differ (builder_version tells why); entries do not.
        ensure(doc["comparable"] or doc["entries"] == [], "Entries in a comparison that is not comparable")
        ensure(doc["baseline"] is not None or doc["differences"] == [], "Differences without a baseline")
        states = [e["state"] for e in doc["entries"]]
        ensure(doc["counts"] == {s: states.count(s) for s in api["ComparisonState"]["enum"]}, "Counts are not recomputed from entries")
        ensure(len({e["fingerprint"] for e in doc["entries"]}) == len(doc["entries"]), "Duplicate comparison entry")
        by_fp = {e["fingerprint"]: e for e in doc["entries"]}
        # W19: a resolved tool entry needs the same tool version and rule context on both sides (docs/40 §3.3).
        rule_keys = {d["key"] for d in doc["differences"]}
        for e in doc["entries"]:
            ensure(e["state"] != "resolved_observed" or (e["producer"] == "tool" and e["current"] == [] and e["baseline"] and e["reason"] is None), "Resolved entry not observed by a tool")
            ensure(e["state"] != "resolved_observed" or not ({"tool:" + e["analyzer"] + ":version", "tool:" + e["analyzer"] + ":rule_context"} & rule_keys),
                   "Resolved entry despite a tool version or rule-context difference")
            ensure(e["state"] != "absent_unverified" or (e["current"] == [] and e["baseline"] and e["reason"] is not None), "Absent entry without a reason")
            ensure(e["state"] != "new" or (e["baseline"] == [] and e["current"]), "New entry with a baseline occurrence")
            ensure(e["state"] not in ("persisting", "changed") or (e["current"] and e["baseline"] and e["reason"] is None), "Matched entry missing a side")
            ensure((e["state"] == "changed") == bool(set(e["changes"]) - {"lines"}) or e["state"] not in ("persisting", "changed"), "changed/persisting disagree with changes")
            ensure(not e["ambiguous"] or max(len(e["current"]), len(e["baseline"])) > 1, "Ambiguous entry with single occurrences")
            ensure(all(m in by_fp and m != e["fingerprint"] for m in e["move_candidates"]), "Move candidate is not another entry")
        ids = [f["finding_id"] for f in doc["findings"]]
        ensure(len(set(ids)) == len(ids), "Duplicate finding in overlay")
        current = {o["finding_id"]: e for e in doc["entries"] for o in e["current"]}
        for f in doc["findings"]:
            e = current.get(f["finding_id"])
            ensure(e is None or (f["fingerprint"] == e["fingerprint"] and f["state"] == e["state"]), "Overlay state differs from its entry")
            ensure(doc["comparable"] or f["state"] is None, "State on a finding of a comparison that is not comparable")
            r, m = f["review"], f["review_match"]
            ensure((r is None) == (m == "none"), "Review/match mismatch")
            if m == "applied":
                ensure(f["current_disposition"] == r["disposition"] and r["disposition"] != "open" and not r["expired"], "Applied review not in effect")
            elif m != "none":
                ensure(f["current_disposition"] == "open", "Unapplied review changed the current disposition")
            ensure(m != "expired" or r["expired"], "Expired match on an unexpired review")
        for key in ("at_publication", "current"):
            field = "publication_disposition" if key == "at_publication" else "current_disposition"
            n = sum(f[field] in hidden for f in doc["findings"])
            ensure(doc["summary"][key]["hidden_findings"] == n and doc["summary"][key]["total_findings"] == len(doc["findings"]), "Summary " + key + " not from findings")

    def check_reviews(doc: dict) -> None:
        ensure(set(doc) == {"items", "next_cursor", "can_review"}, "Finding review list shape")
        versions = {}
        for item in doc["items"]:
            review_validator.validate(item)
            ensure(item["reason"].strip() != "", "Blank review reason")
            ensure(item["disposition"] != "open" or (item["expires_at"] is None and item["path_prefix"] is None), "Open review with expiry or path scope")
            ensure(item["expires_at"] is None or item["expires_at"] > item["created_at"], "Review expiry before it was recorded")
            versions.setdefault(item["fingerprint"], []).append(item["version"])
        ensure(all(sorted(v, reverse=True) == v and len(set(v)) == len(v) for v in versions.values()), "Review versions not newest first")

    check_comparison(comparison)
    check_reviews(listing)
    applied = [f for f in comparison["findings"] if f["review_match"] == "applied"]
    ensure(applied and all(f["review"]["version"] == max(i["version"] for i in listing["items"] if i["fingerprint"] == f["fingerprint"]) for f in applied),
           "Applied review is not the latest version of examples/finding-reviews.json")
    ensure(all("fingerprint" not in f and "fingerprint_basis" not in f for f in published["findings"]), "Code-free export carries finding identity")

    negatives = 0

    def reject(name: str, function, data) -> None:
        nonlocal negatives
        expect_invalid(name, function, data)
        negatives += 1

    resolved = next(i for i, e in enumerate(comparison["entries"]) if e["state"] == "resolved_observed")
    bad = copy.deepcopy(comparison)
    bad["entries"][resolved]["producer"] = "model"
    reject("model candidate resolved", check_comparison, bad)
    bad = copy.deepcopy(comparison)
    bad["entries"][resolved]["state"] = "absent_unverified"
    reject("absent without reason", check_comparison, bad)
    bad = copy.deepcopy(comparison)
    bad["counts"]["persisting"] += 1
    reject("counts not from entries", check_comparison, bad)
    bad = copy.deepcopy(comparison)
    bad["summary"]["current"]["hidden_findings"] = 0
    reject("hidden count not from findings", check_comparison, bad)
    bad = copy.deepcopy(comparison)
    i = next(i for i, f in enumerate(bad["findings"]) if f["review_match"] == "applied")
    bad["findings"][i]["review_match"] = "candidate"
    reject("candidate match hides a finding", check_comparison, bad)
    bad = copy.deepcopy(comparison)
    bad["findings"][0]["publication_disposition"] = "ignored"
    reject("unknown disposition", check_comparison, bad)
    analyzer = comparison["entries"][resolved]["analyzer"]
    for what, baseline, current in (("rule_context", "codeye-go-vet-default@1 config=none suppressions=none", "unknown"), ("version", "go1.27.4", "go1.28.0")):
        bad = copy.deepcopy(comparison)
        bad["differences"].append({"key": "tool:" + analyzer + ":" + what, "baseline": baseline, "current": current})
        reject("resolved despite a " + what + " difference", check_comparison, bad)
    bad = copy.deepcopy(listing)
    bad["items"][0]["reason"] = "   "
    reject("blank review reason", check_reviews, bad)
    bad = copy.deepcopy(listing)
    bad["items"][0]["fingerprint"] = "go-vet-fp-v1:1234"
    reject("review of a non-fp2 fingerprint", check_reviews, bad)
    print(f"PASS: W23 comparison/review contracts (2 examples; disposition/state/reason/match enums across OpenAPI, migration 0008, schema and Go; {negatives} negative cases)")


def resolve_pointer(document, pointer: str):
    if not pointer:
        return document
    ensure(pointer.startswith("/"), "Unsupported reference fragment: " + pointer)
    value = document
    for token in pointer[1:].split("/"):
        token = token.replace("~1", "/").replace("~0", "~")
        value = value[int(token)] if isinstance(value, list) else value[token]
    return value


def load_document(path: Path):
    return json_file(path) if path.suffix == ".json" else yaml_file(path)


def resolve_ref(file: Path, reference: str):
    target, fragment = urldefrag(reference)
    ensure(not re.match(r"https?://", target), "Unexpected remote $ref: " + reference)
    target_file = within_root(file.parent / unquote(target)) if target else file
    ensure(target_file.is_file(), "Missing referenced file: " + str(target_file))
    return resolve_pointer(load_document(target_file), unquote(fragment))


def check_openapi(file: Path) -> int:
    document = yaml_file(file)
    ensure(document["openapi"] == "3.1.0", "Unexpected OpenAPI version")
    refs = 0
    def walk(value):
        nonlocal refs
        if isinstance(value, dict):
            if "$ref" in value:
                resolve_ref(file, value["$ref"])
                refs += 1
            for child in value.values():
                walk(child)
        elif isinstance(value, list):
            for child in value:
                walk(child)
    walk(document)
    operation_ids = set()
    methods = {"get", "put", "post", "delete", "patch", "options", "head", "trace"}
    for route, item in document["paths"].items():
        needed = set(re.findall(r"\{([^}]+)\}", route))
        for method, operation in item.items():
            if method not in methods:
                continue
            op_id = operation["operationId"]
            ensure(op_id not in operation_ids, "Duplicate operationId")
            operation_ids.add(op_id)
            parameters = item.get("parameters", []) + operation.get("parameters", [])
            parameters = [resolve_ref(file, p["$ref"]) if "$ref" in p else p for p in parameters]
            path_parameters = {p["name"] for p in parameters if p.get("in") == "path" and p.get("required")}
            ensure(needed == path_parameters, "Path parameter mismatch: " + route)
            ensure(bool(operation.get("responses")), "Missing responses: " + op_id)
    return refs


def expect_invalid(name: str, function, data) -> None:
    try:
        function(data)
    except (ValueError, ValidationError):
        return
    raise ValueError("Negative case was incorrectly accepted: " + name)


def make_validators():
    schemas = {}
    resources = []
    for path in sorted((ROOT / "schemas").glob("*.json")):
        schema = json_file(path)
        Draft202012Validator.check_schema(schema)
        schemas[path.name] = schema
        resources.append((schema["$id"], Resource.from_contents(schema)))
    registry = Registry().with_resources(resources)
    return {name: Draft202012Validator(schema, registry=registry, format_checker=FormatChecker()) for name, schema in schemas.items()}


def check_r1_corpus() -> None:
    """W24 보류 corpus의 구조·위치·digest·누설을 도구 없이 확인하고 봉인 digest와 대조한다."""
    import subprocess

    corpus = ROOT / "fixtures/r1-quality/corpus.py"
    env = {"PATH": os.environ.get("PATH", ""), "LANG": "C.UTF-8"}
    result = subprocess.run([sys.executable, "-I", str(corpus), "validate"], capture_output=True, text=True, timeout=300, env=env)
    tail = " | ".join((result.stdout + result.stderr).strip().splitlines()[-5:])
    ensure(result.returncode == 0, "R1 corpus validate failed: " + tail)
    out = subprocess.run([sys.executable, "-I", str(corpus), "digest"], capture_output=True, text=True, timeout=300, env=env)
    ensure(out.returncode == 0 and out.stdout.split(), "R1 corpus digest failed")
    sealed = "ab776f5f68cf34ece0dd32b0f18dab962d07fad1131a35c32a6149eebc045fe4"
    ensure(out.stdout.split()[-1] == sealed, "R1 holdout corpus digest differs from the sealed digest; a changed holdout needs a new seal recorded in docs/16")
    print(f"PASS: W24 R1 holdout corpus (structure, locations, digests, leakage scan; sealed digest {sealed[:12]})")


def main() -> None:
    link_count = 0
    ignored = {".git", ".codeye", ".venv", "node_modules", "dist", "bin", "coverage", "__pycache__"}
    markdown_files = []
    for directory, dirs, files in os.walk(ROOT):
        dirs[:] = sorted(name for name in dirs if name not in ignored)
        markdown_files.extend(Path(directory) / name for name in sorted(files) if name.endswith(".md"))
    for path in markdown_files:
        for match in re.finditer(r"\[[^\]]*\]\(([^)]+)\)", path.read_text(encoding="utf-8")):
            target = match.group(1).strip().strip("<>")
            if re.match(r"(?:https?://|mailto:|#)", target):
                continue
            target = unquote(target.split("#", 1)[0])
            if not target:
                continue
            ensure(within_root(path.parent / target).exists(), "Broken Markdown link in " + str(path) + ": " + target)
            link_count += 1
    validators = make_validators()
    validators["doctor.schema.json"].validate(json_file(ROOT / "examples/doctor.json"))
    validators["state.schema.json"].validate(json_file(ROOT / "examples/state.json"))
    state = validators["state.schema.json"].schema["properties"]
    api = yaml_file(ROOT / "api/openapi.yaml")["components"]["schemas"]
    for field, name in (("run_status", "RunStatus"), ("attempt_status", "AttemptStatus"), ("cleanup_state", "CleanupState")):
        ensure(set(state[field]["enum"]) == set(api[name]["enum"]), "State/API enum mismatch: " + field)
    stage_order, stored_stages, stage_states = stage_vocabulary()
    check_stage_enums(api, stage_order, stored_stages, stage_states)
    check_w22_contracts()
    check_w23_contracts(json_file(ROOT / "examples/report-go-vet.json"))
    check_r1_corpus()
    events = yaml_file(ROOT / "api/openapi.yaml")["paths"]["/api/v1/attempts/{attempt_id}/events"]["post"]
    payloads = events["requestBody"]["content"]["application/json"]["schema"]["properties"]["payload"]["oneOf"]
    created = [p for p in payloads if p.get("required") == ["key", "required"]]
    ensure(len(created) == 1 and created[0]["properties"]["stage"]["allOf"] == [{"$ref": "#/components/schemas/TaskStage"}], "task_created stage must use TaskStage")
    activity = json_file(ROOT / "examples/project-activity.json")
    activity_validator = api_validator("ProjectActivity")
    activity_validator.validate(activity)
    check_activity(activity, stage_order)
    validators["snapshot-manifest.schema.json"].validate(json_file(ROOT / "examples/snapshot-manifest.json"))
    validators["snapshot-manifest.schema.json"].validate(json_file(ROOT / "examples/snapshot-manifest-git.json"))
    plan = json_file(ROOT / "examples/plan.json")
    report = json_file(ROOT / "examples/report.json")
    analyzer = yaml_file(ROOT / "examples/analyzer-ruff.yaml")
    go_vet = yaml_file(ROOT / "examples/analyzer-go-vet.yaml")
    observations = json_file(ROOT / "examples/observations-go-vet.json")
    validators["plan.schema.json"].validate(plan)
    validators["report.schema.json"].validate(report)
    validators["analyzer.schema.json"].validate(analyzer)
    validators["analyzer.schema.json"].validate(go_vet)
    validators["go-vet-observations.schema.json"].validate(observations)
    check_plan(plan)
    check_report(report)
    published = json_file(ROOT / "examples/report-go-vet.json")
    validators["report.schema.json"].validate(published)
    check_report(published, ROOT / "fixtures/go-vet")
    check_analyzer(analyzer)
    check_analyzer(go_vet)
    ensure(go_vet["exit_codes"]["findings"] == [], "go vet -json findings are not an exit-code class")
    check_observations(observations)
    w19_negatives = check_w19_contracts(validators)
    print(f"PASS: W19 analyzer pack contracts (staticcheck descriptor, real envelope example, analyzer_tools; {w19_negatives} negative cases)")
    w20_negatives = check_w20_rust_pack(validators)
    print(f"PASS: W20 Rust pack contracts (cargo check and clippy descriptors, real envelope examples; {w20_negatives} negative cases)")
    review = json_file(ROOT / "examples/review-record.json")
    review_validator = validators["review-record.schema.json"]
    review_validator.validate(review)
    check_review_example(review)
    r1_pack_doc, r1_roles = r1_pack()
    r1_raw = {stage: (ROOT / "examples/review-r1" / (stage + ".json")).read_text(encoding="utf-8") for stage in R1_STAGES}
    r1_docs = {stage: json.loads(text) for stage, text in r1_raw.items()}
    for doc in r1_docs.values():
        review_validator.validate(doc)
    check_review_r1_examples(r1_docs, r1_raw, r1_pack_doc, r1_roles)
    for name in ("examples/environment.json", "examples/environment-documents.json"):
        environment = json_file(ROOT / name)
        validators["environment.schema.json"].validate(environment)
        check_environment_example(environment)
    ensure(plan["run_id"] == report["run_id"], "Example run mismatch")
    plan_tasks = {task["id"]: task for task in plan["tasks"]}
    ensure(set(plan_tasks) == {task["id"] for task in report["tasks"]}, "Plan/report task mismatch")
    for task in report["tasks"]:
        ensure(task["required_for_verdict"] == plan_tasks[task["id"]]["required_for_verdict"], "Required-task flag mismatch")
    platform = yaml_file(ROOT / "examples/platform.yaml")
    scheduler = platform["scheduler"]
    ensure(0 < scheduler["heartbeat_seconds"] < scheduler["lease_seconds"] - scheduler["lease_safety_margin_seconds"], "Invalid lease timing")
    for pool in platform["pools"]:
        ensure(0 <= pool["min_workers"] <= pool["max_workers"], "Invalid pool bounds")
    project = yaml_file(ROOT / "examples/project.yaml")
    ensure(project["analysis"]["profile"] == "deep-review", "Unexpected example default profile")
    pack_file = ROOT / "prompts/review-v1/pack.yaml"
    pack = yaml_file(pack_file)
    pack_paths = ["pack.yaml", pack["core"], pack["domains"], *pack["roles"].values(), *pack["languages"].values()]
    digest = hashlib.sha256()
    for relative in sorted(set(pack_paths)):
        path = within_root(pack_file.parent / relative)
        ensure(path.is_file(), "Missing prompt: " + relative)
        digest.update(relative.encode("utf-8") + b"\0" + path.read_bytes() + b"\0")
    required = set(re.findall(r"(?:REQ|NFR)-\d{3}", (ROOT / "docs/01-requirements.md").read_text(encoding="utf-8")))
    trace = set()
    text = (ROOT / "docs/18-acceptance-and-traceability.md").read_text(encoding="utf-8")
    for family, numbers in re.findall(r"(REQ|NFR)-(\d{3}(?:,\d{3})*)", text):
        trace.update(family + "-" + number for number in numbers.split(","))
    ensure(required.issubset(trace), "Unmapped requirements: " + str(sorted(required - trace)))
    openapi_refs = check_openapi(ROOT / "api/openapi.yaml")
    bad = copy.deepcopy(report)
    bad["summary"]["total_findings"] += 1
    expect_invalid("invented total", check_report, bad)
    bad = copy.deepcopy(report)
    bad["findings"][0]["claims"][0]["evidence_ids"] = ["missing"]
    expect_invalid("missing evidence", check_report, bad)
    bad = copy.deepcopy(report)
    bad["findings"][0]["validation"] = "reproduced"
    expect_invalid("reproduced without execution", validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(report)
    bad["findings"][0]["locations"][0]["path"] = "../outside.py"
    expect_invalid("path traversal", check_report, bad)
    bad = copy.deepcopy(published)
    bad["observations"][0]["raw_ref"]["artifact_id"] = "unknown"
    expect_invalid("unbound raw stream", check_report, bad)
    bad = copy.deepcopy(published)
    bad["quality_verdict"] = "no_actionable_findings"
    expect_invalid("clean without AI review", check_report, bad)
    ensure(not published["export"]["include_code"] and all("fingerprint" not in f for f in published["findings"]), "Code-free example carries a fingerprint")
    for field, value in (("fingerprint", "fp2:" + "0" * 64),
                         ("fingerprint_basis", {"scheme": "fp2", "producer": "tool", "analyzer": "go.vet", "rule": "printf", "paths": ["example.go"], "symbols": [], "context": "unavailable", "move_key": "fp2:" + "1" * 64})):
        bad = copy.deepcopy(published)
        bad["findings"][0][field] = value
        expect_invalid("code-free export with " + field, validators["report.schema.json"].validate, bad)
    # W19 rule context (builder 4): the code-free export keeps the mode but not the configuration paths or digest.
    context = {"rule_pack": "codeye-go-staticcheck", "rule_pack_version": "2", "selection": ["-checks", "SA*"], "repository_config": "honored",
               "source_suppressions": "reported", "config": "recorded"}
    ok = copy.deepcopy(published)
    ok["coverage"]["tools"][0]["rule_context"] = context
    validators["report.schema.json"].validate(ok)
    check_report(ok, ROOT / "fixtures/go-vet")
    for field, value in (("config_digest", "0" * 64), ("config_paths", ["staticcheck.conf"])):
        bad = copy.deepcopy(ok)
        bad["coverage"]["tools"][0]["rule_context"][field] = value
        expect_invalid("code-free export with rule-context " + field, validators["report.schema.json"].validate, bad)
        expect_invalid("code-free export with rule-context " + field + " (cross-field)", check_report, bad)
    bad = copy.deepcopy(ok)
    bad["coverage"]["tools"][0]["rule_context"]["config"] = "not_read"
    expect_invalid("honored repository configuration marked not read", validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(ok)
    bad["coverage"]["tools"][0]["rule_context"].update(source_suppressions="honored", unreported_suppression_rules=["U1000"])
    expect_invalid("unreported suppression rules without reported suppressions", validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(ok)
    bad["coverage"]["tools"][0]["rule_context"].update(repository_config="overridden", config="not_read", config_digest="0" * 64)
    bad["export"]["include_code"] = True
    expect_invalid("rule context records configuration it did not read", validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(published)
    bad["observations"][0]["suppressed"] = True
    bad["findings"][0]["observation_ids"] = [bad["observations"][0]["id"]]
    expect_invalid("finding from a suppressed observation", check_report, bad)
    bad = copy.deepcopy(plan)
    bad["tasks"][0]["depends_on"] = [bad["tasks"][-1]["id"]]
    expect_invalid("cyclic plan", check_plan, bad)
    bad = copy.deepcopy(analyzer)
    bad["exit_codes"]["findings"] = [0]
    expect_invalid("exit-code overlap", check_analyzer, bad)
    bad = copy.deepcopy(observations)
    bad["observations"].pop()
    expect_invalid("observation count drift", check_observations, bad)
    bad = copy.deepcopy(observations)
    bad["outcome"].update(task_status="succeeded", reason=None, exit_class="success_without_findings")
    expect_invalid("zero findings with observations", check_observations, bad)
    bad = copy.deepcopy(observations)
    bad["executions"][3]["stdout"]["truncated"] = True
    expect_invalid("truncated success", check_observations, bad)
    bad = copy.deepcopy(observations)
    bad["workspace_drift"] = ["clean/c.go"]
    expect_invalid("success with drift", check_observations, bad)
    bad = copy.deepcopy(observations)
    bad["observations"][0]["location_state"] = "unresolved_missing_file"
    expect_invalid("unresolved with location", validators["go-vet-observations.schema.json"].validate, bad)
    bad = copy.deepcopy(observations)
    bad["outcome"].update(task_status="failed", reason=None)
    expect_invalid("failure without reason", validators["go-vet-observations.schema.json"].validate, bad)
    bad = copy.deepcopy(observations)
    bad["executions"][0]["command"]["dir"] = "/home/user/workspace"
    expect_invalid("node path in command dir", validators["go-vet-observations.schema.json"].validate, bad)
    bad = copy.deepcopy(json_file(ROOT / "examples/doctor.json"))
    bad["checks"][0]["status"] = "success"
    expect_invalid("unknown doctor status", validators["doctor.schema.json"].validate, bad)
    bad = copy.deepcopy(json_file(ROOT / "examples/doctor.json"))
    bad["checks"][-1]["id"] = "host"
    expect_invalid("missing doctor capability", validators["doctor.schema.json"].validate, bad)
    calls_example = copy.deepcopy(published)
    calls_example["coverage"]["review_calls"] = [
        {key: call[key] for key in ("role", "model", "opencode_version", "pack_sha256", "request_sha256", "outcome")}
        for call in review["calls"]]
    validators["report.schema.json"].validate(calls_example)
    calls_example["coverage"]["review_calls"][0]["pack_sha256"] = "invalid"
    expect_invalid("unbound report prompt digest", validators["report.schema.json"].validate, calls_example)
    bad = copy.deepcopy(review)
    bad["verifications"][0]["verdict"] = "reproduced"
    expect_invalid("model reproduction claim", review_validator.validate, bad)
    bad = copy.deepcopy(review)
    bad["evidence"][0]["end_line"] = 100000
    expect_invalid("review source range", check_review_example, bad)
    bad = copy.deepcopy(review)
    bad["verifications"][0]["counterevidence_ids"] = ["ev_" + "0" * 24]
    expect_invalid("unknown counterevidence", check_review_example, bad)
    bad = copy.deepcopy(review)
    bad["evidence"][0]["text"] = "raw source"
    expect_invalid("source text in review record", review_validator.validate, bad)
    r1_calls = copy.deepcopy(published)
    r1_calls["coverage"]["review_calls"] = [
        {key: call[key] for key in ("role", "model", "opencode_version", "pack_sha256", "request_sha256", "outcome", "pack_id", "pack_version")}
        for stage in R1_STAGES for call in r1_docs[stage]["calls"]]
    validators["report.schema.json"].validate(r1_calls)
    r1_calls["coverage"]["review_calls"][0]["pack_version"] = "2.0.0 rc1"
    expect_invalid("invalid report pack version", validators["report.schema.json"].validate, r1_calls)

    def r1_case(stage, edit, check):
        docs = copy.deepcopy(r1_docs)
        edit(docs[stage])
        return lambda _: check(docs[stage]) if check else check_review_r1_examples(docs, r1_raw, r1_pack_doc, r1_roles)

    def set_key(key, value, index=0, field=None):
        def edit(doc):
            if field is None:
                doc[key] = value
            else:
                doc[key][index][field] = value
        return edit

    fake_id = "ev_" + "0" * 24
    r1_negative = [
        ("review-r1 fabricated evidence", "investigate", lambda d: d["candidates"][0]["evidence_ids"].append(fake_id), None),
        ("review-r1 scope generalization", "documents", set_key("contracts", ".", 1, "applies_to"), None),
        ("review-r1 pack digest drift", "verify", set_key("calls", "0" * 64, 0, "pack_sha256"), None),
        ("review-r1 altered upstream candidate", "verify", lambda d: d["candidates"][0].update(title=d["candidates"][0]["title"] + "!"), None),
        ("review-r1 model reproduction verdict", "verify", set_key("verifications", "reproduced", 0, "verdict"), review_validator.validate),
        ("review-r1 source text", "investigate", lambda d: d["evidence"][0].update(text="raw source"), review_validator.validate),
        ("review-r1 platform verdict in environment output", "environment", lambda d: d["environment_notes"][0].update(status="satisfied"), review_validator.validate),
        ("review-r1 interpret candidate without observation", "interpret", lambda d: d["candidates"][0].pop("observation_ids"), review_validator.validate),
        ("review-r1 failed record keeps accepted call", "documents", lambda d: [d.update(outcome="failed", failure_reason="model_timeout")] + [d.pop(k) for k in ("contracts", "conflicts", "questions", "instruction_notes")], review_validator.validate),
        ("review-r1 investigate given raw tool observations", "investigate", lambda d: d.update(observations=docs_obs), review_validator.validate),
    ]
    docs_obs = copy.deepcopy(r1_docs["interpret"]["observations"])
    for name, stage, edit, check in r1_negative:
        expect_invalid(name, r1_case(stage, edit, check), None)
    bad = copy.deepcopy(activity)
    bad["stages"][3]["counts"]["timed_out"] = 0
    expect_invalid("zero stage count", activity_validator.validate, bad)
    bad = copy.deepcopy(activity)
    bad["stages"][3]["reasons"] = ["tool_failed", "handler_error"]
    expect_invalid("unsorted stage reasons", lambda doc: check_activity(doc, stage_order), bad)
    bad = copy.deepcopy(activity)
    bad["detail"] = None
    expect_invalid("stage detail without run", lambda doc: check_activity(doc, stage_order), bad)
    bad = copy.deepcopy(api)
    bad["TaskStage"] = {"enum": [*stage_order, "lint"]}
    expect_invalid("stage enum drift", lambda doc: check_stage_enums(doc, stage_order, stored_stages, stage_states), bad)
    ensure(not (ROOT / "docs/02-benchmark.md").exists(), "Standalone comparison document is out of scope")
    print(f"PASS: {link_count} local Markdown file links; {len(validators)} JSON Schemas; 19 example contracts")
    print(f"PASS: example evidence hashes/ranges/counts; DAG; {len(required)} requirements traced; {openapi_refs} OpenAPI references")
    print(f"PASS: task stage enums (domain/OpenAPI/migration 0006/progress: {len(stage_order)} stages, {len(stage_states)} states)")
    print(f"PASS: {27 + len(r1_negative)} negative cases; example policy timing; prompt manifest")
    print("Prompt pack SHA-256:", digest.hexdigest())
    print("review-r1 candidate pack (not promoted):", ", ".join(f"{role} {value['digest'][:12]}" for role, value in r1_roles.items()))
    print("Scope: documentation/fixture contracts only; not CODEYE runtime or model-quality evidence.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, ValidationError) as exc:
        print("FAIL:", exc, file=sys.stderr)
        raise SystemExit(1)
