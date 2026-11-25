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

module.exports = { lang, koaDownload, sockets };
