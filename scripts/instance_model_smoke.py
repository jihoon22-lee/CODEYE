"""codeye start의 모델 설정 시험(TC-182~TC-184): API 키 파일 하나로 모델 검토까지.

scripts/test_postgres.py --instance-model 이 임시 PostgreSQL 안에서 실행한다. 실제 OpenCode
(~/.opencode/bin/opencode 또는 CODEYE_TEST_OPENCODE_BIN)를 쓰고, 공급자는 OpenAI provider에
base_url만 바꾼 가짜 upstream(scripts/fake_model_upstream.py)이다. 모델 호출 비용은 없다.

1. agent.model 없이 <state>/secrets/model.json만 둔 서비스 설정으로 start → model probe 성공 →
   로컬 Go 프로젝트 run → 검토 단계 수락, ai_review performed, 보고서 게시.
1b. 단서 후속 조사(TC-339): 요청 바이트 40,000으로 다시 시작하고 단서를 남기는 가짜 upstream(--leads --dump)으로
   여러 패키지의 Go 프로젝트를 분석한다. 후속 조사 요청에는 플랫폼이 스냅샷에서 찾은 발췌가 스냅샷·경로·blob·줄로
   계산한 ID로 실리고, 비밀값 모양의 줄이 있어 보류된 파일의 내용은 어떤 요청에도 없다. 저장된 기록과 보고서의
   lead_followups가 같은 수를 센다.
2. 같은 키 파일의 base_url을 401을 내는 가짜로 바꾼다(재시작 없음, 호출마다 다시 읽음) →
   probe는 request 단계 model_auth_failed → run은 정적 분석을 마치고 보고서가 모델 검토 미수행과
   그 사유를 적는다.
3. --version만 다른 값을 답하는 OpenCode wrapper로 재시작 → 검토는 실행되고 기록·보고서에
   opencode_version_untested가 남는다.
4. 관리자가 웹 API로 OpenCode JSONC와 auth.json을 지정하고 연결을 확인한 뒤, 같은 서비스에서 다시 분석한다.
매 단계 키가 키 파일 밖(state·로그·산출물·보고서·DB dump·프로세스 argv)에 없는지 확인한다.

CODEYE_SMOKE_REAL_MODEL_CREDENTIAL=PATH(0600 CODEYE 키 파일, base_url 없음)를 주면 가짜 upstream
대신 그 키로 1단계만 실행한다(**실모델, 비용 발생**, 호출 상한 15). 키 파일은 agent.model.credential_file로
가리키기만 하고 복사하지 않으며, 끝에 같은 키 미노출 검사를 한다.
"""
import hashlib
import json
import os
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from instance_smoke import Session, free_port, wait_for  # noqa: E402

KEY = "sk-codeye-smoke-" + os.urandom(12).hex()
REAL = os.environ.get("CODEYE_SMOKE_REAL_MODEL_CREDENTIAL", "")


