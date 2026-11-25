import codecs
import os
import pathlib

from flask import Flask, abort, request, send_file
from werkzeug.utils import secure_filename

app = Flask(__name__)
ROOT = "/srv/files"


@app.route("/read")
def read():
    name = os.path.basename(request.args["name"])
    with open(os.path.join(ROOT, name)) as fh:
        return fh.read()


@app.route("/upload", methods=["POST"])
def upload():
    upload = request.files["file"]
    upload.save(os.path.join(ROOT, secure_filename(upload.filename)))
    return "saved"


@app.route("/download")
def download():
    path = os.path.realpath(os.path.join(ROOT, request.args["file"]))
    if not path.startswith(ROOT + os.sep):
        abort(404)
    return send_file(path)


@app.route("/fixed")
def fixed():
    with open(os.path.join(ROOT, "index.txt")) as fh:
        return fh.read()


@app.route("/notes")
def notes():
    lang = secure_filename(request.args.get("lang", "en"))
    with codecs.open(f"{ROOT}/notes/{lang}.txt", "r", "utf-8") as f:
        return f.read()


@app.route("/archive")
def archive():
    base = pathlib.Path(ROOT)
    target = base / secure_filename(request.args.get("file", ""))
    return target.read_bytes()
