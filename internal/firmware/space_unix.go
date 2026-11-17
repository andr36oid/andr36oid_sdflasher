//go:build !windows

package firmware

import (
	"os"

	"golang.org/x/sys/unix"
)

// diskFree is what an unprivileged program may still write on dir's disk.
func diskFree(dir string) (int64, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return 0, e
	}
	var st unix.Statfs_t
	if e := unix.Statfs(dir, &st); e != nil {
		return 0, e
	}
	return int64(uint64(st.Bavail) * uint64(st.Bsize)), nil
}
