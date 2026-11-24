import requests
from flask import Flask, request

app = Flask(__name__)


@app.route("/user")
def user():
    uid = request.args["id"]
    # Scheme and host are fixed; the input only selects a path below them.
    return requests.get(f"https://api.example.com/users/{uid}", timeout=5).text


@app.route("/item")
def item():
    return requests.get("https://api.example.com/items/" + request.args["id"], timeout=5).text


@app.route("/health")
def health():
    return requests.get("https://status.example.com/health", timeout=5).text


@app.route("/lookup")
def lookup():
    # The input is a query parameter of a fixed address.
    return requests.get("https://search.example.com/", params={"q": request.args["q"]}, timeout=5).text
