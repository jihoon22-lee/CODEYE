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
