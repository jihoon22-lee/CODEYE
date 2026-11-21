"""Review record, environment example and review instruction checks."""

from __future__ import annotations

import copy
import hashlib
import json
import re
from pathlib import Path, PurePosixPath

from .common import ROOT, ensure, expect_invalid, source_location, within_root, yaml_file


def document_scope(path: str) -> str:
    p = PurePosixPath(path)
    if p.suffix.lower() not in {".md", ".markdown", ".mdx", ".rst", ".adoc", ".asciidoc"}:
        return str(p.parent)
    name = p.name.lower()
    if (name.startswith(("readme", "contributing")) or name in {"agents.md", "agent.md", "claude.md",
            "claude.local.md", "gemini.md"} or (name == "copilot-instructions.md" and p.parent.name.lower() == ".github")):
        return str(p.parent)
    dirs = p.parent.parts
    for i in range(len(dirs) - 1, -1, -1):
        if dirs[i].lower() in {"docs", "doc"}:
            return str(PurePosixPath(*dirs[:i]))
    return str(p.parent)


def evidence_document_scope(evidence: dict, root: Path | None = None) -> str:
    directive = evidence.get("directive_path")
    if directive is None:
        return document_scope(evidence["path"])
    page = PurePosixPath(directive)
    scope = document_scope(directive)
    src = PurePosixPath(evidence["path"])
    ensure(evidence["kind"] == "document" and src.suffix == ".py" and page.suffix.lower() in {".md", ".markdown"}
           and not page.is_absolute() and ".." not in page.parts and src.is_relative_to(PurePosixPath(scope)),
           "Python API documentation provenance is invalid")
    if root is not None:
        ensure((root / page).is_file(), "Python API documentation page is missing")
    return scope


def package_reference_scope(evidence: dict, root: Path | None = None, redacted: bool = False) -> str | None:
    package = evidence.get("package_reference")
    if package is None:
        return None
    manifest, entry = (PurePosixPath(package[field]) for field in ("manifest_path", "entry_path"))
    doc = PurePosixPath(evidence["path"])
    scope = manifest.parent
    runtime_exts = {".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts"}
    excluded_dirs = {"dist", "build", "out", "node_modules", "vendor", "third_party", "testdata", "test", "tests", "__tests__"}
    ensure(evidence["kind"] == "document" and "directive_path" not in evidence and doc.suffix.lower() in {".md", ".mdx", ".markdown"}
           and manifest.name == "package.json" and not manifest.is_absolute() and ".." not in manifest.parts
           and not entry.is_absolute() and ".." not in entry.parts and entry.is_relative_to(scope)
           and entry.suffix.lower() in runtime_exts and not excluded_dirs.intersection(p.lower() for p in entry.parent.parts)
           and not entry.name.lower().endswith((".d.ts", ".d.mts", ".d.cts", ".test-d.ts"))
           and entry.name.lower() not in {"test.js", "test.ts"} and not entry.stem.lower().endswith((".test", ".spec"))
           and ".min." not in entry.name.lower()
           and not doc.is_relative_to(scope) and not scope.is_relative_to(PurePosixPath(evidence_document_scope(evidence))),
           "Package documentation provenance is invalid")
    ensure(all((field in package) != redacted for field in ("name", "callable", "export")), "Package names violate report export mode")
    if root is not None:
        for key, p in (("manifest", manifest), ("entry", entry)):
            ensure(hashlib.sha256((root / p).read_bytes()).hexdigest() == package[key + "_sha256"],
                   "Package documentation blob mismatch: " + str(p))
        if not redacted:
            ensure(json.loads((root / manifest).read_bytes()).get("name") == package["name"], "Package name differs from its manifest")
    return str(scope)


