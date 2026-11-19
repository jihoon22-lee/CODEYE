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
from pathlib import Path
import re
import sys
from urllib.parse import unquote

try:
    from jsonschema import ValidationError
except ImportError as exc:
    raise SystemExit("Install the approved packages in scripts/requirements.txt: " + str(exc))

# Other scripts import these names from validate_bundle.
from bundle_checks.common import ROOT, ensure, expect_invalid, json_file, make_validators, within_root, yaml_file
from bundle_checks.report import check_analyzer, check_observations, check_plan, check_report
from bundle_checks.analyzers import check_cpp_pack_contracts, check_js_ts_pack_contracts, check_python_pack, check_rust_pack, check_staticcheck_pack_contracts, check_structure_pack_contracts
from bundle_checks.review import ROLE_STAGES, check_environment_example, check_review_example, check_role_pack_examples, role_pack
from bundle_checks.api import api_validator, check_activity, check_comparison_contracts, check_doctor_operations, check_openapi, check_schedule_contracts, check_stage_enums, stage_vocabulary
from bundle_checks.docs import check_code_doc_refs, check_doc_names, check_holdout_corpus, check_source_names, heading_anchors


def main() -> None:
    link_count = 0
    ignored = {".git", ".claude", ".codeye", ".venv", "node_modules", "dist", "bin", "coverage", "__pycache__"}
    markdown_files = []
    for directory, dirs, files in os.walk(ROOT):
        dirs[:] = sorted(name for name in dirs if name not in ignored)
        markdown_files.extend(Path(directory) / name for name in sorted(files) if name.endswith(".md"))
    anchors = {}
    for path in markdown_files:
        for match in re.finditer(r"\[[^\]]*\]\(([^)]+)\)", path.read_text(encoding="utf-8")):
            target = match.group(1).strip().strip("<>")
            if re.match(r"(?:https?://|mailto:)", target):
                continue
            file_part, _, anchor = target.partition("#")
            file_part = unquote(file_part)
            linked = within_root(path.parent / file_part) if file_part else path
            ensure(linked.exists(), "Broken Markdown link in " + str(path) + ": " + target)
            if anchor and linked.suffix == ".md":
                if linked not in anchors:
                    anchors[linked] = heading_anchors(linked.read_text(encoding="utf-8"))
                ensure(unquote(anchor).lower() in anchors[linked], "Broken Markdown anchor in " + str(path) + ": " + target)
            link_count += 1
    check_doc_names(markdown_files)
    code_refs = check_code_doc_refs()
    named = check_source_names()
    validators = make_validators()
    validators["doctor.schema.json"].validate(json_file(ROOT / "examples/doctor.json"))
    validators["state.schema.json"].validate(json_file(ROOT / "examples/state.json"))
    state = validators["state.schema.json"].schema["properties"]
    api = yaml_file(ROOT / "api/openapi.yaml")["components"]["schemas"]
    for field, name in (("run_status", "RunStatus"), ("attempt_status", "AttemptStatus"), ("cleanup_state", "CleanupState")):
        ensure(set(state[field]["enum"]) == set(api[name]["enum"]), "State/API enum mismatch: " + field)
    stage_order, stored_stages, stage_states = stage_vocabulary()
    check_stage_enums(api, stage_order, stored_stages, stage_states)
    check_schedule_contracts()
    check_comparison_contracts(json_file(ROOT / "examples/report-go-vet.json"))
    check_holdout_corpus()
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
    staticcheck_negatives = check_staticcheck_pack_contracts(validators)
    print(f"PASS: analyzer pack contracts (staticcheck descriptor, real envelope example, analyzer_tools; {staticcheck_negatives} negative cases)")
    print(f"PASS: C/C++ pack (cppcheck/clang-tidy descriptors, real envelopes bound to the fixture, expected outcomes; {check_cpp_pack_contracts(validators)} negative cases)")
    print(f"PASS: JS/TS pack contracts (eslint and tsc descriptors, real envelope examples; {check_js_ts_pack_contracts(validators)} negative cases)")
    rust_negatives = check_rust_pack(validators)
    print(f"PASS: Rust pack contracts (cargo check and clippy descriptors, real envelope examples; {rust_negatives} negative cases)")
    check_python_pack(validators)
    print(f"PASS: structure and secrets pack contracts (lizard, jscpd, gitleaks descriptors and real envelopes; {check_structure_pack_contracts(validators)} negative cases)")
    review = json_file(ROOT / "examples/review-record.json")
    review_validator = validators["review-record.schema.json"]
    review_validator.validate(review)
    check_review_example(review)
    role_pack_doc, role_files = role_pack()
    role_raw = {stage: (ROOT / "examples/review-r1" / (stage + ".json")).read_text(encoding="utf-8") for stage in ROLE_STAGES}
    role_docs = {stage: json.loads(text) for stage, text in role_raw.items()}
    for doc in role_docs.values():
        review_validator.validate(doc)
    check_role_pack_examples(role_docs, role_raw, role_pack_doc, role_files)
    # review-r1 2.0.0 (docs/REVIEW.md#복구-경로): a stage keeps at most three calls (the
    # request, one repair, one retry of a reply that was not JSON).
    retried = copy.deepcopy(role_docs["verify"])
    first = retried["calls"][0]
    retried["calls"] = [dict(first, attempt=1, output_errors=["evidence_id_dropped", "evidence_id_unknown"]),
                        dict(first, attempt=2, outcome="output_rejected", output_errors=["output_not_json"], usage=None),
                        dict(first, attempt=3)]
    review_validator.validate(retried)
    too_many = copy.deepcopy(retried)
    too_many["calls"].append(dict(first, attempt=4))
    expect_invalid("review-r1 stage with four calls", review_validator.validate, too_many)
    fourth = copy.deepcopy(retried)
    fourth["calls"][2]["attempt"] = 4
    expect_invalid("review-r1 call attempt 4", review_validator.validate, fourth)
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
    required = set(re.findall(r"(?:REQ|NFR)-\d{3}", (ROOT / "docs/REQUIREMENTS.md").read_text(encoding="utf-8")))
    trace = set()
    text = (ROOT / "docs/TESTING.md").read_text(encoding="utf-8")
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
    # Rule context: the code-free export keeps the mode but not the configuration paths or digest.
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
    check_doctor_operations(validators["doctor.schema.json"], json_file(ROOT / "examples/doctor.json"))
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
    role_calls = copy.deepcopy(published)
    role_calls["coverage"]["review_calls"] = [
        {key: call[key] for key in ("role", "model", "opencode_version", "pack_sha256", "request_sha256", "outcome", "pack_id", "pack_version")}
        for stage in ROLE_STAGES for call in role_docs[stage]["calls"]]
    validators["report.schema.json"].validate(role_calls)
    # Five review-r1 stages of up to three calls each fit the report; more do not.
    most = copy.deepcopy(role_calls)
    most["coverage"]["review_calls"] = [dict(role_calls["coverage"]["review_calls"][0]) for _ in range(15)]
    validators["report.schema.json"].validate(most)
    most["coverage"]["review_calls"].append(dict(role_calls["coverage"]["review_calls"][0]))
    expect_invalid("report with 16 review calls", validators["report.schema.json"].validate, most)
    role_calls["coverage"]["review_calls"][0]["pack_version"] = "2.0.0 rc1"
    expect_invalid("invalid report pack version", validators["report.schema.json"].validate, role_calls)

    def role_case(stage, edit, check):
        docs = copy.deepcopy(role_docs)
        edit(docs[stage])
        return lambda _: check(docs[stage]) if check else check_role_pack_examples(docs, role_raw, role_pack_doc, role_files)

    def set_key(key, value, index=0, field=None):
        def edit(doc):
            if field is None:
                doc[key] = value
            else:
                doc[key][index][field] = value
        return edit

    fake_id = "ev_" + "0" * 24
    role_negative = [
        ("review-r1 fabricated evidence", "investigate", lambda d: d["candidates"][0]["evidence_ids"].append(fake_id), None),
        ("review-r1 scope generalization", "documents", set_key("contracts", ".", 1, "applies_to"), None),
        ("review-r1 pack digest drift", "verify", set_key("calls", "0" * 64, 0, "pack_sha256"), None),
        ("review-r1 language guide outside the selection", "investigate", lambda d: d["calls"][0]["pack_files"].insert(3, "languages/python.md"), None),
        ("review-r1 altered upstream candidate", "verify", lambda d: d["candidates"][0].update(title=d["candidates"][0]["title"] + "!"), None),
        ("review-r1 model reproduction verdict", "verify", set_key("verifications", "reproduced", 0, "verdict"), review_validator.validate),
        ("review-r1 source text", "investigate", lambda d: d["evidence"][0].update(text="raw source"), review_validator.validate),
        ("review-r1 platform verdict in environment output", "environment", lambda d: d["environment_notes"][0].update(status="satisfied"), review_validator.validate),
        ("review-r1 interpret candidate without observation", "interpret", lambda d: d["candidates"][0].pop("observation_ids"), review_validator.validate),
        ("review-r1 failed record keeps accepted call", "documents", lambda d: [d.update(outcome="failed", failure_reason="model_timeout")] + [d.pop(k) for k in ("contracts", "conflicts", "questions", "instruction_notes")], review_validator.validate),
        ("review-r1 investigate given raw tool observations", "investigate", lambda d: d.update(observations=docs_obs), review_validator.validate),
    ]
    docs_obs = copy.deepcopy(role_docs["interpret"]["observations"])
    for name, stage, edit, check in role_negative:
        expect_invalid(name, role_case(stage, edit, check), None)
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
    print(f"PASS: {code_refs} document references from source, schema and example files")
    print(f"PASS: {named} source files without build-stage labels")
    print(f"PASS: {link_count} local Markdown file links; {len(validators)} JSON Schemas; 19 example contracts")
    print(f"PASS: example evidence hashes/ranges/counts; DAG; {len(required)} requirements traced; {openapi_refs} OpenAPI references")
    print(f"PASS: task stage enums (domain/OpenAPI/migration 0006/progress: {len(stage_order)} stages, {len(stage_states)} states)")
    print(f"PASS: {27 + len(role_negative)} negative cases; example policy timing; prompt manifest")
    print("Prompt pack SHA-256:", digest.hexdigest())
    print(f"review-r1 default pack {role_pack_doc['version']} (Go-only selection):", ", ".join(f"{role} {value['digest'][:12]}" for role, value in role_files.items()))
    print("Scope: documentation/fixture contracts only; not CODEYE runtime or model-quality evidence.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, ValidationError) as exc:
        print("FAIL:", exc, file=sys.stderr)
        raise SystemExit(1)
