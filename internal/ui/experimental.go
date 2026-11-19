package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// where problems with experimental device trees go, and only there
const cloneTracker = "https://github.com/andr36oid/clone_issue_tracker"

// agreeDialog asks before something risky: the warning in red, a checkbox, and an
// "I agree" button that only works once the box is ticked. done gets the answer.
func (s *screen) agreeDialog(title, warning, check string, done func(bool)) {
	body := wrapped(warning)
	body.Importance = widget.DangerImportance
	box, _, boxRow := wrappedCheck(check, nil)
	agree := widget.NewButton(s.t("I agree"), nil)
	agree.Importance = widget.DangerImportance
	agree.Disable()
	cancel := widget.NewButton(s.t("Cancel"), nil)
	d := dialog.NewCustomWithoutButtons(title, container.NewVScroll(container.NewVBox(body, widget.NewSeparator(), boxRow)), s.win)
	box.OnChanged = func(v bool) {
		if v {
			agree.Enable()
		} else {
			agree.Disable()
		}
	}
	agree.OnTapped = func() {
		d.Hide()
		done(true)
	}
	cancel.OnTapped = func() {
		d.Hide()
		done(false)
	}
	d.SetButtons([]fyne.CanvasObject{cancel, agree})
	d.Resize(fyne.NewSize(min(640, s.win.Canvas().Size().Width-40), min(520, s.win.Canvas().Size().Height-40)))
	d.Show()
}
