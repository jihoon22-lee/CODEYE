"""명시적으로 선택한 회귀 실모델 파일럿. 임시 DB와 승인된 고정 입력에서만 실행한다."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import subprocess
import tempfile
import time
from uuid import uuid4

from eval_harness import Cluster, extract_commit, keep_review_artifacts, tree_digest


DEV_AGENT_MARKER = "<!-- CODEYE test fixture (TC-97)."


# Fixed repository inputs that are not the synthetic defect/fix pairs. The verifier
# fixture is the caller-closes counterexample TC-100 scores separately.
FIXED_INPUTS = {"modules": "fixtures/modules/input", "caller-closes": "internal/worker/review/testdata/verify"}

# The pilot is a copy of this repository at an approved commit. CODEYE_PILOT_COMMIT
# names another commit (e.g. a clean copy of the current main for a dry run).
DEFAULT_PILOT_COMMIT = "bcfad99"


def pilot_commit(root):
    name = os.environ.get("CODEYE_PILOT_COMMIT") or DEFAULT_PILOT_COMMIT
    return subprocess.check_output(["git", "rev-parse", name + "^{commit}"], cwd=root, text=True).strip()


def materialize_fixed_input(root, destination, label="modules"):
    """Copy a fixed input; drop the development-agent warning comment.

    The repository copy of AGENTS.md starts with a comment telling development
    agents not to follow it. A real hostile document would not announce itself,
    so the evaluated snapshot omits that one line. Expected facts stay outside.
    """
    source = root / FIXED_INPUTS[label]
    for path in sorted(source.rglob("*")):
        target = destination / path.relative_to(source)
        if path.is_dir():
            target.mkdir(parents=True, exist_ok=True)
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        data = path.read_bytes()
        if path.name == "AGENTS.md" and data.startswith(DEV_AGENT_MARKER.encode()):
            data = data.split(b"\n", 1)[1]
        target.write_bytes(data)
    return tree_digest(destination)


def audit_reports(output, root):
    from validate_bundle import check_report, make_validators

    qa = root / "fixtures/regression"
    cases = json.loads((qa / "corpus.json").read_bytes())
    materialize = runpy.run_path(str(qa / "corpus.py"))["materialize"]
    schema = make_validators()["report.schema.json"]
    rows = json.loads((output / "summary.json").read_bytes())
    allowed = {"git", "pilot", *FIXED_INPUTS} | {
        c["id"] + "-" + v for c in cases for v in ("before", "after")
    }
    seen = set()
    evidence_count = 0
    claim_count = 0
    with tempfile.TemporaryDirectory(prefix="codeye-pilot-audit-") as td:
        temp = Path(td)
        for row in rows:
            label = row["case"]
            if label not in allowed or label in seen:
                raise ValueError("알 수 없거나 중복된 실행 라벨")
            seen.add(label)
            inputs = temp / "input"
            report = json.loads((output / (label + ".json")).read_bytes())
            schema.validate(report)
            if (
                row["run_id"] != report["run_id"]
                or row["summary"] != report["summary"]
                or row["snapshot_id"] != report["inputs"]["snapshot_id"]
                or row["manifest"] != report["inputs"]["manifest_sha256"]
            ):
                raise ValueError("요약과 게시 리비전 불일치")
            if label in FIXED_INPUTS:
                if materialize_fixed_input(root, inputs, label) != row["input"]:
                    raise ValueError("고정 입력 digest 불일치")
            elif label in ("git", "pilot"):
                commit = row["input"]
                if not isinstance(commit, str) or not re.fullmatch(
                    r"(?:[0-9a-f]{40}|[0-9a-f]{64})", commit
                ):
                    raise ValueError("Git 전체 SHA가 필요합니다")
                if (
                    label == "pilot"
                    and commit
                    != pilot_commit(root)
                ):
                    raise ValueError("승인 파일럿 commit 불일치")
                if label == "git" and commit != report["inputs"]["commit_sha"]:
                    raise ValueError("Git commit 불일치")
                inputs.mkdir()
                extract_commit(root, commit, temp, inputs)
            else:
                case_id, variant = label.split("-")
                if variant not in ("before", "after"):
                    raise ValueError("알 수 없는 평가 변형")
                i, case = next(
                    (i, c) for i, c in enumerate(cases) if c["id"] == case_id
                )
                if (
                    materialize(inputs, case, variant, documented=i % 2 == 0)
                    != row["input"]
                ):
                    raise ValueError("합성 입력 digest 불일치")
            if label != "git" and tree_digest(inputs) != row["source_tree_sha256"]:
                raise ValueError("보관된 입력 전체 digest 불일치")
            check_report(report, inputs)
            evidence_count += sum(len(f["evidence"]) for f in report["findings"])
            claim_count += sum(len(f["claims"]) for f in report["findings"])
            shutil.rmtree(inputs)
    print(
        f"PASS: 실제 보고서 {len(rows)}개, claim {claim_count}개, 근거 {evidence_count}개 및 원본 재검증"
    )


# Module-sliced review (docs/REVIEW.md#모듈-단위-검토-분할): slice task keys and
# the scope artifact a pilot report references.
SLICE_TASK = re.compile(r"^review\.(?:scope|(?:investigate|verify)\.s\d{3})$")
REVIEW_RECORD_MEDIA = "application/vnd.codeye.review+json"
DEFAULT_MAX_CALLS = 99


def _kept_docs(directory):
    """The review records and the scope artifact keep_review_artifacts saved."""
    records, scope = [], None
    for path in sorted(directory.glob("*.json")) if directory.is_dir() else []:
        doc = json.loads(path.read_bytes())
        if doc.get("kind") == "review_scope":
            scope = doc
        elif doc.get("schema_version") in ("0.2", "0.3") and "stage" in doc:
            records.append(doc)
    return records, scope


def _context_files(record):
    return {e["path"] for e in record.get("evidence", [])
            if e.get("origin") == "context" and e.get("kind") in ("code", "test")}


def _test_ledger(ledger):
    """Whether the relay ledger is a test ledger (relay --test-upstream)."""
    if ledger is None:
        return False
    lines = Path(ledger).read_text().splitlines()
    return bool(lines) and json.loads(lines[0]).get("upstream") == "test-loopback"


# Report limitations that mean an input of the pilot report was not read.
_UNREAD = ("artifact_rejected", "review_lineage_mismatch", "unrecognized_artifact", "result_manifest", "analysis_artifact_missing")


def check_coverage(rows, output, root, max_calls=DEFAULT_MAX_CALLS, ledger=None):
    """The conditions registered before a paid run with slices (docs/EVALUATION.md#회귀-집합-실행).

    Small synthetic cases need no model call in a follow-up slot. Their review calls use the pinned
    Go-only pack digests. The pilot completes with the AI review performed;
    its report reads every review artifact and carries the review ledger
    with the configured ceiling; the ledger's calls_used (the attempt counter
    before the slices plus the slice records) is known and at most the
    ceiling; a slice the ceiling refused has no record and no call; the
    overview and the slices never send the same file; the ledger and
    llm_exposed_files count more files sent to the model than the overview
    sent; the relay ledger has no refused or blocked request.

    With a test relay ledger (a dry run against the fake upstream) two causes
    are expected rather than failures, and are listed in dry_run_expected: a
    review task the fake upstream's reply fails (review_output_invalid; its
    exposure is then unknown) and a required analyzer this host has not
    configured (tool_not_configured). The two usually come together: with the
    pilot's analyzers missing, review.interpret gets no observation, and the
    fake's interpret reply then has no item to give. Any other cause still
    fails, and a paid run gets no exception. Returns the failures and the
    pilot's numbers."""
    from bundle_checks.review import pack_selection

    _, roles = pack_selection(("go",))
    test = _test_ledger(ledger)
    failures, pilot = [], None
    for row in rows:
        label = row["case"]
        if label == "git":
            continue
        if label != "pilot":
            # The planner may reserve slots before it knows how many lines
            # the overview will cover. A reserved slot is not a model call.
            if re.fullmatch(r"Q[0-9]+-(before|after)", label):
                for task in row["tasks"]:
                    key = task["key"]
                    if key == "review.scope" and task["status"] != "succeeded":
                        failures.append(f"{label}: scope inventory {task['status']}")
                    elif SLICE_TASK.match(key) and key != "review.scope" and (task["status"], task["reason"]) != ("skipped_policy", "no_review_scope"):
                        failures.append(f"{label}: unnecessary or incomplete follow-up {key}: {task['status']}/{task['reason']}")
                report = json.loads((output / (label + ".json")).read_text())
                extra_calls = [c["task_key"] for c in report["coverage"].get("review_calls", []) if SLICE_TASK.match(c.get("task_key", ""))]
                if extra_calls:
                    failures.append(f"{label}: unexpected model calls {extra_calls}")
            for call in row["review_calls"]:
                want = roles.get(call["role"], {}).get("digest")
                if call["pack_sha256"] != want:
                    failures.append(f"{label}: {call['role']} pack digest {call['pack_sha256']} is not the pinned {want}")
            continue
        report = json.loads((output / "pilot.json").read_bytes())
        tasks = {t["key"]: t for t in report["tasks"]}
        book = (report.get("code_review") or {}).get("ledger")
        records, scope = _kept_docs(output / "pilot")
        overview = next((r for r in records if r["stage"] == "investigate" and "slice" not in r), None)
        slice_inv = [r for r in records if r["stage"] == "investigate" and "slice" in r]
        sent = [(_context_files(overview) if overview else set())] + [_context_files(r) for r in slice_inv]
        union = set().union(*sent)
        exhausted = [k for k, t in tasks.items() if SLICE_TASK.match(k) and t["status"] == "budget_exhausted"]
        recorded = {"review." + r["stage"] + "." + r["slice"]["key"] for r in records if "slice" in r}
        called = {c.get("task_key") for c in report["coverage"].get("review_calls", [])}
        exposed = report["coverage"].get("llm_exposed_files")
        calls_used = book["calls_used"] if book else None
        ledger_sent = sum(m["files_sent_to_model"] for m in book["modules"]) if book else None
        fake = sorted(k for k, t in tasks.items() if k.startswith("review.") and t["status"] == "failed" and t["reason"] == "review_output_invalid") if test else []
        unconfigured = sorted(k for k, t in tasks.items() if t["required_for_verdict"] and t["status"] == "unsupported" and t["reason"] == "tool_not_configured") if test else []
        unrecorded = sorted(k for k, t in tasks.items() if k.startswith("review.") and t["status"] in ("failed", "timed_out"))
        unread = sorted(x for x in report["limitations"] if x.startswith(_UNREAD))
        expected = []
        pilot = {"calls_used": calls_used, "recorded_calls": sum(len(r.get("calls", [])) for r in records), "max_calls": max_calls,
                 "slice_tasks": sum(1 for k in tasks if SLICE_TASK.match(k)), "slices_admitted": len(slice_inv), "slice_tasks_budget_exhausted": len(exhausted),
                 "overview_files": len(sent[0]), "files_sent_by_records": len(union), "ledger_status": book and book["status"],
                 "ledger_files_sent_to_model": ledger_sent, "llm_exposed_files": exposed, "review_tasks_without_record": unrecorded, "dry_run_expected": expected}
        if unread:
            failures.append(f"pilot: report did not read {unread}")
        if row["status"] != "completed" or row["ai_review"] != "performed":
            other = sorted(k for k, t in tasks.items() if t["required_for_verdict"] and t["status"] != "succeeded" and k not in unconfigured)
            if test and row["ai_review"] == "performed" and not other and set(unrecorded) <= set(fake) and (fake or unconfigured):
                expected.append(f"pilot {row['status']}: required analyzers not configured on this host {unconfigured}; fake upstream reply rejected {fake}")
            else:
                failures.append(f"pilot: status {row['status']}, AI review {row['ai_review']}")
        if scope is None or scope["budget"]["max_calls"] != max_calls or book is None or book["max_calls"] != max_calls:
            failures.append("pilot: no scope artifact and ledger with the configured ceiling")
        if not isinstance(calls_used, int) or calls_used > max_calls:
            failures.append(f"pilot: ledger calls_used {calls_used} unknown or above the ceiling {max_calls}")
        if set(exhausted) & (recorded | called):
            failures.append(f"pilot: budget-exhausted slices with a record or a call {sorted(set(exhausted) & (recorded | called))}")
        if sum(len(x) for x in sent) != len(union):
            failures.append("pilot: a file reached two investigate requests")
        if not isinstance(ledger_sent, int) or ledger_sent <= len(sent[0]):
            failures.append(f"pilot: ledger files sent to the model {ledger_sent} not above the overview's {len(sent[0])} files")
        if exposed is None and fake and set(unrecorded) <= set(fake):
            expected.append(f"llm_exposed_files unknown: the fake upstream reply was rejected for {fake}")
        elif not isinstance(exposed, int) or exposed <= len(sent[0]):
            failures.append(f"pilot: llm_exposed_files {exposed} not above the overview's {len(sent[0])} files")
    if ledger is not None:
        bad = [r.get("type") for r in (json.loads(line) for line in Path(ledger).read_text().splitlines() if line.strip())
               if r.get("type") in ("refused", "blocked")]
        if bad:
            failures.append(f"relay ledger: {len(bad)} refused or blocked requests")
    return failures, pilot


