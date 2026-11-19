package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

type Drive struct {
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	Serial     string   `json:"serial"`
	Size       int64    `json:"size"`
	SectorSize int64    `json:"sector_size"`
	Volumes    []string `json:"volumes,omitempty"`
	// Fixed: not a USB or SD/MMC card reader. Only ListAll shows these, and only
	// for advanced users; the disk the computer runs from is never listed.
	Fixed bool `json:"fixed,omitempty"`
	// WriteProtected: the card's lock switch is on (or the reader says it is read
	// only). Listed so that people see their card, but never written.
	WriteProtected bool `json:"write_protected,omitempty"`
	// External: a USB disk that says it is a fixed or hard disk rather than a card
	// in a reader (a USB hard drive or SSD).
	External bool `json:"external,omitempty"`
	// Labels: the partition names the listing could read without administrator
	// rights (Linux only; empty elsewhere). Only shown, never trusted for a write.
	Labels []string `json:"labels,omitempty"`
}

// LooksLikeAndr36oid: the listing's partition names are those of an andr36oid
// card. A hint for the app, read without administrator rights; the card is still
// inspected before anything is written.
func (d Drive) LooksLikeAndr36oid() bool {
	has := map[string]bool{}
	for _, l := range d.Labels {
		has[l] = true
	}
	return has["BOOT"] && has["system"] && has["userdata"]
}

// WriteProtectedReason is why a write-protected card can't be chosen (an en.json
// key; the apps translate it).
const WriteProtectedReason = "write-protected: slide the lock switch on the card or adapter"

// The messages of a card that can't be opened (the apps translate them).
var (
	// ErrCardChanged: the chosen card is gone, or another one is in its place.
	ErrCardChanged = i18n.NewCause("The card was removed or changed after it was chosen. Insert it again, choose it in the list once more, then try again.",
		func(s string) bool {
			return strings.Contains(s, "card changed") || strings.Contains(s, "card contents changed") || strings.Contains(s, "no longer safe to open")
		})
	// ErrSectorSize: the reader reports sectors of another size than 512 bytes.
	ErrSectorSize = i18n.NewFormat("This card reader reports a sector size the flasher cannot write (only 512-byte sectors work). Use a different card reader.")
	// errCapacityChanged: the card reports another size when it is opened.
	errCapacityChanged = i18n.Detailed(ErrCardChanged.New(), errors.New("card capacity changed"))
)

// errWriteProtected: Open for writing refused a write-protected card.
var errWriteProtected = errors.New("the card is write-protected")

// LargeBytes: a drive above this is bigger than most SD cards ("256 GB" cards hold
// less than 256 GB, "512 GB" ones more).
const LargeBytes = 256e9

// Large: a drive bigger than LargeBytes or one that says it is a hard disk; the
// apps ask once more, naming its size and model, before writing it.
func (d Drive) Large() bool { return d.Size > LargeBytes || d.External }

// List returns the removable cards: USB card readers and SD/MMC slots.
func List() ([]Drive, error) { return list(false) }

// ListAll also returns fixed disks (hard drives, SSDs, internal readers that show
// up as one), still never the system or boot disk, a read-only one or one under 1 GiB.
func ListAll() ([]Drive, error) { return list(true) }

// BusyPrefix starts the error of a card that can't be written because one of its
// volumes is in use and won't unmount; the app then asks the user to close what uses
// it. The error crosses from the helper process as text, so it's a prefix.
const BusyPrefix = "card in use: "

// Busy reports whether msg is such an error, with what was in use.
func Busy(msg string) (string, bool) {
	i := strings.Index(msg, BusyPrefix)
	if i < 0 {
		return "", false
	}
	return msg[i+len(BusyPrefix):], true
}

func (d Drive) Label() string {
	return fmt.Sprintf("%s - %.1f GB - %s", d.Name, float64(d.Size)/1e9, d.Path)
}

// Same reports whether two listings describe the same card.
func Same(a, b Drive) bool {
	return difference(a, b) == ""
}

// difference names the first thing that tells two listings apart, "" if nothing
// does. The disk, size and sector size must match. The name and serial only count
// when both listings have one: Windows can show a card reader's serial to the admin
// helper but not to the app that picked the card, and the same card then never
// matched after the admin prompt.
func difference(a, b Drive) string {
	switch {
	case a.Path != b.Path:
		return "disk"
	case a.Size != b.Size:
		return fmt.Sprintf("size %d, was %d", a.Size, b.Size)
	case a.SectorSize != b.SectorSize:
		return fmt.Sprintf("sector size %d, was %d", a.SectorSize, b.SectorSize)
	case a.Name != "" && b.Name != "" && a.Name != b.Name:
		return fmt.Sprintf("name %q, was %q", a.Name, b.Name)
	case a.Serial != "" && b.Serial != "" && a.Serial != b.Serial:
		return fmt.Sprintf("serial %q, was %q", a.Serial, b.Serial)
	}
	return ""
}

func Revalidate(want Drive) (Drive, error) {
	ds, e := list(want.Fixed)
	if e != nil {
		return Drive{}, e
	}
	why := ""
	for _, d := range ds {
		if Same(d, want) {
			if d.SectorSize != 512 {
				return d, i18n.Detailed(ErrSectorSize.New(), fmt.Errorf("%d-byte logical sectors; only 512-byte logical sectors are supported", d.SectorSize))
			}
			return d, nil
		}
		if d.Path == want.Path {
			why = " (" + d.Path + " now has " + difference(d, want) + ")"
		}
	}
	return Drive{}, i18n.Detailed(ErrCardChanged.New(), errors.New("the selected card was removed, changed, or is no longer safe to open"+why))
}

type Handle struct {
	File    *os.File
	Cleanup func()
	// Remount: after writing, the computer mounts the card again instead of
	// ejecting it (macOS ejects otherwise), so people can copy their own APKs to
	// AUTOAPK. Windows and Linux mount it again by themselves.
	Remount bool
}

func (h *Handle) Close() {
	h.File.Close()
	if h.Cleanup != nil {
		h.Cleanup()
	}
}
