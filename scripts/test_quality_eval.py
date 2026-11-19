#!/usr/bin/env python3
"""CI smoke for the evaluation runner and scoring (no product run, no tool, no model).

Fixture reports are synthetic report documents bound to real corpus cases plus one
real tools-condition report captured from the dry run (testdata/quality_eval). Run:
python3 -m unittest discover -s scripts -p test_quality_eval.py -v
"""
from __future__ import annotations

import json
from pathlib import Path
import sys
import tempfile
import unittest

SCRIPTS = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS))
import quality_eval  # noqa: E402
import quality_score  # noqa: E402

CORPUS = quality_eval.Corpus()
TESTDATA = SCRIPTS / "testdata/quality_eval"


def finding(fid, path, start, end, validation="supported", producer="model", title="A finding title", symbol=None):
    loc = {"snapshot_id": "snap", "path": path, "start_line": start, "end_line": end}
    if symbol:
        loc["symbol"] = symbol
    return {"id": fid, "title": title, "category": "correctness", "severity": "medium", "validation": validation,
            "disposition": "open", "locations": [loc], "evidence": [], "claims": [], "conditions": [],
            "observation_ids": ["obs_1"] if producer == "tool" else [],
            "fingerprint": "fp2:" + fid.ljust(64, "0")[:64],
            "fingerprint_basis": {"producer": producer}}


def report(findings, ai="performed", exposed=2):
    actionable = sum(f["validation"] in ("supported", "reproduced") for f in findings)
    return {"status": "partial", "findings": findings, "tasks": [{"id": "t", "key": "review.investigate", "status": "succeeded", "reason": None}],
            "summary": {"total_findings": len(findings), "actionable_findings": actionable,
                        "pending_candidates": sum(f["validation"] in ("unreviewed", "inconclusive") for f in findings),
                        "refuted_findings": sum(f["validation"] == "refuted" for f in findings), "priorities": []},
            "coverage": {"ai_review": {"status": ai}, "review_calls": [], "llm_exposed_files": exposed}, "limitations": []}


def make_batch(root, name, rows, reports, *, dry_run=False, labels=(), condition="review", pack="review-r1"):
    batch = Path(root) / name
    (batch / "reports").mkdir(parents=True)
    manifest = {"schema": quality_eval.MANIFEST_SCHEMA, "batch": name, "condition": condition, "dry_run": dry_run,
                "corpus": {"digest": CORPUS.digest}, "pack": {"id": pack, "version": "x"} if pack else None,
                "holdout": {"labels": list(labels)}, "budget": None}
    (batch / "manifest.json").write_text(json.dumps(manifest))
    with open(batch / "rows.jsonl", "w") as f:
        for row in rows:
            case = CORPUS.by_id[row["case"]]
            full = {"repeat": 1, "status": "partial", "invalid": None, "documented": False, "ai_review": "performed",
                    "llm_exposed_files": 2, "wall_s": 10.0, "review_calls": [], "usage": {}, **row}
            full.setdefault("key", quality_eval.key_of(row["case"], row["variant"], full["repeat"]))
            for k in ("language", "kind", "category", "detection"):
                full.setdefault(k, case[k])
            f.write(json.dumps(full) + "\n")
            if full["key"] in reports:
                (batch / "reports" / (full["key"] + ".json")).write_text(json.dumps(reports[full["key"]]))
    return batch


class StatisticsTests(unittest.TestCase):
    def test_wilson_reference_values(self):
        self.assertIsNone(quality_score.wilson(0, 0))
        self.assertEqual(quality_score.wilson(0, 10), [0.0, 0.2775])
        self.assertEqual(quality_score.wilson(5, 10), [0.2366, 0.7634])
        self.assertEqual(quality_score.wilson(10, 10), [0.7225, 1.0])
        p = quality_score.proportion(3, 4, unjudged=2)
        self.assertEqual((p["rate"], p["unjudged"]), (0.75, 2))

    def test_map_range_follows_the_diff(self):
        before = "a\nb\nc\nd\n"
        after = "a\nx\ny\nb\nc\nd\n"
        self.assertEqual(quality_score.map_range(before, after, 3, 3), (5, 5))
        case = CORPUS.by_id["R1-GO-06"]
        tg = quality_score.targets(CORPUS, case, "after")
        self.assertEqual(tg[0]["kind"], "after_mapped")
        after_text = CORPUS.render(case, "after")["counters.go"].split("\n")
        self.assertIn("func (c *Counters) Add", after_text[tg[0]["start_line"] - 1])


