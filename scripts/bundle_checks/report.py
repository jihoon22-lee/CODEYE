"""Plan, report and analyzer observation checks."""

from __future__ import annotations

import hashlib
from pathlib import Path
import re

from .common import ROOT, ensure, source_location


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
            # Analyzer documents may cite either recorded stream (cppcheck writes its XML to stderr).
            ensure(row["role"] in ("stdout", "stderr") and row["sha256"] == raw["sha256"] and row["task_key"] == obs["task_key"], "Incorrect raw stream")
            ensure(0 <= raw["byte_start"] <= raw["byte_end"] <= row["size_bytes"], "Raw byte range invalid")
            ensure((obs["raw_message"] is not None) == report["export"]["include_code"], "Export leaked or omitted diagnostic text")
        # A diagnostic the tool reported as suppressed in source never becomes a finding.
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
        # Same rule as internal/report/validate.go: a recorded context needs a digest; the paths list is
        # omitted when the tool read no file (gitleaks with only the built-in configuration).
        ensure(not (include and context["config"] == "recorded")
               or bool(re.fullmatch(r"[0-9a-f]{64}", str(context.get("config_digest", "")))),
               "Recorded rule context without digest")
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
    check_sections(report, source_root)


SECTION_BLOCKS = ("overview", "environment", "documents", "static_analysis", "metrics", "secrets", "code_review")
# Code-free model statements carry this label instead of the model's text (internal/report/govet.go).
TEXT_OMITTED = "원문 생략(코드 미포함 내보내기)."


def check_sections(report: dict, source_root: Path = ROOT) -> None:
    """Cross-field rules of the analysis sections (docs/REPORT.md#분석-종류별-절; validate.go)."""
    present = [key for key in SECTION_BLOCKS + ("gaps",) if key in report]
    if not present:
        return
    ensure(all(key in report for key in SECTION_BLOCKS), "Report with only some analysis sections")
    observations = {o["id"]: o for o in report.get("observations", [])}
    findings = {f["id"]: f for f in report["findings"]}
    # Every observation is in exactly one of static analysis, metrics and secrets.
    placed: list[str] = []
    static_findings: list[str] = []
    for language in report["static_analysis"]["languages"]:
        for tool in language["tools"]:
            placed += tool["observation_ids"]
            static_findings += tool["finding_ids"]
            ensure(all(observations.get(i, {}).get("task_key") == tool["task_key"] and "metric" not in observations[i] for i in tool["observation_ids"]),
                   "Static analysis row lists an observation of another task or a metric")
            ensure(sum(rule["count"] for rule in tool["rules"]) == len(tool["observation_ids"]), "Static analysis rule counts differ from the row")
            ensure(not (tool["task_status"] != "succeeded" and tool["exit_class"] in ("success_without_findings", "success_with_findings")),
                   "Static analysis row reads as success for a task that did not succeed")
    for group in report["metrics"]["metrics"]:
        placed += group["observation_ids"]
        values = [observations[i]["metric"] for i in group["observation_ids"] if "metric" in observations.get(i, {})]
        ensure(len(values) == len(group["observation_ids"]) and values, "Metric group lists a non-metric observation")
        ensure(group["max_value"] == max(m["value"] for m in values) and group["thresholds"] == sorted({m["threshold"] for m in values}),
               "Metric group does not follow its observations")
    for detection in report["secrets"]["detections"]:
        placed.append(detection["observation_id"])
        if detection.get("finding_id") is not None:
            ensure(detection["finding_id"] in static_findings, "Secret detection names a finding outside static analysis")
    ensure(sorted(placed) == sorted(observations), "Observations not partitioned between static analysis, metrics and secrets")
    ensure(len([i for i in report["metrics"]["metrics"] for i in i["observation_ids"]]) == report["summary"].get("metric_observations", 0),
           "Metrics section count differs from the summary")
    # Every finding is in exactly one of static analysis and code review.
    review = report["code_review"]["finding_ids"]
    ensure(sorted(static_findings + review) == sorted(findings), "Findings not partitioned between static analysis and code review")
    overview = {s["key"]: s for s in report["overview"]["sections"]}
    ensure(overview["static_analysis"]["counts"].get("findings", 0) + overview["code_review"]["counts"].get("findings", 0) == report["summary"]["total_findings"],
           "Section finding counts do not add up to the summary")
    ensure(overview["gaps"]["counts"]["gaps"] == len(report.get("gaps", [])), "Gap count differs from the gaps")
    ensure(all(a["section"] in overview for a in report["overview"]["attention"]), "Attention item points at an unknown section")
    # Model statements: none without a performed role; code-free exports carry only the label.
    free = not report["export"]["include_code"]
    def statements(block: dict, lists: dict) -> list[str]:
        out = [entry[field] for name, fields in lists.items() for entry in block[name] for field in fields]
        ensure(block["model"]["state"] == "performed" or not out, "Model statements without a performed role")
        return out
    texts = statements(report["environment"], {"notes": ("affected_checks", "still_checkable", "needed_observation")})
    texts += statements(report["documents"], {"contracts": ("statement",), "conflicts": ("summary", "impact"),
                                              "questions": ("question",), "instruction_notes": ("note",)})
    texts += [limit["statement"] for limit in report["code_review"]["interpretation_limits"]]
    check_ledger(report)
    ensure(not free or all(text == TEXT_OMITTED for text in texts), "Code-free export carries model text")
    # Section evidence resolves in the snapshot named by the report.
    for block in ("environment", "documents"):
        catalog = {e["id"]: e for e in report[block]["evidence"]}
        ensure(len(catalog) == len(report[block]["evidence"]), "Duplicate section evidence")
        for evidence in catalog.values():
            location = {"path": evidence["path"], "start_line": evidence["start_line"], "end_line": evidence["end_line"],
                        "snapshot_id": report["inputs"]["snapshot_id"]}
            path = source_location(location, source_root)
            ensure(hashlib.sha256(path.read_bytes()).hexdigest() == evidence["blob_sha256"], "Section evidence hash mismatch: " + str(path))
        cited = [i for entries in report[block].values() if isinstance(entries, list)
                 for entry in entries if isinstance(entry, dict)
                 for key in ("evidence_ids", "first_evidence_ids", "second_evidence_ids") for i in entry.get(key, [])]
        ensure(set(cited) <= set(catalog), "Section statement cites evidence the section does not carry")


