"""Shared harness for the regression pilot (regression_pilot.py) and the evaluation runner (quality_eval.py).

It builds the product binary, runs a real controller and real agents against a
throwaway PostgreSQL (test_postgres.temporary_postgres), creates projects over
a local source root and fetches published reports. It sends no model request
itself; whether agents call a model is decided by the agent arguments the
caller passes (an approved relay file, or none).
"""
from __future__ import annotations

from contextlib import ExitStack
import hashlib
import json
import os
import shutil
import socket
import subprocess
import tarfile
import time
from urllib.error import URLError

from node_smoke import Session, service, wait_for

REVIEW_MEDIA = ("application/vnd.codeye.review+json", "application/vnd.codeye.environment+json",
                "application/vnd.codeye.review-scope+json")
TERMINAL = {"completed", "partial", "failed", "cancelled", "recovery_required"}


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


def keep_review_artifacts(report, artifacts, destination):
    """Copy the model-role, review-scope and discovery artifacts a report references."""
    index = {f.name: f for f in artifacts.rglob("*") if f.is_file()}
    destination.mkdir(mode=0o700)
    kept = 0
    for a in report["artifacts"]:
        if a["media_type"] not in REVIEW_MEDIA:
            continue
        found = index.get(a["sha256"])
        if found is None or hashlib.sha256(found.read_bytes()).hexdigest() != a["sha256"]:
            raise RuntimeError("보고서가 참조한 검토 산출물을 찾지 못했습니다")
        name = (a["task_key"] or a["role"]) + "-" + a["sha256"][:12] + ".json"
        shutil.copyfile(found, destination / name)
        kept += 1
    return kept


def child_env(env):
    """The controller's environment without any model key."""
    return {k: v for k, v in env.items() if k != "OPENAI_API_KEY"}


def build_product(base, root, env):
    """Build the codeye binary from this checkout and migrate the temp database."""
    binary = base / "codeye"
    subprocess.run(["go", "build", "-o", str(binary), "./cmd/codeye"], cwd=root, env=env, check=True, timeout=900)
    subprocess.run([str(binary), "migrate"], cwd=base, env=env, check=True, capture_output=True, timeout=120)
    return binary


class Cluster:
    """A real controller plus enrolled agents over one approved local source root.

    agent_args(root_id) returns the extra agent arguments (tools, grants, model
    relay); every agent gets the same ones. Agents run with PATH and LANG only.
    """

    def __init__(self, base, binary, env, source, *, agent_args, agents=1, max_workers=2,
                 label="evaluation inputs", agent_path=None, web_dir=None, pool_max=None):
        self.base, self.binary, self.env, self.source = base, binary, child_env(env), source
        self.agent_args, self.agents, self.max_workers, self.label = agent_args, agents, max_workers, label
        self.web_dir, self.pool_max = web_dir, pool_max or agents * max_workers
        self.agent_path = agent_path or self.env.get("PATH", "/usr/bin:/bin")
        self.work = base / "cluster"
        self.stack = ExitStack()
        self.children = []

    def cli(self, *args, timeout=30):
        r = subprocess.run([self.binary, *args], cwd=self.work, env=self.env, capture_output=True, timeout=timeout)
        if r.returncode:
            raise RuntimeError(f"운영 명령 실패 {args[0]} ({r.returncode}): {r.stderr.decode(errors='replace')[-300:]}")
        return r.stdout

    def __enter__(self):
        self.work.mkdir(mode=0o700)
        if self.web_dir is None:  # the API is enough for evaluation runs
            self.web_dir = self.work / "web"
            self.web_dir.mkdir()
        try:
            self._start()
        except BaseException:
            self.stack.close()
            raise
        return self

    def _start(self):
        self.user = json.loads(self.cli("user", "create", "--name", "evaluator"))["id"]
        self.root_id = json.loads(self.cli("source-root", "add", "--label", self.label, "--path", self.source))["id"]
        self.cli("source-root", "grant", "--root", self.root_id, "--user", self.user)
        with socket.socket() as s:
            s.bind(("127.0.0.1", 0))
            port = s.getsockname()[1]
        self.origin = f"http://127.0.0.1:{port}"
        self.api = Session(self.origin)

        def ready():
            try:
                return self.api.request("GET", "ready")
            except URLError:
                return None

        self.children.append(self.stack.enter_context(service(
            [self.binary, "serve", "--listen", f"127.0.0.1:{port}", "--web-dir", self.web_dir,
             "--artifact-dir", self.work / "artifacts", "--reconcile-interval", "1s"],
            self.work, self.env, self.work / "control.log")))
        wait_for("controller API", ready, self.children)
        link = self.work / "launch"
        self.cli("login-link", "--user", self.user, "--output", link, "--origin", self.origin)
        self.api.csrf = self.api.request(
            "POST", "session", {"launch_token": link.read_text().strip().split("#launch=", 1)[1]})["csrf_token"]
        link.unlink()
        extra = [str(a) for a in self.agent_args(self.root_id)]
        for i in range(self.agents):
            name = f"eval-{i + 1}"
            token = self.work / (name + ".enroll")
            self.cli("node", "token", "create", "--pool", "default", "--source-root", self.root_id, "--output", token)
            self.children.append(self.stack.enter_context(service(
                [self.binary, "agent", "--controller", self.origin, "--state-dir", self.work / name, "--name", name,
                 "--enroll-token-file", token, "--source-root", f"{self.root_id}={self.source}",
                 "--autoscale", "--max-workers", str(self.max_workers), *extra],
                self.work, {"PATH": self.agent_path, "LANG": "C.UTF-8"}, self.work / (name + ".log"))))
        self.cli("pool", "set", "--name", "default", "--min-workers", "0", "--max-workers",
                 str(self.pool_max), "--idle-timeout-seconds", "3",
                 "--scale-up-cooldown-seconds", "0", "--scale-down-cooldown-seconds", "0")

    def __exit__(self, *exc):
        self.stack.close()
        return False

    def alive(self):
        return all(p.poll() is None for p in self.children)

    def submit(self, name, rel):
        project = self.api.request("POST", "projects", {
            "name": name, "source": {"kind": "local", "source_root_id": self.root_id, "relative_path": rel}})
        return self.api.enqueue(project["id"])

    def run(self, run_id):
        return self.api.request("GET", "runs/" + run_id)

    def report(self, run_id):
        return self.api.request("GET", f"runs/{run_id}/report?format=json&include_code=true")

    def wait(self, run_ids, timeout, on_done=None, poll=1.0):
        """Poll until every run is terminal; returns {run_id: run}. on_done(run_id, run) is called once each."""
        pending, done = set(run_ids), {}
        deadline = time.monotonic() + timeout
        while pending:
            if not self.alive():
                raise RuntimeError("controller 또는 agent가 종료됐습니다")
            if time.monotonic() > deadline:
                raise TimeoutError(f"{len(pending)} runs did not finish in {timeout}s")
            for run_id in sorted(pending):
                run = self.run(run_id)
                if run["status"] in TERMINAL:
                    pending.discard(run_id)
                    done[run_id] = run
                    if on_done:
                        on_done(run_id, run)
            if pending:
                time.sleep(poll)
        return done


def file_sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def home_relative(path):
    """Paths in manifests are recorded relative to $HOME to avoid personal prefixes."""
    p, home = str(path), os.path.expanduser("~")
    return "~" + p[len(home):] if home and p.startswith(home + os.sep) else p
