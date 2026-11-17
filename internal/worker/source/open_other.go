//go:build !linux

package source

// Non-Linux builds do not claim FIFO-race protection; see docs/25 §2.
const openNonblock = 0
