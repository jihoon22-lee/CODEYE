const serialize = require("node-serialize");
const express = require("express");

const app = express();

app.get("/profile", (req, res) => {
  const raw = Buffer.from(req.cookies.profile, "base64").toString();
  const profile = serialize.unserialize(raw); // expect: codeye.javascript.unsafe-deserialization
  res.json({ id: profile.id });
});