class SelectionTests(unittest.TestCase):
    def test_stratified_sample_is_seeded_and_covers_strata(self):
        chosen, record = quality_eval.select(CORPUS, sample=20, seed=3)
        again, _ = quality_eval.select(CORPUS, sample=20, seed=3)
        other, _ = quality_eval.select(CORPUS, sample=20, seed=4)
        self.assertEqual([c["id"] for c in chosen], [c["id"] for c in again])
        self.assertNotEqual([c["id"] for c in chosen], [c["id"] for c in other])
        self.assertEqual(len(chosen), 20)
        strata = record["strata"]
        self.assertEqual(sum(s["selected"] for s in strata.values()), 20)
        self.assertTrue(all(s["selected"] >= 1 for s in strata.values()), strata)  # 18 strata, sample 20
        small, record = quality_eval.select(CORPUS, sample=5, seed=1, languages={"go"})
        self.assertEqual(len(small), 5)
        self.assertEqual({c["language"] for c in small}, {"go"})
        self.assertEqual(len(quality_eval.select(CORPUS)[0]), 55)

    def test_variant_plan_and_repeats(self):
        chosen = [CORPUS.by_id["R1-GO-06"], CORPUS.by_id["R1-GO-10"]]
        plan = quality_eval.variant_plan(CORPUS, chosen)
        self.assertEqual([(c["id"], v, r) for c, v, r in plan],
                         [("R1-GO-06", "before", 1), ("R1-GO-06", "after", 1), ("R1-GO-10", "before", 1)])
        plan = quality_eval.variant_plan(CORPUS, chosen, {"before"}, repeat=3, repeat_cases={"R1-GO-10"})
        self.assertEqual([(c["id"], r) for c, _, r in plan], [("R1-GO-06", 1), ("R1-GO-10", 1), ("R1-GO-10", 2), ("R1-GO-10", 3)])


class PlannerTests(unittest.TestCase):
    def test_constants_estimate_and_refusals(self):
        plan = quality_eval.variant_plan(CORPUS, [CORPUS.by_id["R1-GO-06"]] * 5)
        est = quality_eval.estimate("review-r1", plan)
        self.assertEqual(est["variants"], 10)
        self.assertTrue(est["basis"].startswith("constants:"))
        self.assertEqual(est["upper"]["calls"], 55)  # 5 roles x 10 variants x 1.1
        self.assertAlmostEqual(est["upper"]["ledger_microusd"], 10 * 5 * 1.1 * 22_148 * 1.5, delta=2)
        self.assertLess(est["expected"]["ledger_microusd"], est["upper"]["ledger_microusd"])
        report = {"remaining_microusd": 3_000_000, "max_requests": 200, "requests": 63, "halted": False}
        self.assertEqual(quality_eval.budget_gate(est, report, 1_000_000, 5), [])
        report["remaining_microusd"] = 2_500_000
        self.assertEqual(len(quality_eval.budget_gate(est, report, 1_000_000, 5)), 1)  # 1.83 USD > 2.5 - 1.0
        report.update(remaining_microusd=9_000_000, requests=150)
        self.assertIn("requests", quality_eval.budget_gate(est, report, 1_000_000, 5)[0])
        report.update(requests=0, halted=True)
        self.assertEqual(quality_eval.budget_gate(est, report, 1_000_000, 5), ["relay_halted"])

    def test_history_from_a_batch(self):
        with tempfile.TemporaryDirectory() as tmp:
            rows = [{"case": "R1-GO-06", "variant": "before",
                     "ledger": {"requests": [{"role": "investigate", "input_bytes": 15000, "usage": None},
                                             {"role": "verify", "input_bytes": 21000, "usage": None}]}},
                    {"case": "R1-GO-06", "variant": "after", "ledger": {"requests": [{"role": "investigate", "input_bytes": 9000}]}}]
            batch = make_batch(tmp, "h", rows, {}, dry_run=True)
            hist = quality_eval.history_profile([batch], "review-r1")
            self.assertEqual(hist["calls_per_variant"], {"investigate": 1.0, "verify": 0.5})
            self.assertEqual(hist["input_bytes_max"]["verify"], 21000)
            plan = quality_eval.variant_plan(CORPUS, [CORPUS.by_id["R1-GO-06"]])
            est = quality_eval.estimate("review-r1", plan, history=hist)
            self.assertEqual(est["per_call_microusd"]["verify"], int(21000 / 3 * 5 + 600 * 10))
            with self.assertRaises(SystemExit):  # another pack's batch is not history
                quality_eval.history_profile([batch], "review-v1")
            self.assertIsNone(quality_eval.history_profile([], "review-v1"))
            v1 = make_batch(tmp, "v1", rows[:1], {}, dry_run=True, pack="codeye.review")  # review-v1's pack id
            self.assertEqual(quality_eval.history_profile([v1], "review-v1")["variants"], 1)

    def test_variant_with_slices_is_bounded_by_the_ceiling(self):
        # A variant whose history made more calls than review-r1 has roles planned
        # module slices: with repairs it may use the whole per-attempt ceiling.
        with tempfile.TemporaryDirectory() as tmp:
            sliced = [{"role": r, "input_bytes": 20000} for r in ("documents", "investigate", "verify") + ("investigate", "verify") * 3]
            rows = [{"case": "R1-GO-06", "variant": "before", "ledger": {"requests": sliced}},
                    {"case": "R1-GO-06", "variant": "after", "ledger": {"requests": sliced[:3]}}]
            hist = quality_eval.history_profile([make_batch(tmp, "h", rows, {}, dry_run=True)], "review-r1")
            plan = quality_eval.variant_plan(CORPUS, [CORPUS.by_id["R1-GO-06"]])
            est = quality_eval.estimate("review-r1", plan, history=hist, max_calls=40)
            per_call = est["per_call_microusd"]
            self.assertEqual(est["max_calls"], 40)
            self.assertEqual(est["expected"]["calls"], 12)
            self.assertEqual(est["upper"]["calls"], 40 + int(5 * 1.1 + 0.999))
            dearest = max(per_call["investigate"], per_call["verify"])
            self.assertGreaterEqual(est["upper"]["ledger_microusd"], int(40 * dearest * 1.5))


