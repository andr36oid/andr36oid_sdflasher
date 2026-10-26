//go:build !windows

package jobs

import "os"

// cacheBase is the directory the flasher keeps its cache folder in. Everywhere
// except the Windows portable build that is the user's cache directory.
func cacheBase() (string, error) { return os.UserCacheDir() }
