"""임시 PostgreSQL과 서로 다른 노드 두 개를 사용하는 Linux Native 수용 시험."""
from contextlib import ExitStack, contextmanager
from http.cookiejar import CookieJar
import json
import hashlib
import shutil
import os
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


def without_clock(activity):
    return {key: value for key, value in activity.items() if key != "checked_at"}


def activity_agrees(session, project_id):
    """W15 (TC-98): the project list, project detail and run detail show the
    same representative run and stages, and all match the OpenAPI contract."""
    import validate_bundle
    order = validate_bundle.stage_vocabulary()[0]
    listed = [p for p in session.request("GET", "projects?limit=100")["items"] if p["id"] == project_id]
    detail = session.request("GET", f"projects/{project_id}")
    assert len(listed) == 1, "프로젝트 목록 누락"
    project_schema = validate_bundle.api_validator("Project")
    project_schema.validate(listed[0])
    project_schema.validate(detail)
    activity = detail["activity"]
    assert without_clock(listed[0]["activity"]) == without_clock(activity), "목록과 상세의 단계 표시 불일치"
    validate_bundle.check_activity(activity, order)
    if activity["run"] is not None:
        run = session.request("GET", f"runs/{activity['run']['id']}")
        validate_bundle.api_validator("RunView").validate(run)
        progress = run["progress"]
        assert (progress["detail"], progress["stages"], progress["last_event_at"]) == (activity["detail"], activity["stages"], activity["last_event_at"]), "실행 상세와 프로젝트 단계 불일치"
        assert run["status"] == activity["run"]["status"] == progress["stage"]
    return activity


def stage_map(activity):
    return {stage["key"]: stage for stage in activity["stages"]}


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


