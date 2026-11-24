const axios = require("axios");
const express = require("express");

const app = express();

app.get("/user", async (req, res) => {
  const r = await fetch(`https://api.example.com/users/${encodeURIComponent(req.query.id)}`);
  res.send(await r.text());
});

app.get("/repo", async (req, res) => {
  const r = await axios.get("https://api.example.com/repos/" + req.query.name);
  res.json(r.data);
});

app.get("/status", async (req, res) => {
  const r = await fetch("https://status.example.com/health");
  res.send(await r.text());
});
