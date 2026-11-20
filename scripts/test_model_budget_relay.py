#!/usr/bin/env python3
"""Local-simulation tests for scripts/model_budget_relay.py.

No network and no billable calls: the upstream is a local fake OpenAI server on
127.0.0.1 reached through the in-process _connection_factory hook, and the real
HTTPS constructor is replaced by a recorder. A fake API key canary is used; the
real OPENAI_API_KEY is scrubbed from every child environment.

Run: python3 -m unittest discover -s scripts -p test_model_budget_relay.py -v
"""
from __future__ import annotations

import http.client
import http.server
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest

SCRIPTS = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS))
import model_budget_relay as g  # noqa: E402

CANARY_KEY = "sk-test-CANARY-" + "x" * 24
MODEL = "gpt-6.2-sol"
REASONING_CANARY = "PRIVATE-REASONING-CANARY"


def _no_network(timeout):
    raise AssertionError("real upstream connection attempted in a test")


_ORIGINAL_HTTPS = g.OpenAIUpstream._https  # verified with a recorder, never connected
g.OpenAIUpstream._https = staticmethod(_no_network)


def child_env(**extra):
    env = {k: v for k, v in os.environ.items()
           if k != "OPENAI_API_KEY" and not k.lower().endswith("_proxy")}
    env.update(extra)
    return env


# ---------------------------------------------------------------------------
# Fake OpenAI (local double)
# ---------------------------------------------------------------------------
class FakeOpenAI:
    def __init__(self):
        self.mode = "ok"
        self.text = None  # echo tests: replace the assistant answer text
        self.calls = []
        self.release = threading.Event()
        self.received = threading.Event()
        fake = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *args):
                return

            def do_POST(self):
                body = self.rfile.read(int(self.headers["Content-Length"]))
                fake.calls.append({"path": self.path, "headers": dict(self.headers),
                                   "payload": json.loads(body)})
                fake.received.set()
                status, payload, headers = fake.respond(json.loads(body))
                self.send_response(status)
                for key, value in headers.items():
                    self.send_header(key, value)
                data = payload if isinstance(payload, bytes) else json.dumps(payload).encode()
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                try:
                    self.wfile.write(data)
                except OSError:  # the relay gave up (deadline) or was killed
                    pass

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.server.daemon_threads = True
        self.port = self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def factory(self, timeout):
        return http.client.HTTPConnection("127.0.0.1", self.port, timeout=timeout)

    def respond(self, payload):
        ok = {"id": "resp_x", "object": "response", "status": "completed",
              "model": MODEL + "-2026-10-01", "service_tier": "default", "incomplete_details": None,
              "output": [
                  {"type": "reasoning", "id": "rs_1", "summary": [{"type": "summary_text", "text": REASONING_CANARY}],
                   "encrypted_content": REASONING_CANARY},
                  {"type": "message", "id": "msg_1", "role": "assistant", "status": "completed",
                   "content": [{"type": "output_text", "text": "hello ", "annotations": []},
                               {"type": "output_text", "text": "from fake", "annotations": []}]
                   if self.text is None else [{"type": "output_text", "text": self.text, "annotations": []}]}],
              "usage": {"input_tokens": 40, "output_tokens": 30, "total_tokens": 70,
                        "input_tokens_details": {"cached_tokens": 8},
                        "output_tokens_details": {"reasoning_tokens": 12}}}
        mode = self.mode
        if mode == "ok":
            return 200, ok, {}
        if mode == "no_usage":
            ok.pop("usage")
            return 200, ok, {}
        if mode == "priority":
            ok["service_tier"] = "priority"
            return 200, ok, {}
        if mode == "other_model":
            ok["model"] = "gpt-6.2-solar"
            return 200, ok, {}
        if mode == "no_tier":
            ok.pop("service_tier")
            return 200, ok, {}
        if mode == "incomplete":
            ok.update(status="incomplete", incomplete_details={"reason": "max_output_tokens"})
            ok["output"] = ok["output"][:1]  # the whole cap went to reasoning
            return 200, ok, {}
        if mode == "failed":
            ok.update(status="failed", error={"code": "server_error", "message": "x"})
            return 200, ok, {}
        if mode == "unsupported":
            return 400, {"error": {"message": "Unsupported parameter: 'x'.", "type": "invalid_request_error",
                                   "param": "max_output_tokens", "code": "unsupported_parameter"}}, {}
        if mode == "huge_usage":
            ok["usage"]["output_tokens"] = 99999
            return 200, ok, {}
        if mode == "key_echo":
            return 401, {"error": {"message": "Incorrect API key provided: " + CANARY_KEY,
                                   "type": "invalid_request_error", "code": "invalid_api_key"}}, {}
        if mode == "server_error":
            return 500, {"error": {"message": "boom", "type": "server_error"}}, {}
        if mode == "redirect":
            return 302, b"", {"Location": "http://127.0.0.1:1/elsewhere"}
        if mode == "hang":
            self.release.wait(30)
            return 200, ok, {}
        raise AssertionError(mode)

    def close(self):
        self.release.set()
        self.server.shutdown()
        self.server.server_close()


def chat_request(**extra):
    request = {"model": MODEL, "messages": [{"role": "system", "content": "be brief"},
                                            {"role": "user", "content": "say hi"}]}
    request.update(extra)
    return request


def http_call(port, method, path, body=None, headers=None, raw_host=None):
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=20)
    data = json.dumps(body).encode() if isinstance(body, (dict, list)) else body
    hdrs = {"Content-Type": "application/json"}
    hdrs.update(headers or {})
    connection.putrequest(method, path, skip_host=True, skip_accept_encoding=True)
    connection.putheader("Host", raw_host or f"127.0.0.1:{port}")
    for key, value in hdrs.items():
        connection.putheader(key, value)
    if data is not None and "Transfer-Encoding" not in hdrs:
        connection.putheader("Content-Length", str(len(data)))
    connection.endheaders(data)
    response = connection.getresponse()
    result = response.status, response.getheader("Content-Type"), response.read()
    connection.close()
    return result


