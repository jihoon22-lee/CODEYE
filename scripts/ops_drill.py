"""운영 훈련(Linux Native, 한 호스트). scripts/test_postgres.py --ops-drill 이 실행한다.

훈련마다 한 일·관찰·통과 여부·걸린 시간을 기록하고 끝에 표로 출력한다. 시각은 이 호스트의 측정값이다.
DB 중단 훈련은 시험 harness의 PostgreSQL을 멈추지 않도록 이 스크립트가 띄운 별도 임시 cluster만 멈춘다.
모델 호출은 가짜 upstream(scripts/fake_model_upstream.py)뿐이며 실제 모델·네트워크 다운로드는 없다.
시험용 handler(-tags codeye_fixture)는 분석 결과가 아니다.

D1 모델 endpoint 실패      D2 노드 offline(운영자 CLI 정리 확인)   D3 DB 중단(짧게·lease보다 길게)
D4 도구 버전 변경·사라짐   D5 작업 중 max workers 축소             D6 산출물 저장 상한
D7 실행 중 백업 → 새 상태 복원 → recovery_required 정리 확인 → 새 run
D8 권한 회수(구성원 제거·사용자 비활성화의 열린 SSE, 노드 token 회수)  D9 보존 정리(개인 모드)
"""
from contextlib import ExitStack, contextmanager
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import threading
import time
from urllib.error import HTTPError, URLError
from urllib.request import Request

from multinode_smoke import alive, descendants, kill_node
from node_smoke import Session, wait_for

TERMINAL = {"partial", "completed", "failed", "cancelled", "recovery_required"}


def log(message):
    print(f"[ops-drill {time.strftime('%H:%M:%S')}] {message}", flush=True)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Cluster:
    """A throwaway PostgreSQL that this drill may stop and start (pg_ctl)."""

    def __init__(self, base, env):
        self.base = base
        self.port = free_port()
        self.password = secrets.token_hex(24)
        self.env = {k: v for k, v in env.items() if not k.startswith("PG")}
        pw = base / "pg-password"
        pw.write_text(self.password)
        pw.chmod(0o600)
        subprocess.run(["initdb", "-D", str(base / "pgdata"), "-U", "codeye_ops", "--auth=scram-sha-256", "--pwfile", str(pw),
                        "--no-locale", "-E", "UTF8"], env=self.env, capture_output=True, check=True, timeout=120)
        pw.unlink()
        self.probe_env = dict(self.env, PGHOST="127.0.0.1", PGPORT=str(self.port), PGUSER="codeye_ops", PGPASSWORD=self.password,
                              PGDATABASE="postgres", PGSSLMODE="disable", PGCONNECT_TIMEOUT="2")

    def start(self):
        subprocess.run(["pg_ctl", "-D", str(self.base / "pgdata"), "-l", str(self.base / "pg.log"), "-w", "-t", "60",
                        "-o", f"-h 127.0.0.1 -p {self.port} -k {self.base}", "start"], env=self.env, capture_output=True, check=True, timeout=90)

    def stop(self):
        subprocess.run(["pg_ctl", "-D", str(self.base / "pgdata"), "-m", "fast", "-w", "-t", "60", "stop"],
                       env=self.env, capture_output=True, check=True, timeout=90)

    def url(self, db):
        return f"postgresql://codeye_ops:{self.password}@127.0.0.1:{self.port}/{db}?sslmode=disable"

    def create(self, db):
        subprocess.run(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-c", f"CREATE DATABASE {db}"], env=self.probe_env,
                       capture_output=True, check=True, timeout=10)

    def scalar(self, db, sql):
        return subprocess.check_output(["psql", "-X", "-w", "-v", "ON_ERROR_STOP=1", "-At", "-c", sql],
                                       env=dict(self.probe_env, PGDATABASE=db), text=True, timeout=10).strip()


class EventReader:
    """An open GET /runs/{id}/events stream read on a thread."""

    def __init__(self, session, run_id):
        self.lines, self.closed_at, self.error = [], None, None
        req = Request(session.origin + f"/api/v1/runs/{run_id}/events?cursor=0", headers={"Accept": "text/event-stream"})
        self.response = session.client.open(req, timeout=60)
        threading.Thread(target=self._read, daemon=True).start()

    def _read(self):
        try:
            for raw in self.response:
                self.lines.append((time.monotonic(), raw.decode().rstrip("\n")))
        except Exception as e:  # noqa: BLE001 - the end of the stream is the observation
            self.error = type(e).__name__
        self.closed_at = time.monotonic()

    def closed_reason(self):
        for i, (_, line) in enumerate(self.lines):
            if line == "event: closed":
                for _, data in self.lines[i + 1:i + 3]:
                    if data.startswith("data: "):
                        return json.loads(data[6:]).get("reason")
        return None


