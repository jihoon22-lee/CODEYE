#!/usr/bin/env python3
"""Quality evaluation runner (docs/EVALUATION.md).

Subcommands:
  plan        stratified selection and call/cost estimate; no product run
  run         run one batch: --condition tools (analyzers only, no model) or
              --condition review (the full pipeline with the review instructions)
  score       judged metrics with Wilson intervals, side by side over batches (quality_score.py)
  allow-text  tool messages of a tools batch and instruction envelopes of a capture dry run,
              for an explicit `corpus.py forbidden --allow-text`
  capture-list  sentinel answer list (blocks nothing) for that capture dry run; test upstream only
  blind       blind judging packet of one batch (opaque unit and finding ids) and a separate answer key
  blind-merge   judged packet + answer key -> a judgments file for `score`
  reader      report-reading task sheets and an evaluator answer key (docs/EVALUATION.md#보고서-읽기-평가)
  reader-score  score supplied human answers; without answers the result is "not_measured"

Corpora (--corpus): quality-holdout (default; small synthetic cases) and project-defects (real-fix and
seeded defects in pinned public repositories; --split dev|holdout and --cache DIR are required, and
every run is limited to the case's scope path). --build-root builds the product under test from
another checkout while this runner and corpus stay as checked out here.

The runner never reads a model key and never starts a relay. A review batch
uses a relay the coordinator started (`--relay-file`, `--ledger`); the runner
only reads that ledger (`--report`, read-only JSONL) and refuses a batch whose
upper estimate exceeds what the ledger has left minus a reserve. Dry runs use a
relay started with `--test-upstream` (ledger upstream `test-loopback`); their
results are labelled and never count as evaluation.

Labels stay outside the analysed snapshots: inputs are materialized by the
corpus module's materialize into opaque directories; results, judgment
templates and answer keys live under --results-root.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import platform
import random
import runpy
import shutil
import subprocess
import sys
import tempfile
import time
from uuid import uuid4

SCRIPTS = Path(__file__).resolve().parent
ROOT = SCRIPTS.parent
sys.path.insert(0, str(SCRIPTS))

CORPUS_DIR = ROOT / "fixtures/quality-holdout"
GATEWAY = SCRIPTS / "model_budget_relay.py"
PACK = "review"
BASE_REVIEW_ROLES = ("environment", "documents", "investigate", "interpret", "verify")
# Diagnostic triage slots (docs/REVIEW.md#도구-진단-분류) record their calls as
# triage and verify; they are not an original role of the slice check below.
PACK_ROLES = (*BASE_REVIEW_ROLES, "triage", "contract")
MANIFEST_SCHEMA = "codeye-quality-eval-manifest"
REGISTRY = "holdout-registry.jsonl"
HOLDOUT_LABEL = "holdout_seen_by_prior_revision"
BUDGET_REFUSALS = ("budget_exhausted", "request_limit_reached", "relay_halted")
# Relay settlement prices (microUSD per token; model_budget_relay.PRICE_INPUT_WORST / PRICE_OUTPUT).
PRICE_IN, PRICE_OUT = 5, 10

# Planner fallback when no history directory is given (docs/EVALUATION.md#표본과-예산). Source: docs/EVALUATION.md#회귀-집합-실행,
# TC-100 on the Go pairs and the module input (11 inputs, single-file Go excerpts, reasoning low).
CONSTANTS = {
    "source": "docs/EVALUATION.md#회귀-집합-실행 TC-100: 11 inputs, 27 calls, ledger 0.598 USD",
    "calls_per_variant": {"environment": 1 / 11, "documents": 7 / 11, "investigate": 11 / 11,
                          "interpret": 0 / 11, "verify": 8 / 11},
    "ledger_microusd_per_call": 22_148, "input_tokens_per_call": 3_872, "output_tokens_per_call": 279,
}
DEFAULTS = {"margin": 1.5, "repair_rate": 0.1, "reserve_usd": "1.00", "request_reserve": 5,
            "bytes_per_token": 3.0, "output_tokens_upper": 600, "review_max_calls": 99}


def now():
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, separators=(",", ":"))


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=1, sort_keys=True) + "\n", encoding="utf-8")


# ---------------------------------------------------------------- corpus

CORPORA = {"quality-holdout": CORPUS_DIR, "project-defects": ROOT / "fixtures/project-defects"}
PROJECT_CORPUS = "project-defects"


class Corpus:
    """A corpus module loaded in-process (stdlib only).

    quality-holdout: small synthetic multi-language cases (defect pairs and tricky normals), rendered from
    corpus.json. project-defects: real-fix and seeded defects in pinned public repositories; a split is
    required, inputs are fetched into `cache`, and every run is limited to the case's scope path."""

    def __init__(self, name="quality-holdout", split=None, cache=None):
        if name not in CORPORA:
            raise SystemExit(f"unknown corpus {name!r} (choose from {', '.join(CORPORA)})")
        self.name = name
        self.project = name == PROJECT_CORPUS
        self.module = runpy.run_path(str(CORPORA[name] / "corpus.py"))
        if self.project:
            if split not in self.module["SPLITS"]:
                raise SystemExit("--corpus project-defects needs --split " + "|".join(self.module["SPLITS"]))
            self.cases = self.module["load_cases"](split)
        else:
            if split not in (None, "holdout"):
                raise SystemExit("the quality-holdout corpus has only the holdout split")
            self.cases = self.module["load_cases"]()
        self.split = split
        self.cache = Path(cache) if cache else None
        self.by_id = {c["id"]: c for c in self.cases}
        self.digest = self.module["corpus_digest"](self.cases)

    def describe(self):
        return {"name": self.name, "split": self.split, "digest": self.digest, "cases": len(self.cases)}

    def variants(self, case):
        return self.module["variants"](case)

    def render(self, case, variant, documented=False):
        if self.project:
            raise ValueError("project-defects variants are whole repository trees; use materialize")
        return self.module["render"](case, variant, documented)

    def materialize(self, destination, case, variant, documented):
        if self.project:
            if self.cache is None:
                raise SystemExit("--corpus project-defects needs --cache DIR (fetched repositories)")
            return self.module["materialize"](destination, case, variant, self.cache)
        return self.module["materialize"](destination, case, variant, documented)

    def documented(self, case):
        """README per case, fixed by corpus order (every other case, as in the regression pilot), the same for every batch.
        Project cases are analysed as published: their own README, nothing added."""
        if self.project:
            return False
        return self.cases.index(case) % 2 == 0

    def scope(self, case):
        """Run scope paths (None: the whole snapshot)."""
        if not self.project:
            return None
        return self.module["scope_paths"](case) or None

    def targets(self, case, variant):
        """Label locations of a project case; None for the synthetic corpus (quality_score renders it)."""
        return self.module["targets"](case, variant) if self.project else None

    def stratum(self, case):
        return stratum(case)


def stratum(case):
    return (case["language"], case["kind"], case.get("detection") or "-")


def is_pair(case):
    """Every case kind except the tricky normals has a defect in before and its correction in after."""
    return case["kind"] != "tricky_normal"


def case_facts(case):
    """Row fields shared by both corpora."""
    return {"language": case["language"], "kind": case["kind"], "category": case["category"],
            "detection": case.get("detection"), "cross_file": case.get("cross_file", case.get("needs_cross_file"))}