class RegressionPilotCoverageTests(unittest.TestCase):
    """check_coverage: the conditions registered before a paid run with slices."""

    def setUp(self):
        import regression_pilot
        from bundle_checks.review import role_pack
        self.rp = regression_pilot
        self.digests = {r: v["digest"] for r, v in role_pack(("go",))[1].items()}
        self.tmp = tempfile.TemporaryDirectory()
        self.out = Path(self.tmp.name)
        (self.out / "pilot").mkdir()

    def tearDown(self):
        self.tmp.cleanup()

    def record(self, name, stage, files, calls, slice_key=None):
        doc = {"schema_version": "0.3" if slice_key else "0.2", "stage": stage, "calls": [{}] * calls,
               "evidence": [{"path": p, "kind": "code", "origin": "context"} for p in files]}
        if slice_key:
            doc["slice"] = {"key": slice_key, "modules": ["go:."]}
        (self.out / "pilot" / (name + ".json")).write_text(json.dumps(doc))

    def rows(self, exhausted=("s003",), regression_tasks=("review.investigate",)):
        tasks = [{"key": k, "status": "succeeded", "reason": None} for k in ("review.investigate", "review.verify", "review.scope")]
        for key in ("s001", "s002", "s003"):
            status = "budget_exhausted" if key in exhausted else "succeeded"
            tasks += [{"key": f"review.{s}.{key}", "status": status, "reason": None} for s in ("investigate", "verify")]
        regression = {"case": "Q01-before", "tasks": [{"key": k, "status": "succeeded", "reason": None} for k in regression_tasks],
                      "review_pack": "review-r1", "review_calls": [{"role": "investigate", "pack_sha256": self.digests["investigate"]}]}
        return [regression, {"case": "pilot", "status": "completed", "ai_review": "performed", "tasks": tasks}]

    def check(self, rows, exposed=12, max_calls=99, ledger=None, calls_used=9, ledger_sent=5, required=(), limitations=(), calls=()):
        tasks = [dict(t, required_for_verdict=t["key"] in required) for t in rows[1]["tasks"]]
        report = {"tasks": tasks, "limitations": list(limitations),
                  "coverage": {"llm_exposed_files": exposed, "review_calls": [{"task_key": k} for k in calls]},
                  "code_review": {"ledger": {"calls_used": calls_used, "max_calls": max_calls, "status": "partial", "modules": [{"files_sent_to_model": ledger_sent}]}}}
        (self.out / "pilot.json").write_text(json.dumps(report))
        (self.out / "pilot" / "scope.json").write_text(json.dumps({"kind": "review_scope", "budget": {"max_calls": max_calls}}))
        return self.rp.check_coverage(rows, self.out, SCRIPTS.parent, max_calls, ledger)

    def write_pilot(self):
        self.record("investigate", "investigate", ["a.go", "b.go"], 1)
        self.record("verify", "verify", [], 1)
        self.record("investigate.s001", "investigate", ["c.go"], 1, "s001")
        self.record("verify.s001", "verify", [], 1, "s001")
        self.record("investigate.s002", "investigate", ["d.go", "e.go"], 3, "s002")

    def test_conditions_hold(self):
        self.write_pilot()
        failures, pilot = self.check(self.rows())
        self.assertEqual(failures, [])
        self.assertEqual((pilot["recorded_calls"], pilot["calls_used"], pilot["overview_files"], pilot["files_sent_by_records"], pilot["slices_admitted"]), (7, 9, 2, 5, 2))
        self.assertEqual(pilot["dry_run_expected"], [])

    def test_each_condition_fails(self):
        self.write_pilot()
        rows = self.rows(regression_tasks=("review.investigate", "review.scope"))
        self.assertIn("slice tasks", self.check(rows)[0][0])
        rows = self.rows()
        rows[0]["review_calls"][0]["pack_sha256"] = "0" * 64
        self.assertIn("pack digest", self.check(rows)[0][0])
        self.assertIn("above the ceiling", " ".join(self.check(self.rows(), max_calls=6, calls_used=7)[0]))
        self.assertTrue(any("calls_used None" in f for f in self.check(self.rows(), calls_used=None)[0]))
        self.assertIn("llm_exposed_files", self.check(self.rows(), exposed=2)[0][0])
        self.assertIn("ledger files sent", self.check(self.rows(), ledger_sent=2)[0][0])
        self.assertIn("did not read", self.check(self.rows(), limitations=("unrecognized_artifact:review.scope",))[0][0])
        self.assertIn("with a record", self.check(self.rows(exhausted=("s002",)))[0][0])
        self.assertIn("with a record or a call", self.check(self.rows(), calls=("review.investigate.s003",))[0][0])
        # A paid run (no test ledger): the fake-upstream and host causes are failures.
        rows = self.rows()
        rows[1]["status"] = "partial"
        rows[1]["tasks"] += [{"key": "review.interpret", "status": "failed", "reason": "review_output_invalid"},
                             {"key": "ruff", "status": "unsupported", "reason": "tool_not_configured"}]
        self.assertIn("status partial", self.check(rows, exposed=None, required=("ruff",))[0][0])
        # A dry run: they are expected and listed; another cause still fails.
        test = self.out / "test-ledger.jsonl"
        test.write_text(json.dumps({"type": "header", "upstream": "test-loopback"}) + "\n")
        failures, pilot = self.check(rows, exposed=None, required=("ruff",), ledger=test)
        self.assertEqual(failures, [])
        self.assertEqual(len(pilot["dry_run_expected"]), 2)
        rows[1]["tasks"].append({"key": "go-vet", "status": "failed", "reason": "toolchain_mismatch"})
        self.assertIn("status partial", self.check(rows, exposed=None, required=("ruff", "go-vet"), ledger=test)[0][0])
        self.record("investigate.s002", "investigate", ["a.go"], 1, "s002")
        self.assertIn("two investigate requests", self.check(self.rows())[0][0])
        ledger = self.out / "ledger.jsonl"
        ledger.write_text(json.dumps({"type": "header", "upstream": "test-loopback"}) + "\n" + json.dumps({"type": "refused", "code": "budget_exhausted"}) + "\n")
        self.assertTrue(any("refused" in f for f in self.check(self.rows(), ledger=ledger)[0]))