# ---------------------------------------------------------------------------
class PolicyTests(unittest.TestCase):
    def test_budget_parsing_and_hard_cap(self):
        self.assertEqual(g.parse_budget("9.99"), 9_990_000)
        self.assertEqual(g.parse_budget("0.5"), 500_000)
        for bad in ("10", "9.990001", "0", "-1", "nan", "inf", "1.0000001", "abc"):
            with self.assertRaises(ValueError, msg=bad):
                g.parse_budget(bad)

    def test_reservation_is_worst_case_and_doubled(self):
        bound, reserved = g.reservation_for(1000, 2, 1536)
        self.assertEqual(bound, 1000 + 2 * 16 + 256)
        self.assertEqual(reserved, 2 * (bound * 5 + 1536 * 10) + 1000)
        _, largest = g.reservation_for(g.DEFAULT_MAX_INPUT_BYTES, g.MAX_MESSAGES, g.OUTPUT_TOKEN_CAP)
        self.assertLess(largest, 600_000)  # < $0.60 per request at the default caps

    def test_payload_is_rebuilt_text_only(self):
        request = chat_request(tools=[{"type": "function", "function": {"name": "x"}}],
                               tool_choice="auto", temperature=0.2, n=4, service_tier="priority",
                               prompt_cache_retention="24h", max_tokens=99999, stream=True,
                               stream_options={"include_usage": True})
        request["messages"].append({"role": "user", "content": [{"type": "text", "text": "a"},
                                                                {"type": "text", "text": "b",
                                                                 "cache_control": {"type": "ephemeral"}}]})
        prepared = g.build_upstream_request(request, MODEL, 4096)
        payload = prepared["payload"]
        self.assertEqual(set(payload), {"model", "input", "max_output_tokens", "reasoning",
                                        "service_tier", "store"})
        self.assertEqual(payload["max_output_tokens"], 1536)
        self.assertEqual(payload["reasoning"], {"effort": "low"})
        self.assertIs(payload["store"], False)
        self.assertEqual(payload["service_tier"], "default")
        self.assertEqual(payload["input"][0], {"role": "system", "content": "be brief"})
        self.assertEqual(payload["input"][-1], {"role": "user", "content": "a\nb"})
        self.assertEqual(prepared["dropped_keys"], ["n", "prompt_cache_retention", "service_tier",
                                                    "temperature", "tool_choice", "tools"])
        self.assertTrue(prepared["stream"] and prepared["include_usage"])
        self.assertEqual(g.build_upstream_request(chat_request(max_completion_tokens=200), MODEL,
                                                  4096)["output_cap"], 200)

    def test_rejects_non_text_and_tool_messages(self):
        cases = {
            "image": {"role": "user", "content": [{"type": "image_url", "image_url": {"url": "x"}}]},
            "audio": {"role": "user", "content": [{"type": "input_audio"}]},
            "tool_role": {"role": "tool", "content": "x", "tool_call_id": "1"},
            "tool_calls": {"role": "assistant", "content": None, "tool_calls": [{"id": "1"}]},
            "bad_role": {"role": "function", "content": "x"},
        }
        for name, message in cases.items():
            with self.assertRaises(g.RequestRejected, msg=name):
                g.build_upstream_request({"model": MODEL, "messages": [message]}, MODEL, 4096)
        with self.assertRaises(g.RequestRejected):
            g.build_upstream_request(chat_request(model="gpt-4o"), MODEL, 4096)
        with self.assertRaises(g.RequestRejected) as raised:
            g.build_upstream_request({"model": MODEL, "messages": [{"role": "user", "content": "é" * 3000}]},
                                     MODEL, 4096)
        self.assertEqual(raised.exception.status, 413)
        with self.assertRaises(g.RequestRejected):
            g.build_upstream_request({"model": MODEL, "messages": [{"role": "user", "content": "x"}] * 65},
                                     MODEL, 4096)
        with self.assertRaises(g.RequestRejected):
            g.build_upstream_request(chat_request(max_tokens=True), MODEL, 4096)

    def test_real_upstream_is_pinned_https_without_proxy(self):
        recorded = {}

        class Response:
            status = 200

            def __init__(self):
                self.data = [b'{"ok":true}', b""]

            def read1(self, n):
                return self.data.pop(0)

        class Recorder:
            def __init__(self, host, port, timeout=None, context=None):
                recorded.update(host=host, port=port, context=context)
                self.sock = None

            def connect(self):
                recorded["connected"] = True

            def request(self, method, path, body=None, headers=None):
                recorded.update(method=method, path=path, headers=headers)

            def getresponse(self):
                return Response()

            def close(self):
                pass

        original = g.http.client.HTTPSConnection
        g.http.client.HTTPSConnection = Recorder
        try:
            upstream = g.OpenAIUpstream("sk-unit", _connection_factory=_ORIGINAL_HTTPS)
            status, body = upstream.post(b"{}", time.monotonic() + 5)
        finally:
            g.http.client.HTTPSConnection = original
        self.assertEqual((status, body), (200, b'{"ok":true}'))
        self.assertEqual((recorded["host"], recorded["port"]), ("api.openai.com", 443))
        self.assertEqual((recorded["method"], recorded["path"]), ("POST", "/v1/responses"))
        self.assertIsNotNone(recorded["context"])
        self.assertEqual(recorded["headers"]["Authorization"], "Bearer sk-unit")