def select(corpus, *, cases=None, languages=None, kinds=None, variants=None, sample=None, seed=0):
    """Return (selected cases, selection record). Stratified sampling over language x kind x detection
    (project cases have no detection field: language x kind).

    With --sample N, every stratum first gets floor(N * share) cases (largest remainder for the rest,
    at least one per stratum while N allows), then cases are drawn per stratum with random.Random(seed).
    """
    pool = [c for c in corpus.cases
            if (not cases or c["id"] in cases) and (not languages or c["language"] in languages)
            and (not kinds or c["kind"] in kinds)]
    if cases:
        unknown = sorted(set(cases) - {c["id"] for c in corpus.cases})
        if unknown:
            raise SystemExit("unknown case ids: " + ", ".join(unknown))
    record = {"seed": seed, "sample": sample, "cases": sorted(cases) if cases else None,
              "languages": sorted(languages) if languages else None, "kinds": sorted(kinds) if kinds else None,
              "variants": sorted(variants) if variants else None, "strata": {}}
    groups = defaultdict(list)
    for c in pool:
        groups[stratum(c)].append(c)
    keys = sorted(groups)
    if sample is None or sample >= len(pool):
        chosen = pool
    else:
        if sample <= 0:
            raise SystemExit("--sample must be positive")
        rng = random.Random(seed)
        total = len(pool)
        quota = {k: (sample * len(groups[k])) // total for k in keys}
        if sample >= len(keys):
            for k in keys:
                quota[k] = max(quota[k], 1)
        while sum(quota.values()) > sample:  # the minimum-one rule overshot: trim the largest strata
            k = max(keys, key=lambda k: (quota[k], -keys.index(k)))
            quota[k] -= 1
        remainders = sorted(keys, key=lambda k: (-((sample * len(groups[k])) % total), k))
        for k in remainders:
            if sum(quota.values()) >= sample:
                break
            if quota[k] < len(groups[k]):
                quota[k] += 1
        chosen = []
        for k in keys:
            members = sorted(groups[k], key=lambda c: c["id"])
            rng.shuffle(members)
            chosen.extend(members[:quota[k]])
        chosen.sort(key=lambda c: corpus.cases.index(c))
    for k in keys:
        record["strata"]["/".join(k)] = {"available": len(groups[k]),
                                         "selected": sum(stratum(c) == k for c in chosen)}
    return chosen, record


def variant_plan(corpus, chosen, variants=None, repeat=1, repeat_cases=None):
    """[(case, variant, repeat index)] in corpus order."""
    out = []
    for case in chosen:
        for variant in corpus.variants(case):
            if variants and variant not in variants:
                continue
            n = repeat if (repeat_cases is None or case["id"] in repeat_cases) else 1
            for r in range(1, n + 1):
                out.append((case, variant, r))
    return out


def key_of(case_id, variant, repeat):
    return f"{case_id}.{variant}.{repeat}"


# ---------------------------------------------------------------- pack, registry

def pack_tree(root=ROOT):
    """Identity of the review pack as checked out under root (the checkout the product is built from):
    id, version, status and a tree digest (sha256 over every file, sorted, path NUL content NUL)."""
    directory = Path(root) / "prompts" / PACK
    digest = hashlib.sha256()
    for path in sorted(p for p in directory.rglob("*") if p.is_file()):
        rel = path.relative_to(directory).as_posix()
        digest.update(rel.encode() + b"\0" + path.read_bytes() + b"\0")
    meta = {}
    for line in (directory / "pack.yaml").read_text(encoding="utf-8").splitlines():
        if ":" in line and not line.startswith((" ", "\t", "#")):
            key, _, value = line.partition(":")
            if key in ("id", "version", "status", "schema_version"):
                meta[key] = value.strip().strip("'\"")
    return {"id": meta.get("id", PACK), "version": meta.get("version"), "status": meta.get("status"),
            "tree_digest": digest.hexdigest()}


def checkout_state(root=ROOT):
    try:
        commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True, timeout=30,
                                         stderr=subprocess.DEVNULL).strip()
        dirty = bool(subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=no"], cwd=root,
                                             text=True, timeout=30).strip())
    except (OSError, subprocess.SubprocessError):
        return {"commit": None, "dirty": None}
    return {"commit": commit, "dirty": dirty}


def registry_entries(results_root):
    path = Path(results_root) / REGISTRY
    if not path.exists():
        return []
    out = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            out.append(json.loads(line))
    return out


def holdout_labels(entries, pack_id, pack_digest, corpus_digest):
    """Prior *real* (non-dry-run) revisions of the pack seen on this corpus."""
    prior = sorted({e["pack_digest"] for e in entries
                    if e["pack_id"] == pack_id and e["corpus_digest"] == corpus_digest
                    and e["pack_digest"] != pack_digest and not e.get("dry_run")})
    return ([HOLDOUT_LABEL] if prior else []), prior


def register(results_root, entry):
    """Append (pack id, pack digest, corpus digest, condition) once, with its first run time. Append-only."""
    entries = registry_entries(results_root)
    key = ("pack_id", "pack_digest", "corpus_digest", "condition", "dry_run", "agent_budget")
    if any(all(e.get(k) == entry.get(k) for k in key) for e in entries):
        return False
    path = Path(results_root) / REGISTRY
    with open(path, "a", encoding="utf-8") as handle:
        handle.write(canonical(entry) + "\n")
        handle.flush()
        os.fsync(handle.fileno())
    return True


# ---------------------------------------------------------------- relay ledger (read only)

def ledger_report(ledger):
    """The relay's own --report (no key needed)."""
    out = subprocess.run([sys.executable, str(GATEWAY), "--ledger", str(ledger), "--report"],
                         capture_output=True, text=True, timeout=60, env={"PATH": os.environ.get("PATH", ""),
                                                                          "LANG": "C.UTF-8"})
    if out.returncode:
        raise SystemExit("relay --report failed: " + out.stderr.strip()[-300:])
    return json.loads(out.stdout)


def ledger_records(ledger, start=0):
    """Ledger records from index start (complete lines only; never writes)."""
    data = Path(ledger).read_bytes()
    lines = data.split(b"\n")
    if data and not data.endswith(b"\n"):
        lines = lines[:-1]  # a write in progress
    records = [json.loads(line) for line in lines if line.strip()]
    return records[start:]


def ledger_delta(records):
    """Summarize ledger records appended while one run was in flight."""
    reserves = {r["id"]: r for r in records if r.get("type") == "reserve"}
    settles = {r["id"]: r for r in records if r.get("type") == "settle"}
    requests = []
    for rid, r in reserves.items():
        s = settles.get(rid)
        requests.append({"role": r.get("role_hint"), "input_bytes": r.get("input_bytes"),
                         "message_count": r.get("message_count"),
                         "charged_microusd": s.get("charged_microusd") if s else r.get("reserved_microusd"),
                         "status": s.get("status") if s else "pending", "usage": (s or {}).get("usage"),
                         **({"echo_matches": r["echo_matches"], "echo_match_count": r.get("echo_match_count")}
                            if r.get("echo_matches") else {})})
    blocked = [{"role": r.get("role_hint"), "matches": r.get("matches", []), "match_count": r.get("match_count")}
               for r in records if r.get("type") == "blocked"]
    refused = [r.get("code") for r in records if r.get("type") == "refused"]
    return {"requests": requests, "charged_microusd": sum(q["charged_microusd"] or 0 for q in requests),
            "blocked": blocked, "refused": refused, "halts": [r.get("reason") for r in records if r.get("type") == "halt"]}


# ---------------------------------------------------------------- planner

