package ui

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// "Fix or change a card": the occasional tools, each a short flow of its own that
// starts with choosing the card.

func (s *screen) showFix() {
	s.step = stepStart
	type action struct {
		title, detail string
		run           func()
	}
	cache := newChoiceCard(s.t("Clear cached files"), s.t("Cached files: %s", "..."), nil)
	actions := []action{
		{"Apply hotfixes", "Copies the fixes published for the card's build to the card.", func() {
			s.pickCard(s.t("Apply hotfixes"), s.t("Choose an existing andr36oid card. Only BOOT/hotfix will be updated."), func(d platform.Drive) { s.offerHotfixes(d, "") })
		}},
		{"Check for kernel update", "Looks for a newer kernel for the card's build.", func() {
			s.pickCard(s.t("Check for kernel update"), s.t("Choose an existing andr36oid card to check its kernel."), func(d platform.Drive) { s.offerKernelUpdate(d, "", nil) })
		}},
		{"Update device trees", dtbIntro, func() {
			s.pickCard(s.t("Update device trees"), s.t(dtbIntro), s.dtbPackPage)
		}},
		{"Boot options", "Bootloader, sleep firmware and boot chain of a card.", func() {
			s.pickCard(s.t("Boot options"), s.t(firmware.BootOptionsHint), s.editBootloader)
		}},
		{"Restore an interrupted update...", "Puts back the system an interrupted update saved, from its recovery.json.", func() {
			s.pickCard(s.t("Restore an interrupted update..."), s.t("Select the card that was being updated first."), s.restoreBackup)
		}},
	}
	list := container.NewVBox()
	for _, a := range actions {
		a := a
		list.Add(newChoiceCard(s.t(a.title), s.t(a.detail), func() {
			if !s.busy {
				a.run()
			}
		}))
	}
	measure := func() {
		background(func() {
			total := cacheTotal()
			fyne.Do(func() {
				cache.setText(s.t("Clear cached files"), s.t("Cached files: %s", humanBytes(total)))
				cache.setDisabled(total == 0)
			})
		})
	}
	cache.onTap = func() {
		if !s.busy {
			s.clearCache(measure)
		}
	}
	list.Add(cache)
	measure()
	back := widget.NewButton(s.t("Back"), func() { s.showStep(stepStart) })
	s.leave(pageView(title(s.t("Fix or change a card")), wrapped(s.t("Nothing is written to a card before you confirm it.")), list, nil, back))
}

// pickCard is the card page of a fix flow: the drives as rows, then then(drive).
func (s *screen) pickCard(head, lead string, then func(platform.Drive)) {
	var chosen *platform.Drive
	var drives []platform.Drive
	rows := container.NewVBox()
	status := wrapped(s.t("Looking for cards..."))
	var next *widget.Button
	var list func()
	redraw := func() {
		rows.RemoveAll()
		for _, d := range drives {
			d := d
			lines := []string{d.Path}
			if d.WriteProtected {
				lines = append([]string{s.t("This card can't be used: %s.", s.t(platform.WriteProtectedReason))}, lines...)
			}
			row := newChoiceCard(s.driveTitle(d), strings.Join(lines, "\n"), nil)
			row.onTap = func() {
				chosen = &d
				for _, o := range rows.Objects {
					o.(*choiceCard).setSelected(o == row)
				}
				next.Enable()
			}
			switch {
			case d.Fixed:
				row.setTag(s.t("Not a removable card"))
			case d.External:
				row.setTag(s.t("not a card"))
			}
			row.setDisabled(!s.driveUsable(d))
			row.setSelected(chosen != nil && platform.Same(*chosen, d))
			rows.Add(row)
		}
	}
	list = func() {
		status.SetText(s.t("Looking for cards..."))
		status.Show()
		all := s.allDrives
		background(func() {
			ds, e := listDrives(all)
			fyne.Do(func() {
				status.Hide()
				if e != nil {
					status.SetText(s.t("Could not list cards") + ": " + e.Error())
					status.Show()
					return
				}
				drives = ds
				if chosen != nil {
					found := false
					for _, d := range ds {
						if platform.Same(d, *chosen) {
							found = true
						}
					}
					if !found {
						chosen = nil
						next.Disable()
					}
				}
				if len(ds) == 0 {
					status.SetText(s.t("No removable cards found. Insert a card and refresh."))
					status.Show()
				}
				redraw()
			})
		})
	}
	back := widget.NewButton(s.t("Back"), s.showFix)
	next = widget.NewButton(s.t("Next"), func() {
		if chosen != nil {
			then(*chosen)
		}
	})
	next.Importance = widget.HighImportance
	next.Disable()
	body := container.NewVBox(rows, status, container.NewHBox(quiet(s.t("Refresh cards"), list)))
	s.leave(pageView(title(head), wrapped(lead), body, nil, back, next))
	list()
}