class EchoAuditTests(unittest.TestCase):
    def test_exempted_ids_must_come_from_this_variants_model_output(self):
        import model_budget_relay as gateway
        doc = {"schema": gateway.FORBIDDEN_SCHEMA, "version": gateway.FORBIDDEN_VERSION,
               "normalization": gateway.FORBIDDEN_NORMALIZATION, "corpus_digest": CORPUS.digest,
               "entries": [{"id": "R1-GO-01/token/3", "mode": "words", "text": "goroutine leak"},
                           {"id": "R1-GO-08/token/0", "mode": "subword", "text": "shadowed"}]}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "f.json"
            path.write_text(json.dumps(doc))
            records = [{"candidates": [{"title": "Goroutine leak when the reader stops", "id": "c1"}]}]
            ok = [{"role": "verify", "echo_matches": ["R1-GO-01/token/3"], "echo_match_count": 1}]
            self.assertEqual(quality_eval.echo_audit(ok, records, path), [])
            self.assertEqual(quality_eval.echo_audit([{"role": "verify"}], records, path), [])
            other = [{"role": "verify", "echo_matches": ["R1-GO-08/token/0"], "echo_match_count": 1}]
            self.assertEqual(quality_eval.echo_audit(other, records, path), ["R1-GO-08/token/0"])  # another variant's words
            cut = [{"role": "verify", "echo_matches": ["R1-GO-01/token/3"], "echo_match_count": 20}]
            self.assertEqual(quality_eval.echo_audit(cut, records, path), ["<truncated>"])