SLICE_TASK = re.compile(r"^review\.(investigate|verify)\.(s0(?:0[1-9]|[12][0-9]|3[0-2]))$")
SLICE_FINDING = re.compile(r"^review_(s[0-9]{3})_")
LEDGER_FILE_COUNTS = ("files_sent_to_model", "files_withheld", "files_unreadable", "files_budget_exhausted", "files_not_planned", "files_not_sent")


def check_ledger(report: dict) -> None:
    """The review slice rules of builder 6 (docs/REPORT.md#모듈별-검토-장부; internal/report/review_slices.go)."""
    builder = int(report["builder"]["version"])
    tasks = {t["key"]: t for t in report["tasks"]}
    for call in report["coverage"].get("review_calls", []):
        if "task_key" in call:
            match = SLICE_TASK.match(call["task_key"])
            stage = match.group(1) if match else call["task_key"].removeprefix("review.")
            ensure(builder >= 6 and call["task_key"] in tasks and stage == call["role"], "Review call names a task it does not belong to")
    for finding in report["findings"]:
        match = SLICE_FINDING.match(finding["id"])
        ensure(not match or (builder >= 6 and "review.investigate." + match.group(1) in tasks), "Slice finding of a slice that was not planned")
    ledger = report["code_review"].get("ledger")
    ensure((ledger is not None) == (builder >= 6 and "review.scope" in tasks), "Ledger presence differs from the review.scope task")
    if ledger is None:
        return
    scope = tasks["review.scope"]
    ensure(ledger["scope"]["task_status"] == scope["status"] and ledger["scope"].get("reason") == scope["reason"], "Ledger scope differs from its task")
    ensure((ledger["max_calls"] is None) == (ledger["scope"]["result"] != "accepted") and (ledger["modules"] == [] or ledger["max_calls"] is not None), "Ledger budget without its scope artifact")
    ensure(ledger["calls_used"] is None or ledger["calls_used"] >= len(report["coverage"].get("review_calls", [])), "Ledger counts fewer calls than the report lists")
    ensure(ledger["request_bytes_sent"] in (None, report["code_review"]["request_bytes"]), "Ledger request bytes differ from the code review section")
    ids = [m["module_id"] for m in ledger["modules"]]
    ensure(ids == sorted(set(ids)), "Ledger modules repeated or out of order")
    for module in ledger["modules"]:
        ensure(sum(module[k] for k in LEDGER_FILE_COUNTS) == module["files_eligible"], "Ledger module files do not add up: " + module["module_id"])
        ensure(module["files_truncated"] <= module["files_sent_to_model"] and module["files_with_completed_review"] <= module["files_sent_to_model"], "Ledger module counts beyond its sent files")
        for entry in module["slices"]:
            task = tasks.get(entry["task_key"])
            ensure(task is not None and task["status"] == entry["status"] and task["reason"] == entry["reason"], "Ledger slice differs from its task: " + entry["task_key"])
        ensure(module["files_budget_exhausted"] == 0 or any(e["status"] == "budget_exhausted" for e in module["slices"]), "Ledger budget-exhausted files without such a slice")
    complete = bool(ledger["modules"]) and all(m["files_with_completed_review"] == m["files_eligible"] for m in ledger["modules"])
    ran = any(SLICE_TASK.match(k) and k.startswith("review.investigate.") and t["status"] == "succeeded" for k, t in tasks.items())
    ensure(ledger["status"] == ("complete" if complete else "partial" if ran else "overview_only"), "Ledger status differs from its rows")
    counts = next(s for s in report["overview"]["sections"] if s["key"] == "code_review")["counts"]
    if ledger["modules"]:
        sent = sum(m["files_sent_to_model"] for m in ledger["modules"])
        ensure(counts.get("files_sent_to_model") == sent and counts.get("files_not_sent_to_model") == sum(m["files_eligible"] for m in ledger["modules"]) - sent,
               "Overview code review scope differs from the ledger")
        ensure(sent <= (report["coverage"]["llm_exposed_files"] if report["coverage"]["llm_exposed_files"] is not None else sent), "Ledger sends more files than the report exposed")