# ---------------------------------------------------------------------------
class LedgerTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="relay-ledger-")
        self.path = os.path.join(self.dir.name, "ledger.jsonl")

    def tearDown(self):
        self.dir.cleanup()

    def reserve(self, ledger, amount):
        return ledger.reserve(model=MODEL, reserved_microusd=amount, input_bytes=1, input_token_bound=10,
                              message_count=1, output_cap=10, dropped_keys=[])

    def test_private_file_and_admission_stops_before_cap(self):
        ledger = g.Ledger(self.path, cap_microusd=10_000)
        self.assertEqual(os.stat(self.path).st_mode & 0o777, 0o600)
        self.reserve(ledger, 4000)
        self.reserve(ledger, 4000)
        with self.assertRaises(g.BudgetRefused) as refused:
            self.reserve(ledger, 4000)
        self.assertEqual(refused.exception.code, "budget_exhausted")
        self.reserve(ledger, 2000)  # exactly at cap is allowed
        report = ledger.report()
        self.assertEqual(report["charged_microusd_total"], 10_000)
        self.assertEqual(report["pending_count"], 3)
        ledger.close()

    def test_settlement_and_restart_keep_conservative_totals(self):
        ledger = g.Ledger(self.path, cap_microusd=50_000)
        ok = self.reserve(ledger, 10_000)
        ledger.settle(ok, status="ok", charged_microusd=700,
                      usage={"prompt_tokens": 40, "completion_tokens": 50, "reasoning_tokens": 10})
        failed = self.reserve(ledger, 10_000)
        ledger.settle(failed, status="http_error", charged_microusd=10_000, usage=None)
        self.reserve(ledger, 10_000)  # crash: never settled
        ledger.close()
        reopened = g.Ledger(self.path, cap_microusd=g.HARD_CAP_MICROUSD)  # cannot raise the cap
        report = reopened.report()
        self.assertEqual(report["cap_microusd"], 50_000)
        self.assertEqual(report["reserved_microusd_total"], 30_000)
        self.assertEqual(report["settled_charged_microusd"], 10_700)
        self.assertEqual(report["pending_reserved_microusd"], 10_000)
        self.assertEqual(report["charged_microusd_total"], 20_700)
        self.assertEqual(report["usage"]["prompt_tokens"], 40)
        self.assertEqual(report["usage"]["reasoning_tokens"], 10)
        self.assertEqual(report["standard_rate_estimate_microusd"], 40 * 2 + 50 * 10)
        self.assertEqual(report["settled_by_status"], {"ok": 1, "http_error": 1})
        reopened.close()
        lowered = g.Ledger(self.path, cap_microusd=25_000, max_requests=3)
        self.assertEqual(lowered.report()["cap_microusd"], 25_000)
        with self.assertRaises(g.BudgetRefused) as refused:
            self.reserve(lowered, 1)
        self.assertEqual(refused.exception.code, "request_limit_reached")
        lowered.close()
        again = g.Ledger(self.path, cap_microusd=40_000, max_requests=500)
        self.assertEqual((again.report()["cap_microusd"], again.report()["max_requests"]), (25_000, 3))
        again.close()

    def test_torn_tail_repaired_and_corruption_fails_closed(self):
        ledger = g.Ledger(self.path, cap_microusd=10_000)
        self.reserve(ledger, 1000)
        ledger.close()
        with open(self.path, "ab") as handle:
            handle.write(b'{"type":"reserve","id":"torn","reserved_')
        repaired = g.Ledger(self.path)
        report = repaired.report()
        self.assertEqual(report["charged_microusd_total"], 1000)
        repaired.close()
        lines = Path(self.path).read_text().splitlines()
        self.assertEqual(json.loads(lines[-1])["type"], "repair")
        lines.insert(1, "{not json")
        Path(self.path).write_text("\n".join(lines) + "\n")
        with self.assertRaises(g.LedgerError):
            g.Ledger(self.path)

    def test_rejects_shared_or_symlinked_ledger(self):
        Path(self.path).touch(mode=0o644)
        os.chmod(self.path, 0o644)
        with self.assertRaises(g.LedgerError):
            g.Ledger(self.path, cap_microusd=1000)
        link = os.path.join(self.dir.name, "link.jsonl")
        os.symlink(self.path, link)
        with self.assertRaises(g.LedgerError):
            g.Ledger(link, cap_microusd=1000)

    def test_consecutive_failures_halt_until_audited_clear(self):
        ledger = g.Ledger(self.path, cap_microusd=100_000)
        for _ in range(g.FAILURE_HALT_THRESHOLD):
            rid = self.reserve(ledger, 1000)
            ledger.settle(rid, status="timeout", charged_microusd=1000, usage=None)
        with self.assertRaises(g.BudgetRefused) as refused:
            self.reserve(ledger, 1000)
        self.assertEqual(refused.exception.code, "relay_halted")
        self.assertEqual(ledger.report()["halt_reasons"], ["consecutive_failures"])
        ledger.clear_halt("operator reviewed")
        self.reserve(ledger, 1000)
        report = ledger.report()
        self.assertEqual(report["cap_microusd"], 100_000)
        self.assertEqual(report["charged_microusd_total"], 4000)
        ledger.close()

    def test_concurrent_processes_never_exceed_cap(self):
        g.Ledger(self.path, cap_microusd=21_500).close()
        code = (
            "import sys; sys.path.insert(0, sys.argv[1]); import model_budget_relay as g\n"
            "l = g.Ledger(sys.argv[2]); n = 0\n"
            "while True:\n"
            "    try:\n"
            "        l.reserve(model='m', reserved_microusd=1000, input_bytes=1, input_token_bound=1,"
            " message_count=1, output_cap=1, dropped_keys=[])\n"
            "        n += 1\n"
            "    except g.BudgetRefused:\n"
            "        break\n"
            "print(n)\n")
        processes = [subprocess.Popen([sys.executable, "-c", code, str(SCRIPTS), self.path],
                                      stdout=subprocess.PIPE, env=child_env()) for _ in range(6)]
        counts = [int(p.communicate(timeout=60)[0]) for p in processes]
        self.assertEqual(sum(counts), 21)
        ledger = g.Ledger(self.path)
        report = ledger.report()
        self.assertEqual(report["requests"], 21)
        self.assertEqual(report["charged_microusd_total"], 21_000)
        ledger.close()


