"""codeye up·start 수용 시험(TC-125, TC-127, TC-32): start/status/stop, 백업·복원, 복원본 재시작.

scripts/test_postgres.py --instance 가 임시 PostgreSQL 안에 데이터베이스 셋을 만들어 실행한다.
A: 실제 Go 분석(go vet) → 정지 → 백업 → 비어 있지 않은 대상 복원 거절 → B에 복원 → B 시작·보고서·증거·새 분석.
C: 시험용 handler(-tags codeye_fixture) sleep 작업 중 정지 → drain 시간 초과 → 취소·정리 확인.
관리형 `codeye up`(TC-170~173)과 경로·Git 주소 입력(TC-187~188): 홈 아래 폴더, Git 주소(시험 빌드의 file:// 미러),
데이터 폴더 거절, 브라우저의 한 칸 입력·개선 안내·다시 시도·도구 화면.
Linux 전용이다. 모델 호출·네트워크 다운로드는 없다.
"""
import hashlib
import json
import os
import shutil
import socket
import stat
import subprocess
import time
from http.cookiejar import CookieJar
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPCookieProcessor, Request, build_opener
from uuid import uuid4

TERMINAL = {"partial", "completed", "failed", "cancelled", "recovery_required"}


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_for(description, probe, timeout=120):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = probe()
        if result:
            return result
        time.sleep(0.2)
    raise RuntimeError(f"{description} 시간 초과")


class Session:
    """The browser's calls: launch-token login, CSRF header, Origin."""

    def __init__(self, state_dir, origin, link=None):
        self.origin = origin
        self.client = build_opener(HTTPCookieProcessor(CookieJar()))
        self.csrf = None
        link = link or (state_dir / "login-link").read_text().strip()
        self.token = link.split("#launch=", 1)[1].split("&", 1)[0]
        self.csrf = self.request("POST", "session", {"launch_token": self.token})["csrf_token"]

    def request(self, method, path, payload=None, headers=None, raw=False, timeout=10):
        fields = dict(headers or {})
        if method != "GET":
            fields.update({"Origin": self.origin, "Content-Type": "application/json"})
            if self.csrf:
                fields["X-CODEYE-CSRF"] = self.csrf
        data = json.dumps(payload).encode() if payload is not None else None
        with self.client.open(Request(self.origin + "/api/v1/" + path, method=method, data=data, headers=fields), timeout=timeout) as r:
            return (r.read(), r.headers) if raw else json.load(r)

    def enqueue(self, project_id):
        body = {"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": {"kind": "full"}}
        return self.request("POST", f"projects/{project_id}/runs", body, {"Idempotency-Key": str(uuid4())})["id"]

    def finished(self, run_id, timeout=240):
        def probe():
            view = self.request("GET", f"runs/{run_id}")
            done = view["status"] in TERMINAL and (view.get("report") or view["status"] != "partial")
            return view if done else None
        return wait_for(f"run {run_id} 종료", probe, timeout)


