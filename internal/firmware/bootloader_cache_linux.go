package firmware

import (
	"golang.org/x/sys/unix"
	"os"
)

func discardBootloaderCache(d HotfixDisk, off, n int64) error {
	if v, ok := d.(interface{ DiscardReadCache(int64, int64) error }); ok {
		return v.DiscardReadCache(off, n)
	}
	if f, ok := d.(*os.File); ok {
		return unix.Fadvise(int(f.Fd()), off, n, unix.FADV_DONTNEED)
	}
	return nil
}