# ---------------------------------------------------------------------------
class GatewayHTTPTests(unittest.TestCase):
    statuses: list[int] = []

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="relay-gw-")
        self.fake = FakeOpenAI()
        self.ledger = g.Ledger(os.path.join(self.dir.name, "ledger.jsonl"), cap_microusd=2_000_000)
        self.events = []
        self.gateway = g.Gateway(model=MODEL, ledger=self.ledger, token="tok-123",
                                 upstream=g.OpenAIUpstream(CANARY_KEY, _connection_factory=self.fake.factory),
                                 log=self.events.append)
        self.server = g.start_server(self.gateway)
        self.port = self.gateway.port
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.fake.close()
        self.ledger.close()
        self.dir.cleanup()

    def call(self, body=None, method="POST", path="/v1/chat/completions", headers=None, **kw):
        hdrs = {"Authorization": "Bearer tok-123"}
        hdrs.update(headers or {})
        result = http_call(self.port, method, path, body, hdrs, **kw)
        GatewayHTTPTests.statuses.append(result[0])
        self.assertNotIn(CANARY_KEY.encode(), result[2])
        return result

    def report(self):
        return self.ledger.report()

    def test_non_stream_success_charges_usage_at_worst_case(self):
        status, ctype, body = self.call(chat_request(tools=[{"type": "function"}], temperature=1))
        self.assertEqual((status, ctype), (200, "application/json"))
        data = json.loads(body)
        self.assertEqual(data["choices"][0]["message"]["content"], "hello from fake")
        self.assertEqual(data["usage"]["total_tokens"], 70)
        call = self.fake.calls[0]
        self.assertEqual(call["path"], "/v1/responses")
        self.assertEqual(call["headers"]["Authorization"], "Bearer " + CANARY_KEY)
        self.assertNotIn("tools", call["payload"])
        self.assertEqual(call["payload"]["service_tier"], "default")
        self.assertEqual(data["choices"][0]["finish_reason"], "stop")
        self.assertNotIn(REASONING_CANARY.encode(), body)
        report = self.report()
        self.assertEqual(report["usage"], {"prompt_tokens": 40, "completion_tokens": 30,
                                           "cached_tokens": 8, "reasoning_tokens": 12})
        self.assertEqual(report["settled_by_status"], {"ok": 1})
        self.assertEqual(report["charged_microusd_total"], 40 * 5 + 30 * 10)
        self.assertGreater(report["reserved_microusd_total"], report["charged_microusd_total"])

    def test_stream_request_gets_valid_sse_from_one_nonstream_call(self):
        status, ctype, body = self.call(chat_request(stream=True, stream_options={"include_usage": True}))
        self.assertEqual((status, ctype), (200, "text/event-stream"))
        self.assertNotIn("stream", self.fake.calls[0]["payload"])
        self.assertNotIn(REASONING_CANARY.encode(), body)
        events = [e for e in body.decode().split("\n\n") if e]
        self.assertTrue(all(e.startswith("data: ") for e in events))
        self.assertEqual(events[-1], "data: [DONE]")
        chunks = [json.loads(e[6:]) for e in events[:-1]]
        self.assertTrue(all(c["object"] == "chat.completion.chunk" for c in chunks))
        self.assertEqual(len({c["id"] for c in chunks}), 1)
        text = "".join(c["choices"][0]["delta"].get("content", "") for c in chunks if c["choices"])
        self.assertEqual(text, "hello from fake")
        self.assertEqual(chunks[0]["choices"][0]["delta"]["role"], "assistant")
        self.assertEqual([c["choices"][0]["finish_reason"] for c in chunks if c["choices"]][-1], "stop")
        self.assertEqual(chunks[-1]["choices"], [])
        self.assertEqual(chunks[-1]["usage"]["completion_tokens"], 30)

    def test_local_boundary_rejections_never_reach_upstream(self):
        cases = [
            ({}, {"Authorization": ""}, None, 401),
            ({}, {"Authorization": "Bearer wrong"}, None, 401),
            ({}, {"Origin": "http://evil.example"}, None, 403),
            ({}, {}, "evil.example:80", 403),
            ({"method": "GET"}, {}, None, 404),
            ({"path": "/v1/responses"}, {}, None, 404),
            ({"method": "PUT"}, {}, None, 404),
            ({"method": "OPTIONS"}, {"Access-Control-Request-Method": "POST"}, None, 403),
            ({"body": None}, {"Content-Length": str(g.MAX_BODY_BYTES + 1)}, None, 413),
            ({"body": b"{bad"}, {}, None, 400),
            ({}, {"Content-Type": "text/plain"}, None, 415),
        ]
        for options, headers, host, expected in cases:
            body = options.get("body", chat_request())
            status, _, payload = self.call(body, method=options.get("method", "POST"),
                                           path=options.get("path", "/v1/chat/completions"),
                                           headers=headers, raw_host=host)
            self.assertEqual(status, expected, (options.get("method"), headers, host))
            self.assertIn(b"error", payload)
        status, _, _ = self.call(b"5\r\nhello\r\n0\r\n\r\n", headers={"Transfer-Encoding": "chunked"})
        self.assertEqual(status, 411)
        status, _, _ = self.call(None, method="BREW")
        self.assertEqual(status, 400)
        status, _, body = self.call(None, method="GET", path="/v1/models")
        self.assertEqual((status, json.loads(body)["data"][0]["id"]), (200, MODEL))
        self.assertEqual(self.fake.calls, [])
        self.assertEqual(self.report()["requests"], 0)

    def test_upstream_errors_are_redacted_and_charged_in_full(self):
        for mode, code in (("key_echo", "upstream_http_error"), ("server_error", "upstream_http_error"),
                           ("redirect", "upstream_http_error")):
            self.fake.mode = mode
            status, _, body = self.call(chat_request())
            self.assertEqual(status, 424)
            error = json.loads(body)["error"]
            self.assertEqual(error["code"], code)
            self.assertNotIn("Incorrect API key", body.decode())
        self.assertEqual(json.loads(body)["error"]["upstream_status"], 302)
        report = self.report()
        self.assertEqual(report["settled_by_status"], {"http_error": 3})
        self.assertEqual(report["settled_charged_microusd"], report["reserved_microusd_total"])
        self.assertTrue(report["halted"])  # three consecutive failures
        ledger_text = Path(self.ledger.path).read_text()
        self.assertNotIn(CANARY_KEY, ledger_text)
        self.assertIn('"error_code":"invalid_api_key"', ledger_text)
        calls = len(self.fake.calls)
        status, _, body = self.call(chat_request())
        self.assertEqual((status, json.loads(body)["error"]["code"]), (402, "relay_halted"))
        self.assertEqual(len(self.fake.calls), calls)

    def test_incomplete_and_failed_responses(self):
        self.fake.mode = "incomplete"
        status, _, body = self.call(chat_request())
        self.assertEqual(status, 200)
        choice = json.loads(body)["choices"][0]
        self.assertEqual((choice["message"]["content"], choice["finish_reason"]), ("", "length"))
        self.assertNotIn(REASONING_CANARY.encode(), body)
        self.fake.mode = "failed"
        status, _, body = self.call(chat_request())
        self.assertEqual((status, json.loads(body)["error"]["code"]), (424, "upstream_invalid_response"))
        report = self.report()
        self.assertEqual(report["settled_by_status"], {"ok": 1, "invalid_response": 1})

    def test_unsupported_parameter_error_reports_param_only(self):
        self.fake.mode = "unsupported"
        status, _, body = self.call(chat_request())
        error = json.loads(body)["error"]
        self.assertEqual(status, 424)
        self.assertEqual((error["upstream_code"], error["upstream_param"], error["upstream_status"]),
                         ("unsupported_parameter", "max_output_tokens", 400))
        self.assertNotIn("Unsupported parameter", body.decode())

    def test_missing_usage_charges_reservation_but_returns_text(self):
        self.fake.mode = "no_usage"
        status, _, _ = self.call(chat_request())
        self.assertEqual(status, 200)
        report = self.report()
        self.assertEqual(report["settled_by_status"], {"usage_missing": 1})
        self.assertEqual(report["charged_microusd_total"], report["reserved_microusd_total"])

    def test_absent_service_tier_charges_reservation(self):
        self.fake.mode = "no_tier"
        status, _, _ = self.call(chat_request())
        self.assertEqual(status, 200)
        report = self.report()
        self.assertEqual(report["settled_by_status"], {"tier_unverified": 1})
        self.assertEqual(report["charged_microusd_total"], report["reserved_microusd_total"])
        self.assertFalse(report["halted"])

    def test_tier_or_usage_anomaly_halts_persistently(self):
        for mode, reason in (("priority", "tier_mismatch"), ("huge_usage", "usage_out_of_bounds"),
                             ("other_model", "model_mismatch")):
            self.ledger.clear_halt("test")
            self.fake.mode = mode
            status, _, _ = self.call(chat_request())
            self.assertEqual(status, 424)
            self.assertIn(reason, self.report()["halt_reasons"])
            status, _, _ = self.call(chat_request())
            self.assertEqual(status, 402)
        report = self.report()
        self.assertGreaterEqual(report["settled_charged_microusd"], 99999 * 10)

    def test_budget_exhaustion_refuses_without_upstream_call(self):
        small = g.Ledger(self.ledger.path, cap_microusd=30_000)  # below one reservation
        small.close()
        status, _, body = self.call(chat_request())
        self.assertEqual((status, json.loads(body)["error"]["code"]), (402, "budget_exhausted"))
        self.assertEqual(self.fake.calls, [])

    def test_total_deadline_charges_reservation(self):
        self.fake.mode = "hang"
        original = g.UPSTREAM_DEADLINE_S
        g.UPSTREAM_DEADLINE_S = 0.5
        try:
            status, _, body = self.call(chat_request())
        finally:
            g.UPSTREAM_DEADLINE_S = original
            self.fake.release.set()
        self.assertEqual((status, json.loads(body)["error"]["code"]), (424, "upstream_timeout"))
        report = self.report()
        self.assertEqual(report["settled_by_status"], {"timeout": 1})
        self.assertEqual(report["charged_microusd_total"], report["reserved_microusd_total"])

    @classmethod
    def tearDownClass(cls):
        retryable = [s for s in cls.statuses if s in g.RETRYABLE_STATUSES]
        assert not retryable, f"retryable statuses returned to clients: {retryable}"


