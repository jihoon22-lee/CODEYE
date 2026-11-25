const child_process = require("child_process");
const express = require("express");

const app = express();

app.get("/ping", (req, res) => {
  // The argument vector is passed without a shell.
  const out = child_process.execFileSync("ping", ["-c", "1", req.query.host]);
  res.send(out);
});

app.get("/count", (req, res) => {
  const n = parseInt(req.query.n, 10);
  child_process.exec("seq " + n, (err, out) => res.send(out));
});

app.get("/uptime", (req, res) => {
  child_process.exec("uptime", (err, out) => res.send(out));
});