class ReviewOutcomeTests(unittest.TestCase):
    def test_secret_screen_is_its_own_outcome(self):
        def report(ai, tasks):
            return {"coverage": {"ai_review": {"status": ai, "reason": None}},
                    "tasks": [{"key": k, "status": s, "reason": r} for k, s, r in tasks]}
        screened = report("not_performed", [("gitleaks", "failed", "tool_error"),
                                            ("review.investigate", "skipped_policy", "secret_screen_failed"),
                                            ("review.verify", "skipped_policy", "secret_screen_failed")])
        self.assertEqual(quality_eval.review_outcome(screened), "secret_screen_failed")
        self.assertEqual(quality_eval.review_outcome(report("performed", [("review.verify", "succeeded", None)])), "performed")
        tools = report("not_performed", [("review.context", "skipped_policy", "model_source_not_approved")])
        self.assertEqual(quality_eval.review_outcome(tools), "model_source_not_approved")
        other = report("not_performed", [("review.investigate", "failed", "review_input_unavailable")])
        self.assertEqual(quality_eval.review_outcome(other), "review_input_unavailable")

    def test_review_invalid_precedence(self):
        clean = {"blocked": [], "refused": []}
        self.assertIsNone(quality_eval.review_invalid(None, clean, "performed"))
        self.assertEqual(quality_eval.review_invalid(None, clean, "secret_screen_failed"), "secret_screen_failed")
        self.assertEqual(quality_eval.review_invalid("source_changed", {"blocked": [{}], "refused": []}, "performed"),
                         "source_changed")
        self.assertEqual(quality_eval.review_invalid(None, {"blocked": [{}], "refused": ["budget_exhausted"]},
                                                "secret_screen_failed"), "blocked_answer_leak")
        self.assertEqual(quality_eval.review_invalid(None, {"blocked": [], "refused": ["budget_exhausted"]}, None),
                         "budget_refused")
        self.assertEqual(quality_eval.review_invalid(None, {**clean, "echo_unverified": ["x"]}, "secret_screen_failed"),
                         "answer_echo_unverified")

    def test_agent_switches(self):
        self.assertEqual(quality_eval.agent_switches(["python-both-type-checkers", "python-both-type-checkers"]),
                         ["python-both-type-checkers"])
        for bad in ("--x", "X", "a b", ""):
            with self.assertRaises(SystemExit):
                quality_eval.agent_switches([bad])


class CaptureListTests(unittest.TestCase):
    def test_capture_list_is_a_valid_list_that_blocks_nothing(self):
        import model_budget_relay as gateway
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "capture.json"
            quality_eval.capture_list(out, CORPUS)
            phrases = gateway.load_forbidden(str(out))
            self.assertEqual((phrases.purpose, phrases.corpus_digest), (gateway.FORBIDDEN_CAPTURE, CORPUS.digest))
            texts = [t for v in ("before", "after") for t in CORPUS.render(CORPUS.by_id["R1-GO-06"], v).values()]
            self.assertEqual(phrases.matches(texts), [])
            with self.assertRaises(FileExistsError):
                quality_eval.capture_list(out, CORPUS)


class RegistryTests(unittest.TestCase):
    def test_label_prior_real_revisions_only(self):
        with tempfile.TemporaryDirectory() as root:
            entry = {"pack_id": "review-r1", "pack_digest": "a" * 64, "corpus_digest": CORPUS.digest,
                     "condition": "review", "first_run_at": "t1", "dry_run": True}
            self.assertTrue(quality_eval.register(root, entry))
            self.assertFalse(quality_eval.register(root, dict(entry, first_run_at="t2"))) # append once
            entries = quality_eval.registry_entries(root)
            self.assertEqual(quality_eval.holdout_labels(entries, "review-r1", "b" * 64, CORPUS.digest), ([], []))
            quality_eval.register(root, dict(entry, dry_run=False))
            entries = quality_eval.registry_entries(root)
            self.assertEqual(quality_eval.holdout_labels(entries, "review-r1", "b" * 64, CORPUS.digest),
                             ([quality_eval.HOLDOUT_LABEL], ["a" * 64]))
            self.assertEqual(quality_eval.holdout_labels(entries, "review-r1", "a" * 64, CORPUS.digest), ([], []))
            self.assertEqual(quality_eval.holdout_labels(entries, "review-v1", "b" * 64, CORPUS.digest), ([], []))
            self.assertEqual(len(Path(root, quality_eval.REGISTRY).read_text().splitlines()), 2)

    def test_ledger_delta_attributes_blocks_and_refusals(self):
        records = [{"type": "reserve", "id": "1", "role_hint": "investigate", "input_bytes": 10, "message_count": 2,
                    "reserved_microusd": 900},
                   {"type": "settle", "id": "1", "status": "ok", "charged_microusd": 100, "usage": {"prompt_tokens": 3}},
                   {"type": "blocked", "role_hint": "verify", "matches": ["X/token/0"], "match_count": 1},
                   {"type": "refused", "code": "budget_exhausted"},
                   {"type": "reserve", "id": "2", "role_hint": "verify", "input_bytes": 20, "reserved_microusd": 700}]
        d = quality_eval.ledger_delta(records)
        self.assertEqual(d["charged_microusd"], 800)  # the pending reservation counts in full
        self.assertEqual([q["status"] for q in d["requests"]], ["ok", "pending"])
        self.assertEqual(d["blocked"][0]["role"], "verify")
        self.assertEqual(d["refused"], ["budget_exhausted"])


class ScoringTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        rows = [{"case": "R1-GO-06", "variant": "before"}, {"case": "R1-GO-06", "variant": "after"},
                {"case": "R1-GO-10", "variant": "before"}, {"case": "R1-GO-01", "variant": "before"},
                {"case": "R1-GO-02", "variant": "before", "invalid": "blocked_answer_leak"}]
        reports = {
            "R1-GO-06.before.1": report([finding("f1", "counters.go", 23, 23, producer="tool", validation="unreviewed"),
                                         finding("f2", "counters.go", 22, 25),
                                         finding("f3", "middleware.go", 5, 6, validation="inconclusive")]),
            "R1-GO-06.after.1": report([finding("g1", "counters.go", 23, 24)]),
            "R1-GO-10.before.1": report([finding("n1", "poller.go", 40, 41, title="Poller leaks")]),
            "R1-GO-01.before.1": report([]),
            "R1-GO-02.before.1": report([finding("x", "manifest.go", 30, 30)]),
        }
        self.batch = make_batch(self.tmp.name, "b1", rows, reports)

    def tearDown(self):
        self.tmp.cleanup()

    def template(self):
        return quality_score.judgment_template(self.batch, CORPUS)

    def test_template_prefills_auto_matches_only(self):
        t = self.template()
        keys = [i["key"] for i in t["items"]]
        self.assertNotIn("R1-GO-02.before.1", keys)  # invalid rows are not judged
        items = {i["key"]: i for i in t["items"]}
        auto = {f["finding_id"]: f["auto_match"] for f in items["R1-GO-06.before.1"]["findings"]}
        self.assertEqual(auto["f1"]["kind"], "positive")
        self.assertIsNone(auto["f3"])
        self.assertEqual(items["R1-GO-06.after.1"]["findings"][0]["auto_match"]["kind"], "after_mapped")
        self.assertEqual(items["R1-GO-10.before.1"]["findings"][0]["auto_match"]["kind"], "suspect")
        self.assertTrue(all(f["verdict"] is None for i in t["items"] for f in i["findings"]))

    def metrics(self, judgment_files=()):
        manifest, rows, reports = quality_score.load_batch(self.batch)
        judgments, conflicts, _ = quality_score.load_judgments(judgment_files)
        return quality_score.batch_metrics(manifest, rows, reports, judgments, CORPUS, batch_dir=self.batch), conflicts

    def test_code_exposure_comes_from_records_not_the_file_count(self):
        # GO-06 before: investigate saw the labelled file; GO-01 before: README only (still counted by llm_exposed_files).
        def keep(key, stage, evidence, outcome="accepted"):
            d = Path(self.batch) / "records" / key
            d.mkdir(parents=True, exist_ok=True)
            (d / f"review.{stage}-0.json").write_text(json.dumps(
                {"stage": stage, "calls": [{"outcome": outcome}] if outcome else [], "evidence": evidence}))
        keep("R1-GO-06.before.1", "investigate", [{"path": "counters.go", "kind": "code"},
                                                  {"path": "README.md", "kind": "document"}])
        keep("R1-GO-01.before.1", "investigate", [{"path": "README.md", "kind": "document"}])
        # prepared but never answered: a blocked call and a stage without calls are not exposure
        keep("R1-GO-01.before.1", "verify", [{"path": "manifest.go", "kind": "code"}], outcome="model_unavailable")
        keep("R1-GO-01.before.1", "context", [{"path": "load.go", "kind": "code"}], outcome=None)
        s = self.metrics()[0]["structural"]
        self.assertEqual(s["model_saw_snapshot_files"]["k"], 4)
        self.assertEqual((s["model_saw_code"]["k"], s["model_saw_code"]["n"]), (1, 4))
        self.assertEqual((s["labelled_file_in_investigate"]["k"], s["labelled_file_in_investigate"]["n"]), (1, 2))
        self.assertIn("R1-GO-01.before.1", s["no_code_variants"])
        self.assertNotIn("R1-GO-06.before.1", s["no_code_variants"])

    def test_unjudged_items_are_counted_not_scored(self):
        m, _ = self.metrics()
        recall = m["metrics"]["known_defect_recall"]
        # GO-06 before has unjudged findings -> unjudged; GO-01 before has no finding -> determinable miss.
        self.assertEqual((recall["k"], recall["n"], recall["unjudged"]), (0, 1, 1))
        self.assertEqual(m["excluded"], {"blocked_answer_leak": 1})
        s = m["structural"]
        self.assertEqual((s["auto_location_hit_before"]["k"], s["auto_location_hit_before"]["n"]), (1, 2))
        self.assertEqual(s["auto_location_reflag_after"]["k"], 1)
        self.assertEqual(s["auto_suspect_flag_normal"]["k"], 1)
        self.assertEqual(s["auto_duplicate_candidates"]["findings"], 1)  # f2 overlaps f1

    def test_judged_metrics_and_conflicts(self):
        t = self.template()
        for item in t["items"]:
            for f in item["findings"]:
                if f["finding_id"] == "f2":
                    f.update(verdict="valid_actionable", matches_label=True)
                elif f["finding_id"] == "g1":
                    f.update(verdict="false_positive", matches_label=True)
                elif f["finding_id"] == "n1":
                    f.update(verdict="false_positive", matches_label=False)
        a = Path(self.tmp.name, "a.json")
        a.write_text(json.dumps(dict(t, judge="coordinator")))
        m, _ = self.metrics([a])
        mm = m["metrics"]
        self.assertEqual((mm["known_defect_recall"]["k"], mm["known_defect_recall"]["n"]), (1, 2))
        self.assertEqual((mm["known_defect_recall_model_findings"]["k"], mm["known_defect_recall_model_findings"]["n"]), (1, 2))
        self.assertEqual((mm["same_defect_reflag_after"]["k"], mm["same_defect_reflag_after"]["n"]), (1, 1))
        fp = mm["false_positive_rate"]["actionable_findings"]
        self.assertEqual((fp["tricky_normal"]["k"], fp["after"]["k"], fp["combined"]["n"]), (1, 1, 2))
        self.assertEqual((mm["actionable_precision"]["k"], mm["actionable_precision"]["n"]), (1, 3))
        self.assertEqual(m["breakdown"]["language"]["go"]["known_defect_recall"]["k"], 1)
        # A second judge disagreeing on f2 makes it unjudged rather than picking a side.
        t2 = json.loads(a.read_text())
        for item in t2["items"]:
            for f in item["findings"]:
                if f["finding_id"] == "f2":
                    f["verdict"] = "insufficient_evidence"
        b = Path(self.tmp.name, "b.json")
        b.write_text(json.dumps(t2))
        m, conflicts = self.metrics([a, b])
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(m["metrics"]["known_defect_recall"]["unjudged"], 1)

    def test_variance_and_independence(self):
        rows = [{"case": "R1-GO-06", "variant": "before", "repeat": 1}, {"case": "R1-GO-06", "variant": "before", "repeat": 2}]
        reports = {"R1-GO-06.before.1": report([finding("a", "counters.go", 23, 23)]),
                   "R1-GO-06.before.2": report([])}
        batch = make_batch(self.tmp.name, "rep", rows, reports, labels=[quality_eval.HOLDOUT_LABEL])
        manifest, rows_, reports_ = quality_score.load_batch(batch)
        m = quality_score.batch_metrics(manifest, rows_, reports_, {}, CORPUS)
        item = m["variance"]["items"][0]
        self.assertEqual((item["repeats"], item["auto_location_hit_agreement"], item["fingerprint_jaccard_mean"]), (2, 0.5, 0.0))
        result = {"batches": [m, m], "comparison": {"independent": False, "not_independent_because": ["rep"]}}
        self.assertIn("independent comparison: False", quality_score.render_table(result))