def fake_upstream(root, directory, name, mode, *extra):
    ready = directory / f"{name}.json"
    process = subprocess.Popen([sys.executable, str(root / "scripts/fake_model_upstream.py"), "--ready-file", str(ready), "--mode", mode, *extra],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    wait_for(f"fake upstream {name}", lambda: ready.exists() and ready.read_text().strip(), 30)
    return process, json.loads(ready.read_text())["port"]


def run(base, root, env, probe_env):
    global KEY
    if REAL:
        KEY = json.loads(Path(REAL).read_text())["api_key"]
    opencode = os.environ.get("CODEYE_TEST_OPENCODE_BIN") or str(Path.home() / ".opencode/bin/opencode")
    if not Path(opencode).is_file():
        raise SystemExit("OpenCode 실행 파일이 없습니다(CODEYE_TEST_OPENCODE_BIN)")
    directory = base / "servicel"
    directory.mkdir(mode=0o700)
    binary = base / "codeye"
    base_url = env["CODEYE_DATABASE_URL"]
    subprocess.run(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-c", "CREATE DATABASE instance_model"],
                   env=probe_env, capture_output=True, check=True, timeout=10)
    db_url = base_url.replace("/postgres?", "/instance_model?")
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

    fakes = []
    if not REAL:
        good, good_port = fake_upstream(root, directory, "fake-ok", "normal")
        bad, bad_port = fake_upstream(root, directory, "fake-401", "unauthorized")
        fakes = [good, bad]
    state = directory / "state"
    source = directory / "src"
    (source / "ledger").mkdir(parents=True)
    (source / "ledger/go.mod").write_text("module example.com/ledger\n\ngo 1.22\n")
    (source / "ledger/ledger.go").write_text(
        "package ledger\n\n// Total returns the sum of every entry.\nfunc Total(entries []int) int {\n\tsum := 0\n"
        "\tfor i := 0; i+1 < len(entries); i++ {\n\t\tsum += entries[i]\n\t}\n\treturn sum\n}\n")
    (source / "ledger/README.md").write_text("Total adds every entry, including the last one.\n")
    port = free_port()
    cfg = directory / "config.json"
    doc = {"schema_version": "0.1", "state_dir": str(state), "listen": f"127.0.0.1:{port}", "database": {"mode": "external"},
           "source_roots": [{"label": "work", "path": str(source)}],
           "agent": {"go": shutil.which("go"), "max_workers": 1}}
    if REAL:
        doc["agent"]["model"] = {"credential_file": REAL, "review_max_calls": 15}
    cfg.write_text(json.dumps(doc))
    cfg.chmod(0o600)
    (state / "secrets").mkdir(parents=True, mode=0o700)
    secret = state / "secrets/model.json"

    def write_secret(port):
        secret.write_text(json.dumps({"schema_version": "0.1", "provider": "openai", "api_key": KEY,
                                      "base_url": f"http://127.0.0.1:{port}/v1"}) + "\n")
        secret.chmod(0o600)

    if not REAL:
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
                              env=dict(probe_env, PGDATABASE="instance_model"), capture_output=True, timeout=60)
        assert dump.returncode == 0 and KEY.encode() not in dump.stdout, "키가 DB에 있음"
        return checked

    try:
        cli("migrate")
        started = cli("start", "--config", cfg).stdout
        assert "CODEYE started" in started, started
        probe = cli("model", "probe", "--config", cfg).stdout
        assert "provider openai, model openai/gpt-6.2-sol" in probe and "OK" in probe, probe
        session = Session(state, origin)
        project = session.request("POST", "projects", {
            "name": "ledger", "local_path": str(source / "ledger"),
            "analysis": {"send_source_to_model": True, "fetch_dependencies": False, "run_build_scripts": False},
        })

        # 1. 모델 검토 수행.
        view, report = analyze(session, project["id"])
        ai = report["coverage"]["ai_review"]
        assert ai["status"] == "performed", ai
        accepted = [t["key"] for t in ai["required_tasks"] if t["result"] == "accepted"]
        assert accepted and all(t["result"] == "accepted" for t in ai["required_tasks"]), ai
        assert "opencode_version_untested" not in report["limitations"], report["limitations"]
        print(f"  모델 검토 수행: run {view['status']}, 수락 {accepted}")
        if REAL:
            cli("stop", "--config", cfg)
            checked = no_key_on_disk()
            print(f"  키 미노출: 상태 파일 {checked}개·DB dump·보고서·CLI 출력·프로세스 인자 {len(seen_argv)}건 확인")
            print("PASS: 실모델 codeye start 모델 검토(키 파일 하나)")
            return

        # 1b. 단서 후속 조사: 작은 요청 예산과 단서를 남기는 가짜 upstream.
        dump = directory / "leads-requests"
        lead_fake, lead_port = fake_upstream(root, directory, "fake-leads", "normal", "--leads", "--dump", str(dump))
        fakes.append(lead_fake)
        chain = source / "chain"
        withheld = write_chain(chain)
        cli("stop", "--config", cfg)
        doc["agent"]["model"] = {"review_request_bytes": 40000}
        cfg.write_text(json.dumps(doc))
        cli("start", "--config", cfg)
        session = Session(state, origin)
        write_secret(lead_port)
        chain_project = session.request("POST", "projects", {
            "name": "chain", "local_path": str(chain),
            "analysis": {"send_source_to_model": True, "fetch_dependencies": False, "run_build_scripts": False},
        })
        view, report = analyze(session, chain_project["id"])
        check_leads(session, view, report, state, chain, dump, withheld)
        cli("stop", "--config", cfg)
        del doc["agent"]["model"]
        cfg.write_text(json.dumps(doc))
        cli("start", "--config", cfg)
        session = Session(state, origin)
        write_secret(good_port)

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
        assert "API 키를 거부함" in markdown.decode(), "Markdown 보고서가 모델 검토 미수행 사유를 적지 않음"
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
        assert "1.99.0 (not a tested release" in untested and "OK" in untested, untested
        view, report = analyze(session, project["id"])
        assert report["coverage"]["ai_review"]["status"] == "performed", report["coverage"]["ai_review"]
        assert "opencode_version_untested" in report["limitations"], report["limitations"]
        print(f"  시험하지 않은 OpenCode: run {view['status']}, 한계 opencode_version_untested")

        native = directory / "platform.jsonc"
        native.write_text("// Platform OpenCode profile\n" + json.dumps({
            "model": "openai/gpt-6.2-sol",
            "provider": {"openai": {"options": {"baseURL": f"http://127.0.0.1:{good_port}/v1"}}},
        }))
        auth = directory / "auth.json"
        auth.write_text(json.dumps({"openai": {"type": "api", "key": KEY}}))
        auth.chmod(0o600)
        saved = session.request("PUT", "admin/opencode", {
            "executable": opencode, "config_file": str(native), "credential_file": str(auth), "model": "",
        })
        assert saved["configured"] and saved["effective_model"] == "openai/gpt-6.2-sol", saved
        assert saved["probe"] == "not_probed", saved
        verified = session.request("POST", "admin/opencode/probe", timeout=200)
        assert verified["probe"] == "verified" and KEY not in json.dumps(verified), verified["probe"]
        view, report = analyze(session, project["id"])
        assert report["coverage"]["ai_review"]["status"] == "performed", report["coverage"]["ai_review"]
        assert "opencode_version_untested" not in report["limitations"], report["limitations"]
        print("  웹 설정 저장·연결 확인 후 새 OpenCode 실행 파일과 JSONC로 검토 완료")

        cli("stop", "--config", cfg)
        checked = no_key_on_disk()
        assert stat_mode(secret) == 0o600 and stat_mode(secret.parent) == 0o700
        print(f"  키 미노출: 상태 파일 {checked}개·DB dump·보고서·CLI 출력·프로세스 인자 {len(seen_argv)}건 확인")
        print("PASS: codeye start 모델 설정(API 키 파일, 실패 사유, 시험하지 않은 버전)")
    finally:
        if (state / "run/controller.json").exists() or (state / "run/agent.json").exists():
            subprocess.run([str(binary), "stop", "--config", str(cfg)], cwd=directory, env=run_env, capture_output=True, timeout=200)
        for p in fakes:
            p.send_signal(signal.SIGTERM)
            p.wait(timeout=10)


