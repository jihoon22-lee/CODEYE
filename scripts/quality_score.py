"""W24 scoring: auto-match pre-fill, judgment templates, docs/EVALUATION.md metrics with Wilson intervals,
variance across repeats, and report-reading sheets (docs/EVALUATION.md#채점).

Rules that keep the numbers honest:
- An auto-match (location overlap or symbol) is a pre-fill for the judge, never a verdict.
  Location-based rates are reported separately as `auto_*` structural rates.
- Every judged metric counts only judged units; undeterminable units are counted as `unjudged`.
- Cost/time and accuracy are reported side by side, never folded into one score.
- Rows marked invalid (answer leak, budget refusal, changed source, unverified echo, failed secret screen in
  the review condition, not run) are excluded and counted.
"""
from __future__ import annotations

from collections import Counter, defaultdict
import difflib
import json
import math
from pathlib import Path
import random
import re
import shutil
import statistics
import tempfile

VERDICTS = ("valid_actionable", "valid_informational", "false_positive", "duplicate", "insufficient_evidence",
            "out_of_scope")
VALID = ("valid_actionable", "valid_informational")
ACTIONABLE = ("supported", "reproduced")
PENDING = ("unreviewed", "inconclusive")
JUDGMENT_SCHEMA = "codeye-r1-judgments"
Z95 = 1.959963984540054


# ---------------------------------------------------------------- statistics

def wilson(k, n, z=Z95):
    """Wilson score interval for k successes out of n (None when n == 0)."""
    if n == 0:
        return None
    p = k / n
    denom = 1 + z * z / n
    centre = (p + z * z / (2 * n)) / denom
    half = z * math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / denom
    return [round(max(0.0, centre - half), 4), round(min(1.0, centre + half), 4)]


def proportion(k, n, unjudged=0, **extra):
    out = {"k": k, "n": n, "rate": round(k / n, 4) if n else None, "wilson95": wilson(k, n), "unjudged": unjudged}
    out.update(extra)
    return out


# ---------------------------------------------------------------- findings and labels

def short_symbol(symbol):
    return re.split(r"[.:]+", symbol or "")[-1]


def finding_view(f):
    basis = f.get("fingerprint_basis") or {}
    producer = basis.get("producer") or ("tool" if f.get("observation_ids") else "model")
    locs = [(l["path"], l["start_line"], l["end_line"], l.get("symbol") or "") for l in f.get("locations", [])]
    for e in f.get("evidence", []):
        if e.get("kind") == "code" and e.get("location"):
            l = e["location"]
            locs.append((l["path"], l["start_line"], l["end_line"], l.get("symbol") or ""))
    return {"finding_id": f["id"], "title": f.get("title", ""), "validation": f["validation"],
            "severity": f.get("severity"), "category": f.get("category"), "producer": producer,
            "disposition": f.get("disposition"), "fingerprint": f.get("fingerprint"),
            "actionable": f["validation"] in ACTIONABLE and f.get("disposition") in ("open", "acknowledged"),
            "locations": [list(x) for x in dict.fromkeys(locs)]}


def map_range(before, after, start, end):
    """Map a 1-based before line range onto the after text (the closest corresponding lines)."""
    a, b = before.split("\n"), after.split("\n")
    mapped = []
    for tag, i1, i2, j1, j2 in difflib.SequenceMatcher(a=a, b=b, autojunk=False).get_opcodes():
        lo, hi = max(start - 1, i1), min(end, i2)
        if lo >= hi:
            continue
        if tag == "equal":
            mapped += [j1 + (lo - i1) + 1, j1 + (hi - 1 - i1) + 1]
        elif j2 > j1:
            mapped += [j1 + 1, j2]
        else:
            mapped += [max(1, j1), max(1, j1 + 1)]
    if not mapped:
        return start, end
    return min(mapped), max(mapped)


def targets(corpus, case, variant, documented=False):
    """Label locations a finding is compared with (pre-fill only)."""
    label = case["label"]
    if case["kind"] == "tricky_normal":
        return [{"kind": "suspect", "path": l["path"], "start_line": l["start_line"], "end_line": l["end_line"],
                 "symbol": l.get("symbol", "")} for l in label.get("suspect_locations", [])]
    if variant == "before":
        return [{"kind": "positive", "path": l["path"], "start_line": l["start_line"], "end_line": l["end_line"],
                 "symbol": l.get("symbol", "")} for l in label["positive_locations"]]
    before = corpus.render(case, "before", documented)
    after = corpus.render(case, "after", documented)
    out = []
    for l in label["positive_locations"]:
        if l["path"] in after:
            s, e = map_range(before[l["path"]], after[l["path"]], l["start_line"], l["end_line"])
        else:
            s, e = l["start_line"], l["end_line"]
        out.append({"kind": "after_mapped", "path": l["path"], "start_line": s, "end_line": e,
                    "symbol": l.get("symbol", "")})
    return out