class RealReportTests(unittest.TestCase):
    """A real tools-condition report (go vet + staticcheck, captured from the dry run) re-checked against
    freshly materialized corpus inputs: evidence validity and auto-matching on real bytes."""

    def test_captured_report(self):
        rows = [json.loads(l) for l in (TESTDATA / "rows.jsonl").read_text().splitlines() if l.strip()]
        with tempfile.TemporaryDirectory() as tmp:
            batch = Path(tmp) / "captured"
            (batch / "reports").mkdir(parents=True)
            (batch / "manifest.json").write_text((TESTDATA / "manifest.json").read_text())
            (batch / "rows.jsonl").write_text((TESTDATA / "rows.jsonl").read_text())
            for row in rows:
                src = TESTDATA / "reports" / (row["key"] + ".json")
                (batch / "reports" / src.name).write_text(src.read_text())
            manifest, rows_, reports = quality_score.load_batch(batch)
            ev = quality_score.evidence_validity(batch, rows_, reports, CORPUS)
            self.assertEqual(ev["failures"], [])
            self.assertEqual(ev["input_mismatches"], [])
            self.assertEqual(ev["reports_checked"], len(rows))
            m = quality_score.batch_metrics(manifest, rows_, reports, {}, CORPUS, batch_dir=batch)
            self.assertGreaterEqual(m["structural"]["auto_location_hit_before"]["k"], 1)
            t = quality_score.judgment_template(batch, CORPUS)
            self.assertTrue(any(f["producer"] == "tool" for i in t["items"] for f in i["findings"]))

    def test_recorded_rule_context_needs_only_a_digest(self):
        # internal/report/validate.go: paths are omitted when the tool read no file (gitleaks).
        from validate_bundle import check_report
        key = "R1-GO-06.before.1"
        row = next(json.loads(l) for l in (TESTDATA / "rows.jsonl").read_text().splitlines() if key in l)
        report = json.loads((TESTDATA / "reports" / (key + ".json")).read_text())
        context = {"rule_pack": "codeye-secrets-gitleaks", "rule_pack_version": "1", "selection": [],
                   "repository_config": "honored", "source_suppressions": "disabled", "config": "recorded",
                   "config_digest": "0" * 64}
        report["coverage"]["tools"][0]["rule_context"] = context
        with tempfile.TemporaryDirectory() as tmp:
            CORPUS.materialize(Path(tmp) / "in", CORPUS.by_id[row["case"]], row["variant"], row["documented"])
            check_report(report, Path(tmp) / "in")
            del context["config_digest"]
            with self.assertRaises(ValueError):
                check_report(report, Path(tmp) / "in")


