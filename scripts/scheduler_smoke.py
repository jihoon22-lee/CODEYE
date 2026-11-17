"""W07 scheduler·autoscaler 실제 프로세스 시험(Linux Native, 임시 PostgreSQL).

실제 `codeye serve`(controller·autoscaler)와 `-tags codeye_fixture`로 빌드한 `codeye agent --autoscale`,
그 agent가 시작하는 실제 worker 프로세스를 사용한다. fixture handler는 시험용이며 분석 결과가 아니다.
시나리오: TC-71 0→2→0, TC-72 busy3→max1 drain, TC-73 pool·project 예산 대기 사유,
TC-74 agent SIGKILL·재시작과 controller 재시작에서 시작 명령 중복 없음, TC-75 lease 만료 뒤 늦은 complete 409.
"""
from contextlib import ExitStack
import json
import os
import signal
import socket
import subprocess
import time
from urllib.error import URLError
from urllib.parse import urlsplit

from node_smoke import Session, wait_for


def log(message):
    print(f"[scheduler-smoke {time.strftime('%H:%M:%S')}] {message}", flush=True)


def run(base, root, env):
    directory = base / "scheduler-smoke"
    directory.mkdir(mode=0o700)
    binary = base / "codeye"
    fixture = directory / "codeye-fixture"
    subprocess.run(["go", "build", "-tags", "codeye_fixture", "-o", fixture, "./cmd/codeye"],
                   cwd=root, env=env, check=True, timeout=180)

    def cli(*args):
        result = subprocess.run([binary, *args], cwd=directory, env=env, capture_output=True, timeout=30)
        if result.returncode:
            raise RuntimeError(f"{' '.join(map(str, args[:2]))} 명령 실패 ({result.returncode}): {result.stderr.decode()[-400:]}")
        return result.stdout

    db = urlsplit(env["CODEYE_TEST_DATABASE_URL"])
    sql_env = dict(env, PGHOST=db.hostname, PGPORT=str(db.port), PGDATABASE=db.path[1:],
                   PGUSER=db.username, PGPASSWORD=db.password, PGSSLMODE="disable")

    def scalar(sql):
        return subprocess.check_output(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-At", "-c", sql],
                                       env=sql_env, text=True, timeout=10).strip()

    def count(sql):
        return int(scalar(sql))

    source = directory / "input"
    projects_dirs = []
    for name in ("p1", "p2", "p3"):
        d = source / name
        d.mkdir(parents=True)
        (d / "codeye-fixture-mode").write_text("sleep\n")
        projects_dirs.append(name)
    user = json.loads(cli("user", "create", "--name", "Scheduler fixture"))["id"]
    source_id = json.loads(cli("source-root", "add", "--label", "scheduler-smoke", "--path", source))["id"]
    cli("source-root", "grant", "--root", source_id, "--user", user)
    # 정책: 전역 3, 프로젝트별 기본 1(docs/09), pool default min0/max2, 짧은 유휴·쿨다운(시험 시간 단축).
    cli("scheduler", "set", "--global-max-running", "3")
    cli("pool", "set", "--name", "default", "--min-workers", "0", "--max-workers", "2",
        "--idle-timeout-seconds", "2", "--scale-up-cooldown-seconds", "0", "--scale-down-cooldown-seconds", "0")

    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    session = Session(origin)
    node_env = {key: env[key] for key in ("PATH", "LANG") if key in env}
    serve_argv = [binary, "serve", "--listen", f"127.0.0.1:{port}", "--artifact-dir", directory / "artifacts",
                  "--reconcile-interval", "500ms"]

    def ready():
        try:
            return session.request("GET", "ready")
        except URLError:
            return None

    def live_workers():
        return count("SELECT count(*) FROM workers WHERE state NOT IN ('stopped','failed')")

    def commands(kind):
        return count(f"SELECT count(*) FROM node_commands WHERE kind='{kind}'")

    def status(run_id):
        return session.request("GET", f"runs/{run_id}")

    def wait_status(run_id, want, children, timeout=60):
        return wait_for(f"run {want}", lambda: status(run_id)["status"] == want, children, timeout)

    def pool_state():
        return json.loads(cli("pool", "list"))["items"][0]

    with ExitStack() as services:
        def spawn(argv, launch_env, log_path):
            handle = services.enter_context(log_path.open("wb"))
            child = subprocess.Popen(argv, cwd=directory, env=launch_env, stdin=subprocess.DEVNULL,
                                     stdout=handle, stderr=handle, start_new_session=True)

            def cleanup():
                if child.poll() is None:
                    child.send_signal(signal.SIGCONT)
                    child.send_signal(signal.SIGINT)
                    try:
                        child.wait(timeout=35)
                    except subprocess.TimeoutExpired:
                        os.killpg(child.pid, signal.SIGKILL)
                        child.wait()
                        raise RuntimeError(f"{log_path.stem} 종료 시간 초과") from None

            services.callback(cleanup)
            return child

        controller_log = directory / "controller.log"
        controller = spawn(serve_argv, env, controller_log)
        children = [controller]
        wait_for("controller 준비", ready, children)
        login = directory / "login"
        cli("login-link", "--user", user, "--output", login, "--origin", origin)
        token = login.read_text().strip().split("#launch=", 1)[1]
        session.csrf = session.request("POST", "session", {"launch_token": token})["csrf_token"]
        projects = [session.request("POST", "projects", {"name": name, "source": {
            "kind": "local", "source_root_id": source_id, "relative_path": name}})["id"] for name in projects_dirs]

        enrollment = directory / "agent.token"
        cli("node", "token", "create", "--pool", "default", "--source-root", source_id, "--output", enrollment)
        state_dir = directory / "agent-state"
        agent_argv = [fixture, "agent", "--controller", origin, "--state-dir", state_dir, "--name", "auto",
                      "--source-root", f"{source_id}={source}", "--autoscale", "--max-workers", "3"]
        agent_log = directory / "agent.log"
        agent = spawn(agent_argv + ["--enroll-token-file", enrollment], node_env, agent_log)
        children.append(agent)
        peer_token = directory / "peer.token"
        cli("node", "token", "create", "--pool", "default", "--source-root", source_id, "--output", peer_token)
        peer = spawn([fixture, "agent", "--controller", origin, "--state-dir", directory / "peer-state",
                      "--name", "peer", "--source-root", f"{source_id}={source}", "--autoscale",
                      "--max-workers", "1", "--enroll-token-file", peer_token], node_env, directory / "peer.log")
        children.append(peer)

        wait_for("두 autoscale 노드 능력 광고", lambda: count(
            "SELECT count(*) FROM nodes WHERE worker_scaling='controller' AND max_workers IN (1,3)"
            " AND 'analysis:deep-review' = ANY(capabilities)") == 2, children)
        assert live_workers() == 0, "autoscale agent가 워커를 미리 시작함"
        assert "capability_probe" in agent_log.read_text()
        log("TC-74 zero workers + probed capability: ok")

        # --- TC-71: 0 → 2 → 0 ---------------------------------------------------------------------------
        r1, r2 = session.enqueue(projects[0]), session.enqueue(projects[1])
        wait_status(r1, "running", children)
        wait_status(r2, "running", children)
        assert count("SELECT count(DISTINCT node_id) FROM workers WHERE state='busy'") == 2
        assert commands("start_worker") == 2, "max 2를 넘는 시작 명령"
        # TC-73: pool 상한(2)에 걸린 세 번째 run은 대기하고 시작 명령을 더 만들지 않는다.
        r3 = session.enqueue(projects[2])
        time.sleep(3)
        view = status(r3)
        assert view["status"] == "queued" and view["progress"]["waiting_reason"] == "capacity_full", view
        assert commands("start_worker") == 2
        # TC-73: 같은 프로젝트의 두 번째 run은 project_limit(기본 1)으로 대기한다.
        r1b = session.enqueue(projects[0])
        assert status(r1b)["progress"]["waiting_reason"] == "project_limit"
        for rid in (r1, r2):
            session.request("POST", f"runs/{rid}/cancel")
            wait_status(rid, "cancelled", children)
        wait_status(r3, "running", children)
        wait_status(r1b, "running", children)
        for rid in (r3, r1b):
            session.request("POST", f"runs/{rid}/cancel")
            wait_status(rid, "cancelled", children)
        wait_for("유휴 워커 0으로 축소", lambda: live_workers() == 0, children, timeout=60)
        assert agent.poll() is None and controller.poll() is None
        stops = commands("stop_worker")
        assert stops >= 2, stops
        assert count("SELECT count(*) FROM attempts WHERE cleanup_state<>'confirmed'") == 0
        log(f"TC-71 0->2->0: starts={commands('start_worker')} stops={stops}")

        # --- TC-72: busy 3 → max 1, 강제 종료 없이 drain -------------------------------------------------
        cli("pool", "set", "--name", "default", "--max-workers", "3")
        runs = [session.enqueue(p) for p in projects]
        for rid in runs:
            wait_status(rid, "running", children)
        wait_for("busy 3", lambda: count("SELECT count(*) FROM workers WHERE state='busy'") == 3, children)
        cancels_before = commands("cancel_attempt")
        cli("pool", "set", "--name", "default", "--max-workers", "1")
        wait_for("drain 명령 2개", lambda: commands("drain_worker") == 2, children)
        time.sleep(3)
        for rid in runs:
            assert status(rid)["status"] == "running", "drain이 실행 중 작업을 끝냄"
        assert commands("cancel_attempt") == cancels_before, "drain이 취소 명령을 만듦"
        assert pool_state()["capacity_state"] == "over_target_draining"
        drained = scalar("SELECT string_agg(worker_id::text, ',') FROM node_commands WHERE kind='drain_worker'").split(",")
        kept_run = scalar("SELECT a.run_id FROM attempts a WHERE a.status='leased' AND a.worker_id::text NOT IN ("
                          + ",".join(f"'{w}'" for w in drained) + ")")
        # 남은 워커가 비어도 pool 활성 2 >= max 1이므로 새 작업을 받지 않는다.
        session.request("POST", f"runs/{kept_run}/cancel")
        wait_status(kept_run, "cancelled", children)
        kept_project = scalar(f"SELECT project_id FROM runs WHERE id='{kept_run}'")
        late = session.enqueue(kept_project)
        time.sleep(3)
        view = status(late)
        assert view["status"] == "queued" and view["progress"]["waiting_reason"] == "capacity_full", view
        for rid in runs:
            if rid != kept_run:
                session.request("POST", f"runs/{rid}/cancel")
                wait_status(rid, "cancelled", children)
        wait_for("drain된 워커 종료", lambda: count(
            "SELECT count(*) FROM workers WHERE state NOT IN ('stopped','failed') AND id::text IN ("
            + ",".join(f"'{w}'" for w in drained) + ")") == 0, children)
        wait_status(late, "running", children)
        session.request("POST", f"runs/{late}/cancel")
        wait_status(late, "cancelled", children)
        log("TC-72 busy3->max1 drain without kill, new work held: ok")

        # --- TC-74: agent SIGKILL·재시작, controller 재시작에서 중복 시작 없음 ---------------------------
        wait_for("유휴 워커 정리", lambda: live_workers() == 0, children)
        peer.send_signal(signal.SIGINT)
        assert peer.wait(timeout=35) == 0
        children.remove(peer)
        cli("node", "revoke", "--id", scalar("SELECT id FROM nodes WHERE name='peer'"))
        cli("pool", "set", "--name", "default", "--min-workers", "1", "--max-workers", "2")
        wait_for("warm 1", lambda: count("SELECT count(*) FROM workers WHERE state='idle'") == 1, children)
        time.sleep(2)
        before = commands("start_worker")
        assert live_workers() == 1
        agent.send_signal(signal.SIGKILL)
        agent.wait(timeout=10)
        children.remove(agent)
        agent_log2 = directory / "agent-restart.log"
        agent = spawn(agent_argv, node_env, agent_log2)
        children.append(agent)
        wait_for("재시작 뒤 warm 1", lambda: count("SELECT count(*) FROM workers WHERE state='idle'") == 1
                 and commands("start_worker") == before + 1, children, timeout=90)
        time.sleep(3)
        assert commands("start_worker") == before + 1, "재시작 뒤 중복 시작 명령"
        assert live_workers() == 1
        assert agent_log2.read_text().count('"code":"worker_ready"') == 1, "같은 명령으로 워커를 두 번 시작"
        # controller 재시작: 상태가 DB에 있으므로 새 시작 명령이 없다.
        controller.send_signal(signal.SIGINT)
        controller.wait(timeout=35)
        children.remove(controller)
        controller = spawn(serve_argv, env, directory / "controller2.log")
        children.append(controller)
        wait_for("controller 재시작", ready, children)
        time.sleep(4)
        assert commands("start_worker") == before + 1 and live_workers() == 1, "controller 재시작 뒤 중복 시작"
        log("TC-74 agent/controller restart without duplicate starts: ok")

        # --- TC-75: lease 만료 뒤 늦은 complete는 409, 실제 정리 확인 뒤 failed -------------------------
        cli("pool", "set", "--name", "default", "--min-workers", "0", "--max-workers", "1")
        stale = session.enqueue(projects[1])
        wait_status(stale, "running", children)
        attempt = scalar(f"SELECT id FROM attempts WHERE run_id='{stale}'")
        agent.send_signal(signal.SIGSTOP)
        log("agent SIGSTOP; waiting for lease expiry (30s) and sweep")
        wait_for("lease 만료 → recovery_required", lambda: scalar(
            f"SELECT status FROM attempts WHERE id='{attempt}'") == "recovery_required", children, timeout=90)
        agent.send_signal(signal.SIGCONT)
        wait_status(stale, "failed", children, timeout=90)
        row = scalar(f"SELECT status || ':' || cleanup_state || ':' || (completion_digest IS NULL) FROM attempts WHERE id='{attempt}'")
        assert row == "released:confirmed:true", row
        complete_409 = [json.loads(line) for line in (directory / "controller2.log").read_text().splitlines()
                        if '"route":"attempts.complete"' in line]
        assert any(e.get("status") == 409 for e in complete_409), complete_409
        assert '"code":"lease_lost"' in agent_log2.read_text() and "complete_rejected" in agent_log2.read_text()
        assert count(f"SELECT count(*) FROM attempts WHERE run_id='{stale}'") == 1
        log("TC-75 late complete 409 after lease expiry, run failed after confirmed cleanup: ok")

        # 정상 종료: agent는 SIGINT로 워커 정리 후 0으로 끝나야 한다.
        agent.send_signal(signal.SIGINT)
        assert agent.wait(timeout=40) == 0, "agent 종료 코드"
        controller.send_signal(signal.SIGINT)
        assert controller.wait(timeout=40) == 0, "controller 종료 코드"
    print("PASS: W07 autoscale 0→2→0·drain·예산 대기 사유·재시작 중복 없음·늦은 complete 409 (시험용 handler)")