ANSWERED = ("accepted", "output_rejected")


def model_exposure(records):
    """What the kept review records say reached the model: evidence paths by record kind (code, test,
    config, document) over every review stage, and the paths each stage saw. Records carry the review
    input's evidence list, so this works for paid runs (which have no request dump) too. A stage counts
    only if the model answered at least one of its calls (accepted or output_rejected): a record whose
    calls were all blocked, refused or failed upstream, or that has no call, lists prepared input only."""
    kinds, by_stage = defaultdict(set), defaultdict(set)
    for r in records:
        if not any(isinstance(c, dict) and c.get("outcome") in ANSWERED for c in r.get("calls") or []):
            continue
        stage = r.get("stage")
        for e in r.get("evidence") or []:
            if isinstance(e, dict) and isinstance(e.get("path"), str):
                kinds[str(e.get("kind"))].add(e["path"])
                by_stage[stage].add(e["path"])
    return {"kinds": {k: sorted(v) for k, v in kinds.items()}, "stages": {k: sorted(v) for k, v in by_stage.items()}}


def auto_match(view, tgts, slack=2):
    best = None
    for path, s, e, sym in view["locations"]:
        for index, t in enumerate(tgts):
            if path != t["path"]:
                continue
            if s <= t["end_line"] + slack and t["start_line"] <= e + slack:
                return {"kind": t["kind"], "target": index, "how": "lines"}
            short = short_symbol(t["symbol"])
            if best is None and short and (short == short_symbol(sym) or re.search(rf"\b{re.escape(short)}\b", view["title"])):
                best = {"kind": t["kind"], "target": index, "how": "symbol"}
    return best


# ---------------------------------------------------------------- batches

def load_batch(batch):
    batch = Path(batch)
    manifest = json.loads((batch / "manifest.json").read_text())
    rows = []
    path = batch / "rows.jsonl"
    if path.exists():
        rows = [json.loads(l) for l in path.read_text(encoding="utf-8").splitlines() if l.strip()]
    reports = {}
    for row in rows:
        f = batch / "reports" / (row["key"] + ".json")
        if f.exists():
            reports[row["key"]] = json.loads(f.read_bytes())
    return manifest, rows, reports


def records_of(batch, key):
    out = []
    directory = Path(batch) / "records" / key
    if directory.is_dir():
        for f in sorted(directory.glob("*.json")):
            doc = json.loads(f.read_bytes())
            if "stage" in doc and "calls" in doc:
                out.append(doc)
    return out


def judgment_template(batch, corpus, slack=2):
    """Pre-filled template for the judge. It carries label text, so it stays with the evaluator."""
    manifest, rows, reports = load_batch(batch)
    items = []
    for row in rows:
        if row.get("invalid") or row["key"] not in reports:
            continue
        case = corpus.by_id[row["case"]]
        tg = targets(corpus, case, row["variant"], row.get("documented", False))
        findings = []
        for f in reports[row["key"]]["findings"]:
            v = finding_view(f)
            findings.append({**{k: v[k] for k in ("finding_id", "title", "validation", "severity", "producer",
                                                  "actionable", "locations")},
                             "auto_match": auto_match(v, tg, slack), "verdict": None, "matches_label": None,
                             "duplicate_of": None, "notes": ""})
        label = case["label"]
        items.append({"key": row["key"], "case": row["case"], "variant": row["variant"], "kind": case["kind"],
                      "label": {"trigger": label["trigger"], "expected_impact": label["expected_impact"],
                                "locations": tg, "contract": case["contract"]},
                      "findings": findings})
    return {"schema": JUDGMENT_SCHEMA, "version": 1, "batch": manifest["batch"],
            "corpus_digest": manifest["corpus"]["digest"], "judge": None, "verdicts": list(VERDICTS),
            "instructions": ("Set verdict to one of `verdicts` per finding (docs/EVALUATION.md#정답-라벨) and matches_label to true "
                             "when the finding states the labelled cause and condition (wording may differ). "
                             "auto_match is a location pre-fill, not a verdict. Leave null when not judged."),
            "items": items}


