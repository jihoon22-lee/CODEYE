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


def account_vocabulary() -> dict:
    """The audit action, actor kind and target type vocabularies as written in
    the migrations (the newest migration from 0010 on that sets each CHECK),
    the Go store (internal/storage/postgres/audit.go) and OpenAPI
    (docs/SECURITY.md#감사), plus the Go detail-key allowlist."""
    migrations = sorted(p for p in (ROOT / "migrations/sql").glob("*.sql") if p.name[:4] >= "0010")
    ensure(bool(migrations) and migrations[0].name.startswith("0010_"), "Missing migration 0010")
    sources = [p.read_text(encoding="utf-8") for p in reversed(migrations)]

    def check_list(column: str) -> list[str]:
        for sql in sources:
            match = re.search(r"\b" + column + r" IN \((.*?)\)\)", sql, re.S)
            if match is not None:
                return re.findall(r"'([a-z_.]+)'", match.group(1))
        ensure(False, "No migration has a " + column + " CHECK")
        return []

    go = (ROOT / "internal/storage/postgres/audit.go").read_text(encoding="utf-8")
    consts = dict(re.findall(r'^\s*(Audit\w+)\s*=\s*"([a-z_.]+)"', go, re.M))
    actors = re.findall(r'^\s*Actor\w+\s*=\s*"([a-z_]+)"', go, re.M)
    listed = re.search(r"var auditActions = map\[string\]bool\{(.*?)\n\}", go, re.S)
    keys = re.search(r"var auditDetailKeys = map\[string\]bool\{(.*?)\n\}", go, re.S)
    ensure(bool(consts) and bool(actors) and listed is not None and keys is not None, "Go audit vocabulary not found")
    allowed = [consts.get(name, "?" + name) for name in re.findall(r"(Audit\w+):\s*true", listed.group(1))]
    api = yaml_file(ROOT / "api/openapi.yaml")["components"]["schemas"]
    event = api["AuditEvent"]["properties"]
    return {
        "actions": {"migrations": check_list("action"), "Go constants": list(consts.values()), "Go auditActions": allowed,
                    "OpenAPI AuditAction": api["AuditAction"]["enum"]},
        "actors": {"migrations": check_list("actor_kind"), "Go constants": actors, "OpenAPI AuditEvent": event["actor_kind"]["enum"]},
        "targets": {"migrations": check_list("target_type"), "OpenAPI AuditEvent": [t for t in event["target_type"]["enum"] if t is not None]},
        "detail_keys": set(re.findall(r'"([a-z_]+)":\s*true', keys.group(1))),
    }


def check_account_vocabulary(vocabulary: dict) -> None:
    for name in ("actions", "actors", "targets"):
        lists = vocabulary[name]
        reference = next(iter(lists.values()))
        for source, values in lists.items():
            ensure(len(values) == len(set(values)), f"Duplicate audit {name} in {source}")
            ensure(set(values) == set(reference), f"Audit {name} differ: {source} vs migrations: {sorted(set(values) ^ set(reference))}")
    secret = re.compile(r"password|token|secret|hash|salt|prompt|reasoning|path$|^path")
    leaked = [key for key in vocabulary["detail_keys"] if secret.search(key) and key != "must_change_password"]
    ensure(not leaked, "Audit detail allowlist names a secret or path: " + ", ".join(sorted(leaked)))


