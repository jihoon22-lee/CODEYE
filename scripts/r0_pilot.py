"""명시적으로 선택한 R0 실모델 파일럿. 임시 DB와 승인된 고정 입력에서만 실행한다."""

import argparse
from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import socket
import subprocess
import tarfile
import tempfile
import time
from urllib.error import URLError
from uuid import uuid4

from node_smoke import Session, service, wait_for


def tree_digest(directory):
    digest = hashlib.sha256()
    for path in sorted(directory.rglob("*")):
        if path.is_file():
            digest.update(path.relative_to(directory).as_posix().encode() + b"\0")
            digest.update(hashlib.sha256(path.read_bytes()).digest())
    return digest.hexdigest()


def extract_commit(root, commit, work, destination):
    archive = work / "source.tar"
    subprocess.run(
        ["git", "archive", "--format", "tar", "-o", archive, commit],
        cwd=root,
        check=True,
    )
    with tarfile.open(archive) as tar:
        tar.extractall(destination, filter="data")
    archive.unlink()


W15_MARKER = "<!-- CODEYE test fixture (TC-97)."


# Fixed repository inputs that are not R0 synthetic pairs. The W12 verifier
# fixture is the caller-closes counterexample TC-100 scores separately.
FIXED_INPUTS = {"w15": "fixtures/w15-modules/input", "w12-close": "internal/worker/review/testdata/verify"}


