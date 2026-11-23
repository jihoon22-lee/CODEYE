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
only evidence ids present in `<review-input>` are cited. A code or test excerpt
longer than the citation limit is cited as a line range inside it
(`ev_…:START-END`), and a candidate states a mechanism.

The documents role states one contract for each of the first three input
documents, in-source API documentation (Javadoc, docstring, rustdoc) first.
The whole-run investigate compares every input contract: an in-source document
is compared on one line of the documented declaration's body, cited as a range
of the code excerpt holding it; another contract on the first code excerpt in
its scope, cited as candidates are. An even-numbered contract with a code
excerpt longer than the citation limit in its scope cites that excerpt without
a range, which the product rejects; the same answer after the format repair
lets the product drop that comparison alone (`contract_comparison_omitted`).

Modes: `normal` (small usage), `expensive` (usage at the relay's bounds, to
exhaust a small ledger cap quickly), `fail` (HTTP 500, charged in full by the relay),
`unauthorized` (HTTP 401, which OpenCode does not retry: a fast failing probe).
A request with `"stream": true` (OpenCode's OpenAI provider called directly, without
the relay) gets the same reply as a Responses event stream; its `input` content
may be a list of `input_text` parts.
`--tls-cert CERT --tls-key KEY` serves HTTPS (an internal endpoint behind a private
CA, docs/OPERATIONS.md#사내-ca); the key and certificate are the caller's test files.
`--delay-seconds N` answers each request N seconds late, to see a slow model through the
relay (which sends its headers only once the answer is complete) and OpenCode's waits.
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
import ssl
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
# The longest code or test excerpt a citation may name whole, and the longest
# cited range (record.WholeCitationLines, record.MaxCitedLines).
CITED_LINES = 80


def _evidence(node, out, ranges=None):
    """Collect (id, kind) of every cited-able excerpt in the review input, and
    into ranges the citation of each one: its id, or its first lines as a
    range when it is longer than CITED_LINES."""
    if isinstance(node, dict):
        ident, path = node.get("id") or node.get("evidence_id"), node.get("path")
        if isinstance(ident, str) and ident.startswith("ev_") and isinstance(path, str):
            kind = node.get("kind")
            if kind not in ("code", "document"):
                kind = "code" if CODE_SUFFIX.search(path) else "document"
            out.setdefault(ident, kind)
            start, end = node.get("start_line"), node.get("end_line")
            if ranges is not None and ident not in ranges:
                ranges[ident] = ident
                if (isinstance(start, int) and isinstance(end, int) and end - start + 1 > CITED_LINES
                        and node.get("origin") != "cited"):
                    ranges[ident] = f"{ident}:{start}-{start + CITED_LINES - 1}"
        for value in node.values():
            _evidence(value, out, ranges)
    elif isinstance(node, list):
        for value in node:
            _evidence(value, out, ranges)


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


def _under(path: str, scope: str) -> bool:
    return scope in (".", "") or path == scope or path.startswith(scope.rstrip("/") + "/")


def _body_line(doc: dict, lines: dict[int, str]) -> int | None:
    """One line of the declaration an in-source document describes: the line
    after a docstring, else the first line after the opening brace of the
    first declaration below the comment."""
    end = doc.get("end_line")
    if not isinstance(end, int):
        return None
    if str(doc.get("path", "")).endswith((".py", ".pyi")):
        return end + 1 if end + 1 in lines else None
    n = end + 1
    while n in lines and "{" not in lines[n]:
        n += 1
    if n not in lines:
        return None
    rest = lines[n].split("{", 1)[1].strip()
    if rest and rest != "}":
        return n
    return n + 1 if n + 1 in lines else None


def _comparisons(data: dict) -> list[dict]:
    """One comparison per input contract (see the module docstring)."""
    scope = data.get("scope") if isinstance(data.get("scope"), dict) else {}
    excerpts = [e for e in scope.get("excerpts") or [] if isinstance(e, dict) and isinstance(e.get("id"), str)
                and isinstance(e.get("path"), str) and isinstance(e.get("start_line"), int) and isinstance(e.get("end_line"), int)]
    code = [e for e in excerpts if e.get("kind") == "code"]
    text: dict[str, dict[int, str]] = {}
    for e in excerpts:
        if isinstance(e.get("text"), str):
            for n, line in enumerate(e["text"].split("\n")):
                text.setdefault(e["path"], {}).setdefault(e["start_line"] + n, line)
    documents = data.get("documents") if isinstance(data.get("documents"), dict) else {}
    in_source = {d["id"]: d for d in documents.get("evidence") or [] if isinstance(d, dict) and isinstance(d.get("id"), str)
                 and (d.get("directive_path") or str(d.get("path", "")).endswith(".java"))}
    out = []
    for index, contract in enumerate((documents.get("contracts") or [])[:6], start=1):
        if not isinstance(contract, dict) or not isinstance(contract.get("applies_to"), str):
            continue
        applies, refs = contract["applies_to"], [r for r in contract.get("evidence_ids") or [] if isinstance(r, str)]
        cite = None
        doc = next((in_source[r] for r in refs if r in in_source), None)
        if doc is not None:
            line = _body_line(doc, text.get(doc.get("path"), {}))
            holder = next((e for e in code if line is not None and e["path"] == doc.get("path") and _under(e["path"], applies)
                           and e["start_line"] <= line <= e["end_line"]), None)
            if holder is not None:
                cite = f"{holder['id']}:{line}-{line}"
        else:
            scoped = [e for e in code if _under(e["path"], applies) and e.get("origin") != "cited"]
            long = [e for e in scoped if e["end_line"] - e["start_line"] + 1 > CITED_LINES]
            if index % 2 == 0 and long:
                cite = long[0]["id"]  # rejected: a long excerpt needs a range
            elif scoped:
                e = scoped[0]
                cite = e["id"] if e["end_line"] - e["start_line"] + 1 <= CITED_LINES else \
                    f"{e['id']}:{e['start_line']}-{e['start_line'] + CITED_LINES - 1}"
        out.append({"contract_index": index, "applies_to": applies, "status": "match" if cite else "unknown",
                    "reason": TEXT, "document_evidence_ids": refs, "code_evidence_ids": [cite] if cite else []})
    return out


def result_for(role: str, review_input, echo: str | None = None) -> dict:
    evidence: dict[str, str] = {}
    cited: dict[str, str] = {}
    _evidence(review_input, evidence, cited)
    code = [cited[i] for i, k in evidence.items() if k == "code"]
    title = "Placeholder candidate (dry run)" + (": " + echo if echo else "")
    candidate = {"title": title, "condition": TEXT, "mechanism": TEXT, "impact": TEXT,
                 "improvement": TEXT, "counterpoint": TEXT, "evidence_ids": code[:1]}
    if role == "probe":  # codeye model probe: {"a": int, "b": int} -> {"sum": a + b}
        data = review_input if isinstance(review_input, dict) else {}
        a, b = data.get("a"), data.get("b")
        return {"sum": a + b} if isinstance(a, int) and isinstance(b, int) else {}
    if role == "investigate":
        data = review_input if isinstance(review_input, dict) else {}
        out = {"candidates": [candidate] if code else []}
        if isinstance(data.get("scope"), dict) and data["scope"].get("whole_run") is True:
            comparisons = _comparisons(data)
            if comparisons:
                out["contract_comparisons"] = comparisons
        return out
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
        data = review_input if isinstance(review_input, dict) else {}
        docs = [d for d in data.get("documents") or [] if isinstance(d, dict)
                and isinstance(d.get("evidence_id"), str) and isinstance(d.get("applies_to"), str)]
        docs.sort(key=lambda d: not (d.get("public_api") or d.get("directive_path")))
        contracts = [{"statement": TEXT, "applies_to": d["applies_to"], "evidence_ids": [d["evidence_id"]]} for d in docs[:3]]
        return {"contracts": contracts, "conflicts": [], "questions": [], "instruction_notes": []}
    if role == "environment":  # one note per gap module, citing that module's requirements
        data = review_input if isinstance(review_input, dict) else {}
        reqs = [r for r in data.get("requirements") or [] if isinstance(r, dict)]
        notes = []
        for module in data.get("gap_modules") or []:
            own = [r for r in reqs if r.get("module") == module]
            notes.append({"module": module, "requirement_ids": [r.get("id") for r in own if r.get("id")],
                          "evidence_ids": sorted({r.get("evidence_id") for r in own if r.get("evidence_id")}),
                          "affected_checks": TEXT, "still_checkable": TEXT, "needed_observation": TEXT})
        return {"notes": notes}
    if role == "interpret":  # at least one item; a limit for the first observation
        data = review_input if isinstance(review_input, dict) else {}
        ids = [o.get("id") for o in data.get("observations") or [] if isinstance(o, dict) and o.get("id")]
        return {"hypotheses": [], "limits": [{"observation_ids": ids[:1], "statement": TEXT}] if ids else []}
    return {}


def content_text(content) -> str:
    """The text of a message: a string (the relay's rebuilt payload) or the
    list of input_text parts OpenCode's OpenAI provider sends directly."""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "\n".join(p.get("text", "") for p in content
                         if isinstance(p, dict) and p.get("type") in ("input_text", "text") and isinstance(p.get("text"), str))
    return ""


def reply_text(payload: dict, echo: str | None = None) -> tuple[str, str]:
    """Return (role, assistant text) for a Responses payload."""
    texts = [content_text(m.get("content", "")) for m in payload.get("input", [])
             if isinstance(m, dict) and m.get("role") == "user"]
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
    def __init__(self, mode: str, dump: Path | None, echo: str | None = None, delay: float = 0.0):
        self.mode = mode
        self.delay = delay
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
        if self.delay > 0:
            time.sleep(self.delay)
        if self.mode == "fail":
            return 500, {"error": {"message": "fake failure", "type": "server_error", "code": "fake_failure"}}
        if self.mode == "unauthorized":
            return 401, {"error": {"message": "fake: invalid api key", "type": "invalid_request_error", "code": "invalid_api_key"}}
        role, text = reply_text(payload, self.echo)
        messages = payload.get("input", [])
        input_bytes = sum(len(content_text(m.get("content", "")).encode()) for m in messages if isinstance(m, dict))
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


def sse_events(body: dict) -> bytes:
    """The Responses stream of a completed reply (what OpenCode's OpenAI
    provider requests with "stream": true): created, one text delta, done."""
    message = body["output"][0]
    text = message["content"][0]["text"]
    started = dict(body, status="in_progress", output=[], usage=None, created_at=int(time.time()))
    events = [
        {"type": "response.created", "response": started},
        {"type": "response.output_item.added", "output_index": 0,
         "item": dict(message, status="in_progress", content=[])},
        {"type": "response.content_part.added", "item_id": message["id"], "output_index": 0, "content_index": 0,
         "part": {"type": "output_text", "text": "", "annotations": []}},
        {"type": "response.output_text.delta", "item_id": message["id"], "output_index": 0, "content_index": 0,
         "delta": text, "logprobs": []},
        {"type": "response.output_text.done", "item_id": message["id"], "output_index": 0, "content_index": 0,
         "text": text, "logprobs": []},
        {"type": "response.output_item.done", "output_index": 0, "item": message},
        {"type": "response.completed", "response": dict(body, created_at=started["created_at"])},
    ]
    out = bytearray()
    for number, event in enumerate(events):
        event["sequence_number"] = number
        out += f"event: {event['type']}\ndata: {json.dumps(event)}\n\n".encode()
    return bytes(out)


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
            stream = status == 200 and payload.get("stream") is True
            data = sse_events(body) if stream else json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "text/event-stream" if stream else "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    server = http.server.ThreadingHTTPServer(("127.0.0.1", port), Handler)
    server.daemon_threads = True
    return server


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--ready-file", required=True, type=Path, help="fresh path; receives {\"port\": N}")
    parser.add_argument("--mode", choices=("normal", "expensive", "fail", "unauthorized"), default="normal")
    parser.add_argument("--dump", type=Path, help="write every received payload here (dry-run analysis only)")
    parser.add_argument("--echo-text", help="append this operator text to investigate candidate titles")
    parser.add_argument("--delay-seconds", type=float, default=0.0, help="answer every request this many seconds late")
    parser.add_argument("--tls-cert", type=Path, help="serve HTTPS with this certificate chain (PEM)")
    parser.add_argument("--tls-key", type=Path, help="the private key of --tls-cert (PEM)")
    args = parser.parse_args(argv)
    if (args.tls_cert is None) != (args.tls_key is None):
        print("--tls-cert and --tls-key go together", file=sys.stderr)
        return 2
    if args.ready_file.exists():
        print("ready file exists", file=sys.stderr)
        return 2
    if args.dump is not None:
        args.dump.mkdir(mode=0o700, parents=True, exist_ok=True)
    if not 0 <= args.delay_seconds <= 3600:
        print("--delay-seconds is between 0 and 3600", file=sys.stderr)
        return 2
    fake = FakeUpstream(args.mode, args.dump, args.echo_text, args.delay_seconds)
    server = make_server(fake)
    if args.tls_cert is not None:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(args.tls_cert, args.tls_key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
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