def check_account_contracts() -> None:
    """Accounts, members and the audit log (docs/API.md#계정과-관리자): the
    example documents fit their OpenAPI schemas, the closed audit vocabulary
    is the same in the migrations, Go and OpenAPI, and every example audit
    detail uses only allowlisted keys."""
    vocabulary = account_vocabulary()
    check_account_vocabulary(vocabulary)
    example = json_file(ROOT / "examples/accounts.json")
    session, with_link, member, page = (api_validator(n) for n in ("SessionView", "UserWithLink", "Member", "AuditPage"))
    registration = api_validator("Registration")
    role = api_validator("ProjectRole")

    def check(doc: dict) -> None:
        session.validate(doc["session"])
        registration.validate(doc["registration"])
        signed_up = doc["registration_session"]
        session.validate(signed_up)
        ensure(signed_up["email"] == doc["registration"]["email"].strip().lower(), "Registration response email is canonical")
        ensure(signed_up["has_password"] and not signed_up["is_admin"] and not signed_up["must_change_password"]
               and signed_up["username"] is None and "password" not in signed_up, "A registered user has a regular password session")
        with_link.validate(doc["created_user"])
        ensure(doc["created_user"]["user"]["must_change_password"] and not doc["created_user"]["user"]["has_password"],
               "An account created by an administrator has no password and must set one")
        ensure("#launch=" in doc["created_user"]["login_link"]["url"], "Login link carries the token in the fragment")
        project = doc["project_role"]
        role.validate(project["role"])
        ensure(project["can_run"] == (project["role"] in ("owner", "manager", "runner")) and project["can_manage"] == (project["role"] in ("owner", "manager")),
               "can_run/can_manage do not follow the role")
        for item in doc["members"]["items"]:
            member.validate(item)
        ensure([m["role"] for m in doc["members"]["items"]][:1] == ["owner"], "Member list starts with the owner")
        page.validate(doc["audit"])
        ids = [int(item["id"]) for item in doc["audit"]["items"]]
        ensure(ids == sorted(ids, reverse=True), "Audit page is newest first")
        ensure(doc["audit"]["next_cursor"] in (None, str(ids[-1])), "Audit cursor is the last id of the page")
        for item in doc["audit"]["items"]:
            ensure(set(item["detail"]) <= vocabulary["detail_keys"], "Audit detail key outside the allowlist: " + item["action"])
            ensure((item["actor_kind"] == "user") <= (item["actor_user_id"] is not None), "User actor without a user")
            ensure((item["target_type"] is None) == (item["target_id"] is None), "Audit target pair")

    check(example)
    negatives = 0

    def reject(name: str, function, data) -> None:
        nonlocal negatives
        expect_invalid(name, function, data)
        negatives += 1

    for name, edit in (
        ("unknown audit action", lambda d: d["audit"]["items"][0].update(action="report.viewed")),
        ("audit detail with a password", lambda d: d["audit"]["items"][2]["detail"].update(password="hunter2hunter2")),
        ("session without must_change_password", lambda d: d["session"].pop("must_change_password")),
        ("signup request sets admin role", lambda d: d["registration"].update(is_admin=True)),
        ("signup response exposes password", lambda d: d["registration_session"].update(password=d["registration"]["password"])),
        ("signup response exposes an unnormalized email", lambda d: d["registration_session"].update(email="Dana+review@EXAMPLE.Test")),
        ("reset notice without its time", lambda d: d["session"]["password_reset"].pop("reset_at")),
        ("reset notice that can be dismissed", lambda d: d["session"]["password_reset"].update(acknowledged=True)),
        ("user view with a password hash", lambda d: d["created_user"]["user"].update(password_hash="00")),
        ("member role outside the vocabulary", lambda d: d["members"]["items"][1].update(role="admin")),
        ("viewer that can run", lambda d: d["project_role"].update(role="viewer")),
        ("audit page oldest first", lambda d: d["audit"]["items"].reverse()),
        ("user actor without a user", lambda d: d["audit"]["items"][0].update(actor_user_id=None)),
    ):
        bad = copy.deepcopy(example)
        edit(bad)
        reject(name, check, bad)
    for name, edit in (
        ("audit action drift", lambda v: v["actions"]["OpenAPI AuditAction"].append("report.viewed")),
        ("audit actor drift", lambda v: v["actors"]["Go constants"].remove("system")),
        ("secret detail key", lambda v: v["detail_keys"].add("new_password")),
    ):
        bad = copy.deepcopy(vocabulary)
        edit(bad)
        reject(name, check_account_vocabulary, bad)
    print(f"PASS: account contracts ({len(vocabulary['actions']['migrations'])} audit actions across migrations, Go and OpenAPI; "
          f"examples/accounts.json; {negatives} negative cases)")


