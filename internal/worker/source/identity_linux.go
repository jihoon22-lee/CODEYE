//go:build linux

package source

import (
	"io/fs"
	"syscall"
)

// fileIdentity adds the inode and the change time to a workspace file's
// stamp: a rewrite that restores the size and the modification time still
// changes the change time, which no unprivileged process can set.
type fileIdentity struct {
	ino   uint64
	ctime int64
}

func identityOf(fi fs.FileInfo) fileIdentity {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return fileIdentity{ino: st.Ino, ctime: st.Ctim.Nano()}
	}
	return fileIdentity{}
}
