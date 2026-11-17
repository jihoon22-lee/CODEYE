package doctor

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func resources(path string) (*uint64, *uint64) {
	var memory, disk *uint64
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 3 && fields[0] == "MemTotal:" {
				if n, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					n *= 1024
					memory = &n
				}
			}
		}
	}
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) == nil {
		n := stat.Bavail * uint64(stat.Bsize)
		disk = &n
	}
	return memory, disk
}