def materialize_w15(root, destination, label="w15"):
    """Copy a fixed input; drop the W15 development-agent warning comment.

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
        if path.name == "AGENTS.md" and data.startswith(W15_MARKER.encode()):
            data = data.split(b"\n", 1)[1]
        target.write_bytes(data)
    return tree_digest(destination)


def keep_review_artifacts(report, artifacts, destination):
    """Copy the model-role and discovery artifacts a report references."""
    wanted = ("application/vnd.codeye.review+json", "application/vnd.codeye.environment+json")
    index = {f.name: f for f in artifacts.rglob("*") if f.is_file()}
    destination.mkdir(mode=0o700)
    kept = 0
    for a in report["artifacts"]:
        if a["media_type"] not in wanted:
            continue
        found = index.get(a["sha256"])
        if found is None or hashlib.sha256(found.read_bytes()).hexdigest() != a["sha256"]:
            raise RuntimeError("보고서가 참조한 검토 산출물을 찾지 못했습니다")
        name = (a["task_key"] or a["role"]) + "-" + a["sha256"][:12] + ".json"
        shutil.copyfile(found, destination / name)
        kept += 1
    return kept


def audit_reports(output, root):
    from validate_bundle import check_report, make_validators

    qa = root / "fixtures/r0-quality"
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
                if materialize_w15(root, inputs, label) != row["input"]:
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
                    != subprocess.check_output(
                        ["git", "rev-parse", "bcfad99^{commit}"], cwd=root, text=True
                    ).strip()
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


def run(base, root, env):
    relay = Path(os.environ["CODEYE_R0_RELAY_FILE"]).resolve(strict=True)
    output = Path(os.environ["CODEYE_R0_RESULTS"]).resolve()
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    binary = base / "codeye"
    opencode = shutil.which("opencode")
    go = shutil.which("go")
    if not (opencode and go):
        raise RuntimeError("Go/OpenCode 실행 파일이 필요합니다")
    work = base / "pilot"
    work.mkdir(mode=0o700)
    source = work / "source"
    source.mkdir()
    qa = root / "fixtures/r0-quality"
    materialize = runpy.run_path(str(qa / "corpus.py"))["materialize"]
    cases = json.loads((qa / "corpus.json").read_text())
    selected = (
        os.environ.get("CODEYE_R0_CASES", "").split(",")
        if os.environ.get("CODEYE_R0_CASES")
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
            dataset.append((label, rel, materialize_w15(root, source / rel, label)))
    commit = subprocess.check_output(
        ["git", "rev-parse", "bcfad99^{commit}"], cwd=root, text=True
    ).strip()
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

    def cli(*args):
        r = subprocess.run(
            [binary, *args], cwd=work, env=env, capture_output=True, timeout=15
        )
        if r.returncode:
            raise RuntimeError(f"운영 명령 실패 {args[0]} ({r.returncode})")
        return r.stdout

    provider = (
        json.loads(cli("provider", "add", "--kind", "github", "--host", "github.com"))[
            "id"
        ]
        if git_smoke
        else None
    )
    user = json.loads(cli("user", "create", "--name", "R0 pilot"))["id"]
    root_id = json.loads(
        cli("source-root", "add", "--label", "R0 fixed inputs", "--path", source)
    )["id"]
    cli("source-root", "grant", "--root", root_id, "--user", user)
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    api = Session(origin)

    def ready():
        try:
            return api.request("GET", "ready")
        except URLError:
            return None

    subprocess.run(["npm", "run", "build"], cwd=root / "web", check=True)
    with ExitStack() as stack:
        children = [
            stack.enter_context(
                service(
                    [
                        binary,
                        "serve",
                        "--listen",
                        f"127.0.0.1:{port}",
                        "--web-dir",
                        root / "web/dist",
                        "--artifact-dir",
                        work / "artifacts",
                        "--reconcile-interval",
                        "1s",
                    ],
                    work,
                    env,
                    work / "control.log",
                )
            )
        ]
        wait_for("개인 모드 API", ready, children)
        link = work / "launch"
        cli("login-link", "--user", user, "--output", link, "--origin", origin)
        api.csrf = api.request(
            "POST",
            "session",
            {"launch_token": link.read_text().strip().split("#launch=", 1)[1]},
        )["csrf_token"]
        # 파일럿의 두 노드는 승인한 고정 사본에만 접근한다. 비용 장부와 키는 source 밖에 있다.
        for name in ("pilot-a", "pilot-b"):
            token = work / (name + ".enroll")
            cli(
                "node",
                "token",
                "create",
                "--pool",
                "default",
                "--source-root",
                root_id,
                "--output",
                token,
            )
            children.append(
                stack.enter_context(
                    service(
                        [
                            binary,
                            "agent",
                            "--controller",
                            origin,
                            "--state-dir",
                            work / name,
                            "--name",
                            name,
                            "--enroll-token-file",
                            token,
                            "--source-root",
                            f"{root_id}={source}",
                            "--model-source-root",
                            root_id,
                            *(
                                [
                                    "--git",
                                    shutil.which("git"),
                                    "--git-host",
                                    "github.com",
                                ]
                                if git_smoke
                                else []
                            ),
                            "--go",
                            go,
                            "--go-mod-cache",
                            subprocess.check_output(
                                [go, "env", "GOMODCACHE"], text=True
                            ).strip(),
                            "--opencode",
                            opencode,
                            "--model",
                            "codeye-gateway/gpt-6.2-sol",
                            "--model-relay-file",
                            relay,
                            "--autoscale",
                            "--max-workers",
                            "2",
                            *(
                                ["--review-pack", os.environ["CODEYE_R0_REVIEW_PACK"]]
                                if os.environ.get("CODEYE_R0_REVIEW_PACK")
                                else []
                            ),
                        ],
                        work,
                        {"PATH": env["PATH"], "LANG": "C.UTF-8"},
                        work / (name + ".log"),
                    )
                )
            )
        cli(
            "pool",
            "set",
            "--name",
            "default",
            "--min-workers",
            "0",
            "--max-workers",
            "2",
            "--idle-timeout-seconds",
            "3",
            "--scale-up-cooldown-seconds",
            "0",
            "--scale-down-cooldown-seconds",
            "0",
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
                keep_review_artifacts(report, work / "artifacts", output / label)
                if label == "git":
                    digest = report["inputs"]["commit_sha"]
                    if (
                        not digest
                        or len(digest) not in (40, 64)
                        or report["inputs"]["source_kind"] != "git"
                        or report["coverage"]["ai_review"]["status"] == "performed"
                        or not any(
                            t["key"] == "review.context"
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
                    "review_pack": os.environ.get("CODEYE_R0_REVIEW_PACK", "review-v1"),
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
        inspected = next(
            (row for row in rows if row["summary"]["total_findings"]), rows[0]
        )
        browser_link = work / "browser-launch"
        cli("login-link", "--user", user, "--output", browser_link, "--origin", origin)
        from browser_smoke import published_report

        published_report(
            root, env, origin, browser_link, inspected["case"], inspected["run_id"]
        )
        if dataset and not any(
            row["status"] == "completed" and row["ai_review"] == "performed"
            for row in rows
        ):
            raise RuntimeError("전체 모델 검토를 거친 완료 보고서가 없습니다")
        for row in rows:
            if row["case"] == "pilot" and (
                row["status"] != "completed" or row["ai_review"] != "performed"
            ):
                raise RuntimeError(
                    "개인 프로젝트 파일럿 검토가 부분 완료됐습니다. 기록된 사유를 확인하세요."
                )
    print(
        f"PASS: 실제 R0 보고서 {len(rows)}개 게시·조회, 공개 파일럿 기준 {commit[:12]}"
    )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="R0에 보관한 보고서를 같은 입력과 대조한다"
    )
    parser.add_argument("--audit-results", required=True, type=Path)
    args = parser.parse_args()
    audit_reports(args.audit_results, Path(__file__).resolve().parents[1])
