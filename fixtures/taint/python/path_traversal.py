import codecs
import os
import pathlib

from flask import Flask, request, send_file

app = Flask(__name__)
ROOT = "/srv/files"


@app.route("/read")
def read():
    name = request.args["name"]
    with open(os.path.join(ROOT, name)) as fh:  # expect: codeye.python.path-traversal
        return fh.read()


@app.route("/download")
def download():
    return send_file(ROOT + "/" + request.args["file"])  # expect: codeye.python.path-traversal


@app.route("/delete", methods=["POST"])
def delete():
    target = pathlib.Path(ROOT) / request.form["name"]
    target.unlink()  # expect: codeye.python.path-traversal
    return "deleted"


def report_view(request):
    path = request.GET["report"]
    os.remove(path)  # expect: codeye.python.path-traversal
    return None


@app.route("/notes/<name>")
def notes(name):
    lang = request.args.get("lang", "en")
    with codecs.open(f"{ROOT}/notes/{lang}.txt", "r", "utf-8") as f:  # expect: codeye.python.path-traversal
        return f.read()


@app.route("/archive")
def archive():
    base = pathlib.Path(ROOT)
    target = base / request.args.get("file", "")
    return target.read_bytes()  # expect: codeye.python.path-traversal
