#!/usr/bin/env python3
"""Offline pack check (TC-30): install from the archive and analyze with no network.

  offline_smoke.py --pack codeye-*.tar.gz --postgres-bin-dir DIR [--work DIR]

Linux only, no root. The whole run happens in a new user and network namespace
(`unshare -rn`, loopback only), so any connection to the outside fails at once
and is visible. The nameservers of /etc/resolv.conf are added to the namespace's
loopback with a logger that records every DNS name asked and answers NXDOMAIN;
`strace -f -e trace=connect` records every connect of the process tree. The
report lists both.

Inside, from the unpacked archive only (`codeye` and its `web/dist`), with a
fresh home, configuration and state:
  1. `sha256sum -c SHA256SUMS` and `offline_pack.py verify`;
  2. `codeye up --offline` on a Go project whose module is in the operator's
     module cache (prepared) and on one whose module is not
     (`offline_no_registry_configured`); a model key file without base_url is
     refused and an API key in the environment is not stored;
  3. an "internal mirror": one HTTPS server with a private CA serving a Go module
     proxy, a PyPI simple index, an npm registry and a Git repository (smart
     HTTP through `git http-backend`), and the dry-run model upstream
     (scripts/fake_model_upstream.py) behind the same CA. With `ca_bundle`,
     `go_proxy`, `pip_index_url`, `npm_registry`, `git_hosts` and a key file
     whose base_url is the internal endpoint, the missing module, a Python
     wheel and an npm package are fetched from the mirror, a Git project is
     acquired from it and the model review goes through the internal endpoint;
  4. each tool against the mirror with and without the CA bundle (go, git, npm,
     pnpm, yarn, pip, uv, OpenCode): with it the request succeeds, without it
     TLS verification fails;
  5. `codeye doctor --config --format json` against schemas/doctor.schema.json,
     `codeye tools --checklist`, and the web app served from the archive.
No model is called (the upstream is the dry-run double) and nothing is downloaded
from outside: the mirror's content is copied from this machine's Go module cache
or built here.
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import io
import json
import os
import shutil
import signal
import socket
import ssl
import struct
import subprocess
import sys
import tarfile
import threading
import time
import zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
sys.path.insert(0, str(HERE))

TERMINAL = {"partial", "completed", "failed", "cancelled", "recovery_required"}
GO_SEEDED = ("github.com/google/uuid", "v1.6.0")
GO_MIRRORED = ("github.com/spf13/pflag", "v1.0.6")
DNS_PROBE = "codeye-dns-check.invalid"


def log(*a):
    print(*a, flush=True)


# --- outer: namespaces and the audit -------------------------------------------------

def nameservers() -> list[str]:
    out = []
    try:
        for line in Path("/etc/resolv.conf").read_text().splitlines():
            parts = line.split()
            if len(parts) >= 2 and parts[0] == "nameserver":
                out.append(parts[1])
    except OSError:
        pass
    return out


def outer(args) -> int:
    for tool in ("unshare", "strace", "ip", "openssl", "git", "sha256sum"):
        if not shutil.which(tool):
            raise SystemExit(f"{tool} is required")
    work = Path(args.work).resolve()
    if work.exists() and any(work.iterdir()):
        raise SystemExit(f"{work} must be empty")
    work.mkdir(parents=True, exist_ok=True)
    tools = {
        "go": shutil.which("go"), "opencode": args.opencode or str(Path.home() / ".opencode/bin/opencode"),
        "uv": shutil.which("uv"), "node": shutil.which("node"),
        "npm": None, "pnpm": None, "yarn": None, "mypy": None, "tsc": None, "staticcheck": None,
        "gomodcache": subprocess.run(["go", "env", "GOMODCACHE"], capture_output=True, text=True, check=True).stdout.strip(),
    }
    prefix = Path.home() / ".local/share/codeye-tools"
    for name, rel in (("mypy", "bin/mypy"), ("staticcheck", "go/bin/staticcheck"), ("tsc", "node/node_modules/typescript/bin/tsc"),
                      ("pnpm", "node/node_modules/pnpm/bin/pnpm.cjs"), ("yarn", "node/node_modules/yarn/bin/yarn.js")):
        if (prefix / rel).exists():
            tools[name] = str(prefix / rel)
    if tools["node"]:
        npm = Path(os.path.realpath(tools["node"])).parent.parent / "lib/node_modules/npm/bin/npm-cli.js"
        tools["npm"] = str(npm) if npm.exists() else None
    for name in ("go", "npm"):
        if not tools[name]:
            raise SystemExit(f"{name} is required (the Go and JS/TS projects)")
    (work / "tools.json").write_text(json.dumps(tools, indent=2))
    me = [sys.executable, str(Path(__file__).resolve())]
    common = ["--work", str(work), "--pack", str(Path(args.pack).resolve()), "--postgres-bin-dir", str(Path(args.postgres_bin_dir).resolve())]
    proc = subprocess.run(["unshare", "-rn", *me, "--layer", "net", "--map-uid", str(os.getuid()), "--map-gid", str(os.getgid()), *common])
    clean = audit(work)
    return proc.returncode or (0 if clean else 1)


def layer_net(args) -> int:
    """Root of the new user namespace: loopback up, DNS loggers, then the
    scenario as the original user inside a nested user namespace."""
    work = Path(args.work)
    subprocess.run(["ip", "link", "set", "lo", "up"], check=True)
    loggers = []
    for ns in nameservers():
        family = "-6" if ":" in ns else "-4"
        subprocess.run(["ip", family, "addr", "add", ns + ("/128" if ":" in ns else "/32"), "dev", "lo"], check=False)
        loggers.append(subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "--layer", "dns", "--address", ns, "--work", str(work)]))
    time.sleep(0.5)
    # The logger must see a lookup made through the normal resolver path, or an
    # empty DNS log would prove nothing.
    try:
        socket.getaddrinfo(DNS_PROBE, 443)
    except OSError:
        pass
    # PostgreSQL refuses to run as root: the scenario runs as a non-root user of
    # a nested namespace (the original uid, mapped from this namespace's root).
    me = [sys.executable, str(Path(__file__).resolve())]
    inner = ["unshare", "--user", f"--map-user={args.map_uid}", f"--map-group={args.map_gid}",
             "strace", "-f", "-qq", "-e", "trace=connect", "-o", str(work / "strace.txt"),
             *me, "--layer", "inner", "--work", str(work), "--pack", args.pack, "--postgres-bin-dir", args.postgres_bin_dir]
    code = subprocess.run(inner).returncode
    for p in loggers:
        p.send_signal(signal.SIGTERM)
        p.wait(timeout=5)
    return code


def layer_dns(args) -> int:
    """Answer NXDOMAIN to every DNS query and record the name."""
    out = Path(args.work) / "dns.log"
    family = socket.AF_INET6 if ":" in args.address else socket.AF_INET

    def name_of(q: bytes) -> tuple[str, int, int]:
        labels, i = [], 12
        while i < len(q) and q[i]:
            labels.append(q[i + 1:i + 1 + q[i]].decode(errors="replace"))
            i += 1 + q[i]
        qtype = struct.unpack(">H", q[i + 1:i + 3])[0] if i + 3 <= len(q) else 0
        return ".".join(labels), qtype, i + 5

    def answer(q: bytes) -> bytes:
        name, qtype, end = name_of(q)
        with out.open("a") as f:
            f.write(f"{name} {qtype}\n")
        return q[:2] + struct.pack(">HHHHH", 0x8183, 1, 0, 0, 0) + q[12:end]

    udp = socket.socket(family, socket.SOCK_DGRAM)
    udp.bind((args.address, 53))
    tcp = socket.socket(family, socket.SOCK_STREAM)
    tcp.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    tcp.bind((args.address, 53))
    tcp.listen()

    def serve_tcp():
        while True:
            c, _ = tcp.accept()
            try:
                n = struct.unpack(">H", c.recv(2))[0]
                a = answer(c.recv(n))
                c.sendall(struct.pack(">H", len(a)) + a)
            except Exception:
                pass
            c.close()

    threading.Thread(target=serve_tcp, daemon=True).start()
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
    while True:
        q, peer = udp.recvfrom(4096)
        try:
            udp.sendto(answer(q), peer)
        except Exception:
            pass


def audit(work: Path) -> bool:
    import re
    connects: dict[str, set[str]] = {}
    pattern = re.compile(r'connect\(\d+, \{sa_family=(AF_INET6?), sin6?_port=htons\((\d+)\), (?:sin6_flowinfo=[^,]*, )?'
                         r'(?:inet_pton\(AF_INET6, "([^"]+)"|sin_addr=inet_addr\("([^"]+)"\))(?:.*?\) += (-?\d+)(?: (\w+))?)?')
    text = (work / "strace.txt").read_text(errors="replace") if (work / "strace.txt").exists() else ""
    # Every inet connect must be parsed; one that is not is reported, not dropped.
    inet = len(re.findall(r'connect\(\d+, \{sa_family=AF_INET6?,', text))
    matches = list(pattern.finditer(text))
    if inet != len(matches):
        connects["unparsed connect lines"] = {str(inet - len(matches))}
    for m in matches:
        family, port, ip6, ip4, ret, err = m.groups()
        addr = ip6 or ip4
        dest = f"[{addr}]:{port}" if ip6 else f"{addr}:{port}"
        # A call strace split around another thread's line has no result here.
        outcome = "connected" if ret == "0" else (err or ret or "result on a later line")
        connects.setdefault(dest, set()).add(outcome)
    loopback = {d: o for d, o in connects.items() if d.startswith("127.") or d.startswith("[::1]")}
    outside = {d: o for d, o in connects.items() if d not in loopback}
    asked = set((work / "dns.log").read_text().split("\n")) - {""} if (work / "dns.log").exists() else set()
    probe = {q for q in asked if q.startswith(DNS_PROBE)}
    dns = sorted(asked - probe)
    report = {"dns_logger_answered_probe": bool(probe), "loopback_destinations": sorted(loopback), "non_loopback_destinations": {d: sorted(o) for d, o in sorted(outside.items())},
              "dns_names_asked": dns}
    (work / "audit.json").write_text(json.dumps(report, indent=2))
    log("\n== outbound audit (strace connect + DNS log, whole process tree)")
    log(f"  DNS logger self-check ({DNS_PROBE}): {'logged' if probe else 'NOT logged - the DNS audit is not valid'}")
    log(f"  loopback destinations: {len(loopback)} ({', '.join(sorted(loopback)[:12])}{' ...' if len(loopback) > 12 else ''})")
    for d, o in sorted(outside.items()):
        log(f"  non-loopback connect: {d} -> {', '.join(sorted(o))}")
    for name in dns:
        log(f"  DNS query: {name}")
    clean = not outside and not dns and bool(probe)
    if clean:
        log("  no connection or DNS query left the namespace's loopback")
    return clean


# --- inner: the scenario ---------------------------------------------------------------

class Mirror:
    """One HTTPS server: /go (GOPROXY layout), /pypi (simple index and files),
    /npm (registry documents and tarballs), /git (git http-backend). Every
    request path is recorded."""

    def __init__(self, root: Path, cert: Path, key: Path):
        self.root, self.requests = root, []
        mirror = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *a):
                return

            def do_GET(self):
                self.handle_any()

            def do_POST(self):
                self.handle_any()

            def do_HEAD(self):
                self.handle_any()

            def handle_any(self):
                mirror.requests.append(f"{self.command} {self.path}")
                path = self.path.split("?", 1)[0]
                if path.startswith("/git/"):
                    return self.git(path)
                target = (mirror.root / path.lstrip("/")).resolve()
                if mirror.root not in target.parents and target != mirror.root:
                    return self.send_error(404)
                ctype = "application/octet-stream"
                if target.is_dir():
                    for index, kind in (("index.json", "application/json"), ("index.html", "text/html")):
                        if (target / index).is_file():
                            target, ctype = target / index, kind
                            break
                if not target.is_file():
                    return self.send_error(404)
                data = target.read_bytes()
                self.send_response(200)
                self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                if self.command != "HEAD":
                    self.wfile.write(data)

            def git(self, path):
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0)) if self.command == "POST" else b""
                env = {"PATH": os.environ["PATH"], "GIT_PROJECT_ROOT": str(mirror.root / "git"), "GIT_HTTP_EXPORT_ALL": "1",
                       "PATH_INFO": path[len("/git"):], "QUERY_STRING": self.path.split("?", 1)[1] if "?" in self.path else "",
                       "REQUEST_METHOD": self.command, "CONTENT_TYPE": self.headers.get("Content-Type", ""), "CONTENT_LENGTH": str(len(body)),
                       "GIT_CONFIG_NOSYSTEM": "1", "HOME": str(mirror.root)}
                out = subprocess.run(["git", "http-backend"], input=body, env=env, capture_output=True).stdout
                head, _, payload = out.partition(b"\r\n\r\n")
                status = 200
                headers = []
                for line in head.split(b"\r\n"):
                    k, _, v = line.decode().partition(":")
                    if k.lower() == "status":
                        status = int(v.split()[0])
                    elif k:
                        headers.append((k, v.strip()))
                self.send_response(status)
                for k, v in headers:
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

        class Server(ThreadingHTTPServer):
            def handle_error(self, request, client_address):
                # A client that refuses the certificate (the "without the CA"
                # checks) resets the connection; that is the expected outcome.
                return

        self.server = Server(("127.0.0.1", 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        self.port = self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def url(self, path: str) -> str:
        return f"https://localhost:{self.port}/{path.lstrip('/')}"


def sh(argv, env=None, cwd=None, check=True, timeout=600):
    r = subprocess.run([str(a) for a in argv], env=env, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    if check and r.returncode != 0:
        raise RuntimeError(f"{' '.join(map(str, argv[:3]))} exited {r.returncode}: {(r.stdout + r.stderr)[-3000:]}")
    return r


def make_ca(tls: Path) -> None:
    tls.mkdir(mode=0o700)
    sh(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-days", "2", "-subj", "/CN=CODEYE smoke internal CA",
        "-keyout", tls / "ca.key", "-out", tls / "ca.pem", "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign"])
    sh(["openssl", "req", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-subj", "/CN=localhost", "-keyout", tls / "server.key", "-out", tls / "server.csr"])
    (tls / "ext.cnf").write_text("subjectAltName=DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n")
    sh(["openssl", "x509", "-req", "-in", tls / "server.csr", "-CA", tls / "ca.pem", "-CAkey", tls / "ca.key", "-CAcreateserial", "-days", "2",
        "-extfile", tls / "ext.cnf", "-out", tls / "server.pem"])


def copy_module(cache: Path, dest: Path, module: str, version: str) -> None:
    src = cache / "cache/download" / module / "@v"
    out = dest / module / "@v"
    out.mkdir(parents=True, exist_ok=True)
    for ext in (".info", ".mod", ".zip"):
        shutil.copyfile(src / (version + ext), out / (version + ext))
    (out / "list").write_text(version + "\n")


def build_wheel(dest: Path) -> tuple[str, str]:
    name = "offlinedemo-1.0-py3-none-any.whl"
    files = {"offlinedemo/__init__.py": b"def answer() -> int:\n    return 42\n", "offlinedemo/py.typed": b"",
             "offlinedemo-1.0.dist-info/METADATA": b"Metadata-Version: 2.1\nName: offlinedemo\nVersion: 1.0\n",
             "offlinedemo-1.0.dist-info/WHEEL": b"Wheel-Version: 1.0\nGenerator: offline_smoke\nRoot-Is-Purelib: true\nTag: py3-none-any\n"}
    record = []
    for p, b in files.items():
        digest = base64.urlsafe_b64encode(hashlib.sha256(b).digest()).rstrip(b"=").decode()
        record.append(f"{p},sha256={digest},{len(b)}")
    record.append("offlinedemo-1.0.dist-info/RECORD,,")
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as z:
        for p, b in files.items():
            z.writestr(p, b)
        z.writestr("offlinedemo-1.0.dist-info/RECORD", "\n".join(record) + "\n")
    data = buf.getvalue()
    (dest / "files").mkdir(parents=True, exist_ok=True)
    (dest / "files" / name).write_bytes(data)
    sha = hashlib.sha256(data).hexdigest()
    (dest / "simple/offlinedemo").mkdir(parents=True, exist_ok=True)
    (dest / "simple/offlinedemo/index.html").write_text(f'<!DOCTYPE html><html><body><a href="../../files/{name}#sha256={sha}">{name}</a></body></html>\n')
    (dest / "simple/index.html").write_text('<!DOCTYPE html><html><body><a href="offlinedemo/">offlinedemo</a></body></html>\n')
    return name, sha


def build_npm(dest: Path, base: str) -> None:
    pkg = {"name": "offline-demo", "version": "1.0.0", "main": "index.js", "types": "index.d.ts"}
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tar:
        for name, body in (("package/package.json", json.dumps(pkg).encode()), ("package/index.js", b"exports.answer = () => 42;\n"),
                           ("package/index.d.ts", b"export declare function answer(): number;\n")):
            info = tarfile.TarInfo(name)
            info.size, info.mode, info.mtime = len(body), 0o644, 0
            tar.addfile(info, io.BytesIO(body))
    data = buf.getvalue()
    (dest / "offline-demo/-").mkdir(parents=True, exist_ok=True)
    (dest / "offline-demo/-/offline-demo-1.0.0.tgz").write_bytes(data)
    integrity = "sha512-" + base64.b64encode(hashlib.sha512(data).digest()).decode()
    doc = {"name": "offline-demo", "dist-tags": {"latest": "1.0.0"}, "versions": {"1.0.0": dict(pkg, dist={
        "tarball": base + "offline-demo/-/offline-demo-1.0.0.tgz", "shasum": hashlib.sha1(data).hexdigest(), "integrity": integrity})}}
    (dest / "offline-demo/index.json").write_text(json.dumps(doc))


def build_git(dest: Path, env: dict) -> None:
    work = dest.parent / "git-src"
    work.mkdir()
    (work / "go.mod").write_text("module example.com/gitdemo\n\ngo 1.22\n")
    (work / "demo.go").write_text("package gitdemo\n\n// Double returns twice n.\nfunc Double(n int) int { return n * 2 }\n")
    genv = dict(env, GIT_AUTHOR_NAME="smoke", GIT_AUTHOR_EMAIL="smoke@example.invalid", GIT_COMMITTER_NAME="smoke", GIT_COMMITTER_EMAIL="smoke@example.invalid")
    sh(["git", "init", "-q", "-b", "main"], env=genv, cwd=work)
    sh(["git", "add", "."], env=genv, cwd=work)
    sh(["git", "commit", "-q", "-m", "demo"], env=genv, cwd=work)
    dest.mkdir(parents=True)
    sh(["git", "clone", "-q", "--bare", work, dest / "demo.git"], env=genv)


def inner(args) -> int:
    from personal_smoke import Session, free_port, wait_for  # noqa: PLC0415
    work = Path(args.work)
    tools = json.loads((work / "tools.json").read_text())
    summary: dict = {"steps": []}

    def step(name, **facts):
        summary["steps"].append(dict(step=name, **facts))
        log(f"  {name}: " + ", ".join(f"{k}={v}" for k, v in facts.items()))

    # 1. Unpack and verify with coreutils and the pack script.
    install = work / "install"
    install.mkdir()
    with tarfile.open(args.pack) as tar:
        tar.extractall(install, filter="data")
    top = next(install.iterdir())
    sums = sh(["sha256sum", "-c", "--quiet", "SHA256SUMS"], cwd=top)
    verified = sh([sys.executable, HERE / "offline_pack.py", "verify", args.pack]).stdout.strip()
    exe = top / "codeye"
    version = sh([exe, "version", "--json"]).stdout
    manifest = json.loads((top / "MANIFEST.json").read_text())
    assert json.loads(version)["commit"] == manifest["commit"], "binary and MANIFEST disagree"
    step("pack verified", sha256sum=("OK" if sums.returncode == 0 else "FAIL"), verify=verified.split(":")[0], version=manifest["version"], files=len(manifest["files"]))

    # 2. A fresh home: nothing of the real user's configuration is read.
    home = work / "home"
    home.mkdir()
    godir = str(Path(tools["go"]).parent)
    env = {"PATH": f"{godir}:/usr/bin:/bin", "HOME": str(home), "XDG_CONFIG_HOME": str(home / ".config"), "XDG_STATE_HOME": str(home / ".local/state"),
           "XDG_DATA_HOME": str(home / ".local/share"), "LANG": "C.UTF-8"}
    # The fixtures: an operator module cache with one module, a mirror with both.
    mirror_root = work / "mirror"
    real_cache = Path(tools["gomodcache"])
    seed = work / "operator-go-mod"
    for mod, ver in (GO_SEEDED,):
        copy_module(real_cache, seed / "cache/download", mod, ver)
    for mod, ver in (GO_SEEDED, GO_MIRRORED):
        copy_module(real_cache, mirror_root / "go", mod, ver)
    src = work / "src"
    projects = {"seeded": (GO_SEEDED, 'import "github.com/google/uuid"\n\n// ID returns a new identifier.\nfunc ID() string { return uuid.New().String() }\n'),
                "missing": (GO_MIRRORED, 'import "github.com/spf13/pflag"\n\n// Flags returns an empty flag set.\nfunc Flags() *pflag.FlagSet { return pflag.NewFlagSet("x", pflag.ContinueOnError) }\n')}
    tidy_env = dict(env, GOPROXY="file://" + str(mirror_root / "go"), GOSUMDB="off", GOFLAGS="-mod=mod", GOMODCACHE=str(work / "tidy-cache"),
                    GOTOOLCHAIN="local", GOTELEMETRY="off")
    for name, ((mod, ver), body) in projects.items():
        d = src / name
        d.mkdir(parents=True)
        (d / "go.mod").write_text(f"module example.com/{name}\n\ngo 1.22\n\nrequire {mod} {ver}\n")
        (d / f"{name}.go").write_text(f"package {name}\n\n{body}")
        sh([tools["go"], "mod", "tidy"], env=tidy_env, cwd=d)
    sh(["chmod", "-R", "u+w", work / "tidy-cache"])
    shutil.rmtree(work / "tidy-cache")

    port = free_port()
    state = work / "state"
    config = home / ".config/codeye/personal.json"
    config.parent.mkdir(parents=True)
    doc = {"schema_version": "0.1", "state_dir": str(state), "listen": f"127.0.0.1:{port}", "database": {"mode": "managed"},
           "source_roots": [{"label": "src", "path": str(src)}],
           "agent": {"max_workers": 1, "go": tools["go"], "git": "/usr/bin/git", "go_mod_cache": str(seed),
                     "tools": {k: tools[k] for k in ("staticcheck",) if tools.get(k)}, "model": {"opencode": tools["opencode"]}}}
    config.write_text(json.dumps(doc, indent=2))
    config.chmod(0o600)
    origin = f"http://127.0.0.1:{port}"
    # A key file without base_url and a key in the environment: offline uses neither.
    (state / "secrets").mkdir(parents=True, mode=0o700)
    secret = state / "secrets/model.json"
    secret.write_text(json.dumps({"schema_version": "0.1", "provider": "openai", "api_key": "sk-public-endpoint-key"}) + "\n")
    secret.chmod(0o600)
    before = secret.read_bytes()

    def up(target, extra=(), run_env=None):
        r = sh([exe, "up", "--config", config, "--no-open", "--postgres-bin-dir", args.postgres_bin_dir, *extra, target], env=run_env or env, timeout=900)
        link = next(line.strip() for line in r.stdout.splitlines() if "#launch=" in line)
        run_id = link.rsplit("&run=", 1)[1].split("&", 1)[0]
        return r.stdout, Session(state, origin, link), run_id

    def finished(session, run_id, timeout=900):
        def probe():
            view = session.request("GET", f"runs/{run_id}")
            return view if view["status"] in TERMINAL and (view.get("report") or view["status"] != "partial") else None
        view = wait_for(f"run {run_id}", probe, timeout)
        report = session.request("GET", f"runs/{run_id}/report?revision={view['report']['revision']}") if view.get("report") else None
        return view, report

    def deps(report, key):
        return next((d for d in report["environment"]["dependencies"] if d["task_key"] == key), None)

    def why(d):
        return d.get("task_reason") or d.get("reason")

    def tool_status(report, analyzer):
        for t in report["coverage"]["tools"]:
            if t["analyzer"] == analyzer:
                return t.get("exit_class")
        return None

    try:
        out, session, run_id = up(src / "seeded", ["--offline"], dict(env, OPENAI_API_KEY="sk-env-key-not-stored"))
        assert 'saved "offline": true' in out, out
        assert "has no base_url" in out, out
        assert secret.read_bytes() == before, "the environment key replaced the key file"
        saved = json.loads(config.read_text())
        assert saved["offline"] is True and "git_hosts" not in saved
        view, report = finished(session, run_id)
        d = deps(report, "deps.go")
        assert d and d["task_status"] == "succeeded" and d["state"] == "prepared", d
        ai = report["coverage"]["ai_review"]
        step("offline, operator cache", run=view["status"], deps_go=f"{d['task_status']}/{d['state']}", cache=d["cache_kind"],
             go_vet=tool_status(report, "go.vet") or "-", ai_review=ai["status"], model_message="key file without base_url refused, environment key not stored")

        out, session, run_id = up(src / "missing")
        view, report = finished(session, run_id)
        d = deps(report, "deps.go")
        assert d and why(d) == "offline_no_registry_configured", d
        missing_module = "@".join(GO_MIRRORED)
        assert any(f["name"] == missing_module for f in d["failed_items"]), d["failed_items"]
        step("offline, module not in the cache", run=view["status"], deps_go=f"{d['task_status']}/{why(d)}", failed_items=[f["reason"] for f in d["failed_items"]])
        doctor1 = sh([exe, "doctor", "--config", config, "--format", "json", "--work-dir", work / "doctor-work"], env=env, check=False).stdout
        (work / "doctor-offline.json").write_text(doctor1)
        # Offline with no mirror: no public default is left as a destination.
        dests = json.loads(doctor1)["offline"]["destinations"]
        assert dests and all(d["origin"] == "none" and d.get("reason") for d in dests), dests
        step("doctor, offline without mirrors", destinations={d["kind"]: d["reason"] for d in dests})
        sh([exe, "stop", "--config", config], env=env, timeout=300)

        # 3. The internal mirror and endpoint behind a private CA.
        tls = work / "tls"
        make_ca(tls)
        build_wheel(mirror_root / "pypi")
        mirror = Mirror(mirror_root, tls / "server.pem", tls / "server.key")
        build_npm(mirror_root / "npm", mirror.url("npm/"))
        build_git(mirror_root / "git", env)
        ready = work / "upstream.json"
        upstream = subprocess.Popen([sys.executable, HERE / "fake_model_upstream.py", "--ready-file", ready, "--tls-cert", tls / "server.pem", "--tls-key", tls / "server.key"],
                                    stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
        wait_for("model upstream", lambda: ready.exists() and ready.read_text().strip(), 30)
        upstream_port = json.loads(ready.read_text())["port"]
        endpoint = f"https://localhost:{upstream_port}/v1"
        secret.write_text(json.dumps({"schema_version": "0.1", "provider": "openai", "api_key": "sk-internal-endpoint-key", "base_url": endpoint}) + "\n")
        secret.chmod(0o600)

        # 4. Each tool with and without the CA bundle.
        ca_results = tool_ca_checks(work, tools, mirror, endpoint, tls / "ca.pem", env)
        summary["ca_checks"] = ca_results

        # Python and JS/TS projects that need the mirror; a project from the Git host.
        py = src / "pyproj"
        py.mkdir()
        (py / "pyproject.toml").write_text('[project]\nname = "pyproj"\nversion = "0.1"\ndependencies = ["offlinedemo==1.0"]\n')
        (py / "app.py").write_text("from offlinedemo import answer\n\n\ndef twice() -> int:\n    return answer() * 2\n")
        js = src / "jsproj"
        js.mkdir()
        (js / "package.json").write_text(json.dumps({"name": "jsproj", "version": "0.1.0", "dependencies": {"offline-demo": "1.0.0"}}))
        (js / "tsconfig.json").write_text(json.dumps({"compilerOptions": {"strict": True, "noEmit": True, "module": "commonjs", "target": "es2022"}}))
        (js / "app.ts").write_text('import { answer } from "offline-demo";\n\nexport const twice = (): number => answer() * 2;\n')
        shutil.copyfile(work / "ca-checks/npm-ca/package-lock.json", js / "package-lock.json")

        doc = json.loads(config.read_text())
        doc["ca_bundle"] = str(tls / "ca.pem")
        doc["git_hosts"] = [f"localhost:{mirror.port}"]
        doc["agent"]["dependencies"] = {"go_proxy": mirror.url("go"), "pip_index_url": mirror.url("pypi/simple"), "npm_registry": mirror.url("npm/")}
        for name in ("uv", "mypy", "node", "npm", "tsc"):
            if tools.get(name):
                doc["agent"]["tools"][name] = tools[name]
        config.write_text(json.dumps(doc, indent=2))
        mirror.requests.clear()

        out, session, run_id = up(src / "missing")
        assert "model: openai/" in out, out
        view, report = finished(session, run_id)
        d = deps(report, "deps.go")
        assert d and d["task_status"] == "succeeded", d
        ai = report["coverage"]["ai_review"]
        assert ai["status"] == "performed", ai
        go_requests = [r for r in mirror.requests if "/go/" in r]
        step("mirror + CA: Go", run=view["status"], deps_go=f"{d['task_status']}/{d['state']}", mirror_requests=len(go_requests),
             ai_review=ai["status"])

        results = {}
        for name, key in (("pyproj", "deps.python"), ("jsproj", "deps.javascript")):
            out, session, run_id = up(src / name)
            view, report = finished(session, run_id)
            d = deps(report, key)
            results[name] = d
            assert d and d["task_status"] == "succeeded", (name, d)
            step(f"mirror + CA: {name}", run=view["status"], deps=f"{d['task_status']}/{d['state'] or why(d)}" if d else "not planned",
                 manager=(d or {}).get("manager"), ai_review=report["coverage"]["ai_review"]["status"])
        gitout, session, run_id = up(mirror.url("git/demo.git"))
        view, report = finished(session, run_id)
        assert "registered project" in gitout or "#launch=" in gitout, gitout
        step("mirror + CA: Git project", run=view["status"], ai_review=report["coverage"]["ai_review"]["status"], git_requests=len([r for r in mirror.requests if "/git/" in r]))
        summary["mirror_requests"] = sorted(set(r.split("?")[0] for r in mirror.requests))

        # 5. The web app from the archive, doctor and the tools checklist.
        import urllib.request  # noqa: PLC0415
        with urllib.request.urlopen(origin + "/", timeout=10) as r:
            served = r.read()
        assert served == (top / "web/dist/index.html").read_bytes(), "the web app is not the archive's"
        doctor2 = sh([exe, "doctor", "--config", config, "--format", "json", "--work-dir", work / "doctor-work"], env=env, check=False).stdout
        (work / "doctor-mirror.json").write_text(doctor2)
        try:
            import jsonschema  # noqa: PLC0415
            schema = json.loads((ROOT / "schemas/doctor.schema.json").read_text())
            for raw in (doctor1, doctor2):
                jsonschema.Draft202012Validator(schema).validate(json.loads(raw))
            schema_ok = "valid"
        except ImportError:
            schema_ok = "jsonschema not installed"
        off = json.loads(doctor2)["offline"]
        step("doctor", schema=schema_ok, destinations={d["kind"]: d["origin"] for d in off["destinations"]}, ca_bundle=off["ca_bundle"]["status"])
        checklist = sh([exe, "tools", "--config", config, "--checklist"], env=env).stdout
        (work / "checklist.md").write_text(checklist)
        step("web app and checklist", web="served from the archive", checklist_last=checklist.strip().splitlines()[-1])
        summary["upstream_calls"] = None
    finally:
        sh([exe, "stop", "--config", config], env=env, check=False, timeout=300)
        if "upstream" in locals():
            upstream.send_signal(signal.SIGTERM)
            try:
                _, err = upstream.communicate(timeout=10)
                calls = [json.loads(line)["calls"] for line in err.splitlines() if line.startswith("{")]
                summary["upstream_calls"] = calls[-1] if calls else None
            except Exception:
                upstream.kill()
        (work / "summary.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False))
    log(f"  model upstream calls over TLS: {summary['upstream_calls']}")
    log("PASS: offline pack from the archive with no network")
    return 0


def tool_ca_checks(work: Path, tools: dict, mirror: Mirror, endpoint: str, bundle: Path, env: dict) -> dict:
    """Every tool against the mirror with CODEYE's CA variables and without."""
    from personal_smoke import free_port  # noqa: F401,PLC0415
    base = work / "ca-checks"
    base.mkdir()
    ca = {"SSL_CERT_FILE": str(bundle), "NODE_EXTRA_CA_CERTS": str(bundle), "GIT_SSL_CAINFO": str(bundle), "PIP_CERT": str(bundle), "CARGO_HTTP_CAINFO": str(bundle)}
    results = {}

    def attempt(name, argv, extra, cwd, ok=lambda r: r.returncode == 0):
        out = {}
        for label, vars_ in (("ca", ca), ("none", {})):
            d = base / f"{name}-{label}"
            d.mkdir()
            prep = cwd(d) if callable(cwd) else None
            r = sh(argv(d), env=dict(env, HOME=str(d), **extra, **vars_), cwd=prep or d, check=False, timeout=300)
            out[label] = "ok" if ok(r) else "failed: " + tls_reason(r.stdout + r.stderr)
        results[name] = out
        log(f"  CA check {name}: with bundle {out['ca']}; without {out['none']}")

    attempt("go", lambda d: [tools["go"], "mod", "download", "-json", f"{GO_MIRRORED[0]}@{GO_MIRRORED[1]}"],
            {"GOPROXY": mirror.url("go"), "GOSUMDB": "off", "GOFLAGS": "-mod=mod", "GOTOOLCHAIN": "local", "GOTELEMETRY": "off"},
            lambda d: (d / "gocache").mkdir() or None,
            ok=lambda r: r.returncode == 0)
    attempt("git", lambda d: ["git", "ls-remote", mirror.url("git/demo.git")], {"GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0"}, None)

    def npm_project(d):
        (d / "package.json").write_text(json.dumps({"name": "p", "version": "0.0.0", "dependencies": {"offline-demo": "1.0.0"}}))
        return None
    if tools.get("npm"):
        attempt("npm", lambda d: [tools["node"], tools["npm"], "install", "--registry", mirror.url("npm/"), "--cache", d / "cache", "--no-audit", "--no-fund",
                                  "--no-update-notifier", "--fetch-retries=0"], {}, npm_project)
    if tools.get("pnpm"):
        attempt("pnpm", lambda d: [tools["node"], tools["pnpm"], "install", "--config.registry=" + mirror.url("npm/"), "--store-dir", d / "store",
                                   "--config.fetch-retries=0", "--config.update-notifier=false", "--reporter=append-only"], {"CI": "true"}, npm_project)
    if tools.get("yarn"):
        attempt("yarn", lambda d: [tools["node"], tools["yarn"], "install", "--registry", mirror.url("npm/"), "--cache-folder", d / "cache",
                                   "--non-interactive", "--network-timeout", "5000"], {"CI": "true"}, npm_project)
    attempt("pip", lambda d: ["python3", "-m", "pip", "download", "--no-deps", "--disable-pip-version-check", "--retries", "0", "--timeout", "5",
                              "--index-url", mirror.url("pypi/simple"), "-d", d / "dl", "offlinedemo==1.0"], {}, None)
    if tools.get("uv"):
        attempt("uv", lambda d: [tools["uv"], "pip", "install", "--no-config", "--target", d / "t", "--python", "python3", "--index-url", mirror.url("pypi/simple"),
                                 "--cache-dir", d / "cache", "offlinedemo==1.0"], {"UV_PYTHON_DOWNLOADS": "never"}, None)
    if Path(tools["opencode"]).exists():
        cfg = {"enabled_providers": ["openai"], "model": "openai/gpt-6.2-sol", "small_model": "openai/gpt-6.2-sol", "permission": "deny", "tools": {"*": False},
               "share": "disabled", "snapshot": False, "autoupdate": False, "lsp": False, "formatter": False, "plugin": [],
               "provider": {"openai": {"options": {"apiKey": "sk-ca-check", "baseURL": endpoint}, "models": {"gpt-6.2-sol": {"name": "gpt-6.2-sol"}}}}}
        oc_env = {"OPENCODE_CONFIG_CONTENT": json.dumps(cfg), "OPENCODE_DISABLE_AUTOUPDATE": "1", "OPENCODE_DISABLE_MODELS_FETCH": "1",
                  "OPENCODE_DISABLE_DEFAULT_PLUGINS": "1", "OPENCODE_DISABLE_PROJECT_CONFIG": "1", "npm_config_registry": "http://127.0.0.1:9/"}
        attempt("opencode", lambda d: [tools["opencode"], "run", "--pure", "--format", "json", "--model", "openai/gpt-6.2-sol", "--dir", d, "say hi"], oc_env, None,
                ok=lambda r: r.returncode == 0 and '"step_finish"' in r.stdout)
        # Which of CODEYE's variables OpenCode reads.
        for var in ("NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE"):
            d = base / f"opencode-only-{var}"
            d.mkdir()
            r = sh([tools["opencode"], "run", "--pure", "--format", "json", "--model", "openai/gpt-6.2-sol", "--dir", d, "say hi"],
                   env=dict(env, HOME=str(d), **oc_env, **{var: str(bundle)}), check=False, timeout=300)
            results[f"opencode only {var}"] = "ok" if r.returncode == 0 and '"step_finish"' in r.stdout else "failed: " + tls_reason(r.stdout + r.stderr)
            log(f"  CA check opencode with only {var}: {results[f'opencode only {var}']}")
    return results