class ReaderTests(unittest.TestCase):
    def test_sheets_hide_labels_and_scoring_needs_answers(self):
        with tempfile.TemporaryDirectory() as tmp:
            rows = [{"case": "R1-GO-06", "variant": "before"}, {"case": "R1-GO-10", "variant": "before"}]
            reports = {"R1-GO-06.before.1": report([finding("f", "counters.go", 23, 23)], ai="not_performed", exposed=0),
                       "R1-GO-10.before.1": report([])}
            batch = make_batch(tmp, "r", rows, reports)
            out = Path(tmp) / "reading"
            quality_score.reader_sheets(batch, CORPUS, 2, 0, out)
            sheets = sorted((out / "sheets").glob("S??.json"))
            self.assertEqual(len(sheets), 2)
            for s in (out / "sheets").iterdir():
                text = s.read_text()
                self.assertNotIn("R1-GO", text)
                self.assertNotIn(CORPUS.by_id["R1-GO-06"]["label"]["trigger"][:40], text)
            key = json.loads((out / "answer-key.json").read_text())
            self.assertEqual(quality_score.reader_score(out / "answer-key.json", [])["status"], "not_measured")
            defect = next(s for s in key["sheets"] if s["expected"] == "labelled_defect")
            self.assertIn("ai_review:not_performed", defect["unanalysed_scope"])
            normal = next(s for s in key["sheets"] if s["expected"] == "no_labelled_defect")
            answers = {"sheets": [{"sheet_id": defect["sheet_id"], "risks": [{"path": "counters.go", "line": 23, "condition": "x"}],
                                   "unanalysed_scope": ["AI review was not performed"], "next_action": "x", "seconds": 100,
                                   "questions_asked": 1, "graded": {"condition_correct": [True], "next_action_correct": False}},
                                  {"sheet_id": normal["sheet_id"], "risks": [], "unanalysed_scope": [], "seconds": 60,
                                   "questions_asked": 0}]}
            a = Path(tmp) / "answers.json"
            a.write_text(json.dumps(answers))
            res = quality_score.reader_score(out / "answer-key.json", [a])
            self.assertEqual(res["status"], "measured")
            self.assertEqual((res["defect_location_found"]["k"], res["false_alarm_on_counterevidence"]["k"]), (1, 0))
            self.assertEqual((res["condition_correct_graded"]["k"], res["next_action_correct_graded"]["k"]), (1, 0))
            self.assertEqual(res["seconds_median"], 80)


if __name__ == "__main__":
    unittest.main()
