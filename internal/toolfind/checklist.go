package toolfind

import (
	"fmt"
	"io"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
)

// offlinePlaces says, for a machine without the internet, where a copy of
// each tool brought from a connected machine or an internal mirror goes
// (docs/OPERATIONS.md#분석-도구-옮기기). The tools are not shipped with CODEYE.
var offlinePlaces = map[string]string{
	"go":                 "unpack a Go distribution (go*.linux-amd64.tar.gz) and put its bin on PATH, or set agent.go",
	"git":                "install git from the OS packages of the internal mirror (dnf/apt), or set agent.git",
	"node":               "unpack a Node.js distribution (node-*-linux-x64.tar.xz) and put its bin on PATH, or set agent.tools.node",
	"opencode":           "copy the opencode binary to ~/.opencode/bin/opencode, or set agent.model.opencode",
	"errcheck":           "copy the errcheck binary built for this host to ~/" + ToolPrefixRel + "/go/bin",
	"staticcheck":        "copy the staticcheck binary (built with go install elsewhere) to ~/" + ToolPrefixRel + "/go/bin",
	"gitleaks":           "copy the gitleaks release binary to ~/" + ToolPrefixRel + "/go/bin",
	"ruff":               "copy the ruff binary to ~/" + ToolPrefixRel + "/bin, or `uv tool install ruff --index-url <internal index>`",
	"mypy":               "`uv tool install mypy --index-url <internal index>` (a copied venv keeps its old interpreter path and is refused)",
	"bandit":             "`uv tool install bandit --index-url <internal index>`",
	"lizard":             "`uv tool install lizard --index-url <internal index>`",
	"cppcheck":           "install cppcheck from the OS packages or an internal conda channel into ~/" + ToolPrefixRel + "/cpp",
	"gcc":                "install GCC 13 or newer and its C frontend from the OS packages or an internal mirror",
	"clang-tidy":         "install clang-tools from the OS packages or an internal conda channel into ~/" + ToolPrefixRel + "/cpp",
	"pyright":            "`npm install --prefix ~/" + ToolPrefixRel + "/node --registry <internal registry> pyright`, or copy that node_modules tree",
	"eslint":             "`npm install --prefix ~/" + ToolPrefixRel + "/node --registry <internal registry> eslint @eslint/js typescript-eslint typescript`",
	"tsc":                "`npm install --prefix ~/" + ToolPrefixRel + "/node --registry <internal registry> typescript`",
	"jscpd":              "`npm install --prefix ~/" + ToolPrefixRel + "/node --registry <internal registry> jscpd`",
	"java":               "install OpenJDK and set agent.tools.java to bin/java",
	"pmd":                "extract the PMD 7 binary distribution and set agent.tools.pmd to lib/pmd-cli-*.jar",
	"spotbugs":           "extract the SpotBugs release archive and set agent.tools.spotbugs to lib/spotbugs.jar",
	"cargo":              "install a Rust toolchain from the standalone installer (rust-*-x86_64-unknown-linux-gnu.tar.xz, ./install.sh --prefix=DIR) and set agent.tools.cargo to DIR/bin/cargo",
	"import-linter":      "install Import Linter in an operator Python environment and point agent.tools.import-linter to bin/lint-imports",
	"dependency-cruiser": "install dependency-cruiser and TypeScript in the operator Node tool directory and point agent.tools.dependency-cruiser to bin/dependency-cruiser.mjs",
	"clippy":             "comes with the standalone Rust installer (DIR/bin/cargo-clippy), or set agent.tools.clippy",
	"npm":                "comes with the Node.js distribution",
	"pnpm":               "`npm install --prefix ~/" + ToolPrefixRel + "/node --registry <internal registry> pnpm`",
	"yarn":               "`npm install --prefix ~/" + ToolPrefixRel + "/node --registry <internal registry> yarn@1`",
	"uv":                 "copy the uv binary to ~/" + ToolPrefixRel + "/bin",
	"python":             "install python3 with pip from the OS packages of the internal mirror",
	"gh":                 "optional, online only: offline mode does not use GitHub, so gh is never asked for a token",
}

// TestedSeries is the release series CODEYE was checked with for name
// ("" when any recognized version is accepted).
func TestedSeries(name string) string {
	switch name {
	case "opencode":
		return opencode.Version
	case "uv":
		return uvTestedSeries
	case "python":
		return pythonTestedSeries
	case "npm", "pnpm", "yarn", "git":
		return ""
	}
	return builtin.TestedSeries(name)
}

// WriteChecklist prints r as a Markdown checklist for preparing a machine
// without the internet: each tool checked when found, with its version, the
// tested series and, when missing, where a copy goes. home shortens paths.
func WriteChecklist(w io.Writer, r Result, home string) error {
	if _, err := fmt.Fprint(w, "# CODEYE tools checklist\n\nTools are not shipped with CODEYE. Bring the missing ones from a connected machine or an internal mirror (docs/OPERATIONS.md), then run `codeye tools --checklist` again.\n\n"); err != nil {
		return err
	}
	for _, t := range r.Tools {
		box, detail := " ", ""
		series := TestedSeries(t.Name)
		tested := ""
		if series != "" {
			tested = " (tested " + series + ")"
		}
		switch t.Status {
		case Found:
			box = "x"
			detail = t.Version + tested + " - " + tilde(home, t.Path)
			if !t.VersionTested {
				detail += " [untested version]"
			}
		case Unusable:
			detail = "unusable: " + t.Reason + tested + " - " + offlinePlaces[t.Name]
		default:
			detail = "missing" + tested + " - " + offlinePlaces[t.Name]
		}
		if _, err := fmt.Fprintf(w, "- [%s] %s: %s\n", box, t.Name, strings.TrimSpace(detail)); err != nil {
			return err
		}
	}
	found, missing, unusable := r.Summary()
	_, err := fmt.Fprintf(w, "\n%d found, %d missing, %d unusable.\n", found, missing, unusable)
	return err
}
