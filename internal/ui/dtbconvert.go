package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/dtbconv"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

// Texts of the "use my console's own DTB" option. They are i18n keys.
const (
	ownDTBButton  = "Use my console’s own DTB file..."
	ownDTBTitle   = "Your console’s own DTB"
	ownDTBExplain = "Choose the DTB file from your console’s original ArkOS, dArkOS or EmuELEC card. It is in the BOOT partition, often named rk3326-r36s-linux.dtb. The flasher turns it into an andr36oid DTB: it copies your console’s screen, buttons, battery and other board details into the andr36oid DTB of the same board family. Your file and the files it replaces are kept in the andr36oid-dtb-backup folder on BOOT."
	ownDTBRefused = "This DTB can’t be converted"
	ownDTBUse     = "Use this DTB"
	ownDTBLabel   = "Converted from your console’s DTB: %s"
	ownDTBWarning = "This DTB was made by a program from your console’s own DTB. It has not been tested on your console. If the screen stays dark or the buttons don’t work, write the card again without it."
	ownDTBSummary = "Board family: %s. Screen: %s. Device profile: %s."
	ownDTBCheck   = "Please check"
	ownDTBReport  = "Full report"
	ownDTBNoImage = "Choose a release first. The conversion uses the DTBs of that release."
)

func (s *screen) chooseOwnDTB() {
	if s.busy {
		return
	}
	if s.im == nil {
		s.info(s.t(ownDTBTitle), s.t(ownDTBNoImage), s.win)
		return
	}
	s.confirmDialog(s.t(ownDTBTitle), s.t(ownDTBExplain), func(ok bool) {
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
			p := r.URI().Path()
			r.Close()
			s.previewConversion(p)
		}, s.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".dtb"}))
		s.showFileDialog(d)
	}, s.win)
}

func (s *screen) previewConversion(p string) {
	im := s.im
	s.setBusy(true)
	go func() {
		c, e := firmware.ConvertCustom(im, p)
		fyne.Do(func() {
			s.setBusy(false)
			if e != nil {
				if dtbconv.IsRefusal(e) {
					s.info(s.t(ownDTBRefused), e.Error(), s.win)
				} else {
					s.showError(e)
				}
				return
			}
			s.showConversion(p, c)
		})
	}()
}

func (s *screen) showConversion(p string, c *firmware.Conversion) {
	profile := c.Profile
	for _, x := range s.im.Profiles {
		if x.ID == c.Profile {
			profile = x.Name
		}
	}
	if profile == "" {
		profile = s.profile
	}
	screen := fmt.Sprintf("%d × %d", c.Width, c.Height)
	parts := []fyne.CanvasObject{
		wrapped(s.t(ownDTBSummary, c.Family.Label(), screen, profile)),
		wrapped(s.t(ownDTBWarning)),
	}
	if len(c.Warnings) > 0 {
		parts = append(parts, widget.NewLabelWithStyle(s.t(ownDTBCheck), 0, fyne.TextStyle{Bold: true}), wrapped("- "+strings.Join(c.Warnings, "\n- ")))
	}
	report := widget.NewLabel(c.Report())
	report.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(report)
	scroll.SetMinSize(fyne.NewSize(560, 220))
	parts = append(parts, widget.NewAccordion(widget.NewAccordionItem(s.t(ownDTBReport), scroll)))
	d := dialog.NewCustomConfirm(s.t(ownDTBTitle), s.t(ownDTBUse), s.t("Cancel"), container.NewVBox(parts...), func(ok bool) {
		if !ok {
			return
		}
		s.custom = p
		s.customAccepted = true
		s.convert = true
		if c.Profile != "" {
			s.selectProfile(c.Profile)
		}
		s.customLabel.SetText(s.t(ownDTBLabel, filepath.Base(p)))
		s.ready()
	}, s.win)
	d.Resize(fyne.NewSize(640, 520))
	d.Show()
}
