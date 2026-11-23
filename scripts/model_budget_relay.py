#!/usr/bin/env python3
"""QA-only loopback relay: Chat Completions in (OpenCode), OpenAI Responses API out.

This is NOT a CODEYE product component and not the product model path (D10).
It exists only for a human-approved, budget-capped OpenCode integration probe.

Guarantees (see docs/EVALUATION.md):
- Listens on 127.0.0.1 only, per-run random bearer token, no CORS, Origin rejected.
- Rebuilds every upstream payload from text-only messages with an output cap
  (--max-output-tokens, never above the hard maximum).
- Upstream is pinned to https://api.openai.com/v1/responses via http.client
  (no proxy, no redirect, no retry). OPENAI_API_KEY is read once from the
  environment and never written or logged.
- Every upstream request is reserved in a JSONL ledger (flock + fsync) before it
  is sent. Failures, missing usage and crashes are charged the full reservation.
- Errors returned to clients use non-retryable HTTP statuses only, so SDK retry
  loops cannot silently multiply paid calls.
- with --forbidden-phrases FILE (the evaluation corpus answer list), a
  request whose forwarded text contains a listed phrase is never reserved or
  forwarded; a durable `blocked` ledger record (no charge) is written instead.
  Budget/request-limit refusals are also recorded (`refused`, no charge).
- dry runs only: --test-upstream http://127.0.0.1:PORT replaces the pinned
  endpoint with a loopback fake. It never reads OPENAI_API_KEY, accepts no other
  host, and binds the ledger to the test upstream (a real relay refuses that
  ledger and a test relay refuses a real one). Default behaviour is unchanged.

Stdlib only; Linux/macOS (fcntl).
"""
from __future__ import annotations

import argparse
import decimal
import fcntl
import hashlib
import hmac
import http.client
import http.server
import json
import os
import re
import secrets
import signal
import socket
import ssl
import stat
import sys
import threading
import time
import uuid

