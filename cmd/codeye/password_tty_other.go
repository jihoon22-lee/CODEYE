//go:build !linux

package main

import (
	"errors"
	"io"
	"os"
)

// isTerminal reports a character device (a console). Reading a password
// with echo off is implemented for Linux only; elsewhere a terminal is
// refused and --password-file or a pipe is required.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func readNoEcho(*os.File, io.Writer, string) (string, error) {
	return "", errors.New("reading a password from a terminal is supported on Linux only; use --password-file or pipe it on standard input")
}
