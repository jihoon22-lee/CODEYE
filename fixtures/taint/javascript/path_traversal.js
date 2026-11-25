const fs = require("fs");
const path = require("path");
const express = require("express");

const app = express();
const ROOT = "/srv/files";

app.get("/read", (req, res) => {
  const name = req.query.name;
  res.send(fs.readFileSync(path.join(ROOT, name))); // expect: codeye.javascript.path-traversal
});

app.get("/download/:file", (req, res) => {
  res.download(ROOT + "/" + req.params.file); // expect: codeye.javascript.path-traversal
});

app.post("/remove", async (req, res) => {
  await fs.promises.unlink(path.resolve(ROOT, req.body.target)); // expect: codeye.javascript.path-traversal
  res.end();
});
