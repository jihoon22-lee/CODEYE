import ast

from flask import Flask, request

app = Flask(__name__)


@app.route("/parse")
def parse():
    # literal_eval accepts literals only and runs no code.
    return str(ast.literal_eval(request.args["value"]))


@app.route("/double")
def double():
    n = int(request.args["n"])
    return str(eval("%d * 2" % n))


@app.route("/version")
def version():
    return str(eval("1 + 1"))
