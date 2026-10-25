package firmware

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// recentWindow protects the working files of another flasher instance that may
// be staging or flashing right now: anything touched within it is left alone.
const recentWindow = 15 * time.Minute

// CleanWorkDir drops the scratch the flasher leaves in its cache directory:
// decompressed images (image-*.img), half-finished downloads (download-*), and
// spent operation directories. It is meant to run at startup, when nothing of
// this instance's is in use, so those files are safe to remove. Two things are
// kept: a recovery backup (an operation directory holding a non-empty recovery/
// is left in place, with only its large scratch images stripped) so the user can
// still restore from it, and the content-addressed download cache
// (<digest>-name), which exists to avoid downloading the same release twice.
// Files touched in the last few minutes are skipped so a second instance that is
// working at the same time is never disturbed.
func CleanWorkDir(dir string) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return
	}
	for _, en := range entries {
		name := en.Name()
		full := filepath.Join(dir, name)
		if recentlyTouched(full) {
			continue
		}
		switch {
		case !en.IsDir() && (strings.HasPrefix(name, "image-") || strings.HasPrefix(name, "download-")):
			os.Remove(full)
		case en.IsDir() && strings.HasPrefix(name, "operation-"):
			if hasEntries(filepath.Join(full, "recovery")) {
				stripScratch(full)
			} else {
				os.RemoveAll(full)
			}
		}
	}
}

// CleanStagedImage removes a staged image once it is no longer needed, e.g. when
// a new release supersedes it. It only deletes a file the flasher itself staged
// (an image-*.img inside dir), so a user's own selected .img is never touched.
func CleanStagedImage(dir, path string) {
	if path == "" || filepath.Dir(path) != dir {
		return
	}
	if base := filepath.Base(path); strings.HasPrefix(base, "image-") && strings.HasSuffix(base, ".img") {
		os.Remove(path)
	}
}

// stripScratch removes the large staged images from a kept operation directory
// without touching its recovery backup or the request record a restore needs.
func stripScratch(dir string) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return
	}
	for _, en := range entries {
		if en.IsDir() {
			continue
		}
		n := en.Name()
		if strings.HasPrefix(n, "image-") || strings.HasPrefix(n, "system-") || strings.HasPrefix(n, "boot-") {
			os.Remove(filepath.Join(dir, n))
		}
	}
}

// hasEntries reports whether dir exists and holds at least one entry.
func hasEntries(dir string) bool {
	entries, e := os.ReadDir(dir)
	return e == nil && len(entries) > 0
}

// recentlyTouched reports whether path was modified within recentWindow.
func recentlyTouched(path string) bool {
	fi, e := os.Stat(path)
	return e == nil && time.Since(fi.ModTime()) < recentWindow
}
