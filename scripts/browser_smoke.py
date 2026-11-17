"""실제 임시 DB와 codeye serve를 사용하는 브라우저 smoke. test_postgres.py에서 호출."""
import json
import os
from pathlib import Path
import socket
import signal
import subprocess
import time
import urllib.request


def run(base: Path, root: Path, env: dict):
    binary = base / "codeye"

    def cli(*args):
        result = subprocess.run([str(binary), *args], cwd=root, env=env, capture_output=True, timeout=20)
        if result.returncode != 0:
            raise RuntimeError("브라우저 fixture CLI 준비 실패: " + args[0])
        return result.stdout.decode()

    user = json.loads(cli("user", "create", "--name", "Browser fixture"))["id"]
    source = base / "source"
    source.mkdir()
    (source / "README.md").write_text("browser source fixture\n")
    source_id = json.loads(cli("source-root", "add", "--label", "browser-fixture-root", "--path", str(source)))["id"]
    cli("source-root", "grant", "--root", source_id, "--user", user)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    link = base / "login-link"
    cli("login-link", "--user", user, "--output", str(link), "--origin", origin)
    with (base / "http.log").open("w+") as log:
        server = subprocess.Popen([str(binary), "serve", "--listen", f"127.0.0.1:{port}",
                                   "--web-dir", str(root / "web/dist")], cwd=root, env=env,
                                  stdout=log, stderr=log)
        try:
            for _ in range(100):
                if server.poll() is not None:
                    raise RuntimeError("브라우저 fixture 서버 시작 실패")
                try:
                    with urllib.request.urlopen(origin + "/api/v1/ready", timeout=1) as response:
                        if response.status == 200:
                            break
                except OSError:
                    time.sleep(0.1)
            else:
                raise RuntimeError("브라우저 fixture 서버 준비 시간 초과")
            browser_env = dict(env, CODEYE_TEST_BASE_URL=origin, CODEYE_TEST_LOGIN_FILE=str(link))
            subprocess.run(["npm", "run", "test:e2e"], cwd=root / "web", env=browser_env, check=True, timeout=120)
            log.flush()
            text = (base / "http.log").read_text()
            token = link.read_text().strip().split("#launch=", 1)[1]
            assert token not in text and env["CODEYE_DATABASE_URL"] not in text, "HTTP 로그 credential 노출"
            assert (source / "README.md").read_text() == "browser source fixture\n", "원본 fixture 변경"
        finally:
            server.send_signal(signal.SIGINT)
            try:
                server.wait(timeout=15)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait(timeout=5)
