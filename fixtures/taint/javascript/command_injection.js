const child_process = require("child_process");
const { exec } = require("child_process");
const express = require("express");

const app = express();

app.get("/ping", (req, res) => {
  const host = req.query.host;
  child_process.exec("ping -c 1 " + host, (err, out) => res.send(out)); // expect: codeye.javascript.command-injection
});

app.post("/archive", (req, res) => {
  exec(`tar czf backup.tgz ${req.body.dir}`); // expect: codeye.javascript.command-injection
  res.end();
});

function runShell(script) {
  return child_process.spawnSync("sh", ["-c", script]); // expect: codeye.javascript.command-injection
}

app.get("/run", (req, res) => {
  runShell(req.query.script);
  res.end();
});
