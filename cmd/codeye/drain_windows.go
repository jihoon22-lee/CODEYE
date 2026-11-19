//go:build windows

package main

// drainSignal: Windows has no drain signal (personal stop is Linux-only);
// the agent never drains and stops only on its shutdown signal.
func drainSignal() (<-chan struct{}, func()) { return nil, func() {} }
