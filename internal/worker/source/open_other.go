//go:build !linux

package source

// Non-Linux builds do not claim FIFO-race protection; see docs/ARCHITECTURE.md#로컬-사본.
const openNonblock = 0
