#!/usr/bin/env python3
"""TC-314: tests of the dry-run upstream's concurrency and seeded delays
(scripts/fake_model_upstream.py). Loopback only; no model and no key."""
from __future__ import annotations

import http.client
import json
import sys
import threading
import time
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import fake_model_upstream as f  # noqa: E402


def payload(item: int) -> dict:
    prompt = f"Role: investigate\n<review-input>\n{json.dumps({'item': item})}\n</review-input>"
    return {"model": "fixture", "input": [{"role": "user", "content": prompt}], "max_output_tokens": 64}


class SeededDelayTest(unittest.TestCase):
    def test_delay_depends_on_seed_and_request_only(self):
        a = [f.request_delay(payload(i), 1.0, 2.0, 7) for i in range(20)]
        self.assertEqual(a, [f.request_delay(payload(i), 1.0, 2.0, 7) for i in range(20)])
        self.assertTrue(all(1.0 <= d < 3.0 for d in a))
        self.assertNotEqual(a, [f.request_delay(payload(i), 1.0, 2.0, 8) for i in range(20)])
        self.assertEqual(f.request_delay(payload(1), 1.5, 0.0, 7), 1.5)
        # The attachment and other request fields do not move the delay.
        other = payload(3)
        other["input"].insert(0, {"role": "user", "content": "1: " + "a" * 64})
        self.assertEqual(f.request_delay(other, 0.0, 2.0, 7), f.request_delay(payload(3), 0.0, 2.0, 7))


