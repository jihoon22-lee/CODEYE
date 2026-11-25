from flask import Flask, jsonify, render_template_string, request
from markupsafe import escape

app = Flask(__name__)


@app.route("/hello")
def hello():
    name = request.args.get("name", "")
    return "<h1>Hello " + escape(name) + "</h1>"


@app.route("/greet")
def greet():
    # The template is constant; Jinja escapes the variable.
    return render_template_string("<p>Hi {{ name }}</p>", name=request.args.get("name", ""))


@app.route("/api")
def api():
    return jsonify(name=request.args.get("name", ""))


def escape_html(text):
    return str(escape(text))


@app.route("/search")
def search():
    term = request.args.get("q", "")
    page = "<h2>Results</h2>"
    page += f"<p>No match for {escape_html(term)}</p>"
    return page
