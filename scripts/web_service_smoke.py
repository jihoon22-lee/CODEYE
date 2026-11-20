"""여러 사용자의 한 서비스 수용 시험(TC-263): `codeye up`을 다른 머신에 서비스하고 두 사람이 웹을 쓴다.

scripts/test_postgres.py --web-service 가 실행한다. 두 번 띄운다.
plain: loopback 별칭의 HTTP에서 두 사용자가 가입·접속하고 서버 폴더와 비공개 PR을 분석한다.
tls: 자체 서명 TLS에서 가입·프로젝트 접근과 Secure cookie, 로컬 listener를 확인한다.
wildcard(TC-264): `--listen 0.0.0.0:PORT --plain-http`만으로 띄운다. up이 받는 주소를 모두 출력하고, 듣는 주소가 아닌
127.0.0.2로 로그인·사용자 추가(POST)가 되며, 받지 않는 Host는 받는 주소와 더하는 명령을 듣고, 그 명령 뒤에는 받는다.

시험용 빌드(-tags codeye_fixture)가 가짜 GitHub(scripts/fake_github.py)를 읽는다. 관리 PostgreSQL은 PATH의 프로그램으로
상태 디렉터리 아래에 만든다. HOME·XDG 디렉터리는 시험 디렉터리 안이다. CODEYE_TEST_SCREENSHOTS가 있으면 화면을 거기에 남긴다.
Linux 전용이다. 모델 호출·네트워크 다운로드는 없다.
"""
import json
import os
import shutil
import socket
import stat
import subprocess
from pathlib import Path


def free_port(host):
    with socket.socket() as sock:
        sock.bind((host, 0))
        return sock.getsockname()[1]


def self_signed(directory, ip):
    """A throwaway certificate for ip (openssl), its key 0600."""
    cert, key = directory / "cert.pem", directory / "key.pem"
    subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:prime256v1", "-nodes",
                    "-keyout", str(key), "-out", str(cert), "-days", "2", "-subj", "/CN=codeye-smoke",
                    "-addext", f"subjectAltName=IP:{ip}"], check=True, capture_output=True, timeout=30)
    key.chmod(0o600)
    return cert, key


def run(base, root, env):
    from fake_github import FakeGitHub
    from instance_smoke import bare_repository, git

    if not (root / "web/dist/index.html").exists():
        raise RuntimeError("--web-service needs the built web app (cd web && npm ci && npm run build)")
    for tool in ("openssl", "npm", "initdb", "pg_ctl"):
        if shutil.which(tool) is None:
            raise RuntimeError(f"--web-service needs {tool} on PATH")
    directory = base / "web-service-smoke"
    directory.mkdir(mode=0o700)
    # No GitHub token of this machine reaches the smoke: gh reads an empty configuration.
    env = {k: v for k, v in env.items() if k not in ("GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN")}
    (directory / "gh-config").mkdir(mode=0o700)
    env["GH_CONFIG_DIR"] = str(directory / "gh-config")
    fixture = directory / "codeye"
    subprocess.run(["go", "build", "-tags", "codeye_fixture", "-o", str(fixture), "./cmd/codeye"], cwd=root, env=env, check=True, timeout=180)
    (directory / "web").mkdir()
    (directory / "web/dist").symlink_to(root / "web/dist", target_is_directory=True)

    shared = directory / "srv/shared"
    (shared / "pkg").mkdir(parents=True)
    (shared / "go.mod").write_text("module example.com/shared\n\ngo 1.22\n")
    (shared / "pkg/p.go").write_text('package pkg\n\nfunc P() {}\n')
    mirror = directory / "git-mirror"
    bare_repository(mirror, "github.com/acme/secret", {"go.mod": "module example.com/private\n\ngo 1.22\n", "private.go": "package private\n"})
    repo = mirror / "github.com/acme/secret.git"
    head = git(repo, "rev-parse", "refs/heads/main")
    git(repo, "update-ref", "refs/pull/3/head", head)
    platform_token = "ghp_platform" + os.urandom(12).hex()
    fake = FakeGitHub(platform_token, "codeye-platform", allow_anonymous=True)
    fake.add_pull("acme/secret", 3, base_sha=head, head_sha=head, files=[], readers={"codeye-platform"})
    shots = os.environ.get("CODEYE_TEST_SCREENSHOTS")
    try:
        for variant in ("plain", "tls"):
            serve(directory, root, env, fixture, fake, variant, shared, mirror, platform_token, shots)
        wildcard(directory, env, fixture)
    finally:
        fake.close()


