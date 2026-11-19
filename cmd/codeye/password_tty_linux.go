//go:build linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

// readNoEcho prompts on stderr and reads one line from the terminal with
// echo turned off, restoring the terminal state afterwards.
func readNoEcho(f *os.File, stderr io.Writer, prompt string) (string, error) {
	fd := int(f.Fd())
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return "", errors.New("standard input is not a terminal")
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &quiet); err != nil {
		return "", errors.New("cannot turn off terminal echo")
	}
	defer func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, old) }()
	fmt.Fprint(stderr, prompt)
	line, err := bufio.NewReaderSize(io.LimitReader(f, maxPasswordInput), maxPasswordInput).ReadString('\n')
	fmt.Fprintln(stderr)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("cannot read the password")
	}
	return firstLine(line), nil
}
