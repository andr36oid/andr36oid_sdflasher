package firmware

import (
	"fmt"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// ErrNoSpace: a download or an unpack would fill the disk the flasher keeps its
// files on (the app's storage on Android).
var ErrNoSpace = i18n.NewFormat("Not enough free space: %s needed, %s free. Free up space and try again.")

// spaceMargin is kept free on top of what a step needs: the BOOT copy and the
// recovery files of the write, and the rest of the system.
const spaceMargin = 512 << 20

// freeBytes is diskFree (the tests replace it).
var freeBytes = diskFree

// CheckSpace says whether dir's disk has room for need more bytes (and the margin),
// nil when it can't tell.
func CheckSpace(dir string, need int64) error {
	if need <= 0 {
		return nil
	}
	free, e := freeBytes(dir)
	if e != nil || free < 0 {
		return nil
	}
	if free < need+spaceMargin {
		return ErrNoSpace.New(SizeText(need+spaceMargin), SizeText(free))
	}
	return nil
}

// SizeText is a size in the apps' "1.2 GB" form.
func SizeText(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/1e9) }
