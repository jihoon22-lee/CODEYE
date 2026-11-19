"""Stage vocabulary, schedule, comparison, OpenAPI and doctor checks."""

from __future__ import annotations

import copy
import json
from pathlib import Path
import re
from urllib.parse import unquote, urldefrag

try:
    from jsonschema import Draft202012Validator, FormatChecker
except ImportError as exc:
    raise SystemExit("Install the approved packages in scripts/requirements.txt: " + str(exc))

from .common import ROOT, ensure, expect_invalid, json_file, within_root, yaml_file


SERVER_STAGES = ("source", "report")


def stage_vocabulary() -> tuple[list[str], list[str], list[str]]:
    """Task stage keys in domain.TaskStages order, the migration 0006 CHECK
    values, and the progress.State values (docs/ARCHITECTURE.md#표시-단계와-task-단계)."""
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
    """Cross-field rules of a stage list (project activity or run progress)."""
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
    """Cross-field rules of a project activity (docs/API.md)."""
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


def check_schedule_contracts() -> None:
    """Schedules, plan revision/weighted progress and the run event stream.

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
    print(f"PASS: schedule/progress/stream contracts (3 examples; overlap/misfire/occurrence/reason/stream/weight enums and bounds; {negatives} negative cases)")


def check_comparison_contracts(published: dict) -> None:
    """Run comparison and human finding reviews (docs/REPORT.md).

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
        # A resolved tool entry needs the same tool version and rule context on both sides (docs/REPORT.md#항목-상태).
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
    print(f"PASS: comparison/review contracts (2 examples; disposition/state/reason/match enums across OpenAPI, migration 0008, schema and Go; {negatives} negative cases)")


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


def check_doctor_operations(validator, example) -> None:
    """The doctor operations section (docs/OPERATIONS.md#codeye-doctor) is optional and closed."""
    ensure("operations" in example, "Doctor example lacks the operations section")
    base = copy.deepcopy(example)
    del base["operations"]
    validator.validate(base)
    cases = []
    bad = copy.deepcopy(example)
    bad["operations"]["storage"][1].update(status="over_limit", limit_bytes=None)
    cases.append(("over limit without a limit", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["storage"][0].update(status="unmeasured")
    cases.append(("unmeasured with bytes", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["storage"][4]["enforcement"] = "claim_refusal_node_heartbeat"
    cases.append(("artifacts enforced by heartbeat", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["storage"][4]["area"] = "logs"
    cases.append(("missing storage area", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["storage"][0]["database_url"] = "postgresql://x"
    cases.append(("extra storage field", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["backup"].update(status="none")
    cases.append(("absent backup with a set", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["processes"]["issues"] = ["stale_record"]
    cases.append(("consistent with issues", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["processes"].update(status="inconsistent")
    cases.append(("inconsistent without issues", bad))
    bad = copy.deepcopy(example)
    bad["operations"]["processes"]["controller"] = "alive"
    cases.append(("unknown process state", bad))
    for name, data in cases:
        expect_invalid(name, validator.validate, data)
