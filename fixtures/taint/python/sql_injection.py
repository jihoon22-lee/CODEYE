import sqlite3

from flask import Flask, request
from sqlalchemy import text

app = Flask(__name__)


def lookup_query(name):
    return "SELECT id FROM users WHERE name = '%s'" % name


@app.route("/user")
def user():
    conn = sqlite3.connect("app.db")
    cur = conn.cursor()
    cur.execute(lookup_query(request.args["name"]))  # expect: codeye.python.sql-injection
    return str(cur.fetchall())


@app.route("/sort")
def sort(session):
    order = request.args.get("order", "id")
    rows = session.execute(text(f"SELECT * FROM items ORDER BY {order}"))  # expect: codeye.python.sql-injection
    return str(list(rows))


def search_view(request, Item):
    term = request.GET["q"]
    return Item.objects.raw("SELECT * FROM item WHERE title LIKE '%" + term + "%'")  # expect: codeye.python.sql-injection
