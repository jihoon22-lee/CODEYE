//go:build !codeye_fixture

package builtin

import "github.com/jihoon22-lee/CODEYE/internal/worker/analysis"

// fixtureHandlers is empty in production builds. The worker provides the
// codeye_fixture variant (fixture_on.go); production never advertises it.
func fixtureHandlers() []analysis.Handler { return nil }
