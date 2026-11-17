"""설치된 OpenCode CLI와 로컬 합성 모델 응답으로 도구 차단을 검사한다."""

import http.server
import json
import pathlib
import subprocess
import shutil
import threading
import tempfile

attempts = []
source_marker = "this fixture is outside the submitted excerpts"


class Endpoint(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        msg = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        attempts.append(
            (
                len(msg.get("tools", [])),
                source_marker in json.dumps(msg),
                [m.get("role") for m in msg.get("messages", [])],
            )
        )
        if len(attempts) == 1:
            payload = {
                "role": "assistant",
                "tool_calls": [
                    {
                        "index": 0,
                        "id": "call_fixture",
                        "type": "function",
                        "function": {
                            "name": "read",
                            "arguments": json.dumps({"filePath": str(canary)}),
                        },
                    }
                ],
            }
            reason = "tool_calls"
        else:
            payload = {"role": "assistant", "content": "closed"}
            reason = "stop"
        frames = []
        for delta, finish in [(payload, None), ({}, reason)]:
            frames.append(
                "data: "
                + json.dumps(
                    {
                        "id": "chatcmpl-test",
                        "created": 1,
                        "object": "chat.completion.chunk",
                        "model": "local-fixture",
                        "choices": [
                            {"index": 0, "delta": delta, "finish_reason": finish}
                        ],
                    }
                )
                + "\n\n"
            )
        raw = ("".join(frames) + "data: [DONE]\n\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Endpoint)
threading.Thread(target=server.serve_forever, daemon=True).start()
with tempfile.TemporaryDirectory(prefix="codeye-denied-") as d:
    p = pathlib.Path(d)
    ws = p / "ws"
    ws.mkdir()
    canary = ws / "sample.txt"
    canary.write_text(source_marker)
    cfg = {
        "enabled_providers": ["fixture"],
        "provider": {
            "fixture": {
                "npm": "@ai-sdk/openai-compatible",
                "options": {
                    "apiKey": "fixture",
                    "baseURL": f"http://127.0.0.1:{server.server_port}/v1",
                },
                "models": {"local-fixture": {"name": "Fixture"}},
            }
        },
        "model": "fixture/local-fixture",
        "small_model": "fixture/local-fixture",
        "tools": {"*": False},
        "permission": "deny",
        "share": "disabled",
        "plugin": [],
        "autoupdate": False,
        "snapshot": False,
        "formatter": False,
        "lsp": False,
    }
    env = {
        "HOME": d,
        "PATH": "/usr/bin:/bin",
        "OPENCODE_CONFIG_CONTENT": json.dumps(cfg),
        "DO_NOT_TRACK": "1",
        **{
            "OPENCODE_DISABLE_" + k: "1"
            for k in [
                "AUTOUPDATE",
                "DEFAULT_PLUGINS",
                "LSP_DOWNLOAD",
                "MODELS_FETCH",
                "PROJECT_CONFIG",
                "EXTERNAL_SKILLS",
                "CLAUDE_CODE",
            ]
        },
    }
    try:
        out = subprocess.run(
            [
                shutil.which("opencode"),
                "run",
                "--pure",
                "--format",
                "json",
                "--model",
                "fixture/local-fixture",
                "--title",
                "Policy contract",
            ],
            cwd=ws,
            env=env,
            text=True,
            input="Run the provided fixture contract.",
            capture_output=True,
            timeout=25,
        )
        ev = [json.loads(x) for x in out.stdout.splitlines() if x.startswith("{")]
        print(
            "real CLI events:",
            [
                (
                    x.get("type"),
                    x.get("part", {}).get("tool"),
                    x.get("part", {}).get("state", {}).get("status"),
                )
                for x in ev
            ],
        )
        print(
            "local transport requests (published tools, fixture content sent, roles):",
            attempts,
        )
        assert any(
            e.get("type") == "tool_use"
            and e["part"].get("tool") == "read"
            and e["part"].get("state", {}).get("status") == "error"
            for e in ev
        )
        assert attempts and all(t == 0 and not sent for t, sent, _ in attempts)
        print(
            "PASS no tool advertised and requested file content was withheld by managed OpenCode"
        )
    finally:
        server.shutdown()
        server.server_close()
