"""A loopback fake of the parts of the GitHub REST API CODEYE uses (stdlib only).

Used by scripts/personal_smoke.py with a fixture build (CODEYE_TEST_GITHUB_API) so a pull-request
run and its result comment run end to end without the network. It serves GET /user, GET
/repos/O/R/pulls/N, GET /repos/O/R/pulls/N/files, GET and POST /repos/O/R/issues/N/comments and
PATCH /repos/O/R/issues/comments/ID. Every request must carry "Authorization: Bearer <token>"
of a known token (add_token gives a second user one), else 401; with allow_anonymous a request
without the header reads public pull requests. A private pull request is visible to its readers
only (404 to anyone else). It records every request, with the login of its token, so a test can
assert that a dry run wrote nothing and whose token each call carried.
"""
from __future__ import annotations

import json
import re
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class FakeGitHub:
    def __init__(self, token: str, login: str, *, allow_anonymous: bool = False):
        self.token, self.login = token, login
        self.logins: dict[str, str] = {token: login}
        self.allow_anonymous = allow_anonymous
        # (method, path, login of the token or None for none or an unknown one)
        self.calls: list[tuple[str, str, str | None]] = []
        self.pulls: dict[tuple[str, int], dict] = {}
        self.comments: dict[tuple[str, int], list[dict]] = {}
        self.requests: list[tuple[str, str, bool]] = []
        self.next_id = 5000
        self.lock = threading.Lock()
        fake = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):  # quiet
                pass

            def do_GET(self):
                fake.serve(self, "GET")

            def do_POST(self):
                fake.serve(self, "POST")

            def do_PATCH(self):
                fake.serve(self, "PATCH")

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_address[1]}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self):
        self.server.shutdown()

    def add_token(self, token: str, login: str):
        with self.lock:
            self.logins[token] = login

    def add_pull(self, repo: str, number: int, *, base_sha: str, head_sha: str, files: list[dict], head_repo: str | None = None, title: str = "Change",
                 readers: set[str] | None = None):
        """readers: the logins that see a private pull request (None: public)."""
        with self.lock:
            self.pulls[(repo.lower(), number)] = {"number": number, "state": "open", "title": title, "draft": False, "changed_files": len(files),
                                                  "base": {"ref": "main", "sha": base_sha, "repo": {"full_name": repo}},
                                                  "head": {"ref": "feature", "sha": head_sha, "repo": {"full_name": head_repo or repo}}, "files": files,
                                                  "readers": None if readers is None else set(readers)}

    def logins_of(self, path_prefix: str) -> list[str | None]:
        """The token logins of the recorded calls whose path starts with path_prefix."""
        with self.lock:
            return [login for _, path, login in self.calls if path.startswith(path_prefix)]

    def set_head(self, repo: str, number: int, head_sha: str, files: list[dict] | None = None):
        with self.lock:
            p = self.pulls[(repo.lower(), number)]
            p["head"]["sha"] = head_sha
            if files is not None:
                p["files"], p["changed_files"] = files, len(files)

    def writes(self) -> int:
        with self.lock:
            return sum(1 for m, _, _ in self.requests if m in ("POST", "PATCH"))

    def comment_list(self, repo: str, number: int) -> list[dict]:
        with self.lock:
            return [dict(c) for c in self.comments.get((repo.lower(), number), [])]

    def serve(self, h: BaseHTTPRequestHandler, method: str):
        header = h.headers.get("Authorization")
        login = self.logins.get(header[len("Bearer "):]) if header and header.startswith("Bearer ") else None
        authorized = login is not None
        with self.lock:
            self.requests.append((method, h.path, authorized))
            self.calls.append((method, h.path, login))
        if not authorized and (header is not None or not self.allow_anonymous or method != "GET"):
            return self.reply(h, 401, {"message": "Bad credentials" if header else "Requires authentication"})
        path = h.path.split("?", 1)[0]
        body = None
        if method in ("POST", "PATCH"):
            body = json.loads(h.rfile.read(int(h.headers.get("Content-Length") or 0)) or b"{}")
        if method == "GET" and path == "/user":
            if login is None:
                return self.reply(h, 401, {"message": "Requires authentication"})
            return self.reply(h, 200, {"login": login, "id": 1 + sorted(self.logins.values()).index(login)})
        m = re.fullmatch(r"/repos/([^/]+/[^/]+)/pulls/(\d+)(/files)?", path)
        if m and method == "GET":
            with self.lock:
                p = self.pulls.get((m[1].lower(), int(m[2])))
                if p is None or (p["readers"] is not None and login not in p["readers"]):
                    return self.reply(h, 404, {"message": "Not Found"})
                if m[3]:
                    return self.reply(h, 200, p["files"])
                return self.reply(h, 200, {k: v for k, v in p.items() if k not in ("files", "readers")})
        m = re.fullmatch(r"/repos/([^/]+/[^/]+)/issues/(\d+)/comments", path)
        if m:
            key = (m[1].lower(), int(m[2]))
            with self.lock:
                p = self.pulls.get(key)
                if p is None or (p["readers"] is not None and login not in p["readers"]):
                    return self.reply(h, 404, {"message": "Not Found"})
                if method == "GET":
                    return self.reply(h, 200, [self.view(m[1], c) for c in self.comments.get(key, [])])
                if method == "POST":
                    self.next_id += 1
                    c = {"id": self.next_id, "body": body["body"], "author": login}
                    self.comments.setdefault(key, []).append(c)
                    return self.reply(h, 201, self.view(m[1], c))
        m = re.fullmatch(r"/repos/([^/]+/[^/]+)/issues/comments/(\d+)", path)
        if m and method == "PATCH":
            with self.lock:
                for (repo, _), cs in self.comments.items():
                    for c in cs:
                        if repo == m[1].lower() and c["id"] == int(m[2]):
                            if c["author"] != login:
                                return self.reply(h, 403, {"message": "Resource not accessible"})
                            c["body"] = body["body"]
                            return self.reply(h, 200, self.view(m[1], c))
            return self.reply(h, 404, {"message": "Not Found"})
        return self.reply(h, 404, {"message": "Not Found"})

    def view(self, repo: str, c: dict) -> dict:
        return {"id": c["id"], "body": c["body"], "html_url": f"https://github.com/{repo}/pull/0#issuecomment-{c['id']}", "user": {"login": c["author"]}}

    @staticmethod
    def reply(h: BaseHTTPRequestHandler, status: int, doc):
        raw = json.dumps(doc).encode()
        h.send_response(status)
        h.send_header("Content-Type", "application/json")
        h.send_header("Content-Length", str(len(raw)))
        h.end_headers()
        h.wfile.write(raw)
