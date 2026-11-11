package ui

import (
	"context"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
)

// The dArkOSen box on the options page. It is only there for a new card with a
// release that runs dArkOSen from its own partition and is new enough for the
// dArkOSen release on offer (flash.GuestFits); the writer checks the same again.

func (s *screen) buildGuest() {
	s.guestCheck = widget.NewCheck("", func(bool) { s.ready() })
	s.guestInfo = wrapped("")
	s.guestBox = container.NewVBox(widget.NewSeparator(), heading(s.t("dArkOSen (Linux games system)")), s.guestCheck, s.guestInfo)
	s.guestBox.Hide()
}

// lookupGuest asks once per release image which dArkOSen release there is.
func (s *screen) lookupGuest() {
	if s.im == nil || !s.im.Subsystem() || s.guestFor == s.im.SHA256 {
		return
	}
	s.guestFor, s.guest = s.im.SHA256, nil
	want := s.im.SHA256
	go func() {
		g, e := catalog.LatestGuest(context.Background())
		fyne.Do(func() {
			if e != nil || g == nil || s.im == nil || s.im.SHA256 != want {
				return
			}
			s.guest = g
			s.ready()
		})
	}()
}

// guestOffer is the release to offer for the current choices, nil for none.
func (s *screen) guestOffer() *catalog.GuestRelease {
	if s.guest == nil || s.im == nil || s.updating {
		return nil
	}
	if flash.GuestFits(s.im, &flash.Guest{Minimum: s.guest.Minimum}) != nil {
		return nil
	}
	return s.guest
}

// guestBytes is the room dArkOSen takes on the card with the current choices.
func (s *screen) guestBytes() int64 {
	if g := s.guestOffer(); g != nil && s.guestCheck != nil && s.guestCheck.Checked {
		return g.PartitionBytes
	}
	return 0
}

func (s *screen) refreshGuest() {
	if s.guestBox == nil {
		return
	}
	g := s.guestOffer()
	if g == nil {
		s.guestBox.Hide()
		return
	}
	s.guestCheck.Text = s.t("Put dArkOSen %s on the card", g.Darkosen)
	s.guestCheck.Refresh()
	text := s.t("Download %s. It gets %s of its own at the end of the card and is ready on the console's first start.", humanBytes(g.DownloadBytes()), humanBytes(g.PartitionBytes))
	if s.drive != nil {
		if _, _, _, e := flash.PreparedSizes(s.im, s.drive.Size, s.userdataGiB, s.noROMs.Checked, g.PartitionBytes); e != nil {
			text = s.t("This card is too small for Android and dArkOSen together.")
			s.guestCheck.SetChecked(false)
			s.guestCheck.Disable()
		} else {
			s.guestCheck.Enable()
		}
	}
	s.guestInfo.SetText(text)
	s.guestBox.Show()
}

// withGuest downloads the chosen dArkOSen release, if any, and hands it to then.
func (s *screen) withGuest(then func(*flash.Guest)) {
	g := s.guestOffer()
	if g == nil || !s.guestCheck.Checked {
		then(nil)
		return
	}
	s.setBusy(true)
	s.progress.Show()
	s.progress.SetValue(0)
	go func() {
		dir, e := jobs.CacheDir()
		var path string
		if e == nil {
			path, e = catalog.DownloadGuest(context.Background(), g, dir, func(phase string, done, total int64) {
				fyne.Do(func() {
					if total > 0 {
						// the download is the first half of the bar, unpacking the second
						v := float64(done) / float64(total) / 2
						if phase == "unpack" {
							v += 0.5
						}
						s.progress.SetValue(v)
					}
				})
			})
		}
		fyne.Do(func() {
			s.progress.Hide()
			s.setBusy(false)
			if e != nil {
				s.showError(e)
				return
			}
			then(&flash.Guest{Path: path, Darkosen: g.Darkosen, Revision: g.Revision, Minimum: g.Minimum, Name: g.Image.Name, Size: g.Image.Size, SHA256: g.Image.SHA256, PartitionBytes: g.PartitionBytes})
		})
	}()
}
