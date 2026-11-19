"""Review record, environment example and role pack checks."""

from __future__ import annotations

import hashlib
from pathlib import PurePosixPath

from .common import ROOT, ensure, within_root, yaml_file


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
