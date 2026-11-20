package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
)

// What runs while the window is busy, so that closing it (or Stop) names the step
// that really runs.
const (
	actNone          = iota
	actDownload      // a release image is downloaded
	actCheckImage    // a release image (.img) is checked
	actUnpackImage   // a packed release image is unpacked and checked
	actGuestDownload // the game system is downloaded
	actGuestUnpack   // the game system is unpacked and checked
	actWriteCheck    // the write checks the image and the card first
	actBackup        // an update saves its recovery backup
	actWrite         // the card is written
	actGuestInstall  // the game system's partition is written
	actVerify        // the card is read back
	actBoot          // boot options are written
	actReadCard      // the card is only read
)

// stageActivity is what a stage of the working page does.
var stageActivity = map[int]int{
	stageDownload: actGuestDownload,
	stageCheck:    actWriteCheck,
	stageBackup:   actBackup,
	stageWrite:    actWrite,
	stageVerify:   actVerify,
}

// imageActivity: unpacking for a packed release, checking for a plain .img.
func imageActivity(path string) int {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xz", ".gz", ".zip":
		return actUnpackImage
	}
	return actCheckImage
}

// closePrompt is what closing the window says while something runs: a title, the
// text, and whether it can be stopped (nothing is written then) so that the
// window closes after it.
func (s *screen) closePrompt() (title, body string, stoppable bool) {
	stoppable = s.cancel != nil
	switch s.activity {
	case actDownload:
		return s.t("Stop the download?"), s.t("The download stops and the window closes. Nothing was written to the card."), stoppable
	case actCheckImage:
		return s.t("Stop checking the image?"), s.t("Checking the image stops and the window closes. Nothing was written to the card."), stoppable
	case actUnpackImage:
		return s.t("Stop unpacking the image?"), s.t("Unpacking stops and the window closes. Nothing was written to the card."), stoppable
	case actGuestDownload:
		return s.t("Stop downloading the game system?"), s.t("The download stops and the window closes. Nothing was written to the card."), stoppable
	case actGuestUnpack:
		return s.t("Stop unpacking the game system?"), s.t("Unpacking stops and the window closes. Nothing was written to the card."), stoppable
	case actWriteCheck:
		return s.t("The image is being checked"), s.t("The flasher is checking the image and the card before it writes. Nothing was written to the card yet. Wait until it is finished, or choose Stop first, then close the window."), false
	case actBackup:
		return s.t("A backup is being saved"), s.t("The flasher is saving a backup of the card before it updates it. Nothing on the card was changed yet. Wait until it is finished, or choose Stop first, then close the window."), false
	case actWrite:
		return s.t("The card is being written"), s.t(closeWhileWriting), false
	case actGuestInstall:
		return s.t("The game system is being installed"), s.t("The flasher is writing the game system to the card. Closing it now would leave the card half-written, and it would not start in a console. Wait until it is finished. To end it early, choose Stop first: the flasher then finishes the block in progress and says what the card is like."), false
	case actVerify:
		return s.t("The card is being verified"), s.t("The card is written and is now read back to check it. Closing the flasher now would leave a card that was never checked. Wait until the check is finished, or choose Stop first."), false
	case actBoot:
		return s.t("Boot options are being applied"), s.t("The flasher is writing the bootloader to the card. Closing it now could leave a card that does not start. Wait until it is finished, and keep the card connected."), false
	case actReadCard:
		return s.t("The card is being read"), s.t("The flasher is reading the card. Nothing is written to it. Wait a moment, then close the window."), false
	}
	if stoppable {
		// something that may be stopped, not named here: nothing is written then
		return s.t("Stop the download?"), s.t("The download stops and the window closes. Nothing was written to the card."), true
	}
	return s.t("Operation in progress"), s.t("Wait for the operation to finish before closing. Keep the card connected."), false
}

// closeRequested is the window's close button: while something runs, it says what
// and offers to stop what can be stopped; otherwise the window closes.
func (s *screen) closeRequested() {
	if s.busy {
		title, body, stoppable := s.closePrompt()
		if stoppable {
			s.confirmDialog(title, body, func(ok bool) {
				if ok && s.cancel != nil {
					s.closeAfter = true
					s.cancel()
				}
			}, s.win)
			return
		}
		s.info(title, body, s.win)
		return
	}
	if s.updateCancel != nil {
		s.updateCancel()
	}
	s.stopGuestLookup()
	s.win.Close()
}

// keepStaged keeps the unpacked image of the chosen release from looking stale:
// a second flasher started later clears cache files untouched for a while
// (firmware.CleanWorkDir), and this one may still write from it.
func (s *screen) keepStaged() {
	if s.im == nil || s.im.Path == "" {
		return
	}
	dir, e := jobs.CacheDir()
	if e != nil || filepath.Dir(s.im.Path) != dir || !strings.HasPrefix(filepath.Base(s.im.Path), "image-") {
		return
	}
	now := time.Now()
	os.Chtimes(s.im.Path, now, now)
}
