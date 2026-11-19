"""Review record, environment example and role pack checks."""

from __future__ import annotations

import copy
import hashlib
import json
from pathlib import PurePosixPath

from .common import ROOT, ensure, expect_invalid, within_root, yaml_file


def check_environment_example(doc: dict) -> None:
    """Bind the discovery examples to the fixture input bytes and evidence IDs."""
    root = ROOT / "fixtures/modules/input"
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


ROLE_STAGES = ("environment", "documents", "investigate", "interpret", "verify")


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


# Language guides of review-r1 follow the snapshot's in-scope source
# ecosystems (A29). The example snapshot has Go source only;
# TestRolePackExampleRecords checks that selection against the worker.
ROLE_LANGUAGES = ("cpp", "go", "javascript", "python", "rust")
ROLE_EXAMPLE_LANGUAGES = ("go",)


def role_pack(languages=ROLE_EXAMPLE_LANGUAGES) -> tuple[dict, dict]:
    """The review-r1 default pack: identity, files and per-role digests
    (review-v1 algorithm over pack.yaml and the role's selected files) for
    the given in-scope ecosystems."""
    pack_file = ROOT / "prompts/review-r1/pack.yaml"
    pack = yaml_file(pack_file)
    ensure(pack["id"] == "review-r1" and pack["status"] == "default" and pack["schema_version"] == "0.2", "review-r1 pack identity")
    ensure(tuple(pack["roles"]) == ROLE_STAGES, "review-r1 roles")
    ensure(tuple(pack["languages"]) == ROLE_LANGUAGES and all(f == f"languages/{eco}.md" for eco, f in pack["languages"].items()), "review-r1 language guides")
    for guide in pack["languages"].values():
        ensure(within_root(pack_file.parent / guide).is_file(), "Missing review-r1 prompt: " + guide)
    selected = [pack["languages"][eco] for eco in sorted(set(languages)) if eco in pack["languages"]]
    roles = {}
    for role, file in pack["roles"].items():
        names = sorted({"pack.yaml", pack["core"], file, pack["domains"], *selected})
        digest = hashlib.sha256()
        for relative in names:
            path = within_root(pack_file.parent / relative)
            ensure(path.is_file(), "Missing review-r1 prompt: " + relative)
            digest.update(relative.encode("utf-8") + b"\0" + path.read_bytes() + b"\0")
        roles[role] = {"files": names, "digest": digest.hexdigest()}
    # The domain pack and the Go guide are copies of review-v1's; the other
    # guides are review-r1's own.
    v1 = ROOT / "prompts/review-v1"
    for shared in [pack["domains"], pack["languages"]["go"]]:
        ensure((pack_file.parent / shared).read_bytes() == (v1 / shared).read_bytes(), "review-r1 copy differs from review-v1: " + shared)
    return pack, roles


def check_role_pack_examples(docs: dict, raw: dict, pack: dict, roles: dict) -> None:
    """Bind the review-r1 stage examples to the w15 fixture bytes, the pack
    files, each other (upstream SHA-256, candidates) and the scope rules."""
    root = ROOT / "fixtures/modules/input"
    first = docs["environment"]
    for stage in ROLE_STAGES:
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


# The slice examples (docs/REVIEW.md#모듈-단위-검토-분할): the review.scope
# artifact and the first slice's records of fixtures/modules/slices/input,
# written by TestSliceExampleRecords. That snapshot has Go, JavaScript and
# Python source.
SLICE_EXAMPLE_LANGUAGES = ("go", "javascript", "python")
SLICE_EXAMPLE_FILES = ("scope", "investigate.s001", "verify.s001")


def slice_examples() -> dict:
    return {name: (ROOT / "examples/review-r1/slices" / (name + ".json")).read_text(encoding="utf-8") for name in SLICE_EXAMPLE_FILES}


def check_scope_artifact(scope: dict) -> None:
    """The scope artifact rules the schema cannot express: blobs of the
    fixture, every eligible file in exactly one place, covered_by_overview
    exactly for the files the overview sent, per-module counts and slice
    keys in plan order."""
    root = ROOT / "fixtures/modules/slices/input"
    offered = scope["overview"]["offered"]
    ensure(offered == sorted(offered), "scope offered files are not sorted")
    placed = {}
    files = list(scope["overview"]["files"]) + [f for s in scope["slices"] for f in s["files"]]
    for f in files:
        ensure(f["path"] not in placed, "scope file in two places: " + f["path"])
        placed[f["path"]] = f
        ensure(hashlib.sha256((root / f["path"]).read_bytes()).hexdigest() == f["sha256"], "scope file blob: " + f["path"])
        ensure((f["state"] == "covered_by_overview") == (f["path"] in offered), "scope covered_by_overview differs from offered: " + f["path"])
        ensure(f["planned_bytes"] <= f["bound_bytes"], "scope planned bytes above the bound: " + f["path"])
    ensure(set(offered) <= set(placed), "scope offered file outside the plan")
    for m in scope["modules"]:
        held = sum(1 for f in files if f["module"] == m["id"])
        ensure(held + m["files_not_planned"] == m["files_eligible"], "scope module count: " + m["id"])
    for i, s in enumerate(scope["slices"]):
        key = f"s{i + 1:03d}"
        ensure(s["key"] == key and s["investigate_task"] == "review.investigate." + key and s["verify_task"] == "review.verify." + key, "scope slice keys out of order")
        ensure({f["module"] for f in s["files"]} <= set(s["modules"]), "scope slice file outside its modules: " + key)


