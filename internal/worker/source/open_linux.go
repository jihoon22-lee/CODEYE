//go:build linux

package source

import "syscall"

// openNonblock keeps open(2) of a path that was swapped for a FIFO after the scan from
// blocking forever waiting for a writer. It has no effect on regular files.
const openNonblock = syscall.O_NONBLOCK