# ---------------------------------------------------------------------------
HARNESS = (
    "import sys, http.client\n"
    "sys.path.insert(0, sys.argv[1]); import model_budget_relay as g\n"
    "def nonet(t): raise RuntimeError('network disabled in test')\n"
    "g.OpenAIUpstream._https = staticmethod(nonet)\n"
    "port = int(sys.argv[2])\n"
    "sys.exit(g.main(sys.argv[3:], _connection_factory=lambda t: http.client.HTTPConnection("
    "'127.0.0.1', port, timeout=t)))\n")


class ProcessTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="relay-proc-")
        self.ledger = os.path.join(self.dir.name, "ledger.jsonl")
        self.fake = FakeOpenAI()
        self.processes = []

    def tearDown(self):
        for process in self.processes:
            if process.poll() is None:
                process.kill()
            process.wait()
            for stream in (process.stdout, process.stderr):
                if stream and not stream.closed:
                    stream.close()
        self.fake.close()
        self.dir.cleanup()

    def start(self, ready, *extra, key=CANARY_KEY):
        env = child_env(OPENAI_API_KEY=key) if key else child_env()
        process = subprocess.Popen(
            [sys.executable, "-c", HARNESS, str(SCRIPTS), str(self.fake.port), "--ready-file", ready,
             "--ledger", self.ledger, "--model", MODEL, *extra],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.processes.append(process)
        return process

    def wait_ready(self, process, ready):
        deadline = time.monotonic() + 20
        while not os.path.exists(ready):
            if process.poll() is not None:
                self.fail("gateway exited: " + process.stderr.read().decode())
            if time.monotonic() > deadline:
                self.fail("gateway not ready")
            time.sleep(0.05)
        return json.loads(Path(ready).read_text())

    def cli(self, *args, key=None):
        env = child_env(OPENAI_API_KEY=key) if key else child_env()
        return subprocess.run([sys.executable, str(SCRIPTS / "model_budget_relay.py"), *args],
                              env=env, capture_output=True, timeout=30)

    def post(self, info, body):
        port = int(info["base_url"].rsplit(":", 1)[1].split("/")[0])
        return http_call(port, "POST", "/v1/chat/completions", body,
                         {"Authorization": "Bearer " + info["token"]})

    def test_cli_ready_file_shutdown_and_key_never_persisted(self):
        ready = os.path.join(self.dir.name, "ready.json")
        process = self.start(ready, "--budget-usd", "1.5")
        info = self.wait_ready(process, ready)
        self.assertEqual(os.stat(ready).st_mode & 0o777, 0o600)
        self.assertEqual(info["model"], MODEL)
        self.assertRegex(info["base_url"], r"^http://127\.0\.0\.1:\d+/v1$")
        self.assertGreaterEqual(len(info["token"]), 32)
        ready_text = Path(ready).read_text()
        status, _, body = self.post(info, chat_request(stream=True))
        self.assertEqual(status, 200)
        process.send_signal(signal.SIGTERM)
        out, err = process.communicate(timeout=20)
        self.assertEqual(process.returncode, 0, err)
        self.assertFalse(os.path.exists(ready))
        for blob in (out, err, body, ready_text.encode(), Path(self.ledger).read_bytes()):
            self.assertNotIn(CANARY_KEY.encode(), blob)
        report = json.loads(self.cli("--ledger", self.ledger, "--report").stdout)
        self.assertEqual(report["cap_microusd"], 1_500_000)
        self.assertEqual(report["settled_by_status"], {"ok": 1})

    def test_cli_refuses_unsafe_starts(self):
        ready = os.path.join(self.dir.name, "ready.json")
        Path(ready).write_text("{}")
        self.assertEqual(self.cli("--ready-file", ready, "--ledger", self.ledger, "--model", MODEL,
                                  key=CANARY_KEY).returncode, 2)
        fresh = os.path.join(self.dir.name, "fresh.json")
        for args in (["--budget-usd", "10"], ["--budget-usd", "9.991"], ["--model", "gpt-4o"]):
            argv = ["--ready-file", fresh, "--ledger", self.ledger, "--model", MODEL, *args]
            self.assertNotEqual(self.cli(*argv, key=CANARY_KEY).returncode, 0, args)
        missing = self.cli("--ready-file", fresh, "--ledger", self.ledger, "--model", MODEL)
        self.assertNotEqual(missing.returncode, 0)
        self.assertIn(b"OPENAI_API_KEY", missing.stdout + missing.stderr)
        self.assertFalse(os.path.exists(fresh))
        self.assertFalse(os.path.exists(self.ledger))
        self.assertNotEqual(self.cli("--ledger", self.ledger, "--report").returncode, 0)

    def test_crash_mid_request_is_charged_in_full_after_restart(self):
        self.fake.mode = "hang"
        ready = os.path.join(self.dir.name, "ready1.json")
        process = self.start(ready, "--budget-usd", "0.08")
        info = self.wait_ready(process, ready)
        threading.Thread(target=lambda: self._swallow(self.post, info, chat_request()), daemon=True).start()
        self.assertTrue(self.fake.received.wait(20))
        process.kill()  # SIGKILL between reserve and settle
        process.wait()
        report = json.loads(self.cli("--ledger", self.ledger, "--report").stdout)
        self.assertEqual(report["pending_count"], 1)
        reserved = report["reserved_microusd_total"]
        self.assertEqual(report["charged_microusd_total"], reserved)
        self.fake.mode = "ok"
        self.fake.release.set()
        os.unlink(ready)  # stale file from the killed process
        ready2 = os.path.join(self.dir.name, "ready2.json")
        process = self.start(ready2, "--budget-usd", "9.99")  # cannot raise the 0.08 cap
        info = self.wait_ready(process, ready2)
        calls_before = len(self.fake.calls)
        statuses = []
        for _ in range(60):
            status, _, _ = self.post(info, chat_request())
            statuses.append(status)
            if status == 402:
                break
        self.assertIn(402, statuses)
        process.send_signal(signal.SIGTERM)
        process.communicate(timeout=20)
        report = json.loads(self.cli("--ledger", self.ledger, "--report").stdout)
        self.assertEqual(report["cap_microusd"], 80_000)
        self.assertLessEqual(report["charged_microusd_total"], 80_000)
        self.assertEqual(report["pending_count"], 1)
        self.assertEqual(len(self.fake.calls) - calls_before, statuses.count(200))

    @staticmethod
    def _swallow(function, *args):
        try:
            function(*args)
        except (OSError, http.client.HTTPException):
            pass


# ---------------------------------------------------------------------------
# Answer blocking, durable refusals, loopback-only test upstream
# ---------------------------------------------------------------------------
def forbidden_doc(entries):
    return {"schema": g.FORBIDDEN_SCHEMA, "version": g.FORBIDDEN_VERSION, "normalization": g.FORBIDDEN_NORMALIZATION,
            "ngram": 5, "corpus_digest": "0" * 64, "allowed_texts": [], "dropped": {}, "entries": entries}


PLANTED = forbidden_doc([
    {"id": "X-01/prose/0.3", "cases": ["X-01"], "source": "label_prose", "mode": "words", "text": "each call locks its own"},
    {"id": "X-01/token/0", "cases": ["X-01"], "source": "distinctive_token", "mode": "subword", "text": "copylocks"},
    {"id": "X-02/token/1", "cases": ["X-02"], "source": "distinctive_token", "mode": "substring", "text": "std::terminate"},
])


class AnswerBlockingTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="relay-block-")
        self.fake = FakeOpenAI()
        self.ledger = g.Ledger(os.path.join(self.dir.name, "ledger.jsonl"), cap_microusd=2_000_000, upstream_kind="openai")
        self.phrases = g.ForbiddenPhrases(json.dumps(PLANTED).encode())
        self.ledger.note_forbidden(self.phrases)
        self.events = []
        self.gateway = g.Gateway(model=MODEL, ledger=self.ledger, token="tok-123",
                                 upstream=g.OpenAIUpstream(CANARY_KEY, _connection_factory=self.fake.factory),
                                 log=self.events.append, forbidden=self.phrases)
        self.server = g.start_server(self.gateway)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.fake.close()
        self.ledger.close()
        self.dir.cleanup()

    def call(self, body):
        return http_call(self.gateway.port, "POST", "/v1/chat/completions", body, {"Authorization": "Bearer tok-123"})

    def records(self, kind):
        rows = [json.loads(line) for line in Path(self.ledger.path).read_text().splitlines()]
        return [row for row in rows if row["type"] == kind]

    def test_planted_leak_is_blocked_before_reservation_and_never_forwarded(self):
        # The phrase is split by a newline inside a JSON string and the token hides in camelCase.
        leaks = ["Role: investigate\nThe handler... each call locks\nits own copy.",
                 "Role: verify\nfunc runCopylocksPass() {}", "Role: verify\nfunc copyLocks() {}",
                 "Role: interpret\nnoexcept calls STD::TERMINATE here"]
        for text in leaks:
            request = chat_request()
            request["messages"][-1]["content"] = text
            status, _, body = self.call(request)
            self.assertEqual((status, json.loads(body)["error"]["code"]), (403, "blocked_answer_leak"), text)
        self.assertEqual(self.fake.calls, [])
        report = self.ledger.report()
        self.assertEqual((report["requests"], report["charged_microusd_total"]), (0, 0))
        self.assertEqual(report["blocked_answer_leaks"], 4)
        self.assertEqual(report["blocked_by_role"], {"investigate": 1, "verify": 2, "interpret": 1})
        self.assertEqual(report["forbidden_lists"][0]["sha256"], self.phrases.sha256)
        blocked = self.records("blocked")
        self.assertEqual(blocked[0]["matches"], ["X-01/prose/0.3"])
        self.assertEqual({b["status"] for b in blocked}, {"blocked_answer_leak"})
        self.assertEqual(self.records("reserve"), [])
        self.assertNotIn("locks its own", Path(self.ledger.path).read_text())  # ids only, never label text

    def test_clean_text_and_dropped_keys_pass(self):
        request = chat_request(tools=[{"type": "function", "function": {"name": "copylocks"}}])
        request["messages"][-1]["content"] = "Role: investigate\neach call locks the shared mutex; copy locks? std terminate"
        status, _, _ = self.call(request)
        self.assertEqual(status, 200)  # near misses, and a phrase only in a dropped (never forwarded) key
        self.assertEqual(len(self.fake.calls), 1)
        self.assertEqual(self.ledger.report()["blocked_answer_leaks"], 0)

    def test_strict_mode_blocks_the_models_own_words(self):
        self.fake.text = "candidate: runCopylocksPass copies the mutex"
        clean = chat_request()
        clean["messages"][-1]["content"] = "Role: investigate\nlook at the counters"
        self.assertEqual(self.call(clean)[0], 200)
        echo = chat_request()
        echo["messages"][-1]["content"] = "Role: verify\ncandidate: runCopylocksPass copies the mutex"
        self.assertEqual(self.call(echo)[0], 403)  # default: every match blocks
        self.assertFalse(self.ledger.report()["forbidden_lists"][0]["allow_model_echo"])

    def test_refusals_are_durable_and_uncharged(self):
        small = g.Ledger(self.ledger.path, cap_microusd=30_000)
        small.close()
        status, _, body = self.call(chat_request())
        self.assertEqual((status, json.loads(body)["error"]["code"]), (402, "budget_exhausted"))
        report = self.ledger.report()
        self.assertEqual(report["refused_by_code"], {"budget_exhausted": 1})
        self.assertEqual((report["requests"], report["charged_microusd_total"]), (0, 0))

    def test_malformed_lists_fail_closed(self):
        bad = [b"", b"not json", json.dumps({"schema": "other"}).encode(), json.dumps(forbidden_doc([])).encode(),
               json.dumps(forbidden_doc([{"id": "a", "mode": "subword", "text": "two words"}])).encode(),
               json.dumps(forbidden_doc([{"id": "a", "mode": "regex", "text": "x"}])).encode(),
               json.dumps(dict(PLANTED, normalization="other")).encode()]
        path = os.path.join(self.dir.name, "bad.json")
        for data in bad:
            Path(path).write_bytes(data)
            with self.assertRaises(ValueError, msg=data[:40]):
                g.load_forbidden(path)
        with self.assertRaises(ValueError):
            g.load_forbidden(os.path.join(self.dir.name, "missing.json"))