def history_profile(dirs):
    """Per-role calls per variant and request sizes from saved batch dirs of the review pack (rows.jsonl).

    Batches are matched by the pack id of prompts/review. Given directories without a usable row
    of this pack are an error, never a silent constants fallback."""
    if not dirs:
        return None
    pack_id = pack_tree()["id"]
    variants, calls, sizes, usage = 0, Counter(), defaultdict(list), defaultdict(list)
    per_key = {}
    for d in dirs:
        manifest = json.loads((Path(d) / "manifest.json").read_text())
        if (manifest.get("pack") or {}).get("id") != pack_id:
            continue
        for row in read_rows(Path(d)):
            if row.get("invalid"):
                continue
            variants += 1
            roles = Counter()
            for q in (row.get("ledger") or {}).get("requests", []):
                if q.get("role"):
                    calls[q["role"]] += 1
                    roles[q["role"]] += 1
                    sizes[q["role"]].append(q.get("input_bytes") or 0)
                    u = q.get("usage") or {}
                    if not manifest.get("dry_run") and isinstance(u.get("prompt_tokens"), int):
                        usage[q["role"]].append((u["prompt_tokens"], u.get("completion_tokens") or 0))
            per_key[(row["case"], row["variant"])] = roles
    if not variants:
        raise SystemExit(f"--history: no valid rows of pack {pack_id} in " + ", ".join(map(str, dirs)))
    return {"variants": variants, "calls_per_variant": {r: calls[r] / variants for r in calls},
            "input_bytes_max": {r: max(v) for r, v in sizes.items()},
            "input_bytes_mean": {r: sum(v) / len(v) for r, v in sizes.items()},
            "usage_mean": {r: (sum(a for a, _ in v) / len(v), sum(b for _, b in v) / len(v)) for r, v in usage.items()},
            "per_key": per_key, "dirs": [str(d) for d in dirs]}


def estimate(plan, *, history=None, margin=DEFAULTS["margin"], repair_rate=DEFAULTS["repair_rate"],
             max_calls=DEFAULTS["review_max_calls"], slices_unknown=False, budget=None):
    """Calls and conservative ledger cost for a review batch.

    expected: per-role calls of the same variant in history (else history's per-variant average,
    else CONSTANTS) times a per-call cost.
    upper: every original role is called once per variant (unless history shows the exact
    variant needs fewer: never fewer than history's own count), plus repair_rate extra calls,
    and up to two requests for a contract followup.
    each at the per-call cost times margin. Per-call cost: history usage when real, else
    request bytes / bytes_per_token input tokens and output_tokens_upper output tokens at the
    relay's worst-case prices, else CONSTANTS.
    Module slices (docs/REVIEW.md#모듈-단위-검토-분할): a variant whose history made
    more original-role calls than original roles may have planned slices. Slices and final
    contract followup share the per-attempt ceiling. Its upper is max_calls calls at the
    dearest per-call cost of investigate, verify and contract. A variant without history is
    estimated as above (the constants are single-file inputs without slices); run the capture
    dry run with the same selection first. With slices_unknown (project-scale cases) a variant
    without history is treated as one that may plan slices: its upper is the per-attempt ceiling.
    budget (agent_budget) only lowers the per-call input and output assumed when a cost comes from
    history input sizes or constants; usage recorded in history and the number of calls stay as
    measured, so a smaller request budget (more slices, more calls) is not predicted.
    """
    roles = PACK_ROLES
    base = CONSTANTS
    rates = dict(base["calls_per_variant"])
    basis = "constants:" + base["source"]
    per_call = {r: base["ledger_microusd_per_call"] for r in roles}
    if history:
        basis = "history:" + ",".join(history["dirs"])
        rates = {r: history["calls_per_variant"].get(r, 0.0) for r in roles}
        for r in roles:
            if r in history["usage_mean"]:
                i, o = history["usage_mean"][r]
                per_call[r] = int(i * PRICE_IN + o * PRICE_OUT)
            elif r in history["input_bytes_max"]:
                size = min(history["input_bytes_max"][r], (budget or {}).get("review_request_bytes", float("inf")))
                out_tokens = min(DEFAULTS["output_tokens_upper"], (budget or {}).get("review_output_tokens", float("inf")))
                per_call[r] = int(size / DEFAULTS["bytes_per_token"] * PRICE_IN + out_tokens * PRICE_OUT)
    n = len(plan)
    expected_calls = 0.0
    expected_cost = 0.0
    upper_calls = 0.0
    upper_cost = 0.0
    for case, variant, _ in plan:
        seen = (history or {}).get("per_key", {}).get((case["id"], variant))
        # expected: the exact variant's history when recorded, else the per-variant average
        counts = {r: seen.get(r, 0) for r in roles} if seen is not None else {r: rates.get(r, 0) for r in roles}
        expected_calls += sum(counts.values())
        expected_cost += sum(counts[r] * per_call[r] for r in roles)
        sliced = (sum(seen.get(r, 0) for r in BASE_REVIEW_ROLES) > len(BASE_REVIEW_ROLES) if seen is not None
                  else slices_unknown)
        if sliced:
            upper_calls += max_calls
            upper_cost += max_calls * max(per_call[r] for r in ("investigate", "verify", "contract")) * margin
            continue
        for r in BASE_REVIEW_ROLES:
            count = max(1, seen.get(r, 0)) if seen is not None else 1
            upper_calls += count * (1 + repair_rate)
            upper_cost += count * (1 + repair_rate) * per_call[r] * margin
        # A triage slot classifies and verifies its problem groups: each
        # triage call may be followed by one verify call.
        count = max(1, seen.get("triage", 0)) if seen is not None else 1
        upper_calls += 2 * count * (1 + repair_rate)
        upper_cost += count * (1 + repair_rate) * (per_call["triage"] + per_call["verify"]) * margin
        upper_calls += 2
        upper_cost += 2 * per_call["contract"] * margin
    out = {"variants": n, "basis": basis, "margin": margin, "repair_rate": repair_rate, "max_calls": max_calls,
           "slices_unknown": slices_unknown,
           "per_call_microusd": per_call, "calls_per_variant": rates,
           "expected": {"calls": round(expected_calls, 1), "ledger_microusd": int(expected_cost)},
           "upper": {"calls": int(upper_calls + 0.999), "ledger_microusd": int(upper_cost)}}
    if budget:
        out["agent_budget"] = budget
    return out


def budget_gate(est, report, reserve_microusd, request_reserve):
    """Refusal reasons (empty when the batch may start)."""
    reasons = []
    if report.get("halted"):
        reasons.append("relay_halted")
    remaining = report["remaining_microusd"] - reserve_microusd
    if est["upper"]["ledger_microusd"] > remaining:
        reasons.append(f"upper estimate {est['upper']['ledger_microusd']} microUSD exceeds remaining "
                       f"{report['remaining_microusd']} minus reserve {reserve_microusd}")
    left = report["max_requests"] - report["requests"] - request_reserve
    if est["upper"]["calls"] > left:
        reasons.append(f"upper estimate {est['upper']['calls']} requests exceeds the ledger's remaining "
                       f"{report['max_requests'] - report['requests']} minus reserve {request_reserve}")
    return reasons


def parse_usd(text):
    from decimal import Decimal
    value = Decimal(text)
    if value < 0:
        raise SystemExit("negative amount")
    return int(value * 1_000_000)


# ---------------------------------------------------------------- results

def read_rows(batch):
    path = Path(batch) / "rows.jsonl"
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]


def append_row(batch, row):
    with open(Path(batch) / "rows.jsonl", "a", encoding="utf-8") as handle:
        handle.write(canonical(row) + "\n")
        handle.flush()
        os.fsync(handle.fileno())


def records_of(batch, key):
    out = []
    directory = Path(batch) / "records" / key
    if directory.is_dir():
        for f in sorted(directory.glob("*.json")):
            doc = json.loads(f.read_bytes())
            if "stage" in doc and "calls" in doc:
                out.append(doc)
    return out


