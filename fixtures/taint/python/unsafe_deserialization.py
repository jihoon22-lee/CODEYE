import base64
import pickle

import yaml
from flask import Flask, request

app = Flask(__name__)


@app.route("/session", methods=["POST"])
def session():
    blob = base64.b64decode(request.cookies["state"])
    state = pickle.loads(blob)  # expect: codeye.python.unsafe-deserialization
    return str(state)


@app.route("/config", methods=["POST"])
def config():
    document = request.get_data()
    settings = yaml.load(document, Loader=yaml.Loader)  # expect: codeye.python.unsafe-deserialization
    return str(settings)


@app.route("/import", methods=["POST"])
def import_settings():
    return str(yaml.unsafe_load(request.data))  # expect: codeye.python.unsafe-deserialization
