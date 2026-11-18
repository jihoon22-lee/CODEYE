//go:build windows

package main

// drainSignal: Windows has no drain signal (W25 personal stop is Linux-only);
// the agent never drains and stops only on its shutdown signal.
func drainSignal() (<-chan struct{}, func()) { return nil, func() {} }
