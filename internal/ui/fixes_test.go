package ui

import (
	"errors"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

func fixScreen(t *testing.T) *screen {
	a := test.NewApp()
	t.Cleanup(a.Quit)
	w := a.NewWindow("test")
	t.Cleanup(w.Close)
	s := &screen{win: w, locale: "en", im: &firmware.Image{SHA256: "x", Facts: map[string]string{"layout": "prepared-1", "subsystem": "release-1"}, Profiles: []firmware.Profile{{ID: "Panels/Panel4", Name: "R36S — Panel 4", Console: "R36S"}}}}
	// no lookups on the network: the tests set what they found
	s.guestFor, s.guests = "x", map[string]*guestLookup{}
	s.build()
	return s
}

// A write-protected card is in the list with the reason, and choosing it never
// inspects (or writes) it.
func TestWriteProtectedCardListed(t *testing.T) {
	s := fixScreen(t)
	s.setDrives([]platform.Drive{{Path: "/dev/sdx", Name: "Card Reader", Size: 32e9, WriteProtected: true}})
	label := s.driveLabel(s.drives[0])
	if !strings.Contains(label, "write-protected: slide the lock switch on the card or adapter") || len(s.cardRows.Objects) != 1 {
		t.Fatal(label, len(s.cardRows.Objects))
	}
	row := s.cardRows.Objects[0].(*choiceCard)
	if !row.disabled || !strings.Contains(row.detail.Text, "lock switch") {
		t.Fatalf("row %v %q", row.disabled, row.detail.Text)
	}
	row.Tapped(nil)
	s.pickDrive(s.drives[0])
	if s.drive != nil || s.busy {
		t.Fatalf("drive %v busy %v", s.drive, s.busy)
	}
}

// Each reason a game system can't be chosen is said as it is.
func TestGuestReasons(t *testing.T) {
	s := fixScreen(t)
	s.guestFor = "x"
	s.guests = map[string]*guestLookup{
		"dArkOS":   {done: true},
		"dArkOSRE": {done: true, err: catalog.ErrGuestNeedsNewerFlasher},
		"dArkOSen": {done: true, err: errors.New("GitHub returned 502")},
	}
	s.guestChosen = true
	s.refreshGuest()
	got := strings.Join(s.guestRadio.Options, "|")
	for _, want := range []string{"dArkOS - no release yet", "dArkOSRE - needs a newer flasher", "dArkOSen - not available right now"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q not in %q", want, got)
		}
	}
	for id, want := range map[string]string{"dArkOS": "no release yet", "dArkOSRE": "newer version of this flasher", "dArkOSen": "can't be downloaded right now"} {
		s.guestChoice = id
		s.refreshGuest()
		if !strings.Contains(s.guestInfo.Text, want) {
			t.Errorf("%s: %q", id, s.guestInfo.Text)
		}
	}
}

// An update of a card with a game system says which one is there and that it stays.
func TestUpdateShowsKeptGuest(t *testing.T) {
	s := fixScreen(t)
	s.card = &flash.Card{Installed: true, Guest: &flash.CardGuest{Distribution: "dArkOS", Version: "11072026"}}
	s.setTask(taskUpdate)
	if !s.guestBox.Visible() || s.guestCheckRow.Visible() || s.guestInfo.Text != "This card has dArkOS (11072026) on it. The update keeps it as it is." {
		t.Fatalf("box %v radio %v %q", s.guestBox.Visible(), s.guestRadio.Visible(), s.guestInfo.Text)
	}
	s.card.Guest = nil
	s.ready()
	if s.guestBox.Visible() {
		t.Fatal("game system box on an update of a card without one")
	}
}

// A failure with a message for the user shows it translated, not the general advice.
func TestExplainedError(t *testing.T) {
	s := fixScreen(t)
	s.locale = "en"
	e := flash.NewFailure(flash.FailureCapacity, errors.New(flash.ErrFakeCapacity.New("31.9 GB", "8.6 GB").Error()))
	if got := s.explain(e); !strings.HasPrefix(got, "This card is not the size it claims") {
		t.Fatal(got)
	}
	// a bare system error is explained by its cause; only an unknown one gets the
	// general advice
	if got := s.explain(errors.New("input/output error")); got != i18n.CauseDeviceGone.Key() {
		t.Fatal(got)
	}
	if got := s.explain(errors.New("something only a developer knows")); got != "Check the selected image and card, then try again." {
		t.Fatal(got)
	}
	if h := s.failureHints(e); !strings.Contains(h, "Try a different computer.") || !strings.Contains(h, "Try a different card reader.") {
		t.Fatal(h)
	}
}

// A drive above 256 GB or a USB hard drive is large; a "256 GB" card is not.
func TestLargeDrive(t *testing.T) {
	if (platform.Drive{Size: 250e9}).Large() || !(platform.Drive{Size: 512e9}).Large() || !(platform.Drive{Size: 64e9, External: true}).Large() {
		t.Fatal("large drive rule")
	}
}
