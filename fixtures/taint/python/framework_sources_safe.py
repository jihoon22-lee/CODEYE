import os
import pathlib
import subprocess
from urllib.parse import urlencode

from flask import Flask, render_template_string, request, send_file
from markupsafe import Markup
from werkzeug.utils import safe_join

app = Flask(__name__)
ROOT = "/srv/files"


@app.route("/themes/<theme>/<path:path>")
def theme_file(theme, path):
    return send_file(safe_join(ROOT, "themes", theme, path))


@app.route("/plugins/<plugin>/config")
def plugin_config(plugin):
    # The file's content is rendered, not the name taken from the URL.
    with open(os.path.join(ROOT, "plugins", "index.html")) as f:
        config_html = f.read()
    return render_template_string(config_html, plugin=plugin)


@app.route("/next")
def next_link():
    return Markup('<a href="?' + urlencode(request.args) + '">next</a>')


def rename(req):
    # Django REST framework body and header, passed as argument lists.
    subprocess.run(["mv", "a", req.data["target"]])
    subprocess.run(["tag", req.headers["X-Tag"]])


async def preview(req):
    body = await req.json()
    form = await req.form()
    return render_template_string("<h1>{{ t }}</h1><p>{{ f }}</p>", t=body["title"], f=form["note"])


@app.route("/icons")
def icon():
    # The final component of a path object.
    name = pathlib.Path(request.args["icon"]).name
    with open(os.path.join(ROOT, "icons", name)) as f:
        return f.read()
