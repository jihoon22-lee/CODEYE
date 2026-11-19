//go:build !linux

package source

import "io/fs"

// fileIdentity is empty off Linux: the stamp is size, modification time and
// mode only.
type fileIdentity struct{}

func identityOf(fs.FileInfo) fileIdentity { return fileIdentity{} }