def document_claim_covers(evidence: list[dict], scope: str, statement: str, redacted: bool = False) -> bool:
    outside = [e for e in evidence if not PurePosixPath(scope).is_relative_to(PurePosixPath(evidence_document_scope(e)))]
    if not outside:
        return True
    def names(name: str) -> bool:
        boundary = r"a-zA-Z0-9_.$@/\-"
        return re.search(rf"(?<![{boundary}]){re.escape(name)}(?![{boundary}])", statement) is not None

    anchors = [e for e in outside if "package_reference" in e]
    for anchor in anchors:
        if package_reference_scope(anchor, redacted=redacted) != scope:
            return False
        package = anchor["package_reference"]
        if not redacted and not (names(package["callable"]) or (package["export"] != "default" and names(package["name"]) and names(package["export"]))):
            return False
    return bool(anchors) and all(any(
        "directive_path" not in doc and doc["path"] == anchor["path"]
        and doc.get("blob_sha256", doc.get("sha256")) == anchor.get("blob_sha256", anchor.get("sha256"))
        and (anchor["start_line"] - doc["end_line"] == 1 or doc["start_line"] - anchor["end_line"] == 1)
        for anchor in anchors) for doc in outside if "package_reference" not in doc)


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
        ensure(d["applies_to"] in (str(PurePosixPath(d["path"]).parent), document_scope(d["path"])),
               "Unexpected document applicability: " + d["path"])
        if d["sha256"] is not None:
            body = (root / d["path"]).read_bytes()
            ensure(hashlib.sha256(body).hexdigest() == d["sha256"] and len(body) == d["size_bytes"], "Document blob mismatch: " + d["path"])


ROLE_STAGES = ("environment", "documents", "investigate", "interpret", "verify")
PACK_ROLES = (*ROLE_STAGES, "contract")


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


# Language guides follow the snapshot's in-scope source ecosystems (A29).
# The example snapshot has Go source only; TestReviewExampleRecords checks
# that selection against the worker.
ROLE_LANGUAGES = ("cpp", "go", "javascript", "python", "rust")
ROLE_EXAMPLE_LANGUAGES = ("go",)


def pack_selection(languages=ROLE_EXAMPLE_LANGUAGES) -> tuple[dict, dict]:
    """The review instructions: identity, files and per-role digests (sha256
    over pack.yaml and the role's selected files) for the given in-scope
    ecosystems."""
    pack_file = ROOT / "prompts/review/pack.yaml"
    pack = yaml_file(pack_file)
    ensure(pack["id"] == "review" and pack["status"] == "default" and pack["schema_version"] == "0.2", "review pack identity")
    ensure(tuple(pack["roles"]) == PACK_ROLES, "review roles")
    ensure(tuple(pack["languages"]) == ROLE_LANGUAGES and all(f == f"languages/{eco}.md" for eco, f in pack["languages"].items()), "review language guides")
    for guide in pack["languages"].values():
        ensure(within_root(pack_file.parent / guide).is_file(), "Missing review prompt: " + guide)
    selected = [pack["languages"][eco] for eco in sorted(set(languages)) if eco in pack["languages"]]
    roles = {}
    for role, file in pack["roles"].items():
        names = sorted({"pack.yaml", pack["core"], file, pack["domains"], *selected})
        digest = hashlib.sha256()
        for relative in names:
            path = within_root(pack_file.parent / relative)
            ensure(path.is_file(), "Missing review prompt: " + relative)
            digest.update(relative.encode("utf-8") + b"\0" + path.read_bytes() + b"\0")
        roles[role] = {"files": names, "digest": digest.hexdigest()}
    return pack, roles


