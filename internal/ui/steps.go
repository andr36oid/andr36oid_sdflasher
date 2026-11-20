package ui

import (
	"image/color"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// The setup is a row of steps, listed in the side bar. Each step is one page;
// Back and Next move between them, and the side bar jumps back to any step that
// is already reachable. Nothing is written before the last one.
const (
	stepStart = iota
	stepImage
	stepCard
	stepConsole
	stepOptions
	stepWrite
	stepCount
)

var stepNames = [stepCount]string{"Start", "Release", "Card", "Console", "Options", "Write"}

// what the user wants to do, chosen on the first page
const (
	taskNew = iota
	taskUpdate
	taskFix
)

func heading(text string) *widget.Label {
	l := wrapped(text)
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

// stepDone says whether a step has what the next steps need.
func (s *screen) stepDone(step int) bool {
	switch step {
	case stepStart:
		return s.task != taskFix
	case stepImage:
		return s.im != nil && s.imageOK()
	case stepCard:
		return s.drive != nil && s.card != nil && s.cardProblem() == ""
	case stepConsole:
		return s.profileOK()
	case stepOptions:
		return !s.guestBlocked()
	}
	return false
}

// order is the steps in the order they come: an update reads the card first, so
// that the Release page can offer the image of the kind the card runs.
func (s *screen) order() [stepCount]int {
	if s.task == taskUpdate {
		return [stepCount]int{stepStart, stepCard, stepImage, stepConsole, stepOptions, stepWrite}
	}
	return [stepCount]int{stepStart, stepImage, stepCard, stepConsole, stepOptions, stepWrite}
}

// position is where a step comes in order().
func (s *screen) position(step int) int {
	for i, x := range s.order() {
		if x == step {
			return i
		}
	}
	return 0
}

// after and before are the steps that come after and before one, or -1.
func (s *screen) after(step int) int {
	if i := s.position(step) + 1; i < stepCount {
		return s.order()[i]
	}
	return -1
}

func (s *screen) before(step int) int {
	if i := s.position(step) - 1; i >= 0 {
		return s.order()[i]
	}
	return -1
}

// reachable: every step before it is done.
func (s *screen) reachable(step int) bool {
	o := s.order()
	for i := 0; i < s.position(step); i++ {
		if !s.stepDone(o[i]) {
			return false
		}
	}
	return true
}

// cardProblem is why the chosen card can't be used for the chosen task, "" if it can.
func (s *screen) cardProblem() string {
	switch {
	case s.drive == nil || s.card == nil:
		return ""
	case s.drive.WriteProtected:
		return s.t(platform.WriteProtectedReason)
	case s.updating && !s.card.Installed:
		return s.t("This card has no andr36oid to update.")
	case s.tooSmall():
		return s.tooSmallText()
	}
	return ""
}

// profileOK: a profile of the release is chosen and, for an experimental one, its
// warning was agreed to.
func (s *screen) profileOK() bool {
	p := s.chosenProfile()
	return p != nil && (!p.Experimental || s.experimentalOK[p.ID])
}

func (s *screen) chosenProfile() *firmware.Profile {
	if s.im == nil || s.profile == "" {
		return nil
	}
	for i, p := range s.im.Profiles {
		if p.ID == s.profile {
			return &s.im.Profiles[i]
		}
	}
	return nil
}

// show puts a page on the right side of the window.
func (s *screen) show(page fyne.CanvasObject) {
	if s.body == nil {
		return
	}
	s.body.Objects = []fyne.CanvasObject{page}
	s.body.Refresh()
}

// current is the page shown now, to come back to after a short flow.
func (s *screen) current() fyne.CanvasObject {
	if s.body == nil || len(s.body.Objects) == 0 {
		return nil
	}
	return s.body.Objects[0]
}

// leave shows a page that is not one of the steps (a fix flow, the working page,
// a result); the side bar keeps showing where the setup stands.
func (s *screen) leave(page fyne.CanvasObject) {
	s.inSteps = false
	s.show(page)
	s.refreshSteps()
}

func (s *screen) showStep(step int) {
	if step < 0 || step >= stepCount || !s.reachable(step) {
		return
	}
	s.step = step
	s.inSteps = true
	if s.pages[step] == nil {
		return
	}
	s.show(s.pages[step])
	switch step {
	case stepImage:
		s.lookupReleases()
	case stepCard:
		if !s.listing && !s.busy {
			s.listDrives()
		}
	case stepConsole:
		s.refreshConsoles()
	case stepOptions:
		if s.started && s.bootloaderOptions != nil {
			s.bootloaderOptions.load()
		}
	}
	s.ready()
}

// next is what the Next button of a step does.
func (s *screen) next(step int) {
	switch step {
	case stepStart:
		if s.task == taskFix {
			s.showFix()
			return
		}
	case stepImage:
		// the chosen release is downloaded and checked first; loadImage moves on
		if s.asset != nil && (s.im == nil || s.im.Source != s.asset.URL) {
			s.download(*s.asset)
			return
		}
	}
	s.showStep(s.after(step))
}

// sideBar is the fixed column on the left: the name, the version, the steps, and
// at the bottom the language and the log.
func (s *screen) sideBar() fyne.CanvasObject {
	steps := container.NewVBox()
	// the buttons are positions in order(); which step each shows can change
	for i := range s.sideSteps {
		pos := i
		b := widget.NewButton(s.t(stepNames[s.order()[i]]), func() {
			if s.sideLocked() {
				return
			}
			step := s.order()[pos]
			if s.inSteps || s.reachable(step) {
				s.showStep(step)
			}
		})
		b.Alignment = widget.ButtonAlignLeading
		s.sideSteps[i] = b
		// the side bar's colour as it shows (its fill over the window's), a little
		// see-through
		r, g, bl, a := sideBackground().FillColor.RGBA()
		br, bg, bb, _ := theme.Color(theme.ColorNameBackground).RGBA()
		over := func(top, under uint32) uint8 { return uint8((top + under*(0xffff-a)/0xffff) >> 8) }
		dim := canvas.NewRectangle(color.NRGBA{over(r, br), over(g, bg), over(bl, bb), 80})
		dim.Hide()
		s.sideDims[i] = dim
		steps.Add(container.NewStack(b, dim))
	}
	name := heading("andr36oid SD Flasher")
	version := widget.NewLabel(Version)
	version.Importance = widget.LowImportance
	s.language = widget.NewButtonWithIcon(s.languageName(), theme.SettingsIcon(), s.chooseLanguage)
	s.language.Alignment = widget.ButtonAlignLeading
	s.language.Importance = widget.LowImportance
	s.details = widget.NewButtonWithIcon(s.t("Show details"), theme.ListIcon(), s.showLog)
	s.details.Alignment = widget.ButtonAlignLeading
	s.details.Importance = widget.LowImportance
	s.updateBanner = container.NewVBox()
	s.renderAppUpdate()
	top := container.NewVBox(name, version, widget.NewSeparator(), steps, s.updateBanner)
	bottom := container.NewVBox(widget.NewSeparator(), s.language, s.details)
	bg := container.NewBorder(top, bottom, nil, nil)
	return container.New(fixedWidth{200}, container.NewStack(sideBackground(), container.NewPadded(bg)))
}

// sideLocked: the side bar's steps can't be used now.
func (s *screen) sideLocked() bool {
	return s.busy || !s.inSteps && s.step != stepStart
}

// refreshSteps updates the side bar and the Back and Next buttons of every page.
func (s *screen) refreshSteps() {
	if s.sideSteps[0] == nil {
		return
	}
	// a step that lost what it needs (card removed, new release) sends the user back
	if s.inSteps && !s.busy && !s.reachable(s.step) {
		for s.step != stepStart && !s.reachable(s.step) {
			s.step = s.before(s.step)
		}
		s.showStep(s.step)
		return
	}
	order, now := s.order(), s.position(s.step)
	for pos, b := range s.sideSteps {
		i := order[pos]
		b.SetText(s.t(stepNames[i]))
		switch {
		case i == s.step:
			b.SetIcon(theme.NavigateNextIcon())
			b.Importance = widget.HighImportance
		case pos < now && s.stepDone(i):
			b.SetIcon(theme.ConfirmIcon())
			b.Importance = widget.LowImportance
		default:
			b.SetIcon(theme.RadioButtonIcon())
			b.Importance = widget.LowImportance
		}
		// while a write runs or a fix flow is open the steps stay readable, only
		// a little dimmed; a step not reached yet is greyed out
		switch {
		case s.sideLocked() && s.reachable(i):
			b.Enable()
			s.sideDims[pos].Show()
		case !s.reachable(i):
			b.Disable()
			s.sideDims[pos].Hide()
		default:
			b.Enable()
			s.sideDims[pos].Hide()
		}
		b.Refresh()
	}
	for i := range s.nexts {
		if s.backs[i] != nil {
			if s.busy {
				s.backs[i].Disable()
			} else {
				s.backs[i].Enable()
			}
		}
		if s.nexts[i] == nil {
			continue
		}
		ok := s.stepDone(i)
		if i == stepImage {
			ok = (s.im != nil || s.asset != nil) && s.variantOK()
		}
		if !s.busy && ok {
			s.nexts[i].Enable()
		} else {
			s.nexts[i].Disable()
		}
	}
}

func (s *screen) profileName() string {
	if p := s.chosenProfile(); p != nil {
		return shown(p.Name)
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