def serve(directory, root, env, fixture, fake, variant, shared, mirror, platform_token, shots):
    home = directory / variant
    home.mkdir(mode=0o700)
    state = home / "xdg-state/codeye"
    menv = {k: v for k, v in env.items() if k not in ("CODEYE_DATABASE_URL",) and not k.startswith("PG")}
    menv.update(HOME=str(home), XDG_STATE_HOME=str(home / "xdg-state"), XDG_CONFIG_HOME=str(home / "xdg-config"), BROWSER="/bin/true",
                CODEYE_TEST_GITHUB_API=fake.url, CODEYE_TEST_GIT_MIRROR=str(mirror), CODEYE_TEST_PLATFORM_TOKEN=platform_token)
    cfg = home / "xdg-config/codeye/config.json"
    cfg.parent.mkdir(parents=True, mode=0o700)
    cfg.write_text(json.dumps({"schema_version": "0.1", "github": {"token_env": {"github.com": "CODEYE_TEST_PLATFORM_TOKEN"}}}))
    cfg.chmod(0o600)

    def cli(*args, expect=0, timeout=300):
        result = subprocess.run([str(fixture), *map(str, args)], cwd=home, env=menv, capture_output=True, text=True, timeout=timeout)
        text = result.stdout + result.stderr
        assert platform_token not in text, f"{args[0]}: token 노출"
        if expect is not None and result.returncode != expect:
            raise RuntimeError(f"{args[0]} 종료 코드 {result.returncode} (기대 {expect}): {text[-3000:]}")
        return text

    if variant == "plain":
        ip = "127.0.0.2"
        port = free_port(ip)
        origin = f"http://{ip}:{port}"
        flags = ["--listen", f"{ip}:{port}", "--plain-http"]
    else:
        ip = "127.0.0.3"
        port = free_port(ip)
        origin = f"https://{ip}:{port}"
        cert, key = self_signed(home, ip)
        flags = ["--listen", f"{ip}:{port}", "--tls-cert", cert, "--tls-key", key, "--public-origin", origin]
    try:
        out = cli("up", "--no-open", *flags)
        print(f"--- codeye up ({variant}) ---\n" + "\n".join(line for line in out.splitlines() if "#launch=" not in line))
        for want in ("saved how the service listens in", f"CODEYE is running at {origin}", f"Other people can register with email and password at {origin}",
                     "github: platform account codeye-platform on github.com"):
            assert want in out, (want, out)
        assert ("plain HTTP on" in out) == (variant == "plain"), out
        link = next(line.strip() for line in out.splitlines() if "#launch=" in line)
        assert link.startswith(origin + "/#launch="), link
        link_file = home / "login-link-for-browser"
        link_file.write_text(link + "\n")
        link_file.chmod(0o600)
        config = json.loads((home / "xdg-config/codeye/config.json").read_text())
        assert config["listen"] == f"{ip}:{port}" and config.get("plain_http", False) == (variant == "plain"), config
        if variant == "tls":
            # The local agent and the commands use the loopback listener next to the TLS port.
            status = json.loads(cli("status", "--format", "json"))
            assert status["state"] == "running", status
        benv = dict(env, CODEYE_TEST_BASE_URL=origin, CODEYE_TEST_SERVICE_LINK_FILE=str(link_file),
                    CODEYE_TEST_SERVICE_FLOW="full" if variant == "plain" else "short", CODEYE_TEST_SERVICE_TLS="1" if variant == "tls" else "0",
                    CODEYE_TEST_SERVICE_ROOT=str(shared), CODEYE_TEST_SERVICE_STATE=str(state),
                    CODEYE_TEST_SERVICE_PRIVATE_PR="https://github.com/acme/secret/pull/3")
        if shots:
            Path(shots).mkdir(parents=True, exist_ok=True)
            benv["CODEYE_TEST_SCREENSHOTS"] = shots
        subprocess.run(["npm", "run", "test:e2e", "--", "service.spec.ts", "--global-timeout=600000"], cwd=root / "web", check=True, timeout=660, env=benv)

        if variant == "plain":
            asked = fake.logins_of("/repos/acme/secret/")
            assert asked and set(asked) == {"codeye-platform"}, asked
            credential = state / "secrets/github-platform.json"
            assert stat.S_IMODE(credential.stat().st_mode) == 0o600 and platform_token.encode() in credential.read_bytes()
            for path in state.rglob("*"):
                if path.is_file() and not path.is_symlink() and path != credential:
                    assert platform_token.encode() not in path.read_bytes(), f"token이 다른 상태 파일에 있음: {path.relative_to(state)}"
            print("PASS: 한 서비스의 두 사용자(HTTP): 가입·폴더 보고서·플랫폼 계정으로 비공개 pull request 분석")
        else:
            print("PASS: 한 서비스의 두 사용자(TLS): 가입·시작 화면·Secure cookie·로컬 listener")
    finally:
        subprocess.run([str(fixture), "stop"], cwd=home, env=menv, capture_output=True, timeout=300)