def load_judgments(files):
    """{(batch, key, finding_id): {verdict, matches_label, duplicate_of}} plus conflicts (treated as unjudged)."""
    merged, conflicts, judges = {}, [], []
    for f in files:
        doc = json.loads(Path(f).read_text())
        if doc.get("schema") != JUDGMENT_SCHEMA:
            raise SystemExit(f"{f}: not a judgment file")
        judges.append(doc.get("judge") or Path(f).name)
        for item in doc["items"]:
            for fd in item["findings"]:
                if fd.get("verdict") is None and fd.get("matches_label") is None:
                    continue
                if fd.get("verdict") is not None and fd["verdict"] not in VERDICTS:
                    raise SystemExit(f"{f}: unknown verdict {fd['verdict']}")
                k = (doc["batch"], item["key"], fd["finding_id"])
                value = {"verdict": fd.get("verdict"), "matches_label": fd.get("matches_label"),
                         "duplicate_of": fd.get("duplicate_of")}
                if k in merged and merged[k] != value:
                    conflicts.append(list(k))
                    merged[k] = None
                elif k not in merged:
                    merged[k] = value
    return {k: v for k, v in merged.items() if v is not None}, conflicts, judges


# ---------------------------------------------------------------- metrics

def unit_result(views, judged, select, hit):
    """For one variant: True/False when determinable from judged findings, else None.

    select(view) picks findings in scope; hit(judgment) says whether a judged finding counts.
    No finding in scope => False (determinable without judgment)."""
    scope = [v for v in views if select(v)]
    if not scope:
        return False
    unknown = False
    for v in scope:
        j = judged.get(v["finding_id"])
        if j is None:
            unknown = True
        elif hit(j):
            return True
    return None if unknown else False


