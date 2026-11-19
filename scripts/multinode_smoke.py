"""여러 노드 수용 시험(Linux Native, 임시 PostgreSQL, 한 WSL2·Linux 호스트).

한 호스트에서 노드 신원 여러 개(각자 상태 디렉터리·도구 구성·소스 root)를 실제 `codeye agent`로 띄우고
실제 `codeye serve`(controller·sweep·autoscaler)에 붙인다. 물리 노드 여러 대, 실제 네트워크 분할, NFS는
검증하지 않는다. 장애는 SIGSTOP/SIGKILL, 로컬 TCP 프록시 차단으로만 만든다(root·Docker·네트워크 namespace 없음).

S1 능력 배정: 실제 go vet(--go)과 도구 없는 노드(unsupported 표시), 아무도 못 하는 run의 no_capable_node 대기 →
   능력 있는 노드 등록 뒤 시작(local root, Git host).
S2 노드 사망: agent와 그 worker·작업 프로세스 전부 SIGKILL → lease 만료 → recovery_required, 옛 epoch의 늦은
   complete 409·감사 기록 → 남은 프로세스 없음 확인 → 사용자 정리 확인(POST /runs/{id}/recover) → 다른 노드에서 완료 →
   죽은 agent 재시작(같은 상태) 뒤 다시 작업.
S3 연결 끊김: 프록시 차단(분할) 동안 재배정 없음, agent lease_lost·정리 → 복구 뒤 늦은 결과 거절, run failed,
   같은 노드가 다시 작업. lease 안의 짧은 SIGSTOP은 아무 일도 일으키지 않는다.
S4 drain: drain 중인 노드는 하던 일을 끝내고 새 일을 받지 않으며 autoscaler가 그 노드에 worker를 시작하지 않는다.
S5 agent 갱신: drain → 종료 → 다시 빌드한 바이너리를 같은 신원·상태 디렉터리로 시작 → 재등록 없이 작업.
시각은 이 호스트의 측정값이며 보장값이 아니다. 시험용 handler(-tags codeye_fixture)는 분석 결과가 아니다.
"""
from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import threading
import time
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import Request, urlopen

from node_smoke import Session, wait_for
from personal_smoke import bare_repository

TERMINAL = {"partial", "completed", "failed", "cancelled", "recovery_required"}


def log(message):
    print(f"[multinode-smoke {time.strftime('%H:%M:%S')}] {message}", flush=True)


class Proxy:
    """127.0.0.1:port → [::1]:port for one node's controller URL.

    cut() is a partition: new connections are accepted and held, open ones
    stop forwarding (requests hang until the client's own timeout).
    restore() closes everything cut and forwards again.
    """

    def __init__(self, port):
        self.port = port
        self.lock = threading.Lock()
        self.cut_now = False
        self.open = []
        self.poisoned = []
        self.listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.listener.bind(("127.0.0.1", port))
        self.listener.listen(64)
        threading.Thread(target=self._accept, daemon=True).start()

    def _accept(self):
        while True:
            try:
                client, _ = self.listener.accept()
            except OSError:
                return
            with self.lock:
                if self.cut_now:
                    self.poisoned.append([client])
                    continue
            try:
                upstream = socket.create_connection(("::1", self.port), timeout=5)
                upstream.settimeout(None)
            except OSError:
                client.close()
                continue
            pair = [client, upstream]
            with self.lock:
                self.open.append(pair)
            for src, dst in ((client, upstream), (upstream, client)):
                threading.Thread(target=self._pump, args=(pair, src, dst), daemon=True).start()

    def _pump(self, pair, src, dst):
        try:
            while True:
                data = src.recv(65536)
                if not data:
                    break
                with self.lock:
                    dropped = self.cut_now or any(pair is p for p in self.poisoned)
                if not dropped:
                    dst.sendall(data)
        except OSError:
            pass
        finally:
            for s in pair:
                try:
                    s.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass

    def cut(self):
        with self.lock:
            self.cut_now = True
            self.poisoned.extend(self.open)
            self.open = []

    def restore(self):
        with self.lock:
            self.cut_now = False
            poisoned, self.poisoned = self.poisoned, []
        for pair in poisoned:
            for s in pair:
                try:
                    s.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                s.close()

    def close(self):
        self.restore()
        self.listener.close()