def run(base, root, env, *, browser=False):
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
            [binary, "serve", "--listen", f"127.0.0.1:{port}", "--artifact-dir", directory / "artifacts", "--web-dir", root / "web/dist"],
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
        # W15: the real worker's planned stage is shown once the controller has
        # settled the cancelled run's report (published or refused).
        def report_settled(run):
            return scalar(f"SELECT (SELECT count(*) FROM reports WHERE run_id='{run}')+(SELECT count(*) FROM report_rejections WHERE run_id='{run}')") != "0"
        wait_for("취소 run 보고서 처리", lambda: report_settled(run_id), children)
        cancelled = activity_agrees(session, project["id"])
        stages = stage_map(cancelled)
        assert cancelled["run"]["id"] == run_id and cancelled["run"]["status"] == "cancelled" and cancelled["detail"] == "recorded", cancelled
        assert stages["source"]["state"] == "succeeded" and stages["report"]["state"] in ("succeeded", "cancelled"), stages
        assert stages["static_analysis"]["state"] == "cancelled" and stages["static_analysis"]["counts"] == {"cancelled": 1}, stages

        mode.write_text("succeed\n")
        next_run = session.enqueue(project["id"])
        def finished():
            state = session.request("GET", f"runs/{next_run}")
            return state if state["status"] == "partial" and state["snapshot_id"] else None
        final = wait_for("작업 결과 접수", finished, children)
        assert final["progress"]["completed_tasks"] >= 1
        assert scalar(f"SELECT count(*) FROM attempts WHERE run_id='{next_run}' AND status='released'") == "1"
        assert mode.read_text() == "succeed\n"
        done = activity_agrees(session, project["id"])
        stages = stage_map(done)
        assert done["run"]["id"] == next_run and done["other_active_runs"] == 0 and done["last_report"]["run_id"] == next_run, done
        analysis = stages["static_analysis"]
        assert (analysis["state"], analysis["planned"], analysis["finished"], analysis["succeeded"]) == ("succeeded", 1, 1, 1), analysis
        assert stages["source"]["state"] == stages["report"]["state"] == "succeeded" and analysis["started_at"] and analysis["finished_at"], stages
        print("PASS: W15 실제 워커 단계 기록·목록/상세/실행 상세 일치·취소 단계 (시험용 handler)")
        # 별도 root를 등록해 시험용 handler가 실제 분석 요청을 가져갈 수 없게 한다.
        go_source = directory / "go-project"
        shutil.copytree(root / "fixtures" / "go-vet", go_source)
        original = (go_source / "example.go").read_text()
        go_root = json.loads(cli("source-root", "add", "--label", "go-smoke", "--path", go_source))["id"]
        cli("source-root", "grant", "--root", go_root, "--user", user)
        go_token = directory / "go.token"
        cli("node", "token", "create", "--pool", "default", "--source-root", go_root, "--output", go_token)
        go_project = session.request("POST", "projects", {"name": "published-go-report", "source": {
            "kind": "local", "source_root_id": go_root, "relative_path": "."}})
        production = services.enter_context(service(
            [binary, "agent", "--controller", origin, "--state-dir", directory / "go-agent",
             "--name", "go-smoke", "--enroll-token-file", go_token, "--source-root", f"{go_root}={go_source}",
             "--go", shutil.which("go"), "--autoscale", "--max-workers", "1"], directory, node_env, directory / "go.log"))
        children.append(production)
        actual = session.enqueue(go_project["id"])
        def observations_key():
            state = session.request("GET", f"runs/{actual}")["status"]
            if state in {"failed", "cancelled", "recovery_required"}:
                raise RuntimeError(f"Go 분석 run 상태 {state}")
            if state not in {"finalizing", "partial", "completed"}:
                return None
            return scalar(f"SELECT storage_key FROM artifacts WHERE run_id='{actual}' AND role='observations'")
        key = wait_for("실제 Go vet 산출물", observations_key, children, timeout=140)
        data = (directory / "artifacts" / key).read_bytes()
        assert hashlib.sha256(data).hexdigest() == key.split("/")[-1]
        doc = json.loads(data)
        assert doc["analyzer_id"] == "go.vet"
        assert any(o["rule_id"] == "printf" and o["location"]["path"] == "example.go" for o in doc["observations"])
        assert (go_source / "example.go").read_text() == original
        # 실제 워커가 생성한 서로 다른 두 manifest와 도구 산출물을 함께 검증한다.
        def published():
            state = session.request("GET", f"runs/{actual}")
            if state["status"] in {"failed", "recovery_required", "cancelled"}:
                raise RuntimeError(f"Go 보고서 게시 실패 ({state['status']})")
            return state["report"] if state["status"] == "partial" else None
        revision = wait_for("실제 Go vet 보고서 게시", published, children)
        assert revision["revision"] == 1
        # The product handlers' stages arrive with W15 discovery (track D);
        # until then this run is "not_recorded". Either way list/detail agree.
        go_activity = activity_agrees(session, go_project["id"])
        assert go_activity["run"]["id"] == actual and go_activity["last_report"]["run_id"] == actual and go_activity["last_report"]["revision"] == 1
        if go_activity["detail"] == "recorded":
            go_stages = stage_map(go_activity)
            assert go_stages["static_analysis"]["planned"] and go_stages["report"]["state"] == "succeeded", go_stages
        print(f"PASS: W15 실제 Go run 단계 표시 detail={go_activity['detail']} (목록/상세 일치)")
        path = f"runs/{actual}/report?revision=1"
        code_free = session.request("GET", path)
        full = session.request("GET", path + "&include_code=true")
        assert code_free["summary"] == full["summary"]
        assert full["summary"]["raw_observations"] == len(doc["observations"])
        assert full["summary"]["pending_candidates"] > 0 and full["summary"]["actionable_findings"] == 0
        assert full["coverage"]["ai_review"]["status"] == "not_performed"
        assert full["quality_verdict"] == "inconclusive"
        assert all(o["raw_message"] is None for o in code_free["observations"])
        assert any(o["raw_message"] and o["evidence_state"] == "verified_against_source_manifest"
                   for o in full["observations"])
        import validate_bundle
        schema = validate_bundle.make_validators()["report.schema.json"]
        schema.validate(code_free)
        schema.validate(full)
        for fmt in ("markdown", "html"):
            with session.client.open(origin + "/api/v1/" + path + "&format=" + fmt, timeout=5) as response:
                rendered = response.read()
                assert response.headers["X-CODEYE-Report-SHA256"].encode() in rendered
                assert response.headers["ETag"] == '"' + hashlib.sha256(rendered).hexdigest() + '"'
        if browser:
            import browser_smoke
            # The report may belong to an older project beyond the initial page.
            for i in range(21):
                session.request("POST", "projects", {"name": f"browser-page-{i}", "source": {
                    "kind": "local", "source_root_id": source_id, "relative_path": "."}})
            browser_link = directory / "report-login"
            cli("login-link", "--user", user, "--output", browser_link, "--origin", origin)
            browser_smoke.published_report(root, env, origin, browser_link, "published-go-report", actual)
        print("PASS: 실제 워커→검증된 보고서 revision, 미실행 AI 검토, 코드 제외·포함과 3개 형식")
        print("PASS: 자동 증감 제품 워커·실제 Go vet·printf 관찰·산출물 무결성·원본 보존")
    print("PASS: 두 에이전트 등록·단일 attempt·취소 및 정리·후속 run 결과 접수 (시험용 handler)")
