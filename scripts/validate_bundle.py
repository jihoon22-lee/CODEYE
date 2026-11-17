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


def source_location(location: dict) -> Path:
    name = location["path"]
    relative = PurePosixPath(name)
    ensure(not relative.is_absolute() and ".." not in relative.parts, "Unsafe source path")
    ensure("\\" not in name and not re.match(r"^[A-Za-z]:", name), "Non-canonical source path")
    path = within_root(ROOT / name)
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


def check_report(report: dict) -> None:
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
            source_location(location)
        for item in evidence:
            if item["kind"] in ("code", "document"):
                ensure(item["location"]["snapshot_id"] == report["inputs"]["snapshot_id"], "Evidence snapshot mismatch")
                path = source_location(item["location"])
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
    schemas = {}
    resources = []
    for path in sorted((ROOT / "schemas").glob("*.json")):
        schema = json_file(path)
        Draft202012Validator.check_schema(schema)
        schemas[path.name] = schema
        resources.append((schema["$id"], Resource.from_contents(schema)))
    registry = Registry().with_resources(resources)
    validators = {name: Draft202012Validator(schema, registry=registry, format_checker=FormatChecker()) for name, schema in schemas.items()}
    validators["doctor.schema.json"].validate(json_file(ROOT / "examples/doctor.json"))
    validators["state.schema.json"].validate(json_file(ROOT / "examples/state.json"))
    state = schemas["state.schema.json"]["properties"]
    api = yaml_file(ROOT / "api/openapi.yaml")["components"]["schemas"]
    for field, name in (("run_status", "RunStatus"), ("attempt_status", "AttemptStatus"), ("cleanup_state", "CleanupState")):
        ensure(set(state[field]["enum"]) == set(api[name]["enum"]), "State/API enum mismatch: " + field)
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
    check_analyzer(analyzer)
    check_analyzer(go_vet)
    ensure(go_vet["exit_codes"]["findings"] == [], "go vet -json findings are not an exit-code class")
    check_observations(observations)
    review = json_file(ROOT / "examples/review-record.json")
    review_validator = validators["review-record.schema.json"]
    review_validator.validate(review)
    check_review_example(review)
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
    ensure(not (ROOT / "docs/02-benchmark.md").exists(), "Standalone comparison document is out of scope")
    print(f"PASS: {link_count} local Markdown file links; {len(schemas)} JSON Schemas; 10 example contracts")
    print(f"PASS: example evidence hashes/ranges/counts; DAG; {len(required)} requirements traced; {openapi_refs} OpenAPI references")
    print("PASS: 19 negative cases; example policy timing; prompt manifest")
    print("Prompt pack SHA-256:", digest.hexdigest())
    print("Scope: documentation/fixture contracts only; not CODEYE runtime or model-quality evidence.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, ValidationError) as exc:
        print("FAIL:", exc, file=sys.stderr)
        raise SystemExit(1)