def check_stage_examples(docs: dict, raw: dict, pack: dict, roles: dict) -> None:
    """Bind the review stage examples to the fixture bytes, the pack
    files, each other (upstream SHA-256, candidates) and the scope rules."""
    root = ROOT / "fixtures/modules/input"
    first = docs["environment"]
    for stage in ROLE_STAGES:
        doc = docs[stage]
        ensure(doc["stage"] == stage and doc["pack"] == {"id": pack["id"], "version": pack["version"]}, "review stage or pack: " + stage)
        ensure(all(doc[k] == first[k] for k in ("snapshot_manifest_digest", "run_id", "attempt_id")), "review examples of different attempts")
        catalog = {}
        for e in doc["evidence"]:
            body = (root / e["path"]).read_bytes()
            sha = hashlib.sha256(body).hexdigest()
            ensure(sha == e["sha256"] and 1 <= e["start_line"] <= e["end_line"] <= len(body.splitlines()), "review evidence blob or range: " + e["path"])
            binding = "\0".join([doc["snapshot_manifest_digest"], e["path"], sha, f'{e["start_line"]}:{e["end_line"]}'])
            ensure(e["id"] == "ev_" + hashlib.sha256(binding.encode()).hexdigest()[:24] and e["id"] not in catalog, "review evidence ID")
            evidence_document_scope(e, root)
            package_reference_scope(e, root)
            catalog[e["id"]] = e
        cited = []
        for key in ("environment_notes", "contracts", "conflicts", "questions", "instruction_notes", "candidates", "verifications"):
            for item in doc.get(key, []):
                for field in ("evidence_ids", "first_evidence_ids", "second_evidence_ids", "supporting_evidence_ids", "counterevidence_ids"):
                    cited.extend(item.get(field, []))
        ensure(all(ref in catalog for ref in cited), "review cites evidence it was not given: " + stage)
        # A rule of a sub-directory is never generalized to a parent scope.
        def covers(ids, scope):
            dirs = [evidence_document_scope(catalog[i]) for i in ids]
            return all(d == "." or scope == d or scope.startswith(d + "/") for d in dirs)
        for group, field in (("contracts", "statement"), ("questions", "question")):
            for c in doc.get(group, []):
                ensure(document_claim_covers([catalog[id] for id in c["evidence_ids"]], c["applies_to"], c[field]),
                       "review scope generalized: " + c["applies_to"])
        for c in doc.get("conflicts", []):
            ensure(covers(c["first_evidence_ids"] + c["second_evidence_ids"], c["scope"]), "review conflict scope generalized")
        observations = {o["id"] for o in doc["observations"]}
        for c in doc.get("candidates", []):
            ensure(set(c.get("observation_ids", [])) <= observations, "review candidate cites an unknown observation")
        for call in doc["calls"]:
            ensure(call["role"] == stage and call["pack_files"] == roles[stage]["files"] and call["pack_sha256"] == roles[stage]["digest"], "review call pack digest: " + stage)
            ensure(call["pack_id"] == pack["id"] and call["pack_version"] == pack["version"], "review call pack identity")
        for u in doc["upstream"]:
            if u["state"] == "accepted" and u["source"].startswith("review."):
                sibling = u["source"][len("review."):]
                ensure(hashlib.sha256(compact_json(raw[sibling])).hexdigest() == u["sha256"], "review upstream digest: " + u["source"])
    verify = docs["verify"]
    upstream = docs["investigate"].get("candidates", []) + docs["interpret"].get("candidates", [])
    ensure(verify.get("candidates", []) == upstream, "verify candidates differ from the accepted upstream candidates")
    ensure([c["id"] for c in verify.get("candidates", [])] == [v["candidate_id"] for v in verify.get("verifications", [])], "review verification coverage")


def check_probe_example(validator, role_docs: dict, role_raw: dict) -> int:
    """Check the independently read probe location against the overview and snapshot."""
    raw = (ROOT / "examples/review/investigate-probe.json").read_text(encoding="utf-8")
    probe_example = json.loads(raw)
    root = ROOT / "fixtures/modules/input"

    def check(doc: dict) -> None:
        validator.validate(doc)
        base = role_docs["investigate"]
        ensure(all(doc[k] == base[k] for k in ("snapshot_manifest_digest", "run_id", "attempt_id", "pack")), "probe attempt identity")
        upstream = {u["source"]: u for u in doc["upstream"]}
        docs_source = upstream["review.documents"]
        ensure(docs_source["state"] == "accepted" and docs_source["sha256"] == hashlib.sha256(compact_json(role_raw["documents"])).hexdigest(), "probe documents digest")
        probe = doc["contract_probe"]
        idx = probe["contract_index"]
        comparisons = [c for c in doc["contract_comparisons"] if c["contract_index"] == idx]
        contracts = role_docs["documents"]["contracts"]
        ensure(idx <= len(contracts) and len(comparisons) == 1 and comparisons[0]["status"] == "unknown", "probe unresolved comparison")
        scope = comparisons[0]["applies_to"]
        ensure(PurePosixPath(scope).is_relative_to(PurePosixPath(contracts[idx - 1]["applies_to"])) and
               comparisons[0]["document_evidence_ids"] == contracts[idx - 1]["evidence_ids"], "probe scope or document citation differs")
        ids = [c["id"] for c in probe["candidates"]]
        ensure(len(ids) == len(set(ids)), "duplicate probe location")
        for candidate in probe["candidates"]:
            ensure(PurePosixPath(candidate["path"]).is_relative_to(PurePosixPath(scope)), "probe source outside comparison scope")
            path = source_location(candidate, root)
            sha = hashlib.sha256(path.read_bytes()).hexdigest()
            start, end = candidate["start_line"], candidate["end_line"]
            binding = "\0".join([doc["snapshot_manifest_digest"], candidate["path"], sha, f"{start}:{end}"])
            ensure(candidate["sha256"] == sha and end - start < 16 and
                   candidate["id"] == "loc_" + hashlib.sha256(binding.encode()).hexdigest()[:24], "probe location binding")
            if symbol := candidate.get("symbol"):
                excerpt = "\n".join(path.read_text(encoding="utf-8").splitlines()[start - 1:end])
                ensure(re.search(r"(?<![\w$])" + re.escape(symbol) + r"(?![\w$])", excerpt), "probe source symbol missing")

    check(probe_example)
    negatives = {
        "probe on a legacy record": lambda d: d.update(schema_version="0.2"),
        "probe for another contract": lambda d: d["contract_probe"].update(contract_index=1),
        "probe literal expression": lambda d: d["contract_probe"].update(literals=["Charge()"]),
        "probe location used as evidence": lambda d: d["contract_comparisons"][0].update(code_evidence_ids=[d["contract_probe"]["candidates"][0]["id"]]),
        "probe location digest drift": lambda d: d["contract_probe"]["candidates"][0].update(sha256="0" * 64),
        "probe location outside file": lambda d: d["contract_probe"]["candidates"][0].update(end_line=1000),
        "probe invented source symbol": lambda d: d["contract_probe"]["candidates"][0].update(symbol="Absent"),
    }
    for name, edit in negatives.items():
        d = copy.deepcopy(probe_example)
        edit(d)
        expect_invalid(name, check, d)
    return len(negatives)