def check_slice_examples(raw: dict, validators: dict) -> int:
    """Validate and bind the slice examples; returns the negative cases run."""

    record_v, scope_v = validators["review-record.schema.json"], validators["review-scope.schema.json"]
    docs = {name: json.loads(text) for name, text in raw.items()}
    pack, roles = role_pack(SLICE_EXAMPLE_LANGUAGES)

    def check(docs: dict) -> None:
        scope = docs["scope"]
        scope_v.validate(scope)
        check_scope_artifact(scope)
        root = ROOT / "fixtures/modules/slices/input"
        entry = scope["slices"][0]
        for name in ("investigate.s001", "verify.s001"):
            doc = docs[name]
            record_v.validate(doc)
            stage = name.split(".")[0]
            ensure(doc["schema_version"] == "0.3" and doc["stage"] == stage and doc["slice"] == {"key": entry["key"], "modules": entry["modules"]}, "slice record identity: " + name)
            ensure(all(doc[k] == scope[k] for k in ("snapshot_manifest_digest", "run_id", "attempt_id", "pack")), "slice record of another attempt: " + name)
            for e in doc["evidence"]:
                body = (root / e["path"]).read_bytes()
                sha = hashlib.sha256(body).hexdigest()
                binding = "\0".join([doc["snapshot_manifest_digest"], e["path"], sha, f'{e["start_line"]}:{e["end_line"]}'])
                ensure(sha == e["sha256"] and e["id"] == "ev_" + hashlib.sha256(binding.encode()).hexdigest()[:24], "slice evidence blob or ID: " + e["path"])
            for call in doc["calls"]:
                ensure(call["role"] == stage and call["pack_files"] == roles[stage]["files"] and call["pack_sha256"] == roles[stage]["digest"], "slice call pack digest: " + name)
            by_source = {u["source"]: u for u in doc["upstream"]}
            ensure(by_source.get("review.scope", {}).get("sha256") == hashlib.sha256(compact_json(raw["scope"])).hexdigest(), "slice upstream scope digest: " + name)
        planned = {f["path"] for f in entry["files"] if f["state"] == "planned"}
        sent = {e["path"] for e in docs["investigate.s001"]["evidence"] if e.get("origin") == "context" and e["kind"] in ("code", "test")}
        ensure(sent and sent <= planned, "slice sent files outside its planned files")
        verify = docs["verify.s001"]
        inv = {u["source"]: u for u in verify["upstream"]}.get("review.investigate.s001", {})
        ensure(inv.get("sha256") == hashlib.sha256(compact_json(raw["investigate.s001"])).hexdigest(), "slice verify upstream investigate digest")
        ensure(verify["candidates"] == docs["investigate.s001"]["candidates"], "slice verify candidates differ from its investigate candidates")

    check(docs)

    def case(name: str, edit) -> tuple[str, dict]:
        d = copy.deepcopy(docs)
        edit(d)
        return name, d

    def first_file(d):
        return d["scope"]["slices"][1]["files"][1]

    negative = [
        case("slice record without slice", lambda d: d["investigate.s001"].pop("slice")),
        case("0.2 record with a slice", lambda d: d["investigate.s001"].update(schema_version="0.2")),
        case("slice key outside s001..s032", lambda d: d["verify.s001"]["slice"].update(key="s033")),
        case("slice of nine modules", lambda d: d["investigate.s001"]["slice"].update(modules=[f"go:m{i}" for i in range(9)])),
        case("slice verify given an interpret candidate", lambda d: d["verify.s001"]["candidates"][0].update(origin="interpret")),
        case("slice interpret record", lambda d: d["investigate.s001"].update(stage="interpret")),
        case("whole-run record bound to a slice", lambda d: [d["investigate.s001"].pop("slice"), d["investigate.s001"].update(schema_version="0.2")]),
        case("scope file in two slices", lambda d: d["scope"]["slices"][2]["files"].append(dict(first_file(d)))),
        case("scope offered not sorted", lambda d: d["scope"]["overview"]["offered"].reverse()),
        case("scope covered file the overview did not send", lambda d: first_file(d).update(state="covered_by_overview")),
        case("scope state outside the enum", lambda d: first_file(d).update(state="verified")),
        case("scope not_planned miscount", lambda d: d["scope"]["modules"][0].update(files_not_planned=1)),
        case("scope screen outside the enum", lambda d: d["scope"].update(screen="unscreened")),
        case("scope not_screened without its limitation", lambda d: d["scope"].update(screen="not_screened")),
        case("scope ceiling above 207", lambda d: d["scope"]["budget"].update(max_calls=208)),
        case("scope slice out of order", lambda d: d["scope"]["slices"].reverse()),
    ]
    for name, d in negative:
        expect_invalid(name, check, d)
    return len(negative)
