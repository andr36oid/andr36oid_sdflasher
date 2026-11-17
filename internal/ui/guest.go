package ui

import (
	"context"
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
)

// The game system box on the options page: one choice of dArkOS (preselected),
// dArkOSRE, dArkOSen or none. One system can go on the card's own partition when it
// is written; more are added later on the console. The box is only there for a new
// card with a release that runs a system from its own partition; each system says
// why it can't be chosen (still looking, not available, release too old, card too
// small), and the writer checks the same again (flash.GuestFits).

// guestLookup is what is known about one source for the loaded release.
type guestLookup struct {
	done bool
	g    *catalog.GuestRelease // nil when done: none published or the lookup failed
	err  error                 // why the lookup failed
}

// guestOptions are the choices in their order: the sources, then none.
func guestOptions() []string {
	var ids []string
	for _, s := range catalog.GuestSources {
		ids = append(ids, s.ID)
	}
	return append(ids, catalog.NoGuest)
}

func (s *screen) buildGuest() {
	s.guestRadio = widget.NewRadioGroup(nil, func(label string) {
		if s.guestUpdating {
			return
		}
		for i, l := range s.guestLabels {
			if l == label {
				s.guestChoice, s.guestChosen = guestOptions()[i], true
			}
		}
		s.ready()
	})
	s.guestRadio.Required = true
	s.guestChoice = catalog.DefaultGuest
	s.guestInfo = wrapped("")
	s.guestIntro = wrapped(s.t("Choose one system to put on the card now. You can add more later on the console."))
	s.guestBox = container.NewVBox(widget.NewSeparator(), heading(s.t("Game system")),
		s.guestIntro, s.guestRadio, s.guestInfo)
	s.guestBox.Hide()
}

// lookupGuest asks once per release image for the latest release of every source.
func (s *screen) lookupGuest() {
	if s.im == nil || !s.im.Subsystem() || s.guestFor == s.im.SHA256 {
		return
	}
	s.guestFor, s.guests = s.im.SHA256, map[string]*guestLookup{}
	if !s.guestChosen {
		s.guestChoice = catalog.DefaultGuest
	}
	// the lookups for an earlier release, or of a window that closes, stop
	s.stopGuestLookup()
	ctx, cancel := context.WithCancel(context.Background())
	s.guestLookupCancel = cancel
	want := s.im.SHA256
	for _, src := range catalog.GuestSources {
		src := src
		s.guests[src.ID] = &guestLookup{}
		go func() {
			g, e := catalog.LatestGuest(ctx, src)
			if e != nil {
				g = nil
			}
			fyne.Do(func() {
				if s.im == nil || s.im.SHA256 != want || s.guestFor != want {
					return
				}
				s.guests[src.ID] = &guestLookup{done: true, g: g, err: e}
				s.ready()
			})
		}()
	}
}

// stopGuestLookup ends the game system lookups that are still running.
func (s *screen) stopGuestLookup() {
	if s.guestLookupCancel != nil {
		s.guestLookupCancel()
		s.guestLookupCancel = nil
	}
}

// guest states, for the label and the text under the choice
const (
	guestLooking = iota
	guestNone    // no release published yet
	guestMissing // the lookup failed: no connection, GitHub, a damaged release
	guestFlasher // the release needs a newer flasher
	guestTooOld  // the chosen andr36oid release can't run it
	guestTooBig  // it doesn't fit the card next to Android
	guestReady
)

