package github

import (
	"errors"
	"os"
	"runtime"
)

func writeTokenFile(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the token directory")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			return errors.New("cannot restrict the token directory")
		}
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return errors.New("cannot write the token")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if runtime.GOOS != "windows" {
		if err := tmp.Chmod(0o600); err != nil {
			_ = tmp.Close()
			return errors.New("cannot restrict the token file")
		}
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return errors.New("cannot write the token")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("cannot write the token")
	}
	if err := os.Rename(name, path); err != nil {
		return errors.New("cannot write the token")
	}
	return nil
}