def review_outcome(report):
    """Why model review did or did not run, from task states only. A failed secret screen (:
    every review role skipped_policy/secret_screen_failed) is a platform policy outcome, not a
    model failure, and is reported as its own value."""
    review = [t for t in report["tasks"] if str(t.get("key", "")).startswith("review.")]
    reasons = {t.get("reason") for t in review}
    if any(t.get("status") == "skipped_policy" and t.get("reason") == "secret_screen_failed" for t in review):
        return "secret_screen_failed"
    if report["coverage"]["ai_review"]["status"] == "performed":
        return "performed"
    for reason in ("model_source_not_approved", "review_input_unavailable"):
        if reason in reasons:
            return reason
    return "not_performed:" + str(report["coverage"]["ai_review"].get("reason") or "unknown")


def review_invalid(current, delta, outcome):
    """Why a review-condition variant is excluded from the metrics, in precedence order. A failed
    secret screen means the model never ran: counting the variant would score a policy outcome as a
    model miss, so it is excluded and counted under its own reason."""
    if current:
        return current  # source_changed is kept over any later reason
    if delta["blocked"]:
        return "blocked_answer_leak"
    if set(delta["refused"]) & set(BUDGET_REFUSALS):
        return "budget_refused"
    if delta.get("echo_unverified"):
        return "answer_echo_unverified"
    if outcome == "secret_screen_failed":
        return "secret_screen_failed"
    return None


def summarize_report(report, records):
    """Structured facts of one published report (no LLM narrative is used)."""
    tasks = [{k: t.get(k) for k in ("key", "status", "reason", "required_for_verdict")} for t in report["tasks"]]
    gaps = sorted({t["key"] + ":" + str(t.get("reason")) for t in report["tasks"]
                   if t.get("status") in ("unsupported", "skipped_policy", "failed")})
    calls = report["coverage"].get("review_calls", [])
    usage, seen = Counter(), set()
    for r in records:
        for c in r.get("calls", []):
            key = (c.get("request_sha256"), c.get("attempt"), c.get("role"))
            if key in seen:
                continue
            seen.add(key)
            u = c.get("usage") or {}
            for k in ("input", "output"):
                if isinstance(u.get(k), int):
                    usage[k] += u[k]
    limitations = sorted({m for r in records for m in r.get("limitations", [])})
    tools = [{k: row.get(k) for k in ("task_key", "analyzer", "tool_version", "exit_class", "processed_files",
                                       "files_in_scope")} for row in report["coverage"].get("tools", [])]
    return {"status": report["status"], "quality_verdict": report.get("quality_verdict"),
            "ai_review": report["coverage"]["ai_review"]["status"], "review_outcome": review_outcome(report),
            "summary": report["summary"],
            "llm_exposed_files": report["coverage"].get("llm_exposed_files"),
            "tool_processed_files": report["coverage"].get("tool_processed_files"),
            "tasks": tasks, "gaps": gaps, "tools": tools,
            "review_calls": [{k: c.get(k) for k in ("role", "outcome", "pack_id", "pack_version", "pack_sha256", "model",
                                                    "opencode_version")} for c in calls],
            "calls": len(calls), "usage": dict(usage), "record_limitations": limitations,
            "report_limitations": report.get("limitations", [])}


# ---------------------------------------------------------------- run

def tool_specs(values):
    out = {}
    for value in values or []:
        name, sep, path = value.partition("=")
        if not sep or not name or not os.path.isabs(path):
            raise SystemExit(f"--tool expects NAME=/absolute/path, got {value!r}")
        if not os.path.isfile(path):  # the agent checks the rest (JS entry scripts run under --node)
            raise SystemExit(f"--tool {name}: not a regular file")
        out[name] = path
    return out


def help_flags(binary):
    """Option names in the usage lines of `codeye agent --help` (the agent itself rejects unknown ones)."""
    import re
    text = subprocess.run([str(binary), "agent", "--help"], capture_output=True, text=True, timeout=30).stdout
    usage, inside = [], False
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("codeye "):
            inside = stripped.startswith("codeye agent ")
        elif not stripped.startswith("["):
            inside = False  # the description under the usage lines
        if inside:
            usage.append(line)
    return set(re.findall(r"--([a-z][a-z0-9-]*)", "\n".join(usage)))


# Accepted ranges of codeye agent --review-request-bytes / --review-output-tokens. They mirror
# MinRequestBytes..MaxRequestBytes and MinOutputTokens..MaxOutputTokens in
# internal/worker/opencode/client.go; test_quality_eval fails when those constants change.
REQUEST_BYTES_RANGE = (40000, 1 << 20)
OUTPUT_TOKENS_RANGE = (1536, 65536)


def agent_budget(args):
    """The operator request budget given on the command line as {"review_request_bytes": N,
    "review_output_tokens": N} (only the given ones), or None when neither was given (the product
    default applies and nothing is passed or recorded)."""
    out = {}
    for name, (low, high) in (("review_request_bytes", REQUEST_BYTES_RANGE),
                              ("review_output_tokens", OUTPUT_TOKENS_RANGE)):
        value = getattr(args, name, None)
        if value is None:
            continue
        if not low <= value <= high:
            raise SystemExit(f"--{name.replace('_', '-')} must be {low}..{high}, got {value}")
        out[name] = value
    return out or None


def agent_switches(values):
    """Boolean agent options given as --agent-flag NAME (e.g. python-both-type-checkers)."""
    import re
    out = []
    for value in values or []:
        if not re.fullmatch(r"[a-z][a-z0-9-]{0,63}", value):
            raise SystemExit(f"--agent-flag expects an option name without dashes, got {value!r}")
        out.append(value)
    return sorted(set(out))


def plant_leak(destination, case, phrases_file):
    """Dry run only: put one forbidden label phrase of this case in a comment on line 2 of its
    contract file (inside the excerpt window every role reads)."""
    doc = json.loads(Path(phrases_file).read_text())
    entry = next(e for e in doc["entries"] if case["id"] in e["cases"] and e["mode"] == "words")
    marker = "#" if case["language"] == "python" else "//"
    # project cases: the labelled file, so the comment lands in a file the scope covers
    target = Path(destination) / (case.get("contract_file") or case["label"]["positive_locations"][0]["path"])
    os.chmod(target, 0o644)
    lines = target.read_text(encoding="utf-8").split("\n")
    lines.insert(1, f"{marker} Note: {entry['text']}.")
    target.write_text("\n".join(lines), encoding="utf-8")
    return entry["id"]


def model_texts(records):
    """Every string the model produced in this run's accepted stage records (candidates, facts, notes...)."""
    out = []

    def walk(node):
        if isinstance(node, dict):
            for k, v in node.items():
                if k not in ("id", "evidence_ids", "observation_ids", "candidate_id", "calls", "pack", "upstream",
                             "evidence", "observations"):
                    walk(v)
        elif isinstance(node, list):
            for v in node:
                walk(v)
        elif isinstance(node, str):
            out.append(node)

    for r in records:
        for key in ("candidates", "facts", "verifications", "contracts", "conflicts", "questions",
                    "instruction_notes", "environment_notes", "hypotheses", "limits"):
            walk(r.get(key))
    return out


def classify_blocks(blocks, records, phrases_file):
    """A block whose every matched entry also occurs in the run's own model output is a model echo
    (the model wrote label-like words that a later role would receive); anything else came from the
    request inputs or instructions. Both make the variant invalid; they are reported separately."""
    import model_budget_relay as gateway
    phrases = gateway.load_forbidden(phrases_file)
    echoed = set(phrases.matches(model_texts(records)))
    for b in blocks:
        b["cause"] = "model_echo" if b["matches"] and set(b["matches"]) <= echoed else "input_or_instruction"
    return blocks


