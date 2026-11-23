package review

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// PromptBudget is the request budget of the tests' default handlers.
const PromptBudget = DefaultRequestBytes

// smallBudget is the smallest request budget: its limits are those of the
// earlier fixed request (eight excerpts, 12000 source bytes, 400-byte
// prose, four candidates). The selector tests run at it, so the selector
// windows keep their own caps beside the units.
var smallBudget = Budget{RequestBytes: MinRequestBytes, OutputTokens: MinOutputTokens}

// runRoleSmall is runRole at smallBudget.
func runRoleSmall(t *testing.T, j analysis.Job, static analysis.Handler, caller Caller, goVersion string) ([]analysis.TaskSpec, map[string]analysis.TaskResult) {
	t.Helper()
	return runRoleWith(t, j, Handler{Static: static, Caller: caller, Budget: smallBudget}, goVersion)
}

// cite is a model citation of an excerpt: its ID when it is short enough to
// cite whole, otherwise its first lines as a range.
func cite(e record.Excerpt) string {
	if e.End-e.Start+1 <= record.WholeCitationLines || e.Origin == record.OriginCited {
		return e.ID
	}
	return fmt.Sprintf("%s:%d-%d", e.ID, e.Start, e.Start+record.WholeCitationLines-1)
}

// citeLine cites the first line of e that contains text, or e as cite does
// when no line does.
func citeLine(e record.Excerpt, text string) string {
	for i, line := range strings.Split(e.Text, "\n") {
		if strings.Contains(line, text) {
			return fmt.Sprintf("%s:%d-%d", e.ID, e.Start+i, e.Start+i)
		}
	}
	return cite(e)
}
