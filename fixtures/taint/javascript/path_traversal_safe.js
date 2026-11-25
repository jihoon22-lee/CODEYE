const fs = require("fs");
const path = require("path");
const express = require("express");

const app = express();
const ROOT = "/srv/files";

app.get("/read", (req, res) => {
  const name = path.basename(req.query.name);
  res.send(fs.readFileSync(path.join(ROOT, name)));
});

app.get("/download", (req, res) => {
  const full = path.resolve(ROOT, req.query.file);
  if (!full.startsWith(ROOT + path.sep)) {
    return res.status(404).end();
  }
  res.sendFile(full);
});

app.get("/index", (req, res) => {
  res.send(fs.readFileSync(path.join(ROOT, "index.txt")));
});
