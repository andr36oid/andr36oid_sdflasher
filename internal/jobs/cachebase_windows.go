//go:build windows

package jobs

import (
	"os"
	"path/filepath"
)

// cacheBase keeps the cache next to the executable on Windows, so the portable
// build is self-contained: everything it unpacks and downloads lives in a folder
// beside the .exe and goes away when that folder is deleted, leaving nothing in
// the user profile. If the executable's folder cannot be written (for example a
// read-only location), it falls back to the user's cache directory.
func cacheBase() (string, error) {
	if exe, e := os.Executable(); e == nil {
		if resolved, e := filepath.EvalSymlinks(exe); e == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		if writable(dir) {
			return dir, nil
		}
	}
	return os.UserCacheDir()
}

// writable reports whether a file can be created in dir.
func writable(dir string) bool {
	f, e := os.CreateTemp(dir, ".write-check-*")
	if e != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}