def run(base, root, env, probe_env):
    directory = base / "instance-smoke"
    directory.mkdir(mode=0o700)
    # No GitHub token of this machine reaches a smoke installation: the token variables are
    # dropped and gh reads an empty configuration directory (no login).
    env = {k: v for k, v in env.items() if k not in ("GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN")}
    (directory / "gh-config").mkdir(mode=0o700)
    env["GH_CONFIG_DIR"] = str(directory / "gh-config")
    binary = base / "codeye"
    fixture = directory / "codeye-fixture"
    subprocess.run(["go", "build", "-tags", "codeye_fixture", "-o", fixture, "./cmd/codeye"],
                   cwd=root, env=env, check=True, timeout=180)
    pg_bin = Path(shutil.which("pg_dump")).parent
    go = shutil.which("go")
    base_url = env["CODEYE_DATABASE_URL"]
    password = urlsplit(base_url).password
    urls = {}
    for name in ("instance_a", "instance_b", "instance_c"):
        subprocess.run(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-c", f"CREATE DATABASE {name}"],
                       env=probe_env, capture_output=True, check=True, timeout=10)
        urls[name] = base_url.replace("/postgres?", f"/{name}?")

    # start detaches the controller and agent (setsid): stop them even when an assertion fails.
    launched = []

    # The fixture handler stands in for analyzers only on a node without any
    # baseline analyzer; start takes go from PATH when agent.go is not set, so
    # the fixture installation runs without the Go toolchain directory on PATH.
    go_dir = str(Path(go).parent)
    no_go_path = os.pathsep.join(p for p in env["PATH"].split(os.pathsep) if p != go_dir)

    def cli(exe, *args, db, expect=0, timeout=120, extra_env=None):
        if args[0] == "start":
            launched.append((exe, args[2], db))
        extra = dict(extra_env or {})
        if exe == fixture:
            extra.setdefault("PATH", no_go_path)
        result = subprocess.run([exe, *map(str, args)], cwd=directory, env=dict(env, CODEYE_DATABASE_URL=urls[db], **extra),
                                capture_output=True, text=True, timeout=timeout)
        text = result.stdout + result.stderr
        assert password not in text and urls[db] not in text, f"{args[0]}: credential 노출"
        if expect is not None and result.returncode != expect:
            raise RuntimeError(f"{args[0]} 종료 코드 {result.returncode} (기대 {expect}): {text[-2000:]}")
        return result

    def config(name, port, roots, **extra):
        state = directory / name / "state"
        # GitHub token discovery is off: the machine's own gh login stays out of the smoke.
        doc = {"schema_version": "0.1", "state_dir": str(state), "listen": f"127.0.0.1:{port}",
               "database": {"mode": "external", "postgres_bin_dir": str(pg_bin)},
               "source_roots": [{"label": label, "path": str(path)} for label, path in roots],
               "backup_dir": str(directory / "backups"), "github": {"enabled": False}, **extra}
        (directory / name).mkdir(mode=0o700)
        path = directory / name / "config.json"
        path.write_text(json.dumps(doc, indent=2))
        path.chmod(0o600)
        return path, state, f"http://127.0.0.1:{port}"

    def private(path):
        return stat.S_IMODE(path.stat().st_mode)

    def no_secrets(state, tokens=()):
        secret = password.encode()
        checked = 0
        for path in state.rglob("*"):
            if path.is_file() and not path.is_symlink():
                data = path.read_bytes()
                assert secret not in data and b"postgresql://" not in data, f"DB 인증정보가 상태 파일에 있음: {path.relative_to(state)}"
                if path.parent.name == "logs":
                    for token in tokens:
                        assert token.encode() not in data, f"로그인 토큰이 로그에 있음: {path.name}"
                checked += 1
        assert checked > 0, state

    source = directory / "src"
    (source / "hello").mkdir(parents=True)
    (source / "hello/go.mod").write_text("module example.com/hello\n\ngo 1.22\n")
    (source / "hello/main.go").write_text('package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Printf("%d\\n", "not a number")\n}\n')
    agent = {"go": go, "max_workers": 1}
    quota = {"workspace_bytes": 1 << 30, "artifacts_bytes": 1 << 30}
    stop = {"drain_timeout_seconds": 60, "cancel_timeout_seconds": 60}
    cfg_a, state_a, origin_a = config("a", free_port(), [("work", source)], agent=agent, quota=quota, stop=stop)
    cfg_b, state_b, origin_b = config("b", free_port(), [("work", source)], agent=agent, quota=quota, stop=stop)

    try:
        # 1-3. A: 시작, 상태, 프로젝트 추가와 실제 Go 분석.
        cli(binary, "migrate", db="instance_a")
        stopped = json.loads(cli(binary, "status", "--config", cfg_a, "--format", "json", db="instance_a").stdout)
        assert stopped["state"] == "stopped" and stopped["issues"] == [], stopped
        started = cli(binary, "start", "--config", cfg_a, db="instance_a", timeout=200).stdout
        assert "CODEYE started" in started and "registered source root" in started, started
        for path in (state_a, state_a / "run", state_a / "logs", state_a / "agent", state_a / "artifacts"):
            assert private(path) == 0o700, path
        for path in (state_a / "run/controller.json", state_a / "run/agent.json", state_a / "admin.json", state_a / "login-link"):
            assert private(path) == 0o600, path
        assert not (state_a / "run/enroll-token").exists(), "일회용 등록 토큰이 남음"
        again = cli(binary, "start", "--config", cfg_a, db="instance_a").stdout
        assert "already running" in again, again
        status = json.loads(cli(binary, "status", "--config", cfg_a, "--format", "json", db="instance_a").stdout)
        assert status["state"] == "running" and status["controller"]["ready"] and status["agent"]["heartbeat_fresh"], status
        assert {s["area"] for s in status["storage"]} == {"snapshot", "workspace", "cache", "logs", "artifacts"}, status
        session = Session(state_a, origin_a)
        project = session.request("POST", "projects", {"name": "hello", "local_path": str(source / "hello")})
        first = session.finished(session.enqueue(project["id"]))
        assert first["status"] in ("partial", "completed") and first["report"], first
        report = session.request("GET", f"runs/{first['id']}/report?revision={first['report']['revision']}")
        assert report["artifacts"], "보고서 증거 없음"

        # 같은 시점 온라인 백업(실행 중): 성공하고 확정 산출물만 담는다.
        live = json.loads(cli(binary, "backup", "--config", cfg_a, "--output", directory / "backups/live", "--format", "json",
                              db="instance_a").stdout)
        assert live["manifest"]["consistency"] and live["manifest"]["artifacts"]["count"] >= len({a["sha256"] for a in report["artifacts"]}), live

        # 4. 정지: 진행 중 작업이 없으면 drain 즉시 끝나고 정리 확인.
        stopped = json.loads(cli(binary, "stop", "--config", cfg_a, "--format", "json", db="instance_a", timeout=200).stdout)
        assert stopped["outcome"] == "stopped" and stopped["agent_cleanup"] == "confirmed", stopped
        assert {p["state_after"] for p in stopped["processes"]} == {"exited"}, stopped
        assert json.loads(cli(binary, "stop", "--config", cfg_a, "--format", "json", db="instance_a").stdout)["outcome"] == "not_running"
        assert not (state_a / "run/controller.json").exists() and not (state_a / "run/agent.json").exists()
        no_secrets(state_a, [session.token])
        # Without --config the path is $XDG_CONFIG_HOME/codeye/config.json.
        xdg = directory / "xdg"
        (xdg / "codeye").mkdir(parents=True, mode=0o700)
        shutil.copy(cfg_a, xdg / "codeye/config.json")
        default = json.loads(cli(binary, "status", "--format", "json", db="instance_a", extra_env={"XDG_CONFIG_HOME": str(xdg)}).stdout)
        assert default["state"] == "stopped" and default["controller"]["origin"] == origin_a, default

        # 5. 백업: 비밀값 없는 manifest, 0600 파일, 같은 출력 재사용 거절.
        out = directory / "backups/b1"
        made = json.loads(cli(binary, "backup", "--config", cfg_a, "--output", out, "--format", "json", db="instance_a").stdout)
        manifest = json.loads((out / "manifest.json").read_text())
        assert manifest["migration_version"] >= 9 and manifest["database"]["sha256"] == made["manifest"]["database"]["sha256"]
        for name in ("manifest.json", "artifacts.json"):
            text = (out / name).read_text()
            assert password not in text and "postgresql://" not in text and "127.0.0.1" not in text, name
        for path in [out / "manifest.json", out / "database.dump", out / "artifacts.json", *(p for p in (out / "artifacts").rglob("*") if p.is_file())]:
            assert private(path) == 0o600, path
        cli(binary, "backup", "--config", cfg_a, "--output", out, db="instance_a", expect=1)

        # 6. 복원: 비어 있지 않은 DB는 거절(산출물 디렉터리도 비어 있음), 새 DB와 새 state_dir에는 성공.
        cfg_x, state_x, _ = config("x", free_port(), [("work", source)])
        refused = cli(binary, "restore", "--config", cfg_x, "--from", out, db="instance_a", expect=1)
        assert "not empty" in refused.stderr, refused.stderr
        assert not any(p.is_file() for p in (state_x / "artifacts").rglob("*")), "거절된 복원이 산출물을 남김"
        restored = json.loads(cli(binary, "restore", "--config", cfg_b, "--from", out, "--format", "json", db="instance_b", timeout=300).stdout)
        fence = restored["fence"]
        assert fence["nodes_revoked"] == 1 and fence["leased_remaining"] == 0 and restored["artifacts_restored"] == manifest["artifacts"]["count"], restored
        cli(binary, "restore", "--config", cfg_b, "--from", out, db="instance_b", expect=1)

        # 7-8. B 시작(새 노드 등록), 복원된 보고서·증거 확인, 새 분석.
        cli(binary, "start", "--config", cfg_b, db="instance_b", timeout=200)
        restored_session = Session(state_b, origin_b)
        projects = restored_session.request("GET", "projects?limit=100")["items"]
        same = next(p for p in projects if p["id"] == project["id"])
        assert same["activity"]["last_report"]["run_id"] == first["id"], same
        restored_report = restored_session.request("GET", f"runs/{first['id']}/report?revision={first['report']['revision']}")
        assert restored_report["summary"] == report["summary"] and restored_report["artifacts"] == report["artifacts"], "복원 보고서 불일치"
        for artifact in restored_report["artifacts"]:
            blob = (state_b / "artifacts/sha256" / artifact["sha256"][:2] / artifact["sha256"]).read_bytes()
            assert hashlib.sha256(blob).hexdigest() == artifact["sha256"] and len(blob) == artifact["size_bytes"], artifact
        body, headers = restored_session.request("GET", f"runs/{first['id']}/report?revision={first['report']['revision']}&format=markdown", raw=True)
        assert headers["X-CODEYE-Report-SHA256"].encode() in body
        second = restored_session.finished(restored_session.enqueue(project["id"]))
        assert second["status"] in ("partial", "completed") and second["report"], second
        doctor = cli(binary, "doctor", "--format", "json", "--config", cfg_b, "--work-dir", directory / "doctor-work", db="instance_b", expect=None)
        report_json = json.loads(doctor.stdout)
        from jsonschema import Draft202012Validator
        Draft202012Validator(json.loads((root / "schemas/doctor.schema.json").read_text())).validate(report_json)
        ops = report_json["operations"]
        assert ops["processes"]["status"] == "consistent" and ops["processes"]["controller"] == "running", ops
        assert ops["backup"]["status"] == "recent" and ops["backup"]["latest"] in ("b1", "live"), ops
        stopped = json.loads(cli(binary, "stop", "--config", cfg_b, "--format", "json", db="instance_b", timeout=200).stdout)
        assert stopped["outcome"] == "stopped", stopped
        no_secrets(state_b, [restored_session.token])
        print("PASS: codeye start/status/stop·실제 Go 분석·온라인/정지 백업·비어 있지 않은 대상 거절·복원·새 노드·보고서와 증거 hash·새 분석")

        # C: 진행 중 작업이 drain 시간을 넘기면 취소하고 정리를 확인한다(시험용 handler).
        fsrc = directory / "fixture-src"
        fsrc.mkdir()
        (fsrc / "codeye-fixture-mode").write_text("sleep\n")
        # 시험용 handler는 실제 분석기가 없을 때만 선택되므로 도구 탐지를 끈다.
        cfg_c, state_c, origin_c = config("c", free_port(), [("fixture", fsrc)], agent={"auto_detect": False},
                                         stop={"drain_timeout_seconds": 2, "cancel_timeout_seconds": 60})
        cli(fixture, "migrate", db="instance_c")
        cli(fixture, "start", "--config", cfg_c, db="instance_c", timeout=200)
        fixture_session = Session(state_c, origin_c)
        fproject = fixture_session.request("POST", "projects", {"name": "fixture", "local_path": str(fsrc)})
        sleeping = fixture_session.enqueue(fproject["id"])
        wait_for("sleep 작업 실행", lambda: fixture_session.request("GET", f"runs/{sleeping}")["status"] == "running")
        busy = json.loads(cli(fixture, "status", "--config", cfg_c, "--format", "json", db="instance_c").stdout)
        assert [a["run_id"] for a in busy["in_flight"]] == [sleeping], busy
        stopped = json.loads(cli(fixture, "stop", "--config", cfg_c, "--format", "json", db="instance_c", timeout=200).stdout)
        agent_proc = next(p for p in stopped["processes"] if p["role"] == "agent")
        assert stopped["outcome"] == "stopped" and agent_proc["action"] == "cancelled_after_drain_timeout", stopped
        assert stopped["agent_cleanup"] == "confirmed" and [a["run_id"] for a in stopped["in_flight_at_stop"]] == [sleeping], stopped
        after = stopped["attempts_after_stop"][0]
        assert after["cleanup_state"] == "confirmed" and after["status"] != "leased" and after["run_status"] != "running", stopped
        left = []
        for proc in Path("/proc").iterdir():
            try:
                if proc.name.isdigit() and os.readlink(proc / "cwd").startswith(str(state_c)):
                    left.append(proc.name)
            except OSError:
                pass
        assert not left, f"정지 뒤 남은 프로세스 {left}"
        # Stale record of a reused pid (this python process): status degraded, start removes it without signalling.
        me = os.getpid()
        (state_c / "run/controller.json").write_text(json.dumps({"schema_version": "0.1", "role": "controller", "pid": me,
                                                                 "start_time": 1, "executable": str(fixture), "started_at": "2026-01-01T00:00:00Z"}))
        (state_c / "run/controller.json").chmod(0o600)
        degraded = cli(fixture, "status", "--config", cfg_c, "--format", "json", db="instance_c", expect=1)
        assert json.loads(degraded.stdout)["issues"] == ["stale_record"], degraded.stdout
        (fsrc / "codeye-fixture-mode").write_text("succeed\n")
        restarted = cli(fixture, "start", "--config", cfg_c, db="instance_c", timeout=200).stdout
        assert f"removed stale controller record (pid {me} pid_reused)" in restarted, restarted
        fresh = Session(state_c, origin_c)
        rerun = fresh.finished(fresh.enqueue(fproject["id"]))
        assert rerun["status"] in ("partial", "completed"), rerun
        old = fresh.request("GET", f"runs/{sleeping}")
        assert old["status"] in ("failed", "cancelled"), old
        stopped = json.loads(cli(fixture, "stop", "--config", cfg_c, "--format", "json", db="instance_c", timeout=200).stdout)
        assert stopped["outcome"] == "stopped", stopped
        print("PASS: drain 시간 초과 취소·정리 확인·남은 프로세스 없음·재사용 pid 기록 미신호·재시작 (시험용 handler)")

        # TC-124: an artifact quota already reached refuses new work with a structured
        # reason; the finished report stays readable and nothing is deleted.
        doc = json.loads(cfg_c.read_text())
        doc["quota"] = {"artifacts_bytes": 1}
        cfg_c.write_text(json.dumps(doc, indent=2))
        cli(fixture, "start", "--config", cfg_c, db="instance_c", timeout=200)
        fresh = Session(state_c, origin_c)
        blocked = fresh.enqueue(fproject["id"])
        wait_for("저장 상한 대기 사유", lambda: fresh.request("GET", f"runs/{blocked}")["progress"]["waiting_reason"] == "storage_quota_exceeded", 30)
        quota_status = json.loads(cli(fixture, "status", "--config", cfg_c, "--format", "json", db="instance_c").stdout)
        artifacts = next(s for s in quota_status["storage"] if s["area"] == "artifacts")
        assert artifacts["exceeded"] and artifacts["basis"] == "database_accounting" and quota_status["in_flight"] == [], quota_status
        assert fresh.request("GET", f"runs/{rerun['id']}")["report"] == rerun["report"]
        time.sleep(2)
        assert fresh.request("GET", f"runs/{blocked}")["status"] == "queued"
        stopped = json.loads(cli(fixture, "stop", "--config", cfg_c, "--format", "json", db="instance_c", timeout=200).stdout)
        assert stopped["outcome"] == "stopped" and stopped["in_flight_at_stop"] == [], stopped
        # A node area (logs, measured by the agent and sent in its heartbeat) at its
        # limit refuses the same queued run; dropping artifacts_bytes clears that quota.
        doc["quota"] = {"logs_bytes": 1}
        cfg_c.write_text(json.dumps(doc, indent=2))
        cli(fixture, "start", "--config", cfg_c, db="instance_c", timeout=200)
        fresh = Session(state_c, origin_c)

        def node_quota():
            status = json.loads(cli(fixture, "status", "--config", cfg_c, "--format", "json", db="instance_c").stdout)
            areas = {s["area"]: s for s in status["storage"]}
            return status if areas["logs"]["exceeded"] and not areas["artifacts"]["exceeded"] else None
        node_status = wait_for("노드 저장 영역 상한 보고", node_quota, 30)
        assert next(s for s in node_status["storage"] if s["area"] == "logs")["basis"] == "node_heartbeat", node_status
        assert fresh.request("GET", f"runs/{blocked}")["progress"]["waiting_reason"] == "storage_quota_exceeded"
        time.sleep(2)
        assert fresh.request("GET", f"runs/{blocked}")["status"] == "queued" and node_status["in_flight"] == []
        stopped = json.loads(cli(fixture, "stop", "--config", cfg_c, "--format", "json", db="instance_c", timeout=200).stdout)
        assert stopped["outcome"] == "stopped" and stopped["in_flight_at_stop"] == [], stopped
        no_secrets(state_c, [fixture_session.token, fresh.token])
        print("PASS: 산출물·노드 저장 영역(logs) 상한 도달 시 새 작업 거절(storage_quota_exceeded)·기존 보고서 유지")
        print("PASS: 상태 디렉터리 전체에 DB 인증정보 없음·로그에 로그인 토큰 없음·기본 설정 경로")
    finally:
        for exe, cfg, db in launched:
            subprocess.run([exe, "stop", "--config", str(cfg)], cwd=directory, env=dict(env, CODEYE_DATABASE_URL=urls[db]),
                           capture_output=True, timeout=200)
    managed(base, root, env, directory, source)
    paths_and_git(root, env, directory, fixture)
    pull_request(root, env, directory, fixture)


