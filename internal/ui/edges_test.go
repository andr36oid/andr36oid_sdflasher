package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// The download failures of the game system and the card sizes found before the
// write each say what to do, not the general line about the image and the card.
func TestWhatToDoDownloadsAndSizes(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	general := s.t("Check the selected image and card, then try again.")
	for name, c := range map[string]struct {
		e    error
		want string
	}{
		"rate limit":  {i18n.Detailed(catalog.ErrRateLimited.New(), errors.New("HTTP 403")), "Wait about an hour, or connect this computer to a different network, then choose Try again."},
		"gone":        {catalog.ErrNotThere.New(), "Go back to setup, open the release list again and choose the release there, then write the card."},
		"stalled":     {fmt.Errorf("%w; %w", catalog.ErrStalled.New(), flash.StateUntouched.New()), "Check the internet connection, then choose Try again. If it keeps failing, try again later or use a different network."},
		"server":      {catalog.ErrServer.New(), "Check the internet connection, then choose Try again. If it keeps failing, try again later or use a different network."},
		"checksum":    {catalog.ErrDamagedDownload.New(), "Download the release again (Back to setup, then Release), or choose the image file again, then write the card."},
		"no room":     {errors.New("this card is too small for the game system"), "Go back to Options and turn the game system off, or use a larger card."},
		"no room too": {errors.New("this card is too small for Android and the game system together"), "Go back to Options and turn the game system off, or use a larger card."},
		"tiny":        {flash.TooSmall(8e9), "Use a larger card."},
	} {
		got := s.whatToDo(c.e)
		if got == general || got != c.want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

// The size checks before the write have a plain message of their own in What
// happened (i18n causes), and the card is untouched.
func TestTooSmallHasAMessage(t *testing.T) {
	for raw, want := range map[string]*i18n.Format{
		"this card is too small for the game system":                      flash.CauseNoRoomForGuest,
		"this card is too small for Android and the game system together": flash.CauseNoRoomForGuest,
		"the card is too small":                                           flash.CauseCardTooSmall,
	} {
		if key, _, ok := i18n.Find(errors.New(raw)); !ok || key != want.Key() {
			t.Errorf("%q: %q", raw, key)
		}
	}
	if key, _, _ := i18n.Find(flash.TooSmall(8e9)); key != flash.CauseCardTooSmall.Key() {
		t.Errorf("TooSmall: %q", key)
	}
}

// An error that says only what the card is like keeps the general advice before
// it, so that the dialog never shows just "Nothing was written to the card."
func TestExplainStateOnly(t *testing.T) {
	s := fixScreen(t)
	e := fmt.Errorf("%w; %w", errors.New("something only a developer knows"), flash.StateUntouched.New())
	if got := s.explain(e); got != "Check the selected image and card, then try again.\n\n"+flash.StateUntouched.Key() {
		t.Fatal(got)
	}
	// a card that could not be inspected: what is known first, then that it is untouched
	e = fmt.Errorf("%w; %w", i18n.Detailed(i18n.CauseHelperStopped.New(), errors.New("exit status 126")), flash.StateUntouched.New())
	if got := s.explain(e); got != i18n.CauseHelperStopped.Key()+"\n\n"+flash.StateUntouched.Key() {
		t.Fatal(got)
	}
}

// A listing that failed says what is known, or the way on, and keeps the error.
func TestListErrorIsPlain(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	got := s.listError(errors.New("exec: lsblk: permission denied"))
	if !strings.Contains(got, i18n.CausePermission.Key()) || !strings.Contains(got, "permission denied") {
		t.Fatal(got)
	}
	got = s.listError(errors.New("bad json"))
	if !strings.Contains(got, "Choose Refresh to look again") || !strings.HasSuffix(got, "Technical details: bad json") {
		t.Fatal(got)
	}
}

// A failed kernel check says what happened and that the card is untouched; a
// failed kernel update says what to do with the card.
func TestKernelErrorIsPlain(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	head := s.t("Could not check for kernel updates.")
	rate := catalog.ErrRateLimited.New()
	got := s.kernelError(head, rate, false)
	if !strings.Contains(got, rate.Error()) || !strings.Contains(got, flash.StateUntouched.Key()) {
		t.Fatal(got)
	}
	got = s.kernelError(head, errors.New("dial tcp: lookup example.test: no such host"), false)
	if !strings.Contains(got, i18n.CauseOffline.Key()) {
		t.Fatal(got)
	}
	// a key of its own is translated, not shown twice as raw text
	got = s.kernelError(head, errors.New(firmware.KernelChanged), false)
	if !strings.Contains(got, s.t(firmware.KernelChanged)) {
		t.Fatal(got)
	}
	got = s.kernelError(s.t("Kernel update could not finish."), errors.New("write /dev/sdb: input/output error"), true)
	if !strings.Contains(got, i18n.CauseDeviceGone.Key()) || !strings.Contains(got, "Keep the card out of the console") || strings.Contains(got, flash.StateUntouched.Key()) {
		t.Fatal(got)
	}
}

// A game system whose lookup failed says why when it is known (a rate limit here).
func TestGuestMissingSaysWhy(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Size: 128e9, Name: "Card Reader"}
	s.card = &flash.Card{}
	s.guestChoice, s.guestChosen = catalog.DefaultGuest, true
	s.guests[catalog.DefaultGuest] = &guestLookup{done: true, err: i18n.Detailed(catalog.ErrRateLimited.New(), errors.New("HTTP 403"))}
	s.ready()
	s.refreshGuest()
	if got := s.guestInfo.Text; !strings.Contains(got, "can't be downloaded right now") || !strings.Contains(got, catalog.ErrRateLimited.Key()) {
		t.Fatal(got)
	}
}