def run(base, root, env):
    for tool in ("initdb", "pg_ctl", "postgres", "psql"):
        if shutil.which(tool) is None:
            raise SystemExit(f"필요한 PostgreSQL 도구가 PATH에 없습니다: {tool}")
    directory = base / "ops-drill"
    directory.mkdir(mode=0o700)
    records = []
    try:
        drills(base, root, env, directory, records)
    except BaseException:
        for path in sorted(directory.rglob("*.log")):
            lines = path.read_text(errors="replace").splitlines()[-10:]
            print(f"--- {path.relative_to(directory)} (last {len(lines)} lines)", *lines, sep="\n", flush=True)
        raise
    finally:
        print("\n| 훈련 | 한 일 | 관찰 | 결과 | 시간(s) |\n|---|---|---|---|---|")
        for r in records:
            print(f"| {r['drill']} | {r['done']} | {r['observed']} | {r['result']} | {r['seconds']:.1f} |")
    failed = [r["drill"] for r in records if r["result"] == "FAIL"]
    if failed:
        raise RuntimeError(f"실패한 훈련: {failed}")
    print("PASS: 운영 훈련 " + ", ".join(f"{r['drill']}={r['result']}" for r in records))
    return records


def drills(base, root, env, directory, records):
    @contextmanager
    def drill(name, done):
        record = {"drill": name, "done": done, "observed": "", "result": "FAIL", "seconds": 0.0}
        records.append(record)
        log(f"{name}: {done}")
        started = time.monotonic()
        try:
            yield record
            if record["result"] == "FAIL":
                record["result"] = "PASS"
        finally:
            record["seconds"] = time.monotonic() - started
            log(f"{name}: {record['result']} — {record['observed']} ({record['seconds']:.1f}s)")

    binary = base / "codeye"
    fixture = directory / "codeye-fixture"
    subprocess.run(["go", "build", "-tags", "codeye_fixture", "-o", fixture, "./cmd/codeye"], cwd=root, env=env, check=True, timeout=300)
    go = shutil.which("go")
    cluster = Cluster(directory, env)
    cluster.start()
    with ExitStack() as services:
        services.callback(lambda: subprocess.run(["pg_ctl", "-D", str(directory / "pgdata"), "-m", "immediate", "stop"],
                                                 env=cluster.env, capture_output=True, timeout=60))
        cluster.create("ops")
        ops_env = dict(cluster.env, GOTOOLCHAIN="local", CODEYE_DATABASE_URL=cluster.url("ops"))

        def cli(*args, expect=0, timeout=60, run_env=None):
            result = subprocess.run([str(binary), *map(str, args)], cwd=directory, env=run_env or ops_env,
                                    capture_output=True, text=True, timeout=timeout)
            assert cluster.password not in result.stdout + result.stderr, "DB 인증정보 노출"
            if expect is not None and result.returncode != expect:
                raise RuntimeError(f"{' '.join(map(str, args[:2]))} 종료 {result.returncode} (기대 {expect}): {(result.stdout + result.stderr)[-1500:]}")
            return result

        def q(sql):
            return cluster.scalar("ops", sql)

        def n(sql):
            return int(q(sql))

        subprocess.run([str(binary), "migrate"], cwd=directory, env=ops_env, check=True, capture_output=True, timeout=120)
        src = directory / "src"
        fx = src / "fx"
        for p in ("offline", "db", "db2", "w1", "w2", "quota", "rev"):
            (fx / p).mkdir(parents=True)
            (fx / p / "codeye-fixture-mode").write_text("succeed\n")
        py = src / "py"
        (py / "app").mkdir(parents=True)
        (py / "app/main.py").write_text("import os\nimport sys\n\n\ndef main():\n    return sys.argv\n")
        (py / "app/pyproject.toml").write_text('[project]\nname = "app"\nversion = "0.1.0"\n')
        user = json.loads(cli("user", "create", "--name", "Ops owner").stdout)["id"]
        roots = {}
        for name, path in (("fx", fx), ("py", py)):
            roots[name] = json.loads(cli("source-root", "add", "--label", "ops-" + name, "--path", path).stdout)["id"]
            cli("source-root", "grant", "--root", roots[name], "--user", user)
        cli("scheduler", "set", "--global-max-running", "8", "--project-max-running", "1")
        cli("pool", "set", "--name", "default", "--min-workers", "0", "--max-workers", "8", "--idle-timeout-seconds", "2",
            "--scale-up-cooldown-seconds", "0", "--scale-down-cooldown-seconds", "0")
        port = free_port()
        origin = f"http://127.0.0.1:{port}"
        node_env = {k: env[k] for k in ("PATH", "LANG") if k in env}
        logs = directory / "logs"
        logs.mkdir()
        children = []

        def spawn(argv, launch_env, name):
            handle = services.enter_context((logs / f"{name}.log").open("ab"))
            child = subprocess.Popen(list(map(str, argv)), cwd=directory, env=launch_env, stdin=subprocess.DEVNULL,
                                     stdout=handle, stderr=handle, start_new_session=True)

            def cleanup():
                if child.poll() is None:
                    for p in [child.pid, *descendants(child.pid)]:
                        try:
                            os.kill(p, signal.SIGCONT)
                        except ProcessLookupError:
                            pass
                    child.send_signal(signal.SIGINT)
                    try:
                        child.wait(timeout=60)
                    except subprocess.TimeoutExpired:
                        os.killpg(child.pid, signal.SIGKILL)
                        child.wait()
            services.callback(cleanup)
            children.append(child)
            return child

        def controller():
            return spawn([binary, "serve", "--listen", f"127.0.0.1:{port}", "--artifact-dir", directory / "artifacts",
                          "--reconcile-interval", "500ms"], ops_env, "controller")

        def get(path, as_user=None):
            """Status of a GET, signed in as as_user's session when given."""
            try:
                with (as_user or Session(origin)).client.open(Request(origin + "/api/v1/" + path), timeout=3) as r:
                    return r.status
            except HTTPError as e:
                return e.code
            except (URLError, OSError):
                return None

        ctl = controller()
        wait_for("controller", lambda: get("ready") == 200, [ctl])

        def login(user_id):
            link = directory / f"login-{user_id}"
            cli("login-link", "--user", user_id, "--output", link, "--origin", origin)
            s = Session(origin)
            s.csrf = s.request("POST", "session", {"launch_token": link.read_text().strip().split("#launch=", 1)[1]})["csrf_token"]
            return s

        session = login(user)
        projects = {}
        for p in ("offline", "db", "db2", "w1", "w2", "quota", "rev"):
            projects[p] = session.request("POST", "projects", {"name": "ops-" + p, "source": {
                "kind": "local", "source_root_id": roots["fx"], "relative_path": p}})["id"]
        projects["py"] = session.request("POST", "projects", {"name": "ops-py", "source": {
            "kind": "local", "source_root_id": roots["py"], "relative_path": "app"}})["id"]

        def view(run_id):
            return session.request("GET", f"runs/{run_id}")

        def wait_status(run_id, want, timeout=120):
            def probe():
                v = view(run_id)
                if v["status"] in want:
                    return v
                if v["status"] in TERMINAL - set(want) - {"recovery_required"}:
                    raise RuntimeError(f"run 상태 {v['status']} (기대 {want})")
                return None
            return wait_for(f"run {want}", probe, children, timeout)

        def mode(project, text):
            (fx / project / "codeye-fixture-mode").write_text(text)

        def running_task(run_id):
            wait_for("작업 실행", lambda: n(f"SELECT count(*) FROM tasks WHERE run_id='{run_id}' AND status='running'") >= 1, children)

        def agent(name, exe, root_names, extra, enroll=True):
            state = directory / ("state-" + name)
            argv = [exe, "agent", "--controller", origin, "--state-dir", state, "--name", name, *extra]
            for r in root_names:
                argv += ["--source-root", f"{roots[r]}={fx if r == 'fx' else py}"]
            if enroll and not (state / "node.json").exists():
                token = directory / (name + ".token")
                cli("node", "token", "create", "--pool", "default", "--source-root", ",".join(roots[r] for r in root_names), "--output", token)
                argv += ["--enroll-token-file", token]
            return spawn(argv, node_env, name)

        def stop(child, code=0):
            if child.poll() is None:
                child.send_signal(signal.SIGINT)
            got = child.wait(timeout=60)
            children.remove(child)
            assert got == code, f"종료 코드 {got} (기대 {code})"

        def events(name):
            return [json.loads(line) for line in (logs / f"{name}.log").read_text().splitlines() if line.startswith("{")]

        # --- D2 노드 offline: 운영자가 CLI로 정리를 확인한다 -------------------------------------------------------
        with drill("D2 노드 offline", "작업 중 agent·worker·작업 프로세스 SIGKILL, 운영자 CLI 정리 확인, 다른 노드로 이어 실행") as r:
            mode("offline", "sleep\n")
            n1 = agent("n1", fixture, ["fx"], ["--workers", "1"])
            rid = session.enqueue(projects["offline"])
            running_task(rid)
            children.remove(n1)
            t0 = time.monotonic()
            killed = kill_node(n1)
            wait_status(rid, {"recovery_required"}, timeout=90)
            t_rr = time.monotonic() - t0
            nodes = json.loads(cli("node", "list").stdout)["items"]
            seen = next(x for x in nodes if x["name"] == "n1")
            refused = cli("run", "recover", "--id", rid, expect=2)
            assert "--confirm-cleanup" in refused.stderr, refused.stderr
            mode("offline", "succeed\n")
            out = json.loads(cli("run", "recover", "--id", rid, "--confirm-cleanup").stdout)
            assert out["status"] == "failed" and out["retry"] == "new_run" and out["confirmed_run"] == rid, out
            assert n(f"SELECT count(*) FROM audit_events WHERE action='run.recovery_confirmed' AND target_id='{rid}' AND actor_kind='operator_cli'") == 1
            n2 = agent("n2", fixture, ["fx"], ["--workers", "1"])
            wait_status(out["run"], {"partial", "completed"})
            r["observed"] = (f"{len(killed)} processes killed; recovery_required after {t_rr:.0f}s; node list "
                             f"{ {k: seen[k] for k in seen if k in ('status', 'stale', 'state', 'last_heartbeat_at')} }; "
                             "recover without --confirm-cleanup exit 2; with it: failed + new_run (audit operator_cli); new run finished on n2")

        # --- D3 DB 중단 ---------------------------------------------------------------------------------------
        with drill("D3 DB 중단(짧게)", "작업 중 별도 cluster를 8초 멈춤(pg_ctl stop -m fast) 후 재시작") as r:
            mode("db", "pause\n25\n")
            rid = session.enqueue(projects["db"])
            running_task(rid)
            cluster.stop()
            t0 = time.monotonic()
            ready_down, run_down = get("ready"), get(f"runs/{rid}", session)
            time.sleep(8)
            cluster.start()
            wait_for("ready 복귀", lambda: get("ready") == 200, children, timeout=60)
            back = time.monotonic() - t0
            v = wait_status(rid, {"partial", "completed"}, timeout=90)
            assert ready_down == 503, ready_down
            assert run_down == 503, run_down
            assert q(f"SELECT number || ':' || epoch || ':' || status || ':' || cleanup_state FROM attempts WHERE run_id='{rid}'") == "1:1:released:confirmed"
            r["observed"] = f"ready {ready_down}, GET run {run_down} while down; ready again {back:.1f}s after stop; run {v['status']} with its first attempt (no lease loss)"

        with drill("D3 DB 중단(lease보다 길게)", "작업 중 cluster를 40초 멈춤(lease 30s, agent 여유 20s)") as r:
            mode("db2", "pause\n90\n")
            rid = session.enqueue(projects["db2"])
            running_task(rid)
            attempt = q(f"SELECT id FROM attempts WHERE run_id='{rid}'")
            cluster.stop()
            t0 = time.monotonic()
            wait_for("agent lease_lost", lambda: any(e.get("code") == "lease_lost" and e.get("attempt_id") == attempt for e in events("n2")),
                     children, timeout=40)
            t_lost = time.monotonic() - t0
            wait_for("작업 프로세스 정리", lambda: not [p for p in descendants(n2.pid) if Path(f"/proc/{p}/comm").exists()
                                                     and Path(f"/proc/{p}/comm").read_text().strip() == "sleep"], children, timeout=30)
            remaining = 40 - (time.monotonic() - t0)
            if remaining > 0:
                time.sleep(remaining)
            assert n2.poll() is None, "agent가 DB 중단 중 종료함"
            cluster.start()
            t1 = time.monotonic()
            wait_for("ready 복귀", lambda: get("ready") == 200, children, timeout=60)
            v = wait_status(rid, {"failed"}, timeout=90)
            t_failed = time.monotonic() - t1
            row = q(f"SELECT status || ':' || cleanup_state || ':' || (completion_digest IS NULL) FROM attempts WHERE id='{attempt}'")
            assert row == "released:confirmed:true", row
            integrity = {
                "leased_expired": n("SELECT count(*) FROM attempts WHERE status='leased' AND expires_at < clock_timestamp()"),
                "released_unconfirmed": n("SELECT count(*) FROM attempts WHERE status='released' AND cleanup_state<>'confirmed'"),
                "open_reservations_of_released": n("SELECT count(*) FROM capacity_reservations c JOIN attempts a ON a.id=c.attempt_id WHERE c.released_at IS NULL AND a.status='released'"),
                "running_runs_without_lease": n("SELECT count(*) FROM runs r WHERE r.status='running' AND NOT EXISTS (SELECT 1 FROM attempts a WHERE a.run_id=r.id AND a.status='leased')"),
            }
            assert all(v == 0 for v in integrity.values()), integrity
            doctor = json.loads(cli("doctor", "--format", "json", "--work-dir", directory / "doctor", expect=None).stdout)
            db_check = next(c for c in doctor["checks"] if c["id"] == "database")
            assert db_check["status"] == "available", db_check
            mode("db2", "succeed\n")
            # A new run reads the source again (a retry would reuse the paused snapshot).
            fresh = session.enqueue(projects["db2"])
            wait_status(fresh, {"partial", "completed"})
            r["observed"] = (f"agent lease_lost {t_lost:.0f}s after stop and stopped its task, agent kept running; after start the node's "
                             f"cleanup report made the run {v['status']} in {t_failed:.1f}s, late result not accepted; integrity {integrity}; "
                             f"doctor database {db_check['status']}; a new run finished")
        stop(n2)

        # --- D4 도구 버전 변경·사라짐 -----------------------------------------------------------------------------
        ruff = shutil.which("ruff")
        with drill("D4 도구 버전 변경·사라짐", "ruff 버전만 다르게 답하는 wrapper로 재시작, 다음엔 ruff 없이 재시작(go는 유지)") as r:
            if not ruff:
                r["result"], r["observed"] = "NOT RUN", "ruff not on PATH"
            else:
                real = subprocess.run([ruff, "--version"], capture_output=True, text=True, check=True).stdout.split()[1]
                major, minor, patch = real.split(".")
                fake = f"{major}.{minor}.{int(patch) + 1}"
                wrapper = directory / "ruff-wrapper"
                wrapper.write_text(f'#!/bin/sh\nif [ "$1" = --version ]; then echo "ruff {fake}"; exit 0; fi\nexec {ruff} "$@"\n')
                wrapper.chmod(0o755)

                def py_run(tool_args):
                    a = agent("py", binary, ["py"], ["--go", go, *tool_args, "--workers", "1"])
                    rid = session.enqueue(projects["py"])
                    v = wait_status(rid, {"partial", "completed"})
                    wait_for("보고서 게시", lambda: view(rid).get("report"), children, timeout=60)
                    stop(a)
                    return rid, v

                first, _ = py_run(["--ruff", ruff])
                second, _ = py_run(["--ruff", wrapper])
                third, _ = py_run([])
                c2 = session.request("GET", f"runs/{second}/comparison")
                c3 = session.request("GET", f"runs/{third}/comparison")
                diffs2 = {d["key"]: d for d in c2.get("differences", [])}
                version_diff = next((d for k, d in diffs2.items() if k.startswith("tool:") and k.endswith(":version") and "ruff" in k), None)
                assert version_diff, c2.get("differences")
                assert c2["counts"]["resolved_observed"] == 0 and c2["counts"]["persisting"] >= 1, c2["counts"]
                absent = [e for e in c3["entries"] if e["producer"] == "tool" and e["state"] != "persisting"]
                assert c3["counts"]["resolved_observed"] == 0 and absent and all(e["state"] == "absent_unverified" for e in absent), c3["counts"]
                reasons = sorted({e["reason"] for e in absent})
                ruff_tasks = q(f"SELECT string_agg(key || '=' || status || ':' || coalesce(reason,''), ' ') FROM tasks WHERE run_id='{third}' AND key LIKE 'ruff%'")
                assert "unsupported:tool_not_configured" in ruff_tasks, ruff_tasks
                r["observed"] = (f"version change: difference {version_diff['key']} {version_diff.get('baseline')}→{version_diff.get('current')}, "
                                 f"counts {c2['counts']}; tool removed: {ruff_tasks}, {len(absent)} absent_unverified {reasons}, resolved_observed 0")

        # --- D5 작업 중 max workers 축소 -------------------------------------------------------------------------
        with drill("D5 작업 중 max workers 축소", "autoscale 노드에서 두 작업 실행 중 pool max 2→1") as r:
            cli("pool", "set", "--name", "default", "--max-workers", "2")
            mode("w1", "pause\n20\n")
            mode("w2", "pause\n20\n")
            auto = agent("auto", fixture, ["fx"], ["--autoscale", "--max-workers", "2"])
            runs = [session.enqueue(projects["w1"]), session.enqueue(projects["w2"])]
            for rid in runs:
                running_task(rid)
            cancels = n("SELECT count(*) FROM node_commands WHERE kind='cancel_attempt'")
            t0 = time.monotonic()
            cli("pool", "set", "--name", "default", "--max-workers", "1")
            wait_for("drain 명령", lambda: n("SELECT count(*) FROM node_commands WHERE kind='drain_worker'") >= 1, children, timeout=20)
            t_drain = time.monotonic() - t0
            for rid in runs:
                wait_status(rid, {"partial", "completed"}, timeout=60)
                assert q(f"SELECT number || ':' || status || ':' || cleanup_state FROM attempts WHERE run_id='{rid}'") == "1:released:confirmed"
            assert n("SELECT count(*) FROM node_commands WHERE kind='cancel_attempt'") == cancels
            wait_for("worker 1개 이하", lambda: n("SELECT count(*) FROM workers WHERE state NOT IN ('stopped','failed')") <= 1, children, timeout=60)
            stop(auto)
            cli("pool", "set", "--name", "default", "--max-workers", "8")
            r["observed"] = f"drain_worker after {t_drain:.1f}s, no cancel_attempt, both runs finished with their first attempt, workers ≤ 1 after"

        # --- D6 산출물 저장 상한 --------------------------------------------------------------------------------
        with drill("D6 저장 상한", "산출물 상한 1 byte(scheduler set --artifact-quota-bytes 1) 뒤 새 run, 기존 보고서 조회, 상한 해제") as r:
            n3 = agent("n3", fixture, ["fx"], ["--workers", "1"])
            done = session.enqueue(projects["quota"])
            v = wait_status(done, {"partial", "completed"})
            wait_for("보고서", lambda: view(done).get("report"), children, timeout=60)
            cli("scheduler", "set", "--artifact-quota-bytes", "1")
            blocked = session.enqueue(projects["quota"])
            wait_for("대기 사유", lambda: view(blocked)["progress"].get("waiting_reason") == "storage_quota_exceeded", children, timeout=20)
            report = session.request("GET", f"runs/{done}/report")
            time.sleep(2)
            assert view(blocked)["status"] == "queued"
            cli("scheduler", "set", "--artifact-quota-bytes", "0")
            wait_status(blocked, {"partial", "completed"})
            r["observed"] = f"new run waited storage_quota_exceeded, earlier report readable ({len(report['artifacts'])} artifacts), runs after the limit is cleared"

        # --- D8 권한 회수 --------------------------------------------------------------------------------------
        with drill("D8 권한 회수", "열린 SSE 중 구성원 제거·사용자 비활성화, 실행 중 노드 token 회수") as r:
            viewer = json.loads(cli("user", "create", "--name", "Viewer").stdout)["id"]
            gone = json.loads(cli("user", "create", "--name", "Leaver").stdout)["id"]
            for uid in (viewer, gone):
                session.request("PUT", f"projects/{projects['quota']}/members/{uid}", {"role": "viewer"})
            vs, gs = login(viewer), login(gone)
            s1, s2 = EventReader(vs, done), EventReader(gs, done)
            time.sleep(1.5)
            t0 = time.monotonic()
            removal = Request(origin + f"/api/v1/projects/{projects['quota']}/members/{viewer}", method="DELETE",
                              headers={"Origin": origin, "X-CODEYE-CSRF": session.csrf})
            with session.client.open(removal, timeout=5) as response:
                assert response.status in (200, 204), response.status
            cli("user", "disable", "--id", gone)
            wait_for("두 stream 종료", lambda: s1.closed_at and s2.closed_at, children, timeout=20)
            closed = (s1.closed_reason(), s2.closed_reason())
            t_closed = max(s1.closed_at, s2.closed_at) - t0
            assert closed == ("access_revoked", "unauthenticated"), closed
            try:
                vs.request("GET", f"runs/{done}")
                raise AssertionError("제거된 구성원이 run을 읽음")
            except HTTPError as e:
                code_removed = e.code
            try:
                gs.request("GET", "session")
                raise AssertionError("비활성화된 사용자의 session이 유효함")
            except HTTPError as e:
                code_disabled = e.code
            assert code_removed == 404 and code_disabled == 401, (code_removed, code_disabled)
            # 노드 token 회수: 실행 중인 노드는 다음 요청에서 거절되어 종료한다.
            mode("rev", "sleep\n")
            rid = session.enqueue(projects["rev"])
            running_task(rid)
            node_id = q("SELECT id FROM nodes WHERE name='n3'")
            tree = descendants(n3.pid)
            t1 = time.monotonic()
            cli("node", "revoke", "--id", node_id)
            code = n3.wait(timeout=60)
            children.remove(n3)
            t_exit = time.monotonic() - t1
            assert code == 4, code
            time.sleep(1)
            left = [p for p in tree if alive(p)]
            assert not left, left
            wait_status(rid, {"recovery_required", "failed", "cancelled"}, timeout=90)
            status_after = view(rid)["status"]
            r["observed"] = (f"streams closed {closed} {t_closed:.1f}s after the change; removed member GET run {code_removed}, disabled user "
                             f"GET session {code_disabled}; revoked node exited code {code} after {t_exit:.1f}s, leftovers {left}, run {status_after}")
            if status_after == "recovery_required":
                mode("rev", "succeed\n")
                out = json.loads(cli("run", "recover", "--id", rid, "--confirm-cleanup").stdout)
                r["observed"] += f"; recover → {out['retry']}"

        stop(ctl)
        children.clear()

        # --- D1·D7 개인 모드 ----------------------------------------------------------------------------------
        personal_drills(root, env, directory, binary, fixture, go, cluster, drill)


