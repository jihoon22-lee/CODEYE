import os
import subprocess

from flask import Flask, request

app = Flask(__name__)


def archive_command(name):
    return "tar czf /tmp/out.tgz " + name


@app.route("/archive")
def archive():
    name = request.args.get("name", "")
    os.system(archive_command(name))  # expect: codeye.python.command-injection
    return "ok"


@app.route("/grep", methods=["POST"])
def grep():
    pattern = request.form["pattern"]
    out = subprocess.check_output(f"grep -r {pattern} /srv/data", shell=True)  # expect: codeye.python.command-injection
    return out


@app.route("/shell")
def shell():
    script = request.args["script"]
    subprocess.run(["/bin/sh", "-c", script], check=True)  # expect: codeye.python.command-injection
    return "ok"


def ping_view(request):
    host = request.GET.get("host")
    stream = os.popen("ping -c 1 %s" % host)  # expect: codeye.python.command-injection
    return stream.read()
