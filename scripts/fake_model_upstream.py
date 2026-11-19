#!/usr/bin/env python3
"""Dry-run double: a loopback-only fake of the OpenAI Responses endpoint.

It exists so the evaluation runner, OpenCode and the budget relay can be run end
to end without a model or a key. It is NOT a model: every reply is a fixed
placeholder derived from the request structure (role name, evidence ids), never
from labels, and results produced through it carry no quality information. The
relay reaches it only through `model_budget_relay.py --test-upstream
http://127.0.0.1:PORT`.

Replies satisfy the relay's settlement rules (model `<requested>-fake`,
`service_tier: default`, usage within the request bounds) and the CODEYE review
output contract: `workspace_proof` is copied from the attached identity file and
only evidence ids present in `<review-input>` are cited.

Modes: `normal` (small usage), `expensive` (usage at the relay's bounds, to
exhaust a small ledger cap quickly), `fail` (HTTP 500, charged in full by the relay).
`--echo-text TEXT` appends an operator-chosen text to every investigate candidate
title, to see whether a model's own words reach the verify request (the relay's
`--allow-model-echo` path). The text comes from the operator, never from labels.

Stdlib only.
"""
from __future__ import annotations

import argparse
import http.server
import json
import os
import re
import signal
import sys
import threading
import time
from pathlib import Path

# OpenCode sends the attached identity file as a Read tool result ("1: <proof>") before the prompt.
PROOF = re.compile(r"^1: ([0-9a-f]{64})$", re.M)
REVIEW_INPUT = re.compile(r"<review-input>\n(.*)\n</review-input>", re.S)
ROLE = re.compile(r"^Role: ([a-z]+)$", re.M)
CODE_SUFFIX = re.compile(r"\.(go|py|pyi|js|jsx|mjs|cjs|ts|tsx|mts|cts|rs|c|cc|cpp|cxx|h|hh|hpp|hxx)$")
TEXT = "Placeholder from the dry-run upstream; no model produced this text."


def _evidence(node, out):
    """Collect (id, kind) of every cited-able excerpt in the review input."""
    if isinstance(node, dict):
        ident, path = node.get("id") or node.get("evidence_id"), node.get("path")
        if isinstance(ident, str) and ident.startswith("ev_") and isinstance(path, str):
            kind = node.get("kind")
            if kind not in ("code", "document"):
                kind = "code" if CODE_SUFFIX.search(path) else "document"
            out.setdefault(ident, kind)
        for value in node.values():
            _evidence(value, out)
    elif isinstance(node, list):
        for value in node:
            _evidence(value, out)


def _candidates(node, out):
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "candidates" and isinstance(value, list):
                for c in value:
                    if isinstance(c, dict) and isinstance(c.get("id"), str):
                        refs = c.get("evidence_ids") or c.get("evidence") or []
                        out.append((c["id"], [r for r in refs if isinstance(r, str)]))
            else:
                _candidates(value, out)
    elif isinstance(node, list):
        for value in node:
            _candidates(value, out)


def result_for(role: str, review_input, echo: str | None = None) -> dict:
    evidence: dict[str, str] = {}
    _evidence(review_input, evidence)
    code = [i for i, k in evidence.items() if k == "code"]
    docs = [i for i, k in evidence.items() if k == "document"]
    title = "Placeholder candidate (dry run)" + (": " + echo if echo else "")
    candidate = {"title": title, "condition": TEXT, "impact": TEXT,
                 "improvement": TEXT, "counterpoint": TEXT, "evidence_ids": code[:1]}
    if role == "context":  # review-v1
        if code:
            return {"facts": [{"kind": "observed", "statement": TEXT, "evidence_ids": code[:1]}]}
        return {"facts": [{"kind": "documented", "statement": TEXT, "evidence_ids": docs[:1]}]}
    if role == "investigate":
        return {"candidates": [candidate] if code else []}
    if role == "verify":
        found: list[tuple[str, list[str]]] = []
        _candidates(review_input, found)
        out = []
        for ident, refs in found:
            out.append({"candidate_id": ident, "verdict": "supported", "category": "correctness", "severity": "low",
                        "severity_reason": TEXT, "impact": TEXT, "valid_conditions": [TEXT], "invalid_conditions": [],
                        "supporting_evidence_ids": refs[:1], "counterevidence_ids": [], "unresolved_assumptions": [],
                        "next_verification": [TEXT]})
        return {"verifications": out}
    if role == "documents":
        return {"contracts": [], "conflicts": [], "questions": [], "instruction_notes": []}
    if role == "environment":  # review-r1: one note per gap module, citing that module's requirements
        data = review_input if isinstance(review_input, dict) else {}
        reqs = [r for r in data.get("requirements") or [] if isinstance(r, dict)]
        notes = []
        for module in data.get("gap_modules") or []:
            own = [r for r in reqs if r.get("module") == module]
            notes.append({"module": module, "requirement_ids": [r.get("id") for r in own if r.get("id")],
                          "evidence_ids": sorted({r.get("evidence_id") for r in own if r.get("evidence_id")}),
                          "affected_checks": TEXT, "still_checkable": TEXT, "needed_observation": TEXT})
        return {"notes": notes}
    if role == "interpret":  # review-r1: at least one item; a limit for the first observation
        data = review_input if isinstance(review_input, dict) else {}
        ids = [o.get("id") for o in data.get("observations") or [] if isinstance(o, dict) and o.get("id")]
        return {"hypotheses": [], "limits": [{"observation_ids": ids[:1], "statement": TEXT}] if ids else []}
    return {}


