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
stands for the whole excerpt (`citation_range_unspecified`).

`--shape-errors RATE` (0 to 1, default 0: replies unchanged) makes a share of
the items carry a fault of form on every call, the format repair included, so
the product's repair-then-resolve path runs: a verdict cites the long excerpt
holding its candidate's range without a range (`citation_range_missing`), a
candidate, document contract or environment note repeats an ID, an
interpretation limit repeats its observation ID, a triage answer for a
problem or harmless group repeats its citation and one for an unclear group
names a classification that does not exist (`triage_classification_invalid`,
the group is left out). `--integrity-errors RATE` makes a share of the
verdicts cite an evidence ID the request did not hold (`evidence_id_unknown`),
which the product keeps rejecting: the candidate is quarantined and the
verify task ends incomplete. In a triage answer the chosen group cites such
an ID (problem or harmless; the group is rejected) and the answer adds an
entry for a group the request did not offer (`triage_group_unknown`).

The investigate role also raises a candidate about the first excerpt on a
path that is not shipped code (the request's `path_class`), beside its candidate
about shipped code: the product does not send it to verification and the report
counts it instead of listing it. The triage role classifies the offered
diagnostic groups in turn as problem, harmless and unclear, citing each group's
first context excerpt (as a range when it is longer than the citation limit).
An unclear answer names what is missing (`missing`); a harmless group of two or
more diagnostics is answered part by part (the first diagnostic harmless, the
others unclear). `--shape-errors` also makes a share of the unclear answers name
a function the request holds as the missing definition
(`triage_unclear_input_present`, the answer stands with a limitation) and of the
part answers list a diagnostic twice (`triage_diagnostics_invalid`, the valid part
stands and the rest is not triaged). The choice of an item depends
on its role, the snapshot digest and the item (candidate ID or position), not
on the repair block, so the repair of an item gets the same fault.

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
`--delay-jitter J --seed S` adds to that a delay between 0 and J seconds drawn from the
seed and the request's review input, so concurrent requests (codeye agent
--review-parallel) end in a seeded order other than the order they arrived in. Requests
are answered concurrently, each on its own thread; the stop line reports the most
requests in flight at once (`peak_in_flight`).
`--echo-text TEXT` appends an operator-chosen text to every investigate candidate
title, to see whether a model's own words reach the verify request (the relay's
`--allow-model-echo` path). The text comes from the operator, never from labels.
`--leads` (or `--leads=qualified`) makes an investigation with a candidate whose
request offers leads (`scope.lead_limit`, docs/REVIEW.md#단서-후속-조사) name up to
that many: one per qualified call (`pkg.Name(`) on a sent code line whose name no
sent excerpt declares, each for its first candidate. `--leads=project` chooses
leads the way the lead contract asks a model to: any call on a sent code line,
qualified or not (`Name(`, `pkg.Name(`, `recv.Name(`), whose name no sent
excerpt declares, leaving out the builtins, keywords and standard namespaces of
Go, Java, Python, JS/TS, C/C++ and Rust in a small fixed list (`NOT_PROJECT`),
up to `lead_limit`, each a `definition` lead of the first candidate. In both
modes the follow-up (role `leads`) refines that candidate from its first lead
and adds one candidate from each other lead (basis the lead). A refinement
keeps every evidence ID of the candidate and cites the lead's line and the
first resolved excerpt that is not partial (`partial_evidence_ids`); with no
such excerpt that lead gets no result. An added candidate cites the lead's
line and its first resolved excerpt. `--shape-errors` makes a share of the
results repeat (`lead_result_duplicate`, the repeat is dropped). Without
`--leads`, or without a candidate, the investigation names no lead.

Stdlib only.
"""
from __future__ import annotations

import argparse
import hashlib
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


def _evidence(node, out, ranges=None, classes=None):
    """Collect (id, kind) of every cited-able excerpt in the review input, and
    into ranges the citation of each one: its id, or its first lines as a
    range when it is longer than CITED_LINES. classes gets the path class of
    each excerpt a request names as not shipped code."""
    if isinstance(node, dict):
        ident, path = node.get("id") or node.get("evidence_id"), node.get("path")
        if isinstance(ident, str) and ident.startswith("ev_") and isinstance(path, str):
            kind = node.get("kind")
            if kind not in ("code", "document"):
                kind = "code" if CODE_SUFFIX.search(path) else "document"
            out.setdefault(ident, kind)
            if classes is not None and node.get("path_class"):
                classes[ident] = node["path_class"]
            start, end = node.get("start_line"), node.get("end_line")
            if ranges is not None and ident not in ranges:
                ranges[ident] = ident
                if (isinstance(start, int) and isinstance(end, int) and end - start + 1 > CITED_LINES
                        and node.get("origin") != "cited"):
                    ranges[ident] = f"{ident}:{start}-{start + CITED_LINES - 1}"
        for value in node.values():
            _evidence(value, out, ranges, classes)
    elif isinstance(node, list):
        for value in node:
            _evidence(value, out, ranges, classes)


def _texts(node, out):
    """Collect the numbered text of every excerpt of the request by its id."""
    if isinstance(node, dict):
        ident = node.get("id")
        if isinstance(ident, str) and ident.startswith("ev_") and isinstance(node.get("text"), str):
            out[ident] = node["text"]
        for value in node.values():
            _texts(value, out)
    elif isinstance(node, list):
        for value in node:
            _texts(value, out)


FUNCTION = re.compile(r"^\d+\| func ([A-Za-z_][A-Za-z0-9_]*)\(", re.M)
NUMBERED = re.compile(r"^(\d+)\| (.*)$")
QUALIFIED_CALL = re.compile(r"\b([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)\(")
# Qualifiers of common standard-library calls. A snapshot holds no
# definition for them, so the double names them last.
STANDARD_QUALIFIERS = frozenset((
    "atomic", "base64", "binary", "bufio", "bytes", "context", "errors", "exec", "filepath", "flag", "fmt", "fs", "hex",
    "http", "io", "json", "log", "maps", "math", "os", "path", "rand", "reflect", "regexp", "runtime", "sha256", "slices",
    "slog", "sort", "strconv", "strings", "sync", "syscall", "time", "unicode", "url", "utf8"))
DECLARATION = re.compile(r"\bfunc (?:\([^)]*\) )?([A-Za-z_][A-Za-z0-9_]*)\(")
# --leads=project: every call on a line (an optional qualifier or receiver,
# then the name), the declarations of the six languages a sent excerpt may
# hold (Go, Python, Rust and JS functions by keyword; a Java, C/C++ or JS/TS
# method or function head is a line ending in "{" whose first call-like name
# is not a keyword, with or without a type before it; a class, struct,
# interface, enum, trait or type by keyword), comment lines, and the names
# no project holds.
ANY_CALL = re.compile(r"(?:\b([A-Za-z_][A-Za-z0-9_]*)\s*(?:\.|->|::)\s*)?\b([A-Za-z_][A-Za-z0-9_]*)\s*\(")
KEYWORD_DECLARATION = re.compile(r"\b(?:func(?:\s*\([^)]*\))?|def|fn|function)\s+([A-Za-z_][A-Za-z0-9_]*)")
HEAD_DECLARATION = re.compile(r"^\s*(?:[A-Za-z_][\w<>\[\],:*&.?]*\s+)+[*&]*([A-Za-z_][A-Za-z0-9_]*)\s*\([^;]*\)[^;]*\{\s*$")
# A JS/TS method head has no type before its name; its parameters hold no
# call, so a call that opens a callback block is not taken for one.
METHOD_HEAD = re.compile(r"^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\([^;()]*\)\s*(?::\s*[^;{}()]*)?\{\s*$")
TYPE_DECLARATION = re.compile(r"\b(?:class|struct|interface|enum|trait|type|union)\s+([A-Za-z_][A-Za-z0-9_]*)")
COMMENT = re.compile(r"^\s*(?://|#|/\*|\*|--)")
# A line opening with one of these is a statement, not a declaration head.
CONTROL = frozenset(("if", "for", "while", "switch", "catch", "return", "else", "do", "try", "case", "defer", "go", "await", "yield",
                     "throw", "new", "synchronized", "elif", "with", "assert", "match", "loop", "foreach", "using", "lock"))
NOT_PROJECT = frozenset((
    # keywords that take parentheses
    "if", "for", "while", "switch", "catch", "return", "sizeof", "typeof", "alignof", "decltype", "new", "delete", "throw", "match",
    "await", "yield", "super", "this", "self", "assert", "elif", "not", "and", "or", "in", "lambda", "with", "func", "function", "def", "fn",
    "defer", "go", "select", "case", "else", "do", "try", "synchronized", "import", "require", "loop", "where", "impl",
    # Go builtins and conversions
    "append", "cap", "clear", "close", "complex", "copy", "imag", "len", "make", "max", "min", "panic", "print", "println", "real",
    "recover", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64",
    "complex64", "complex128", "string", "byte", "rune", "bool", "error", "any",
    # Python builtins
    "range", "str", "float", "list", "dict", "set", "tuple", "open", "isinstance", "issubclass", "type", "repr", "sorted", "reversed",
    "enumerate", "zip", "map", "filter", "sum", "abs", "all", "getattr", "setattr", "hasattr", "iter", "next", "id", "hash", "format",
    "vars", "dir", "round", "divmod", "chr", "ord", "bytes", "bytearray", "object", "property", "staticmethod", "classmethod", "input",
    # JS/TS builtins
    "parseInt", "parseFloat", "setTimeout", "setInterval", "clearTimeout", "clearInterval", "isNaN", "isFinite", "fetch", "Number",
    "String", "Boolean", "Array", "Object", "Error", "Promise", "Symbol", "Map", "Set", "Date", "RegExp", "BigInt",
    # Java, C/C++ and Rust names a project does not define
    "equals", "hashCode", "toString", "valueOf", "printf", "fprintf", "sprintf", "snprintf", "puts", "malloc", "calloc", "realloc",
    "free", "memcpy", "memmove", "memset", "memcmp", "strlen", "strcmp", "strncmp", "strcpy", "strncpy", "exit", "abort", "Some", "Ok",
    "Err", "Box", "Vec", "Option", "Result", "unwrap", "expect", "clone", "into", "collect", "to_string", "as_str", "push", "iter_mut",
))
# Qualifiers of standard or runtime namespaces (and the Go ones above).
STANDARD_NAMESPACES = STANDARD_QUALIFIERS | frozenset((
    "console", "Math", "JSON", "Object", "Array", "Promise", "Number", "String", "Reflect", "process", "Buffer", "window", "document",
    "System", "Integer", "Long", "Double", "Arrays", "Collections", "Objects", "List", "Set", "Map", "Optional", "Stream", "Files",
    "Paths", "Thread", "sys", "re", "logging", "itertools", "functools", "collections", "subprocess", "shutil", "std", "Vec", "Box"))


def _leads(review_input, limit: int) -> list[dict]:
    """Leads of an investigation that offers them, all for its first
    candidate: one per qualified call on a sent code line whose name no sent
    excerpt declares. Calls whose qualifier names a directory of a sent path
    (a package of the project) come first and calls of standard-library
    qualifiers last."""
    scope = review_input.get("scope") if isinstance(review_input, dict) else None
    excerpts = [e for e in (scope or {}).get("excerpts") or [] if isinstance(e, dict) and e.get("kind") == "code"
                and isinstance(e.get("id"), str) and isinstance(e.get("text"), str)]
    declared = {m for e in excerpts for line in e["text"].split("\n") for m in DECLARATION.findall(line)}
    dirs = {d for e in excerpts if isinstance(e.get("path"), str) for d in e["path"].split("/")[:-1]}
    calls = []
    for e in excerpts:
        for line in e["text"].split("\n"):
            numbered = NUMBERED.match(line)
            if not numbered or DECLARATION.search(numbered.group(2)):
                continue
            for qualifier, name in QUALIFIED_CALL.findall(numbered.group(2)):
                if name not in declared:
                    rank = 0 if qualifier in dirs else 2 if qualifier in STANDARD_QUALIFIERS else 1
                    calls.append((rank, name, f"{e['id']}:{numbered.group(1)}-{numbered.group(1)}"))
    out: list[dict] = []
    for _, name, ref in sorted(calls, key=lambda c: c[0]):
        if len(out) == limit:
            break
        if not any(x["symbol"] == name for x in out):
            out.append({"need": "definition", "evidence_id": ref, "symbol": name, "suspicion": TEXT, "candidate": 1})
    return out


def _project_leads(review_input, limit: int) -> list[dict]:
    """Leads of --leads=project, all for the first candidate: one per call
    on a sent code line, qualified or not, whose name no sent excerpt
    declares and that is no builtin, keyword or standard namespace call.
    Calls whose qualifier names a directory of a sent path come first, then
    unqualified calls, then calls on another receiver."""
    scope = review_input.get("scope") if isinstance(review_input, dict) else None
    excerpts = [e for e in (scope or {}).get("excerpts") or [] if isinstance(e, dict) and e.get("kind") == "code"
                and isinstance(e.get("id"), str) and isinstance(e.get("text"), str)]
    declared = set()
    for e in excerpts:
        for line in e["text"].split("\n"):
            numbered = NUMBERED.match(line)
            body = numbered.group(2) if numbered else line
            declared.update(KEYWORD_DECLARATION.findall(body))
            declared.update(TYPE_DECLARATION.findall(body))
            head = HEAD_DECLARATION.match(body) or METHOD_HEAD.match(body)
            if head and body.split()[0] not in CONTROL and head.group(1) not in CONTROL:
                declared.add(head.group(1))
    dirs = {d for e in excerpts if isinstance(e.get("path"), str) for d in e["path"].split("/")[:-1]}
    calls = []
    for e in excerpts:
        for line in e["text"].split("\n"):
            numbered = NUMBERED.match(line)
            if not numbered or COMMENT.match(numbered.group(2)):
                continue
            body = numbered.group(2)
            for m in ANY_CALL.finditer(body):
                qualifier, name = m.group(1) or "", m.group(2)
                # A constructor call, new Name(), names a type, not a function.
                if name in declared or name in NOT_PROJECT or qualifier in STANDARD_NAMESPACES or re.search(r"\bnew\s+$", body[:m.start()]):
                    continue
                rank = 1 if not qualifier else 0 if qualifier in dirs else 2
                calls.append((rank, name, f"{e['id']}:{numbered.group(1)}-{numbered.group(1)}"))
    out: list[dict] = []
    for _, name, ref in sorted(calls, key=lambda c: c[0]):
        if len(out) == limit:
            break
        if not any(x["symbol"] == name for x in out):
            out.append({"need": "definition", "evidence_id": ref, "symbol": name, "suspicion": TEXT, "candidate": 1})
    return out


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


def _pick(rate: float, role: str, review_input, item) -> bool:
    """Whether the item gets an injected fault: a deterministic share rate of
    the items, keyed by role, snapshot digest and item."""
    if rate <= 0:
        return False
    snapshot = ""
    if isinstance(review_input, dict):
        scope = review_input.get("scope")
        snapshot = str((scope if isinstance(scope, dict) else review_input).get("snapshot_manifest_digest", ""))
    digest = hashlib.sha256(f"{role}\x00{snapshot}\x00{item}".encode()).digest()
    return int.from_bytes(digest[:8], "big") < rate * 2**64


def _holder(review_input, ident: str) -> str | None:
    """The id of a code excerpt longer than CITED_LINES that the request sends
    and that holds the excerpt ident's lines: citing it alone is the fault
    citation_range_missing."""
    excerpts = []
    scope = review_input.get("scope") if isinstance(review_input, dict) else None
    if isinstance(scope, dict):
        excerpts = [e for e in scope.get("excerpts") or [] if isinstance(e, dict)]
    inner = next((e for e in excerpts if e.get("id") == ident), None)
    if inner is None:
        return None
    for e in excerpts:
        if (e.get("id") != ident and e.get("path") == inner.get("path") and e.get("origin") != "cited"
                and isinstance(e.get("start_line"), int) and isinstance(e.get("end_line"), int)
                and e["end_line"] - e["start_line"] + 1 > CITED_LINES
                and e["start_line"] <= inner.get("start_line", 0) and inner.get("end_line", 0) <= e["end_line"]):
            return e["id"]
    return None


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


def result_for(role: str, review_input, echo: str | None = None, shape: float = 0.0, integrity: float = 0.0, leads: bool | str = False) -> dict:
    """The double's answer to one role request. leads is the --leads mode:
    "qualified" (or True), "project", or off (False, None, "")."""
    evidence: dict[str, str] = {}
    cited: dict[str, str] = {}
    classes: dict[str, str] = {}
    _evidence(review_input, evidence, cited, classes)
    code = [cited[i] for i, k in evidence.items() if k == "code" and i not in classes] or [cited[i] for i, k in evidence.items() if k == "code"]
    aside = [cited[i] for i, k in evidence.items() if k == "code" and i in classes]
    title = "Placeholder candidate (dry run)" + (": " + echo if echo else "")
    candidate = {"title": title, "condition": TEXT, "mechanism": TEXT, "impact": TEXT,
                 "improvement": TEXT, "counterpoint": TEXT, "evidence_ids": code[:1]}
    if role == "probe":  # codeye model probe: {"a": int, "b": int} -> {"sum": a + b}
        data = review_input if isinstance(review_input, dict) else {}
        a, b = data.get("a"), data.get("b")
        return {"sum": a + b} if isinstance(a, int) and isinstance(b, int) else {}
    if role == "investigate":
        data = review_input if isinstance(review_input, dict) else {}
        if code and _pick(shape, role, review_input, 0):
            candidate["evidence_ids"] = code[:1] * 2  # evidence_id_duplicate
        out = {"candidates": [candidate] if code else []}
        if code and aside and code[0] not in aside:  # a candidate about a test or example: not verified
            out["candidates"].append(dict(candidate, title="Placeholder candidate about a test path (dry run)", evidence_ids=aside[:1]))
        if isinstance(data.get("scope"), dict) and data["scope"].get("whole_run") is True:
            comparisons = _comparisons(data)
            if comparisons:
                out["contract_comparisons"] = comparisons
        limit = data["scope"].get("lead_limit") if isinstance(data.get("scope"), dict) else None
        if leads and isinstance(limit, int) and limit > 0 and out["candidates"]:
            named = _project_leads(data, limit) if leads == "project" else _leads(data, limit)
            if named:
                out["leads"] = named
        return out
    if role == "leads":
        data = review_input if isinstance(review_input, dict) else {}
        results, bases = [], set()
        originals = {c["id"]: [i for i in c.get("evidence_ids") or [] if isinstance(i, str)]
                     for c in data.get("candidates") or [] if isinstance(c, dict) and isinstance(c.get("id"), str)}
        for lead in data.get("leads") or []:
            if not isinstance(lead, dict) or not isinstance(lead.get("id"), str) or not isinstance(lead.get("evidence_id"), str):
                continue
            # The first lead of a candidate refines it; any other lead adds.
            basis = lead.get("candidate_id")
            if not isinstance(basis, str) or basis in bases:
                basis = lead["id"]
            ids = [i for i in lead.get("resolved_evidence_ids") or [] if isinstance(i, str)]
            partial = set(i for i in lead.get("partial_evidence_ids") or [] if isinstance(i, str))
            if basis in bases or not ids:
                continue
            bases.add(basis)
            line = cited.get(lead["evidence_id"], lead["evidence_id"])
            if basis == lead["id"]:
                evidence = [line, cited.get(ids[0], ids[0])]
            else:
                # A refinement keeps the candidate's citations and rests on a
                # whole resolved excerpt; without one this lead gets no result.
                whole = [i for i in ids if i not in partial]
                if not whole:
                    continue
                evidence = list(dict.fromkeys(originals.get(basis, []) + [line, cited.get(whole[0], whole[0])]))
                if len(evidence) > 6:
                    continue
            result = dict(candidate, title="Placeholder candidate after a lead (dry run)", basis=basis,
                          disposition="added" if basis == lead["id"] else "refined", evidence_ids=evidence)
            results.append(result)
            if _pick(shape, role, review_input, basis):
                results.append(dict(result))  # lead_result_duplicate
        return {"results": results}
    if role == "verify":
        found: list[tuple[str, list[str]]] = []
        _candidates(review_input, found)
        out = []
        for ident, refs in found:
            cite = refs[:1]
            if cite and _pick(integrity, role + ".integrity", review_input, ident):
                cite = ["ev_" + "0" * 24]  # evidence_id_unknown
            elif cite and _pick(shape, role, review_input, ident):
                holder = _holder(review_input, cite[0])
                cite = [holder] if holder else cite * 2  # citation_range_missing, else evidence_id_duplicate
            out.append({"candidate_id": ident, "verdict": "supported", "category": "correctness", "severity": "low",
                        "severity_reason": TEXT, "impact": TEXT, "valid_conditions": [TEXT], "invalid_conditions": [],
                        "supporting_evidence_ids": cite, "counterevidence_ids": [], "unresolved_assumptions": [],
                        "next_verification": [TEXT]})
        return {"verifications": out}
    if role == "documents":
        data = review_input if isinstance(review_input, dict) else {}
        docs = [d for d in data.get("documents") or [] if isinstance(d, dict)
                and isinstance(d.get("evidence_id"), str) and isinstance(d.get("applies_to"), str)]
        docs.sort(key=lambda d: not (d.get("public_api") or d.get("directive_path")))
        contracts = [{"statement": TEXT, "applies_to": d["applies_to"], "evidence_ids": [d["evidence_id"]]} for d in docs[:3]]
        for index, contract in enumerate(contracts):
            if _pick(shape, role, review_input, index):
                contract["evidence_ids"] = contract["evidence_ids"] * 2  # evidence_id_duplicate
        return {"contracts": contracts, "conflicts": [], "questions": [], "instruction_notes": []}
    if role == "environment":  # one note per gap module, citing that module's requirements
        data = review_input if isinstance(review_input, dict) else {}
        reqs = [r for r in data.get("requirements") or [] if isinstance(r, dict)]
        notes = []
        for module in data.get("gap_modules") or []:
            own = [r for r in reqs if r.get("module") == module]
            repeat = 2 if _pick(shape, role, review_input, module) else 1  # requirement_id_duplicate
            notes.append({"module": module, "requirement_ids": [r.get("id") for r in own if r.get("id")] * repeat,
                          "evidence_ids": sorted({r.get("evidence_id") for r in own if r.get("evidence_id")}),
                          "affected_checks": TEXT, "still_checkable": TEXT, "needed_observation": TEXT})
        return {"notes": notes}
    if role == "triage":
        data = review_input if isinstance(review_input, dict) else {}
        groups = [g for g in data.get("groups") or [] if isinstance(g, dict) and isinstance(g.get("id"), str)]
        out, unknown = [], False
        texts: dict[str, str] = {}
        _texts(review_input, texts)
        for index, g in enumerate(groups):
            context = [cited.get(i, i) for i in g.get("context_evidence_ids") or [] if isinstance(i, str)]
            kind = ("problem", "harmless", "unclear")[index % 3]
            answer = {"group_id": g["id"], "classification": kind, "reason": TEXT}
            if kind == "problem":
                answer.update(title="Placeholder triage problem (dry run)", condition=TEXT, mechanism=TEXT, impact=TEXT, improvement=TEXT)
            if kind != "unclear":
                answer["evidence_ids"] = context[:1]
            else:
                answer["missing"] = [{"kind": "other", "name": ""}]
            count = g.get("diagnostics") if isinstance(g.get("diagnostics"), int) else 0
            parts = kind == "harmless" and count >= 2
            if kind != "unclear" and _pick(integrity, role + ".integrity", review_input, g["id"]):
                answer["evidence_ids"] = ["ev_" + "0" * 24]  # evidence_id_unknown
                unknown = True
            elif _pick(shape, role, review_input, g["id"]):
                functions = FUNCTION.findall(texts.get((g.get("context_evidence_ids") or [""])[0], ""))
                if kind == "unclear" and functions and (index // 3) % 2 == 1:
                    answer["missing"] = [{"kind": "definition", "name": functions[0]}]  # triage_unclear_input_present
                elif kind == "unclear":
                    answer["classification"] = "maybe"  # triage_classification_invalid
                elif not parts:
                    answer["evidence_ids"] = context[:1] * 2  # evidence_id_duplicate
            if parts:
                # A group answered part by part: the first diagnostic is
                # harmless, the others unclear.
                rest = list(range(2, count + 1))
                if _pick(shape, role, review_input, g["id"]):
                    rest = [1] + rest  # triage_diagnostics_invalid
                answer["positions"] = [1]
                out.append(answer)
                answer = {"group_id": g["id"], "classification": "unclear", "reason": TEXT, "positions": rest,
                          "missing": [{"kind": "other", "name": ""}]}
            out.append(answer)
        if unknown:
            out.append({"group_id": "g65", "classification": "unclear", "reason": TEXT})  # triage_group_unknown
        return {"triage": out}
    if role == "interpret":  # at least one item; a limit for the first observation
        data = review_input if isinstance(review_input, dict) else {}
        ids = [o.get("id") for o in data.get("observations") or [] if isinstance(o, dict) and o.get("id")]
        repeat = 2 if _pick(shape, role, review_input, 0) else 1  # observation_id_duplicate
        return {"hypotheses": [], "limits": [{"observation_ids": ids[:1] * repeat, "statement": TEXT}] if ids else []}
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


def reply_text(payload: dict, echo: str | None = None, shape: float = 0.0, integrity: float = 0.0, leads: bool | str = False) -> tuple[str, str]:
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
    return role, json.dumps({"workspace_proof": proof, "result": result_for(role, review_input, echo, shape, integrity, leads)},
                            ensure_ascii=False)


def request_delay(payload: dict, delay: float, jitter: float, seed: int) -> float:
    """The seconds a request is answered late: delay, plus a share of jitter
    drawn from the seed and the request's review input (the whole payload
    when it has none), so the same request waits as long under any arrival
    order."""
    if jitter <= 0:
        return delay
    texts = [content_text(m.get("content", "")) for m in payload.get("input", []) if isinstance(m, dict)]
    found = next((m.group(1) for m in map(REVIEW_INPUT.search, texts) if m), None)
    key = found if found is not None else json.dumps(payload, sort_keys=True)
    digest = hashlib.sha256(f"{seed}\x00{key}".encode()).digest()
    return delay + jitter * int.from_bytes(digest[:8], "big") / 2**64


class FakeUpstream:
    def __init__(self, mode: str, dump: Path | None, echo: str | None = None, delay: float = 0.0,
                 shape: float = 0.0, integrity: float = 0.0, jitter: float = 0.0, seed: int = 0, leads: bool | str = False):
        self.mode = mode
        self.leads = leads
        self.shape = shape
        self.integrity = integrity
        self.delay = delay
        self.jitter = jitter
        self.seed = seed
        self.dump = dump
        self.echo = echo
        self.calls = 0
        self.in_flight = 0
        self.peak = 0
        self.lock = threading.Lock()

    def respond(self, payload: dict) -> tuple[int, dict]:
        with self.lock:
            self.calls += 1
            number = self.calls
            self.in_flight += 1
            self.peak = max(self.peak, self.in_flight)
        try:
            return self._respond(payload, number)
        finally:
            with self.lock:
                self.in_flight -= 1

    def _respond(self, payload: dict, number: int) -> tuple[int, dict]:
        if self.dump is not None:
            (self.dump / f"{number:04d}.json").write_text(json.dumps(payload, ensure_ascii=False, indent=1))
        wait = request_delay(payload, self.delay, self.jitter, self.seed)
        if wait > 0:
            time.sleep(wait)
        if self.mode == "fail":
            return 500, {"error": {"message": "fake failure", "type": "server_error", "code": "fake_failure"}}
        if self.mode == "unauthorized":
            return 401, {"error": {"message": "fake: invalid api key", "type": "invalid_request_error", "code": "invalid_api_key"}}
        role, text = reply_text(payload, self.echo, self.shape, self.integrity, self.leads)
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
    parser.add_argument("--leads", nargs="?", const="qualified", choices=("qualified", "project"), default=None, metavar="MODE",
                        help="name leads when an investigation offers them and answer their follow-up: qualified (the default "
                             "of a bare --leads) names qualified calls only, project any undeclared project call")
    parser.add_argument("--delay-seconds", type=float, default=0.0, help="answer every request this many seconds late")
    parser.add_argument("--delay-jitter", type=float, default=0.0, metavar="SECONDS",
                        help="add a seeded delay between 0 and SECONDS per request (see --seed)")
    parser.add_argument("--seed", type=int, default=0, help="seed of --delay-jitter")
    parser.add_argument("--shape-errors", type=float, default=0.0, metavar="RATE",
                        help="share (0-1) of items answered with a fault of form on every call")
    parser.add_argument("--integrity-errors", type=float, default=0.0, metavar="RATE",
                        help="share (0-1) of verdicts citing an evidence id the request did not hold")
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
    if not (0 <= args.delay_seconds <= 3600 and 0 <= args.delay_jitter <= 3600):
        print("--delay-seconds and --delay-jitter are between 0 and 3600", file=sys.stderr)
        return 2
    if not (0 <= args.shape_errors <= 1 and 0 <= args.integrity_errors <= 1):
        print("--shape-errors and --integrity-errors are between 0 and 1", file=sys.stderr)
        return 2
    fake = FakeUpstream(args.mode, args.dump, args.echo_text, args.delay_seconds, args.shape_errors, args.integrity_errors,
                        args.delay_jitter, args.seed, args.leads)
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
        print(json.dumps({"event": "stopped", "calls": fake.calls, "peak_in_flight": fake.peak, "at": time.time()}),
              file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