def batch_metrics(manifest, rows, reports, judgments, corpus, slack=2, batch_dir=None):
    name = manifest["batch"]
    valid = [r for r in rows if not r.get("invalid") and r["key"] in reports]
    excluded = Counter(r["invalid"] for r in rows if r.get("invalid"))
    excluded.update("no_report" for r in rows if not r.get("invalid") and r["key"] not in reports)
    units = []
    for row in valid:
        case = corpus.by_id[row["case"]]
        tg = targets(corpus, case, row["variant"], row.get("documented", False))
        views = [finding_view(f) for f in reports[row["key"]]["findings"]]
        for v in views:
            v["auto"] = auto_match(v, tg, slack)
        judged = {fid: j for (b, key, fid), j in judgments.items() if b == name and key == row["key"]}
        exposure = model_exposure(records_of(batch_dir, row["key"])) if batch_dir else {"kinds": {}, "stages": {}}
        units.append({"row": row, "case": case, "views": views, "judged": judged, "targets": tg, "exposure": exposure})

    def group(pred):
        return [u for u in units if pred(u)]

    def rate(us, fn):
        results = [fn(u) for u in us]
        k = sum(r is True for r in results)
        n = sum(r is not None for r in results)
        return proportion(k, n, unjudged=sum(r is None for r in results))

    before = lambda u: u["case"]["kind"] == "defect_pair" and u["row"]["variant"] == "before"
    after = lambda u: u["row"]["variant"] == "after"
    normal = lambda u: u["case"]["kind"] == "tricky_normal"
    any_finding = lambda v: True
    model_only = lambda v: v["producer"] == "model"
    actionable = lambda v: v["actionable"]
    captured = lambda j: j.get("verdict") in VALID and j.get("matches_label") is True
    reflagged = lambda j: j.get("matches_label") is True
    false_pos = lambda j: j.get("verdict") == "false_positive"

    def recall(scope):
        return lambda u: unit_result(u["views"], u["judged"], scope, captured)

    metrics = {
        "known_defect_recall": rate(group(before), recall(any_finding)),
        "known_defect_recall_model_findings": rate(group(before), recall(model_only)),
        "same_defect_reflag_after": rate(group(after), lambda u: unit_result(u["views"], u["judged"], any_finding, reflagged)),
        "false_positive_rate": {
            scope_name: {
                "tricky_normal": rate(group(normal), lambda u, s=scope: unit_result(u["views"], u["judged"], s, false_pos)),
                "after": rate(group(after), lambda u, s=scope: unit_result(u["views"], u["judged"], s, false_pos)),
                "combined": rate(group(lambda u: normal(u) or after(u)),
                                 lambda u, s=scope: unit_result(u["views"], u["judged"], s, false_pos)),
            } for scope_name, scope in (("actionable_findings", actionable), ("any_finding", any_finding))},
    }
    judged_actionable = [(u, v, u["judged"][v["finding_id"]]) for u in units for v in u["views"]
                         if v["actionable"] and v["finding_id"] in u["judged"] and u["judged"][v["finding_id"]].get("verdict")]
    all_actionable = sum(v["actionable"] for u in units for v in u["views"])
    metrics["actionable_precision"] = proportion(sum(j["verdict"] == "valid_actionable" for _, _, j in judged_actionable),
                                                 len(judged_actionable), unjudged=all_actionable - len(judged_actionable))
    metrics["actionable_precision_valid_any"] = proportion(sum(j["verdict"] in VALID for _, _, j in judged_actionable),
                                                           len(judged_actionable),
                                                           unjudged=all_actionable - len(judged_actionable))
    judged_all = [(v, u["judged"][v["finding_id"]]) for u in units for v in u["views"]
                  if v["finding_id"] in u["judged"] and u["judged"][v["finding_id"]].get("verdict")]
    total_findings = sum(len(u["views"]) for u in units)
    metrics["unsupported_assertions_judged"] = proportion(
        sum(j["verdict"] == "insufficient_evidence" for _, j in judged_all), len(judged_all),
        unjudged=total_findings - len(judged_all))
    metrics["duplicate_burden_judged"] = {"duplicates": sum(j["verdict"] == "duplicate" for _, j in judged_all),
                                          "judged_findings": len(judged_all), "variants": len(units)}

    # Structural (automatic) rates: location pre-fill and product facts, never verdicts.
    def auto_hit(kinds, scope=any_finding):
        return lambda u: any(scope(v) and v["auto"] and v["auto"]["kind"] in kinds for v in u["views"])

    structural = {
        "auto_location_hit_before": rate(group(before), auto_hit(("positive",))),
        "auto_location_hit_before_model": rate(group(before), auto_hit(("positive",), model_only)),
        "auto_location_reflag_after": rate(group(after), auto_hit(("after_mapped",))),
        "auto_suspect_flag_normal": rate(group(normal), auto_hit(("suspect",))),
        "auto_any_actionable_on_after_or_normal": rate(group(lambda u: normal(u) or after(u)),
                                                       lambda u: any(v["actionable"] for v in u["views"])),
        "ai_review_performed": rate(units, lambda u: u["row"].get("ai_review") == "performed"),
        # why review did or did not run, over every reported variant including excluded ones
        # (secret_screen_failed is a policy outcome, not a model failure, and is excluded from the metrics)
        "review_outcomes": dict(Counter(r.get("review_outcome", "unrecorded") for r in rows if r["key"] in reports)),
        # the report gives only a count, and README/document excerpts count too: this is not "saw code"
        "model_saw_snapshot_files": rate(units, lambda u: (u["row"].get("llm_exposed_files") or 0) > 0),
        # from the kept review records: a code-kind excerpt reached some review role
        "model_saw_code": rate(units, lambda u: bool(u["exposure"]["kinds"].get("code"))),
        # the labelled defect file (before variants) was in the investigate input
        "labelled_file_in_investigate": rate(group(before), lambda u: bool(
            {t["path"] for t in u["targets"]} & set(u["exposure"]["stages"].get("investigate", [])))),
    }
    # review condition: valid variants where no code excerpt reached any review role
    structural["no_code_variants"] = sorted(u["row"]["key"] for u in units if not u["exposure"]["kinds"].get("code"))         if manifest.get("condition") == "review" else []
    reproduced = sum(v["validation"] == "reproduced" for u in units for v in u["views"])
    errors, stats_bad, rejected, markers = Counter(), 0, 0, Counter()
    try:
        import w15_pack_compare
        for u in units:
            records = records_of(batch_dir, u["row"]["key"]) if batch_dir else []
            s = w15_pack_compare.structural({"row": {"status": u["row"]["status"]}, "report": reports[u["row"]["key"]],
                                             "records": records})
            errors.update(s["output_errors"])
            stats_bad += not s["stats_consistent"]
            rejected += len(s["rejected_outputs"])
            markers.update(s["markers"])
    except ImportError:  # pragma: no cover
        pass
    structural["unsupported_assertions_structural"] = {
        "reproduced_without_execution": reproduced, "rejected_model_outputs": rejected,
        "output_errors_by_class": dict(errors), "markers": dict(markers), "stats_inconsistent_reports": stats_bad}
    groups = defaultdict(list)
    for u in units:
        seen = []
        for v in u["views"]:
            for path, s, e, _ in v["locations"][:1]:
                if any(p == path and s <= e2 and s2 <= e for p, s2, e2 in seen):
                    groups[u["row"]["key"]].append(v["finding_id"])
                seen.append((path, s, e))
    structural["auto_duplicate_candidates"] = {"findings": sum(len(v) for v in groups.values()),
                                               "variants_with_overlaps": len(groups), "variants": len(units)}

    # Cost and time, reported next to accuracy and never combined with it.
    walls = [r.get("wall_s") for r in valid if isinstance(r.get("wall_s"), (int, float))]
    calls = Counter(c["role"] + ":" + str(c.get("outcome")) for r in valid for c in r.get("review_calls", []))
    ledger = [r.get("ledger") or {} for r in rows]
    cost = {"variants": len(valid), "review_calls": sum(calls.values()), "review_calls_by_role": dict(sorted(calls.items())),
            "usage_tokens": dict(sum((Counter(r.get("usage") or {}) for r in valid), Counter())),
            "ledger_requests": sum(len(l.get("requests", [])) for l in ledger),
            "ledger_charged_microusd": sum(l.get("charged_microusd", 0) for l in ledger),
            "batch_delta_microusd": (manifest.get("budget") or {}).get("batch_delta_microusd"),
            "wall_s_total": round(sum(walls), 1),
            "wall_s_median": round(statistics.median(walls), 1) if walls else None,
            "wall_s_max": max(walls) if walls else None,
            "dry_run_upstream": bool(manifest.get("dry_run"))}

    def breakdown(field):
        out = {}
        for value in sorted({u["case"][field] for u in units}):
            sub = [u for u in units if u["case"][field] == value]
            b = [u for u in sub if before(u)]
            fa = [u for u in sub if normal(u) or after(u)]
            out[value] = {"known_defect_recall": rate(b, recall(any_finding)),
                          "false_positive_rate_any": rate(fa, lambda u: unit_result(u["views"], u["judged"], any_finding, false_pos)),
                          "auto_location_hit_before": rate(b, auto_hit(("positive",))),
                          "auto_any_actionable_on_after_or_normal": rate(fa, lambda u: any(v["actionable"] for v in u["views"]))}
        return out

    forbidden = manifest.get("forbidden") or None
    blocks = [b for l in ledger for b in l.get("blocked", [])]
    blocking = None if forbidden is None else {
        "mode": "model_echo_allowed" if forbidden.get("allow_model_echo") else "strict",
        "list_sha256": forbidden.get("sha256"),
        "blocked_variants": sum(r.get("invalid") == "blocked_answer_leak" for r in rows),
        "blocked_requests_by_cause": dict(Counter(b.get("cause", "unknown") for b in blocks)),
        "echo_exempted_requests": sum(bool(q.get("echo_matches")) for l in ledger for q in l.get("requests", [])),
        "echo_unverified_variants": sum(r.get("invalid") == "answer_echo_unverified" for r in rows)}
    return {"batch": name, "condition": manifest["condition"], "pack": (manifest.get("pack") or {}).get("id"),
            "pack_version": (manifest.get("pack") or {}).get("version"), "dry_run": bool(manifest.get("dry_run")),
            "answer_blocking": blocking,
            "holdout_labels": (manifest.get("holdout") or {}).get("labels", []),
            "rows": len(rows), "scored_variants": len(units), "excluded": dict(excluded),
            "judged_findings": len(judged_all), "findings": total_findings,
            "metrics": metrics, "structural": structural, "cost_time": cost,
            "breakdown": {"language": breakdown("language"), "category": breakdown("category"),
                          "detection": breakdown("detection")},
            "variance": variance(units)}