def run(base, root, env, web=True, coverage_check=True):
    relay = Path(os.environ["CODEYE_PILOT_RELAY_FILE"]).resolve(strict=True)
    output = Path(os.environ["CODEYE_PILOT_RESULTS"]).resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    model = json.loads(relay.read_text())["model"]
    binary = base / "codeye"
    opencode = shutil.which("opencode")
    go = shutil.which("go")
    if not (opencode and go):
        raise RuntimeError("Go/OpenCode 실행 파일이 필요합니다")
    work = base / "pilot"
    work.mkdir(mode=0o700)
    source = work / "source"
    source.mkdir()
    qa = root / "fixtures/regression"
    materialize = runpy.run_path(str(qa / "corpus.py"))["materialize"]
    cases = json.loads((qa / "corpus.json").read_text())
    selected = (
        os.environ.get("CODEYE_PILOT_CASES", "").split(",")
        if os.environ.get("CODEYE_PILOT_CASES")
        else None
    )
    dataset = []
    for i, case in enumerate(cases):
        for variant in ("before", "after"):
            label = f"{case['id']}-{variant}"
            if selected and label not in selected:
                continue
            rel = uuid4().hex
            digest = materialize(source / rel, case, variant, documented=i % 2 == 0)
            dataset.append((label, rel, digest))
    for label in FIXED_INPUTS:
        if selected and label in selected:
            rel = uuid4().hex
            dataset.append((label, rel, materialize_fixed_input(root, source / rel, label)))
    commit = pilot_commit(root)
    if not selected or "pilot" in selected:
        rel = uuid4().hex
        path = source / rel
        path.mkdir()
        extract_commit(root, commit, work, path)
        dataset.append(("pilot", rel, commit))
    original_trees = {label: tree_digest(source / rel) for label, rel, _ in dataset}
    git_smoke = not selected or "git" in selected
    if not dataset and not git_smoke:
        raise ValueError("선택한 사례가 없습니다")

    def agent_args(root_id):
        return [
            *(["--git", shutil.which("git"), "--git-host", "github.com"] if git_smoke else []),
            "--go", go,
            "--go-mod-cache", subprocess.check_output([go, "env", "GOMODCACHE"], text=True).strip(),
            "--model-source-root", root_id,
            "--opencode", opencode,
            "--model", "codeye-gateway/" + model,
            "--model-relay-file", relay,
            # 비밀정보 선별이 있어야 모듈 단위 slice가 실행된다. 호출 상한은 시도마다 적용된다.
            *(["--gitleaks", os.environ["CODEYE_PILOT_GITLEAKS"]] if os.environ.get("CODEYE_PILOT_GITLEAKS") else []),
            *(["--review-max-calls", os.environ["CODEYE_PILOT_REVIEW_MAX_CALLS"]] if os.environ.get("CODEYE_PILOT_REVIEW_MAX_CALLS") else []),
            # 파일럿 사본의 Python·JS 모듈은 ruff·eslint가 판정에 필요하다. name=path를 공백으로
            # 구분해 주면 에이전트 플래그 --name path가 된다(예: ruff=/abs/ruff node=/abs/node).
            *[a for spec in os.environ.get("CODEYE_PILOT_TOOLS", "").split() for a in ("--" + spec.partition("=")[0], spec.partition("=")[2])],
        ]

    if web:
        subprocess.run(["npm", "run", "build"], cwd=root / "web", check=True)
    # 파일럿의 두 노드는 승인한 고정 사본에만 접근한다. 비용 장부와 키는 source 밖에 있다.
    with Cluster(base, binary, env, source, agent_args=agent_args, agents=2, max_workers=2,
                 label="fixed inputs", web_dir=root / "web/dist" if web else None, agent_path=env["PATH"], pool_max=2) as cluster:
        api, cli, origin, user, children = cluster.api, cluster.cli, cluster.origin, cluster.user, cluster.children
        root_id = cluster.root_id
        provider = (
            json.loads(cli("provider", "add", "--kind", "github", "--host", "github.com"))["id"]
            if git_smoke
            else None
        )
        outstanding = {}
        start = time.monotonic()
        for label, rel, digest in dataset:
            project = api.request(
                "POST",
                "projects",
                {
                    "name": label,
                    "source": {
                        "kind": "local",
                        "source_root_id": root_id,
                        "relative_path": rel,
                    },
                },
            )
            run_id = api.enqueue(project["id"])
            outstanding[run_id] = (label, rel, digest)
        if git_smoke:
            git_project = api.request(
                "POST",
                "projects",
                {
                    "name": "git-acquisition",
                    "source": {
                        "kind": "git",
                        "provider_id": provider,
                        "repository_url": "https://github.com/jihoon22-lee/CODEYE.git",
                        "branch": "main",
                    },
                },
            )
            outstanding[api.enqueue(git_project["id"])] = ("git", None, None)
        rows = []
        while outstanding:
            if time.monotonic() - start > 3600 or any(
                p.poll() is not None for p in children
            ):
                raise RuntimeError("파일럿 기한 또는 프로세스 종료")
            for run_id, (label, rel, digest) in list(outstanding.items()):
                run = api.request("GET", "runs/" + run_id)
                if run["status"] in {"failed", "cancelled", "recovery_required"}:
                    raise RuntimeError(f"{label} run {run_id} 상태 {run['status']}")
                if run["status"] not in {"completed", "partial"}:
                    continue
                report = api.request(
                    "GET", f"runs/{run_id}/report?format=json&include_code=true"
                )
                if (
                    report["run_id"] != run_id
                    or report["inputs"]["snapshot_id"] != run["snapshot_id"]
                ):
                    raise RuntimeError("보고서 입력 참조 불일치")
                dest = output / (label + ".json")
                dest.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
                keep_review_artifacts(report, cluster.work / "artifacts", output / label)
                if label == "git":
                    digest = report["inputs"]["commit_sha"]
                    if (
                        not digest
                        or len(digest) not in (40, 64)
                        or report["inputs"]["source_kind"] != "git"
                        or report["coverage"]["ai_review"]["status"] == "performed"
                        or not any(
                            t["key"].startswith("review.")
                            and t["reason"] == "model_source_not_approved"
                            for t in report["tasks"]
                        )
                    ):
                        raise RuntimeError("Git commit 고정 또는 모델 전송 정책 불일치")
                elif tree_digest(source / rel) != original_trees[label]:
                    raise RuntimeError(f"{label} 원본 입력이 변경됐습니다")
                if label not in ("git", "pilot", *FIXED_INPUTS):
                    assert (
                        hashlib.sha256(
                            (source / rel / "metrics.go").read_bytes()
                        ).hexdigest()
                        == digest
                    )
                row = {
                    "case": label,
                    "input": digest,
                    "source_tree_sha256": original_trees.get(label),
                    "run_id": run_id,
                    "snapshot_id": run["snapshot_id"],
                    "manifest": report["inputs"]["manifest_sha256"],
                    "status": run["status"],
                    "ai_review": report["coverage"]["ai_review"]["status"],
                    "summary": report["summary"],
                    "tasks": [
                        {k: v for k, v in t.items() if k in ("key", "status", "reason")}
                        for t in report["tasks"]
                    ],
                    "review_calls": [
                        {k: c.get(k) for k in ("role", "outcome", "pack_sha256")}
                        for c in report["coverage"].get("review_calls", [])
                    ],
                    "elapsed_s": round(time.monotonic() - start, 1),
                }
                print(
                    label,
                    row["status"],
                    row["ai_review"],
                    "findings",
                    row["summary"]["total_findings"],
                    flush=True,
                )
                rows.append(row)
                del outstanding[run_id]
                (output / "summary.json").write_text(
                    json.dumps(rows, ensure_ascii=False, indent=2) + "\n"
                )
            if outstanding:
                time.sleep(1)
        if web:
            inspected = next(
                (row for row in rows if row["summary"]["total_findings"]), rows[0]
            )
            browser_link = work / "browser-launch"
            cli("login-link", "--user", user, "--output", browser_link, "--origin", origin)
            from browser_smoke import published_report

            published_report(
                root, env, origin, browser_link, inspected["case"], inspected["run_id"]
            )
        if coverage_check:
            ceiling = int(os.environ.get("CODEYE_PILOT_REVIEW_MAX_CALLS") or DEFAULT_MAX_CALLS)
            failures, pilot = check_coverage(rows, output, root, ceiling, os.environ.get("CODEYE_PILOT_LEDGER"))
            (output / "coverage-check.json").write_text(
                json.dumps({"failures": failures, "pilot": pilot}, ensure_ascii=False, indent=2) + "\n")
            print("coverage", json.dumps(pilot, ensure_ascii=False), flush=True)
            if failures:
                raise RuntimeError("모듈 단위 검토 조건 불충족: " + "; ".join(failures))
        # A dry run whose pilot is partial only for the causes check_coverage
        # expects of the fake upstream and this host (dry_run_expected).
        expected_partial = coverage_check and pilot is not None and any(e.startswith("pilot ") for e in pilot["dry_run_expected"])
        if dataset and not any(
            row["status"] == "completed" and row["ai_review"] == "performed"
            for row in rows
        ):
            raise RuntimeError("전체 모델 검토를 거친 완료 보고서가 없습니다")
        for row in rows:
            if row["case"] == "pilot" and not expected_partial and (
                row["status"] != "completed" or row["ai_review"] != "performed"
            ):
                raise RuntimeError(
                    "개인 프로젝트 파일럿 검토가 부분 완료됐습니다. 기록된 사유를 확인하세요."
                )
    print(
        f"PASS: 실제 회귀 보고서 {len(rows)}개 게시·조회, 공개 파일럿 기준 {commit[:12]}"
    )


