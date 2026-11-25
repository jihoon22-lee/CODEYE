#!/usr/bin/env python3
"""Tests of the dry-run upstream's concurrency and seeded delays
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


if __name__ == "__main__":
    unittest.main()