def variance(units):
    """Agreement across --repeat runs of the same variant (structural facts; judged capture when judged)."""
    by = defaultdict(list)
    for u in units:
        by[(u["row"]["case"], u["row"]["variant"])].append(u)
    out = []
    for (case, variant), us in sorted(by.items()):
        if len(us) < 2:
            continue
        hits = [any(v["auto"] and v["auto"]["kind"] in ("positive", "after_mapped", "suspect") for v in u["views"]) for u in us]
        counts = [sum(v["actionable"] for v in u["views"]) for u in us]
        prints = [frozenset(v["fingerprint"] for v in u["views"] if v["fingerprint"]) for u in us]
        pairs = [(a, b) for i, a in enumerate(prints) for b in prints[i + 1:]]
        jacc = [len(a & b) / len(a | b) if (a | b) else 1.0 for a, b in pairs]
        ai = [u["row"].get("ai_review") for u in us]
        out.append({"case": case, "variant": variant, "repeats": len(us),
                    "auto_location_hit_agreement": round(max(Counter(hits).values()) / len(us), 4),
                    "actionable_count": counts, "actionable_count_agreement": round(max(Counter(counts).values()) / len(us), 4),
                    "fingerprint_jaccard_mean": round(sum(jacc) / len(jacc), 4) if jacc else None,
                    "ai_review_agreement": round(max(Counter(ai).values()) / len(us), 4)})
    return {"variants_repeated": len(out), "items": out,
            "mean_auto_location_hit_agreement": round(sum(i["auto_location_hit_agreement"] for i in out) / len(out), 4) if out else None}


