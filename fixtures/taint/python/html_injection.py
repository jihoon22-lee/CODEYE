from flask import Flask, render_template_string, request
from markupsafe import Markup

app = Flask(__name__)


@app.route("/hello")
def hello():
    name = request.args.get("name", "")
    return "<h1>Hello " + name + "</h1>"  # expect: codeye.python.html-injection


@app.route("/greet")
def greet():
    name = request.args.get("name", "")
    return render_template_string("<p>Hi " + name + "</p>")  # expect: codeye.python.html-injection


@app.route("/note")
def note():
    text = request.form["note"]
    body = Markup("<div>%s</div>" % text)  # expect: codeye.python.html-injection
    return render_template_string("{{ body }}", body=body)