def echo_audit(requests, records, phrases_file):
    """--allow-model-echo audit for one variant: exempted ids that do not occur in this variant's
    own model output (the relay exempts ids from any answer it returned in the process; the
    runner runs review variants one at a time and narrows that to the variant). A truncated id
    list cannot be checked and counts as unverified."""
    ids, truncated = set(), False
    for q in requests:
        ids.update(q.get("echo_matches") or [])
        truncated |= (q.get("echo_match_count") or 0) > len(q.get("echo_matches") or [])
    if not ids:
        return []
    import model_budget_relay as gateway
    own = set(gateway.load_forbidden(phrases_file).matches(model_texts(records)))
    return sorted(ids - own) + (["<truncated>"] if truncated else [])


def run_batch(args):
    from eval_harness import Cluster, build_product, file_sha256, home_relative, keep_review_artifacts, tree_digest
    import test_postgres

    corpus = Corpus(args.corpus, args.split, args.cache)
    build_root = Path(args.build_root).resolve()
    if not (build_root / "cmd" / "codeye").is_dir():
        raise SystemExit(f"--build-root {build_root} is not a CODEYE checkout (no cmd/codeye)")
    review = args.condition == "review"
    if not os.path.isabs(args.go) or not os.access(args.go, os.X_OK):
        raise SystemExit("--go must be an absolute executable path")
    tools = tool_specs(args.tool)
    switches = agent_switches(args.agent_flag)
    budget = agent_budget(args)
    if budget and not review:
        raise SystemExit("--review-request-bytes and --review-output-tokens apply to --condition review only")
    results_root = Path(args.results_root).resolve()
    results_root.mkdir(mode=0o700, parents=True, exist_ok=True)
    chosen, selection = select(corpus, cases=set(args.cases.split(",")) if args.cases else None,
                               languages=set(args.languages.split(",")) if args.languages else None,
                               kinds=set(args.kinds.split(",")) if args.kinds else None,
                               variants=set(args.variants.split(",")) if args.variants else None,
                               sample=args.sample, seed=args.seed)
    plan = variant_plan(corpus, chosen, set(args.variants.split(",")) if args.variants else None,
                        args.repeat, set(args.repeat_cases.split(",")) if args.repeat_cases else None)
    if not plan:
        raise SystemExit("no case variant selected")
    stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    name = args.batch or f"{args.condition}-{stamp}"
    batch = results_root / name
    batch.mkdir(mode=0o700)
    pack = pack_tree(build_root) if review else None  # the instructions are compiled into the built binary
    relay, report_before, est, forbidden = None, None, None, None
    upstream = None
    if review:
        relay_info = json.loads(Path(args.relay_file).read_text())
        relay = {"model": relay_info.get("model"), "pid": relay_info.get("pid")}
        if "codeye-gateway/" + str(relay["model"]) != args.model:
            raise SystemExit("--model does not match the relay file's model")
        report_before = ledger_report(args.ledger)
        upstream = report_before.get("upstream", "openai")
        if args.dry_run != (upstream == "test-loopback"):
            raise SystemExit("--dry-run must be given exactly when the relay ledger belongs to the test upstream")
        phrases = Path(args.forbidden_phrases).read_bytes()
        forbidden_doc = json.loads(phrases)
        forbidden = {"sha256": hashlib.sha256(phrases).hexdigest(), "entries": len(forbidden_doc["entries"]),
                     "corpus_digest": forbidden_doc.get("corpus_digest"),
                     "allowed_texts": forbidden_doc.get("allowed_texts", []), "purpose": forbidden_doc.get("purpose")}
        if forbidden["purpose"] is not None and not args.dry_run:
            raise SystemExit("an envelope-capture answer list blocks nothing; it is for dry runs only")
        active = [f for f in report_before.get("forbidden_lists", []) if f.get("pid") == relay["pid"]]
        if not active or active[-1]["sha256"] != forbidden["sha256"] or forbidden["corpus_digest"] != corpus.digest:
            raise SystemExit("answer blocking is not active: the relay process must have loaded this "
                             "--forbidden-phrases file for the current corpus digest")
        forbidden["allow_model_echo"] = bool(active[-1].get("allow_model_echo"))
        history = history_profile([Path(h) for h in args.history or []])
        est = estimate(plan, history=history, margin=args.margin, max_calls=args.review_max_calls,
                       slices_unknown=corpus.project, budget=budget)
        refusal = budget_gate(est, report_before, parse_usd(args.reserve_usd), args.request_reserve)
        write_json(batch / "plan.json", {"estimate": est, "refusal": refusal, "selection": selection})
        if refusal:
            write_json(batch / "manifest.json", {"schema": MANIFEST_SCHEMA, "version": 1, "batch": name,
                                                 "condition": args.condition, "refused": refusal, "estimate": est,
                                                 "started_at": None, "dry_run": args.dry_run})
            raise SystemExit("batch refused before start: " + "; ".join(refusal))
    if args.plant_leak and not args.dry_run:
        raise SystemExit("--plant-leak is a dry-run self-test only")
    entries = registry_entries(results_root)
    if review:
        labels, prior = holdout_labels(entries, pack["id"], pack["tree_digest"], corpus.digest)
        reg_entry = {"pack_id": pack["id"], "pack_version": pack["version"], "pack_digest": pack["tree_digest"]}
    else:
        config = canonical(sorted((k, file_sha256(v)) for k, v in {**tools, "go": args.go}.items()) + switches)
        labels, prior = [], []
        reg_entry = {"pack_id": "analyzers", "pack_version": None, "pack_digest": hashlib.sha256(config.encode()).hexdigest()}
    if budget:
        reg_entry["agent_budget"] = budget
    reg_entry.update(corpus_digest=corpus.digest, condition=args.condition, first_run_at=now(), batch=name,
                     dry_run=bool(args.dry_run))
    manifest = {
        "schema": MANIFEST_SCHEMA, "version": 1, "batch": name, "condition": args.condition,
        "dry_run": bool(args.dry_run), "upstream": upstream, "started_at": now(), "finished_at": None,
        "selection": selection, "variants": [key_of(c["id"], v, r) for c, v, r in plan],
        "corpus": corpus.describe(),
        "pack": pack, "model": ({"id": args.model, "relay_model": relay["model"]} if review else None),
        # checkout: the build root the product binary (and its instructions) came from; runner_checkout:
        # this runner and corpus. They differ when a baseline binary is measured with a newer runner.
        "checkout": checkout_state(build_root), "runner_checkout": checkout_state(ROOT),
        "build_root": home_relative(build_root),
        "os": {"platform": platform.platform(), "python": platform.python_version(), "machine": platform.machine()},
        "analyzers": {"go": {"path": home_relative(args.go), "sha256": file_sha256(args.go)},
                      "tools": {k: {"path": home_relative(v), "sha256": file_sha256(v)} for k, v in sorted(tools.items())},
                      "agent_flags": switches,
                      "repo_code_grant": not args.no_repo_code_grant, "go_mod_cache": bool(args.go_mod_cache),
                      "toolchain_path": args.toolchain_path, "observed": {}},
        "budget": ({"ledger_before": {k: report_before.get(k) for k in (
            "cap_microusd", "charged_microusd_total", "remaining_microusd", "requests", "max_requests", "halted",
            "upstream")}, "reserve_microusd": parse_usd(args.reserve_usd), "request_reserve": args.request_reserve,
            "estimate": est} if review else None),
        "forbidden": forbidden, "holdout": {"labels": labels, "prior_revisions": prior, "registered": False},
        "agents": args.agents, "max_workers": args.max_workers, "repeat": args.repeat,
        "planted_leak": args.plant_leak, "notes": [],
    }
    if budget:
        manifest["agent_budget"] = budget
    if args.dry_run:
        manifest["notes"].append("dry run: fake loopback upstream; results carry no quality information")
    write_json(batch / "manifest.json", manifest)
    manifest["holdout"]["registered"] = register(results_root, reg_entry)
    write_json(batch / "manifest.json", manifest)

    with tempfile.TemporaryDirectory(prefix="codeye-eval-") as tmp:
        base = Path(tmp)
        source = base / "source"
        source.mkdir()
        with test_postgres.temporary_postgres(base) as (url, env, _):
            env = {k: v for k, v in env.items() if k != "OPENAI_API_KEY"}
            env["CODEYE_DATABASE_URL"] = url
            binary = build_product(base, build_root, env)
            flags = help_flags(binary)
            unknown = sorted(t for t in list(tools) + switches if t not in flags)
            if unknown:
                manifest["notes"].append("agent options not listed in the codeye --help agent usage (the agent "
                                         "accepted them if the batch ran): " + ", ".join(unknown))

            def agent_args(root_id):
                out = ["--go", args.go, "--toolchain-path", args.toolchain_path]
                if args.go_mod_cache:
                    out += ["--go-mod-cache", args.go_mod_cache]
                for k, v in sorted(tools.items()):
                    out += ["--" + k, v]
                out += ["--" + s for s in switches]
                if not args.no_repo_code_grant:
                    out += ["--repo-code-source-root", root_id]
                if review:
                    out += ["--model-source-root", root_id, "--opencode", args.opencode, "--model", args.model,
                            "--model-relay-file", args.relay_file,
                            "--review-max-calls", str(args.review_max_calls)]
                    for name, value in (budget or {}).items():
                        out += ["--" + name.replace("_", "-"), str(value)]
                return out

            concurrency = 1 if review else max(1, args.agents * args.max_workers)
            with Cluster(base, binary, env, source, agent_args=agent_args, agents=args.agents,
                         max_workers=args.max_workers, label="evaluation inputs",
                         admin=not args.no_repo_code_grant) as cluster:
                stopped = execute(cluster, corpus, plan, batch, args, review, source, concurrency,
                                  keep_review_artifacts, tree_digest)
    rows = read_rows(batch)
    observed = defaultdict(set)
    for row in rows:
        for t in row.get("tools", []):
            if t.get("tool_version"):  # pack-gap tasks have no tool
                observed[t["analyzer"]].add(str(t["tool_version"]))
    manifest["analyzers"]["observed"] = {k: sorted(v) for k, v in sorted(observed.items())}
    if review:
        digests = defaultdict(set)
        for row in rows:
            for c in row.get("review_calls", []):
                digests[c["role"]].add(c.get("pack_sha256"))
        manifest["pack"]["role_digests_observed"] = {k: sorted(v) for k, v in sorted(digests.items())}
        versions = {c.get("pack_version") for row in rows for c in row.get("review_calls", []) if c.get("pack_version")}
        if versions and pack["version"] not in versions:
            manifest["notes"].append("review records carry pack versions " + ", ".join(sorted(versions)))
        report_after = ledger_report(args.ledger)
        manifest["budget"]["ledger_after"] = {k: report_after.get(k) for k in (
            "charged_microusd_total", "remaining_microusd", "requests", "blocked_answer_leaks", "refused_by_code",
            "halted")}
        manifest["budget"]["batch_delta_microusd"] = (report_after["charged_microusd_total"]
                                                      - report_before["charged_microusd_total"])
        manifest["model"]["opencode_versions"] = sorted({c.get("opencode_version") for row in rows
                                                         for c in row.get("review_calls", []) if c.get("opencode_version")})
    manifest["stopped"] = stopped
    manifest["finished_at"] = now()
    manifest["counts"] = dict(Counter("invalid:" + r["invalid"] if r.get("invalid") else r["status"] for r in rows))
    write_json(batch / "manifest.json", manifest)
    import quality_score
    write_json(batch / "judgments-template.json", quality_score.judgment_template(batch, corpus))
    print(json.dumps({"batch": str(batch), "rows": len(rows), "counts": manifest["counts"], "stopped": stopped},
                     ensure_ascii=False))
    return batch


