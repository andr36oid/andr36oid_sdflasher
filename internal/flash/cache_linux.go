package flash

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropCached makes Linux forget the card's bytes it still holds in memory from
// writing them, so that the read-back that follows reads the card itself and not the
// page cache (a block device's reads are cached like a file's). macOS reads the raw
// /dev/rdisk and Windows the physical drive, which are not cached; the Android app
// talks to the reader over USB.
func dropCached(d Device, off, n int64) {
	for {
		switch v := d.(type) {
		case *guardedDevice:
			d = v.Device
		case deviceErrors:
			d = v.Device
		case *os.File:
			unix.Fadvise(int(v.Fd()), off, n, unix.FADV_DONTNEED)
			return
		default:
			return
		}
	}
}
