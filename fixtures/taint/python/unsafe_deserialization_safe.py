import json
import pickle

import yaml
from flask import Flask, request

app = Flask(__name__)


@app.route("/config", methods=["POST"])
def config():
    # safe_load builds only plain data types.
    settings = yaml.safe_load(request.get_data())
    return str(settings)


@app.route("/state", methods=["POST"])
def state():
    return str(json.loads(request.get_data()))


def load_cache(path):
    # A file the service wrote itself, not request input.
    with open(path, "rb") as fh:
        return pickle.load(fh)