def execute(cluster, corpus, plan, batch, args, review, source, concurrency, keep_review_artifacts, tree_digest):
    """Submit variants (at most `concurrency` in flight), collect reports, return a stop reason or None."""
    (batch / "reports").mkdir()
    (batch / "records").mkdir()
    queue = list(plan)
    inflight = {}
    stopped = None
    ledger = args.ledger if review else None
    reserve = parse_usd(args.reserve_usd) if review else 0
    est_one = None
    if review:
        est_one = estimate(plan[:1], history=history_profile([Path(h) for h in args.history or []]),
                           margin=args.margin, max_calls=args.review_max_calls,
                           slices_unknown=corpus.project, budget=agent_budget(args))["upper"]
    deadline = time.monotonic() + args.timeout
    while queue or inflight:
        if not cluster.alive():
            raise RuntimeError("controller or agent exited")
        if time.monotonic() > deadline:
            stopped = "batch_timeout"
            break
        while queue and len(inflight) < concurrency and stopped is None:
            if review:
                report = ledger_report(ledger)
                if report.get("halted"):
                    stopped = "relay_halted"
                    break
                if (report["remaining_microusd"] - reserve < est_one["ledger_microusd"]
                        or report["max_requests"] - report["requests"] - args.request_reserve < est_one["calls"]):
                    stopped = "budget_reserve_reached"
                    break
            case, variant, repeat = queue.pop(0)
            rel = uuid4().hex
            documented = corpus.documented(case)
            digest = corpus.materialize(source / rel, case, variant, documented)
            planted = None
            if args.plant_leak and args.plant_leak == f"{case['id']}:{variant}" and repeat == 1:
                planted = plant_leak(source / rel, case, args.forbidden_phrases)
            original = tree_digest(source / rel)
            mark = len(ledger_records(ledger)) if review else None
            started = time.monotonic()
            scope = corpus.scope(case)
            run_id = cluster.submit("eval-" + uuid4().hex[:12], rel, scope)
            inflight[run_id] = {"case": case, "variant": variant, "repeat": repeat, "rel": rel, "digest": digest,
                                "documented": documented, "original": original, "mark": mark, "started": started,
                                "started_at": now(), "planted": planted, "scope": scope}
        if not inflight:
            break
        for run_id in list(inflight):
            run = cluster.run(run_id)
            if run["status"] not in ("completed", "partial", "failed", "cancelled", "recovery_required"):
                continue
            info = inflight.pop(run_id)
            case, variant, repeat = info["case"], info["variant"], info["repeat"]
            key = key_of(case["id"], variant, repeat)
            row = {"key": key, "case": case["id"], "variant": variant, "repeat": repeat, **case_facts(case),
                   "scope_paths": info["scope"], "documented": info["documented"], "input_digest": info["digest"],
                   "source_tree_sha256": info["original"], "run_id": run_id, "snapshot_id": run.get("snapshot_id"),
                   "run_status": run["status"], "started_at": info["started_at"], "finished_at": now(),
                   "wall_s": round(time.monotonic() - info["started"], 1), "invalid": None, "planted_leak": info["planted"]}
            if tree_digest(source / info["rel"]) != info["original"]:
                row["invalid"] = "source_changed"  # kept over any later reason
            report = None
            try:
                report = cluster.report(run_id)
            except Exception as exc:  # a failed run may have no published report; record it as data
                code = getattr(exc, "code", None)
                detail = ""
                try:
                    detail = json.loads(exc.read()).get("error", {}).get("code", "")  # HTTPError body
                except Exception:
                    pass
                row["report_error"] = f"{type(exc).__name__}:{code}:{detail}" if code else type(exc).__name__
            records = []
            if report is not None:
                (batch / "reports" / (key + ".json")).write_text(json.dumps(report, ensure_ascii=False, indent=1) + "\n")
                keep_review_artifacts(report, cluster.work / "artifacts", batch / "records" / key)
                records = records_of(batch, key)
                row.update(summarize_report(report, records))
                row["status"] = report["status"]
                row["manifest_sha256"] = report["inputs"]["manifest_sha256"]
            else:
                row["status"] = run["status"]
            if review:
                delta = ledger_delta(ledger_records(ledger, info["mark"]))
                if delta["blocked"]:
                    classify_blocks(delta["blocked"], records, args.forbidden_phrases)
                unverified = echo_audit(delta["requests"], records, args.forbidden_phrases)
                if unverified:
                    delta["echo_unverified"] = unverified
                row["ledger"] = delta
                if set(delta["refused"]) & set(BUDGET_REFUSALS):
                    stopped = stopped or "relay_refused_budget"
                row["invalid"] = review_invalid(row["invalid"], delta, row.get("review_outcome"))
            if info["planted"]:
                row["planted_leak_blocked"] = any(info["planted"] in b["matches"]
                                                  for b in (row.get("ledger") or {}).get("blocked", []))
                row["invalid"] = "planted_leak" if row["planted_leak_blocked"] else "planted_leak_not_blocked"
            append_row(batch, row)
            print(key, row["status"], row.get("review_outcome"), "findings", (row.get("summary") or {}).get("total_findings"),
                  "invalid=" + str(row["invalid"]), f"{row['wall_s']}s", flush=True)
            shutil.rmtree(source / info["rel"], ignore_errors=True)
        if inflight:
            time.sleep(1)
        if stopped and not inflight:
            break
    if stopped:
        for case, variant, repeat in queue:
            append_row(batch, {"key": key_of(case["id"], variant, repeat), "case": case["id"], "variant": variant,
                               "repeat": repeat, **case_facts(case),
                               "status": "not_run", "invalid": "not_run:" + stopped})
    return stopped