def tls_reason(text: str) -> str:
    low = text.lower()
    for marker in ("certificate", "x509", "self-signed", "self signed", "unable to get local issuer", "ssl", "tls"):
        if marker in low:
            i = low.index(marker)
            return " ".join(text[max(0, i - 60):i + 80].split())
    return " ".join(text.strip().splitlines()[-1:])[:160] if text.strip() else "no output"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--pack", default="")
    parser.add_argument("--postgres-bin-dir", default="")
    parser.add_argument("--work", default=str(ROOT / ".codeye" / "offline-smoke"))
    parser.add_argument("--opencode", default="")
    parser.add_argument("--layer", choices=("outer", "net", "dns", "inner"), default="outer")
    parser.add_argument("--address", default="")
    parser.add_argument("--map-uid", default=str(os.getuid()))
    parser.add_argument("--map-gid", default=str(os.getgid()))
    args = parser.parse_args()
    if sys.platform != "linux":
        raise SystemExit("Linux only")
    if args.layer != "dns" and not (args.pack and args.postgres_bin_dir):
        parser.error("--pack and --postgres-bin-dir are required")
    return {"outer": outer, "net": layer_net, "dns": layer_dns, "inner": inner}[args.layer](args)


if __name__ == "__main__":
    sys.exit(main())
