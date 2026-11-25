import sqlite3

from flask import Flask, request

app = Flask(__name__)


@app.route("/user")
def user():
    conn = sqlite3.connect("app.db")
    cur = conn.cursor()
    # The input is a bound parameter, not part of the statement text.
    cur.execute("SELECT id FROM users WHERE name = ?", (request.args["name"],))
    return str(cur.fetchall())


@app.route("/page")
def page():
    conn = sqlite3.connect("app.db")
    limit = int(request.args.get("limit", "10"))
    rows = conn.execute("SELECT id FROM items LIMIT %d" % limit)
    return str(rows.fetchall())


@app.route("/count")
def count():
    conn = sqlite3.connect("app.db")
    return str(conn.execute("SELECT count(*) FROM items").fetchone())
