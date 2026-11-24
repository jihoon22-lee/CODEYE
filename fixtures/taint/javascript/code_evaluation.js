const vm = require("vm");
const express = require("express");

const app = express();

app.get("/calc", (req, res) => {
  res.send(String(eval(req.query.expr))); // expect: codeye.javascript.code-evaluation
});

app.post("/filter", (req, res) => {
  const fn = new Function("row", req.body.predicate); // expect: codeye.javascript.code-evaluation
  res.json([1, 2, 3].filter(fn));
});

app.post("/template", (req, res) => {
  res.send(vm.runInNewContext(req.body.code, {})); // expect: codeye.javascript.code-evaluation
});
