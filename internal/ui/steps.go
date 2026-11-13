package ui

import (
	"fmt"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

// The setup is a row of steps. Each step is one page; Back and Next move
// between them, and the step buttons at the top jump to any step that is
// already reachable.
const (
	stepImage = iota
	stepCard
	stepConsole
	stepOptions
	stepWrite
	stepCount
)

var stepNames = [stepCount]string{"Release", "Card", "Console", "Options", "Write"}

func heading(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

// stepDone says whether a step has what the next steps need.
func (s *screen) stepDone(step int) bool {
	switch step {
	case stepImage:
		return s.im != nil
	case stepCard:
		return s.drive != nil && s.card != nil
	case stepConsole:
		return s.profile != ""
	case stepOptions:
		return !s.updating || (s.card != nil && s.card.Installed)
	}
	return false
}

// reachable: every step before it is done.
func (s *screen) reachable(step int) bool {
	for i := 0; i < step; i++ {
		if !s.stepDone(i) {
			return false
		}
	}
	return true
}

func (s *screen) showStep(step int) {
	if step < 0 || step >= stepCount || !s.reachable(step) {
		return
	}
	s.step = step
	if s.pages == nil {
		return
	}
	for i, p := range s.pages {
		if i == step {
			p.Show()
		} else {
			p.Hide()
		}
	}
	s.refreshSteps()
	if step == stepOptions && s.started && s.bootloaderOptions != nil {
		s.bootloaderOptions.load()
	}
	if step == stepWrite {
		s.summarize()
	}
}

// refreshSteps updates the step bar, Back/Next and the write button.
func (s *screen) refreshSteps() {
	if s.stepButtons[0] == nil {
		return
	}
	// a step that lost what it needs (card removed, new release) sends the user back
	if !s.busy && !s.reachable(s.step) {
		for s.step > 0 && !s.reachable(s.step) {
			s.step--
		}
		s.showStep(s.step)
		return
	}
	for i, b := range s.stepButtons {
		b.SetText(fmt.Sprintf("%d. %s", i+1, s.t(stepNames[i])))
		switch {
		case i == s.step:
			b.Importance = widget.HighImportance
		default:
			b.Importance = widget.MediumImportance
		}
		if s.busy || !s.reachable(i) {
			b.Disable()
		} else {
			b.Enable()
		}
		b.Refresh()
	}
	if s.step == 0 || s.busy {
		s.back.Disable()
	} else {
		s.back.Enable()
	}
	if s.step == stepWrite {
		s.next.Hide()
	} else {
		s.next.Show()
		if !s.busy && s.stepDone(s.step) {
			s.next.Enable()
		} else {
			s.next.Disable()
		}
	}
}

func (s *screen) profileName() string {
	if s.im != nil {
		for _, p := range s.im.Profiles {
			if p.ID == s.profile {
				return p.Name
			}
		}
	}
	return s.profile
}

func (s *screen) dtbText() string {
	switch {
	case s.convert:
		return s.t(ownDTBLabel, filepath.Base(s.custom))
	case s.custom != "":
		return s.t("Custom Android DTB") + ": " + filepath.Base(s.custom)
	}
	return s.t("Use the DTB shipped with the selected profile.")
}

// summarize fills the last page: what will be written where.
func (s *screen) summarize() {
	if s.sumValues[0] == nil {
		return
	}
	set := func(i int, v string) { s.sumValues[i].SetText(v) }
	set(0, "")
	if s.im != nil {
		set(0, s.im.Version+"\n"+filepath.Base(s.im.Source))
	}
	set(1, "")
	if s.drive != nil {
		set(1, s.drive.Label())
	}
	set(2, s.profileName())
	set(3, s.dtbText())
	if s.bootloaderOptions != nil {
		if c := s.bootloaderOptions.selection(); c != nil {
			if c.Changed() {
				set(6, s.bootloaderOptions.summary.Text)
			} else if c.Build != nil {
				set(6, s.bootloaderSummary(&firmware.BootloaderRecord{Choice: c.Choice, Version: c.Build.Version, Tag: c.Build.Tag}))
			} else {
				set(6, s.t("Bootloader: as in the image"))
			}
		} else {
			set(6, s.t(firmware.BootloaderOnlySD))
		}
	}
	if s.updating {
		set(4, s.t("Update an existing installation"))
		if s.system != "" {
			set(4, s.sumValues[4].Text+" · "+s.systemText())
		}
		set(5, s.t("Kept as it is"))
	} else {
		set(4, s.t("Install a new card (erase)"))
		if s.system != "" {
			set(4, s.sumValues[4].Text+" · "+s.systemText())
		}
		set(5, s.storageText())
	}
	if s.updating {
		s.action.SetText(s.t("Update this card…"))
		s.help.SetText(s.t("BOOT, system, and vendor will be updated together. The dedicated cache will be cleared. Userdata and games stay in place. A recovery backup will be saved before writing."))
		s.help.Importance = widget.MediumImportance
		s.action.Importance = widget.HighImportance
	} else {
		s.action.SetText(s.t("Erase and write this card…"))
		s.help.SetText(s.t("Everything currently on this card will be erased."))
		s.help.Importance = widget.DangerImportance
		s.action.Importance = widget.DangerImportance
	}
	s.help.Refresh()
	s.action.Refresh()
}

var summaryKeys = [len(screen{}.sumValues)]string{"Release", "microSD card", "Device profile", "DTB", "Action", "Storage", "Boot options"}

func (s *screen) summaryForm() *widget.Form {
	f := widget.NewForm()
	for i, k := range summaryKeys {
		s.sumValues[i] = wrapped("")
		f.Append(s.t(k), s.sumValues[i])
	}
	return f
}

// pagesFor builds the five step pages. The widgets are made in build().
func (s *screen) pagesFor(advanced fyne.CanvasObject) []*fyne.Container {
	unknown := widget.NewButton(s.t("I don’t know my board or panel"), func() {
		s.info(s.t("Finding your board and panel"), s.t("An existing andr36oid card may identify its selected profile. For a new card, use your console’s board markings and the panel information from its supplier. Similar-looking R36S consoles can have different wiring. Choose the board family first; a panel number alone is not enough. If your console is missing here, this release does not ship its profile."), s.win)
	})
	own := widget.NewButton(s.t(ownDTBButton), s.chooseOwnDTB)
	pages := []*fyne.Container{
		container.NewVBox(
			heading(s.t("Choose the andr36oid release")),
			wrapped(s.t("Download a release, or choose an image file you already have. Only Treble images can be installed.")),
			container.NewGridWithColumns(2, s.online, s.local),
			s.releaseLabel, s.progress,
		),
		container.NewVBox(
			heading(s.t("Choose the microSD card")),
			wrapped(s.t("The card is only read now. Nothing is written before the last step.")),
			container.NewBorder(nil, nil, nil, s.refresh, s.driveSelect),
			s.cardLabel,
			s.allCheck,
		),
		container.NewVBox(
			heading(s.t("Choose your console")),
			wrapped(s.t("Choose the board family first, then the board and screen.")),
			s.console, s.profileSelect, unknown,
			s.imageDTBSection(),
			widget.NewSeparator(),
			heading(s.t("DTB")),
			s.customLabel,
			own,
			advanced,
		),
		container.NewVBox(
			heading(s.t("What should happen to the card?")),
			s.mode,
			s.modeHelp,
			widget.NewSeparator(),
			heading(s.t("Storage")),
			s.noROMs,
			wrapped(s.t("Unchecked: choose below how much of the card Android gets. The rest becomes a games partition readable on your computer. Updates keep the existing arrangement.")),
			s.storage,
			s.guestBox,
			widget.NewSeparator(),
			s.systemSection(),
			widget.NewSeparator(),
			s.bootloaderSection(),
		),
		container.NewVBox(
			heading(s.t("Check and write")),
			s.summaryForm(),
			widget.NewSeparator(),
			s.help,
			s.action,
		),
	}
	return pages
}