def check_analyzer(descriptor: dict) -> None:
    codes = descriptor["exit_codes"]
    merged = codes["clean"] + codes["findings"] + codes["error"]
    ensure(len(merged) == len(set(merged)), "Overlapping exit-code categories")


ABSOLUTE = re.compile(r"^(?:/|\\|[A-Za-z]:[\\/])")


def check_observations(doc: dict) -> None:
    """Cross-field rules of the Go vet observations artifact ."""
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
    """Cross-field rules of the generic analyzer envelope (docs/ANALYZERS.md; obsdoc.Validate)."""
    observations = doc["observations"]
    parse, outcome, coverage = doc["parse"], doc["outcome"], doc["coverage"]
    ensure(parse["observations"] == len(observations), "Envelope observation count mismatch")
    ensure(parse["tool_errors"] == len(doc["tool_errors"]), "Envelope tool error count mismatch")
    ensure(parse["unresolved_observations"] == sum(1 for o in observations if o["location_state"] != "resolved"), "Envelope unresolved count mismatch")
    ensure(len({o["id"] for o in observations}) == len(observations), "Duplicate envelope observation id")
    ensure(doc["limitations"] == sorted(set(doc["limitations"])), "Envelope limitations must be sorted unique codes")
    # Analyzer purpose (docs/ANALYZERS.md#분석기-목적): a metrics envelope holds only metrics, a
    # secrets envelope none; without a purpose the analyzer is diagnostics.
    purpose = doc["analyzer"].get("purpose")
    ensure(purpose != "metrics" or all("metric" in o for o in observations), "Metrics analyzer with a diagnostic observation")
    ensure(purpose != "secrets" or not any("metric" in o for o in observations), "Secrets analyzer with a metric observation")
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
    # Rule context (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제): config files only when the tool honors
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