def personal_drills(root, env, directory, binary, fixture, go, cluster, drill):
    from personal_smoke import Session as PSession

    pg_bin = Path(shutil.which("pg_dump")).parent
    go_dir = str(Path(go).parent)
    no_go_path = os.pathsep.join(p for p in env["PATH"].split(os.pathsep) if p != go_dir)
    launched = []

    def pcli(exe, *args, db, expect=0, timeout=200, path=None):
        run_env = {k: v for k, v in env.items() if not k.startswith("PG")}
        run_env.update(CODEYE_DATABASE_URL=cluster.url(db), GOTOOLCHAIN="local")
        for k in ("OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENCODE_API_KEY"):
            run_env.pop(k, None)
        if path:
            run_env["PATH"] = path
        if args[0] == "start":
            launched.append((exe, args[2], db))
        result = subprocess.run([str(exe), *map(str, args)], cwd=directory, env=run_env, capture_output=True, text=True, timeout=timeout)
        assert cluster.password not in result.stdout + result.stderr, "DB 인증정보 노출"
        if expect is not None and result.returncode != expect:
            raise RuntimeError(f"{args[0]} 종료 {result.returncode} (기대 {expect}): {(result.stdout + result.stderr)[-2000:]}")
        return result

    def config(name, roots, **extra):
        state = directory / name / "state"
        (directory / name).mkdir(mode=0o700)
        port = free_port()
        doc = {"schema_version": "0.1", "state_dir": str(state), "listen": f"127.0.0.1:{port}",
               "database": {"mode": "external", "postgres_bin_dir": str(pg_bin)},
               "source_roots": [{"label": label, "path": str(path)} for label, path in roots],
               "backup_dir": str(directory / "backups"), **extra}
        path = directory / name / "personal.json"
        path.write_text(json.dumps(doc, indent=2))
        path.chmod(0o600)
        return path, state, f"http://127.0.0.1:{port}", doc

    try:
        # D7 실행 중 백업과 새 상태 복원.
        # The restored installation B runs in offline mode, so its start,
        # codeye run recover and codeye cleanup (D9) are offline-mode runs.
        with drill("D7 실행 중 백업·복원", "작업 실행 중 온라인 백업 → 새 DB·state에 복원 → 복원본을 오프라인 모드로 시작") as r:
            for db in ("pa", "pb"):
                cluster.create(db)
            fsrc = directory / "psrc"
            fsrc.mkdir()
            (fsrc / "codeye-fixture-mode").write_text("succeed\n")
            fx_agent = {"auto_detect": False}
            stop_cfg = {"drain_timeout_seconds": 2, "cancel_timeout_seconds": 60}
            cfg_a, state_a, origin_a, _ = config("pa", [("fixture", fsrc)], agent=fx_agent, stop=stop_cfg)
            cfg_b, state_b, origin_b, _ = config("pb", [("fixture", fsrc)], agent=fx_agent, stop=stop_cfg, offline=True)
            pcli(fixture, "migrate", db="pa", path=no_go_path)
            pcli(fixture, "start", "--config", cfg_a, db="pa", path=no_go_path)
            sa = PSession(state_a, origin_a)
            froot = next(x for x in sa.request("GET", "source-roots")["items"] if x["label"] == "fixture")
            project = sa.request("POST", "projects", {"name": "fixture", "source": {"kind": "local", "source_root_id": froot["id"], "relative_path": "."}})
            first = sa.finished(sa.enqueue(project["id"]))
            report = sa.request("GET", f"runs/{first['id']}/report?revision={first['report']['revision']}")
            (fsrc / "codeye-fixture-mode").write_text("sleep\n")
            inflight = sa.enqueue(project["id"])
            wait_for("실행 중", lambda: sa.request("GET", f"runs/{inflight}")["status"] == "running", [], 60)
            t0 = time.monotonic()
            made = json.loads(pcli(fixture, "backup", "--config", cfg_a, "--output", directory / "backups/inflight", "--format", "json", db="pa").stdout)
            t_backup = time.monotonic() - t0
            stopped = json.loads(pcli(fixture, "stop", "--config", cfg_a, "--format", "json", db="pa").stdout)
            assert stopped["outcome"] == "stopped", stopped
            t1 = time.monotonic()
            restored = json.loads(pcli(fixture, "restore", "--config", cfg_b, "--from", directory / "backups/inflight", "--format", "json", db="pb").stdout)
            t_restore = time.monotonic() - t1
            fence = restored["fence"]
            assert fence["leased_remaining"] == 0, fence
            (fsrc / "codeye-fixture-mode").write_text("succeed\n")
            pcli(fixture, "start", "--config", cfg_b, db="pb", path=no_go_path)
            sb = PSession(state_b, origin_b)
            same = sb.request("GET", f"runs/{first['id']}/report?revision={first['report']['revision']}")
            assert same["summary"] == report["summary"] and same["artifacts"] == report["artifacts"], "복원 보고서 불일치"
            at_restore = sb.request("GET", f"runs/{inflight}")["status"]
            how = None
            if at_restore == "recovery_required":
                # The personal-mode CLI form (codeye run recover --config).
                out = json.loads(pcli(fixture, "run", "recover", "--id", inflight, "--confirm-cleanup", "--config", cfg_b, db="pb").stdout)
                how = out["retry"]
                follow = sb.finished(out["run"])
            else:
                follow = None
            fresh = sb.finished(sb.enqueue(project["id"]))
            assert fresh["status"] in ("partial", "completed"), fresh
            assert at_restore == "recovery_required" and how in ("new_run", "same_run") and follow["status"] in ("partial", "completed"), (at_restore, how, follow)
            r["observed"] = (f"online backup {t_backup:.1f}s (consistency {made['manifest']['consistency']}), restore {t_restore:.1f}s fence {fence}; "
                             f"same report summary and artifacts; restored installation in offline mode; the in-flight run was {at_restore} → codeye run recover --config {how} → {follow['status']}; new run {fresh['status']}")

        # D9 보존 정리: every finished run's artifacts are made 40 days old (created_at
        # is immutable, so as the throwaway cluster's superuser with triggers off).
        # All runs share the fixture's outputs, so only aging all of them frees files.
        with drill("D9 보존 정리(개인 모드)", "오프라인 모드 설치에서 끝난 run의 산출물을 40일 전으로 돌린 뒤 codeye cleanup --config dry run → --apply → 다시 dry run") as r:
            finished = "SELECT id FROM runs WHERE status IN ('completed','partial','failed','cancelled')"
            raw = int(cluster.scalar("pb", "SELECT count(*) FROM artifacts WHERE role IN ('stdout','stderr','raw_output','other') AND finalized "
                                           f"AND storage_key IS NOT NULL AND content_removed_at IS NULL AND run_id IN ({finished})"))
            cluster.scalar("pb", f"SET session_replication_role=replica; UPDATE artifacts SET created_at=created_at-interval '40 days' WHERE run_id IN ({finished})")
            files = lambda: sum(1 for f in (state_b / "artifacts").rglob("*") if f.is_file())  # noqa: E731

            def cleanup(*extra):
                return json.loads(pcli(fixture, "cleanup", "--config", cfg_b, "--older-than", "30d", "--format", "json", *extra, db="pb").stdout)
            before = files()
            dry = cleanup()
            assert raw > 0 and not dry["applied"] and dry["artifacts"]["rows"] == raw and dry["artifacts"]["freed_contents"] > 0, (raw, dry["artifacts"])
            assert files() == before and sb.request("GET", f"runs/{first['id']}")["raw_evidence_removed_at"] is None
            applied = cleanup("--apply")
            got = applied["artifacts"]
            assert applied["applied"] and got["rows"] == raw and got["failed_files"] == 0 and got["deleted_files"] > 0, got
            assert got["deleted_files"] + got["kept_files"] == got["freed_contents"] and files() == before - got["deleted_files"], (got, before, files())
            removed_at = sb.request("GET", f"runs/{first['id']}")["raw_evidence_removed_at"]
            kept = sb.request("GET", f"runs/{first['id']}/report?revision={first['report']['revision']}")
            assert removed_at and kept["summary"] == report["summary"], removed_at
            assert int(cluster.scalar("pb", "SELECT count(*) FROM audit_events WHERE action='storage.retention_applied'")) == 1
            assert cleanup()["artifacts"]["rows"] == 0
            r["observed"] = (f"dry run listed {raw} raw rows ({dry['artifacts']['freed_contents']} contents to free) and changed nothing; --apply marked them "
                             f"and deleted {got['deleted_files']} files (kept {got['kept_files']} empty or still referenced), files {before}→{files()}, "
                             f"one storage.retention_applied audit row, run shows raw_evidence_removed_at, report unchanged, second dry run lists 0")
        pcli(fixture, "stop", "--config", cfg_b, "--format", "json", db="pb")

        # D1 모델 endpoint 실패.
        opencode = os.environ.get("CODEYE_TEST_OPENCODE_BIN") or str(Path.home() / ".opencode/bin/opencode")
        with drill("D1 모델 endpoint 실패", "가짜 upstream이 모든 모델 요청에 HTTP 500(fake_model_upstream --mode fail)") as r:
            if not Path(opencode).is_file():
                r["result"], r["observed"] = "NOT RUN", "OpenCode not installed (CODEYE_TEST_OPENCODE_BIN)"
            else:
                cluster.create("pm")
                ready = directory / "fake-500.json"
                fake = subprocess.Popen([sys.executable, str(root / "scripts/fake_model_upstream.py"), "--ready-file", str(ready), "--mode", "fail"],
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                try:
                    wait_for("fake upstream", lambda: ready.exists() and ready.read_text().strip(), [], 30)
                    fport = json.loads(ready.read_text())["port"]
                    msrc = directory / "msrc"
                    (msrc / "ledger").mkdir(parents=True)
                    (msrc / "ledger/go.mod").write_text("module example.com/ledger\n\ngo 1.22\n")
                    (msrc / "ledger/ledger.go").write_text('package ledger\n\nimport "fmt"\n\nfunc Show() { fmt.Printf("%d\\n", "x") }\n')
                    cfg, state, morigin, _ = config("pm", [("work", msrc)], agent={"go": go, "max_workers": 1,
                                                                                   "model": {"timeout_seconds": 60, "review_max_calls": 15}})
                    (state / "secrets").mkdir(parents=True, mode=0o700)
                    key = "sk-codeye-drill-" + os.urandom(8).hex()
                    secret = state / "secrets/model.json"
                    secret.write_text(json.dumps({"schema_version": "0.1", "provider": "openai", "api_key": key, "base_url": f"http://127.0.0.1:{fport}/v1"}))
                    secret.chmod(0o600)
                    pcli(binary, "migrate", db="pm")
                    pcli(binary, "start", "--config", cfg, db="pm")
                    probe = pcli(binary, "model", "probe", "--config", cfg, db="pm", expect=1).stdout
                    sm = PSession(state, morigin)
                    work = next(x for x in sm.request("GET", "source-roots")["items"] if x["label"] == "work")
                    project = sm.request("POST", "projects", {"name": "ledger", "source": {"kind": "local", "source_root_id": work["id"], "relative_path": "ledger"}})
                    t0 = time.monotonic()
                    v = sm.finished(sm.enqueue(project["id"]), timeout=900)
                    t_run = time.monotonic() - t0
                    rep = sm.request("GET", f"runs/{v['id']}/report?revision={v['report']['revision']}")
                    ai = rep["coverage"]["ai_review"]
                    reasons = sorted({t.get("reason") for t in ai["required_tasks"] if t.get("reason")})
                    md, _ = sm.request("GET", f"runs/{v['id']}/report?revision={v['report']['revision']}&format=markdown", raw=True)
                    assert ai["status"] == "not_performed" and reasons, ai
                    # The static result stays: go vet's printf diagnostic is in the report.
                    assert rep["coverage"]["tools"] and rep["findings"], rep["findings"]
                    # OpenCode retries an HTTP 500 until the per-call timeout, so the
                    # recorded reason is model_unavailable or model_timeout; the
                    # Markdown report states it in words.
                    said = {"model_unavailable": "모델 공급자가 응답하지 않았거나 오류를 냈다",
                            "model_timeout": "모델 호출이 제한시간 안에 끝나지 않았다"}
                    model_reasons = [x for x in reasons if x in said]
                    assert model_reasons and all(said[x] in md.decode() for x in model_reasons), reasons
                    assert key not in json.dumps(rep) and key not in md.decode()
                    pcli(binary, "stop", "--config", cfg, db="pm")
                    first_probe = next((line.strip() for line in probe.splitlines() if "FAILED" in line), probe.strip()[:120])
                    r["observed"] = (f"probe: {first_probe}; run {v['status']} in {t_run:.0f}s with {len(rep['findings'])} tool findings; "
                                     f"ai_review not_performed {reasons}; Markdown states the reason")
                finally:
                    fake.send_signal(signal.SIGTERM)
                    fake.wait(timeout=10)
    finally:
        for exe, cfg, db in launched:
            subprocess.run([str(exe), "stop", "--config", str(cfg)], cwd=directory,
                           env=dict({k: v for k, v in env.items() if not k.startswith("PG")}, CODEYE_DATABASE_URL=cluster.url(db)),
                           capture_output=True, timeout=200)
