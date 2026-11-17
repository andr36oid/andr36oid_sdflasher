package firmware

import (
	"os"

	"golang.org/x/sys/windows"
)

// diskFree is what this user may still write on dir's disk.
func diskFree(dir string) (int64, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return 0, e
	}
	p, e := windows.UTF16PtrFromString(dir)
	if e != nil {
		return 0, e
	}
	var free, total, all uint64
	if e = windows.GetDiskFreeSpaceEx(p, &free, &total, &all); e != nil {
		return 0, e
	}
	return int64(free), nil
}
