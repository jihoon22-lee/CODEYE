const express = require("express");

const app = express();

app.get("/hello", (req, res) => {
  res.send("<h1>Hello " + req.query.name + "</h1>"); // expect: codeye.javascript.html-injection
});

app.get("/search", (req, res) => {
  const q = req.query.q;
  res.send(`<p>Results for ${q}</p>`); // expect: codeye.javascript.html-injection
});

function showMessage() {
  const params = new URLSearchParams(location.search);
  document.getElementById("msg").innerHTML = params.get("msg"); // expect: codeye.javascript.html-injection
}
