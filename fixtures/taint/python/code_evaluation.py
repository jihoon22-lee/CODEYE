from flask import Flask, request

app = Flask(__name__)


def calculate(expression):
    return eval(expression)  # expect: codeye.python.code-evaluation


@app.route("/calc")
def calc():
    return str(calculate(request.args["expr"]))


@app.route("/hook", methods=["POST"])
def hook():
    source = request.form["code"]
    exec(source, {})  # expect: codeye.python.code-evaluation
    return "ran"
