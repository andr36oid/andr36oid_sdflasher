package ui

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

// The advanced option to write a different system image (a GSI) instead of the andr36oid one.
func (s *screen) systemSection() fyne.CanvasObject {
	s.systemLabel = wrapped(s.systemText())
	choose := widget.NewButton(s.t("Choose system image..."), s.chooseSystem)
	var clear *widget.Button
	clear = widget.NewButton(s.t("Use the andr36oid system"), func() {
		s.system = ""
		s.systemAccepted = false
		s.systemLabel.SetText(s.systemText())
		clear.Hide()
		s.ready()
	})
	if s.system == "" {
		clear.Hide()
	}
	s.systemClear = clear
	return optionRow(s.systemLabel, container.NewHBox(clear, choose), s.t(firmware.GSIWarning))
}

func (s *screen) systemText() string {
	if s.system == "" {
		return s.t("The andr36oid system is written.")
	}
	return s.t("System image") + ": " + filepath.Base(s.system)
}

func (s *screen) chooseSystem() {
	if s.busy {
		return
	}
	s.confirmDialog(s.t("Advanced: different system image (GSI)"), s.t(firmware.GSIWarning), func(ok bool) {
		if !ok {
			return
		}
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, e error) {
			if e != nil {
				s.showError(e)
				return
			}
			if r == nil {
				return
			}
			s.system = r.URI().Path()
			r.Close()
			s.systemAccepted = true
			s.systemLabel.SetText(s.systemText())
			s.systemClear.Show()
			s.ready()
		}, s.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".img"}))
		s.showFileDialog(d)
	}, s.win)
}