# ---------------------------------------------------------------------------
# Fixed policy. None of these are configurable from the command line upward.
# ---------------------------------------------------------------------------
UPSTREAM_HOST = "api.openai.com"
UPSTREAM_PORT = 443
UPSTREAM_PATH = "/v1/responses"  # OpenCode-facing side stays /v1/chat/completions
ALLOWED_MODELS = ("gpt-6.2-sol", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-luna")
SERVICE_TIER = "default"
REASONING_EFFORT = "low"

HARD_CAP_MICROUSD = 25_000_000  # $25 for all processes sharing one ledger
# Integer microUSD per token. $X per 1M tokens == X microUSD per token.
# Input is priced at the worst case of standard input ($2), 30m cache write
# ($2.50) and 12h cache write ($5). Output ($10) includes reasoning tokens.
PRICE_INPUT_WORST = 5
PRICE_OUTPUT = 10
# Tenths of a microUSD per token, uncached Standard prices. Informational
# only; admission and settlement keep the shared worst-case rates above.
STANDARD_RATES = {"gpt-6.2-sol": (20, 100), "gpt-6.1-sol": (20, 100),
                  "gpt-6.1-terra": (10, 50), "gpt-6-luna": (1, 5)}
RESERVATION_MULTIPLIER = 2
RESERVATION_FIXED_MICROUSD = 1000
PER_MESSAGE_TOKENS = 16
FIXED_PROMPT_TOKENS = 256

# Output tokens per request: the worker's default review output budget and
# its largest (internal/worker/opencode: DefaultOutputTokens, MaxOutputTokens).
DEFAULT_MAX_OUTPUT_TOKENS = 16384
HARD_MAX_OUTPUT_TOKENS = 65536
MAX_MESSAGES = 64
# Message text per request: the worker's request budget (default 256 KiB, at
# most 1 MiB) plus room for OpenCode's own messages.
OPENCODE_MESSAGE_BYTES = 16384
DEFAULT_MAX_INPUT_BYTES = (256 << 10) + OPENCODE_MESSAGE_BYTES
HARD_MAX_INPUT_BYTES = (1 << 20) + OPENCODE_MESSAGE_BYTES
DEFAULT_MAX_REQUESTS = 200
HARD_MAX_REQUESTS = 1000
FAILURE_HALT_THRESHOLD = 3

# A request body carries its messages JSON-escaped; a response carries up to
# HARD_MAX_OUTPUT_TOKENS of text.
MAX_BODY_BYTES = 4 << 20
MAX_HEADER_BYTES = 16384
MAX_RESPONSE_BYTES = 4 << 20
CLIENT_IO_TIMEOUT_S = 30.0
# The worker's default model call timeout (opencode.DefaultTimeout).
UPSTREAM_DEADLINE_S = 600.0
QUEUE_WAIT_S = UPSTREAM_DEADLINE_S + 60.0

LEDGER_VERSION = 1
PRICING = {
    "input_worst_microusd_per_token": PRICE_INPUT_WORST,
    "output_microusd_per_token": PRICE_OUTPUT,
    "reservation_multiplier": RESERVATION_MULTIPLIER,
    "reservation_fixed_microusd": RESERVATION_FIXED_MICROUSD,
    "per_message_tokens": PER_MESSAGE_TOKENS,
    "fixed_prompt_tokens": FIXED_PROMPT_TOKENS,
}
ALLOWED_ROLES = ("system", "developer", "user", "assistant")
INCOMPLETE_FINISH = {"max_output_tokens": "length", "content_filter": "content_filter"}
# Statuses that delivered usable text to the client; anything else is a failure.
NON_FAILURE_STATUSES = ("ok", "usage_missing", "tier_unverified")
HALT_STATUSES = ("usage_out_of_bounds", "tier_mismatch", "model_mismatch")
# Client-visible statuses. AI SDK style clients retry 408/409/429/5xx; never use them.
RETRYABLE_STATUSES = frozenset({408, 409, 429}) | frozenset(range(500, 600))
SAFE_TOKEN = re.compile(r"^[A-Za-z0-9_.-]{1,64}$")

# Answer blocking. The file is produced by fixtures/quality-holdout/corpus.py forbidden.
FORBIDDEN_SCHEMA = "codeye-forbidden-phrases"
FORBIDDEN_VERSION = 1
FORBIDDEN_NORMALIZATION = "codeye-words-v1"
FORBIDDEN_MAX_BYTES = 16 << 20
BLOCKED_STATUS = "blocked_answer_leak"
FORBIDDEN_CAPTURE = "envelope_capture"
MAX_RECORDED_MATCHES = 16
# CODEYE prompts carry "Role: <role>" on its own line (opencode.Prompt); OpenCode puts the
# attached identity file before it. Review input is JSON, so input text cannot start a line here.
ROLE_HINT = re.compile(r"^Role: ([a-z]{1,32})$", re.M)
# Test-only upstream: an explicit loopback URL, nothing else.
TEST_UPSTREAM = re.compile(r"\Ahttp://127\.0\.0\.1:([0-9]{1,5})/?\Z")
TEST_UPSTREAM_KIND = "test-loopback"
TEST_UPSTREAM_KEY = "test-upstream-no-key"


class LedgerError(Exception):
    """The ledger is unusable; the gateway must not send upstream requests."""


class BudgetRefused(Exception):
    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


class RequestRejected(Exception):
    def __init__(self, status: int, code: str):
        super().__init__(code)
        self.status = status
        self.code = code


class UpstreamFailure(Exception):
    def __init__(self, kind: str):
        super().__init__(kind)
        self.kind = kind


def _now() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _is_count(value) -> bool:
    return isinstance(value, int) and not isinstance(value, bool) and value >= 0


def _fsync_dir(path: str) -> None:
    fd = os.open(os.path.dirname(os.path.abspath(path)) or ".", os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def parse_budget(text: str) -> int:
    """Parse --budget-usd into integer microUSD, bounded by the hard cap."""
    try:
        value = decimal.Decimal(text)
    except decimal.InvalidOperation:
        raise ValueError("budget must be a decimal USD amount")
    if not value.is_finite() or value <= 0:
        raise ValueError("budget must be positive")
    micro = value * 1_000_000
    if micro != micro.to_integral_value():
        raise ValueError("budget supports at most 6 decimal places")
    micro = int(micro)
    if micro > HARD_CAP_MICROUSD:
        raise ValueError(f"budget exceeds the hard cap of {HARD_CAP_MICROUSD // 1_000_000} USD")
    return micro


def reservation_for(input_bytes: int, message_count: int, output_cap: int) -> tuple[int, int]:
    """Return (input token upper bound, reserved microUSD).

    BPE tokens cover at least one UTF-8 byte each, so text bytes bound text tokens.
    Chat framing and any provider-injected preamble are covered by fixed allowances,
    and the whole worst-case price is doubled.
    """
    input_bound = input_bytes + PER_MESSAGE_TOKENS * message_count + FIXED_PROMPT_TOKENS
    worst = input_bound * PRICE_INPUT_WORST + output_cap * PRICE_OUTPUT
    return input_bound, RESERVATION_MULTIPLIER * worst + RESERVATION_FIXED_MICROUSD


# ---------------------------------------------------------------------------
# Ledger
# ---------------------------------------------------------------------------
class Ledger:
    """Append-only JSONL ledger shared by every relay process of one run.

    Each mutation runs under an exclusive flock (cross-process) and a thread lock
    (flock is per open file description), re-reads the whole file and appends one
    fsynced line. A reserve record is durable before the upstream request starts.
    """

    def __init__(self, path: str, *, cap_microusd: int | None = None,
                 max_requests: int | None = None, create: bool = True,
                 upstream_kind: str | None = None):
        """upstream_kind: None (report/clear-halt: any ledger), "openai" (the
        pinned endpoint; refuses a test ledger) or TEST_UPSTREAM_KIND (refuses
        a real ledger; a new test ledger is marked in its header)."""
        self.path = path
        self._thread_lock = threading.Lock()
        flags = os.O_RDWR | os.O_APPEND | os.O_CLOEXEC | os.O_NOFOLLOW
        if create:
            flags |= os.O_CREAT
        try:
            self._fd = os.open(path, flags, 0o600)
        except OSError as exc:
            raise LedgerError(f"cannot open ledger: {exc.strerror}") from None
        try:
            info = os.fstat(self._fd)
            if not stat.S_ISREG(info.st_mode):
                raise LedgerError("ledger is not a regular file")
            if info.st_uid != os.getuid():
                raise LedgerError("ledger is owned by another user")
            if info.st_mode & 0o077:
                raise LedgerError("ledger must not be accessible by group/other (chmod 600)")
            with self._locked() as records:
                if not records:
                    if not create or cap_microusd is None:
                        raise LedgerError("ledger is empty")
                    header = {"type": "header", "v": LEDGER_VERSION, "created_at": _now(),
                              "cap_microusd": cap_microusd,
                              "max_requests": max_requests or DEFAULT_MAX_REQUESTS,
                              "pricing": PRICING}
                    if upstream_kind == TEST_UPSTREAM_KIND:
                        header["upstream"] = TEST_UPSTREAM_KIND  # real ledgers keep the original header
                    self._append(header)
                    _fsync_dir(path)
                else:
                    kind = records[0].get("upstream", "openai")
                    if upstream_kind is not None and kind != upstream_kind:
                        raise LedgerError("ledger belongs to the " + str(kind)[:32]
                                          + " upstream; use a separate ledger for each upstream")
                    state = self._state(records)
                    if cap_microusd is not None and cap_microusd < state["cap_microusd"]:
                        self._append({"type": "cap", "at": _now(), "cap_microusd": cap_microusd})
                    if max_requests is not None and max_requests < state["max_requests"]:
                        self._append({"type": "cap", "at": _now(), "max_requests": max_requests})
        except BaseException:
            os.close(self._fd)
            raise

    def close(self) -> None:
        os.close(self._fd)

    # -- low level ---------------------------------------------------------
    class _Lock:
        def __init__(self, ledger: "Ledger"):
            self.ledger = ledger

        def __enter__(self):
            self.ledger._thread_lock.acquire()
            try:
                fcntl.flock(self.ledger._fd, fcntl.LOCK_EX)
                return self.ledger._read()
            except BaseException:
                fcntl.flock(self.ledger._fd, fcntl.LOCK_UN)
                self.ledger._thread_lock.release()
                raise

        def __exit__(self, *exc):
            try:
                fcntl.flock(self.ledger._fd, fcntl.LOCK_UN)
            finally:
                self.ledger._thread_lock.release()

    def _locked(self) -> "Ledger._Lock":
        return Ledger._Lock(self)

    def _read(self) -> list[dict]:
        size = os.fstat(self._fd).st_size
        data = os.pread(self._fd, size, 0) if size else b""
        if len(data) != size:
            raise LedgerError("short ledger read")
        if data and not data.endswith(b"\n"):
            # A torn final line means its write never completed. Reserve records
            # are fsynced before any upstream call, so no request depends on it.
            keep = data.rfind(b"\n") + 1
            os.ftruncate(self._fd, keep)
            os.fsync(self._fd)
            if keep:
                self._append({"type": "repair", "at": _now(), "truncated_bytes": size - keep})
            return self._read()
        records = []
        for number, line in enumerate(data.splitlines(), 1):
            try:
                record = json.loads(line)
            except ValueError:
                raise LedgerError(f"corrupt ledger line {number}; refusing to continue") from None
            if not isinstance(record, dict) or not isinstance(record.get("type"), str):
                raise LedgerError(f"invalid ledger record at line {number}")
            records.append(record)
        if records:
            head = records[0]
            if head.get("type") != "header" or head.get("v") != LEDGER_VERSION:
                raise LedgerError("ledger header missing or unsupported version")
            if head.get("pricing") != PRICING:
                raise LedgerError("ledger pricing differs from this relay version")
        return records

    def _append(self, record: dict) -> None:
        line = (json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n").encode()
        written = os.write(self._fd, line)
        if written != len(line):
            raise LedgerError("short ledger write")
        os.fsync(self._fd)

    # -- state -------------------------------------------------------------
    @staticmethod
    def _state(records: list[dict]) -> dict:
        head = records[0]
        cap = min(head["cap_microusd"], HARD_CAP_MICROUSD)
        max_requests = min(head["max_requests"], HARD_MAX_REQUESTS)
        reserves: dict[str, dict] = {}
        settles: dict[str, dict] = {}
        halted, halt_reasons, failures = False, [], 0
        for record in records[1:]:
            kind = record["type"]
            if kind == "cap":
                if _is_count(record.get("cap_microusd")):
                    cap = min(cap, record["cap_microusd"])
                if _is_count(record.get("max_requests")):
                    max_requests = min(max_requests, record["max_requests"])
            elif kind == "reserve":
                if not _is_count(record.get("reserved_microusd")) or record.get("id") in reserves:
                    raise LedgerError("invalid reserve record")
                reserves[record["id"]] = record
            elif kind == "settle":
                rid = record.get("id")
                if rid not in reserves or rid in settles or not _is_count(record.get("charged_microusd")):
                    raise LedgerError("invalid settle record")
                settles[rid] = record
                failures = 0 if record.get("status") in NON_FAILURE_STATUSES else failures + 1
            elif kind == "halt":
                halted = True
                halt_reasons.append(record.get("reason"))
            elif kind == "clear_halt":
                halted, failures = False, 0
        charged = sum(s["charged_microusd"] for s in settles.values())
        pending = [r for rid, r in reserves.items() if rid not in settles]
        pending_microusd = sum(r["reserved_microusd"] for r in pending)
        return {
            "cap_microusd": cap,
            "max_requests": max_requests,
            "reserves": reserves,
            "settles": settles,
            "pending": pending,
            "committed_microusd": charged + pending_microusd,
            "settled_charged_microusd": charged,
            "pending_reserved_microusd": pending_microusd,
            "halted": halted or failures >= FAILURE_HALT_THRESHOLD,
            "halt_reasons": halt_reasons,
            "consecutive_failures": failures,
        }

    # -- transactions ------------------------------------------------------
    def reserve(self, *, model: str, reserved_microusd: int, input_bytes: int,
                input_token_bound: int, message_count: int, output_cap: int,
                dropped_keys: list[str], role_hint: str | None = None,
                echo_matches: list[str] | None = None) -> str:
        with self._locked() as records:
            state = self._state(records)
            refused = None
            if state["halted"]:
                refused = "relay_halted"
            elif len(state["reserves"]) >= state["max_requests"]:
                refused = "request_limit_reached"
            elif state["committed_microusd"] + reserved_microusd > state["cap_microusd"]:
                refused = "budget_exhausted"
            if refused is not None:
                # Durable signal for batch runners; never charged, never forwarded.
                self._append({"type": "refused", "at": _now(), "pid": os.getpid(), "code": refused,
                              "reserved_microusd": reserved_microusd})
                raise BudgetRefused(refused)
            rid = uuid.uuid4().hex
            self._append({"type": "reserve", "id": rid, "at": _now(), "pid": os.getpid(),
                          "model": model, "reserved_microusd": reserved_microusd,
                          "input_bytes": input_bytes, "input_token_bound": input_token_bound,
                          "message_count": message_count, "output_cap": output_cap,
                          "dropped_keys": dropped_keys, "role_hint": role_hint,
                          **({"echo_matches": echo_matches[:MAX_RECORDED_MATCHES], "echo_match_count": len(echo_matches)}
                             if echo_matches else {})})
            return rid

    def settle(self, rid: str, *, status: str, charged_microusd: int, usage: dict | None,
               detail: dict | None = None, halt_reason: str | None = None) -> None:
        with self._locked() as records:
            state_before = self._state(records)
            self._append({"type": "settle", "id": rid, "at": _now(), "status": status,
                          "charged_microusd": charged_microusd, "usage": usage,
                          "detail": detail or {}})
            state = self._state(self._read())
            if halt_reason is None and state["halted"] and not state_before["halted"]:
                halt_reason = "consecutive_failures"
            if halt_reason is not None:
                self._append({"type": "halt", "at": _now(), "reason": halt_reason, "after": rid})

    def clear_halt(self, reason: str) -> None:
        with self._locked():
            self._append({"type": "clear_halt", "at": _now(), "reason": reason[:200]})

    def note_forbidden(self, phrases: "ForbiddenPhrases", allow_model_echo: bool = False) -> None:
        """Record which answer list this relay process enforces, and whether ids the model
        itself returned earlier in this process are exempt (--allow-model-echo)."""
        with self._locked():
            self._append({"type": "forbidden", "at": _now(), "pid": os.getpid(), "sha256": phrases.sha256,
                          "entries": len(phrases.entries), "corpus_digest": phrases.corpus_digest,
                          "allow_model_echo": bool(allow_model_echo)})

    def block(self, *, matches: list[str], match_count: int, input_bytes: int, message_count: int,
              role_hint: str | None, echo_matches: list[str] | None = None) -> None:
        """A request that would have carried answer text: recorded, never reserved or charged."""
        with self._locked():
            self._append({"type": "blocked", "at": _now(), "pid": os.getpid(), "status": BLOCKED_STATUS,
                          "matches": matches[:MAX_RECORDED_MATCHES], "match_count": match_count,
                          "input_bytes": input_bytes, "message_count": message_count, "role_hint": role_hint,
                          **({"echo_matches": echo_matches[:MAX_RECORDED_MATCHES]} if echo_matches else {})})

    def report(self) -> dict:
        with self._locked() as records:
            state = self._state(records)
        refused_by_code: dict[str, int] = {}
        blocked_by_role: dict[str, int] = {}
        echo_by_role: dict[str, int] = {}
        forbidden_lists = []
        for record in records[1:]:
            if record["type"] == "reserve" and record.get("echo_matches"):
                role = str(record.get("role_hint") or "unknown")
                echo_by_role[role] = echo_by_role.get(role, 0) + 1
            if record["type"] == "refused":
                code = str(record.get("code"))
                refused_by_code[code] = refused_by_code.get(code, 0) + 1
            elif record["type"] == "blocked":
                role = str(record.get("role_hint") or "unknown")
                blocked_by_role[role] = blocked_by_role.get(role, 0) + 1
            elif record["type"] == "forbidden":
                forbidden_lists.append({k: record.get(k) for k in ("at", "pid", "sha256", "entries", "corpus_digest",
                                                                    "allow_model_echo")})
        usage_totals = {"prompt_tokens": 0, "completion_tokens": 0, "cached_tokens": 0,
                        "reasoning_tokens": 0}
        by_status: dict[str, int] = {}
        standard_tenths = 0
        for record in state["settles"].values():
            by_status[record["status"]] = by_status.get(record["status"], 0) + 1
            usage = record.get("usage") or {}
            for key in usage_totals:
                if _is_count(usage.get(key)):
                    usage_totals[key] += usage[key]
            if _is_count(usage.get("prompt_tokens")) and _is_count(usage.get("completion_tokens")):
                model = state["reserves"][record["id"]]["model"]
                input_rate, output_rate = STANDARD_RATES[model]
                standard_tenths += usage["prompt_tokens"] * input_rate + usage["completion_tokens"] * output_rate
        reserved_total = sum(r["reserved_microusd"] for r in state["reserves"].values())
        return {
            "ledger": os.path.abspath(self.path),
            "cap_microusd": state["cap_microusd"],
            "hard_cap_microusd": HARD_CAP_MICROUSD,
            "max_requests": state["max_requests"],
            "requests": len(state["reserves"]),
            "settled": len(state["settles"]),
            "pending_count": len(state["pending"]),
            "settled_by_status": by_status,
            "reserved_microusd_total": reserved_total,
            "settled_charged_microusd": state["settled_charged_microusd"],
            "pending_reserved_microusd": state["pending_reserved_microusd"],
            "charged_microusd_total": state["committed_microusd"],
            "remaining_microusd": max(0, state["cap_microusd"] - state["committed_microusd"]),
            "usage": usage_totals,
            "standard_rate_estimate_microusd": (standard_tenths + 9) // 10,
            "halted": state["halted"],
            "halt_reasons": state["halt_reasons"],
            "consecutive_failures": state["consecutive_failures"],
            "upstream": records[0].get("upstream", "openai"),
            "refused_by_code": refused_by_code,
            "blocked_answer_leaks": sum(blocked_by_role.values()),
            "blocked_by_role": blocked_by_role,
            "echo_exempted_requests": sum(echo_by_role.values()),
            "echo_exempted_by_role": echo_by_role,
            "forbidden_lists": forbidden_lists,
            "records": len(records),
            "note": ("charged_microusd_total is the conservative budget debit (worst-case rates; "
                     "full reservation for failures, missing usage and pending/crashed requests). "
                     "standard_rate_estimate_microusd uses reported usage at each model's uncached Standard rates and is "
                     "informational only; neither is an OpenAI invoice."),
        }


# ---------------------------------------------------------------------------
# Request rebuilding
# ---------------------------------------------------------------------------
def _text_of(content) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for part in content:
            if not isinstance(part, dict) or part.get("type") != "text" or not isinstance(part.get("text"), str):
                raise RequestRejected(400, "unsupported_content_part")
            parts.append(part["text"])
        return "\n".join(parts)
    raise RequestRejected(400, "unsupported_content")


def build_upstream_request(request, model: str, max_input_bytes: int,
                           max_output_tokens: int = DEFAULT_MAX_OUTPUT_TOKENS) -> dict:
    """Rebuild a text-only upstream payload from an untrusted client request."""
    if not isinstance(request, dict):
        raise RequestRejected(400, "invalid_json_object")
    if request.get("model") != model:
        raise RequestRejected(400, "model_not_allowed")
    messages = request.get("messages")
    if not isinstance(messages, list) or not 1 <= len(messages) <= MAX_MESSAGES:
        raise RequestRejected(400, "invalid_messages")
    rebuilt, total_bytes = [], 0
    for message in messages:
        if not isinstance(message, dict) or message.get("role") not in ALLOWED_ROLES:
            raise RequestRejected(400, "unsupported_role")
        if message.get("tool_calls") or message.get("function_call") or "tool_call_id" in message:
            raise RequestRejected(400, "tool_messages_not_supported")
        text = _text_of(message.get("content"))
        try:
            total_bytes += len(text.encode("utf-8"))
        except UnicodeEncodeError:
            raise RequestRejected(400, "invalid_text") from None
        if total_bytes > max_input_bytes:
            raise RequestRejected(413, "input_too_large")
        rebuilt.append({"role": message["role"], "content": text})
    output_cap = max_output_tokens
    for key in ("max_completion_tokens", "max_tokens"):
        if key in request and request[key] is not None:
            value = request[key]
            if not _is_count(value) or value == 0:
                raise RequestRejected(400, "invalid_max_tokens")
            output_cap = min(output_cap, value)
    stream = request.get("stream", False)
    if not isinstance(stream, bool):
        raise RequestRejected(400, "invalid_stream")
    options = request.get("stream_options")
    include_usage = isinstance(options, dict) and options.get("include_usage") is True
    handled = {"model", "messages", "max_completion_tokens", "max_tokens", "stream", "stream_options"}
    dropped = sorted(str(key)[:64] for key in request if key not in handled)[:32]
    # Responses API payload; every key is pinned here (single output, no tools,
    # no cache retention, no storage, standard tier, low reasoning effort).
    payload = {"model": model, "input": rebuilt, "max_output_tokens": output_cap,
               "reasoning": {"effort": REASONING_EFFORT}, "service_tier": SERVICE_TIER,
               "store": False}
    input_bound, reserved = reservation_for(total_bytes, len(rebuilt), output_cap)
    return {"payload": payload, "input_bytes": total_bytes, "message_count": len(rebuilt),
            "output_cap": output_cap, "input_token_bound": input_bound,
            "reserved_microusd": reserved, "stream": stream, "include_usage": include_usage,
            "dropped_keys": dropped}


# ---------------------------------------------------------------------------
# Answer blocking
# ---------------------------------------------------------------------------
def _words(text: str) -> list[str]:
    return re.findall(r"[a-z0-9]+", text.lower())


def _subwords(text: str) -> set[str]:
    out = set()
    for token in re.findall(r"[A-Za-z0-9]+", text):
        out.add(token.lower())
        for part in re.findall(r"[A-Z]+(?![a-z])|[A-Z]?[a-z]+|[0-9]+", token):
            out.add(part.lower())
    return out


class ForbiddenPhrases:
    """The corpus answer list, matched exactly like corpus.py's leakage scan
    (normalization codeye-words-v1): `subword` tokens against whole tokens and
    their camelCase/snake_case parts, `words` phrases against the text's run of
    [a-z0-9]+ words, `substring` against the lowercased text."""

    def __init__(self, data: bytes):
        self.sha256 = hashlib.sha256(data).hexdigest()
        try:
            doc = json.loads(data)
        except ValueError:
            raise ValueError("forbidden phrase file is not JSON") from None
        if not isinstance(doc, dict) or doc.get("schema") != FORBIDDEN_SCHEMA or doc.get("version") != FORBIDDEN_VERSION \
                or doc.get("normalization") != FORBIDDEN_NORMALIZATION:
            raise ValueError("forbidden phrase file has an unknown schema, version or normalization")
        entries = doc.get("entries")
        if not isinstance(entries, list) or not entries:
            raise ValueError("forbidden phrase file has no entries")
        self.corpus_digest = doc.get("corpus_digest") if isinstance(doc.get("corpus_digest"), str) else None
        # "envelope_capture": a sentinel list that blocks nothing, for recording instruction
        # envelopes in a dry run. Only a --test-upstream relay accepts it.
        self.purpose = doc.get("purpose") if isinstance(doc.get("purpose"), str) else None
        self.entries = []
        self.subword: dict[str, str] = {}
        self.phrases: dict[int, dict[str, str]] = {}  # word count -> phrase -> entry id
        self.substrings: list[tuple[str, str]] = []
        for entry in entries:
            if not isinstance(entry, dict) or not isinstance(entry.get("id"), str) or not isinstance(entry.get("text"), str) \
                    or not entry["text"] or entry.get("mode") not in ("subword", "words", "substring"):
                raise ValueError("invalid forbidden phrase entry")
            text, eid, mode = entry["text"], entry["id"], entry["mode"]
            if mode == "subword":
                if not re.fullmatch(r"[a-z0-9]+", text):
                    raise ValueError("invalid subword entry")
                self.subword.setdefault(text, eid)
            elif mode == "words":
                if not re.fullmatch(r"[a-z0-9]+( [a-z0-9]+)+", text):
                    raise ValueError("invalid words entry")
                self.phrases.setdefault(text.count(" ") + 1, {}).setdefault(text, eid)
            else:
                self.substrings.append((text.lower(), eid))
            self.entries.append(eid)

    def matches(self, texts: list[str]) -> list[str]:
        """Entry ids found in any of the texts (each text is one forwarded message)."""
        found = set()
        for text in texts:
            subs = _subwords(text)
            for token in subs & self.subword.keys():
                found.add(self.subword[token])
            seq = _words(text)
            for size, table in self.phrases.items():
                for i in range(len(seq) - size + 1):
                    eid = table.get(" ".join(seq[i:i + size]))
                    if eid is not None:
                        found.add(eid)
            lower = text.lower()
            for needle, eid in self.substrings:
                if needle in lower:
                    found.add(eid)
        return sorted(found)


def load_forbidden(path: str) -> ForbiddenPhrases:
    """Fail closed: a missing, empty, oversized or malformed list is an error."""
    try:
        with open(path, "rb") as handle:
            data = handle.read(FORBIDDEN_MAX_BYTES + 1)
    except OSError as exc:
        raise ValueError(f"cannot read forbidden phrase file: {exc.strerror}") from None
    if not data or len(data) > FORBIDDEN_MAX_BYTES:
        raise ValueError("forbidden phrase file is empty or too large")
    return ForbiddenPhrases(data)


def parse_test_upstream(url: str) -> int:
    """Return the port of an explicit http://127.0.0.1:PORT test upstream; refuse anything else."""
    match = TEST_UPSTREAM.match(url or "")
    if not match or not 1 <= int(match.group(1)) <= 65535:
        raise ValueError("--test-upstream must be http://127.0.0.1:PORT (loopback only)")
    return int(match.group(1))


# ---------------------------------------------------------------------------
# Upstream
# ---------------------------------------------------------------------------
class OpenAIUpstream:
    """Single-shot POST to the pinned endpoint. No proxy, redirect or retry."""

    def __init__(self, api_key: str, *, _connection_factory=None):
        self._api_key = api_key
        self._factory = _connection_factory or self._https

    @staticmethod
    def _https(timeout: float) -> http.client.HTTPConnection:
        # http.client never consults proxy environment variables or follows redirects.
        return http.client.HTTPSConnection(UPSTREAM_HOST, UPSTREAM_PORT, timeout=timeout,
                                           context=ssl.create_default_context())

    @staticmethod
    def _tighten(connection, deadline: float) -> None:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise UpstreamFailure("timeout")
        if connection.sock is not None:
            connection.sock.settimeout(remaining)

    def post(self, body: bytes, deadline: float) -> tuple[int, bytes]:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise UpstreamFailure("timeout")
        connection = self._factory(remaining)
        try:
            # One total deadline across connect, send, header wait and body read.
            connection.connect()
            self._tighten(connection, deadline)
            connection.request("POST", UPSTREAM_PATH, body=body, headers={
                "Authorization": "Bearer " + self._api_key,
                "Content-Type": "application/json",
                "Accept": "application/json",
                "User-Agent": "codeye-qa-relay/1",
            })
            self._tighten(connection, deadline)
            response = connection.getresponse()
            chunks, size = [], 0
            while True:
                self._tighten(connection, deadline)
                chunk = response.read1(65536) if hasattr(response, "read1") else response.read(65536)
                if not chunk:
                    break
                size += len(chunk)
                if size > MAX_RESPONSE_BYTES:
                    raise UpstreamFailure("response_too_large")
                chunks.append(chunk)
            return response.status, b"".join(chunks)
        except UpstreamFailure:
            raise
        except (TimeoutError, socket.timeout):
            raise UpstreamFailure("timeout") from None
        except (OSError, http.client.HTTPException, ValueError):
            raise UpstreamFailure("transport_error") from None
        finally:
            connection.close()


class LoopbackTestUpstream(OpenAIUpstream):
    """Dry runs: plain HTTP to 127.0.0.1:PORT, same path, deadline and
    response limits. It never sees a real key: the bearer is a fixed dummy."""

    def __init__(self, port: int):
        super().__init__(TEST_UPSTREAM_KEY,
                         _connection_factory=lambda timeout: http.client.HTTPConnection("127.0.0.1", port, timeout=timeout))
        self.port = port


def _safe(value) -> str | None:
    return value if isinstance(value, str) and SAFE_TOKEN.match(value) else None


def _response_text(data: dict) -> tuple[str | None, str | None]:
    """Return (assistant text, finish_reason) from a Responses API object.

    Only assistant message items are read; reasoning and every other output item
    type are ignored and never returned to the client.
    """
    status = data.get("status")
    if status == "completed":
        finish = "stop"
    elif status == "incomplete":
        details = data.get("incomplete_details")
        reason = details.get("reason") if isinstance(details, dict) else None
        finish = INCOMPLETE_FINISH.get(reason, "length")
    else:
        return None, None
    output = data.get("output")
    if not isinstance(output, list):
        return None, None
    texts, refusals, seen_message = [], [], False
    for item in output:
        if not isinstance(item, dict) or item.get("type") != "message" or item.get("role") != "assistant":
            continue
        seen_message = True
        content = item.get("content")
        for part in content if isinstance(content, list) else []:
            if not isinstance(part, dict):
                continue
            if part.get("type") == "output_text" and isinstance(part.get("text"), str):
                texts.append(part["text"])
            elif part.get("type") == "refusal" and isinstance(part.get("refusal"), str):
                refusals.append(part["refusal"])
    if texts or refusals:
        return "".join(texts) if texts else "".join(refusals), finish
    if status == "incomplete" or seen_message:
        return "", finish  # e.g. the output cap was consumed by reasoning
    return None, None


def evaluate_upstream(status: int, body: bytes, prepared: dict, model: str) -> dict:
    """Decide the ledger settlement and the client outcome from a Responses API reply.

    Ledger usage keeps the original field names: prompt_tokens = input_tokens,
    completion_tokens = output_tokens (reasoning included).
    """
    reserved = prepared["reserved_microusd"]
    outcome = {"status": "", "charged": reserved, "usage": None, "detail": {},
               "halt": None, "text": None, "finish": "stop"}
    try:
        data = json.loads(body)
    except ValueError:
        data = None
    if status != 200:
        outcome["status"] = "http_error"
        detail = {"http_status": status}
        error = data.get("error") if isinstance(data, dict) else None
        if isinstance(error, dict):
            detail["error_code"] = _safe(error.get("code"))
            detail["error_type"] = _safe(error.get("type"))
            detail["error_param"] = _safe(error.get("param"))
        outcome["detail"] = detail
        return outcome
    if not isinstance(data, dict):
        outcome["status"] = "invalid_response"
        return outcome
    usage = data.get("usage")
    prompt = usage.get("input_tokens") if isinstance(usage, dict) else None
    completion = usage.get("output_tokens") if isinstance(usage, dict) else None
    usage_ok = _is_count(prompt) and _is_count(completion)
    if usage_ok:
        recorded = {"prompt_tokens": prompt, "completion_tokens": completion}
        in_details = usage.get("input_tokens_details")
        out_details = usage.get("output_tokens_details")
        cached = in_details.get("cached_tokens") if isinstance(in_details, dict) else None
        reasoning = out_details.get("reasoning_tokens") if isinstance(out_details, dict) else None
        if _is_count(cached):
            recorded["cached_tokens"] = cached
        if _is_count(reasoning):
            recorded["reasoning_tokens"] = reasoning
        outcome["usage"] = recorded
        usage_cost = prompt * PRICE_INPUT_WORST + completion * PRICE_OUTPUT
    returned_model = data.get("model")
    tier = data.get("service_tier")
    outcome["detail"] = {"api": "responses", "model": _safe(returned_model), "service_tier": _safe(tier),
                         "response_status": _safe(data.get("status"))}
    if not isinstance(returned_model, str) or not (returned_model == model or returned_model.startswith(model + "-")):
        outcome.update(status="model_mismatch", halt="model_mismatch",
                       charged=max(reserved, usage_cost) if usage_ok else reserved)
        return outcome
    if tier is not None and tier != SERVICE_TIER:
        outcome.update(status="tier_mismatch", halt="tier_mismatch",
                       charged=max(reserved, usage_cost) if usage_ok else reserved)
        return outcome
    if usage_ok and (prompt > prepared["input_token_bound"] or completion > prepared["output_cap"]):
        outcome.update(status="usage_out_of_bounds", halt="usage_out_of_bounds",
                       charged=max(reserved, usage_cost))
        return outcome
    text, finish = _response_text(data)
    if text is None:
        outcome["status"] = "invalid_response"  # failed/cancelled/unknown: full reservation
        return outcome
    outcome["finish"] = finish
    outcome["text"] = text
    if not usage_ok:
        outcome["status"] = "usage_missing"
    elif tier is None:
        outcome["status"] = "tier_unverified"
    else:
        outcome.update(status="ok", charged=usage_cost)
    return outcome


# ---------------------------------------------------------------------------
# Client responses
# ---------------------------------------------------------------------------
def completion_body(model: str, text: str, finish: str, usage: dict | None) -> bytes:
    body = {"id": "chatcmpl-relay-" + uuid.uuid4().hex, "object": "chat.completion",
            "created": int(time.time()), "model": model,
            "choices": [{"index": 0, "message": {"role": "assistant", "content": text},
                         "finish_reason": finish}]}
    if usage:
        body["usage"] = _client_usage(usage)
    return json.dumps(body).encode()


def _client_usage(usage: dict) -> dict:
    return {"prompt_tokens": usage["prompt_tokens"], "completion_tokens": usage["completion_tokens"],
            "total_tokens": usage["prompt_tokens"] + usage["completion_tokens"]}


def sse_body(model: str, text: str, finish: str, usage: dict | None, include_usage: bool) -> bytes:
    cid, created = "chatcmpl-relay-" + uuid.uuid4().hex, int(time.time())

    def chunk(choices, extra=None):
        payload = {"id": cid, "object": "chat.completion.chunk", "created": created,
                   "model": model, "choices": choices}
        if extra:
            payload.update(extra)
        return "data: " + json.dumps(payload) + "\n\n"

    events = [chunk([{"index": 0, "delta": {"role": "assistant", "content": ""}, "finish_reason": None}])]
    for start in range(0, len(text), 2048):
        events.append(chunk([{"index": 0, "delta": {"content": text[start:start + 2048]},
                              "finish_reason": None}]))
    events.append(chunk([{"index": 0, "delta": {}, "finish_reason": finish}]))
    if include_usage and usage:
        events.append(chunk([], {"usage": _client_usage(usage)}))
    events.append("data: [DONE]\n\n")
    return "".join(events).encode()


def error_body(code: str, kind: str = "qa_relay_error", extra: dict | None = None) -> bytes:
    error = {"message": "relay: " + code, "type": kind, "code": code}
    if extra:
        error.update({k: v for k, v in extra.items() if v is not None})
    return json.dumps({"error": error}).encode()


# ---------------------------------------------------------------------------
# Gateway
# ---------------------------------------------------------------------------
class Gateway:
    def __init__(self, *, model: str, ledger: Ledger, upstream: OpenAIUpstream, token: str,
                 max_input_bytes: int = DEFAULT_MAX_INPUT_BYTES, log=None,
                 forbidden: ForbiddenPhrases | None = None, allow_model_echo: bool = False,
                 max_output_tokens: int = DEFAULT_MAX_OUTPUT_TOKENS):
        if allow_model_echo and forbidden is None:
            raise ValueError("allow_model_echo needs an answer list")
        if not 1 <= max_output_tokens <= HARD_MAX_OUTPUT_TOKENS:
            raise ValueError("max_output_tokens out of range")
        self.max_output_tokens = max_output_tokens
        self.model = model
        self.ledger = ledger
        self.upstream = upstream
        self.token = token.encode()
        self.max_input_bytes = max_input_bytes
        self.forbidden = forbidden
        # --allow-model-echo: entry ids found in upstream answers this process returned. Only
        # the relay's own responses count (never client-supplied assistant messages); kept in
        # memory, so a restart starts strict again.
        self.allow_model_echo = allow_model_echo
        self.emitted: set[str] = set()
        self.emitted_lock = threading.Lock()
        self.in_flight = threading.Semaphore(1)
        self.log = log or (lambda event: print(json.dumps(event), file=sys.stderr, flush=True))
        self.port = 0

    def authorized(self, header: str | None) -> bool:
        if not header or not header.startswith("Bearer "):
            return False
        return hmac.compare_digest(header[7:].encode(), self.token)

    def chat(self, body: bytes) -> tuple[int, str, bytes]:
        try:
            request = json.loads(body)
        except ValueError:
            raise RequestRejected(400, "invalid_json") from None
        prepared = build_upstream_request(request, self.model, self.max_input_bytes, self.max_output_tokens)
        texts = [m["content"] for m in prepared["payload"]["input"]]
        hint = next((m.group(1) for m in map(ROLE_HINT.search, texts) if m), None)
        echo = None
        if self.forbidden is not None:
            # Scan exactly the text that would be forwarded, one message at a time.
            found = self.forbidden.matches(texts)
            if found and self.allow_model_echo:
                with self.emitted_lock:
                    echo = [i for i in found if i in self.emitted]
                found = [i for i in found if i not in echo]
            if found:  # any id the model did not itself return blocks the whole request
                self.ledger.block(matches=found, match_count=len(found), input_bytes=prepared["input_bytes"],
                                  message_count=prepared["message_count"], role_hint=hint, echo_matches=echo)
                self.log({"event": "blocked", "status": BLOCKED_STATUS, "match_count": len(found), "role_hint": hint})
                raise RequestRejected(403, BLOCKED_STATUS)
        if not self.in_flight.acquire(timeout=QUEUE_WAIT_S):
            raise RequestRejected(424, "relay_busy")
        try:
            try:
                rid = self.ledger.reserve(
                    model=self.model, reserved_microusd=prepared["reserved_microusd"],
                    input_bytes=prepared["input_bytes"], input_token_bound=prepared["input_token_bound"],
                    message_count=prepared["message_count"], output_cap=prepared["output_cap"],
                    dropped_keys=prepared["dropped_keys"], role_hint=hint, echo_matches=echo)
            except BudgetRefused as refused:
                self.log({"event": "refused", "code": refused.code,
                          "reserved_microusd": prepared["reserved_microusd"]})
                raise RequestRejected(402, refused.code) from None
            outcome = None
            try:
                upstream_body = json.dumps(prepared["payload"]).encode()
                status, raw = self.upstream.post(upstream_body, time.monotonic() + UPSTREAM_DEADLINE_S)
                outcome = evaluate_upstream(status, raw, prepared, self.model)
            except UpstreamFailure as failure:
                outcome = {"status": failure.kind, "charged": prepared["reserved_microusd"],
                           "usage": None, "detail": {}, "halt": None, "text": None}
            finally:
                if outcome is None:  # unexpected gateway error: charge everything
                    outcome = {"status": "gateway_error", "charged": prepared["reserved_microusd"],
                               "usage": None, "detail": {}, "halt": None, "text": None}
                self.ledger.settle(rid, status=outcome["status"], charged_microusd=outcome["charged"],
                                   usage=outcome["usage"], detail=outcome["detail"],
                                   halt_reason=outcome["halt"])
                self.log({"event": "settled", "id": rid, "status": outcome["status"],
                          "reserved_microusd": prepared["reserved_microusd"],
                          "charged_microusd": outcome["charged"], "usage": outcome["usage"]})
        finally:
            self.in_flight.release()
        if outcome["text"] is None or outcome["halt"] is not None:
            detail = outcome["detail"]
            if detail.get("http_status"):
                raise _UpstreamHTTPError(detail)
            raise RequestRejected(424, "upstream_" + outcome["status"])
        if self.allow_model_echo:
            emitted = self.forbidden.matches([outcome["text"]])
            if emitted:
                with self.emitted_lock:
                    self.emitted.update(emitted)
        if prepared["stream"]:
            return 200, "text/event-stream", sse_body(self.model, outcome["text"], outcome["finish"],
                                                      outcome["usage"], prepared["include_usage"])
        return 200, "application/json", completion_body(self.model, outcome["text"], outcome["finish"],
                                                         outcome["usage"])

    def models(self) -> bytes:
        return json.dumps({"object": "list", "data": [
            {"id": self.model, "object": "model", "created": 0, "owned_by": "codeye-qa-relay"}]}).encode()


class _UpstreamHTTPError(RequestRejected):
    def __init__(self, detail: dict):
        super().__init__(424, "upstream_http_error")
        self.extra = {"upstream_status": detail.get("http_status"),
                      "upstream_code": detail.get("error_code"),
                      "upstream_type": detail.get("error_type"),
                      "upstream_param": detail.get("error_param")}


def make_handler(gateway: Gateway):
    class Handler(http.server.BaseHTTPRequestHandler):
        server_version = "codeye-qa-relay"
        sys_version = ""
        timeout = CLIENT_IO_TIMEOUT_S
        error_content_type = "application/json"

        def log_message(self, format, *args):  # noqa: A002 - no request lines in logs
            return

        def send_error(self, code, message=None, explain=None):
            # BaseHTTPRequestHandler emits 400/414/431/501/505 itself; keep all non-retryable.
            if code in RETRYABLE_STATUSES:
                code = 400
            self._reply(code, "application/json", error_body("http_" + str(code)))
            self.close_connection = True

        def _reply(self, status: int, content_type: str, body: bytes) -> None:
            if status in RETRYABLE_STATUSES:
                status = 400
            self.send_response(status)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Connection", "close")
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(body)
            self.close_connection = True

        def _dispatch(self):
            try:
                self._route()
            except RequestRejected as rejected:
                self._reply(rejected.status, "application/json",
                            error_body(rejected.code, extra=getattr(rejected, "extra", None)))
            except Exception as exc:  # never leak details or drop the connection
                gateway.log({"event": "internal_error", "error": type(exc).__name__})
                self._reply(400, "application/json", error_body("internal_error"))

        def _route(self):
            if self.client_address[0] != "127.0.0.1":
                raise RequestRejected(403, "loopback_only")
            if "Origin" in self.headers or "Access-Control-Request-Method" in self.headers:
                raise RequestRejected(403, "origin_not_allowed")
            if self.headers.get("Host") not in (f"127.0.0.1:{gateway.port}", f"localhost:{gateway.port}"):
                raise RequestRejected(403, "host_not_allowed")
            if sum(len(k) + len(v) for k, v in self.headers.items()) > MAX_HEADER_BYTES:
                raise RequestRejected(431, "headers_too_large")
            if not gateway.authorized(self.headers.get("Authorization")):
                raise RequestRejected(401, "unauthorized")
            if self.command == "GET" and self.path == "/v1/models":
                self._reply(200, "application/json", gateway.models())
                return
            if self.command != "POST" or self.path != "/v1/chat/completions":
                raise RequestRejected(404, "not_found")
            if "Transfer-Encoding" in self.headers:
                raise RequestRejected(411, "content_length_required")
            if not (self.headers.get("Content-Type") or "").lower().startswith("application/json"):
                raise RequestRejected(415, "json_required")
            length = self.headers.get("Content-Length")
            if length is None or not length.isdigit():
                raise RequestRejected(411, "content_length_required")
            length = int(length)
            if not 0 < length <= MAX_BODY_BYTES:
                raise RequestRejected(413, "body_too_large")
            deadline = time.monotonic() + CLIENT_IO_TIMEOUT_S
            body = bytearray()
            while len(body) < length:
                if time.monotonic() > deadline:
                    raise RequestRejected(400, "body_timeout")
                chunk = self.rfile.read1(length - len(body))
                if not chunk:
                    raise RequestRejected(400, "body_incomplete")
                body += chunk
            status, content_type, payload = gateway.chat(bytes(body))
            self._reply(status, content_type, payload)

        do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = do_HEAD = do_OPTIONS = _dispatch

    return Handler


class _Server(http.server.ThreadingHTTPServer):
    daemon_threads = True
    request_queue_size = 16


def start_server(gateway: Gateway, port: int = 0) -> _Server:
    server = _Server(("127.0.0.1", port), make_handler(gateway))
    gateway.port = server.server_address[1]
    return server


# ---------------------------------------------------------------------------
# Ready file and CLI
# ---------------------------------------------------------------------------
def write_ready_file(path: str, data: dict) -> tuple[int, int]:
    """Create a fresh 0600 ready file atomically; fail if anything exists at path."""
    if os.path.lexists(path):
        raise FileExistsError("ready file already exists")
    temp = f"{path}.tmp-{os.getpid()}-{secrets.token_hex(4)}"
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
    try:
        os.write(fd, json.dumps(data).encode() + b"\n")
        os.fsync(fd)
        info = os.fstat(fd)
    finally:
        os.close(fd)
    try:
        os.link(temp, path)  # fails if path appeared meanwhile
    finally:
        os.unlink(temp)
    _fsync_dir(path)
    return info.st_dev, info.st_ino


def remove_ready_file(path: str, identity: tuple[int, int]) -> None:
    try:
        info = os.lstat(path)
        if (info.st_dev, info.st_ino) == identity:
            os.unlink(path)
    except FileNotFoundError:
        pass


def _read_api_key() -> str:
    key = os.environ.pop("OPENAI_API_KEY", None)
    if not key or not 8 <= len(key) <= 512 or not all(33 <= ord(c) <= 126 for c in key):
        raise SystemExit("OPENAI_API_KEY is missing or malformed (value not shown)")
    return key


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="QA-only budgeted loopback relay (Chat Completions in, OpenAI Responses API out; not a product path).")
    parser.add_argument("--ledger", required=True, help="durable JSONL budget ledger (one per run)")
    parser.add_argument("--ready-file", help="fresh path to write {base_url, token, model} (0600)")
    parser.add_argument("--model", choices=ALLOWED_MODELS)
    parser.add_argument("--budget-usd", default="9.99",
                        help=f"ledger cap in USD (default %(default)s, <= {HARD_CAP_MICROUSD // 1_000_000}); "
                             "can lower but never raise an existing ledger")
    parser.add_argument("--max-requests", type=int, default=None,
                        help=f"ledger-wide request cap (default {DEFAULT_MAX_REQUESTS}, <= {HARD_MAX_REQUESTS})")
    parser.add_argument("--max-input-bytes", type=int, default=DEFAULT_MAX_INPUT_BYTES,
                        help=f"per-request message text cap (default %(default)s: the worker's default request "
                             f"budget plus OpenCode's messages; <= {HARD_MAX_INPUT_BYTES})")
    parser.add_argument("--max-output-tokens", type=int, default=DEFAULT_MAX_OUTPUT_TOKENS,
                        help=f"per-request output token cap, reserved in full (default %(default)s; "
                             f"<= {HARD_MAX_OUTPUT_TOKENS})")
    parser.add_argument("--port", type=int, default=0, help="loopback port (default: ephemeral)")
    parser.add_argument("--report", action="store_true", help="print the ledger report and exit")
    parser.add_argument("--clear-halt", metavar="REASON",
                        help="append an audited clear_halt record (never changes cap or charges)")
    parser.add_argument("--forbidden-phrases", metavar="FILE",
                        help="answer list from `corpus.py forbidden`; matching requests are blocked "
                             "(ledger `blocked`, no charge, never forwarded). A bad file is a usage error.")
    parser.add_argument("--allow-model-echo", action="store_true",
                        help="opt-in: do not block answer-list ids that occur in an upstream answer this "
                             "process already returned (the model's own words fed back, e.g. investigate -> "
                             "verify). Exempted ids are recorded on the request. Default: block every match.")
    parser.add_argument("--test-upstream", metavar="URL",
                        help="dry runs only: http://127.0.0.1:PORT fake upstream instead of the pinned "
                             "endpoint. Never reads OPENAI_API_KEY; needs a ledger created in this mode.")
    return parser


def main(argv=None, *, _connection_factory=None) -> int:
    """CLI entry. _connection_factory is an in-process test hook only (no CLI/env override)."""
    args = _parser().parse_args(argv)
    if args.report or args.clear_halt is not None:
        try:
            ledger = Ledger(args.ledger, create=False)
            if args.clear_halt is not None:
                ledger.clear_halt(args.clear_halt)
            print(json.dumps(ledger.report(), indent=2, sort_keys=True))
            ledger.close()
        except LedgerError as exc:
            print("ledger error:", exc, file=sys.stderr)
            return 1
        return 0
    if not args.ready_file or not args.model:
        print("--ready-file and --model are required to serve", file=sys.stderr)
        return 2
    try:
        cap = parse_budget(args.budget_usd)
    except ValueError as exc:
        print("invalid --budget-usd:", exc, file=sys.stderr)
        return 2
    if args.max_requests is not None and not 1 <= args.max_requests <= HARD_MAX_REQUESTS:
        print("invalid --max-requests", file=sys.stderr)
        return 2
    if not 1 <= args.max_input_bytes <= HARD_MAX_INPUT_BYTES:
        print("invalid --max-input-bytes", file=sys.stderr)
        return 2
    if not 1 <= args.max_output_tokens <= HARD_MAX_OUTPUT_TOKENS:
        print("invalid --max-output-tokens", file=sys.stderr)
        return 2
    if not 0 <= args.port <= 65535:
        print("invalid --port", file=sys.stderr)
        return 2
    if os.path.lexists(args.ready_file):
        print("ready file already exists; use a fresh path", file=sys.stderr)
        return 2
    forbidden = None
    if args.forbidden_phrases is not None:
        try:
            forbidden = load_forbidden(args.forbidden_phrases)
        except ValueError as exc:
            print("invalid --forbidden-phrases:", exc, file=sys.stderr)
            return 2
        if forbidden.purpose is not None and (forbidden.purpose != FORBIDDEN_CAPTURE or args.test_upstream is None):
            print("invalid --forbidden-phrases: an envelope-capture list blocks nothing and only runs with --test-upstream",
                  file=sys.stderr)
            return 2
    if args.allow_model_echo and forbidden is None:
        print("--allow-model-echo needs --forbidden-phrases", file=sys.stderr)
        return 2
    test_port = None
    if args.test_upstream is not None:
        try:
            test_port = parse_test_upstream(args.test_upstream)
        except ValueError as exc:
            print("invalid --test-upstream:", exc, file=sys.stderr)
            return 2
        if args.port and args.port == test_port:
            print("invalid --test-upstream: same port as the relay", file=sys.stderr)
            return 2
        # Never read, keep or forward a real key in test mode.
        os.environ.pop("OPENAI_API_KEY", None)
        upstream = LoopbackTestUpstream(test_port)
        kind = TEST_UPSTREAM_KIND
    else:
        api_key = _read_api_key()
        upstream = OpenAIUpstream(api_key, _connection_factory=_connection_factory)
        del api_key
        kind = "openai"
    try:
        ledger = Ledger(args.ledger, cap_microusd=cap, max_requests=args.max_requests, upstream_kind=kind)
        if forbidden is not None:
            ledger.note_forbidden(forbidden, allow_model_echo=args.allow_model_echo)
    except LedgerError as exc:
        print("ledger error:", exc, file=sys.stderr)
        return 1
    gateway = Gateway(model=args.model, ledger=ledger, token=secrets.token_urlsafe(32),
                      upstream=upstream, max_input_bytes=args.max_input_bytes, forbidden=forbidden,
                      allow_model_echo=args.allow_model_echo, max_output_tokens=args.max_output_tokens)
    server = start_server(gateway, args.port)
    if test_port is not None and gateway.port == test_port:
        server.server_close()
        ledger.close()
        print("invalid --test-upstream: same port as the relay", file=sys.stderr)
        return 2
    stop = threading.Event()

    def _shutdown(signum, frame):
        if not stop.is_set():
            stop.set()
            threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)
    identity = None
    try:
        identity = write_ready_file(args.ready_file, {
            "base_url": f"http://127.0.0.1:{gateway.port}/v1",
            "token": gateway.token.decode(), "model": args.model, "pid": os.getpid()})
        report = ledger.report()
        gateway.log({"event": "ready", "port": gateway.port, "model": args.model,
                     "cap_microusd": report["cap_microusd"],
                     "charged_microusd_total": report["charged_microusd_total"],
                     "halted": report["halted"], "upstream": kind,
                     "forbidden_sha256": forbidden.sha256 if forbidden else None,
                     "allow_model_echo": args.allow_model_echo})
        server.serve_forever(poll_interval=0.2)
    except FileExistsError:
        print("ready file already exists; use a fresh path", file=sys.stderr)
        return 2
    finally:
        server.server_close()
        if identity is not None:
            remove_ready_file(args.ready_file, identity)
        # Let an in-flight upstream call settle before closing the ledger. If it
        # cannot, its reservation stays pending and is charged in full.
        if not gateway.in_flight.acquire(timeout=UPSTREAM_DEADLINE_S + 10):
            gateway.log({"event": "shutdown_with_pending_request"})
        ledger.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