def bare_repository(mirror, host_path, files):
    """A bare repository at mirror/HOST/OWNER/REPO.git with one commit on main."""
    work = mirror.parent / ("work-" + host_path.replace("/", "-"))
    for name, text in files.items():
        (work / name).parent.mkdir(parents=True, exist_ok=True)
        (work / name).write_text(text)
    genv = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_AUTHOR_NAME="codeye", GIT_AUTHOR_EMAIL="codeye@example.invalid",
                GIT_COMMITTER_NAME="codeye", GIT_COMMITTER_EMAIL="codeye@example.invalid")
    for args in (["init", "-q", "-b", "main"], ["add", "-A"], ["commit", "-q", "-m", "init"]):
        subprocess.run(["git", *args], cwd=work, env=genv, check=True, capture_output=True, timeout=30)
    target = mirror / (host_path + ".git")
    target.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["git", "clone", "-q", "--bare", str(work), str(target)], env=genv, check=True, capture_output=True, timeout=30)


def paths_and_git(root, env, directory, fixture):
    """TC-185~188: a folder path and a GitHub address from one field, retry, tools, hints.

    The fixture build reads CODEYE_TEST_GIT_MIRROR: https://github.com/O/R is fetched from
    MIRROR/github.com/O/R.git over file:// (no network in CI). The real GitHub check is manual.
    """
    home = directory / "paths"
    home.mkdir(mode=0o700)
    binary = home / "codeye"
    shutil.copy2(fixture, binary)
    if (root / "web/dist/index.html").exists():
        (home / "web").mkdir()
        (home / "web/dist").symlink_to(root / "web/dist", target_is_directory=True)
    user_home = home / "h"
    folder = user_home / "code/hello"
    (folder / "pkg").mkdir(parents=True)
    (folder / "go.mod").write_text("module example.com/hello\n\ngo 1.22\n")
    (folder / "pkg/a.go").write_text('package pkg\n\nimport "fmt"\n\nfunc A() { fmt.Printf("%d\\n", "x") }\n')
    mirror = home / "mirror"
    go_files = {"go.mod": "module example.com/r\n\ngo 1.22\n", "r.go": 'package r\n\nimport "fmt"\n\nfunc R() { fmt.Printf("%d\\n", "x") }\n'}
    bare_repository(mirror, "github.com/o/r", go_files)
    rust = shutil.which("cargo") is not None
    web_files = dict(go_files)
    if rust:
        web_files.update({"crate/Cargo.toml": '[package]\nname = "c"\nversion = "0.1.0"\nedition = "2021"\n\n[dependencies]\n',
                          "crate/src/lib.rs": "pub fn f() -> i32 { 1 }\n"})
    bare_repository(mirror, "github.com/o/web", web_files)
    state = user_home / ".local/state/codeye"
    menv = {k: v for k, v in env.items() if k not in ("CODEYE_DATABASE_URL", "XDG_CONFIG_HOME", "XDG_STATE_HOME") and not k.startswith("PG")}
    menv.update(HOME=str(user_home), BROWSER="/bin/true", CODEYE_TEST_GIT_MIRROR=str(mirror))

    def cli(*args, expect=0, timeout=300):
        result = subprocess.run([binary, *map(str, args)], cwd=home, env=menv, capture_output=True, text=True, timeout=timeout)
        if expect is not None and result.returncode != expect:
            raise RuntimeError(f"{args[0]} 종료 코드 {result.returncode} (기대 {expect}): {(result.stdout + result.stderr)[-3000:]}")
        return result.stdout

    def link_of(text):
        line = next(line.strip() for line in text.splitlines() if "#launch=" in line)
        return line, line.split("/#", 1)[0]

    try:
        # TC-185: a folder under $HOME needs no new source root; a GitHub address without a branch.
        first = cli("up", "--no-open", folder)
        assert 'registered source root "home"' in first and 'registered project "hello"' in first, first
        repo = cli("up", "--no-open", "https://github.com/o/r")
        assert 'registered project "r" (https://github.com/o/r)' in repo and "queued an analysis" in repo, repo
        link, origin = link_of(repo)
        session = Session(state, origin, link)
        view = session.finished(link.rsplit("&run=", 1)[1], timeout=300)
        assert view["status"] in ("partial", "completed") and view["report"], view
        doc = session.request("GET", f"runs/{view['id']}/report?format=json&include_code=false")
        assert doc["inputs"]["source_kind"] == "git" and doc["inputs"]["commit_sha"], doc["inputs"]
        assert any(f["evidence"] for f in doc["findings"]), "Git 저장소의 go vet 발견 없음"
        projects = {p["name"]: p for p in session.request("GET", "projects?limit=100")["items"]}
        assert projects["r"]["analysis"] == {"send_source_to_model": True, "run_build_scripts": False, "fetch_dependencies": True}, projects["r"]
        assert projects["hello"]["analysis"] == {"send_source_to_model": True, "run_build_scripts": True, "fetch_dependencies": True}, projects["hello"]
        # TC-186: the home folder itself holds the state directory: refused, naming the folder.
        try:
            session.request("POST", "projects", {"local_path": str(user_home)})
            raise AssertionError("상태 폴더를 품은 폴더가 등록됨")
        except HTTPError as e:
            body = json.loads(e.read())
            assert e.code == 422 and body["error"]["code"] == "FOLDER_HOLDS_CODEYE_DATA" and str(user_home) in body["error"]["message"], body
        setup = session.request("GET", "setup")
        assert setup["is_admin"] and setup["available"]["local_path"]["available"] and setup["git"]["available"] and setup["tools"]["tools"], setup
        if (home / "web/dist").exists():
            # TC-187/188: the browser flows (path, Git, hints, retry, tools).
            browser_link, _ = link_of(cli("open", "--no-open"))
            link_file = home / "browser-link"
            link_file.write_text(browser_link + "\n")
            link_file.chmod(0o600)
            benv = dict(env, CODEYE_TEST_BASE_URL=origin, CODEYE_TEST_START_LINK_FILE=str(link_file), CODEYE_TEST_START_FOLDER=str(folder),
                        CODEYE_TEST_START_REPO="https://github.com/o/web", CODEYE_TEST_START_MISSING_REPO="https://github.com/o/missing")
            if rust:
                benv["CODEYE_TEST_START_BUILD_HINT"] = "1"
            subprocess.run(["npm", "run", "test:e2e", "--", "start.spec.ts", "--global-timeout=600000"], cwd=root / "web", check=True, timeout=660, env=benv)
        print("PASS: 경로·Git 주소 입력(홈 아래 폴더·Git 주소 기본 브랜치·프로젝트별 분석 선택·데이터 폴더 거절·도구·다시 시도·개선 안내)")
    finally:
        subprocess.run([binary, "stop"], cwd=home, env=menv, capture_output=True, timeout=300)



