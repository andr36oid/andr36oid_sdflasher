//go:build !linux

package flash

// dropCached is only needed on Linux (cache_linux.go).
func dropCached(Device, int64, int64) {}
