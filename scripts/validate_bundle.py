#!/usr/bin/env python3
"""Validate CODEYE contracts and examples, independently of runtime tests.

Checks local Markdown links and anchors, JSON Schemas,
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
from bundle_checks.report import check_analyzer, check_document_comparisons, check_observations, check_plan, check_report
from bundle_checks.analyzers import check_cpp_pack_contracts, check_js_ts_pack_contracts, check_python_pack, check_rust_pack, check_staticcheck_pack_contracts, check_structure_pack_contracts
from bundle_checks.review import ROLE_STAGES, check_contract_example, check_environment_example, check_probe_example, check_stage_examples, check_slice_examples, check_workflow_example, pack_selection, slice_examples
from bundle_checks.api import api_validator, check_account_contracts, check_activity, check_comparison_contracts, check_doctor_operations, check_openapi, check_schedule_contracts, check_shared_service_contracts, check_stage_enums, check_tools_contract, stage_vocabulary
from bundle_checks.docs import check_code_doc_refs, check_doc_names, check_holdout_corpus, check_markdown_anchors, check_mode_names, check_stage_labels, heading_anchors


def main() -> None:
    check_markdown_anchors()
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
    named = check_stage_labels()
    moded = check_mode_names()
    validators = make_validators()
    validators["doctor.schema.json"].validate(json_file(ROOT / "examples/doctor.json"))
    tools_example = json_file(ROOT / "examples/tools.json")
    validators["tools.schema.json"].validate(tools_example)
    check_tools_contract(validators["tools.schema.json"], tools_example)
    validators["state.schema.json"].validate(json_file(ROOT / "examples/state.json"))
    state = validators["state.schema.json"].schema["properties"]
    api = yaml_file(ROOT / "api/openapi.yaml")["components"]["schemas"]
    for field, name in (("run_status", "RunStatus"), ("attempt_status", "AttemptStatus"), ("cleanup_state", "CleanupState")):
        ensure(set(state[field]["enum"]) == set(api[name]["enum"]), "State/API enum mismatch: " + field)
    stage_order, stored_stages, stage_states = stage_vocabulary()
    check_stage_enums(api, stage_order, stored_stages, stage_states)
    check_schedule_contracts()
    check_comparison_contracts(json_file(ROOT / "examples/report-go-vet.json"))
    check_account_contracts()
    check_shared_service_contracts()
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
    git_manifest = json_file(ROOT / "examples/snapshot-manifest-git.json")
    validators["snapshot-manifest.schema.json"].validate(git_manifest)
    # A pull-request snapshot names its ref instead of a branch: exactly one of the two.
    pull_manifest = {k: v for k, v in git_manifest.items() if k != "source_branch"} | {"source_ref": "refs/pull/7/head"}
    validators["snapshot-manifest.schema.json"].validate(pull_manifest)
    for bad in (pull_manifest | {"source_branch": "main"}, {k: v for k, v in pull_manifest.items() if k != "source_ref"}, pull_manifest | {"source_ref": "refs/heads/main"}):
        ensure(not validators["snapshot-manifest.schema.json"].is_valid(bad), "snapshot manifest must have exactly one of source_branch and source_ref")
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
    # examples/report.json is the builder's output for fixtures/modules/input
    # (internal/worker/review/report_example_test.go); its evidence hashes bind to that tree.
    modules_root = ROOT / "fixtures/modules/input"
    check_report(report, modules_root)
    observation_negatives = check_additional_observation_example(report, validators["report.schema.json"])
    # examples/report-slices.json is the builder's output for fixtures/modules/slices/input
    # under call ceiling 15 (internal/worker/review/report_slices_example_test.go): review slices and the ledger.
    slices_report = json_file(ROOT / "examples/report-slices.json")
    coverage_root = ROOT / "fixtures/modules/slices/input"
    validators["report.schema.json"].validate(slices_report)
    check_report(slices_report, coverage_root)
    # The same fixture without a secrets scan at the default ceiling: the slices still run, and the
    # report says the model input was not screened.
    unscreened = json_file(ROOT / "internal/report/testdata/report-unscreened.json")
    validators["report.schema.json"].validate(unscreened)
    check_report(unscreened, coverage_root)
    unscreened_ledger = unscreened["code_review"]["ledger"]
    ensure(unscreened_ledger["scope"]["result"] == "accepted" and unscreened_ledger["modules"] and unscreened_ledger["max_calls"] == 99
           and unscreened["secrets"]["model_input"] == "not_screened"
           and any(g["code"] == "model_input_not_screened" for g in unscreened["gaps"])
           and any(f["id"].startswith("review_s") for f in unscreened["findings"]), "Unscreened slice report without its slices or its not-screened state")
    # The same fixture after a secrets scan that ran but failed: review still runs on the read
    # filters alone, nothing is skipped for the screen and the report says the input was not screened.
    unusable = json_file(ROOT / "internal/report/testdata/report-screen-unusable.json")
    validators["report.schema.json"].validate(unusable)
    check_report(unusable, coverage_root)
    unusable_tasks = {t["key"]: t for t in unusable["tasks"]}
    ensure(unusable["secrets"]["model_input"] == "not_screened" and unusable["secrets"]["task_keys"] == ["gitleaks"]
           and unusable_tasks["gitleaks"]["status"] == "failed"
           and unusable["coverage"]["ai_review"]["status"] == "performed"
           and not any(t["reason"] == "secret_screen_failed" for t in unusable["tasks"])
           and any(g["code"] == "model_input_not_screened" for g in unusable["gaps"]), "Unusable-screen report skipped review or lost its not-screened state")
    pull_request = json_file(ROOT / "examples/report-pull-request.json")
    validators["report.schema.json"].validate(pull_request)
    pr = pull_request["pull_request"]
    ensure(pr["files_listed"] == pr["files_removed"] + pr["review"]["reviewable"]
           and len(pr["items"]) == sum(pr[k]["changed_lines"] + pr[k]["changed_file"] for k in ("findings", "observations"))
           and all(i["scope"] in ("changed_lines", "changed_file") for i in pr["items"]), "Pull-request report counts are inconsistent")
    bad = copy.deepcopy(pull_request)
    bad["pull_request"]["items"][0]["scope"] = "elsewhere"
    expect_invalid("pull-request item outside the changes", validators["report.schema.json"].validate, bad)
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
    review_validator = validators["review-record.schema.json"]
    pack_doc, role_files = pack_selection()
    role_raw = {stage: (ROOT / "examples/review" / (stage + ".json")).read_text(encoding="utf-8") for stage in ROLE_STAGES}
    role_docs = {stage: json.loads(text) for stage, text in role_raw.items()}
    for doc in role_docs.values():
        review_validator.validate(doc)
    check_stage_examples(role_docs, role_raw, pack_doc, role_files)
    workflow_negatives = check_workflow_example(review_validator, pack_doc, role_files)
    print(f"PASS: workflow route selections attached to the accepted document contract; {workflow_negatives} negative cases")
    workflow_report = copy.deepcopy(unscreened)
    workflow_artifact = next(a for a in workflow_report["artifacts"] if a["task_key"] == "review.documents" and a["media_type"] == "application/vnd.codeye.review+json")
    workflow_artifact["decoder"] = "application/vnd.codeye.review+json;schema_version=0.5"
    validators["report.schema.json"].validate(workflow_report)
    workflow_report["builder"]["version"] = "13"
    expect_invalid("older builder claiming the workflow document decoder", validators["report.schema.json"].validate, workflow_report)
    workflow_report = copy.deepcopy(report)
    doc_call = next(c for c in workflow_report["coverage"]["review_calls"] if c["role"] == "documents")
    workflow_report["coverage"]["review_calls"].append(dict(doc_call, purpose="workflow_selection"))
    validators["report.schema.json"].validate(workflow_report)
    workflow_report["builder"]["version"] = "13"
    expect_invalid("older builder marking a workflow selection call", validators["report.schema.json"].validate, workflow_report)
    probe_negatives = check_probe_example(review_validator, role_docs, role_raw)
    print(f"PASS: review navigation probe bound to the example snapshot; {probe_negatives} negative cases")
    contract_negatives = check_contract_example(review_validator, pack_doc, role_files)
    print(f"PASS: final contract review bound to accepted documents and overview artifacts; {contract_negatives} negative cases")
    slice_negatives = check_slice_examples(slice_examples(), validators)
    print(f"PASS: review slices (scope artifact 0.2 and slice records 0.3 bound to the coverage fixture; {slice_negatives} negative cases)")
    # Review 2.0.0 (docs/REVIEW.md#복구-경로): a stage keeps at most three calls (the
    # request, one repair, one retry of a reply that was not JSON).
    retried = copy.deepcopy(role_docs["verify"])
    first = retried["calls"][0]
    retried["calls"] = [dict(first, attempt=1, output_errors=["evidence_id_dropped", "evidence_id_unknown"]),
                        dict(first, attempt=2, outcome="output_rejected", output_errors=["output_not_json"], usage=None),
                        dict(first, attempt=3)]
    review_validator.validate(retried)
    too_many = copy.deepcopy(retried)
    too_many["calls"].append(dict(first, attempt=4))
    expect_invalid("review stage with four calls", review_validator.validate, too_many)
    fourth = copy.deepcopy(retried)
    fourth["calls"][2]["attempt"] = 4
    expect_invalid("review call attempt 4", review_validator.validate, fourth)
    dependencies = json_file(ROOT / "examples/dependencies.json")
    validators["dependencies.schema.json"].validate(dependencies)
    for name, edit in {
        "succeeded but partial": lambda d: d.update(task_status="succeeded"),
        "failed without reason": lambda d: d.update(reason=None),
        "shared cache without key": lambda d: d["cache"].update(key=None),
        "skipped_policy not skipped": lambda d: d.update(task_status="skipped_policy"),
        "unknown task key": lambda d: d.update(task_key="deps.go.x"),
    }.items():
        bad = copy.deepcopy(dependencies)
        edit(bad)
        expect_invalid("dependency record " + name, validators["dependencies.schema.json"].validate, bad)
    js_dependencies = json_file(ROOT / "examples/dependencies-javascript.json")
    validators["dependencies.schema.json"].validate(js_dependencies)
    for name, edit in {
        "installed_dirs in a shared cache": lambda d: d.update(cache={"kind": "shared", "key": "javascript/pnpm"}),
        "installed_dirs of another ecosystem": lambda d: d.update(module={"ecosystem": "rust", "id": "rust:web", "path": "web"}, task_key="deps.rust.m01"),
        "installed dir not node_modules": lambda d: d.update(installed_dirs=["web/vendor"]),
    }.items():
        bad = copy.deepcopy(js_dependencies)
        edit(bad)
        expect_invalid("dependency record " + name, validators["dependencies.schema.json"].validate, bad)
    for name in ("examples/environment.json", "examples/environment-documents.json"):
        environment = json_file(ROOT / name)
        validators["environment.schema.json"].validate(environment)
        check_environment_example(environment)
    platform = yaml_file(ROOT / "examples/platform.yaml")
    scheduler = platform["scheduler"]
    ensure(0 < scheduler["heartbeat_seconds"] < scheduler["lease_seconds"] - scheduler["lease_safety_margin_seconds"], "Invalid lease timing")
    for pool in platform["pools"]:
        ensure(0 <= pool["min_workers"] <= pool["max_workers"], "Invalid pool bounds")
    project = yaml_file(ROOT / "examples/project.yaml")
    ensure(project["analysis"]["profile"] == "deep-review", "Unexpected example default profile")
    pack_file = ROOT / "prompts/review/pack.yaml"
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
    # Negative cases pick findings by shape, not by position: the builder orders them.
    claimed = next(i for i, f in enumerate(report["findings"]) if f["claims"] and f["claims"][0]["evidence_ids"])
    located = next(i for i, f in enumerate(report["findings"]) if f["locations"])
    bad = copy.deepcopy(report)
    bad["summary"]["total_findings"] += 1
    expect_invalid("invented total", lambda d: check_report(d, modules_root), bad)
    bad = copy.deepcopy(report)
    bad["findings"][claimed]["claims"][0]["evidence_ids"] = ["missing"]
    expect_invalid("missing evidence", lambda d: check_report(d, modules_root), bad)
    bad = copy.deepcopy(report)
    bad["findings"][claimed]["validation"] = "reproduced"
    expect_invalid("reproduced without execution", validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(report)
    bad["findings"][located]["locations"][0]["path"] = "../outside.py"
    expect_invalid("path traversal", lambda d: check_report(d, modules_root), bad)
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
    # Analysis sections (docs/REPORT.md#분석-종류별-절): a revision without them keeps the flat
    # layout, and a sectioned one partitions observations and findings exactly once.
    ensure("overview" not in published and "overview" in report, "Section presence of the report examples")
    tool_with = lambda d, key: next(t for l in d["static_analysis"]["languages"] for t in l["tools"] if t[key])
    section_cases = {
        "report with only some sections": lambda d: d.pop("secrets"),
        "observation dropped from its section": lambda d: d["secrets"]["detections"].pop(),
        "observation listed twice": lambda d: d["secrets"]["detections"].append(dict(d["secrets"]["detections"][0])),
        "finding in static analysis and code review": lambda d: d["code_review"]["finding_ids"].append(tool_with(d, "finding_ids")["finding_ids"][0]),
        "finding dropped from code review": lambda d: d["code_review"]["finding_ids"].pop(),
        "invented metric maximum": lambda d: d["metrics"]["metrics"][0].update(max_value=d["metrics"]["metrics"][0]["max_value"] + 1),
        "invented section finding count": lambda d: next(s for s in d["overview"]["sections"] if s["key"] == "code_review")["counts"].update(findings=99),
        "invented gap count": lambda d: next(s for s in d["overview"]["sections"] if s["key"] == "gaps")["counts"].update(gaps=99),
        "failed tool row read as success": lambda d: next(t for l in d["static_analysis"]["languages"] for t in l["tools"] if t["task_status"] == "failed").update(exit_class="success_without_findings"),
        "model statement without a performed role": lambda d: d["documents"]["model"].update(state="not_performed"),
        "code-free export with model text": lambda d: (d["export"].update(include_code=False), [o.update(raw_message=None) for o in d["observations"]]),
        "section evidence hash mismatch": lambda d: d["documents"]["evidence"][0].update(blob_sha256="0" * 64),
        "section statement cites unknown evidence": lambda d: d["documents"]["contracts"][0]["evidence_ids"].append("ev_missing"),
        "comparison changes its contract": lambda d: d["documents"]["comparisons"][0].update(statement="다른 문서 계약에 대한 판정이다."),
        "comparison verdict without implementation": lambda d: d["documents"]["comparisons"][0].update(code_evidence_ids=[]),
        "comparison uses code outside its scope": lambda d: d["documents"]["comparisons"][1].update(
            status="match", code_evidence_ids=d["documents"]["comparisons"][0]["code_evidence_ids"]),
    }
    for name, edit in section_cases.items():
        bad = copy.deepcopy(report)
        edit(bad)
        expect_invalid(name, lambda d: check_report(d, modules_root), bad)
    bad = copy.deepcopy(published)
    bad["gaps"] = []
    expect_invalid("flat revision with a sections block", lambda d: check_report(d, ROOT / "fixtures/go-vet"), bad)
    # Review slices and the ledger (docs/REPORT.md#모듈별-검토-장부).
    ensure("ledger" in slices_report["code_review"] and "ledger" not in report["code_review"], "Ledger presence of the report examples")
    store = lambda d: next(m for m in d["code_review"]["ledger"]["modules"] if m["module_id"] == "go:store")
    ledger_cases = {
        "ledger files that do not add up": lambda d: store(d).update(files_sent_to_model=store(d)["files_sent_to_model"] + 1),
        "ledger status not following its rows": lambda d: d["code_review"]["ledger"].update(status="complete"),
        "ledger dropped while review.scope ran": lambda d: d["code_review"].pop("ledger"),
        "ledger slice differs from its task": lambda d: store(d)["slices"][0].update(status="failed"),
        "ledger counts fewer calls than listed": lambda d: d["code_review"]["ledger"].update(calls_used=1),
        "ledger request bytes differ": lambda d: d["code_review"]["ledger"].update(request_bytes_sent=1),
        "review call naming another role's task": lambda d: d["coverage"]["review_calls"][0].update(task_key="review.verify.s001"),
        "slice finding of an unplanned slice": lambda d: [f.update(id="review_s031_x") for f in d["findings"] if f["id"].startswith("review_s001_")],
        "overview scope not from the ledger": lambda d: next(s for s in d["overview"]["sections"] if s["key"] == "code_review")["counts"].update(files_sent_to_model=0),
    }
    for name, edit in ledger_cases.items():
        bad = copy.deepcopy(slices_report)
        edit(bad)
        expect_invalid(name, lambda d: check_report(d, coverage_root), bad)
    for name, edit in {"ledger with an invalid ceiling": lambda d: d["code_review"]["ledger"].update(max_calls=14),
                       "ledger with an unknown status": lambda d: d["code_review"]["ledger"].update(status="verified"),
                       "review call with an invalid task key": lambda d: d["coverage"]["review_calls"][0].update(task_key="review.scope")}.items():
        bad = copy.deepcopy(slices_report)
        edit(bad)
        expect_invalid(name, validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(report)
    bad["builder"]["version"] = "5"
    bad["coverage"]["review_calls"][0]["task_key"] = "review.environment"
    expect_invalid("builder-5 revision with a review call task key", lambda d: check_report(d, modules_root), bad)
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
    role_calls = copy.deepcopy(published)
    role_calls["coverage"]["review_calls"] = [
        {key: call[key] for key in ("role", "model", "opencode_version", "pack_sha256", "request_sha256", "outcome", "pack_id", "pack_version")}
        for stage in ROLE_STAGES for call in role_docs[stage]["calls"]]
    validators["report.schema.json"].validate(role_calls)
    bad = copy.deepcopy(role_calls)
    bad["coverage"]["review_calls"][0]["pack_sha256"] = "invalid"
    expect_invalid("unbound report prompt digest", validators["report.schema.json"].validate, bad)
    bad = copy.deepcopy(role_calls)
    del bad["coverage"]["review_calls"][0]["pack_id"]
    expect_invalid("report review call without its pack", validators["report.schema.json"].validate, bad)
    # The largest call ceiling (207: five whole-run roles and 96 slots of two calls each) fits the report; more do not.
    most = copy.deepcopy(role_calls)
    most["coverage"]["review_calls"] = [dict(role_calls["coverage"]["review_calls"][0]) for _ in range(207)]
    validators["report.schema.json"].validate(most)
    most["coverage"]["review_calls"].append(dict(role_calls["coverage"]["review_calls"][0]))
    expect_invalid("report with 208 review calls", validators["report.schema.json"].validate, most)
    role_calls["coverage"]["review_calls"][0]["pack_version"] = "2.0.0 rc1"
    expect_invalid("invalid report pack version", validators["report.schema.json"].validate, role_calls)

    def role_case(stage, edit, check):
        docs = copy.deepcopy(role_docs)
        edit(docs[stage])
        return lambda _: check(docs[stage]) if check else check_stage_examples(docs, role_raw, pack_doc, role_files)

    def set_key(key, value, index=0, field=None):
        def edit(doc):
            if field is None:
                doc[key] = value
            else:
                doc[key][index][field] = value
        return edit

    fake_id = "ev_" + "0" * 24
    role_negative = [
        ("review fabricated evidence", "investigate", lambda d: d["candidates"][0]["evidence_ids"].append(fake_id), None),
        ("review scope generalization", "documents", set_key("contracts", ".", 1, "applies_to"), None),
        ("review pack digest drift", "verify", set_key("calls", "0" * 64, 0, "pack_sha256"), None),
        ("review language guide outside the selection", "investigate", lambda d: d["calls"][0]["pack_files"].insert(3, "languages/python.md"), None),
        ("review altered upstream candidate", "verify", lambda d: d["candidates"][0].update(title=d["candidates"][0]["title"] + "!"), None),
        ("review model reproduction verdict", "verify", set_key("verifications", "reproduced", 0, "verdict"), review_validator.validate),
        ("review source text", "investigate", lambda d: d["evidence"][0].update(text="raw source"), review_validator.validate),
        ("review platform verdict in environment output", "environment", lambda d: d["environment_notes"][0].update(status="satisfied"), review_validator.validate),
        ("review interpret candidate without observation", "interpret", lambda d: d["candidates"][0].pop("observation_ids"), review_validator.validate),
        ("review failed record keeps accepted call", "documents", lambda d: [d.update(outcome="failed", failure_reason="model_timeout")] + [d.pop(k) for k in ("contracts", "conflicts", "questions", "instruction_notes")], review_validator.validate),
        ("review investigate given raw tool observations", "investigate", lambda d: d.update(observations=docs_obs), review_validator.validate),
        ("review source range outside the file", "investigate", lambda d: d["evidence"][0].update(end_line=100000), None),
        ("review unknown counterevidence", "verify", lambda d: d["verifications"][0].update(counterevidence_ids=[fake_id]), None),
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
    print(f"PASS: {named} tracked text files and their paths without stage labels")
    print(f"PASS: {moded} tracked text files and their paths without run-mode names (outside the history files)")
    print(f"PASS: {link_count} local Markdown file links; {len(validators)} JSON Schemas; 23 example contracts")
    print(f"PASS: example evidence hashes/ranges/counts; DAG; {len(required)} requirements traced; {openapi_refs} OpenAPI references")
    print(f"PASS: task stage enums (domain/OpenAPI/migration 0006/progress: {len(stage_order)} stages, {len(stage_states)} states)")
    print(f"PASS: {25 + len(section_cases) + len(role_negative) + observation_negatives} negative cases; example policy timing; prompt manifest")
    print("Prompt pack SHA-256:", digest.hexdigest())
    print(f"review instructions {pack_doc['version']} (Go-only selection):", ", ".join(f"{role} {value['digest'][:12]}" for role, value in role_files.items()))
    print("Scope: documentation/fixture contracts only; not CODEYE runtime or model-quality evidence.")


def check_additional_observation_example(report: dict, validator) -> int:
    observed = copy.deepcopy(report)
    row = observed["documents"]["comparisons"][0]
    row["status"] = "unknown"
    catalog = {e["id"]: e for e in observed["documents"]["evidence"]}
    source = catalog[row["code_evidence_ids"][0]]
    split = (source["start_line"] + source["end_line"]) // 2

    def window(start, end):
        evidence = dict(source, start_line=start, end_line=end)
        evidence.pop("excerpt", None)
        identity = f'{observed["inputs"]["manifest_sha256"]}\0{evidence["path"]}\0{evidence["blob_sha256"]}\0{start}:{end}'
        evidence["id"] = "ev_" + hashlib.sha256(identity.encode()).hexdigest()[:24]
        observed["documents"]["evidence"].append(evidence)
        catalog[evidence["id"]] = evidence
        return evidence["id"]

    prior = window(source["start_line"], split)
    covered = window(source["start_line"] + 1, split)
    new = window(split + 1, source["end_line"])
    row["code_evidence_ids"] = [prior]
    row["additional_observation"] = {"reason": "호출부에서 별도의 처리 함수까지 읽었지만 전체 경로는 미확인이다.",
                                     "code_evidence_ids": [covered, prior, new]}
    key = "review.contract"
    task = next(t for t in observed["tasks"] if t["key"] == key)
    task.update(status="succeeded")
    role = {"key": key, "result": "accepted", "task_status": "succeeded"}
    for roles in (observed["coverage"]["ai_review"]["optional_tasks"], observed["code_review"]["roles"]):
        roles[:] = [r for r in roles if r["key"] != key] + [role]
    calls = observed["coverage"]["review_calls"]
    calls.append(dict(calls[0], task_key=key, role="contract", outcome="accepted"))
    validator.validate(observed)
    check_document_comparisons(observed, catalog)

    invalid_refs = (("old evidence only", [covered, prior]), ("duplicate evidence", [prior, new, prior]),
                    ("unknown evidence", [new, "ev_" + "0" * 24]))
    for name, refs in invalid_refs:
        row["additional_observation"]["code_evidence_ids"] = refs
        expect_invalid(name, lambda r: check_document_comparisons(r, catalog), observed)

    row["additional_observation"]["code_evidence_ids"] = [prior, new]
    catalog[new] = dict(catalog[new], kind="test")
    expect_invalid("only a test adds lines", lambda r: check_document_comparisons(r, catalog), observed)
    catalog[new] = dict(catalog[new], kind="code", path="other/file.go")
    row["applies_to"] = source["path"]
    expect_invalid("new observation outside narrowed scope", lambda r: check_document_comparisons(r, catalog), observed)
    return len(invalid_refs) + 2


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, ValidationError) as exc:
        print("FAIL:", exc, file=sys.stderr)
        raise SystemExit(1)
