package flash

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// FailureKind says what a failed write was up against, so that the apps can add
// the right hints after the error itself. Only failures of the card or the reader
// get one; a cancel, a wrong choice, the network or a damaged download keep just
// their own message.
type FailureKind string

const (
	// FailureOther: no hints.
	FailureOther FailureKind = ""
	// FailureDevice: the card or the reader failed or went away: an I/O error, a short
	// write, the device gone, or no access to it.
	FailureDevice FailureKind = "device"
	// FailureVerify: what was read back from the card is not what was written.
	FailureVerify FailureKind = "verify"
	// FailureCapacity: the card is not the size it reports (checkCapacity).
	FailureCapacity FailureKind = "capacity"
)

// Failure is an error of a known kind. Its message is the wrapped error's.
type Failure struct {
	Kind FailureKind
	Err  error
}

func (f *Failure) Error() string { return f.Err.Error() }
func (f *Failure) Unwrap() error { return f.Err }

// NewFailure marks e as a failure of kind; nil stays nil, FailureOther leaves e as it is.
func NewFailure(kind FailureKind, e error) error {
	if e == nil || kind == FailureOther {
		return e
	}
	return &Failure{kind, e}
}

// DeviceFailure marks e (an error of the card or the reader) as FailureDevice.
func DeviceFailure(e error) error { return NewFailure(FailureDevice, e) }

// Classify is the kind of e: the innermost marked one wins over the outer, so that
// a verify mismatch stays one even when restoring an update wrapped it.
func Classify(e error) FailureKind {
	var f *Failure
	kind := FailureOther
	for errors.As(e, &f) {
		kind = f.Kind
		e = f.Err
	}
	return kind
}

// The hints, in English: the apps translate each line.
const (
	hintIntro    = "If it keeps failing:"
	hintReader   = "Try a different card reader."
	hintComputer = "Try a different computer."
	hintAndroid  = "Some Android phones and tablets cannot write SD cards this way at all. If so, use a computer."
	hintFaulty   = "The card may be faulty or not its stated size."
)

// FailureHints are the lines to show after the error of a failure of kind, none for
// FailureOther. android is true in the Android app: a phone is not a computer, so
// instead of another computer it says that some phones can't do this at all.
func FailureHints(kind FailureKind, android bool) []string {
	var out []string
	switch kind {
	case FailureVerify:
		out = append(out, hintFaulty)
		fallthrough
	case FailureDevice, FailureCapacity:
		// a reader that can't address the whole card looks like a fake card too
		out = append(out, hintIntro, hintReader)
		if android {
			out = append(out, hintAndroid)
		} else {
			out = append(out, hintComputer)
		}
	}
	return out
}

// The messages of failures the apps show translated (i18n.Find reads them back out
// of an error's text).
var (
	// ErrFakeCapacity: the card claims more than it holds (checkCapacity).
	ErrFakeCapacity = i18n.NewFormat("This card is not the size it claims (it reports %s, but only about %s works). It cannot be used.")
	// ErrWriteProtected: the card refused the first write.
	ErrWriteProtected = i18n.NewFormat("This card is write-protected, so nothing was written. Slide the lock switch on the card or adapter, then try again.")
	// ErrStoppedMidWrite: the write ended (the disk helper died, was killed) after it
	// had begun to change the card.
	ErrStoppedMidWrite = i18n.NewFormat("The writing stopped before it finished. The card is incomplete and has to be written again before you use it.")
	// ErrGuestInTheWay: an update would put the new release's partitions where the
	// card's game system is.
	ErrGuestInTheWay = i18n.NewFormat("The new release needs room on the card that its game system uses, so this card can't be updated. Write it again with a new installation instead.")
	// ErrRestoreFailed marks an update whose automatic restore failed.
	ErrRestoreFailed = errors.New("automatic restore failed")
)