def evidence_validity(batch, rows, reports, corpus):
    """Re-check every published report against freshly materialized inputs (validate_bundle.check_report)."""
    from validate_bundle import check_report
    checked, failures, mismatches, evidence, claims = 0, [], [], 0, 0
    with tempfile.TemporaryDirectory(prefix="codeye-r1-evidence-") as tmp:
        for row in rows:
            if row.get("invalid") or row["key"] not in reports:
                continue
            report = reports[row["key"]]
            case = corpus.by_id[row["case"]]
            inputs = Path(tmp) / row["key"]
            digest = corpus.materialize(inputs, case, row["variant"], row.get("documented", False))
            if digest != row.get("input_digest"):
                mismatches.append(row["key"])
                continue
            checked += 1
            try:
                check_report(report, inputs)
            except (ValueError, KeyError, OSError) as exc:
                failures.append({"key": row["key"], "error": str(exc)[:200]})
            evidence += sum(len(f["evidence"]) for f in report["findings"])
            claims += sum(len(f["claims"]) for f in report["findings"])
            shutil.rmtree(inputs, ignore_errors=True)
    return {"reports_checked": checked, "rate": proportion(checked - len(failures), checked),
            "evidence_items": evidence, "claims": claims, "failures": failures, "input_mismatches": mismatches}


def score(batches, judgment_files, corpus, line_slack=2):
    judgments, conflicts, judges = load_judgments(judgment_files)
    out = {"judges": judges, "judgment_conflicts": conflicts, "batches": []}
    for b in batches:
        manifest, rows, reports = load_batch(b)
        m = batch_metrics(manifest, rows, reports, judgments, corpus, line_slack, batch_dir=b)
        m["evidence_validity"] = evidence_validity(b, rows, reports, corpus)
        out["batches"].append(m)
    labelled = [m["batch"] for m in out["batches"] if m["holdout_labels"] or m["dry_run"]]
    modes = sorted({m["answer_blocking"]["mode"] for m in out["batches"] if m.get("answer_blocking")})
    out["comparison"] = {"independent": len(out["batches"]) > 1 and not labelled,
                         "not_independent_because": labelled,
                         "answer_blocking_modes": modes,
                         "note": ("A comparison is independent only when no batch is a dry run and no pack digest "
                                  "is labelled holdout_seen_by_prior_revision (docs/EVALUATION.md#holdout-등록부). Review batches with "
                                  "different answer_blocking modes exclude different variants (docs/EVALUATION.md#정답-차단).")}
    out["report_reading"] = {"status": "not_measured",
                             "reason": "report-reading answers are scored separately with `r1_eval.py reader-score`"}
    return out


def fmt(p):
    if not isinstance(p, dict) or "k" not in p:
        return "-"
    if not p["n"]:
        return f"0/0 (unjudged {p['unjudged']})" if p["unjudged"] else "0/0"
    ci = p["wilson95"]
    tail = f" (unjudged {p['unjudged']})" if p["unjudged"] else ""
    return f"{p['k']}/{p['n']} [{ci[0]:.2f},{ci[1]:.2f}]{tail}"


