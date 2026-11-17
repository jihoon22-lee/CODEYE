// Package builtin assembles the analysis handlers a worker may advertise.
// A handler is returned only when its toolchain is configured and a real
// probe through the W05 runner succeeded; otherwise it is reported as
// unavailable and its capability is not advertised.
package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Config comes from the agent hello (go_executable, go_mod_cache) and the
// worker's own scratch root.
type Config struct {
	// GoExecutable is the absolute go command; "" disables the go-vet handler.
	GoExecutable string
	// GoModCache is an optional absolute offline module cache.
	GoModCache string
	// ProbeDir is an absolute writable directory outside any source tree
	// (e.g. the worker scratch root) used for toolchain probes.
	ProbeDir string
}

// Unavailable explains why a known handler is not advertised.
type Unavailable struct {
	Name   string
	Reason string // not_configured | invalid_config | probe_failed
	Detail string // no secrets, no paths
}

// Handlers probes configured toolchains through runner and returns the
// handlers that may be advertised, plus the ones that may not.
func Handlers(ctx context.Context, cfg Config, runner executor.Runner) ([]analysis.Handler, []Unavailable) {
	var out []analysis.Handler
	var missing []Unavailable
	switch {
	case cfg.GoExecutable == "":
		missing = append(missing, Unavailable{Name: govet.HandlerName, Reason: "not_configured"})
	default:
		h, err := govet.New(govet.Config{GoExecutable: cfg.GoExecutable, GoModCache: cfg.GoModCache})
		if err != nil {
			missing = append(missing, Unavailable{Name: govet.HandlerName, Reason: "invalid_config", Detail: err.Error()})
			break
		}
		if runner == nil || cfg.ProbeDir == "" {
			missing = append(missing, Unavailable{Name: govet.HandlerName, Reason: "probe_failed", Detail: "no runner or probe dir"})
			break
		}
		if _, err := h.Probe(ctx, runner, cfg.ProbeDir); err != nil {
			missing = append(missing, Unavailable{Name: govet.HandlerName, Reason: "probe_failed", Detail: err.Error()})
			break
		}
		out = append(out, h)
	}
	out = append(out, fixtureHandlers()...)
	return out, missing
}
