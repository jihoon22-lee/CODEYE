const axios = require("axios");
const https = require("https");
const express = require("express");

const app = express();

app.get("/proxy", async (req, res) => {
  const target = req.query.url;
  const r = await fetch(target); // expect: codeye.javascript.ssrf
  res.send(await r.text());
});

app.post("/hook", async (req, res) => {
  await axios.post(req.body.callback, { ok: true }); // expect: codeye.javascript.ssrf
  res.end();
});

app.get("/avatar", (req, res) => {
  https.get(`https://${req.query.host}/avatar.png`, (r) => r.pipe(res)); // expect: codeye.javascript.ssrf
});
