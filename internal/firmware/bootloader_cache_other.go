//go:build !linux
// +build !linux

package firmware

// Physical/raw card devices are opened uncached on these platforms.
func discardBootloaderCache(d HotfixDisk, off, n int64) error {
	if v, ok := d.(interface{ DiscardReadCache(int64, int64) error }); ok {
		return v.DiscardReadCache(off, n)
	}
	return nil
}