// guestState is how the system id stands for the current choices, with its release
// when there is one.
func (s *screen) guestState(id string) (int, *catalog.GuestRelease) {
	l := s.guests[id]
	if l == nil || !l.done {
		return guestLooking, nil
	}
	if l.g == nil {
		switch {
		case errors.Is(l.err, catalog.ErrGuestNeedsNewerFlasher):
			return guestFlasher, nil
		case l.err == nil:
			return guestNone, nil
		}
		return guestMissing, nil
	}
	if e := flash.GuestFits(s.im, &flash.Guest{Distribution: l.g.Distribution, Minimum: l.g.Minimum}); e != nil {
		if errors.Is(e, flash.ErrGuestNeedsNewerRelease) {
			return guestTooOld, l.g
		}
		// a minimum in a form this flasher doesn't read
		return guestFlasher, l.g
	}
	if s.drive != nil {
		if _, _, _, e := flash.PreparedSizes(s.im, s.drive.Size, s.userdataGiB, s.noROMs.Checked, l.g.PartitionBytes); e != nil {
			return guestTooBig, l.g
		}
	}
	return guestReady, l.g
}

// guestShown: the box is there for these choices.
func (s *screen) guestShown() bool {
	return s.im != nil && s.im.Subsystem() && !s.updating && s.guestBox != nil
}

// guestOffer is the chosen release when it can go on the card, nil otherwise.
func (s *screen) guestOffer() *catalog.GuestRelease {
	if !s.guestShown() || s.guestChoice == catalog.NoGuest {
		return nil
	}
	if st, g := s.guestState(s.guestChoice); st == guestReady {
		return g
	}
	return nil
}

// guestBlocked: a system is chosen that can't go on the card (yet), so the card is
// not written until the user chooses another one or none.
func (s *screen) guestBlocked() bool {
	return s.guestShown() && s.guestChoice != catalog.NoGuest && s.guestOffer() == nil
}

// guestBytes is the room the game system takes on the card with the current choices.
func (s *screen) guestBytes() int64 {
	if g := s.guestOffer(); g != nil {
		return g.PartitionBytes
	}
	return 0
}

// guestDefault moves the preselection, while the user hasn't chosen, to the first
// system in the list that can go on this card: dArkOS when it can. While one before
// it is still being looked up, that one stays selected. None when no system can.
func (s *screen) guestDefault() {
	if s.guestChosen {
		return
	}
	s.guestChoice = catalog.NoGuest
	for _, src := range catalog.GuestSources {
		if st, _ := s.guestState(src.ID); st == guestLooking || st == guestReady {
			s.guestChoice = src.ID
			return
		}
	}
}

// guestKept is what an update says of the game system on the card, "" when the
// card has none.
func (s *screen) guestKept() string {
	if s.card == nil || s.card.Guest == nil {
		return ""
	}
	switch g := s.card.Guest; {
	case g.Distribution == "":
		return s.t("This card has a game system on it. The update keeps it as it is.")
	case g.Version == "":
		return s.t("This card has %s on it. The update keeps it as it is.", g.Distribution)
	default:
		return s.t("This card has %s (%s) on it. The update keeps it as it is.", g.Distribution, g.Version)
	}
}

