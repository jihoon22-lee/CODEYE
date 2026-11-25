const fs = require("fs");
const path = require("path");

const ROOT = "/srv/files";

function tail(stream) {
  // A stream's data event is not a client message.
  stream.on("data", (chunk) => {
    fs.appendFileSync(path.join(ROOT, "log.txt"), chunk);
  });
}

function sockets(io) {
  io.on("connection", (socket) => {
    socket.on("read", (name, callback) => {
      callback(fs.readFileSync(path.join(ROOT, path.basename(name))));
    });
  });
}

function lang(req, res) {
  res.send(fs.readFileSync(path.join(ROOT, "i18n", path.basename(req.get("Accept-Language")))));
  res.send(fs.readFileSync(path.join(ROOT, path.basename(req.header("X-File")))));
}

async function koaDownload(ctx) {
  ctx.body = fs.readFileSync(path.join(ROOT, path.basename(ctx.query.file)));
  ctx.body = fs.readFileSync(path.join(ROOT, path.basename(ctx.params.name)));
  ctx.body = fs.readFileSync(path.join(ROOT, "themes", path.basename(ctx.cookies.get("theme"))));
  ctx.body = fs.readFileSync(path.join(ROOT, path.basename(ctx.request.body.name)));
}

module.exports = { tail, sockets, lang, koaDownload };