class ModelEchoTests(unittest.TestCase):
    """--allow-model-echo: ids the relay itself returned in an upstream answer may come back."""

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="relay-echo-")
        self.fake = FakeOpenAI()
        self.ledger = g.Ledger(os.path.join(self.dir.name, "ledger.jsonl"), cap_microusd=2_000_000, upstream_kind="openai")
        self.phrases = g.ForbiddenPhrases(json.dumps(PLANTED).encode())
        self.ledger.note_forbidden(self.phrases, allow_model_echo=True)
        self.gateway = g.Gateway(model=MODEL, ledger=self.ledger, token="tok-123",
                                 upstream=g.OpenAIUpstream(CANARY_KEY, _connection_factory=self.fake.factory),
                                 log=lambda event: None, forbidden=self.phrases, allow_model_echo=True)
        self.server = g.start_server(self.gateway)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.fake.close()
        self.ledger.close()
        self.dir.cleanup()

    def call(self, text, assistant=None):
        request = chat_request()
        if assistant is not None:
            request["messages"].insert(-1, {"role": "assistant", "content": assistant})
        request["messages"][-1]["content"] = text
        return http_call(self.gateway.port, "POST", "/v1/chat/completions", request, {"Authorization": "Bearer tok-123"})

    def records(self, kind):
        rows = [json.loads(line) for line in Path(self.ledger.path).read_text().splitlines()]
        return [row for row in rows if row["type"] == kind]

    def test_only_the_relays_own_answers_exempt_ids(self):
        self.assertEqual(self.call("Role: verify\ncandidate copylocks")[0], 403)  # not emitted yet
        # A client-supplied assistant message is not the relay's own answer.
        self.assertEqual(self.call("Role: verify\ncandidate copylocks", assistant="I found copylocks")[0], 403)
        self.fake.text = '{"result": {"candidates": [{"title": "runCopylocksPass copies the mutex"}]}}'
        self.assertEqual(self.call("Role: investigate\nlook at the counters")[0], 200)
        self.fake.text = None
        self.assertEqual(self.call("Role: verify\ncandidate: runCopylocksPass copies the mutex")[0], 200)
        echoed = [r for r in self.records("reserve") if r.get("echo_matches")]
        self.assertEqual([(r["role_hint"], r["echo_matches"]) for r in echoed], [("verify", ["X-01/token/0"])])
        # Mixed: an exempt id does not excuse another id in the same request.
        status, _, body = self.call("Role: verify\ncopylocks; noexcept calls std::terminate")
        self.assertEqual((status, json.loads(body)["error"]["code"]), (403, "blocked_answer_leak"))
        blocked = self.records("blocked")[-1]
        self.assertEqual((blocked["matches"], blocked["echo_matches"]), (["X-02/token/1"], ["X-01/token/0"]))
        report = self.ledger.report()
        self.assertEqual((report["echo_exempted_requests"], report["echo_exempted_by_role"]), (1, {"verify": 1}))
        self.assertEqual(report["blocked_answer_leaks"], 3)
        self.assertTrue(report["forbidden_lists"][0]["allow_model_echo"])
        self.assertEqual(len(self.fake.calls), 2)
        self.assertNotIn("copies the mutex", Path(self.ledger.path).read_text())

    def test_flag_needs_a_list(self):
        with self.assertRaises(ValueError):
            g.Gateway(model=MODEL, ledger=self.ledger, token="t", upstream=None, allow_model_echo=True)
        ready = os.path.join(self.dir.name, "ready.json")
        result = subprocess.run([sys.executable, str(SCRIPTS / "model_budget_relay.py"), "--ready-file", ready,
                                 "--ledger", os.path.join(self.dir.name, "other.jsonl"), "--model", MODEL,
                                 "--allow-model-echo", "--test-upstream", f"http://127.0.0.1:{self.fake.port}"],
                                capture_output=True, env=child_env(), timeout=30)
        self.assertEqual(result.returncode, 2)
        self.assertIn(b"needs --forbidden-phrases", result.stderr)
        self.assertFalse(os.path.exists(ready))


