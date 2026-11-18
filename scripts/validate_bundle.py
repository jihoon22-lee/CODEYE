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
        if report["coverage"]["ai_review"]["status"] != "performed":
            ensure(report["status"] != "completed" and report["quality_verdict"] != "no_actionable_findings", "Incomplete AI review marked clean")
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
    print(f"PASS: {link_count} local Markdown file links; {len(validators)} JSON Schemas; 17 example contracts")
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
