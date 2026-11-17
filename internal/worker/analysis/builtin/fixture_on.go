//go:build codeye_fixture

package builtin

import (
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/fixture"
)

func fixtureHandlers() []analysis.Handler { return []analysis.Handler{fixture.New()} }
