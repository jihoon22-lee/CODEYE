const serialize = require("node-serialize");
const express = require("express");

const app = express();

app.get("/profile", (req, res) => {
  const raw = Buffer.from(req.cookies.profile, "base64").toString();
  const profile = JSON.parse(raw);
  res.json({ id: profile.id });
});

app.get("/defaults", (req, res) => {
  res.json(serialize.unserialize('{"theme":"dark"}'));
});
