//go:build !linux && !windows

package executor

import (
	"context"
	"testing"
	"time"
)

func TestUnsupportedPlatform(t *testing.T) {
	var e Executor
	res := e.Run(context.Background(), Spec{Executable: "/bin/true", Timeout: time.Second})
	if res.Status != StatusUnsupported || res.Cleanup != CleanupNotStarted {
		t.Fatalf("got %s/%s", res.Status, res.Cleanup)
	}
}
