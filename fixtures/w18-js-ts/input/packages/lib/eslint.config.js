// Repository ESLint configuration is a program. CODEYE never loads it
// without the repository-code grant; if it ran, it would leave a marker.
const fs = require("node:fs");
const path = require("node:path");

fs.writeFileSync(path.join(__dirname, "CONFIG_LOADED"), "repository eslint config was executed\n");

module.exports = [];