def wildcard(directory, env, fixture):
    """TC-264: every address of this machine works, with no extra flag."""
    import urllib.error
    from urllib.request import Request, urlopen
    from instance_smoke import Session

    home = directory / "wildcard"
    home.mkdir(mode=0o700)
    menv = {k: v for k, v in env.items() if k not in ("CODEYE_DATABASE_URL",) and not k.startswith("PG")}
    menv.update(HOME=str(home), XDG_STATE_HOME=str(home / "xdg-state"), XDG_CONFIG_HOME=str(home / "xdg-config"), BROWSER="/bin/true")

    def cli(*args):
        result = subprocess.run([str(fixture), *map(str, args)], cwd=home, env=menv, capture_output=True, text=True, timeout=300)
        if result.returncode != 0:
            raise RuntimeError(f"{args[0]} 종료 코드 {result.returncode}: {(result.stdout + result.stderr)[-3000:]}")
        return result.stdout + result.stderr

    port = free_port("0.0.0.0")
    try:
        out = cli("up", "--no-open", "--listen", f"0.0.0.0:{port}", "--plain-http")
        print("--- codeye up (wildcard) ---\n" + "\n".join(line for line in out.splitlines() if "#launch=" not in line))
        assert "The service answers on:" in out and f"http://127.0.0.1:{port}" in out and f"http://localhost:{port}" in out, out
        assert f"0.0.0.0:{port}/" not in out and f"//0.0.0.0" not in out, out
        link = next(line.strip() for line in out.splitlines() if "#launch=" in line)
        primary = link.split("/#launch=", 1)[0]
        config = json.loads((home / "xdg-config/codeye/config.json").read_text())
        assert config["listen"] == f"0.0.0.0:{port}" and config["plain_http"] is True and "public_origins" not in config, config
        # Sign in and post through an address that is not the listen address nor the one up printed first.
        second = f"http://127.0.0.2:{port}"
        session = Session(None, second, link)
        setup = session.request("GET", "setup")
        assert setup["is_admin"] and setup["service_origin"] == primary, setup["service_origin"]
        made = session.request("POST", "admin/users", {"username": "carol"})
        # 127.0.0.2 is loopback: the link is for someone else, so it carries the address to share.
        assert made["login_link"]["url"].startswith(primary + "/#launch="), made["login_link"]
        settings = session.request("GET", "admin/settings")
        kinds = {a["kind"] for a in settings["service_addresses"]}
        assert "this_machine" in kinds and all("0.0.0.0" not in a["origin"] for a in settings["service_addresses"]), settings
        # A Host this machine does not answer to hears which addresses work and how to add it.
        foreign = f"not-this-machine.example:{port}"
        try:
            urlopen(Request(second + "/api/v1/health", headers={"Host": foreign}), timeout=10)
            raise AssertionError("a foreign Host was answered")
        except urllib.error.HTTPError as e:
            body = json.load(e)
            assert e.code == 403 and body["error"]["code"] == "HOST_REJECTED", body
            command = f"codeye up --public-origin http://{foreign}"
            assert body["error"]["details"]["add_command"] == command and command in body["error"]["message"], body
            assert f"http://127.0.0.1:{port}" in body["error"]["details"]["accepted_origins"], body
        # The command it names works on the running service (the controller restarts with the new origin).
        out = cli("up", "--no-open", "--public-origin", f"http://{foreign}")
        assert "saved public_origins in" in out and "restarting the controller:" in out, out
        config = json.loads((home / "xdg-config/codeye/config.json").read_text())
        assert config["public_origins"] == [f"http://{foreign}"] and config["listen"] == f"0.0.0.0:{port}" and config["plain_http"] is True, config
        with urlopen(Request(second + "/api/v1/health", headers={"Host": foreign}), timeout=10) as r:
            assert r.status == 200
        status = json.loads(cli("status", "--format", "json"))
        assert status["state"] == "running", status
        print(f"PASS: 0.0.0.0의 plain HTTP(플래그 없이 이 머신의 모든 주소): 받는 주소 출력·두 번째 주소({second})로 로그인과 POST·"
              "loopback에서 만든 링크는 알려 줄 주소·받지 않는 Host의 안내와 그 명령으로 더하기(controller 재시작)")
    finally:
        subprocess.run([str(fixture), "stop"], cwd=home, env=menv, capture_output=True, timeout=300)
