const { exec } = require("child_process");
const fs = require("fs");
const path = require("path");

const ROOT = "/srv/files";

function lang(req, res) {
  const file = path.join(ROOT, "i18n", req.get("Accept-Language") + ".json");
  res.send(fs.readFileSync(file)); // expect: codeye.javascript.path-traversal
}

async function koaDownload(ctx) {
  ctx.body = fs.createReadStream(path.join(ROOT, ctx.query.file)); // expect: codeye.javascript.path-traversal
}

function sockets(io) {
  io.on("connection", (socket) => {
    socket.on("ping", async (target, callback) => {
      exec("ping -c 1 " + target.host, (err, out) => callback(out)); // expect: codeye.javascript.command-injection
    });
  });
}

function attachment(req, res) {
  res.send(fs.readFileSync(path.join(ROOT, req.header("X-File")))); // expect: codeye.javascript.path-traversal
}

async function koaConvert(ctx) {
  exec("convert " + ctx.request.body.name, () => {}); // expect: codeye.javascript.command-injection
  ctx.body = fs.readFileSync(path.join(ROOT, ctx.params.name)); // expect: codeye.javascript.path-traversal
  ctx.body = fs.readFileSync(path.join(ROOT, "themes", ctx.cookies.get("theme"))); // expect: codeye.javascript.path-traversal
  exec("echo " + ctx.headers["x-run"], () => {}); // expect: codeye.javascript.command-injection
}

module.exports = { lang, koaDownload, sockets, attachment, koaConvert };
