//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// drainSignal returns a channel closed when the agent receives SIGUSR1, the
// drain request `codeye stop` sends. stop releases the handler.
func drainSignal() (<-chan struct{}, func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1)
	drain := make(chan struct{})
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			close(drain)
		case <-done:
		}
	}()
	return drain, func() { signal.Stop(sig); close(done) }
}
