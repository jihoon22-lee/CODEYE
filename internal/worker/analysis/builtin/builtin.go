// Package builtin assembles the handlers a worker may advertise.
// TEMPORARY W06 COPY with W08's signature: the W08 branch owns the final file
// (it adds the probed go-vet handler). Without W08 a production build
// registers no handler, so the node advertises no analysis capability and
// never claims.
package builtin

import (
	"context"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Config comes from the agent hello and the worker's own scratch root.
type Config struct {
	GoExecutable string
	GoModCache   string
	ProbeDir     string
}

// Unavailable explains why a known handler is not advertised.
type Unavailable struct {
	Name   string
	Reason string // not_configured | invalid_config | probe_failed
	Detail string
}

// Handlers returns the handlers that may be advertised.
func Handlers(ctx context.Context, cfg Config, runner executor.Runner) ([]analysis.Handler, []Unavailable) {
	missing := []Unavailable{{Name: "go-vet", Reason: "not_configured", Detail: "W08 handler not in this build"}}
	return fixtureHandlers(), missing
}