func (s *screen) refreshGuest() {
	if s.guestBox == nil {
		return
	}
	// an update keeps the system that is on the card; the choice is for new cards
	if s.updating && s.guestKept() != "" {
		s.guestRadio.Hide()
		s.guestIntro.Hide()
		s.guestInfo.SetText(s.guestKept())
		s.guestBox.Show()
		return
	}
	s.guestRadio.Show()
	s.guestIntro.Show()
	if !s.guestShown() {
		s.guestBox.Hide()
		return
	}
	s.guestDefault()
	var labels []string
	for _, id := range guestOptions() {
		if id == catalog.NoGuest {
			labels = append(labels, s.t("None"))
			continue
		}
		src, _ := catalog.FindGuestSource(id)
		st, g := s.guestState(id)
		label := src.Display
		switch {
		case st == guestTooOld:
			label += " · " + s.t("needs a newer andr36oid release")
		case st == guestNone:
			label += " · " + s.t("no release yet")
		case st == guestFlasher:
			label += " · " + s.t("needs a newer flasher")
		case st == guestMissing:
			label += " · " + s.t("not available right now")
		case g != nil:
			label += " · " + g.Darkosen + " · " + humanBytes(g.DownloadBytes())
		}
		labels = append(labels, label)
	}
	s.guestUpdating = true
	s.guestLabels = labels
	s.guestRadio.Options = labels
	for i, id := range guestOptions() {
		if id == s.guestChoice {
			s.guestRadio.Selected = labels[i]
		}
	}
	s.guestRadio.Refresh()
	s.guestUpdating = false
	text := s.t("No system is put on the card. You can add one later on the console.")
	if s.guestChoice != catalog.NoGuest {
		src, _ := catalog.FindGuestSource(s.guestChoice)
		switch st, g := s.guestState(s.guestChoice); st {
		case guestLooking:
			text = s.t("Looking for the newest version…")
		case guestMissing:
			text = s.t("This system can't be downloaded right now. Choose another one, or none.")
		case guestNone:
			text = s.t("This system has no release yet. Choose another one, or none.")
		case guestFlasher:
			text = s.t("This system's newest release needs a newer version of this flasher. Update the flasher, or choose another system or none.")
		case guestTooOld:
			text = s.t("This system needs a newer andr36oid release than the one you chose. Choose another system or none, or use a newer release.")
		case guestTooBig:
			text = s.t("This card is too small for Android and %s together.", src.Display)
		default:
			text = s.t("Download %s. It gets %s of its own at the end of the card and is ready on the console's first start.", humanBytes(g.DownloadBytes()), humanBytes(g.PartitionBytes))
		}
	}
	s.guestInfo.SetText(text)
	s.guestBox.Show()
}

// withGuest downloads the chosen system, if any, and hands it to then.
func (s *screen) withGuest(then func(*flash.Guest)) {
	g := s.guestOffer()
	if g == nil {
		then(nil)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.setBusy(true)
	src, _ := catalog.FindGuestSource(g.Distribution)
	s.guestStatus.SetText(s.t("Downloading %s…", src.Display))
	s.guestBar.SetValue(0)
	s.guestStop.Enable()
	s.guestWork.Show()
	go func() {
		dir, e := jobs.CacheDir()
		var path string
		if e == nil {
			path, e = catalog.DownloadGuest(ctx, g, dir, func(phase string, done, total int64) {
				fyne.Do(func() {
					if total > 0 {
						// the download is the first half of the bar, unpacking the second
						v := float64(done) / float64(total) / 2
						if phase == "unpack" {
							v += 0.5
						}
						s.guestBar.SetValue(v)
					}
				})
			})
		}
		fyne.Do(func() {
			stopped := ctx.Err() != nil
			cancel()
			s.cancel = nil
			s.guestWork.Hide()
			s.setBusy(false)
			if s.closeAfter {
				s.win.Close()
				return
			}
			if stopped {
				s.info(s.t("Download stopped"), s.t("The game system was not downloaded. Nothing was written to the card."), s.win)
				return
			}
			if e != nil {
				s.showError(e)
				return
			}
			then(flashGuest(g, path))
		})
	}()
}

// buildGuestWork is the game system download on the write page, with its Stop.
func (s *screen) buildGuestWork() *fyne.Container {
	s.guestStatus = wrapped("")
	s.guestBar = widget.NewProgressBar()
	s.guestStop = widget.NewButton(s.t("Stop"), func() {
		if s.cancel != nil {
			s.guestStop.Disable()
			s.cancel()
		}
	})
	s.guestWork = container.NewVBox(s.guestStatus, container.NewBorder(nil, nil, nil, s.guestStop, s.guestBar))
	s.guestWork.Hide()
	return s.guestWork
}

// flashGuest is what the writer needs of a downloaded release.
func flashGuest(g *catalog.GuestRelease, path string) *flash.Guest {
	return &flash.Guest{Path: path, Distribution: g.Distribution, Darkosen: g.Darkosen, Revision: g.Revision, Minimum: g.Minimum,
		Name: g.Image.Name, Size: g.Image.Size, SHA256: g.Image.SHA256, PartitionBytes: g.PartitionBytes}
}