def check_workflow_example(validator, pack: dict, roles: dict) -> int:
    root = ROOT / "fixtures/modules/workflows/input"
    example = json.loads((ROOT / "examples/review/documents-workflow.json").read_text(encoding="utf-8"))

    def check(doc: dict) -> None:
        validator.validate(doc)
        ensure(doc["pack"] == {"id": pack["id"], "version": pack["version"]}, "workflow document pack")
        for call in doc["calls"]:
            ensure(call["pack_files"] == roles["documents"]["files"] and call["pack_sha256"] == roles["documents"]["digest"],
                   "workflow document role digest")
        selected = doc.get("workflow_selections", [])
        indices = [entry["contract_index"] for entry in selected]
        ensure(indices == sorted(set(indices)) and (not indices or indices[-1] <= len(doc["contracts"])) and
               sum(len(entry["route_digests"]) for entry in selected) <= 4, "workflow routes exceed contract or selection bounds")
        for excerpt in doc["evidence"]:
            p = source_location(excerpt, root)
            sha = hashlib.sha256(p.read_bytes()).hexdigest()
            bind = "\0".join([doc["snapshot_manifest_digest"], excerpt["path"], sha, f'{excerpt["start_line"]}:{excerpt["end_line"]}'])
            ensure(excerpt["kind"] == "document" and excerpt["sha256"] == sha and
                   excerpt["id"] == "ev_" + hashlib.sha256(bind.encode()).hexdigest()[:24], "workflow document evidence")

    check(example)
    no_choice = copy.deepcopy(example)
    no_choice.pop("workflow_selections")
    no_choice["calls"].append(dict(no_choice["calls"][0], attempt=2, purpose="workflow_selection",
                                  outcome="output_rejected", output_errors=["workflow_selection_invalid"]))
    check(no_choice)
    negatives = {
        "workflow selections on a legacy record": lambda d: d.update(schema_version="0.2"),
        "workflow selection of absent contract": lambda d: d["workflow_selections"][0].update(contract_index=2),
        "workflow selection repeats contract": lambda d: d["workflow_selections"].append({"contract_index": 1, "route_digests": ["0" * 64]}),
        "workflow alias persisted as selection": lambda d: d["workflow_selections"][0].update(route_digests=["R1"]),
        "workflow call before accepted document response": lambda d: d["calls"].insert(0, no_choice["calls"][1]),
        "workflow call repeated": lambda d: d["calls"].extend([no_choice["calls"][1], dict(no_choice["calls"][1], attempt=3)]),
    }
    for name, edit in negatives.items():
        bad = copy.deepcopy(example)
        edit(bad)
        expect_invalid(name, check, bad)
    return len(negatives)