def children_map():
    kids = {}
    for entry in os.listdir("/proc"):
        if not entry.isdigit():
            continue
        try:
            stat = Path(f"/proc/{entry}/stat").read_text()
        except OSError:
            continue
        kids.setdefault(int(stat.rsplit(")", 1)[1].split()[1]), []).append(int(entry))
    return kids


def descendants(pid):
    kids, out, todo = children_map(), [], [pid]
    while todo:
        for child in kids.get(todo.pop(), []):
            out.append(child)
            todo.append(child)
    return out


def comm(pid):
    try:
        return Path(f"/proc/{pid}/comm").read_text().strip()
    except OSError:
        return ""


def alive(pid):
    try:
        stat = Path(f"/proc/{pid}/stat").read_text()
    except OSError:
        return False
    return stat.rsplit(")", 1)[1].split()[0] != "Z"


def kill_node(agent):
    """SIGKILL an agent and every descendant (workers and their tasks run in
    their own process groups): freeze the tree with SIGSTOP first so nothing
    forks or reacts, then kill all. Returns the killed PIDs and their names."""
    os.kill(agent.pid, signal.SIGSTOP)
    frozen = {agent.pid}
    while True:
        fresh = [p for p in descendants(agent.pid) if p not in frozen]
        if not fresh:
            break
        for p in fresh:
            try:
                os.kill(p, signal.SIGSTOP)
            except ProcessLookupError:
                pass
        frozen.update(fresh)
    names = {p: comm(p) for p in frozen}
    for p in frozen:
        try:
            os.kill(p, signal.SIGKILL)
        except ProcessLookupError:
            pass
    agent.wait(timeout=10)
    return names


def without_enrollment(argv):
    """The same agent command line without the (already used) enrollment token."""
    argv = list(argv)
    if "--enroll-token-file" in argv:
        i = argv.index("--enroll-token-file")
        del argv[i:i + 2]
    return argv


def node_post(origin, secret, path, body):
    req = Request(origin + "/api/v1/" + path, method="POST", data=json.dumps(body).encode(),
                  headers={"Authorization": "Bearer " + secret, "Content-Type": "application/json", "Accept": "application/json"})
    try:
        with urlopen(req, timeout=5) as response:
            return response.status, json.load(response)
    except HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def run(base, root, env):
    try:
        return scenarios(base, root, env)
    except BaseException:
        # The temporary directory goes away with the harness: show the tails.
        for path in sorted((base / "multinode-smoke").glob("*.log")):
            lines = path.read_text(errors="replace").splitlines()[-12:]
            print(f"--- {path.name} (last {len(lines)} lines)", *lines, sep="\n", flush=True)
        raise


