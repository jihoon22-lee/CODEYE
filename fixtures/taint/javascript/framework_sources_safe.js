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

module.exports = { tail, sockets };
