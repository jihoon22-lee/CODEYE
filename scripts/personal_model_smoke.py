"""개인 모드 모델 설정 시험(TC-170~TC-172): API 키 파일 하나로 모델 검토까지.

scripts/test_postgres.py --personal-model 이 임시 PostgreSQL 안에서 실행한다. 실제 OpenCode
(~/.opencode/bin/opencode 또는 CODEYE_TEST_OPENCODE_BIN)를 쓰고, 공급자는 OpenAI provider에
base_url만 바꾼 가짜 upstream(scripts/fake_model_upstream.py)이다. 모델 호출 비용은 없다.

1. agent.model 없이 <state>/secrets/model.json만 둔 개인 설정으로 start → model probe 성공 →
   로컬 Go 프로젝트 run → 검토 단계 수락, ai_review performed, 보고서 게시.
2. 같은 키 파일의 base_url을 401을 내는 가짜로 바꾼다(재시작 없음, 호출마다 다시 읽음) →
   probe는 request 단계 model_auth_failed → run은 정적 분석을 마치고 보고서가 모델 검토 미수행과
   그 사유를 적는다.
3. --version만 다른 값을 답하는 OpenCode wrapper로 재시작 → 검토는 실행되고 기록·보고서에
   opencode_version_untested가 남는다.
매 단계 키가 키 파일 밖(state·로그·산출물·보고서·DB dump·프로세스 argv)에 없는지 확인한다.
"""
import json
import os
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from personal_smoke import Session, free_port, wait_for  # noqa: E402

KEY = "sk-codeye-smoke-" + os.urandom(12).hex()


