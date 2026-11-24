import urllib.request

import requests
from flask import Flask, request

app = Flask(__name__)


@app.route("/proxy")
def proxy():
    target = request.args["url"]
    return requests.get(target, timeout=5).text  # expect: codeye.python.ssrf


@app.route("/hook", methods=["POST"])
def hook():
    callback = request.get_json()["callback"]
    requests.post(callback, json={"ok": True}, timeout=5)  # expect: codeye.python.ssrf
    return "sent"


@app.route("/fetch")
def fetch():
    host = request.args["host"]
    with urllib.request.urlopen("http://" + host + "/status") as resp:  # expect: codeye.python.ssrf
        return resp.read()