// The causes of failures that the write's own checks find (i18n.NewCause): they
// explain the shorter texts of these errors.
var (
	// CauseImageChanged: the image file is not what was checked when it was chosen.
	CauseImageChanged = i18n.NewCause("The image file changed after it was checked: it was moved or overwritten, or its download was still running. Choose the image again.",
		func(s string) bool { return strings.Contains(s, "source image changed") })
	// CauseVerify: the read-back of the card is not what was written (verify).
	CauseVerify = i18n.NewCause("What the card gave back is not what was written to it: the card or the card reader does not store data reliably.",
		func(s string) bool { return strings.Contains(s, "read-back mismatch") })
	// CauseGuestDamaged: the downloaded game system is not what its release lists.
	CauseGuestDamaged = i18n.NewCause("The downloaded game system is damaged. Start again: it is then downloaded once more.",
		func(s string) bool {
			return strings.Contains(s, "game system download is damaged") || strings.Contains(s, "game system download is not the size")
		})
)

// The causes of a card too small for what was chosen (the size checks in plan.go
// and prepared.go): they are found before the write, so the card is untouched.
var (
	// CauseNoRoomForGuest: Android fits, but not the game system beside it.
	CauseNoRoomForGuest = i18n.NewCause("This card has no room for the game system next to Android. Turn the game system off under Options, or use a larger card.",
		func(s string) bool {
			return strings.Contains(s, "too small for the game system") || strings.Contains(s, "too small for android and the game system")
		})
	// CauseCardTooSmall: the card can't hold andr36oid at all.
	CauseCardTooSmall = i18n.NewCause("This card is too small for andr36oid. It needs a card of at least 16 GB. Use a larger card.",
		func(s string) bool {
			return strings.Contains(s, "card is too small") || strings.Contains(s, "card is smaller than the release image")
		})
)

// What a failed write left the card like: the apps show it after the error's own
// message (i18n.NewState).
var (
	// StateIncomplete: an installation stopped after it had begun to write.
	StateIncomplete = i18n.NewState("Writing stopped. The card is incomplete and has to be written again before you use it.")
	// StateCardTest: a new installation stopped in the card tests (size and speed),
	// before anything of the release went to the card; only their test blocks did.
	StateCardTest = i18n.NewState("Nothing of andr36oid was written yet. Only the card test wrote a few small test blocks, so whatever was on the card before may be damaged in places.")
	// StateUntouched: the write stopped before its first byte went to the card.
	StateUntouched = i18n.NewState("Nothing was written to the card.")
	// StateRestored: a failed update put the card back as it was.
	StateRestored = i18n.NewState("The previous installation was restored and verified.")
	// StateRestoreFailed: a failed update could not put the card back; the argument
	// is the recovery backup's folder.
	StateRestoreFailed = i18n.NewState("The card could not be put back as it was before the update. Keep it out of the console, and put it back with \"Restore an interrupted update\" and the recovery backup (%s).")
)

// gigabytes is a card size as the apps show it.
func gigabytes(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/1e9) }

// DeviceErrors is d with every error of it marked as FailureDevice, for the card
// I/O around the write (reading the card first, the records after it).
func DeviceErrors(d Device) Device { return deviceErrors{d} }

// deviceErrors marks every error of the card it wraps as FailureDevice.
type deviceErrors struct{ Device }

func (d deviceErrors) ReadAt(b []byte, off int64) (int, error) {
	n, e := d.Device.ReadAt(b, off)
	if e == io.EOF {
		// callers compare with io.EOF itself
		return n, e
	}
	return n, DeviceFailure(e)
}

func (d deviceErrors) WriteAt(b []byte, off int64) (int, error) {
	n, e := d.Device.WriteAt(b, off)
	if e == nil && n != len(b) {
		e = io.ErrShortWrite
	}
	return n, DeviceFailure(e)
}

func (d deviceErrors) Sync() error { return DeviceFailure(flush(d.Device, hostOS)) }