class CorpusListTests(unittest.TestCase):
    """The relay matcher agrees with corpus.py: no hit on any materialized input, a hit on planted label text."""

    def test_corpus_list_is_clean_on_inputs_and_catches_plants(self):
        import runpy
        # fixtures/quality-holdout, or the one non-regression fixture corpus
        # of a checkout that keeps the holdout under another directory name.
        corpus_py = SCRIPTS.parent / "fixtures/quality-holdout/corpus.py"
        if not corpus_py.is_file():
            corpus_py, = [p for p in sorted((SCRIPTS.parent / "fixtures").glob("*/corpus.py")) if p.parent.name != "regression"]
        corpus = runpy.run_path(str(corpus_py))
        with tempfile.TemporaryDirectory(prefix="relay-forbidden-") as tmp:
            out = os.path.join(tmp, "forbidden.json")
            result = subprocess.run([sys.executable, "-I", str(corpus_py), "forbidden", "--output", out],
                                    capture_output=True, timeout=120, env=child_env())
            self.assertEqual(result.returncode, 0, result.stderr)
            phrases = g.load_forbidden(out)
        cases = corpus["load_cases"]()
        self.assertEqual(phrases.corpus_digest, corpus["corpus_digest"](cases))
        listed = set(phrases.entries)
        planted_hits = 0
        for case in cases:
            for variant in corpus["variants"](case):
                for documented in (False, True):
                    files = corpus["render"](case, variant, documented)
                    self.assertEqual(phrases.matches([p + "\n" + c for p, c in files.items()]), [], (case["id"], variant))
            tokens = [(i, t) for i, t in enumerate(case["label"]["distinctive_tokens"]) if f"{case['id']}/token/{i}" in listed]
            files = corpus["render"](case, "before")
            path = case["contract_file"]
            if tokens:
                planted = files[path] + f"\n// {tokens[0][1]}\n"
                self.assertIn(f"{case['id']}/token/{tokens[0][0]}", phrases.matches([planted]), case["id"])
                planted_hits += 1
            phrase = " ".join(corpus["words"](case["label"]["expected_impact"])[:5])
            spread = files[path] + "\n# " + phrase.upper().replace(" ", "\n# ") + "\n"  # one word per comment line
            self.assertTrue(phrases.matches([spread]), case["id"])
        self.assertGreater(planted_hits, 40)


class TestUpstreamTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory(prefix="relay-testup-")
        self.ledger = os.path.join(self.dir.name, "ledger.jsonl")
        self.fake = FakeOpenAI()
        self.processes = []

    def tearDown(self):
        for process in self.processes:
            if process.poll() is None:
                process.kill()
            process.communicate()
        self.fake.close()
        self.dir.cleanup()

    def test_only_explicit_loopback_urls(self):
        self.assertEqual(g.parse_test_upstream("http://127.0.0.1:8080"), 8080)
        self.assertEqual(g.parse_test_upstream("http://127.0.0.1:8080/"), 8080)
        for bad in ("", "http://localhost:8080", "http://10.0.0.1:80", "https://127.0.0.1:443", "http://127.0.0.1",
                    "http://127.0.0.1:0", "http://127.0.0.1:99999", "http://127.0.0.2:80", "http://[::1]:80",
                    "http://127.0.0.1:80/v1", "http://user@127.0.0.1:80", "http://127.0.0.1:80 ", "api.openai.com"):
            with self.assertRaises(ValueError, msg=bad):
                g.parse_test_upstream(bad)

    def start(self, ready, *extra, key=CANARY_KEY):
        env = child_env(OPENAI_API_KEY=key) if key else child_env()
        process = subprocess.Popen(
            [sys.executable, "-c", HARNESS, str(SCRIPTS), "1", "--ready-file", ready, "--ledger", self.ledger,
             "--model", MODEL, *extra], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.processes.append(process)
        deadline = time.monotonic() + 20
        while not os.path.exists(ready):
            if process.poll() is not None:
                return process
            self.assertLess(time.monotonic(), deadline, "relay not ready")
            time.sleep(0.05)
        return process

    def test_cli_never_reads_the_key_and_binds_its_ledger(self):
        ready = os.path.join(self.dir.name, "ready.json")
        url = f"http://127.0.0.1:{self.fake.port}"
        process = self.start(ready, "--test-upstream", url, "--budget-usd", "0.5")
        info = json.loads(Path(ready).read_text())
        port = int(info["base_url"].rsplit(":", 1)[1].split("/")[0])
        status, _, body = http_call(port, "POST", "/v1/chat/completions", chat_request(),
                                    {"Authorization": "Bearer " + info["token"]})
        self.assertEqual(status, 200, body)
        process.send_signal(signal.SIGTERM)
        out, err = process.communicate(timeout=20)
        self.assertEqual(process.returncode, 0, err)
        self.assertEqual(len(self.fake.calls), 1)
        self.assertEqual(self.fake.calls[0]["headers"]["Authorization"], "Bearer " + g.TEST_UPSTREAM_KEY)
        self.assertEqual(self.fake.calls[0]["path"], g.UPSTREAM_PATH)
        for blob in (out, err, json.dumps(self.fake.calls).encode(), Path(self.ledger).read_bytes()):
            self.assertNotIn(CANARY_KEY.encode(), blob)
        report = json.loads(subprocess.run([sys.executable, str(SCRIPTS / "model_budget_relay.py"), "--ledger", self.ledger,
                                            "--report"], capture_output=True, env=child_env(), timeout=30).stdout)
        self.assertEqual((report["upstream"], report["settled_by_status"]), ("test-loopback", {"ok": 1}))
        # A real relay never adopts a test ledger.
        real = self.start(os.path.join(self.dir.name, "ready2.json"))
        real.wait(timeout=20)
        self.assertEqual(real.returncode, 1)
        self.assertIn(b"test-loopback upstream", real.stderr.read())

    def test_test_relay_refuses_a_real_ledger_bad_urls_and_bad_lists(self):
        g.Ledger(self.ledger, cap_microusd=100_000, upstream_kind="openai").close()
        url = f"http://127.0.0.1:{self.fake.port}"
        process = self.start(os.path.join(self.dir.name, "r1.json"), "--test-upstream", url, key=None)
        process.wait(timeout=20)
        self.assertEqual(process.returncode, 1)
        self.assertIn(b"openai upstream", process.stderr.read())
        for bad in ("http://localhost:1", "https://127.0.0.1:1", "http://10.1.2.3:80"):
            process = self.start(os.path.join(self.dir.name, "r2.json"), "--test-upstream", bad, key=None)
            process.wait(timeout=20)
            self.assertEqual(process.returncode, 2, bad)
        for data in (b"", b"{}"):
            Path(self.dir.name, "f.json").write_bytes(data)
            process = self.start(os.path.join(self.dir.name, "r3.json"), "--test-upstream", url,
                                 "--forbidden-phrases", os.path.join(self.dir.name, "f.json"), key=None)
            process.wait(timeout=20)
            self.assertEqual(process.returncode, 2)
        self.assertEqual(self.fake.calls, [])

    def test_capture_list_only_with_the_test_upstream(self):
        doc = {"schema": g.FORBIDDEN_SCHEMA, "version": g.FORBIDDEN_VERSION, "normalization": g.FORBIDDEN_NORMALIZATION,
               "purpose": g.FORBIDDEN_CAPTURE, "entries": [{"id": "capture/sentinel", "mode": "words",
                                                           "text": "zq capture sentinel zq"}]}
        capture = os.path.join(self.dir.name, "capture.json")
        Path(capture).write_text(json.dumps(doc))
        real = self.start(os.path.join(self.dir.name, "c1.json"), "--forbidden-phrases", capture)
        real.wait(timeout=20)
        self.assertEqual(real.returncode, 2)
        self.assertIn(b"envelope-capture", real.stderr.read())
        doc["purpose"] = "something_else"
        Path(capture).write_text(json.dumps(doc))
        url = f"http://127.0.0.1:{self.fake.port}"
        other = self.start(os.path.join(self.dir.name, "c2.json"), "--test-upstream", url, "--forbidden-phrases", capture, key=None)
        other.wait(timeout=20)
        self.assertEqual(other.returncode, 2)
        doc["purpose"] = g.FORBIDDEN_CAPTURE
        Path(capture).write_text(json.dumps(doc))
        ready = os.path.join(self.dir.name, "c3.json")
        test = self.start(ready, "--test-upstream", url, "--forbidden-phrases", capture, key=None)
        self.assertTrue(os.path.exists(ready))
        test.send_signal(signal.SIGTERM)
        test.communicate(timeout=20)
        self.assertEqual(test.returncode, 0)
        self.assertFalse(os.path.exists(os.path.join(self.dir.name, "c1.json")))
        self.assertEqual(self.fake.calls, [])


if __name__ == "__main__":
    unittest.main()