def git_env():
    return dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull, GIT_AUTHOR_NAME="codeye", GIT_AUTHOR_EMAIL="codeye@example.invalid",
                GIT_COMMITTER_NAME="codeye", GIT_COMMITTER_EMAIL="codeye@example.invalid")


def git(work, *args):
    return subprocess.run(["git", *args], cwd=work, env=git_env(), check=True, capture_output=True, text=True, timeout=30).stdout.strip()


def pull_files(work, base, head):
    """The changed files of base..head as GitHub lists them, with the patch hunks."""
    out = []
    for line in git(work, "diff", "--name-status", base, head).splitlines():
        status, name = line.split("\t", 1)
        entry = {"filename": name, "status": {"A": "added", "M": "modified", "D": "removed"}[status[0]]}
        diff = git(work, "diff", "--unified=3", base, head, "--", name)
        if "@@" in diff:
            entry["patch"] = diff[diff.index("@@"):]
        out.append(entry)
    return out


def pull_request(root, env, directory, fixture):
    """TC-227~229: a pull-request address, its pinned head, the one result comment and the dry run.

    The fixture build reads CODEYE_TEST_GIT_MIRROR (the repository and its refs/pull/7/head) and
    CODEYE_TEST_GITHUB_API (scripts/fake_github.py): no network. The test explicitly configures the platform token environment variable.
    """
    from fake_github import FakeGitHub

    token = "ghp_smoke" + os.urandom(12).hex()
    home = directory / "pull"
    home.mkdir(mode=0o700)
    binary = home / "codeye"
    shutil.copy2(fixture, binary)
    user_home = home / "h"
    user_home.mkdir(mode=0o700)
    mirror = home / "mirror"
    work = home / "work"
    work.mkdir()
    (work / "go.mod").write_text("module example.com/widget\n\ngo 1.22\n")
    (work / "old.go").write_text('package widget\n\nimport "fmt"\n\nfunc Old() { fmt.Printf("%d\\n", "old") }\n')
    (work / "gone.go").write_text("package widget\n\nfunc Gone() {}\n")
    (work / "parse.go").write_text("package widget\n\nfunc Parse(s string) string {\n\treturn s\n}\n")
    git(work, "init", "-q", "-b", "main")
    git(work, "add", "-A")
    git(work, "commit", "-q", "-m", "base")
    base = git(work, "rev-parse", "HEAD")
    git(work, "checkout", "-q", "-b", "feature")
    (work / "parse.go").write_text('package widget\n\nimport "fmt"\n\nfunc Parse(s string) string {\n\tfmt.Printf("%d\\n", s)\n\treturn s\n}\n')
    (work / "gone.go").unlink()
    git(work, "add", "-A")
    git(work, "commit", "-q", "-m", "change")
    head1 = git(work, "rev-parse", "HEAD")
    target = mirror / "github.com/acme/widget.git"
    target.parent.mkdir(parents=True)
    subprocess.run(["git", "clone", "-q", "--bare", "--branch", "main", str(work), str(target)], env=git_env(), check=True, capture_output=True, timeout=30)
    git(work, "push", "-q", str(target), f"{head1}:refs/pull/7/head")
    fake = FakeGitHub(token, "codeye-bot")
    fake.add_pull("acme/widget", 7, base_sha=base, head_sha=head1, files=pull_files(work, base, head1), head_repo="someone/widget")
    state = user_home / ".local/state/codeye"
    menv = {k: v for k, v in env.items() if k not in ("CODEYE_DATABASE_URL", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "GITHUB_TOKEN") and not k.startswith("PG")}
    menv.update(HOME=str(user_home), BROWSER="/bin/true", CODEYE_TEST_GIT_MIRROR=str(mirror), CODEYE_TEST_GITHUB_API=fake.url, CODEYE_TEST_PLATFORM_TOKEN=token)

    config = user_home / ".config/codeye/config.json"
    config.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
    config.write_text(json.dumps({"schema_version": "0.1", "github": {"token_env": {"github.com": "CODEYE_TEST_PLATFORM_TOKEN"}}}))
    config.chmod(0o600)

    def cli(*args, expect=0, timeout=300):
        result = subprocess.run([binary, *map(str, args)], cwd=home, env=menv, capture_output=True, text=True, timeout=timeout)
        text = result.stdout + result.stderr
        assert token not in text, f"{args[0]}: token 노출"
        if expect is not None and result.returncode != expect:
            raise RuntimeError(f"{args[0]} 종료 코드 {result.returncode} (기대 {expect}): {text[-3000:]}")
        return text

    def link_of(text):
        line = next(line.strip() for line in text.splitlines() if "#launch=" in line)
        return line, line.split("/#", 1)[0]

    def comment_state(session, run_id, want, timeout=120):
        def probe():
            c = session.request("GET", f"runs/{run_id}")["pull_request"]["comment"]
            return c if c["state"] == want else None
        return wait_for(f"run {run_id} 댓글 {want}", probe, timeout)

    try:
        # TC-227: up with a pull-request address and --comment.
        first = cli("up", "--no-open", "--comment", "https://github.com/acme/widget/pull/7")
        assert "github: platform account codeye-bot on github.com" in first, first
        assert f"queued an analysis of pull request #7 of acme/widget (feature from someone/widget, head {head1[:7]}" in first, first
        assert "comment: on" in first and "pull-request comments are now on" in first, first
        link, origin = link_of(first)
        session = Session(state, origin, link)
        run1 = link.rsplit("&run=", 1)[1]
        view = session.finished(run1, timeout=300)
        assert view["report"], json.dumps(view, ensure_ascii=False)[:3000]
        pr = view["pull_request"]
        assert pr["number"] == 7 and pr["head_sha"] == head1 and pr["base_sha"] == base and pr["head_repository"] == "someone/widget", pr
        doc = session.request("GET", f"runs/{run1}/report?format=json&include_code=false")
        assert doc["inputs"]["commit_sha"] == head1, doc["inputs"]
        s = doc["pull_request"]
        assert s["head_analyzed"] and s["files_removed"] == 1 and s["review"]["reviewable"] == 1 and s["line_basis"] == "lines", s
        # No model: the section says the changed files had static analysis only, not "0 sent".
        assert s["review"].get("sent_to_model") is None, s["review"]
        assert s["observations"]["changed_lines"] >= 1 and s["observations"]["elsewhere"] >= 1, s
        md = session.request("GET", f"runs/{run1}/report?format=markdown&include_code=false&detail=summary", raw=True)[0].decode()
        assert "pull request \\#7" in md and "같은 깊이로 검토하지 않았을 수 있다" in md, md[:2000]
        comment_state(session, run1, "posted")
        comments = fake.comment_list("acme/widget", 7)
        assert len(comments) == 1 and head1 in comments[0]["body"] and "parse.go" in comments[0]["body"], comments
        setup = session.request("GET", "setup")
        assert setup["github_connections"] == [{"enabled": True, "host": "github.com", "token": True, "account": "codeye-bot"}], setup["github_connections"]
        # TC-228: a push, then the same address again: a new run on the new head updates the one comment.
        (work / "parse.go").write_text('package widget\n\nimport "fmt"\n\nfunc Parse(s string) string {\n\tfmt.Printf("%d %d\\n", s, s)\n\treturn s\n}\n')
        git(work, "commit", "-q", "-am", "push")
        head2 = git(work, "rev-parse", "HEAD")
        git(work, "push", "-q", "-f", str(target), f"{head2}:refs/pull/7/head")
        fake.set_head("acme/widget", 7, head2, pull_files(work, base, head2))
        second = cli("up", "--no-open", "https://github.com/acme/widget/pull/7")
        assert f"head {head2[:7]}" in second and "comment: on" in second, second
        run2 = second.split("(run ", 1)[1].split(")", 1)[0]
        assert run2 != run1
        session.finished(run2, timeout=300)
        comment_state(session, run2, "posted")
        comments = fake.comment_list("acme/widget", 7)
        assert len(comments) == 1 and head2 in comments[0]["body"] and head1 not in comments[0]["body"], comments
        cmp = session.request("GET", f"runs/{run2}/comparison")
        assert cmp["baseline"]["run_id"] == run1, cmp["baseline"]
        # TC-229: the dry run posts nothing, keeps the remembered choice and prints the comment.
        writes = fake.writes()
        dry = cli("up", "--no-open", "--dry-run-comment", "https://github.com/acme/widget/pull/7", timeout=420)
        assert "comment: dry run (nothing is posted)" in dry and "----- pull-request comment preview (not posted) -----" in dry and "<!-- codeye:pull-request-comment project=" in dry, dry[-3000:]
        assert fake.writes() == writes and len(fake.comment_list("acme/widget", 7)) == 1, "dry run이 게시함"
        projects = {p["name"]: p for p in session.request("GET", "projects?limit=100")["items"]}
        assert projects["widget"]["analysis"].get("comment_on_pull_request") is True, projects["widget"]["analysis"]
        assert all(authorized for _, _, authorized in fake.requests), "token 없는 GitHub 호출"
        credential = state / "secrets/github-platform.json"
        assert token.encode() in credential.read_bytes()
        assert stat.S_IMODE(credential.stat().st_mode) == 0o600
        assert stat.S_IMODE(credential.parent.stat().st_mode) == 0o700
        for path in state.rglob("*"):
            if path.is_file() and not path.is_symlink() and path != credential:
                assert token.encode() not in path.read_bytes(), f"token이 다른 상태 파일에 있음: {path.relative_to(state)}"
        print("PASS: pull request 주소·head 고정·변경 보고서·댓글 갱신·dry run·플랫폼 인증정보")
    finally:
        subprocess.run([binary, "stop"], cwd=home, env=menv, capture_output=True, timeout=300)
        fake.close()

