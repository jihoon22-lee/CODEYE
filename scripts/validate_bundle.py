#!/usr/bin/env python3
"""Validate CODEYE design artifacts, not the unimplemented application.

Checks local Markdown file links (not rendered anchors), JSON Schemas,
examples, evidence locations/hashes, counts, DAGs, prompt references,
OpenAPI references/path parameters, and a small set of negative cases.
It is not a complete OpenAPI semantic validator or a product E2E test.
"""
from __future__ import annotations

import copy
import hashlib
import json
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
    for path in ROOT.rglob("*.md"):
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
    plan = json_file(ROOT / "examples/plan.json")
    report = json_file(ROOT / "examples/report.json")
    analyzer = yaml_file(ROOT / "examples/analyzer-ruff.yaml")
    validators["plan.schema.json"].validate(plan)
    validators["report.schema.json"].validate(report)
    validators["analyzer.schema.json"].validate(analyzer)
    check_plan(plan)
    check_report(report)
    check_analyzer(analyzer)
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
    ensure(not (ROOT / "docs/02-benchmark.md").exists(), "Standalone comparison document is out of scope")
    print(f"PASS: {link_count} local Markdown file links; {len(schemas)} JSON Schemas; 3 example contracts")
    print(f"PASS: example evidence hashes/ranges/counts; DAG; {len(required)} requirements traced; {openapi_refs} OpenAPI references")
    print("PASS: 6 negative cases; example policy timing; prompt manifest")
    print("Prompt pack SHA-256:", digest.hexdigest())
    print("Scope: documentation/fixture contracts only; CODEYE runtime and model quality remain untested.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, OSError, ValidationError) as exc:
        print("FAIL:", exc, file=sys.stderr)
        raise SystemExit(1)
