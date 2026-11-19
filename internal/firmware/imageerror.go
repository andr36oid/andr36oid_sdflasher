package firmware

import (
	"context"
	"errors"
	"io/fs"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// The messages of an image that can't be used (the apps translate them; what was
// wrong with it follows as the technical detail).
var (
	// ErrNotRelease: the file unpacked (or needed no unpacking), but it is not an
	// andr36oid release image: another system's image, a damaged one, or a new
	// layout this flasher doesn't know.
	ErrNotRelease = i18n.NewFormat("This file is not an andr36oid release image that this flasher can write. Choose an andr36oid image (an .img.xz file from the andr36oid releases, built on 22 October 2026 or later). If it is one, download it again: it may be damaged.")
	// ErrCardCopy: an image of a card that was already in use, not a release.
	ErrCardCopy = i18n.NewFormat("This is a copy of a card that was already used in a console, not a release image. Choose a release image instead: a copy could carry someone's personal data onto the card.")
	// ErrImageType: a file of a kind the flasher doesn't open.
	ErrImageType     = i18n.NewFormat("The flasher opens andr36oid images packed as .img.xz, .img.gz or .img.zip, and unpacked .img files. Choose one of those.")
	errNoProfileList = i18n.NewFormat("This image is too old for this flasher: it does not list its consoles (andr36oid-profiles.txt). Choose an andr36oid release from 22 October 2026 or later.")
)

// imageError is the error to show for e, the failure to unpack (unpacking true) or
// to inspect an image: e itself when it already says what to do, or is about the
// file (it vanished, can't be read: an i18n cause explains it) or a stop; else a
// damaged file (unpacking) or not a release (inspecting), with e as the detail.
func imageError(e error, unpacking bool) error {
	var m *i18n.Error
	var pe *fs.PathError
	switch {
	case e == nil, errors.As(e, &m), errors.As(e, &pe), errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return e
	case unpacking:
		return i18n.Detailed(i18n.CauseDamaged.New(), e)
	}
	return i18n.Detailed(ErrNotRelease.New(), e)
}

// errTooLarge: an image that unpacks to more than MaxImageSize is not a release.
func errTooLarge() error {
	return i18n.Detailed(ErrNotRelease.New(), errors.New("the image unpacks to more than 32 GiB"))
}
