import os
import subprocess

from flask import Flask, render_template_string
from starlette.requests import Request

app = Flask(__name__)
ROOT = "/srv/files"


@app.route("/pages/<page>")
def page(page):
    with open(os.path.join(ROOT, "pages", page)) as f:  # expect: codeye.python.path-traversal
        return f.read()


def rename(request):
    # Django REST framework: the parsed body of the request.
    subprocess.run("mv a " + request.data["target"], shell=True)  # expect: codeye.python.command-injection


async def preview(request: Request):
    body = await request.json()
    return render_template_string("<h1>" + body["title"] + "</h1>")  # expect: codeye.python.html-injection


def tag(request):
    # Django: a request header.
    subprocess.run("tag " + request.headers["X-Tag"], shell=True)  # expect: codeye.python.command-injection


async def print_job(request: Request):
    form = await request.form()
    os.system("lp " + form["file"])  # expect: codeye.python.command-injection


async def print_raw(request: Request):
    body = await request.body()
    os.system("lp " + body.decode())  # expect: codeye.python.command-injection