def render_table(result):
    bs = result["batches"]
    head = "| metric | " + " | ".join(f"{b['condition']}:{b['pack'] or 'analyzers'}{' (dry)' if b['dry_run'] else ''}"
                                      for b in bs) + " |"
    lines = [head, "|---" * (len(bs) + 1) + "|"]
    rows = [("known-defect recall (judged)", lambda b: fmt(b["metrics"]["known_defect_recall"])),
            ("  model findings only", lambda b: fmt(b["metrics"]["known_defect_recall_model_findings"])),
            ("same-defect re-flag on after (judged)", lambda b: fmt(b["metrics"]["same_defect_reflag_after"])),
            ("FP rate normals, actionable (judged)", lambda b: fmt(b["metrics"]["false_positive_rate"]["actionable_findings"]["tricky_normal"])),
            ("FP rate after, actionable (judged)", lambda b: fmt(b["metrics"]["false_positive_rate"]["actionable_findings"]["after"])),
            ("FP rate normals+after, any finding (judged)", lambda b: fmt(b["metrics"]["false_positive_rate"]["any_finding"]["combined"])),
            ("actionable precision (judged)", lambda b: fmt(b["metrics"]["actionable_precision"])),
            ("unsupported assertions (judged)", lambda b: fmt(b["metrics"]["unsupported_assertions_judged"])),
            ("evidence validity (report check)", lambda b: fmt((b.get("evidence_validity") or {}).get("rate"))),
            ("auto: location hit on before", lambda b: fmt(b["structural"]["auto_location_hit_before"])),
            ("auto: location re-flag on after", lambda b: fmt(b["structural"]["auto_location_reflag_after"])),
            ("auto: suspect flagged on normals", lambda b: fmt(b["structural"]["auto_suspect_flag_normal"])),
            ("auto: any actionable on after/normal", lambda b: fmt(b["structural"]["auto_any_actionable_on_after_or_normal"])),
            ("ai review performed", lambda b: fmt(b["structural"]["ai_review_performed"])),
            ("model saw snapshot files, documents included (llm_exposed_files>0)",
             lambda b: fmt(b["structural"]["model_saw_snapshot_files"])),
            ("model saw code (review records, kind code)", lambda b: fmt(b["structural"]["model_saw_code"])),
            ("labelled file in investigate input (before)", lambda b: fmt(b["structural"]["labelled_file_in_investigate"])),
            ("review outcome counts (reported variants, excluded included)", lambda b: ", ".join(f"{k} {v}" for k, v in sorted(b["structural"]["review_outcomes"].items()))),
            ("reproduced without execution", lambda b: str(b["structural"]["unsupported_assertions_structural"]["reproduced_without_execution"])),
            ("review calls / ledger requests", lambda b: f"{b['cost_time']['review_calls']} / {b['cost_time']['ledger_requests']}"),
            ("ledger charged (USD)", lambda b: f"{b['cost_time']['ledger_charged_microusd'] / 1e6:.4f}"),
            ("wall s total / median", lambda b: f"{b['cost_time']['wall_s_total']} / {b['cost_time']['wall_s_median']}"),
            ("variants scored / excluded", lambda b: f"{b['scored_variants']} / {sum(b['excluded'].values())}"),
            ("answer blocking: mode / blocked variants / echo-exempted requests",
             lambda b: "-" if not b.get("answer_blocking") else
             f"{b['answer_blocking']['mode']} / {b['answer_blocking']['blocked_variants']} / "
             f"{b['answer_blocking']['echo_exempted_requests']}"),
            ("findings judged / total", lambda b: f"{b['judged_findings']} / {b['findings']}")]
    for name, fn in rows:
        lines.append(f"| {name} | " + " | ".join(fn(b) for b in bs) + " |")
    c = result["comparison"]
    lines.append("")
    lines.append(f"independent comparison: {c['independent']} {('(labelled: ' + ', '.join(c['not_independent_because']) + ')') if c['not_independent_because'] else ''}")
    if len(c.get("answer_blocking_modes", [])) > 1:
        lines.append("answer blocking differs between batches: " + ", ".join(c["answer_blocking_modes"]))
    lines.append("report reading: not measured here (reader-score)")
    return "\n".join(lines)


# ---------------------------------------------------------------- report reading (docs/EVALUATION.md#보고서-읽기-평가)

SHEET_QUESTIONS = [
    "이 보고서에서 가장 중요한 위험 3개를 고르고, 각각의 발생 조건과 근거 위치(경로:줄)를 적으세요.",
    "이 보고서가 분석하지 못한 범위(도구 미실행, 모델 미검토, 공백)를 적으세요.",
    "보고서만 보고 정한 다음 조치를 한 문장으로 적으세요.",
    "걸린 시간(초)과 답을 얻기 위해 필요했던 추가 질문 수를 적으세요.",
]


def report_gaps(report):
    gaps = []
    for t in report["tasks"]:
        if t.get("status") in ("unsupported", "skipped_policy", "failed", "skipped"):
            gaps.append(f"{t.get('key') or t.get('id')}:{t.get('reason')}")
    gaps += [l for l in report.get("limitations", []) if isinstance(l, str) and re.match(r"^[a-z_]+(:[a-z_.]+)?$", l)]
    ai = report["coverage"]["ai_review"]["status"]
    if ai != "performed":
        gaps.append("ai_review:" + ai)
    if not report["coverage"].get("llm_exposed_files"):
        gaps.append("llm_exposed_files:0")
    return sorted(set(gaps))