def check_contract_example(validator, pack: dict, roles: dict) -> int:
    """Verify the snapshot and prior comparison for a scripted final role."""
    fixture = ROOT / "fixtures/modules/input"
    directory = ROOT / "examples/review/contract"
    raw = {stage: (directory / f"{stage}.json").read_text(encoding="utf-8")
           for stage in ("documents", "investigate", "contract")}
    documents, investigate, final = (json.loads(raw[stage]) for stage in raw)
    for parent in (documents, investigate):
        validator.validate(parent)

    def check(final):
        validator.validate(final)
        identity = ("snapshot_manifest_digest", "run_id", "attempt_id")
        ensure(all(final[key] == documents[key] == investigate[key] for key in identity), "contract example identity")
        expected_upstream = {f"review.{stage}": hashlib.sha256(compact_json(raw[stage])).hexdigest()
                             for stage in ("documents", "investigate")}
        ensure(len(final["upstream"]) == 2 and {u["source"]: u["sha256"] for u in final["upstream"]} == expected_upstream,
               "contract example parent digest")
        ensure(final["pack"] == {"id": pack["id"], "version": pack["version"]}, "contract example pack")
        call, = final["calls"]
        ensure(call["attempt"] == 1 and call["outcome"] == "accepted" and call["pack_files"] == roles["contract"]["files"]
               and call["pack_sha256"] == roles["contract"]["digest"], "contract example call provenance")
        comparison, = final["contract_comparisons"]
        index = investigate["contract_probe"]["contract_index"]
        prior = next(x for x in investigate["contract_comparisons"] if x["contract_index"] == index)
        ensure(prior["status"] == "unknown" and comparison["status"] == "match", "contract example transition")
        ensure(all(comparison[key] == prior[key] for key in ("contract_index", "applies_to", "document_evidence_ids"))
               and comparison["document_evidence_ids"] == documents["contracts"][index - 1]["evidence_ids"], "contract example scope or document")
        evidence = {}
        for e in final["evidence"]:
            body = (fixture / e["path"]).read_bytes()
            sha = hashlib.sha256(body).hexdigest()
            start, end = e["start_line"], e["end_line"]
            binding = "\0".join([final["snapshot_manifest_digest"], e["path"], sha, f"{start}:{end}"])
            ensure(sha == e["sha256"] and 1 <= start <= end <= len(body.split(b"\n"))
                   and e["id"] not in evidence and e["id"] == "ev_" + hashlib.sha256(binding.encode()).hexdigest()[:24],
                   "contract evidence binding")
            evidence[e["id"]] = e
        for evidence_id in comparison["document_evidence_ids"]:
            ensure(evidence_id in evidence and evidence[evidence_id]["kind"] == "document"
                   and all(evidence[evidence_id] in parent["evidence"] for parent in (documents, investigate)), "contract document provenance")
        fresh_code = False
        for evidence_id in comparison["code_evidence_ids"]:
            ensure(evidence_id in evidence, "contract comparison cites an unknown excerpt")
            e = evidence[evidence_id]
            scope = comparison["applies_to"]
            ensure(e["kind"] in {"code", "test"} and (scope == "." or e["path"] == scope or e["path"].startswith(scope + "/")),
                   "contract cites out-of-scope or non-code evidence")
            fresh_code |= e["kind"] == "code" and any(
                not any(old["kind"] == "code" and old["path"] == e["path"] and old["start_line"] <= line <= old["end_line"]
                        for old in investigate["evidence"])
                for line in range(e["start_line"], e["end_line"] + 1))
        ensure(fresh_code, "contract finding without fresh scoped implementation")

    check(final)

    def case(mutation):
        changed = copy.deepcopy(final)
        mutation(changed)
        return changed

    cases = {
        "contract on schema 0.2": lambda d: d.update(schema_version="0.2"),
        "contract omitted parent": lambda d: d["upstream"].pop(),
        "contract unbound parent": lambda d: d["upstream"][0].update(sha256="0" * 64),
        "contract changed scope": lambda d: d["contract_comparisons"][0].update(applies_to="svc/b"),
        "contract without implementation": lambda d: d["contract_comparisons"][0].update(code_evidence_ids=[]),
        "contract with documentation as implementation": lambda d: d["contract_comparisons"][0].update(
            code_evidence_ids=d["contract_comparisons"][0]["document_evidence_ids"]),
        "contract with three calls": lambda d: d["calls"].extend(d["calls"] * 2),
        "partial without matching and violating conditions": lambda d: d["contract_comparisons"][0].update(status="partial"),
    }
    for name, mutation in cases.items():
        expect_invalid(name, check, case(mutation))
    return len(cases)


# The slice examples (docs/REVIEW.md#모듈-단위-검토-분할): the review.scope
# artifact and the first slice's records of fixtures/modules/slices/input,
# written by TestSliceExampleRecords. That snapshot has Go, JavaScript and
# Python source.
SLICE_EXAMPLE_LANGUAGES = ("go", "javascript", "python")
SLICE_EXAMPLE_FILES = ("scope", "investigate.s001", "verify.s001")