_NORMALIZE = [
    (re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"), "<uuid>"),
    (re.compile(r"\bev_[0-9a-f]{24}\b"), "<evidence>"),
    (re.compile(r"\bobs_[0-9a-f]{20}\b"), "<observation>"),
    (re.compile(r"\b[0-9a-f]{64}\b"), "<sha256>"),
    (re.compile(r"\b[0-9a-f]{32}\b"), "<dir>"),
    (re.compile(r"/tmp/[^\s\"'\\]+"), "<tmp>"),
]


def normalized_requests(dump):
    """The (role, prompt) of every request a fake_model_upstream.py --dump
    directory holds, with run-specific identities replaced (UUIDs, evidence
    and observation IDs bound to the snapshot digest, digests, the random
    source directory and temporary paths)."""
    out = []
    for path in sorted(Path(dump).glob("*.json")):
        payload = json.loads(path.read_bytes())
        text = "\n".join(str(m.get("content", "")) for m in payload.get("input", []) if isinstance(m, dict))
        for pattern, repl in _NORMALIZE:
            text = pattern.sub(repl, text)
        role = re.search(r"^Role: ([a-z]+)$", text, re.M)
        out.append((role.group(1) if role else "unknown", text))
    return out


def compare_dumps(baseline, candidate):
    """Requests of candidate missing from baseline and the reverse, as multisets."""
    from collections import Counter

    a, b = Counter(normalized_requests(baseline)), Counter(normalized_requests(candidate))
    return {"baseline": sum(a.values()), "candidate": sum(b.values()),
            "only_baseline": sorted(Counter({k: n for k, n in (a - b).items()}).elements()),
            "only_candidate": sorted(Counter({k: n for k, n in (b - a).items()}).elements())}


def dry_run(args, root):
    """The regression set through the product with a --test-upstream relay:
    a temporary PostgreSQL, the binary built from --build-root, no web build
    and no browser. The relay's ledger must be a test ledger; a real ledger is
    refused, so this never sends a model request."""
    from test_postgres import temporary_postgres

    header = json.loads(Path(args.ledger).read_text().splitlines()[0])
    if header.get("upstream") != "test-loopback":
        raise SystemExit("--dry-run needs the ledger of a relay started with --test-upstream")
    env = dict(os.environ, CODEYE_PILOT_RELAY_FILE=str(args.relay_file), CODEYE_PILOT_RESULTS=str(args.results),
               CODEYE_PILOT_CASES=args.cases, CODEYE_PILOT_LEDGER=str(args.ledger))
    env.pop("OPENAI_API_KEY", None)
    for key, value in (("CODEYE_PILOT_GITLEAKS", args.gitleaks), ("CODEYE_PILOT_REVIEW_MAX_CALLS", args.max_calls), ("CODEYE_PILOT_COMMIT", args.pilot_commit),
                       ("CODEYE_PILOT_TOOLS", " ".join(args.tool or []))):
        env.pop(key, None)
        if value:
            env[key] = str(value)
    os.environ.clear()
    os.environ.update(env)
    with tempfile.TemporaryDirectory(prefix="codeye-pilot-dry-") as directory:
        base = Path(directory)
        with temporary_postgres(base) as (url, penv, _):
            penv = {k: v for k, v in penv.items() if k != "OPENAI_API_KEY"}
            penv["CODEYE_DATABASE_URL"] = url
            subprocess.run(["go", "build", "-o", str(base / "codeye"), "./cmd/codeye"], cwd=args.build_root, env=penv, check=True)
            subprocess.run([str(base / "codeye"), "migrate"], cwd=base, env=penv, check=True, capture_output=True, timeout=120)
            run(base, root, penv, web=False, coverage_check=not args.no_coverage_check)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="보관한 회귀 보고서를 같은 입력과 대조한다"
    )
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--audit-results", type=Path)
    mode.add_argument("--dry-run", action="store_true", help="test-upstream relay only (no model request)")
    mode.add_argument("--compare-dumps", nargs=2, type=Path, metavar=("BASELINE", "CANDIDATE"))
    parser.add_argument("--relay-file", type=Path)
    parser.add_argument("--ledger", type=Path)
    parser.add_argument("--results", type=Path)
    parser.add_argument("--cases", default="")
    parser.add_argument("--gitleaks")
    parser.add_argument("--tool", action="append", help="agent tool flag as name=path, e.g. ruff=/abs/ruff (repeatable)")
    parser.add_argument("--max-calls", type=int)
    parser.add_argument("--pilot-commit", help="commit of this repository the pilot copies (default bcfad99)")
    parser.add_argument("--build-root", type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument("--no-coverage-check", action="store_true", help="baseline build without slices")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    if args.audit_results:
        audit_reports(args.audit_results, root)
    elif args.compare_dumps:
        diff = compare_dumps(*args.compare_dumps)
        for side in ("only_baseline", "only_candidate"):
            diff[side] = [{"role": role, "bytes": len(text.encode()), "sha256": hashlib.sha256(text.encode()).hexdigest()[:16]}
                          for role, text in diff[side]]
        print(json.dumps(diff, ensure_ascii=False, indent=1))
    else:
        if not (args.relay_file and args.ledger and args.results and args.cases):
            raise SystemExit("--dry-run needs --relay-file, --ledger, --results and --cases")
        dry_run(args, root)
