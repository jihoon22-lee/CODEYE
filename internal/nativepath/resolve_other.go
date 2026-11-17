//go:build !windows

// Package nativepath resolves existing Native tool and cache paths.
package nativepath

import "path/filepath"

func Resolve(path string) (string, error) { return filepath.EvalSymlinks(path) }
