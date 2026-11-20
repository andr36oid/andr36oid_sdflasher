package flash

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
)

// flushRefusals are the errors with which a card reader turns down the flush
// request itself ("I have no cache to flush"), per system. Only where the read-back
// reads the card and not a copy in memory: macOS writes and reads the raw
// /dev/rdisk, Windows the physical drive opened write-through, neither cached by
// the system. Every write is then read back from the card, so a refused flush costs
// nothing. Not on Linux: there the block device is cached, and pages a failed fsync
// left dirty are not dropped before the read-back (cache_linux.go), so it would
// read memory; the kernel already ignores a flush a device has no cache for. Real
// failures (EIO, ENODEV, ENXIO, a reader that went away) are not in the lists and
// still end the write.
var flushRefusals = map[string][]syscall.Errno{
	// EINVAL, ENOTTY, ENOTSUP, EOPNOTSUPP (Go retries ENOTSUP with fsync itself)
	"darwin": {22, 25, 45, 102},
	// ERROR_INVALID_FUNCTION, ERROR_NOT_SUPPORTED, ERROR_INVALID_PARAMETER
	"windows": {1, 50, 87},
}

// flushRefused reports whether e, the error of a flush on goos, is the device
// turning down the request rather than failing.
func flushRefused(goos string, e error) bool {
	var errno syscall.Errno
	if !errors.As(e, &errno) {
		return false
	}
	for _, r := range flushRefusals[goos] {
		if errno == r {
			return true
		}
	}
	return false
}

var flushRefusedOnce sync.Once

// flush is d.Sync, with a refused flush taken as done (said once on stderr).
func flush(d Device, goos string) error {
	e := d.Sync()
	if e != nil && flushRefused(goos, e) {
		flushRefusedOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "card reader refused the flush (%v); relying on the read-back\n", e)
		})
		return nil
	}
	return e
}

var hostOS = runtime.GOOS