# ---------------------------------------------------------------- allow-text

def allow_text(output, batches=(), dumps=()):
    """Texts that legitimately reach the model, for an explicit `corpus.py forbidden --allow-text`:
    tool messages and rule ids of tools batches, and the instruction envelope of recorded dry-run
    requests (system messages and user text outside <review-input>: OpenCode's prompt, the pack
    instructions and the role contracts). Review inputs themselves are never taken from dumps."""
    import re
    tool, envelope = set(), set()
    for batch in batches:
        for f in sorted((Path(batch) / "reports").glob("*.json")):
            report = json.loads(f.read_bytes())
            for o in report.get("observations", []):
                for k in ("raw_message", "rule_id"):
                    if isinstance(o.get(k), str):
                        tool.add(o[k])
    for d in dumps:
        for f in sorted(Path(d).glob("*.json")):
            payload = json.loads(f.read_bytes())
            for m in payload.get("input", []):
                text = m.get("content", "")
                if m.get("role") == "user":
                    text = re.sub(r"<review-input>\n.*?\n</review-input>", "", text, flags=re.S)
                envelope.add(text)
    lines = sorted(tool) + sorted(envelope)
    Path(output).write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(f"{len(tool)} tool lines, {len(envelope)} instruction envelopes -> {output}")


def capture_list(output, corpus):
    """Sentinel answer list for a dry run that records instruction envelopes (`--request-dump` of
    the fake upstream): it blocks nothing, so every role's request reaches the dump. The relay and
    `run` accept it only with the test upstream."""
    import model_budget_relay as gateway
    doc = {"schema": gateway.FORBIDDEN_SCHEMA, "version": gateway.FORBIDDEN_VERSION,
           "normalization": gateway.FORBIDDEN_NORMALIZATION, "purpose": gateway.FORBIDDEN_CAPTURE,
           "corpus_digest": corpus.digest,
           "entries": [{"id": "capture/sentinel", "mode": "words", "text": "zq capture sentinel zq", "cases": [],
                        "source": "sentinel"}]}
    path = Path(output)
    with open(path, "x", encoding="utf-8") as f:
        f.write(json.dumps(doc, indent=1) + "\n")
    print(f"envelope-capture list -> {path} (dry runs only)")


# ---------------------------------------------------------------- cli

def add_corpus(p):
    p.add_argument("--corpus", choices=sorted(CORPORA), default="quality-holdout",
                   help="case corpus (default quality-holdout)")
    p.add_argument("--split", help="project-defects: dev or holdout (required); quality-holdout: holdout")
    p.add_argument("--cache", type=Path, help="project-defects: repository cache directory (filled over the network)")


def corpus_of(args):
    return Corpus(args.corpus, args.split, args.cache)


