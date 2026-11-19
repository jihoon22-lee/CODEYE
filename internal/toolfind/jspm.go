package toolfind

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// The JS package managers the dependency tasks install with
// (docs/ANALYZERS.md#jsts-의존성-받기): JS entry scripts found like the JS
// analyzer tools, validated with node, after them in Result.Tools.
var jsPackageManagerNames = []string{"npm", "pnpm", "yarn"}

func init() {
	specs["npm"] = spec{kindJS, "npm", "npm/bin/npm-cli.js", "npm comes with Node.js (https://nodejs.org); or set `agent.tools.npm`"}
	specs["pnpm"] = spec{kindJS, "pnpm", "pnpm/bin/pnpm.cjs", npmHint("pnpm", "pnpm")}
	specs["yarn"] = spec{kindJS, "yarn", "yarn/bin/yarn.js", npmHint("yarn", "yarn@1")}
	order = append(order, jsPackageManagerNames...)
}

// defaultNpmRegistry is npm's own default; a value equal to it is not a
// user setting.
const defaultNpmRegistry = "https://registry.npmjs.org/"

// npmRegistry asks the found npm for the registry of the user's real
// configuration (`npm config get registry`, their ~/.npmrc and variables).
func npmRegistry(ctx context.Context, node, npm, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, npm, "config", "get", "registry")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "NO_UPDATE_NOTIFIER=1", "npm_config_update_notifier=false")
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	if cmd.Run() != nil {
		return ""
	}
	return registryOf(out.String())
}

// registryOf keeps a registry that differs from npm's default and carries
// no credential (a credential would land in tools.json, the agent's
// arguments and the worker hello).
func registryOf(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == defaultNpmRegistry || strings.TrimSuffix(v, "/")+"/" == defaultNpmRegistry || !domain.ValidRegistryURL(v) {
		return ""
	}
	return v
}
