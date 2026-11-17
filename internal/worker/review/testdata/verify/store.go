package store

import "os"

// Open opens the named data file for reading. The caller owns the returned
// handle and must close it.
func Open(name string) (*os.File, error) {
	return os.Open(name)
}
