package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// toolsCommand is `codeye tools [--config PATH] [--json]`: it detects the
// tools CODEYE can use on this machine and prints what was found, what is
// missing and how to add it (docs/ANALYZERS.md#도구-찾기). It changes nothing
// except <state_dir>/tools.json when a personal configuration names an
// existing state directory.
func toolsCommand(ctx context.Context, args []string, out, stderr io.Writer) int {
	var configPath string
	var asJSON bool
	if !parse("tools", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&configPath, "config", "", "")
		fs.BoolVar(&asJSON, "json", false, "")
	}) {
		return 2
	}
	var cfg *personal.Config
	switch {
	case configPath != "":
		c, err := personal.Load(configPath, knownTools())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		cfg = c
	default:
		// The default configuration is optional here: the table is useful
		// before anything is set up.
		if p, err := personal.DefaultPath(); err == nil {
			if _, statErr := os.Stat(p); statErr == nil {
				c, err := personal.Load(p, knownTools())
				if err != nil {
					fmt.Fprintln(stderr, "ignoring the default configuration:", err)
				} else {
					cfg = c
				}
			}
		}
	}
	res := detectTools(ctx, cfg)
	if cfg != nil {
		// Only into an existing state directory: this command does not set
		// anything up.
		if st, err := os.Stat(cfg.StateDir); err == nil && st.IsDir() {
			if err := toolfind.Write(filepath.Join(cfg.StateDir, toolfind.FileName), res); err != nil {
				fmt.Fprintln(stderr, "cannot write", toolfind.FileName+":", err)
			}
		}
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fmt.Fprintln(stderr, "cannot write the result")
			return 1
		}
		return 0
	}
	home, _ := os.UserHomeDir()
	if err := toolfind.WriteTable(out, res, home); err != nil {
		fmt.Fprintln(stderr, "cannot write the result")
		return 1
	}
	return 0
}

// explicitTools maps the personal configuration onto the settings that always
// win over detection. cfg may be nil.
func explicitTools(cfg *personal.Config) toolfind.Explicit {
	if cfg == nil {
		return toolfind.Explicit{}
	}
	a := cfg.Agent
	ex := toolfind.Explicit{Go: a.Go, GoModCache: a.GoModCache, Tools: a.Tools}
	if a.Model != nil {
		ex.OpenCode = a.Model.OpenCode
	}
	return ex
}

// detectTools runs the detection with the explicit settings of cfg (nil for
// none).
func detectTools(ctx context.Context, cfg *personal.Config) toolfind.Result {
	return toolfind.Detect(ctx, toolfind.Options{Explicit: explicitTools(cfg)})
}

// useDetectedTools puts what detection found into the agent settings of cfg:
// explicit values stay, missing ones are filled, and both Python type checkers
// are enabled when both are present. The agent arguments built from cfg.Agent
// (agentArgv) are then complete.
func useDetectedTools(cfg *personal.Config, res toolfind.Result) {
	r := toolfind.Resolve(res, explicitTools(cfg))
	a := &cfg.Agent
	a.Go, a.GoModCache = r.Go, r.GoModCache
	a.Tools = r.Tools
	a.PythonBothTypeCheckers = r.PythonBothTypeCheckers
}

// prepareTools is the start-up step of personal mode: detect the tools
// (unless agent.auto_detect is false), record <state_dir>/tools.json, print
// the found / missing / how-to-add table to out and complete cfg.Agent with
// the result. It returns the detection result; with detection off the result
// is empty and cfg.Agent is unchanged.
func prepareTools(ctx context.Context, cfg *personal.Config, out io.Writer) (toolfind.Result, error) {
	if !cfg.Agent.AutoDetectEnabled() {
		return toolfind.Result{}, nil
	}
	res := detectTools(ctx, cfg)
	var werr error
	if err := toolfind.Write(filepath.Join(cfg.StateDir, toolfind.FileName), res); err != nil {
		werr = errors.New("cannot write " + toolfind.FileName)
	}
	home, _ := os.UserHomeDir()
	if err := toolfind.WriteTable(out, res, home); err != nil {
		return res, err
	}
	useDetectedTools(cfg, res)
	return res, werr
}