def reply_text(payload: dict, echo: str | None = None) -> tuple[str, str]:
    """Return (role, assistant text) for a rebuilt Responses payload."""
    texts = [m.get("content", "") for m in payload.get("input", []) if isinstance(m, dict) and m.get("role") == "user"]
    prompt = next((t for t in texts if ROLE.search(t)), "")
    found = ROLE.search(prompt)
    role = found.group(1) if found else "unknown"
    match = REVIEW_INPUT.search(prompt)
    review_input = None
    if match:
        try:
            review_input = json.loads(match.group(1))
        except ValueError:
            review_input = None
    head = prompt[:found.start()] if found else prompt  # the attachment precedes the prompt
    proofs = PROOF.findall(head)
    proof = proofs[-1] if proofs else ""
    return role, json.dumps({"workspace_proof": proof, "result": result_for(role, review_input, echo)},
                            ensure_ascii=False)


class FakeUpstream:
    def __init__(self, mode: str, dump: Path | None, echo: str | None = None):
        self.mode = mode
        self.dump = dump
        self.echo = echo
        self.calls = 0
        self.lock = threading.Lock()

    def respond(self, payload: dict) -> tuple[int, dict]:
        with self.lock:
            self.calls += 1
            number = self.calls
        if self.dump is not None:
            (self.dump / f"{number:04d}.json").write_text(json.dumps(payload, ensure_ascii=False, indent=1))
        if self.mode == "fail":
            return 500, {"error": {"message": "fake failure", "type": "server_error", "code": "fake_failure"}}
        role, text = reply_text(payload, self.echo)
        messages = payload.get("input", [])
        input_bytes = sum(len(str(m.get("content", "")).encode()) for m in messages if isinstance(m, dict))
        cap = int(payload.get("max_output_tokens") or 1)
        if self.mode == "expensive":
            usage_in, usage_out = input_bytes, cap  # the relay's bounds: input bytes and the output cap
        else:
            usage_in, usage_out = max(1, input_bytes // 4), min(cap, max(1, len(text) // 4))
        model = str(payload.get("model", "")) + "-fake"
        return 200, {"id": f"resp_fake_{number}", "object": "response", "status": "completed", "model": model,
                     "service_tier": "default", "incomplete_details": None,
                     "output": [{"type": "message", "id": f"msg_{number}", "role": "assistant", "status": "completed",
                                 "content": [{"type": "output_text", "text": text, "annotations": []}]}],
                     "usage": {"input_tokens": usage_in, "output_tokens": usage_out,
                               "total_tokens": usage_in + usage_out,
                               "input_tokens_details": {"cached_tokens": 0},
                               "output_tokens_details": {"reasoning_tokens": 0}},
                     "metadata": {"codeye_fake_role": role}}


def make_server(fake: FakeUpstream, port: int = 0) -> http.server.ThreadingHTTPServer:
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *args):
            return

        def do_POST(self):
            if self.client_address[0] != "127.0.0.1" or self.path != "/v1/responses":
                self.send_error(404)
                return
            length = int(self.headers.get("Content-Length") or 0)
            try:
                payload = json.loads(self.rfile.read(length))
            except ValueError:
                self.send_error(400)
                return
            status, body = fake.respond(payload)
            data = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    server = http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler)
    server.daemon_threads = True
    return server


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--ready-file", required=True, type=Path, help="fresh path; receives {\"port\": N}")
    parser.add_argument("--mode", choices=("normal", "expensive", "fail"), default="normal")
    parser.add_argument("--dump", type=Path, help="write every received payload here (dry-run analysis only)")
    parser.add_argument("--echo-text", help="append this operator text to investigate candidate titles")
    args = parser.parse_args(argv)
    if args.ready_file.exists():
        print("ready file exists", file=sys.stderr)
        return 2
    if args.dump is not None:
        args.dump.mkdir(mode=0o700, parents=True, exist_ok=True)
    fake = FakeUpstream(args.mode, args.dump, args.echo_text)
    server = make_server(fake)
    stop = threading.Event()

    def _shutdown(signum, frame):
        if not stop.is_set():
            stop.set()
            threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, _shutdown)
    signal.signal(signal.SIGINT, _shutdown)
    temp = args.ready_file.with_name(args.ready_file.name + f".tmp-{os.getpid()}")
    temp.write_text(json.dumps({"port": server.server_address[1], "pid": os.getpid(), "mode": args.mode}) + "\n")
    os.replace(temp, args.ready_file)
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()
        try:
            args.ready_file.unlink()
        except FileNotFoundError:
            pass
        print(json.dumps({"event": "stopped", "calls": fake.calls, "at": time.time()}), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
