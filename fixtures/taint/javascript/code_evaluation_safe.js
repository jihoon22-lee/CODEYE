const vm = require("vm");
const express = require("express");

const app = express();

app.get("/calc", (req, res) => {
  const n = Number(req.query.n);
  res.send(String(eval(n + 1)));
});

app.post("/parse", (req, res) => {
  res.json(JSON.parse(req.body.data));
});

app.get("/version", (req, res) => {
  res.send(vm.runInNewContext("1 + 1", {}));
});