class ConcurrentAnswersTest(unittest.TestCase):
    def test_requests_are_answered_concurrently(self):
        fake = f.FakeUpstream("normal", None, delay=0.4)
        server = f.make_server(fake)
        threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.05}, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        port = server.server_address[1]
        statuses: list[int] = []
        lock = threading.Lock()

        def call(i: int) -> None:
            conn = http.client.HTTPConnection("127.0.0.1", port, timeout=10)
            conn.request("POST", "/v1/responses", json.dumps(payload(i)), {"Content-Type": "application/json"})
            status = conn.getresponse().status
            conn.close()
            with lock:
                statuses.append(status)

        start = time.monotonic()
        threads = [threading.Thread(target=call, args=(i,)) for i in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        elapsed = time.monotonic() - start
        self.assertEqual(statuses, [200] * 4)
        self.assertEqual(fake.calls, 4)
        self.assertGreaterEqual(fake.peak, 2)
        self.assertEqual(fake.in_flight, 0)
        # Four answers 0.4 s late each, one after another, would take 1.6 s.
        self.assertLess(elapsed, 1.2)


class LeadsTest(unittest.TestCase):
    """TC-338: the --leads double names leads only when the request offers
    them and the investigation has a candidate, every lead for that
    candidate and never for a name a sent excerpt declares, and answers the
    follow-up with one result per candidate or lead."""

    INPUT = {"scope": {"snapshot_manifest_digest": "0" * 64, "lead_limit": 3, "excerpts": [
        {"id": "ev_" + "a" * 24, "path": "app/main.go", "start_line": 3, "end_line": 6, "kind": "code",
         "text": "3| func main() {\n4| \tstore.Load(1)\n5| \tvault.Token()\n6| \thelper.Local()\n7| }"},
        {"id": "ev_" + "b" * 24, "path": "helper/h.go", "start_line": 1, "end_line": 1, "kind": "code",
         "text": "1| func Local() int { return 1 }"},
    ]}}

    def test_investigate_names_undeclared_calls(self):
        out = f.result_for("investigate", self.INPUT, leads=True)
        self.assertEqual([(x["symbol"], x["evidence_id"], x["candidate"]) for x in out["leads"]],
                         [("Load", "ev_" + "a" * 24 + ":4-4", 1), ("Token", "ev_" + "a" * 24 + ":5-5", 1)])
        self.assertNotIn("leads", f.result_for("investigate", self.INPUT))
        # A call into a sent path's package comes first, a standard-library
        # call after the other calls.
        standard = json.loads(json.dumps(self.INPUT))
        standard["scope"]["excerpts"][0]["text"] = "3| func main() {\n4| \tfmt.Println(1)\n5| \tvault.Token()\n6| \thelper.Open()\n7| }"
        self.assertEqual([(x["symbol"], x["evidence_id"], x["candidate"]) for x in f.result_for("investigate", standard, leads=True)["leads"]],
                         [("Open", "ev_" + "a" * 24 + ":6-6", 1), ("Token", "ev_" + "a" * 24 + ":5-5", 1), ("Println", "ev_" + "a" * 24 + ":4-4", 1)])
        unoffered = json.loads(json.dumps(self.INPUT))
        del unoffered["scope"]["lead_limit"]
        self.assertNotIn("leads", f.result_for("investigate", unoffered, leads=True))
        # No candidate, no lead: a lead names the candidate it follows.
        uncited = {"scope": {"snapshot_manifest_digest": "0" * 64, "lead_limit": 3, "excerpts": []}}
        out = f.result_for("investigate", uncited, leads=True)
        self.assertEqual(out["candidates"], [])
        self.assertNotIn("leads", out)

    def test_follow_up_refines_and_adds(self):
        follow = {"scope": {"snapshot_manifest_digest": "0" * 64, "excerpts": [
            {"id": "ev_" + "a" * 24, "path": "app/main.go", "start_line": 4, "end_line": 4, "kind": "code", "text": "4| \tstore.Load(1)"},
            {"id": "ev_" + "c" * 24, "path": "store/s.go", "start_line": 10, "end_line": 14, "kind": "code", "text": "10| func Load(n int) {"}]},
            "leads": [{"id": "lead_1", "evidence_id": "ev_" + "a" * 24, "candidate_id": "investigate_1", "resolved_evidence_ids": ["ev_" + "c" * 24]},
                      {"id": "lead_2", "evidence_id": "ev_" + "a" * 24, "candidate_id": "investigate_1", "resolved_evidence_ids": ["ev_" + "c" * 24]},
                      {"id": "lead_3", "evidence_id": "ev_" + "a" * 24, "candidate_id": "investigate_1", "resolved_evidence_ids": []}],
            "candidates": [{"id": "investigate_1", "evidence_ids": ["ev_" + "a" * 24]}]}
        results = f.result_for("leads", follow, leads=True)["results"]
        self.assertEqual([(r["basis"], r["disposition"], r["evidence_ids"]) for r in results],
                         [("investigate_1", "refined", ["ev_" + "a" * 24, "ev_" + "c" * 24]), ("lead_2", "added", ["ev_" + "a" * 24, "ev_" + "c" * 24])])
        self.assertNotIn("lead_3", [r["basis"] for r in results])  # nothing resolved, nothing to cite
        repeated = f.result_for("leads", follow, shape=1.0, leads=True)["results"]
        self.assertEqual(len(repeated), 4)
        self.assertEqual(repeated[0], repeated[1])
        # A refinement keeps every citation of its candidate and rests on a
        # resolved excerpt that is not partial; with only a partial one the
        # lead that would refine gets no result, and the others still add.
        cited = json.loads(json.dumps(follow))
        cited["candidates"][0]["evidence_ids"] = ["ev_" + "d" * 24, "ev_" + "e" * 24]
        first = f.result_for("leads", cited, leads="project")["results"][0]
        self.assertEqual((first["disposition"], first["evidence_ids"]),
                         ("refined", ["ev_" + "d" * 24, "ev_" + "e" * 24, "ev_" + "a" * 24, "ev_" + "c" * 24]))
        partial = json.loads(json.dumps(follow))
        partial["leads"][0]["partial_evidence_ids"] = ["ev_" + "c" * 24]
        self.assertEqual([(r["basis"], r["disposition"]) for r in f.result_for("leads", partial, leads=True)["results"]], [("lead_2", "added")])

    PROJECT = {"scope": {"snapshot_manifest_digest": "0" * 64, "lead_limit": 3, "excerpts": [
        {"id": "ev_" + "a" * 24, "path": "app/main.go", "start_line": 3, "end_line": 9, "kind": "code",
         "text": "3| func Run(n int) int {\n4| \tif check(n) {\n5| \t\treturn total(n) + len(xs)\n6| \t}\n7| \tfmt.Println(n)\n"
                 "8| \treturn s.Save(n) + store.Load(n) + Run(n)\n9| }"},
    ]}}

    def test_project_mode_names_undeclared_project_calls(self):
        ev = "ev_" + "a" * 24
        named = [(x["symbol"], x["evidence_id"], x["need"], x["candidate"]) for x in f.result_for("investigate", self.PROJECT, leads="project")["leads"]]
        # Unqualified calls before calls on a receiver; no builtin (len), no
        # keyword (if), no standard namespace (fmt) and no name a sent
        # excerpt declares (Run).
        self.assertEqual(named, [("check", ev + ":4-4", "definition", 1), ("total", ev + ":5-5", "definition", 1), ("Save", ev + ":8-8", "definition", 1)])
        # The qualified mode is unchanged: qualified calls only.
        self.assertEqual([x["symbol"] for x in f.result_for("investigate", self.PROJECT, leads=True)["leads"]], ["Save", "Load", "Println"])
        self.assertEqual(f.result_for("investigate", self.PROJECT, leads="qualified"), f.result_for("investigate", self.PROJECT, leads=True))

    def test_project_mode_leaves_out_builtins_of_six_languages(self):
        lines = {
            "a/x.py": ["print(len(items))", "value = sorted(items)", "logging.info(x)", "return compute(items)"],
            "a/x.ts": ["console.log(parseInt(s))", "const d = new Widget(s)", "if (isNaN(s)) {", "return compute(s)"],
            "a/X.java": ["System.out.println(s.toString())", "String t = String.valueOf(s)", "return compute(s);"],
            "a/x.c": ["char *p = malloc(sizeof(int));", "printf(\"%d\", n);", "return compute(n);"],
            "a/x.rs": ["let v = Some(x).unwrap();", "println!(\"{}\", v);", "compute(v)"],
            "a/x.go": ["n := len(append(xs, 1))", "panic(err)", "return compute(n)"],
        }
        for path, body in lines.items():
            text = "\n".join(f"{i + 1}| {line}" for i, line in enumerate(body))
            request = {"scope": {"snapshot_manifest_digest": "0" * 64, "lead_limit": 3, "excerpts": [
                {"id": "ev_" + "a" * 24, "path": path, "start_line": 1, "end_line": len(body), "kind": "code", "text": text}]}}
            with self.subTest(path=path):
                self.assertEqual([x["symbol"] for x in f.result_for("investigate", request, leads="project")["leads"]], ["compute"])

    def test_project_mode_leaves_out_names_the_sent_code_declares(self):
        lines = {
            # a JS/TS method head with no type before its name, with and
            # without a return type; a call that opens a callback block is
            # still a call
            "a/w.ts": ["class Widget {", "  load(x) {", "    return compute(x)", "  }", "  size(): number {", "    return 1", "  }",
                       "}", "run(() => {", "  Widget.load(1)", "})"],
            # a Python class with a base, a Rust struct and a C struct
            "a/m.py": ["class Store(Base):", "    def save(self, x):", "        return Store(compute(x))"],
            "a/s.rs": ["struct Pair(i32, i32);", "fn make() -> Pair {", "    Pair(compute(), 1)", "}"],
        }
        for path, body in lines.items():
            text = "\n".join(f"{i + 1}| {line}" for i, line in enumerate(body))
            request = {"scope": {"snapshot_manifest_digest": "0" * 64, "lead_limit": 3, "excerpts": [
                {"id": "ev_" + "a" * 24, "path": path, "start_line": 1, "end_line": len(body), "kind": "code", "text": text}]}}
            want = ["compute", "run"] if path == "a/w.ts" else ["compute"]
            with self.subTest(path=path):
                self.assertEqual([x["symbol"] for x in f.result_for("investigate", request, leads="project")["leads"]], want)

    def test_project_mode_reads_no_call_in_a_string(self):
        lines = {
            # SQL in a Go raw string across lines and a Go const block
            "a/q.go": ["const (", "\tlimit = 3", ")", "var q = `SELECT clock_timestamp()", "WHERE EXISTS (SELECT 1)`",
                       "rows := db.Query(q, fmt.Sprintf(\"%d\", count(n)))"],
            # a call in a quoted string, a JS template literal and a Python docstring
            "a/s.ts": ["log(\"retry(later)\")", "const t = `wait(${n})`", "return compute(t)"],
            "a/d.py": ['"""Runs report(x) on', 'every row."""', "return compute('x(y)')"],
        }
        want = {"a/q.go": ["count", "Query"], "a/s.ts": ["log", "compute"], "a/d.py": ["compute"]}
        for path, body in lines.items():
            text = "\n".join(f"{i + 1}| {line}" for i, line in enumerate(body))
            request = {"scope": {"snapshot_manifest_digest": "0" * 64, "lead_limit": 3, "excerpts": [
                {"id": "ev_" + "a" * 24, "path": path, "start_line": 1, "end_line": len(body), "kind": "code", "text": text}]}}
            with self.subTest(path=path):
                self.assertEqual([x["symbol"] for x in f.result_for("investigate", request, leads="project")["leads"]], want[path])


if __name__ == "__main__":
    unittest.main()
