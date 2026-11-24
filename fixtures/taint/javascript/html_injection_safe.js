const escapeHtml = require("escape-html");
const express = require("express");

const app = express();

app.get("/hello", (req, res) => {
  res.send("<h1>Hello " + escapeHtml(req.query.name) + "</h1>");
});

app.get("/search", (req, res) => {
  res.json({ query: req.query.q });
});

app.get("/count", (req, res) => {
  res.send(`<p>${Number(req.query.n)} items</p>`);
});

function showMessage() {
  const params = new URLSearchParams(location.search);
  document.getElementById("msg").textContent = params.get("msg");
}
