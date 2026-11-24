import os
import shlex
import subprocess

from flask import Flask, request

app = Flask(__name__)


@app.route("/archive")
def archive():
    name = request.args.get("name", "")
    # The value is one argument of a fixed program; no shell parses it.
    subprocess.run(["tar", "czf", "/tmp/out.tgz", "--", name], check=True)
    return "ok"


@app.route("/quoted")
def quoted():
    name = request.args.get("name", "")
    os.system("ls -l " + shlex.quote(name))
    return "ok"


@app.route("/count")
def count():
    n = int(request.args.get("n", "1"))
    os.system("seq %d" % n)
    return "ok"


@app.route("/fixed")
def fixed():
    subprocess.run("uptime", shell=True, check=True)
    return "ok"


def operator_tool(path):
    # An operator-supplied argument, not HTTP request input.
    os.system("wc -l " + path)
