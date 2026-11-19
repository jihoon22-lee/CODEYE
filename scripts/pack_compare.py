"""TC-100: compare two archived pilot result directories (active vs candidate pack).

Reads only archived results written by ``regression_pilot.py`` (summary.json, one report
JSON per case and the kept review/discovery artifacts) and an optional judgment
file kept outside every snapshot. It never sends model requests. Structural
metrics come from the reports; defect-detection judgments come from the
judgment file and are reported as the evaluator's labels, not as measured
precision.
"""

import argparse
from collections import Counter
import json
from pathlib import Path

ACTION = {"supported", "reproduced"}
# Validator error codes (docs/REVIEW.md) grouped into the TC-100 blocking classes.
ERROR_CLASS = {
    **dict.fromkeys(("evidence_id_unknown", "evidence_missing", "evidence_kind_invalid", "observation_id_unknown",
                     "evidence_not_at_observation", "code_evidence_missing"), "fabricated_evidence"),
    **dict.fromkeys(("scope_generalized", "scope_unknown_path", "scope_invalid", "module_out_of_scope"), "scope_violation"),
    "execution_claim": "execution_claim",
}
PENDING = {"unreviewed", "inconclusive"}


def load(results):
    rows = json.loads((results / "summary.json").read_bytes())
    out = {}
    for row in rows:
        report = json.loads((results / (row["case"] + ".json")).read_bytes())
        records = []
        kept = results / row["case"]
        if kept.is_dir():
            for f in sorted(kept.glob("*.json")):
                doc = json.loads(f.read_bytes())
                if "stage" in doc and "calls" in doc:  # review record 0.1 or 0.2
                    records.append(doc)
        out[row["case"]] = {"row": row, "report": report, "records": records}
    return out


def structural(case):
    report, records = case["report"], case["records"]
    findings = report["findings"]
    validations = Counter(f["validation"] for f in findings)
    calls = Counter((c["role"], c["outcome"]) for c in report["coverage"].get("review_calls", []))
    tasks = {t["key"]: (t["status"], t.get("reason")) for t in report["tasks"]}
    # A reproduced verdict needs an execution record; the regression runs execute no tests.
    reproduced = validations.get("reproduced", 0)
    summary = report["summary"]
    stats_ok = (
        summary["total_findings"] == len(findings)
        and summary["actionable_findings"] == sum(validations[v] for v in ACTION)
        and summary["pending_candidates"] == sum(validations[v] for v in PENDING)
        and summary["refuted_findings"] == validations.get("refuted", 0)
    )
    rejected = [
        {"stage": r["stage"], "outcome": r.get("outcome"), "reason": r.get("failure_reason")}
        for r in records
        if r.get("outcome") not in (None, "", "accepted")
    ]
    # Output errors are counted per call attempt across stages (the content of a
    # rejected stage is dropped, so the errors are the only trace of what was refused).
    errors = Counter()
    usage = Counter()
    seen = set()
    for r in records:
        for c in r.get("calls", []):
            # review-v1 stage records repeat earlier stages' calls; count each request once.
            key = (c.get("request_sha256"), c.get("attempt"), c.get("role"))
            if key in seen:
                continue
            seen.add(key)
            for e in c.get("output_errors") or []:
                errors[ERROR_CLASS.get(e, "other")] += 1
            u = c.get("usage") or {}
            for k in ("input", "output", "input_tokens", "output_tokens"):
                if isinstance(u.get(k), int):
                    usage[k] += u[k]
    markers = Counter(
        m for r in records for m in r.get("limitations", [])
        if m in ("execution_claim_rejected", "quarantined") or m.startswith("upstream_")
    )
    markers.update(
        m for r in records for v in r.get("verifications", []) for m in v.get("limitations", [])
        if m == "reproduction_claim_rejected"
    )
    candidates = Counter(r["stage"] for r in records for _ in r.get("candidates", []))
    return {
        "status": case["row"]["status"],
        "ai_review": report["coverage"]["ai_review"]["status"],
        "calls": sum(calls.values()),
        "calls_by_role": {f"{role}:{outcome}": n for (role, outcome), n in sorted(calls.items())},
        "tasks": {k: v[0] + (f"({v[1]})" if v[1] else "") for k, v in sorted(tasks.items())},
        "findings": dict(sorted(validations.items())),
        "actionable": sum(validations[v] for v in ACTION),
        "reproduced_without_execution": reproduced,
        "stats_consistent": stats_ok,
        "rejected_outputs": rejected,
        "output_errors": dict(sorted(errors.items())),
        "markers": dict(sorted(markers.items())),
        "candidates_by_stage": dict(sorted(candidates.items())),
        "documents_contracts": sum(len(r.get("contracts", [])) for r in records),
        "documents_conflicts": sum(len(r.get("conflicts", [])) for r in records),
        "instruction_notes": sum(len(r.get("instruction_notes", [])) for r in records),
        "usage": dict(usage),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("active", type=Path)
    parser.add_argument("candidate", type=Path)
    parser.add_argument("--judgments", type=Path, help="evaluator labels kept outside the snapshots")
    parser.add_argument("--output", type=Path, help="write the comparison JSON here")
    args = parser.parse_args()
    packs = {"active": load(args.active), "candidate": load(args.candidate)}
    if set(packs["active"]) != set(packs["candidate"]):
        raise SystemExit("두 결과의 사례 집합이 다릅니다")
    judgments = json.loads(args.judgments.read_bytes()) if args.judgments else {}
    table = {}
    for name in sorted(packs["active"]):
        a, c = packs["active"][name], packs["candidate"][name]
        # The manifest digest also binds the fix time (A16), so two runs of the same
        # bytes differ there; the archived source tree digest must match instead.
        if a["row"]["source_tree_sha256"] != c["row"]["source_tree_sha256"] or a["row"]["input"] != c["row"]["input"]:
            raise SystemExit(f"{name}: 두 팩의 입력 소스가 다릅니다")
        table[name] = {
            "source_tree_sha256": a["row"]["source_tree_sha256"],
            "active": structural(a),
            "candidate": structural(c),
            "judgment": judgments.get(name),
        }
    totals = {}
    for pack in ("active", "candidate"):
        rows = [t[pack] for t in table.values()]
        totals[pack] = {
            "cases": len(rows),
            "calls": sum(r["calls"] for r in rows),
            "performed": sum(r["ai_review"] == "performed" for r in rows),
            "actionable": sum(r["actionable"] for r in rows),
            "reproduced_without_execution": sum(r["reproduced_without_execution"] for r in rows),
            "stats_inconsistent": sum(not r["stats_consistent"] for r in rows),
            "rejected_outputs": sum(len(r["rejected_outputs"]) for r in rows),
            "output_errors": dict(sum((Counter(r["output_errors"]) for r in rows), Counter())),
            "usage": dict(sum((Counter(r["usage"]) for r in rows), Counter())),
        }
    result = {"cases": table, "totals": totals}
    if args.output:
        args.output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print("| case | active calls/actionable/ai | candidate calls/actionable/ai |")
    print("|---|---|---|")
    for name, t in table.items():
        a, c = t["active"], t["candidate"]
        print(f"| {name} | {a['calls']}/{a['actionable']}/{a['ai_review']} | {c['calls']}/{c['actionable']}/{c['ai_review']} |")
    print(json.dumps(totals, ensure_ascii=False))


if __name__ == "__main__":
    main()
