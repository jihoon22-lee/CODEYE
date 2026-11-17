//go:build !linux

package doctor

func resources(string) (*uint64, *uint64) { return nil, nil }