def add_selection(p):
    add_corpus(p)
    p.add_argument("--cases", help="comma-separated case ids")
    p.add_argument("--languages", help="comma-separated languages (cpp,python,javascript,go,rust; project-defects adds java)")
    p.add_argument("--kinds", help="defect_pair,tricky_normal (project-defects: real_fix,seeded)")
    p.add_argument("--variants", help="before,after (tricky normals have only before)")
    p.add_argument("--sample", type=int, help="stratified sample size (language x kind x detection)")
    p.add_argument("--seed", type=int, default=0)
    p.add_argument("--repeat", type=int, default=1, help="runs per selected variant (variance)")
    p.add_argument("--repeat-cases", help="limit --repeat to these case ids")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    p = sub.add_parser("plan", help="selection and estimate only")
    add_selection(p)
    p.add_argument("--history", action="append", help="saved review batch dir (repeatable)")
    p.add_argument("--ledger", help="relay ledger to check the estimate against (read only)")
    p.add_argument("--margin", type=float, default=DEFAULTS["margin"])
    p.add_argument("--review-max-calls", type=int, default=DEFAULTS["review_max_calls"],
                   help="the agents' model call ceiling per attempt (codeye agent --review-max-calls)")
    p.add_argument("--reserve-usd", default=DEFAULTS["reserve_usd"])
    p.add_argument("--review-request-bytes", type=int, help="operator request budget in bytes, 40000..1048576 "
                     "(codeye agent --review-request-bytes); omitted: product default, nothing passed. Recorded in "
                     "the manifest and holdout registry. The estimate only lowers its per-call input assumption to "
                     "it; call counts come from --history, not from this size")
    p.add_argument("--review-output-tokens", type=int, help="operator output budget per call, 1536..65536 "
                     "(codeye agent --review-output-tokens); omitted: product default, nothing passed. The estimate "
                     "only lowers its assumed output tokens to it")
    p.add_argument("--request-reserve", type=int, default=DEFAULTS["request_reserve"])
    r = sub.add_parser("run", help="run one batch through a real controller and agents")
    add_selection(r)
    r.add_argument("--condition", choices=("tools", "review"), required=True)
    r.add_argument("--build-root", type=Path, default=ROOT,
                   help="CODEYE checkout to build the product binary from (default: this checkout); the "
                        "manifest records it and this runner's checkout separately")
    r.add_argument("--results-root", required=True, help="stable directory above every batch (holds the registry)")
    r.add_argument("--batch", help="batch directory name (default condition-UTC)")
    r.add_argument("--go", required=True, help="absolute go toolchain (--go)")
    r.add_argument("--go-mod-cache")
    r.add_argument("--tool", action="append", help="analyzer tool or shared toolchain flag NAME=/abs/path (repeatable)")
    r.add_argument("--agent-flag", action="append", help="boolean agent option NAME, passed as --NAME (repeatable), "
                                                          "e.g. python-both-type-checkers")
    r.add_argument("--toolchain-path", default="/usr/bin:/bin")
    r.add_argument("--no-repo-code-grant", action="store_true", help="omit --repo-code-source-root for the corpus root")
    r.add_argument("--agents", type=int, default=1)
    r.add_argument("--max-workers", type=int, default=2)
    r.add_argument("--timeout", type=int, default=6 * 3600, help="batch wall-time limit in seconds")
    r.add_argument("--opencode", help="absolute OpenCode executable (review)")
    r.add_argument("--model", default="codeye-gateway/gpt-6.2-sol")
    r.add_argument("--relay-file", help="ready file of a relay the coordinator started (review)")
    r.add_argument("--ledger", help="that relay's ledger (read only)")
    r.add_argument("--forbidden-phrases", help="the answer list the relay was started with (review)")
    r.add_argument("--history", action="append")
    r.add_argument("--margin", type=float, default=DEFAULTS["margin"])
    r.add_argument("--review-max-calls", type=int, default=DEFAULTS["review_max_calls"],
                   help="the agents' model call ceiling per attempt (codeye agent --review-max-calls)")
    r.add_argument("--reserve-usd", default=DEFAULTS["reserve_usd"])
    r.add_argument("--review-request-bytes", type=int, help="operator request budget in bytes, 40000..1048576 "
                     "(codeye agent --review-request-bytes); omitted: product default, nothing passed. Recorded in "
                     "the manifest and holdout registry. The estimate only lowers its per-call input assumption to "
                     "it; call counts come from --history, not from this size")
    r.add_argument("--review-output-tokens", type=int, help="operator output budget per call, 1536..65536 "
                     "(codeye agent --review-output-tokens); omitted: product default, nothing passed. The estimate "
                     "only lowers its assumed output tokens to it")
    r.add_argument("--request-reserve", type=int, default=DEFAULTS["request_reserve"])
    r.add_argument("--dry-run", action="store_true", help="the relay uses --test-upstream (labelled, never evaluation)")
    r.add_argument("--plant-leak", metavar="CASE:VARIANT", help="dry-run self-test: put a label phrase in a comment near the top of that input")
    s = sub.add_parser("score", help="metrics over judged items")
    add_corpus(s)
    s.add_argument("batches", nargs="+", type=Path)
    s.add_argument("--judgments", action="append", type=Path, default=[])
    s.add_argument("--output", type=Path)
    s.add_argument("--line-slack", type=int, default=2)
    a = sub.add_parser("allow-text", help="tool messages and instruction envelopes that may reach the model")
    a.add_argument("--tools-batch", type=Path, action="append", default=[])
    a.add_argument("--request-dump", type=Path, action="append", default=[],
                   help="fake_model_upstream.py --dump directory of a clean dry run")
    a.add_argument("--output", type=Path, required=True)
    cl = sub.add_parser("capture-list", help="sentinel answer list for an envelope-capture dry run")
    add_corpus(cl)
    cl.add_argument("--output", type=Path, required=True, help="new file")
    bl = sub.add_parser("blind", help="blind judging packet of one batch and its separate answer key")
    add_corpus(bl)
    bl.add_argument("batch", type=Path)
    bl.add_argument("--seed", type=int, default=0)
    bl.add_argument("--output", type=Path, required=True, help="new directory for packet.json and answer-key.json")
    bm = sub.add_parser("blind-merge", help="judged blind packet + answer key -> judgments file for score")
    bm.add_argument("--packet", type=Path, required=True, help="the judged packet.json")
    bm.add_argument("--key", type=Path, required=True)
    bm.add_argument("--output", type=Path, required=True, help="new judgments file")
    rd = sub.add_parser("reader", help="report-reading task sheets and answer key")
    add_corpus(rd)
    rd.add_argument("batch", type=Path)
    rd.add_argument("--sample", type=int, default=6)
    rd.add_argument("--seed", type=int, default=0)
    rd.add_argument("--output", type=Path, required=True, help="new directory for sheets/ and answer-key.json")
    rs = sub.add_parser("reader-score", help="score human reader answers")
    rs.add_argument("--key", type=Path, required=True)
    rs.add_argument("--answers", type=Path, action="append", default=[])
    rs.add_argument("--output", type=Path)
    args = parser.parse_args(argv)

    if args.command == "plan":
        corpus = corpus_of(args)
        chosen, selection = select(corpus, cases=set(args.cases.split(",")) if args.cases else None,
                                   languages=set(args.languages.split(",")) if args.languages else None,
                                   kinds=set(args.kinds.split(",")) if args.kinds else None,
                                   variants=set(args.variants.split(",")) if args.variants else None,
                                   sample=args.sample, seed=args.seed)
        plan = variant_plan(corpus, chosen, set(args.variants.split(",")) if args.variants else None, args.repeat,
                            set(args.repeat_cases.split(",")) if args.repeat_cases else None)
        history = history_profile([Path(h) for h in args.history or []])
        est = estimate(plan, history=history, margin=args.margin, max_calls=args.review_max_calls,
                       slices_unknown=corpus.project, budget=agent_budget(args))
        out = {"corpus": corpus.describe(), "selection": selection,
               "variants": [key_of(c["id"], v, r) for c, v, r in plan], "estimate": est,
               "usd": {"expected": est["expected"]["ledger_microusd"] / 1e6, "upper": est["upper"]["ledger_microusd"] / 1e6}}
        if args.ledger:
            report = ledger_report(args.ledger)
            out["ledger"] = {k: report.get(k) for k in ("upstream", "remaining_microusd", "requests", "max_requests",
                                                         "halted")}
            out["refusal"] = budget_gate(est, report, parse_usd(args.reserve_usd), args.request_reserve)
        print(json.dumps(out, ensure_ascii=False, indent=1))
        return 1 if out.get("refusal") else 0
    if args.command == "run":
        if args.condition == "review":
            for flag in ("opencode", "relay_file", "ledger", "forbidden_phrases"):
                if not getattr(args, flag):
                    raise SystemExit(f"--condition review needs --{flag.replace('_', '-')}")
            if not os.path.isabs(args.opencode):
                raise SystemExit("--opencode must be absolute")
        elif args.relay_file or args.ledger or args.dry_run or args.plant_leak:
            raise SystemExit("--condition tools runs no model: drop --relay-file/--ledger/--dry-run/--plant-leak")
        run_batch(args)
        return 0
    import quality_score
    if args.command == "score":
        result = quality_score.score(args.batches, args.judgments, corpus_of(args), line_slack=args.line_slack)
        text = json.dumps(result, ensure_ascii=False, indent=1, sort_keys=True)
        if args.output:
            args.output.write_text(text + "\n")
        print(quality_score.render_table(result))
        return 0
    if args.command == "allow-text":
        allow_text(args.output, args.tools_batch, args.request_dump)
        return 0
    if args.command == "capture-list":
        capture_list(args.output, corpus_of(args))
        return 0
    if args.command == "blind":
        quality_score.blind_packet(args.batch, corpus_of(args), args.seed, args.output)
        return 0
    if args.command == "blind-merge":
        quality_score.blind_merge(args.packet, args.key, args.output)
        return 0
    if args.command == "reader":
        quality_score.reader_sheets(args.batch, corpus_of(args), args.sample, args.seed, args.output)
        return 0
    if args.command == "reader-score":
        result = quality_score.reader_score(args.key, args.answers)
        text = json.dumps(result, ensure_ascii=False, indent=1, sort_keys=True)
        if args.output:
            args.output.write_text(text + "\n")
        print(text)
        return 0
    return 2


if __name__ == "__main__":
    sys.exit(main())