def reader_sheets(batch, corpus, sample, seed, output):
    manifest, rows, reports = load_batch(batch)
    output = Path(output)
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    (output / "sheets").mkdir()
    pool = [r for r in rows if not r.get("invalid") and r["key"] in reports and r.get("repeat", 1) == 1]
    groups = defaultdict(list)
    for r in pool:
        groups[(corpus.by_id[r["case"]]["kind"], r["variant"])].append(r)
    rng = random.Random(seed)
    picked = []
    order = sorted(groups)
    for members in groups.values():
        rng.shuffle(members)
    while len(picked) < min(sample, len(pool)):
        for g in order:
            if groups[g] and len(picked) < sample:
                picked.append(groups[g].pop())
    key = {"schema": "codeye-r1-reader-key", "version": 1, "batch": manifest["batch"], "seed": seed, "sheets": []}
    for i, row in enumerate(picked, 1):
        sheet_id = f"S{i:02d}"
        report = reports[row["key"]]
        (output / "sheets" / f"{sheet_id}-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=1) + "\n")
        sheet = {"sheet_id": sheet_id, "report_file": f"{sheet_id}-report.json", "questions": SHEET_QUESTIONS,
                 "answer_format": {"risks": [{"condition": "", "path": "", "line": 0}], "unanalysed_scope": [""],
                                   "next_action": "", "seconds": 0, "questions_asked": 0}}
        (output / "sheets" / f"{sheet_id}.json").write_text(json.dumps(sheet, ensure_ascii=False, indent=1) + "\n")
        case = corpus.by_id[row["case"]]
        label = case["label"]
        entry = {"sheet_id": sheet_id, "key": row["key"], "case": row["case"], "variant": row["variant"],
                 "kind": case["kind"], "unanalysed_scope": report_gaps(report)}
        if case["kind"] == "defect_pair" and row["variant"] == "before":
            entry.update(expected="labelled_defect", condition=label["trigger"], impact=label["expected_impact"],
                         locations=targets(corpus, case, "before", row.get("documented", False)),
                         next_action_reference="change " + ", ".join(label["corrected_variant"]["changed_files"])
                         + " so the documented contract holds")
        else:
            entry.update(expected="no_labelled_defect",
                         counterevidence=[{k: c.get(k) for k in ("path", "start_line", "end_line", "note")}
                                          for c in label["known_counterevidence"] if c.get("path")],
                         locations=targets(corpus, case, row["variant"], row.get("documented", False)),
                         next_action_reference="no code change for the labelled behaviour")
        key["sheets"].append(entry)
    (output / "answer-key.json").write_text(json.dumps(key, ensure_ascii=False, indent=1) + "\n")
    print(f"{len(picked)} sheets in {output / 'sheets'}; answer key {output / 'answer-key.json'} (evaluator only)")


def reader_score(key_file, answer_files, slack=2):
    key = json.loads(Path(key_file).read_text())
    if not answer_files:
        return {"status": "not_measured", "reason": "no human reader answers supplied; readers are never simulated",
                "sheets": len(key["sheets"])}
    by_sheet = {s["sheet_id"]: s for s in key["sheets"]}
    found, located, false_alarm, scope_hits, scope_total = 0, 0, 0, 0, 0
    graded_cond, cond_ok, graded_next, next_ok = 0, 0, 0, 0
    seconds, questions, answered = [], [], 0
    for f in answer_files:
        doc = json.loads(Path(f).read_text())
        for a in doc.get("sheets", []):
            s = by_sheet.get(a.get("sheet_id"))
            if s is None:
                continue
            answered += 1
            risks = a.get("risks", [])[:3]

            def at(loc):
                return any(r.get("path") == loc["path"] and loc["start_line"] - slack <= int(r.get("line") or 0)
                           <= loc["end_line"] + slack for r in risks)

            if s["expected"] == "labelled_defect":
                found += 1
                located += any(at(l) for l in s["locations"])
            else:
                false_alarm += any(at(l) for l in s["locations"])
            mentioned = " ".join(a.get("unanalysed_scope", [])).lower()
            for gap in s["unanalysed_scope"]:
                scope_total += 1
                scope_hits += any(part and part.lower() in mentioned for part in re.split(r"[:]", gap)[:1])
            graded = a.get("graded") or {}
            for ok in graded.get("condition_correct", []):
                graded_cond += 1
                cond_ok += bool(ok)
            if "next_action_correct" in graded:
                graded_next += 1
                next_ok += bool(graded["next_action_correct"])
            if isinstance(a.get("seconds"), (int, float)):
                seconds.append(a["seconds"])
            if isinstance(a.get("questions_asked"), int):
                questions.append(a["questions_asked"])
    normals = answered - found
    return {"status": "measured", "answers": answered,
            "defect_location_found": proportion(located, found),
            "false_alarm_on_counterevidence": proportion(false_alarm, normals),
            "unanalysed_scope_recall": proportion(scope_hits, scope_total),
            "condition_correct_graded": proportion(cond_ok, graded_cond),
            "next_action_correct_graded": proportion(next_ok, graded_next),
            "seconds_median": statistics.median(seconds) if seconds else None,
            "questions_mean": round(sum(questions) / len(questions), 2) if questions else None}
