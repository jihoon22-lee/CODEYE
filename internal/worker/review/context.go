// Package review composes the static analyzer with bounded model tasks on the
// same immutable snapshot. Source text is data and cannot select tools or roles.
package review

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// maxFileBytes bounds a snapshot file the review reads.
const maxFileBytes = record.MaxSourceBytes

// inScope reports whether a snapshot path lies in the run scope.

func inScope(name string, scope domain.Scope) bool {
	if scope.Kind == "full" {
		return true
	}
	if scope.Kind != "paths" {
		return false
	}
	for _, prefix := range scope.Paths {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}
