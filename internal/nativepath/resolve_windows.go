//go:build windows

package nativepath

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Resolve follows the kernel's actual mapping, including tool-cache junctions
// between volumes, and returns a DOS or UNC path for exec and comparisons.
func Resolve(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 0)
	if err != nil {
		return "", err
	}
	if n == 0 || n >= uint32(len(buf)) {
		return "", errors.New("unresolvable native path")
	}
	name := windows.UTF16ToString(buf[:n])
	if strings.HasPrefix(name, `\\?\UNC\`) {
		return `\\` + name[len(`\\?\UNC\`):], nil
	}
	return strings.TrimPrefix(name, `\\?\`), nil
}