CHAIN_PAD = "\t// The chain steps down one package per call and adds its own index.\n" * 28


def write_chain(chain):
    """A Go module of a main package and seven chained packages whose
    functions are each about one review unit long, so a 40,000-byte request
    sends some and leaves the next package's function out; main also calls
    vault, whose credential-shaped line withholds the file. The value is
    built here so no fixture holds it. Returns that value."""
    value = "codeye" + "smokefixture" + "0" * 6
    (chain / "vault").mkdir(parents=True)
    (chain / "go.mod").write_text("module example.com/chain\n\ngo 1.22\n")
    (chain / "main.go").write_text('package main\n\nimport (\n\t"fmt"\n\n\t"example.com/chain/p01"\n\t"example.com/chain/vault"\n)\n\n'
                                   "func main() {\n" + CHAIN_PAD + "\ttoken := vault.Token()\n\tfmt.Println(p01.F01(3), len(token))\n}\n")
    for i in range(1, 8):
        body = f"package p{i:02d}\n\n" + (f'import "example.com/chain/p{i + 1:02d}"\n\n' if i < 7 else "")
        if i < 7:
            body += (f"// F{i:02d} steps down the chain.\nfunc F{i:02d}(n int) int {{\n" + CHAIN_PAD
                     + f"\tif n <= 0 {{\n\t\treturn 0\n\t}}\n\treturn p{i + 1:02d}.F{i + 1:02d}(n-1) + {i}\n}}\n")
        else:
            body += f"// F{i:02d} ends the chain; a negative input gives -1.\nfunc F{i:02d}(n int) int {{\n" + CHAIN_PAD + "\tif n < 0 {\n\t\treturn -1\n\t}\n\treturn n\n}\n"
        (chain / f"p{i:02d}").mkdir()
        (chain / f"p{i:02d}/f.go").write_text(body)
    (chain / "vault/vault.go").write_text(f'package vault\n\nconst password = "{value}"\n\n// Token returns the stored token.\nfunc Token() string {{ return password }}\n')
    return value