def check_shared_service_contracts() -> None:
    """Shared platform source access and per-user analysis options."""
    example = json_file(ROOT / "examples/shared-service.json")
    setup, settings, root, provider, error = (api_validator(n) for n in ("Setup", "ServiceSettings", "AdminSourceRoot", "AdminProvider", "Error"))

    def check(doc: dict) -> None:
        for key in ("setup_member", "setup_member_after"):
            s = doc[key]
            setup.validate(s)
            path = s["available"]["local_path"]
            ensure(path["available"] and path["reason"] is None and s["readable_folders"] == [],
                   "Local path registration follows service account permissions without exposing server folder lists")
            for tool in (s["tools"] or {}).get("tools", []):
                ensure(s["is_admin"] or not {"path", "hint", "source"} & set(tool), "A user who is not an administrator sees no tool path or hint")
            built = s["available"]["build_scripts"]
            ensure(built["available"] or s["analysis_defaults"]["local"]["run_build_scripts"] is False,
                   "A switch that is off must also turn the default off")
        error.validate(doc["folder_refusal"])
        refusal = doc["folder_refusal"]["error"]
        ensure(refusal["code"] == "FOLDER_NOT_READABLE" and "details" not in refusal,
               "A refused path reports the permission failure without exposing server folders")
        settings.validate(doc["settings"])
        ensure(doc["settings"]["service_origin"] in [a["origin"] for a in doc["settings"]["service_addresses"]],
               "The address to share is one the service answers on")
        error.validate(doc["host_refusal"])
        refused = doc["host_refusal"]["error"]
        details = refused.get("details", {})
        ensure(refused["code"] == "HOST_REJECTED" and details.get("accepted_origins") and details.get("add_command", "").startswith("codeye up ")
               and details["add_command"] in refused["message"] and details.get("host", "") in refused["message"],
               "A refused address is told which addresses work and the command that adds it")
        root.validate(doc["source_root"])
        provider.validate(doc["provider"])


    check(example)
    negatives = 0
    for name, edit in (
        ("setup with a mode", lambda d: d["setup_member"].update(mode="server")),
        ("connection that answers the token", lambda d: d["setup_member"]["github_connections"][0].update(token="ghp_example-token")),
        ("path gated by a grant", lambda d: d["setup_member"]["available"]["local_path"].update(available=False, reason="no_allowed_folder")),
        ("tool path shown to a user", lambda d: d["setup_member"]["tools"]["tools"][0].update(path="/usr/bin/go")),
        ("reason outside the vocabulary", lambda d: d["setup_member"]["available"]["build_scripts"].update(reason="server_mode")),
        ("settings without the service address", lambda d: d["settings"].pop("service_origin")),
        ("settings without the address list", lambda d: d["settings"].pop("service_addresses")),
        ("address of an unknown kind", lambda d: d["settings"]["service_addresses"][0].update(kind="docker")),
        ("refused address without the command", lambda d: d["host_refusal"]["error"]["details"].pop("add_command")),
        ("refusal with server folders", lambda d: d["folder_refusal"]["error"].update(details={"readable_folders": ["/srv/private"]})),
        ("provider of another kind", lambda d: d["provider"].update(kind="gitlab")),
    ):
        bad = copy.deepcopy(example)
        edit(bad)
        expect_invalid(name, check, bad)
        negatives += 1
    print(f"PASS: shared service contracts (examples/shared-service.json; {negatives} negative cases)")


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
    bad = copy.deepcopy(example)
    bad["tools"][0]["path"] = "/home/user/.local/bin/go"
    cases.append(("tool path in the doctor report", bad))
    bad = copy.deepcopy(example)
    bad["checks"] = [c for c in bad["checks"] if c["id"] != "database"] + [{"id": "model", "status": "unverified", "reason": "adapter_contract_pending"}]
    cases.append(("a check that is no longer reported", bad))
    # The offline readiness section (docs/OPERATIONS.md#오프라인-준비-점검) is optional and closed.
    ensure("offline" in example, "Doctor example lacks the offline readiness section")
    base = copy.deepcopy(example)
    del base["offline"]
    validator.validate(base)
    bad = copy.deepcopy(example)
    bad["offline"]["destinations"][1]["value"] = "sum.golang.org"
    cases.append(("a destination of origin none with a value", bad))
    bad = copy.deepcopy(example)
    del bad["offline"]["destinations"][0]["value"]
    cases.append(("a configured destination without a value", bad))
    bad = copy.deepcopy(example)
    bad["offline"]["destinations"][0]["value"] = "https://user:token@mirror.corp.example/go"
    cases.append(("a destination carrying a credential", bad))
    bad = copy.deepcopy(example)
    bad["offline"]["ca_bundle"]["path"] = "/etc/pki/ca.pem"
    cases.append(("a CA bundle path in the report", bad))
    bad = copy.deepcopy(example)
    bad["offline"]["ca_bundle"] = {"status": "unavailable"}
    cases.append(("an unavailable bundle without a reason", bad))
    bad = copy.deepcopy(example)
    bad["offline"]["languages"][0]["missing"] = ["staticcheck"]
    cases.append(("a ready language with something missing", bad))
    bad = copy.deepcopy(example)
    bad["offline"]["languages"][1]["missing"] = []
    cases.append(("a partial language with nothing missing", bad))
    bad = copy.deepcopy(example)
    bad["offline"]["postgres"]["programs"].pop()
    cases.append(("a PostgreSQL program left out", bad))
    for name, data in cases:
        expect_invalid(name, validator.validate, data)


def check_tools_contract(validator, example) -> None:
    """The tool detection document (docs/ANALYZERS.md#도구-찾기) matches the Go tool list
    and ties each status to the fields it needs."""
    names = [t["name"] for t in example["tools"]]
    go = (ROOT / "internal/toolfind/detect.go").read_text(encoding="utf-8")
    declared = re.findall(r'^\t"([a-z-]+)":\s+\{kind', go, re.M)
    # The package managers of the dependency fetch keep their specs in their own file.
    managers = (ROOT / "internal/toolfind/managers.go").read_text(encoding="utf-8")
    declared += re.findall(r'^\t"([a-z-]+)":\s+\{kind', managers, re.M)
    ensure(sorted(names) == sorted(declared), "tools example and toolfind specs list different tools")
    cases = []
    bad = copy.deepcopy(example)
    bad["tools"][0]["status"] = "not_found"
    cases.append(("not found with a path", bad))
    bad = copy.deepcopy(example)
    del bad["tools"][0]["path"]
    cases.append(("found without a path", bad))
    bad = copy.deepcopy(example)
    bad["tools"][0]["path"] = "bin/go"
    cases.append(("relative tool path", bad))
    bad = copy.deepcopy(example)
    bad["tools"] = bad["tools"][1:]
    cases.append(("missing tool entry", bad))
    bad = copy.deepcopy(example)
    next(t for t in bad["tools"] if t["status"] == "unusable").pop("hint")
    cases.append(("unusable without a hint", bad))
    bad = copy.deepcopy(example)
    bad["tools"][0]["notes"] = ["other"]
    cases.append(("unknown note", bad))
    for name, data in cases:
        expect_invalid(name, validator.validate, data)