def fake_upstream(root, directory, name, mode):
    ready = directory / f"{name}.json"
    process = subprocess.Popen([sys.executable, str(root / "scripts/fake_model_upstream.py"), "--ready-file", str(ready), "--mode", mode],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    wait_for(f"fake upstream {name}", lambda: ready.exists() and ready.read_text().strip(), 30)
    return process, json.loads(ready.read_text())["port"]


def run(base, root, env, probe_env):
    opencode = os.environ.get("CODEYE_TEST_OPENCODE_BIN") or str(Path.home() / ".opencode/bin/opencode")
    if not Path(opencode).is_file():
        raise SystemExit("OpenCode 실행 파일이 없습니다(CODEYE_TEST_OPENCODE_BIN)")
    directory = base / "personal-model"
    directory.mkdir(mode=0o700)
    binary = base / "codeye"
    base_url = env["CODEYE_DATABASE_URL"]
    subprocess.run(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-c", "CREATE DATABASE personal_model"],
                   env=probe_env, capture_output=True, check=True, timeout=10)
    db_url = base_url.replace("/postgres?", "/personal_model?")
    run_env = dict(env, CODEYE_DATABASE_URL=db_url)
    for k in ("OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENCODE_API_KEY"):
        run_env.pop(k, None)

    def cli(*args, expect=0, timeout=200):
        result = subprocess.run([str(binary), *map(str, args)], cwd=directory, env=run_env, capture_output=True, text=True, timeout=timeout)
        text = result.stdout + result.stderr
        assert KEY not in text, f"{args[0]}: 키 노출"
        if expect is not None and result.returncode != expect:
            raise RuntimeError(f"{' '.join(map(str, args[:2]))} 종료 코드 {result.returncode} (기대 {expect}): {text[-3000:]}")
        return result

    good, good_port = fake_upstream(root, directory, "fake-ok", "normal")
    bad, bad_port = fake_upstream(root, directory, "fake-401", "unauthorized")
    state = directory / "state"
    source = directory / "src"
    (source / "ledger").mkdir(parents=True)
    (source / "ledger/go.mod").write_text("module example.com/ledger\n\ngo 1.22\n")
    (source / "ledger/ledger.go").write_text(
        "package ledger\n\n// Total returns the sum of every entry.\nfunc Total(entries []int) int {\n\tsum := 0\n"
        "\tfor i := 0; i+1 < len(entries); i++ {\n\t\tsum += entries[i]\n\t}\n\treturn sum\n}\n")
    (source / "ledger/README.md").write_text("Total adds every entry, including the last one.\n")
    port = free_port()
    cfg = directory / "personal.json"
    doc = {"schema_version": "0.1", "state_dir": str(state), "listen": f"127.0.0.1:{port}", "database": {"mode": "external"},
           "source_roots": [{"label": "work", "path": str(source)}],
           "agent": {"go": shutil.which("go"), "max_workers": 1}}
    cfg.write_text(json.dumps(doc))
    cfg.chmod(0o600)
    (state / "secrets").mkdir(parents=True, mode=0o700)
    secret = state / "secrets/model.json"

    def write_secret(port):
        secret.write_text(json.dumps({"schema_version": "0.1", "provider": "openai", "api_key": KEY,
                                      "base_url": f"http://127.0.0.1:{port}/v1"}) + "\n")
        secret.chmod(0o600)

    write_secret(good_port)
    origin = f"http://127.0.0.1:{port}"
    seen_argv = []

    def scan_processes():
        # Every process of this user: the key must never be in an argv.
        for proc in Path("/proc").iterdir():
            if proc.name.isdigit():
                try:
                    argv = (proc / "cmdline").read_bytes()
                except OSError:
                    continue
                assert KEY.encode() not in argv, f"키가 프로세스 인자에 있음: pid {proc.name}"
                seen_argv.append(argv)

    def analyze(session, project_id):
        run_id = session.enqueue(project_id)
        deadline = time.monotonic() + 600
        view = None
        while time.monotonic() < deadline:
            scan_processes()
            view = session.request("GET", f"runs/{run_id}")
            if view["status"] in ("partial", "completed", "failed", "cancelled", "recovery_required") and (view.get("report") or view["status"] != "partial"):
                break
            time.sleep(0.3)
        assert view and view.get("report"), view
        report = session.request("GET", f"runs/{run_id}/report?revision={view['report']['revision']}")
        assert KEY not in json.dumps(report), "키가 보고서에 있음"
        return view, report

    def no_key_on_disk():
        checked = 0
        for path in state.rglob("*"):
            if path.is_file() and not path.is_symlink() and path != secret:
                assert KEY.encode() not in path.read_bytes(), f"키가 상태 파일에 있음: {path.relative_to(state)}"
                checked += 1
        dump = subprocess.run([str(Path(shutil.which("pg_dump"))), "--no-owner", "-d", db_url.split("?")[0]],
                              env=dict(probe_env, PGDATABASE="personal_model"), capture_output=True, timeout=60)
        assert dump.returncode == 0 and KEY.encode() not in dump.stdout, "키가 DB에 있음"
        return checked

    try:
        cli("migrate")
        started = cli("start", "--config", cfg).stdout
        assert "CODEYE started" in started, started
        probe = cli("model", "probe", "--config", cfg).stdout
        assert "provider openai, model openai/gpt-6.2-sol" in probe and "OK" in probe, probe
        session = Session(state, origin)
        work = next(r for r in session.request("GET", "source-roots")["items"] if r["label"] == "work")
        project = session.request("POST", "projects", {"name": "ledger", "source": {"kind": "local", "source_root_id": work["id"], "relative_path": "ledger"}})

        # 1. 모델 검토 수행.
        view, report = analyze(session, project["id"])
        ai = report["coverage"]["ai_review"]
        assert ai["status"] == "performed", ai
        accepted = [t["key"] for t in ai["required_tasks"] if t["result"] == "accepted"]
        assert accepted and all(t["result"] == "accepted" for t in ai["required_tasks"]), ai
        assert "opencode_version_untested" not in report["limitations"], report["limitations"]
        print(f"  모델 검토 수행: run {view['status']}, 수락 {accepted}")

        # 2. 키 거부: 재시작 없이 다음 호출부터 401. 정적 분석은 계속, 보고서가 사유를 적는다.
        write_secret(bad_port)
        failed_probe = cli("model", "probe", "--config", cfg, expect=1).stdout
        assert "FAILED at request: model_auth_failed" in failed_probe, failed_probe
        view, report = analyze(session, project["id"])
        ai = report["coverage"]["ai_review"]
        reasons = {t.get("reason") for t in ai["required_tasks"]}
        assert ai["status"] == "not_performed" and "model_auth_failed" in reasons, ai
        assert report["coverage"]["tools"], "정적 분석 결과 없음"
        markdown, _ = session.request("GET", f"runs/{view['id']}/report?revision={view['report']['revision']}&format=markdown", raw=True)
        assert "API 키를 거부했다" in markdown.decode(), "Markdown 보고서가 모델 검토 미수행 사유를 적지 않음"
        print(f"  키 거부: run {view['status']}, ai_review {ai['status']} ({sorted(r for r in reasons if r)})")

        # 3. 시험하지 않은 OpenCode 버전: wrapper로 재시작.
        write_secret(good_port)
        wrapper = directory / "opencode-wrapper"
        wrapper.write_text(f'#!/bin/sh\nif [ "$1" = --version ]; then echo 1.99.0; exit 0; fi\nexec {opencode} "$@"\n')
        wrapper.chmod(0o755)
        cli("stop", "--config", cfg)
        doc["agent"]["model"] = {"opencode": str(wrapper)}
        cfg.write_text(json.dumps(doc))
        cli("start", "--config", cfg)
        session = Session(state, origin)
        untested = cli("model", "probe", "--config", cfg).stdout
        assert "1.99.0 (not the tested" in untested and "OK" in untested, untested
        view, report = analyze(session, project["id"])
        assert report["coverage"]["ai_review"]["status"] == "performed", report["coverage"]["ai_review"]
        assert "opencode_version_untested" in report["limitations"], report["limitations"]
        print(f"  시험하지 않은 OpenCode: run {view['status']}, 한계 opencode_version_untested")

        cli("stop", "--config", cfg)
        checked = no_key_on_disk()
        assert stat_mode(secret) == 0o600 and stat_mode(secret.parent) == 0o700
        print(f"  키 미노출: 상태 파일 {checked}개·DB dump·보고서·CLI 출력·프로세스 인자 {len(seen_argv)}건 확인")
        print("PASS: 개인 모드 모델 설정(API 키 파일, 실패 사유, 시험하지 않은 버전)")
    finally:
        if (state / "run/controller.json").exists() or (state / "run/agent.json").exists():
            subprocess.run([str(binary), "stop", "--config", str(cfg)], cwd=directory, env=run_env, capture_output=True, timeout=200)
        for p in (good, bad):
            p.send_signal(signal.SIGTERM)
            p.wait(timeout=10)


def stat_mode(path):
    return path.stat().st_mode & 0o777
