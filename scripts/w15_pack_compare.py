"""W15 TC-100: compare two archived pilot result directories (active vs candidate pack).

Reads only archived results written by ``r0_pilot.py`` (summary.json, one report
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
                if "stage" in doc and "calls" in doc:
                    records.append(doc)
        out[row["case"]] = {"row": row, "report": report, "records": records}
    return out


def structural(case):
    report, records = case["report"], case["records"]
    findings = report["findings"]
    validations = Counter(f["validation"] for f in findings)
    calls = Counter((c["role"], c["outcome"]) for c in report["coverage"].get("review_calls", []))
    tasks = {t["key"]: (t["status"], t.get("reason")) for t in report["tasks"]}
    # A reproduced verdict needs an execution record; the R0/W15 runs execute no tests.
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
        if a["report"]["inputs"]["manifest_sha256"] != c["report"]["inputs"]["manifest_sha256"]:
            raise SystemExit(f"{name}: 두 팩의 입력 manifest가 다릅니다")
        table[name] = {
            "manifest_sha256": a["report"]["inputs"]["manifest_sha256"],
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
