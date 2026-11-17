"""임시 PostgreSQL과 서로 다른 노드 두 개를 사용하는 Linux Native 수용 시험."""
from contextlib import ExitStack, contextmanager
from http.cookiejar import CookieJar
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import time
from urllib.error import URLError
from urllib.parse import urlsplit
from urllib.request import build_opener, HTTPCookieProcessor, Request
from uuid import UUID, uuid4


class Session:
    def __init__(self, origin):
        self.origin = origin
        self.client = build_opener(HTTPCookieProcessor(CookieJar()))
        self.csrf = None

    def request(self, method, path, payload=None, headers=None):
        fields = dict(headers or {})
        if method != "GET":
            fields["Origin"] = self.origin
            fields["Content-Type"] = "application/json"
            if self.csrf:
                fields["X-CODEYE-CSRF"] = self.csrf
        data = json.dumps(payload).encode() if payload is not None else None
        req = Request(self.origin + "/api/v1/" + path, method=method, data=data, headers=fields)
        with self.client.open(req, timeout=5) as response:
            return json.load(response)

    def enqueue(self, project_id):
        body = {"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": {"kind": "full"}}
        ack = self.request("POST", f"projects/{project_id}/runs", body, {"Idempotency-Key": str(uuid4())})
        assert ack["status"] == "queued"
        return str(UUID(ack["id"]))


def wait_for(description, probe, children, timeout=60):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if any(child.poll() is not None for child in children):
            raise RuntimeError(f"{description} 도중 프로세스 종료")
        result = probe()
        if result:
            return result
        time.sleep(0.1)
    raise RuntimeError(f"{description} 시간 초과")


@contextmanager
def service(argv, directory, env, log_path):
    with log_path.open("wb") as log:
        child = subprocess.Popen(argv, cwd=directory, env=env, stdin=subprocess.DEVNULL,
                                 stdout=log, stderr=log, start_new_session=True)
        try:
            yield child
        finally:
            if child.poll() is None:
                child.send_signal(signal.SIGINT)
            try:
                code = child.wait(timeout=35)
            except subprocess.TimeoutExpired:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
                raise RuntimeError(f"{log_path.stem} 정상 종료 시간 초과") from None
            if code != 0:
                raise RuntimeError(f"{log_path.stem} 비정상 종료 ({code})")


def run(base, root, env):
    directory = base / "node-smoke"
    directory.mkdir(mode=0o700)
    binary = base / "codeye"
    fixture = directory / "codeye-fixture"
    subprocess.run(["go", "build", "-tags", "codeye_fixture", "-o", fixture, "./cmd/codeye"],
                   cwd=root, env=env, check=True, timeout=120)

    def cli(*args):
        result = subprocess.run([binary, *args], cwd=directory, env=env,
                                capture_output=True, timeout=15)
        if result.returncode:
            raise RuntimeError(f"{args[0]} 명령 실패 ({result.returncode})")
        return result.stdout

    source = directory / "input"
    source.mkdir()
    mode = source / "codeye-fixture-mode"
    mode.write_text("sleep\n")
    user = json.loads(cli("user", "create", "--name", "Node fixture"))["id"]
    source_id = json.loads(cli("source-root", "add", "--label", "node-smoke", "--path", source))["id"]
    cli("source-root", "grant", "--root", source_id, "--user", user)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    session = Session(origin)

    def ready():
        try:
            return session.request("GET", "ready")
        except URLError:
            return None

    # 진단용 읽기 쿼리도 임시 DB에만 연결한다. 비밀번호는 argv에 넣지 않는다.
    db = urlsplit(env["CODEYE_TEST_DATABASE_URL"])
    sql_env = dict(env, PGHOST=db.hostname, PGPORT=str(db.port), PGDATABASE=db.path[1:],
                   PGUSER=db.username, PGPASSWORD=db.password, PGSSLMODE="disable")

    def scalar(sql):
        return subprocess.check_output(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-At", "-c", sql],
                                       env=sql_env, text=True, timeout=5).strip()

    with ExitStack() as services:
        children = [services.enter_context(service(
            [binary, "serve", "--listen", f"127.0.0.1:{port}", "--artifact-dir", directory / "artifacts"],
            directory, env, directory / "controller.log"))]
        wait_for("controller 준비", ready, children)
        login = directory / "login"
        cli("login-link", "--user", user, "--output", login, "--origin", origin)
        token = login.read_text().strip().split("#launch=", 1)[1]
        session.csrf = session.request("POST", "session", {"launch_token": token})["csrf_token"]
        project = session.request("POST", "projects", {"source": {
            "kind": "local", "source_root_id": source_id, "relative_path": "."}})
        # 에이전트·워커에는 관리 DB 연결 정보를 주지 않는다.
        node_env = {key: env[key] for key in ("PATH", "LANG") if key in env}
        for name in ("first", "second"):
            enrollment = directory / (name + ".token")
            cli("node", "token", "create", "--pool", "default", "--source-root", source_id,
                "--output", enrollment)
            child = services.enter_context(service(
                [fixture, "agent", "--controller", origin, "--state-dir", directory / name, "--name", name,
                 "--enroll-token-file", enrollment, "--source-root", f"{source_id}={source}", "--workers", "1"],
                directory, node_env, directory / (name + ".log")))
            children.append(child)
        wait_for("두 노드의 워커 등록", lambda: scalar("SELECT count(DISTINCT node_id) FROM workers WHERE state='idle'") == "2", children)

        run_id = session.enqueue(project["id"])
        lookup = f"runs/{run_id}"
        wait_for("취소 가능한 실행", lambda: session.request("GET", lookup)["status"] == "running", children)
        session.request("POST", lookup + "/cancel")
        wait_for("실행 취소 확인", lambda: session.request("GET", lookup)["status"] == "cancelled", children)
        assert scalar(f"SELECT number || ':' || epoch || ':' || cleanup_state FROM attempts WHERE run_id='{run_id}'") == "1:1:confirmed"
        assert mode.read_text() == "sleep\n"

        mode.write_text("succeed\n")
        next_run = session.enqueue(project["id"])
        def finished():
            state = session.request("GET", f"runs/{next_run}")
            return state if state["status"] == "finalizing" and state["snapshot_id"] else None
        final = wait_for("작업 결과 접수", finished, children)
        assert final["progress"]["completed_tasks"] >= 1
        assert scalar(f"SELECT count(*) FROM attempts WHERE run_id='{next_run}' AND status='released'") == "1"
        assert mode.read_text() == "succeed\n"
    print("PASS: 두 에이전트 등록·단일 attempt·취소 및 정리·후속 run 결과 접수 (시험용 handler)")