def slice_examples() -> dict:
    return {name: (ROOT / "examples/review/slices" / (name + ".json")).read_text(encoding="utf-8") for name in SLICE_EXAMPLE_FILES}


def secondary_segment(rule: dict, path: str):
    """The secondary-path segment of the rule a file path's directories
    match (case-insensitive; a segment with / matches consecutive
    directories; not_for_go segments skip .go files), or None. Mirrors
    record.SecondaryRule.Match."""
    dirs = path.lower().split("/")[:-1]
    for seg in rule["segments"]:
        if path.endswith(".go") and seg in rule["not_for_go"]:
            continue
        parts = seg.split("/")
        if any(dirs[i:i + len(parts)] == parts for i in range(len(dirs) - len(parts) + 1)):
            return seg
    return None


def check_scope_artifact(scope: dict) -> None:
    """The scope artifact rules the schema cannot express: blobs of the
    fixture, every eligible file in exactly one place, covered_by_overview
    exactly for the files the overview sent, per-module counts, slot keys in
    order within the plan's slots, file classes from the recorded rule and
    primary slots before secondary ones (docs/REVIEW.md#검토-우선순위)."""
    root = ROOT / "fixtures/modules/slices/input"
    offered = scope["overview"]["offered"]
    ensure(offered == sorted(offered), "scope offered files are not sorted")
    placed = {}
    files = list(scope["overview"]["files"]) + [f for s in scope["slices"] for f in s["files"]] + list(scope.get("excluded", []))
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
    if scope["schema_version"] == "0.1":
        return
    ensure(len(scope["slices"]) <= scope["limits"]["slots"], "scope fills more slots than the plan made")
    rule = scope["priority"]["secondary"]
    for f in files:
        seg = secondary_segment(rule, f["path"])
        want = "secondary_path:" + seg if seg else f.get("secondary") if f.get("secondary") == "generated_file" else None
        ensure(f.get("secondary") == want and (f["class"] == "secondary") == (want is not None), "scope file class differs from the rule: " + f["path"])
    for m in scope["modules"]:
        own = [f for f in files if f["module"] == m["id"]]
        ensure(m["files_secondary"] >= sum(1 for f in own if f["class"] == "secondary") and m["files_secondary_not_planned"] <= min(m["files_secondary"], m["files_not_planned"]), "scope module secondary count: " + m["id"])
    classes = [s["class"] for s in scope["slices"]]
    ensure(classes == sorted(classes, key=lambda c: c == "secondary"), "scope secondary slot before a primary one")
    for s in scope["slices"]:
        ensure(s["class"] == s["files"][0]["class"] and s["rank"] == s["files"][0]["rank"] and all(f["class"] == s["class"] for f in s["files"]), "scope slot class differs from its files: " + s["key"])


def check_slice_examples(raw: dict, validators: dict) -> int:
    """Validate and bind the slice examples; returns the negative cases run."""

    record_v, scope_v = validators["review-record.schema.json"], validators["review-scope.schema.json"]
    docs = {name: json.loads(text) for name, text in raw.items()}
    pack, roles = pack_selection(SLICE_EXAMPLE_LANGUAGES)

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
        case("slice key outside s001..s096", lambda d: d["verify.s001"]["slice"].update(key="s097")),
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
        case("scope slot key beyond s096", lambda d: d["scope"]["slices"][0].update(key="s097")),
        case("scope fills more slots than planned", lambda d: d["scope"]["limits"].update(slots=len(d["scope"]["slices"]) - 1)),
        case("scope slot limit other than 96", lambda d: d["scope"]["limits"].update(max_slices=32)),
        case("scope without its priority rule", lambda d: d["scope"].pop("priority")),
        case("scope secondary file without a reason", lambda d: first_file(d).update({"class": "secondary"})),
        case("scope primary file below a secondary path", lambda d: d["scope"]["priority"]["secondary"]["segments"].append("store")),
        case("scope rank outside the enum", lambda d: first_file(d).update(rank="core")),
        case("scope severity without observations", lambda d: first_file(d).update(severity="high")),
        case("scope planned file in the excluded list", lambda d: d["scope"]["excluded"].append(dict(d["scope"]["slices"][-1]["files"].pop(), state="planned"))),
        case("scope 0.1 shape labelled 0.2", lambda d: [d["scope"].pop("priority"), d["scope"].pop("excluded"), d["scope"].update(schema_version="0.1")]),
    ]
    for name, d in negative:
        expect_invalid(name, check, d)
    return len(negative)