def scenarios(base, root, env):
    started = time.monotonic()
    measured = {}
    directory = base / "multinode-smoke"
    directory.mkdir(mode=0o700)
    binary = base / "codeye"
    fixture = directory / "codeye-fixture"
    subprocess.run(["go", "build", "-tags", "codeye_fixture", "-o", fixture, "./cmd/codeye"],
                   cwd=root, env=env, check=True, timeout=300)
    go = shutil.which("go")
    git = shutil.which("git")

    def cli(*args):
        result = subprocess.run([binary, *map(str, args)], cwd=directory, env=env, capture_output=True, timeout=30)
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

    # --- 입력: root마다 다른 노드가 맡는다 -----------------------------------------------------------------
    def go_copy(name):
        d = directory / "src" / name
        shutil.copytree(root / "fixtures" / "go-vet", d)
        return d

    def fixture_dirs(name, *projects):
        d = directory / "src" / name
        for p in projects:
            (d / p).mkdir(parents=True)
            (d / p / "codeye-fixture-mode").write_text("succeed\n")
        return d

    paths = {"go": go_copy("go"), "nogo": go_copy("nogo"), "late": go_copy("late"),
             "fx": fixture_dirs("fx", "kill", "part"), "a": fixture_dirs("a", "resume"),
             "b": fixture_dirs("b", "resume", "pause"), "c": fixture_dirs("c", "drain", "after")}
    user = json.loads(cli("user", "create", "--name", "Multinode fixture"))["id"]
    roots = {}
    for name, path in paths.items():
        roots[name] = json.loads(cli("source-root", "add", "--label", "mn-" + name, "--path", path))["id"]
        cli("source-root", "grant", "--root", roots[name], "--user", user)
    provider = json.loads(cli("provider", "add", "--kind", "github", "--host", "github.com"))["id"]
    mirror = directory / "mirror"
    bare_repository(mirror, "github.com/o/r", {"go.mod": "module example.com/r\n\ngo 1.22\n",
                                               "r.go": 'package r\n\nimport "fmt"\n\nfunc R() { fmt.Printf("%d\\n", "x") }\n'})
    cli("scheduler", "set", "--global-max-running", "8", "--project-max-running", "1")
    cli("pool", "set", "--name", "default", "--min-workers", "0", "--max-workers", "8",
        "--idle-timeout-seconds", "2", "--scale-up-cooldown-seconds", "0", "--scale-down-cooldown-seconds", "0")

    # controller는 [::1]:P에서만 듣는다. 분할 대상 노드만 127.0.0.1:P(프록시)로 붙는다.
    for _ in range(20):
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        try:
            with socket.socket(socket.AF_INET6) as sock6:
                sock6.bind(("::1", port))
            break
        except OSError:
            continue
    origin = f"http://[::1]:{port}"
    proxied = f"http://127.0.0.1:{port}"
    session = Session(origin)
    node_env = {key: env[key] for key in ("PATH", "LANG") if key in env}
    serve_argv = [binary, "serve", "--listen", f"[::1]:{port}", "--artifact-dir", directory / "artifacts",
                  "--reconcile-interval", "500ms"]

    def ready():
        try:
            return session.request("GET", "ready")
        except URLError:
            return None

    def view(run_id):
        return session.request("GET", f"runs/{run_id}")

    def wait_status(run_id, want, children, timeout=120):
        def probe():
            v = view(run_id)
            if v["status"] in want:
                return v
            # recovery_required still becomes failed when the node's cleanup report arrives.
            if v["status"] in TERMINAL - set(want) - {"recovery_required"}:
                raise RuntimeError(f"run {run_id} 상태 {v['status']} (기대 {want})")
            return None
        return wait_for(f"run {want}", probe, children, timeout)

    def node_of(run_id):
        return scalar(f"SELECT n.name FROM attempts a JOIN nodes n ON n.id=a.node_id WHERE a.run_id='{run_id}' ORDER BY a.number DESC LIMIT 1")

    def attempts(run_id):
        return count(f"SELECT count(*) FROM attempts WHERE run_id='{run_id}'")

    def starts_to(name):
        return count(f"SELECT count(*) FROM node_commands c JOIN nodes n ON n.id=c.node_id WHERE c.kind='start_worker' AND n.name='{name}'")

    def task_rows(run_id):
        return scalar(f"SELECT coalesce(string_agg(key || '=' || status || coalesce(':' || reason, ''), ' ' ORDER BY key), '') FROM tasks WHERE run_id='{run_id}'")

    with ExitStack() as services:
        def spawn(argv, launch_env, log_path):
            handle = services.enter_context(log_path.open("ab"))
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
                        raise RuntimeError(f"{log_path.stem} 종료 시간 초과") from None

            services.callback(cleanup)
            return child

        proxy = Proxy(port)
        services.callback(proxy.close)
        controller = spawn(serve_argv, env, directory / "controller.log")
        children = [controller]
        wait_for("controller 준비", ready, children)
        login = directory / "login"
        cli("login-link", "--user", user, "--output", login, "--origin", origin)
        token = login.read_text().strip().split("#launch=", 1)[1]
        session.csrf = session.request("POST", "session", {"launch_token": token})["csrf_token"]

        projects = {}
        for key, (name, rel) in {"go": ("go", "."), "nogo": ("nogo", "."), "late": ("late", "."),
                                 "kill": ("fx", "kill"), "part": ("fx", "part"), "a": ("a", "resume"),
                                 "b": ("b", "resume"), "pause": ("b", "pause"), "drain": ("c", "drain"),
                                 "after": ("c", "after")}.items():
            projects[key] = session.request("POST", "projects", {"name": "mn-" + key, "source": {
                "kind": "local", "source_root_id": roots[name], "relative_path": rel}})["id"]
        projects["git"] = session.request("POST", "projects", {"name": "mn-git", "source": {
            "kind": "git", "provider_id": provider, "repository_url": "https://github.com/o/r", "branch": "main"}})["id"]

        agents = {}

        def agent(name, exe, roots_of, extra, *, controller_url=origin, launch_env=None, enroll=True):
            state = directory / ("state-" + name)
            argv = [exe, "agent", "--controller", controller_url, "--state-dir", state, "--name", name, *extra]
            for r in roots_of:
                argv += ["--source-root", f"{roots[r]}={paths[r]}"]
            if enroll:
                token_file = directory / (name + ".token")
                grant = ["--source-root", ",".join(roots[r] for r in roots_of)] if roots_of else []
                cli("node", "token", "create", "--pool", "default", *grant, "--output", token_file)
                argv += ["--enroll-token-file", token_file]
            child = spawn(argv, launch_env or node_env, directory / (name + ".log"))
            children.append(child)
            agents[name] = (child, argv, state)
            return child

        def stop_agent(name):
            child = agents[name][0]
            child.send_signal(signal.SIGINT)
            assert child.wait(timeout=60) == 0, f"{name} 종료 코드"
            children.remove(child)

        def identity(name):
            return json.loads((agents[name][2] / "node.json").read_text())

        def agent_events(name):
            return [json.loads(line) for line in (directory / (name + ".log")).read_text().splitlines() if line.startswith("{")]

        # --- S1 능력 배정과 대기 ------------------------------------------------------------------------------
        # 도구는 배정 기준이 아니다: 기준 분석기가 하나라도 쓸 수 있으면 analysis:deep-review를 알리고, 없는
        # 도구의 작업은 unsupported로 남는다. 도구가 하나도 없는 노드는 알리지 않는다(docs/EXECUTION.md).
        ruff = shutil.which("ruff")
        other_tool = ["--ruff", ruff] if ruff else ["--advertise-without-baseline"]
        agent("go", binary, ["go"], ["--go", go, "--workers", "1"])
        agent("nogo", binary, ["nogo"], [*other_tool, "--workers", "1"])
        agent("bare", binary, ["late"], ["--workers", "1"])
        wait_for("go·nogo 노드 능력 광고", lambda: count(
            "SELECT count(*) FROM nodes WHERE name IN ('go','nogo') AND 'analysis:deep-review'=ANY(capabilities)") == 2, children)
        wait_for("bare 노드 heartbeat", lambda: count(
            f"SELECT count(*) FROM nodes WHERE name='bare' AND 'source:local:{roots['late']}'=ANY(capabilities)") == 1, children)
        assert count("SELECT count(*) FROM nodes WHERE name='bare' AND 'analysis:deep-review'=ANY(capabilities)") == 0
        r_go, r_nogo = session.enqueue(projects["go"]), session.enqueue(projects["nogo"])
        r_late, r_git = session.enqueue(projects["late"]), session.enqueue(projects["git"])
        for rid in (r_late, r_git):
            wait_for("대기 사유", lambda: view(rid)["progress"].get("waiting_reason") == "no_capable_node", children, timeout=30)
        v_go = wait_status(r_go, {"partial", "completed"}, children)
        v_nogo = wait_status(r_nogo, {"partial", "completed", "failed"}, children)
        assert node_of(r_go) == "go" and node_of(r_nogo) == "nogo", (node_of(r_go), node_of(r_nogo))
        go_tasks, nogo_tasks = task_rows(r_go), task_rows(r_nogo)
        vet_go = [t for t in go_tasks.split() if t.startswith("go-vet") or t.startswith("govet")]
        vet_nogo = [t for t in nogo_tasks.split() if t.startswith("go-vet") or t.startswith("govet")]
        assert vet_go and all("=succeeded" in t for t in vet_go), go_tasks
        assert vet_nogo and all("=unsupported:tool_not_configured" in t for t in vet_nogo), nogo_tasks
        assert v_nogo["status"] != "completed", v_nogo["status"]
        assert view(r_late)["status"] == "queued" and view(r_git)["status"] == "queued"
        log(f"S1 routing: go→go {v_go['status']} [{' '.join(vet_go)}]; nogo→nogo {v_nogo['status']} [{' '.join(vet_nogo)}]")
        t0 = time.monotonic()
        agent("late", binary, ["late"], ["--go", go, "--workers", "1"])
        wait_status(r_late, {"running", "finalizing", "partial", "completed"}, children)
        measured["S1 late node start → waiting run claimed"] = time.monotonic() - t0
        wait_status(r_late, {"partial", "completed"}, children)
        assert node_of(r_late) == "late"
        t0 = time.monotonic()
        agent("git", fixture, [], ["--git", git, "--git-host", "github.com", "--go", go, "--workers", "1"],
              launch_env=dict(node_env, CODEYE_TEST_GIT_MIRROR=str(mirror)))
        v_git = wait_status(r_git, {"partial", "completed"}, children)
        measured["S1 git node start → git run finished"] = time.monotonic() - t0
        assert node_of(r_git) == "git", node_of(r_git)
        git_vet = [t for t in task_rows(r_git).split() if t.startswith("go-vet") or t.startswith("govet")]
        assert git_vet and all("=succeeded" in t for t in git_vet), task_rows(r_git)
        for name in ("go", "nogo", "late", "git"):
            assert count(f"SELECT count(*) FROM attempts a JOIN nodes n ON n.id=a.node_id WHERE n.name='{name}'") == 1, name
        assert count("SELECT count(*) FROM attempts a JOIN nodes n ON n.id=a.node_id WHERE n.name='bare'") == 0
        log(f"S1 waiting no_capable_node (a tool-less node with the root advertises nothing) → late/git nodes took their runs (git {v_git['status']}): ok")
        for name in ("go", "nogo", "bare", "late", "git"):
            stop_agent(name)

        # --- S2·S3 준비: 고정 worker 노드 둘(alpha 직접, beta 프록시) -----------------------------------------
        for key in ("kill", "part"):
            (paths["fx"] / key / "codeye-fixture-mode").write_text("sleep\n")
        agent("alpha", fixture, ["fx", "a"], ["--workers", "1"])
        agent("beta", fixture, ["fx", "b"], ["--workers", "1"], controller_url=proxied)
        wait_for("alpha·beta worker", lambda: count(
            "SELECT count(DISTINCT w.node_id) FROM workers w JOIN nodes n ON n.id=w.node_id WHERE w.state='idle' AND n.name IN ('alpha','beta')") == 2, children)
        r_kill = session.enqueue(projects["kill"])
        wait_status(r_kill, {"running"}, children)
        r_part = session.enqueue(projects["part"])
        wait_status(r_part, {"running"}, children)
        wait_for("두 작업 실행", lambda: count(
            f"SELECT count(*) FROM tasks WHERE run_id IN ('{r_kill}','{r_part}') AND status='running'") == 2, children)
        # 어느 노드가 어느 run을 받았는지는 경쟁이다. alpha가 가진 run을 죽이고 beta가 가진 run을 끊는다.
        holder = {node_of(r_kill): r_kill, node_of(r_part): r_part}
        assert set(holder) == {"alpha", "beta"}, holder
        dead_run, cut_run = holder["alpha"], holder["beta"]
        dead_attempt = scalar(f"SELECT id FROM attempts WHERE run_id='{dead_run}'")
        cut_attempt = scalar(f"SELECT id FROM attempts WHERE run_id='{cut_run}'")
        dead_worker = scalar(f"SELECT worker_id FROM attempts WHERE id='{dead_attempt}'")
        cut_worker = scalar(f"SELECT worker_id FROM attempts WHERE id='{cut_attempt}'")
        # 실패 대체 노드: autoscale, fx와 c root. 지금은 대기 작업이 없어 worker가 없다.
        agent("gamma", fixture, ["fx", "c"], ["--autoscale", "--max-workers", "2"])
        wait_for("gamma 능력", lambda: count("SELECT count(*) FROM nodes WHERE name='gamma' AND worker_scaling='controller'"
                                           " AND 'analysis:deep-review'=ANY(capabilities)") == 1, children)

        # --- S2 SIGKILL과 S3 프록시 차단을 같이 시작해 lease 만료 대기를 겹친다 --------------------------------
        assert scalar(f"SELECT status FROM attempts WHERE id='{dead_attempt}'") == "leased"
        alpha = agents["alpha"][0]
        children.remove(alpha)
        t_fault = time.monotonic()
        killed = kill_node(alpha)
        proxy.cut()
        assert "sleep" in killed.values() and sum(1 for n in killed.values() if n == "codeye-fixture") >= 2, killed
        log(f"S2 SIGKILL alpha tree {sorted(set(killed.values()))} ({len(killed)} processes); S3 proxy cut for beta")
        wait_for("죽은 노드 프로세스 소멸", lambda: not any(alive(p) for p in killed), children, timeout=15)
        # 끊긴 beta의 agent는 lease 여유(20s)에서 스스로 작업을 멈추고 정리한다.
        wait_for("beta lease_lost", lambda: any(e.get("code") == "lease_lost" and e.get("attempt_id") == cut_attempt
                                                for e in agent_events("beta")), children, timeout=40)
        measured["S3 proxy cut → agent lease_lost (own watchdog)"] = time.monotonic() - t_fault
        wait_for("alpha stale", lambda: scalar("SELECT last_heartbeat_at < clock_timestamp() - interval '20 seconds' FROM nodes WHERE name='alpha'") == "t",
                 children, timeout=40)
        measured["S2 kill → node stale (20s rule)"] = time.monotonic() - t_fault
        wait_for("lease 만료 → recovery_required", lambda: scalar(
            f"SELECT string_agg(status, ',' ORDER BY id) FROM attempts WHERE id IN ('{dead_attempt}','{cut_attempt}')") == "recovery_required,recovery_required",
            children, timeout=60)
        measured["S2/S3 fault → both attempts recovery_required"] = time.monotonic() - t_fault
        assert view(dead_run)["status"] == view(cut_run)["status"] == "recovery_required"
        # 분할·사망 동안 다른 노드(gamma)가 같은 run을 다시 실행하지 않는다.
        time.sleep(3)
        assert attempts(dead_run) == attempts(cut_run) == 1, "recovery_required run이 재배정됨"
        assert starts_to("gamma") == 0, "recovery_required run 때문에 worker를 시작함"
        assert scalar(f"SELECT cleanup_state FROM attempts WHERE id='{dead_attempt}'") == "uncertain"

        # 옛 epoch의 늦은 결과: 죽은 노드의 자격으로 보낸 complete는 409이고 감사에 남는다.
        alpha_id = identity("alpha")
        late = {"worker_id": dead_worker, "lease_epoch": 1, "outcome": "failed", "cleanup_state": "confirmed",
                "result_manifest_id": None, "reason": "late_result"}
        stale_before = count(f"SELECT count(*) FROM audit_events WHERE action='attempt.stale_rejected' AND target_id='{dead_attempt}'")
        status, body = node_post(origin, alpha_id["node_secret"], f"attempts/{dead_attempt}/complete", late)
        assert status == 409 and body.get("error", {}).get("code") == "STALE_ATTEMPT", (status, body)
        assert count(f"SELECT count(*) FROM audit_events WHERE action='attempt.stale_rejected' AND target_id='{dead_attempt}'") == stale_before + 1
        assert scalar(f"SELECT (completion_digest IS NULL)::text FROM attempts WHERE id='{dead_attempt}'") == "true"
        log("S2 late complete from the dead node's epoch → 409 STALE_ATTEMPT + attempt.stale_rejected")

        # 운영자 확인: 노드에 그 run의 프로세스가 없음을 보고(위 kill_node 결과) 정리를 확인한다.
        assert not any(alive(p) for p in killed)
        (paths["fx"] / "kill" / "codeye-fixture-mode").write_text("succeed\n")
        try:
            session.request("POST", f"runs/{dead_run}/recover", {"cleanup_confirmed": False})
            raise AssertionError("cleanup_confirmed=false accepted")
        except HTTPError as e:
            assert e.code == 400, e.code
        t0 = time.monotonic()
        ack = session.request("POST", f"runs/{dead_run}/recover", {"cleanup_confirmed": True})
        assert ack["retry"] == "new_run" and ack["attempt_id"] == dead_attempt, ack
        assert scalar(f"SELECT status || ':' || cleanup_state FROM attempts WHERE id='{dead_attempt}'") == "released:confirmed"
        assert view(dead_run)["status"] == "failed"
        assert count(f"SELECT count(*) FROM audit_events WHERE action='run.recovery_confirmed' AND target_id='{dead_run}' AND actor_kind='user'") == 1
        follow = ack["id"]
        wait_status(follow, {"partial", "completed"}, children)
        measured["S2 recover → follow-up run finished on gamma"] = time.monotonic() - t0
        assert node_of(follow) == "gamma", node_of(follow)
        # 새 attempt가 끝난 뒤에도 옛 결과는 거절되고 새 결과는 그대로다.
        follow_digest = scalar(f"SELECT completion_digest FROM attempts WHERE run_id='{follow}'")
        status, body = node_post(origin, alpha_id["node_secret"], f"attempts/{dead_attempt}/complete", late)
        assert status == 409, (status, body)
        assert scalar(f"SELECT completion_digest FROM attempts WHERE run_id='{follow}'") == follow_digest and follow_digest
        log("S2 recovery confirmed by the user (audited) → new run completed on gamma; second late complete 409")

        # --- S3 복구: 프록시를 되살리면 beta가 정리 보고를 보내고 run은 failed, 재배정 없음 ----------------------
        t0 = time.monotonic()
        proxy.restore()
        v_cut = wait_status(cut_run, {"failed"}, children, timeout=60)
        measured["S3 restore → cleanup report accepted, run failed"] = time.monotonic() - t0
        assert scalar(f"SELECT status || ':' || cleanup_state || ':' || (completion_digest IS NULL) FROM attempts WHERE id='{cut_attempt}'") == "released:confirmed:true"
        assert attempts(cut_run) == 1, "분할 동안 중복 실행"
        beta_codes = {e.get("code") for e in agent_events("beta") if e.get("attempt_id") == cut_attempt}
        assert {"lease_lost", "cleanup_reported"} <= beta_codes, beta_codes
        beta_id = identity("beta")
        status, _ = node_post(origin, beta_id["node_secret"], f"attempts/{cut_attempt}/complete",
                              dict(late, worker_id=cut_worker))
        assert status == 409, status
        leftovers = [p for p in descendants(agents["beta"][0].pid) if comm(p) == "sleep"]
        assert not leftovers, leftovers
        log(f"S3 partition: no reassignment, beta stopped its task (lease_lost), late complete 409, run {v_cut['status']}")
        r_b = session.enqueue(projects["b"])
        wait_status(r_b, {"partial", "completed"}, children)
        assert node_of(r_b) == "beta"
        # lease 안의 짧은 정지(8s < 여유 20s): 같은 attempt가 그대로 끝난다.
        (paths["b"] / "pause" / "codeye-fixture-mode").write_text("pause\n14\n")
        r_pause = session.enqueue(projects["pause"])
        wait_for("pause 작업 실행", lambda: count(f"SELECT count(*) FROM tasks WHERE run_id='{r_pause}' AND status='running'") == 1, children)
        beta = agents["beta"][0]
        beta.send_signal(signal.SIGSTOP)
        time.sleep(8)
        beta.send_signal(signal.SIGCONT)
        wait_status(r_pause, {"partial", "completed"}, children, timeout=60)
        assert scalar(f"SELECT number || ':' || epoch || ':' || status || ':' || cleanup_state FROM attempts WHERE run_id='{r_pause}'") == "1:1:released:confirmed"
        assert not any(e.get("code") == "lease_lost" and e.get("attempt_id") != cut_attempt for e in agent_events("beta"))
        log("S3 beta resumed claiming; an 8s SIGSTOP inside the lease changed nothing")

        # 죽었던 alpha를 같은 상태로 다시 시작: 남은 journal을 정리 보고하고 다시 작업한다.
        t0 = time.monotonic()
        alpha2 = spawn(without_enrollment(agents["alpha"][1]), node_env, directory / "alpha.log")
        children.append(alpha2)
        agents["alpha"] = (alpha2, agents["alpha"][1], agents["alpha"][2])
        r_a = session.enqueue(projects["a"])
        wait_status(r_a, {"partial", "completed"}, children)
        measured["S2 dead agent restart → new run finished on it"] = time.monotonic() - t0
        assert node_of(r_a) == "alpha" and identity("alpha")["node_id"] == alpha_id["node_id"]
        assert count("SELECT count(*) FROM nodes WHERE name='alpha'") == 1
        alpha_codes = {e.get("code") for e in agent_events("alpha") if e.get("attempt_id") == dead_attempt}
        assert "cleanup_reported" in alpha_codes, alpha_codes
        log(f"S2 restarted alpha reported the old attempt ({sorted(alpha_codes)}) and took new work")
        stop_agent("alpha")
        stop_agent("beta")

        # --- S4 drain: 하던 일은 끝내고 새 일·worker 시작은 없다 --------------------------------------------------
        (paths["c"] / "drain" / "codeye-fixture-mode").write_text("pause\n15\n")
        r_drain = session.enqueue(projects["drain"])
        wait_for("drain 대상 작업 실행", lambda: count(f"SELECT count(*) FROM tasks WHERE run_id='{r_drain}' AND status='running'") == 1, children)
        assert node_of(r_drain) == "gamma"
        gamma = agents["gamma"][0]
        t0 = time.monotonic()
        gamma.send_signal(signal.SIGUSR1)
        wait_for("drain heartbeat", lambda: scalar("SELECT worker_scaling || ':' || (NOT 'analysis:deep-review'=ANY(capabilities))::text FROM nodes WHERE name='gamma'") == "static:true",
                 children, timeout=20)
        starts_at_drain = starts_to("gamma")
        r_after = session.enqueue(projects["after"])
        wait_for("drain 중 대기 사유", lambda: view(r_after)["progress"].get("waiting_reason") == "no_capable_node", children, timeout=20)
        time.sleep(4)
        assert starts_to("gamma") == starts_at_drain, "drain 중인 노드에 worker 시작 명령"
        assert view(r_after)["status"] == "queued" and attempts(r_after) == 0
        assert view(r_drain)["status"] in {"running", "finalizing"}, view(r_drain)["status"]
        children.remove(gamma)  # a drained agent exits by itself after its run
        wait_status(r_drain, {"partial", "completed"}, children, timeout=60)
        assert scalar(f"SELECT number || ':' || status || ':' || cleanup_state FROM attempts WHERE run_id='{r_drain}'") == "1:released:confirmed"
        assert gamma.wait(timeout=60) == 0, "drain 뒤 agent 종료 코드"
        measured["S4 drain signal → agent exited after its run"] = time.monotonic() - t0
        assert any(e.get("code") == "agent_drained" for e in agent_events("gamma"))
        assert starts_to("gamma") == starts_at_drain
        log("S4 drain: the running work finished, new run waited (no_capable_node), no start_worker to gamma, agent exited 0")

        # --- S5 agent 갱신: 다시 빌드한 바이너리, 같은 신원·상태, 재등록 없음 -----------------------------------------
        updated = directory / "codeye-fixture-updated"
        t0 = time.monotonic()
        subprocess.run(["go", "build", "-trimpath", "-tags", "codeye_fixture", "-o", updated, "./cmd/codeye"],
                       cwd=root, env=env, check=True, timeout=300)
        measured["S5 rebuild (go build -trimpath)"] = time.monotonic() - t0
        digest = lambda p: hashlib.sha256(Path(p).read_bytes()).hexdigest()
        assert digest(updated) != digest(fixture), "갱신 바이너리가 같음"
        enrolled = count("SELECT count(*) FROM audit_events WHERE action='node.enrolled'")
        gamma_id = identity("gamma")
        argv = without_enrollment([updated if str(a) == str(fixture) else a for a in agents["gamma"][1]])
        t0 = time.monotonic()
        gamma2 = spawn(argv, node_env, directory / "gamma.log")
        children.append(gamma2)
        agents["gamma"] = (gamma2, argv, agents["gamma"][2])
        wait_status(r_after, {"partial", "completed"}, children)
        measured["S5 updated agent start → waiting run finished"] = time.monotonic() - t0
        assert node_of(r_after) == "gamma" and identity("gamma") == gamma_id
        assert count("SELECT count(*) FROM audit_events WHERE action='node.enrolled'") == enrolled
        assert scalar("SELECT protocol_version || ':' || worker_scaling FROM nodes WHERE name='gamma'") == "0.1:controller"
        assert count("SELECT count(*) FROM nodes") == 8
        log("S5 update: same node_id and secret, no enrollment, protocol 0.1, took the waiting run")
        stop_agent("gamma")
        assert count("SELECT count(*) FROM attempts WHERE status<>'released' OR cleanup_state<>'confirmed'") == 0
        controller.send_signal(signal.SIGINT)
        children.remove(controller)
        assert controller.wait(timeout=60) == 0, "controller 종료 코드"
    measured["total"] = time.monotonic() - started
    for key, seconds in measured.items():
        log(f"measured {key}: {seconds:.1f}s")
    print("PASS: 여러 노드(한 호스트) 능력 배정·대기·노드 사망 복구·분할·drain·agent 갱신 (시험용 handler 포함)")
    return measured
