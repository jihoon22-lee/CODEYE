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
    them, never for a name a sent excerpt declares, and answers the
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
                         [("Load", "ev_" + "a" * 24 + ":4-4", 1), ("Token", "ev_" + "a" * 24 + ":5-5", 0)])
        self.assertNotIn("leads", f.result_for("investigate", self.INPUT))
        unoffered = json.loads(json.dumps(self.INPUT))
        del unoffered["scope"]["lead_limit"]
        self.assertNotIn("leads", f.result_for("investigate", unoffered, leads=True))

    def test_follow_up_refines_and_adds(self):
        follow = {"scope": {"snapshot_manifest_digest": "0" * 64, "excerpts": [
            {"id": "ev_" + "a" * 24, "path": "app/main.go", "start_line": 4, "end_line": 4, "kind": "code", "text": "4| \tstore.Load(1)"},
            {"id": "ev_" + "c" * 24, "path": "store/s.go", "start_line": 10, "end_line": 14, "kind": "code", "text": "10| func Load(n int) {"}]},
            "leads": [{"id": "lead_1", "evidence_id": "ev_" + "a" * 24, "candidate_id": "investigate_1", "resolved_evidence_ids": ["ev_" + "c" * 24]},
                      {"id": "lead_2", "evidence_id": "ev_" + "a" * 24, "resolved_evidence_ids": ["ev_" + "c" * 24]},
                      {"id": "lead_3", "evidence_id": "ev_" + "a" * 24, "candidate_id": "investigate_1", "resolved_evidence_ids": ["ev_" + "c" * 24]}],
            "candidates": [{"id": "investigate_1", "evidence_ids": ["ev_" + "a" * 24]}]}
        results = f.result_for("leads", follow, leads=True)["results"]
        self.assertEqual([(r["basis"], r["disposition"], r["evidence_ids"]) for r in results],
                         [("investigate_1", "refined", ["ev_" + "a" * 24, "ev_" + "c" * 24]), ("lead_2", "added", ["ev_" + "a" * 24, "ev_" + "c" * 24])])
        repeated = f.result_for("leads", follow, shape=1.0, leads=True)["results"]
        self.assertEqual(len(repeated), 4)
        self.assertEqual(repeated[0], repeated[1])


if __name__ == "__main__":
    unittest.main()