// The log: what the flasher did, for "Show details" and a report.

func (s *screen) logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + " " + format
	if len(args) > 0 {
		line = time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	}
	s.log = append(s.log, line)
	if len(s.log) > 2000 {
		s.log = s.log[len(s.log)-2000:]
	}
}

func (s *screen) logText() string {
	return "andr36oid SD Flasher " + Version + "\n" + strings.Join(s.log, "\n") + "\n"
}

// showLog opens the log, with Copy and Save.
func (s *screen) showLog() {
	text := widget.NewLabel(s.logText())
	text.Wrapping = fyne.TextWrapWord
	text.Selectable = true
	text.TextStyle = fyne.TextStyle{Monospace: true}
	scroll := container.NewVScroll(text)
	scroll.SetMinSize(fyne.NewSize(640, 380))
	var d *dialog.CustomDialog
	copyLog := widget.NewButton(s.t("Copy"), func() { fyne.CurrentApp().Clipboard().SetContent(s.logText()) })
	save := widget.NewButton(s.t("Save log..."), s.saveLog)
	closeButton := widget.NewButton(s.t("Close"), func() { d.Hide() })
	closeButton.Importance = widget.HighImportance
	d = dialog.NewCustomWithoutButtons(s.t("Details"), scroll, s.win)
	d.SetButtons([]fyne.CanvasObject{copyLog, save, closeButton})
	d.Resize(fyne.NewSize(min(720, s.win.Canvas().Size().Width-40), min(520, s.win.Canvas().Size().Height-40)))
	d.Show()
}

func (s *screen) saveLog() {
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, e error) {
		if e != nil {
			s.showError(e)
			return
		}
		if w == nil {
			return
		}
		defer w.Close()
		if _, e := w.Write([]byte(s.logText())); e != nil {
			s.showError(e)
		}
	}, s.win)
	d.SetFileName("andr36oid-sdflasher-log.txt")
	if home, e := os.UserHomeDir(); e == nil {
		if l, e := storage.ListerForURI(storage.NewFileURI(home)); e == nil {
			d.SetLocation(l)
		}
	}
	s.showFileDialog(d)
}

// slotPicture shows what to do with the card: into the console's first slot.
func slotPicture() fyne.CanvasObject {
	r := canvas.NewRaster(func(w, h int) image.Image {
		im := image.NewNRGBA(image.Rect(0, 0, w, h))
		fg := theme.Color(theme.ColorNameForeground)
		accent := theme.Color(theme.ColorNamePrimary)
		box := func(x0, y0, x1, y1 int, c color.Color, filled bool) {
			x0, y0, x1, y1 = x0*w/320, y0*h/120, x1*w/320, y1*h/120
			if filled {
				draw.Draw(im, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{}, draw.Src)
				return
			}
			t := max(2, w/160)
			for _, rc := range []image.Rectangle{image.Rect(x0, y0, x1, y0+t), image.Rect(x0, y1-t, x1, y1), image.Rect(x0, y0, x0+t, y1), image.Rect(x1-t, y0, x1, y1)} {
				draw.Draw(im, rc, image.NewUniform(c), image.Point{}, draw.Src)
			}
		}
		// the console from below: its body and the two slots, TF1 on the left
		box(20, 10, 300, 70, fg, false)
		box(70, 62, 130, 70, accent, true)
		box(190, 62, 250, 70, fg, false)
		// the card going up into TF1, and an arrow
		box(80, 84, 120, 116, accent, true)
		box(97, 74, 103, 84, accent, true)
		return im
	})
	r.SetMinSize(fyne.NewSize(320, 120))
	tf1 := canvas.NewText("TF1", theme.Color(theme.ColorNamePrimary))
	tf1.TextStyle = fyne.TextStyle{Bold: true}
	tf2 := canvas.NewText("TF2", theme.Color(theme.ColorNameForeground))
	tf1.Move(fyne.NewPos(84, 36))
	tf2.Move(fyne.NewPos(204, 36))
	return container.NewStack(r, container.NewWithoutLayout(tf1, tf2))
}
