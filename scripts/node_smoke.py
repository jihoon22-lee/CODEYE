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

    def enqueue(self, project_id, scope_paths=None):
        """Queue a deep-review run; scope_paths limits it to those directories (scope kind paths)."""
        scope = {"kind": "paths", "paths": list(scope_paths)} if scope_paths else {"kind": "full"}
        body = {"profile": "deep-review", "source_policy": "latest_at_acquire", "scope": scope}
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
    """TC-98: the project list, project detail and run detail show the
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


def stream_agrees(session, run_id):
    """TC-99: the real run's event stream replays its stored events in
    sequence with allowlisted fields only, then the same RunView as
    GET /runs/{id}; the plan fields follow the stored tasks."""
    import validate_bundle
    run = session.request("GET", f"runs/{run_id}")
    validate_bundle.api_validator("RunView").validate(run)
    row_schema = validate_bundle.api_validator("RunEventData")
    allowed = set(validate_bundle.yaml_file(validate_bundle.ROOT / "api/openapi.yaml")["components"]["schemas"]["RunEventData"]["x-codeye-payload-fields"])
    head = run["event_sequence"]
    start = max(0, head - 1000)  # the server's default replay window
    req = Request(session.origin + f"/api/v1/runs/{run_id}/events?cursor={start}", headers={"Accept": "text/event-stream"})
    rows, view, fields = [], None, {}
    with session.client.open(req, timeout=10) as response:
        assert response.headers.get_content_type() == "text/event-stream" and response.headers["Cache-Control"] == "no-store"
        for raw in response:
            line = raw.decode("utf-8").rstrip("\n")
            if line:
                key, _, value = line.partition(": ")
                fields[key] = value
                continue
            message, fields = fields, {}
            if "data" not in message:
                continue  # the retry: hint
            data = json.loads(message["data"])
            event = message.get("event")
            if event is None:
                row_schema.validate(data)
                assert int(message["id"]) == data["sequence"] == start + len(rows) + 1, "SSE 이벤트 id 불연속"
                assert set(data) - {"run_id", "sequence", "version", "type", "at"} <= allowed, sorted(data)
                rows.append(data)
            elif event == "run.view":
                assert "id" not in message, "run.view에 id"
                view = data
                if rows and rows[-1]["sequence"] >= head:
                    break
            else:
                assert event == "heartbeat" and "id" not in message, message
    assert rows and rows[-1]["sequence"] >= head and view["event_sequence"] >= head, "SSE가 저장된 이벤트를 모두 전달하지 않음"
    types = [r["type"] for r in rows]
    assert "task.created" in types and "task.status" in types, types
    assert start > 0 or types[0] == "run.queued", types
    again = session.request("GET", f"runs/{run_id}")
    strip = lambda v: {**v, "progress": {k: x for k, x in v["progress"].items() if k != "checked_at"}}
    assert strip(view) == strip(again), "SSE run.view와 GET 불일치"
    p = view["progress"]
    # Product handlers declare no weights: the percentage is the finished task ratio.
    assert p["plan_revision"] is not None and p["weight_basis"] == "task_count" and p["planned_weight"] == p["total_tasks"], p
    assert p["finished_weight"] == p["planned_weight"] and p["estimated_percent"] == 100, p
    return len(rows), p["plan_revision"]


def published_report(session, run_id):
    state = session.request("GET", f"runs/{run_id}")
    if state["status"] in {"failed", "recovery_required", "cancelled"}:
        raise RuntimeError(f"Go 보고서 게시 실패 ({state['status']})")
    return state["report"] if state["status"] == "partial" else None


def comparison_history(session, project_id, first_run, first_report, go_source, original, publish):
    """REQ-028, TC-14 with the real go vet worker: a review recorded on
    run 1 is stamped into run 2's publication; moved lines persist, a new
    diagnostic is new; the fixed diagnostic is resolved_observed only because
    go vet succeeded again and analyzed example.go. Schemas: OpenAPI."""
    import validate_bundle
    comparison_schema = validate_bundle.api_validator("RunComparison")

    def compare(run_id, query=""):
        doc = session.request("GET", f"runs/{run_id}/comparison{query}")
        comparison_schema.validate(doc)
        return doc

    first = compare(first_run)
    assert first["baseline"] is None and first["not_comparable_reason"] == "no_baseline" and first["entries"] == [], first
    printf = [f for f in first_report["findings"] if f["fingerprint_basis"]["rule"] == "printf"]
    assert len(printf) == 1 and printf[0]["fingerprint"].startswith("fp2:"), first_report["findings"]
    original_fp = printf[0]["fingerprint"]
    review = {"fingerprint": original_fp, "disposition": "accepted_risk", "reason": "smoke: 의도된 형식 문자열",
              "run_id": first_run, "revision": 1, "finding_id": printf[0]["id"]}
    recorded = session.request("POST", f"projects/{project_id}/finding-reviews", review,
                               {"Idempotency-Key": str(uuid4()), "If-None-Match": "*"})
    assert recorded["version"] == 1 and recorded["disposition"] == "accepted_risk", recorded

    # Run 2: two lines inserted above the diagnostic and a second, different printf misuse.
    (go_source / "example.go").write_text(original.replace("func Print()", "// shifted\n// retains the original diagnostic.\nfunc Print()")
                                           + '\nfunc Count() { fmt.Printf("%s %s", "x") }\n')
    second = session.enqueue(project_id)
    assert publish(second)["revision"] == 1
    assert session.request("GET", f"runs/{second}/reports")["default_baseline"]["run_id"] == first_run
    c = compare(second)
    assert c["baseline"]["run_id"] == first_run and c["baseline"]["selection"] == "default" and c["comparable"], c
    assert c["counts"] == {"new": 1, "persisting": 1, "changed": 0, "absent_unverified": 0, "resolved_observed": 0}, c["counts"]
    moved = next(e for e in c["entries"] if e["fingerprint"] == original_fp)
    assert moved["state"] == "persisting" and moved["changes"] == ["lines"], moved
    assert moved["current"][0]["start_line"] == moved["baseline"][0]["start_line"] + 2, moved
    stamped = next(f for f in c["findings"] if f["fingerprint"] == original_fp)
    assert stamped["publication_disposition"] == stamped["current_disposition"] == "accepted_risk" and stamped["review_match"] == "applied", stamped
    report2 = session.request("GET", f"runs/{second}/report?include_code=true")
    assert report2["summary"]["hidden_findings"] == 1 and report2["summary"]["total_findings"] == 2, report2["summary"]
    code_free = session.request("GET", f"runs/{second}/report")
    assert all("fingerprint" not in f and "fingerprint_basis" not in f for f in code_free["findings"])

    # Run 3: the original misuse fixed; the second one stays.
    (go_source / "example.go").write_text((go_source / "example.go").read_text().replace('Printf("%d", "sample")', 'Printf("%s", "sample")'))
    third = session.enqueue(project_id)
    assert publish(third)["revision"] == 1
    c = compare(third)
    assert c["baseline"]["run_id"] == second and c["counts"]["resolved_observed"] == 1 and c["counts"]["persisting"] == 1, c["counts"]
    resolved = next(e for e in c["entries"] if e["state"] == "resolved_observed")
    assert resolved["fingerprint"] == original_fp and resolved["reason"] is None and resolved["producer"] == "tool", resolved
    c = compare(third, f"?baseline={first_run}")
    assert c["baseline"]["selection"] == "explicit" and c["counts"]["resolved_observed"] == 1 and c["counts"]["new"] == 1, c["counts"]
    (go_source / "example.go").write_text(original)
    print("PASS: 실제 go vet 세 run 비교(줄 이동 persisting·새 진단 new·수정 resolved_observed)·게시 시점 판정·코드 제외 식별 없음")


def secrets_pack(session, cli, services, children, directory, binary, origin, user, node_env, sql_env, scalar):
    """TC-119, docs/ANALYZERS.md through the agent, the controller and PostgreSQL
    with the real lizard, jscpd (on node) and gitleaks: the structure metrics
    are no findings, the secret generated here is an unreviewed detection, and
    its value is in no uploaded artifact (streams included), report revision,
    export, log, DB row or worker scratch. Runs only when CODEYE_TEST_LIZARD,
    CODEYE_TEST_JSCPD, CODEYE_TEST_NODE and CODEYE_TEST_GITLEAKS name the tools."""
    names = ("lizard", "jscpd", "node", "gitl" + "eaks")
    tools = {name: os.environ.get("CODEYE_TEST_" + name.upper(), "") for name in names}
    if not all(tools.values()):
        print("SKIP: 실도구 smoke (CODEYE_TEST_LIZARD·JSCPD·NODE·GITLEAKS 중 미설정)")
        return
    import base64
    import secrets as entropy
    secret = "AKIA" + base64.b32encode(entropy.token_bytes(10)).decode()  # fake, generated now
    body = secret[4:]
    forms = [f.encode() for f in (secret, body, body.lower())] + [secret.encode().hex().encode(), body.encode().hex().encode()]

    def absent(where, data):
        assert not any(f in data for f in forms), "비밀값 노출: " + where

    source = directory / "secrets-project"
    branches = "".join(f"    if v == {i}:\n        return {i}\n" for i in range(1, 17))
    block = ("function alpha(items, token) {\n  const key = \"" + secret + "\";\n  let total = 0;\n  for (const item of items) {\n"
             "    if (item.active && item.value > 10) {\n      total += item.value * 2;\n    } else if (item.value < 0) {\n"
             "      total -= item.value;\n    } else {\n      total += 1;\n    }\n  }\n"
             "  const label = \"total:\" + total + \":\" + key.length;\n  console.log(label, token);\n"
             "  return { total, label, count: items.length };\n}\nmodule.exports = { alpha };\n")
    files = {"src/app.py": "import os\n\nAWS_KEY = \"" + secret + "\"\n",
             "src/complex.py": "def route(v, token=\"" + secret + "\"):\n" + branches + "    return 0\n",
             "src/a.js": block, "lib/b.js": block}
    for rel, text in files.items():
        (source / rel).parent.mkdir(parents=True, exist_ok=True)
        (source / rel).write_text(text)
    root_id = json.loads(cli("source-root", "add", "--label", "secrets-smoke", "--path", source))["id"]
    cli("source-root", "grant", "--root", root_id, "--user", user)
    token = directory / "secrets.token"
    cli("node", "token", "create", "--pool", "default", "--source-root", root_id, "--output", token)
    tool_args = [arg for name in names for arg in ("--" + name, tools[name])]
    state = directory / "secrets-agent"
    children.append(services.enter_context(service(
        [binary, "agent", "--controller", origin, "--state-dir", state, "--name", "secrets-smoke", "--enroll-token-file", token,
         "--source-root", f"{root_id}={source}", "--go", shutil.which("go"), *tool_args, "--autoscale", "--max-workers", "1"],
        directory, node_env, directory / "secrets.log")))
    project = session.request("POST", "projects", {"name": "secrets-pack", "source": {"kind": "local", "source_root_id": root_id, "relative_path": "."}})
    run_id = session.enqueue(project["id"])
    revision = wait_for("보고서 게시", lambda: published_report(session, run_id), children, timeout=240)
    assert revision["revision"] == 1
    path = f"runs/{run_id}/report?revision=1"
    full = session.request("GET", path + "&include_code=true")
    code_free = session.request("GET", path)
    # The stored revision is the full report: the DB check below reads it.
    assert scalar(f"SELECT count(*) FROM reports WHERE run_id='{run_id}'") == "1"
    import validate_bundle
    schema = validate_bundle.make_validators()["report.schema.json"]
    schema.validate(full)
    schema.validate(code_free)
    # Sections: the metrics and secrets sections hold those observations, and each
    # detection keeps its location and links to its unreviewed tool finding.
    from bundle_checks.report import check_sections
    for doc in (full, code_free):
        check_sections(doc, source)
        clones = [o for o in doc["observations"] if o.get("metric", {}).get("name") == "duplicated_lines"]
        assert len(clones) == 1, clones
        clone = clones[0]
        counterpart = clone["metric"]["counterpart"]
        assert {clone["location"]["path"], counterpart["path"]} == {"lib/b.js", "src/a.js"}, clone
        assert counterpart["snapshot_id"] == doc["inputs"]["snapshot_id"] and counterpart["end_line"] > counterpart["start_line"], counterpart
    assert sum(len(g["observation_ids"]) for g in full["metrics"]["metrics"]) == 2, full["metrics"]
    assert full["secrets"]["task_keys"] == ["gitl" "eaks"] and all(d["finding_id"] for d in full["secrets"]["detections"]), full["secrets"]
    rows = {row["task_key"]: row for row in full["coverage"]["tools"]}
    assert (rows["lizard"]["exit_class"], rows["jscpd"]["exit_class"], rows["gitl" + "eaks"]["exit_class"]) == (
        "success_without_findings", "success_without_findings", "success_with_findings"), rows
    assert rows["lizard"]["packages"]["functions_measured"] >= 1, rows["lizard"]
    summary = full["summary"]
    assert summary == code_free["summary"] and summary["metric_observations"] == 2 and summary["actionable_findings"] == 0, summary
    metrics = {o["id"] for o in full["observations"] if "metric" in o}
    assert len(metrics) == 2 and not any(set(f["observation_ids"]) & metrics for f in full["findings"]), full["findings"]
    detected = sorted(f["locations"][0]["path"] for f in full["findings"] if f["category"] == "security")
    assert detected == ["lib/b.js", "src/a.js", "src/app.py", "src/complex.py"], detected
    assert all(f["validation"] == "unreviewed" and f["severity"] == "low" for f in full["findings"] if f["category"] == "security")
    for name, doc in (("full report", full), ("code-free report", code_free)):
        absent(name, json.dumps(doc).encode())
    for fmt in ("markdown", "html"):
        for query in ("", "&include_code=true"):
            with session.client.open(origin + "/api/v1/" + path + query + "&format=" + fmt, timeout=5) as response:
                absent(fmt + query, response.read())
    stored = 0
    for item in sorted((directory / "artifacts").rglob("*")):
        if item.is_file():
            absent("artifact " + item.name, item.read_bytes())
            stored += 1
    # The worker's own copies of the source (snapshot and workspace under
    # data/) hold the file; everything else of the node (scratch, home, tmp,
    # journal) must not.
    for item in sorted(state.rglob("*")):
        if item.is_file() and "data" not in item.relative_to(state).parts[:1]:
            absent("node state " + str(item.relative_to(state)), item.read_bytes())
    for log in ("controller.log", "secrets.log"):
        absent(log, (directory / log).read_bytes())
    dump = shutil.which("pg_dump", path=sql_env.get("PATH"))
    assert dump, "pg_dump 없음: DB 행 확인 불가"
    # stderr only carries pg_dump's notes on circular foreign keys of a data-only dump.
    rows = subprocess.run([dump, "--data-only", "--no-owner"], env=sql_env, capture_output=True, check=True, timeout=30).stdout
    assert b"COPY public.reports" in rows and len(rows) > 10000, "DB dump is not the run database"
    absent("DB rows", rows)
    assert all((source / rel).read_text() == text for rel, text in files.items())
    print(f"PASS: 실제 lizard·jscpd·gitleaks run(지표 2개는 finding 아님, 비밀값 탐지 {len(detected)}개 미검토) — 산출물 {stored}개·보고서·내보내기·로그·DB 행·노드 scratch에 비밀값 없음")


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
        # The real worker's planned stage is shown once the controller has
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
        print("PASS: 실제 워커 단계 기록·목록/상세/실행 상세 일치·취소 단계 (시험용 handler)")
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
        # Staticcheck runs only when the operator names its binary
        # (CODEYE_TEST_STATICCHECK, absolute); otherwise its task is planned
        # and finishes unsupported/tool_not_configured.
        staticcheck = os.environ.get("CODEYE_TEST_STATICCHECK", "")
        tool_args = ["--staticcheck", staticcheck] if staticcheck else []
        # The shared node toolchain (docs/ANALYZERS.md#공유-interpreter-toolchain) is probed at worker
        # startup when CODEYE_TEST_NODE names it; only pyright needs it
        # and the Go fixture plans no Python task, so only startup codes are
        # checked.
        node_toolchain = os.environ.get("CODEYE_TEST_NODE", "")
        if node_toolchain:
            tool_args += ["--node", node_toolchain]
        # The Python tools named by CODEYE_TEST_RUFF/MYPY/PYRIGHT/BANDIT are
        # probed at worker startup (the Go fixture plans no Python task); both
        # type checkers need the explicit choice (docs/ANALYZERS.md#타입-검사기-선택).
        python_tools = {t: os.environ.get("CODEYE_TEST_" + t.upper(), "") for t in ("ruff", "mypy", "pyright", "bandit")}
        python_tools = {t: p for t, p in python_tools.items() if p and (t != "pyright" or node_toolchain)}
        for tool, path in python_tools.items():
            tool_args += ["--" + tool, path]
        if "mypy" in python_tools and "pyright" in python_tools:
            tool_args.append("--python-both-type-checkers")
        production = services.enter_context(service(
            [binary, "agent", "--controller", origin, "--state-dir", directory / "go-agent",
             "--name", "go-smoke", "--enroll-token-file", go_token, "--source-root", f"{go_root}={go_source}",
             "--go", shutil.which("go"), *tool_args, "--autoscale", "--max-workers", "1"], directory, node_env, directory / "go.log"))
        children.append(production)
        actual = session.enqueue(go_project["id"])
        def observations_keys():
            state = session.request("GET", f"runs/{actual}")["status"]
            if state in {"failed", "cancelled", "recovery_required"}:
                raise RuntimeError(f"Go 분석 run 상태 {state}")
            if state not in {"finalizing", "partial", "completed"}:
                return None
            keys = scalar(f"SELECT string_agg(storage_key, ' ' ORDER BY storage_key) FROM artifacts WHERE run_id='{actual}' AND role='observations'")
            return keys.split() if keys else None
        observation_docs = []
        for key in wait_for("실제 Go 분석 산출물", observations_keys, children, timeout=140):
            data = (directory / "artifacts" / key).read_bytes()
            assert hashlib.sha256(data).hexdigest() == key.split("/")[-1]
            observation_docs.append(json.loads(data))
        # go vet keeps its v0.1 document. The optional Go checks share the generic envelope.
        vet_docs = [d for d in observation_docs if d.get("kind") == "go_vet_observations"]
        sc_docs = [d for d in observation_docs if d.get("kind") == "analyzer_observations" and d["analyzer"]["id"] == "go.staticcheck"]
        fmt_docs = [d for d in observation_docs if d.get("kind") == "analyzer_observations" and d["analyzer"]["id"] == "go.gofmt"]
        err_docs = [d for d in observation_docs if d.get("kind") == "analyzer_observations" and d["analyzer"]["id"] == "go.errcheck"]
        # (docs/ANALYZERS.md): lizard, jscpd and gitleaks are planned for every
        # project; this node has none of their tools, so each is a visible
        # not_run stand-in.
        structure_docs = [d for d in observation_docs if d.get("kind") == "analyzer_observations" and d["analyzer"]["ecosystem"] == "any"]
        assert len(vet_docs) == len(sc_docs) == len(fmt_docs) == len(err_docs) == 1 and len(structure_docs) == 3 and len(observation_docs) == 7, [d.get("kind") for d in observation_docs]
        assert all(d["outcome"]["exit_class"] == "not_run" and d["outcome"]["reason"] == "tool_not_configured" for d in [*structure_docs, *err_docs]), [d["outcome"] for d in [*structure_docs, *err_docs]]
        doc, sc_doc = vet_docs[0], sc_docs[0]
        fmt_doc = fmt_docs[0]
        assert fmt_doc["outcome"]["exit_class"] == "success_without_findings" and fmt_doc["coverage"]["processed_paths"] == ["example.go"], fmt_doc["outcome"]
        assert doc["analyzer_id"] == "go.vet"
        assert any(o["rule_id"] == "printf" and o["location"]["path"] == "example.go" for o in doc["observations"])
        assert sc_doc["analyzer"] == {"id": "go.staticcheck", "ecosystem": "go", "required": False}
        if staticcheck:
            # staticcheck reports nothing on the fixture (exit 0): clean, never an error.
            assert sc_doc["outcome"]["task_status"] == "succeeded" and sc_doc["coverage"]["processed_paths"] == ["example.go"], sc_doc["outcome"]
        else:
            assert sc_doc["outcome"] == {"task_status": "unsupported", "reason": "tool_not_configured", "exit_class": "not_run", "partial_parse": False}, sc_doc["outcome"]
            assert sc_doc["executions"] == [] and sc_doc["coverage"]["processed_paths"] == []
        assert (go_source / "example.go").read_text() == original
        # 실제 워커가 생성한 서로 다른 두 manifest와 도구 산출물을 함께 검증한다.
        def published():
            state = session.request("GET", f"runs/{actual}")
            if state["status"] in {"failed", "recovery_required", "cancelled"}:
                raise RuntimeError(f"Go 보고서 게시 실패 ({state['status']})")
            return state["report"] if state["status"] == "partial" else None
        revision = wait_for("실제 Go vet 보고서 게시", published, children)
        assert revision["revision"] == 1
        # The product handlers declare a stage for every planned task,
        # so the real run is recorded and list/detail agree.
        go_activity = activity_agrees(session, go_project["id"])
        assert go_activity["run"]["id"] == actual and go_activity["last_report"]["run_id"] == actual and go_activity["last_report"]["revision"] == 1
        assert go_activity["detail"] == "recorded", go_activity
        go_stages = stage_map(go_activity)
        assert go_stages["source"]["state"] == "succeeded", go_stages
        # The review plans review.environment next to env.discover;
        # the model is not approved for this source, so that task is skipped by policy
        # and the stage is partial, never succeeded.
        env_stage = go_stages["environment"]
        assert env_stage["state"] == "partial" and env_stage["succeeded"] == 1 and env_stage["counts"] == {"skipped_policy": 1}, go_stages
        assert env_stage["reasons"] == ["model_source_not_approved"], go_stages
        static_stage = go_stages["static_analysis"]
        # This worker runs go vet and gofmt, optionally staticcheck. The
        # unconfigured errcheck and three cross-language tools remain visible.
        completed_tools = 2 + bool(staticcheck)
        assert static_stage["state"] == "partial" and static_stage["succeeded"] == completed_tools, static_stage
        assert static_stage["counts"] == {"unsupported": 7 - completed_tools}, static_stage
        assert static_stage["reasons"] == ["tool_not_configured"], static_stage
        assert static_stage["planned"] == 7 and go_stages["report"]["state"] == "succeeded", go_stages
        # The model review was not approved for this source: excluded by policy, never success.
        assert go_stages["code_review"]["state"] == "skipped" and go_stages["code_review"]["reasons"] == ["model_source_not_approved"], go_stages
        planned_review = go_stages["code_review"]["planned"]
        assert planned_review >= 4 and go_stages["code_review"]["counts"] == {"skipped_policy": planned_review}, go_stages
        assert go_stages["documents"]["succeeded"] == 1 and "model_source_not_approved" in go_stages["documents"]["reasons"], go_stages
        print(f"PASS: 실제 Go run 단계 표시 detail={go_activity['detail']} (목록/상세 일치)")
        streamed, plan_revision = stream_agrees(session, actual)
        print(f"PASS: 실제 Go run SSE {streamed}개 이벤트 순서 전달, run.view=GET, plan revision {plan_revision}")
        path = f"runs/{actual}/report?revision=1"
        code_free = session.request("GET", path)
        full = session.request("GET", path + "&include_code=true")
        assert code_free["summary"] == full["summary"]
        assert full["summary"]["raw_observations"] == len(doc["observations"]) + len(sc_doc["observations"])
        tool_rows = {row["task_key"]: row for row in full["coverage"]["tools"]}
        assert sorted(tool_rows) == ["errcheck", "gitl" "eaks", "go-vet", "gofmt", "jscpd", "lizard", "staticcheck"], tool_rows
        assert tool_rows["gofmt"]["exit_class"] == "success_without_findings" and tool_rows["gofmt"]["processed_files"] == 1, tool_rows
        assert all(tool_rows[k]["exit_class"] == "not_run" for k in ("errcheck", "lizard", "jscpd", "gitl" "eaks")), tool_rows
        assert tool_rows["staticcheck"]["analyzer"] == "go.staticcheck"
        assert tool_rows["staticcheck"]["exit_class"] == ("success_without_findings" if staticcheck else "not_run"), tool_rows
        decoders = {a["task_key"]: a["decoder"] for a in full["artifacts"] if a["role"] == "observations"}
        assert decoders["staticcheck"] == "application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations", decoders
        assert full["summary"]["pending_candidates"] > 0 and full["summary"]["actionable_findings"] == 0
        assert full["coverage"]["ai_review"]["status"] == "not_performed"
        assert full["quality_verdict"] == "inconclusive"
        # The real worker's discovery artifacts pass the report decoder.
        environment = [a for a in full["artifacts"] if a["media_type"] == "application/vnd.codeye.environment+json"]
        assert sorted(a["task_key"] for a in environment) == ["docs.discover", "env.discover"]
        assert all(a["decoder"] == "application/vnd.codeye.environment+json;schema_version=0.1" for a in environment)
        assert not any(item.endswith((":env.discover", ":docs.discover")) for item in full["limitations"])
        statuses = {task["key"]: task["status"] for task in full["tasks"]}
        assert all(statuses[k] == "skipped_policy" for k in ("review.investigate", "review.interpret", "review.verify", "review.scope")), statuses
        assert statuses["env.discover"] == statuses["docs.discover"] == "succeeded"
        assert statuses["staticcheck"] == ("succeeded" if staticcheck else "unsupported"), statuses
        assert statuses["errcheck"] == statuses["lizard"] == statuses["jscpd"] == statuses["gitl" "eaks"] == "unsupported", statuses
        # Startup codes of the worker (handler_unavailable) reach the agent log.
        agent_log = (directory / "go.log").read_text(errors="replace")
        assert ("staticcheck:not_configured" in agent_log) == (not staticcheck), "startup codes not in the agent log"
        assert all(f"{t}:not_configured" in agent_log for t in ("lizard", "jscpd", "gitl" "eaks")), "startup codes not in the agent log"
        if node_toolchain:
            assert not any(f"node:{r}" in agent_log for r in ("invalid_config", "probe_failed", "unknown_tool")), "node toolchain rejected"
        for tool in ("ruff", "mypy", "pyright", "bandit"):
            assert (f"{tool}:not_configured" in agent_log) == (tool not in python_tools), f"{tool} startup code"
            assert not any(f"{tool}:{r}" in agent_log for r in ("invalid_config", "probe_failed", "unknown_tool", "toolchain_unavailable")), f"{tool} rejected at startup"
        assert all(o["raw_message"] is None for o in code_free["observations"])
        assert any(o["raw_message"] and o["evidence_state"] == "verified_against_source_manifest"
                   for o in full["observations"])
        import validate_bundle
        schema = validate_bundle.make_validators()["report.schema.json"]
        schema.validate(code_free)
        schema.validate(full)
        # Analysis sections of the real report: one partition, evidence bound to the
        # source, and the same stage states as the run's stage display.
        from bundle_checks.report import check_sections
        for doc in (full, code_free):
            check_sections(doc, go_source)
        assert full["overview"] == code_free["overview"], (full["overview"], code_free["overview"])
        section_tools = {t["task_key"]: t for language in full["static_analysis"]["languages"] for t in language["tools"]}
        assert all(section_tools[k]["task_status"] == "unsupported" and section_tools[k]["exit_class"] == "not_run"
                   for k in ("lizard", "jscpd", "gitl" "eaks")), section_tools
        for key in ("environment", "static_analysis", "code_review"):
            assert full[key]["stage"]["state"] == go_stages[key]["state"], (key, full[key]["stage"], go_stages[key])
        assert full["code_review"]["finding_ids"] == [] and full["documents"]["model"]["state"] == "not_performed", full["code_review"]
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
        comparison_history(session, go_project["id"], actual, full, go_source, original, lambda run: wait_for(
            "Go vet 보고서 게시", lambda: published_report(session, run), children, timeout=140))
        print("PASS: 자동 증감 제품 워커·실제 Go vet·printf 관찰·산출물 무결성·원본 보존")
        secrets_pack(session, cli, services, children, directory, binary, origin, user, node_env, sql_env, scalar)
    print("PASS: 두 에이전트 등록·단일 attempt·취소 및 정리·후속 run 결과 접수 (시험용 handler)")