def managed(base, root, env, directory, source):
    """TC-170~173: codeye up with no configuration file and the managed PostgreSQL.

    A fresh HOME without XDG variables: the configuration and the state go to
    their default paths and no command gets --config. The PostgreSQL binaries
    come from PATH discovery (no flag, no config). The harness database URL
    and PG* variables in the environment are ignored. A stub $BROWSER records
    the link it is asked to open.
    """
    home = directory / "managed"
    home.mkdir(mode=0o700)
    # A copy next to a web/dist link: up finds the web app next to its executable.
    binary = home / "codeye"
    shutil.copy2(base / "codeye", binary)
    if (root / "web/dist/index.html").exists():
        (home / "web").mkdir()
        (home / "web/dist").symlink_to(root / "web/dist", target_is_directory=True)
    user_home = home / "h"
    user_home.mkdir(mode=0o700)
    config = user_home / ".config/codeye/config.json"
    state = user_home / ".local/state/codeye"
    opened = home / "browser-link"
    stub = home / "stub-browser"
    stub.write_text(f'#!/bin/sh\nprintf "%s\\n" "$1" > {opened}\n')
    stub.chmod(0o700)
    menv = {k: v for k, v in env.items() if k not in ("CODEYE_DATABASE_URL", "XDG_CONFIG_HOME", "XDG_STATE_HOME")}
    menv.update(HOME=str(user_home), CODEYE_DATABASE_URL=env["CODEYE_DATABASE_URL"], BROWSER=str(stub),
                PGPASSWORD="ignored-by-managed-mode", PGHOST="/nonexistent")
    second = home / "src-b"
    (second / "pkg").mkdir(parents=True)
    (second / "go.mod").write_text("module example.com/b\n\ngo 1.22\n")
    (second / "pkg/b.go").write_text('package pkg\n\nimport "fmt"\n\nfunc B() { fmt.Println("%d", "x") }\n')

    def cli(*args, expect=0, timeout=300):
        result = subprocess.run([binary, *map(str, args)], cwd=home, env=menv, capture_output=True, text=True, timeout=timeout)
        text = result.stdout + result.stderr
        assert env["CODEYE_DATABASE_URL"] not in text and "ignored-by-managed-mode" not in text, f"{args[0]}: credential 노출"
        if expect is not None and result.returncode != expect:
            raise RuntimeError(f"{args[0]} 종료 코드 {result.returncode} (기대 {expect}): {text[-3000:]}")
        return result

    def link_of(text):
        line = next(line.strip() for line in text.splitlines() if "#launch=" in line)
        return line, line.split("/#", 1)[0]

    def finished(session, run_id):
        view = session.finished(run_id, timeout=300)
        assert view["status"] in ("partial", "completed") and view["report"], view
        return view

    def postgres_processes():
        found = []
        for proc in Path("/proc").iterdir():
            try:
                if proc.name.isdigit() and str(state / "postgres/data").encode() in (proc / "cmdline").read_bytes():
                    found.append(proc.name)
            except OSError:
                pass
        return found

    try:
        # TC-170: no configuration, no database: one command to a queued run and a signed-in link.
        first = cli("up", source / "hello").stdout
        for want in ("wrote a new configuration: " + str(config), "initialized a PostgreSQL", "database schema created", "created the administrator user",
                     "registered project \"hello\"", "queued an analysis", "note: ignoring CODEYE_DATABASE_URL, PGHOST, PGPASSWORD",
                     "opened the browser with " + str(stub)):
            assert want in first, (want, first)
        written = json.loads(config.read_text())
        assert written == {"schema_version": "0.1"}, written
        assert stat.S_IMODE(config.stat().st_mode) == 0o600 and stat.S_IMODE((state / "postgres/password").stat().st_mode) == 0o600
        up_link, origin = link_of(first)
        assert "&project=" in up_link and "&run=" in up_link, up_link
        assert wait_for("브라우저 stub 호출", lambda: opened.exists() and opened.read_text().strip(), 10) == up_link
        run_a = up_link.rsplit("&run=", 1)[1]
        status = json.loads(cli("status", "--format", "json").stdout)
        assert status["state"] == "running" and status["database"]["postgres"] == "running", status
        assert status["database"]["port"] >= 55432, status
        # A separate link for the API checks: the up link stays unused for the browser.
        link, _ = link_of(cli("open", "--no-open").stdout)
        session = Session(state, origin, link)
        assert session.request("GET", "session")["expires_at"] > time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 6 * 86400)), "7일 세션 아님"
        finished(session, run_a)
        if (home / "web/dist").exists():
            # TC-171: the printed link signs the browser in and opens the project's run and report.
            link_file = home / "up-link"
            link_file.write_text(up_link + "\n")
            link_file.chmod(0o600)
            subprocess.run(["npm", "run", "test:e2e", "--", "up.spec.ts"], cwd=root / "web", check=True, timeout=180,
                           env=dict(env, CODEYE_TEST_BASE_URL=origin, CODEYE_TEST_UP_LINK_FILE=str(link_file), CODEYE_TEST_UP_PROJECT="hello"))

        # Another folder is analyzed without changing the configuration or restarting the agent.
        again = cli("up", "--no-open", second).stdout
        assert "registered project" in again and "restarting the agent" not in again, again
        link_b, _ = link_of(again)
        finished(Session(state, origin, link_b), link_b.rsplit("&run=", 1)[1])

        # TC-173: stop also stops PostgreSQL; up reuses the cluster; backup needs no configuration.
        stopped = json.loads(cli("stop", "--format", "json").stdout)
        assert stopped["outcome"] == "stopped" and stopped["database"] == "stopped", stopped
        assert not postgres_processes(), "정지 뒤 PostgreSQL이 남음"
        reused = cli("up", "--no-open").stdout
        assert "initialized" not in reused and "created the administrator user" not in reused and "PostgreSQL" in reused, reused
        link, origin = link_of(reused)
        names = {p["name"] for p in Session(state, origin, link).request("GET", "projects?limit=100")["items"]}
        assert names == {"hello", "src-b"}, names
        stopped = json.loads(cli("stop", "--format", "json").stdout)
        assert stopped["outcome"] == "stopped" and stopped["database"] == "stopped", stopped
        made = cli("backup", "--format", "json").stdout
        out = Path(json.loads(made)["output"])
        assert out.parent == user_home / ".local/state/codeye-backups" and (out / "manifest.json").exists(), made
        assert not postgres_processes(), "백업이 띄운 PostgreSQL이 남음"
        # Only the generated password file holds the database credential.
        password = (state / "postgres/password").read_text().strip()
        for path in state.rglob("*"):
            if path.is_file() and not path.is_symlink() and path != state / "postgres/password":
                assert password.encode() not in path.read_bytes(), f"DB 비밀번호가 다른 파일에 있음: {path.relative_to(state)}"
        print("PASS: codeye up 관리형 PostgreSQL(설정 없음→보고서·로그인 링크·7일 세션)·실행 중 새 폴더·stop이 PostgreSQL 정지·재사용·설정 없는 백업")
    finally:
        subprocess.run([binary, "stop"], cwd=home, env=menv, capture_output=True, timeout=300)