def captured(dump):
    """(role, prompt, review input, user text bytes) of every request the fake received."""
    import fake_model_upstream as fake
    out = []
    for path in sorted(dump.glob("*.json")):
        payload = json.loads(path.read_text())
        texts = [fake.content_text(m.get("content", "")) for m in payload.get("input", []) if isinstance(m, dict) and m.get("role") == "user"]
        prompt = next((t for t in texts if fake.ROLE.search(t)), "")
        role = fake.ROLE.search(prompt)
        found = fake.REVIEW_INPUT.search(prompt)
        out.append((role.group(1) if role else "", prompt, json.loads(found.group(1)) if found else None, sum(len(t.encode()) for t in texts)))
    return out


def check_leads(session, view, report, state, chain, dump, withheld):
    requests = captured(dump)
    assert requests and all(withheld not in prompt for _, prompt, _, _ in requests), "보류 파일의 내용이 모델 요청에 있음"
    followups = [(data, size) for role, _, data, size in requests if role == "leads"]
    assert followups, f"후속 조사 요청 없음: {[r[0] for r in requests]} {report['code_review'].get('lead_followups')}"
    resolved = set()
    for data, size in followups:
        snapshot = data["scope"]["snapshot_manifest_digest"]
        excerpts = {e["id"]: e for e in data["scope"]["excerpts"]}
        for lead in data["leads"]:
            assert lead["evidence_id"] in excerpts and lead["resolved_evidence_ids"], lead
            for ident in lead["resolved_evidence_ids"]:
                e = excerpts[ident]
                blob = (chain / e["path"]).read_bytes()
                sha = hashlib.sha256(blob).hexdigest()
                binding = "\0".join([snapshot, e["path"], sha, f'{e["start_line"]}:{e["end_line"]}'])
                assert e["sha256"] == sha and ident == "ev_" + hashlib.sha256(binding.encode()).hexdigest()[:24], f"발췌 ID가 스냅샷 blob과 맞지 않음: {ident}"
                lines = blob.decode().split("\n")[e["start_line"] - 1:e["end_line"]]
                if "text" in e:
                    assert e["text"] == "\n".join(f"{e['start_line'] + i}| {x}" for i, x in enumerate(lines)), f"발췌 원문이 스냅샷과 다름: {ident}"
                resolved.add(ident)
    lf = report["code_review"]["lead_followups"]
    assert lf["leads_resolved"] >= 1 and lf["leads_unresolved"].get("lead_secret_withheld", 0) >= 1, lf
    assert lf["calls"] == len(followups) and lf["tasks_accepted"] >= 1 and lf["new_excerpts"] >= 1 and lf["new_lines"] >= 1, lf
    stored = {"asked": 0, "new_lines": 0, "new": set(), "bytes": []}
    for a in report["artifacts"]:
        key = a.get("task_key") or ""
        if a["media_type"] != "application/vnd.codeye.review+json" or not (key.startswith("review.leads") or key.startswith("review.investigate")):
            continue
        record = json.loads((state / "artifacts/sha256" / a["sha256"][:2] / a["sha256"]).read_bytes())
        stored["asked"] += len(record.get("leads", []))
        if record.get("lead_followup"):
            stored["new_lines"] += record["lead_followup"]["new_lines"]
            stored["new"] |= set(record["lead_followup"]["new_evidence_ids"])
            stored["bytes"] += [c["request_bytes"] for c in record["calls"]]
    assert stored["asked"] == lf["leads_asked"] and stored["new_lines"] == lf["new_lines"] and stored["new"] <= resolved, (stored, lf)
    assert stored["bytes"] and max(stored["bytes"]) <= 40000, stored["bytes"]
    markdown, _ = session.request("GET", f"runs/{view['id']}/report?revision={view['report']['revision']}&format=markdown", raw=True)
    assert "단서 후속 조사" in markdown.decode(), "Markdown 보고서에 단서 후속 조사가 없음"
    first, size = followups[0]
    where = {e["id"]: f"{e['path']}:{e['start_line']}-{e['end_line']}" for e in first["scope"]["excerpts"]}
    print("  단서 후속 조사: 요청 " + ", ".join(f"{len(d['leads'])}개 단서·발췌 {len(d['scope']['excerpts'])}개" for d, _ in followups)
          + f"; 첫 요청(사용자 메시지 {size}바이트) 단서 {[(x['id'], x['symbol'], [(i, where[i]) for i in x['resolved_evidence_ids']]) for x in first['leads']]}")
    print(f"  기록 request_bytes {stored['bytes']} (상한 40000), 보고서 lead_followups {json.dumps(lf, ensure_ascii=False, sort_keys=True)}")


def stat_mode(path):
    return path.stat().st_mode & 0o777
