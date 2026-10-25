package ui

import (
	"context"
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// Standalone card selection deliberately does not depend on loading an image.
func (s *screen) chooseHotfixCard() {
	if s.busy {
		return
	}
	s.setBusy(true)
	go func() {
		list := platform.List
		if s.allDrives {
			list = platform.ListAll
		}
		drives, err := list()
		fyne.Do(func() {
			s.setBusy(false)
			if err != nil {
				s.showError(err)
				return
			}
			s.pickHotfixCard(drives)
		})
	}()
}

func (s *screen) pickHotfixCard(drives []platform.Drive) {
	labels := []string{}
	for _, d := range drives {
		labels = append(labels, s.driveLabel(d))
	}
	if len(labels) == 0 {
		s.info(s.t("Apply hotfixes"), s.t("No removable cards found. Insert a card and refresh."), s.win)
		return
	}
	pick := widget.NewSelect(labels, nil)
	pick.PlaceHolder = s.t("Select a microSD card")
	dialog.ShowCustomConfirm(s.t("Apply hotfixes"), s.t("Continue"), s.t("Cancel"), container.NewVBox(wrapped(s.t("Choose an existing andr36oid card. Only BOOT/hotfix will be updated.")), pick), func(ok bool) {
		if ok && pick.SelectedIndex() >= 0 {
			s.offerHotfixes(drives[pick.SelectedIndex()], "")
		}
	}, s.win)
}

func (s *screen) offerHotfixes(drive platform.Drive, expected string) {
	previous := s.win.Content()
	ctx, cancel := context.WithCancel(context.Background())
	s.setBusy(true)
	status := wrapped(s.t("Checking for hotfixes"))
	bar := widget.NewProgressBar()
	items := container.NewVBox()
	skip := widget.NewButton(s.t("Skip hotfixes"), func() { cancel(); s.win.SetContent(previous); s.setBusy(false) })
	apply := widget.NewButton(s.t("Apply selected hotfixes"), nil)
	apply.Disable()
	apply.Importance = widget.HighImportance
	retry := widget.NewButton(s.t("Try again"), func() { s.win.SetContent(previous); s.setBusy(false); s.offerHotfixes(drive, expected) })
	retry.Hide()
	s.win.SetContent(container.NewPadded(container.NewBorder(
		container.NewVBox(heading(s.t("Apply hotfixes")), wrapped(drive.Label()), status, bar),
		container.NewHBox(skip, apply, retry), nil, nil, container.NewVScroll(items))))
	go func() {
		result, e := jobs.Run(jobs.Request{Action: "hotfix-list", Drive: drive}, nil)
		var offer *firmware.HotfixOffer
		if e == nil && expected != "" && result.BuildIdentity != expected {
			e = errors.New(firmware.HotfixChanged)
		}
		if e == nil {
			var index *firmware.HotfixIndex
			index, e = firmware.FetchHotfixIndex(ctx, firmware.HotfixIndexURL)
			if e == nil {
				offer = index.ForBuild(result.BuildIdentity)
			}
		}
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			s.setBusy(false)
			skip.Enable()
			bar.Hide()
			if e != nil {
				status.SetText(s.t(e.Error()))
				retry.Show()
				return
			}
			if len(offer.Hotfixes) == 0 {
				status.SetText(s.t("No hotfixes are available for this exact build."))
				return
			}
			status.SetText(s.t("Bug fixes are selected. Optional extras are unchecked. Choose which hotfixes to copy to the card."))
			content, checks := s.hotfixChecklist(offer)
			items.Add(content)
			apply.Enable()
			apply.OnTapped = func() {
				if s.busy {
					return
				}
				selected := []string{}
				for i, c := range checks {
					if c.Checked {
						selected = append(selected, offer.Hotfixes[i].File)
					}
					c.Disable()
				}
				apply.Disable()
				skip.Disable()
				retry.Hide()
				bar.Show()
				bar.SetValue(0)
				s.setBusy(true)
				go func() {
					result, err := jobs.Run(jobs.Request{Action: "hotfix-apply", Drive: drive, HotfixOffer: offer, HotfixFiles: selected}, func(p flash.Progress) {
						fyne.Do(func() {
							status.SetText(s.t(p.Phase) + ": " + p.Name)
							if p.Total > 0 {
								bar.SetValue(float64(p.Done) / float64(p.Total))
							}
						})
					})
					fyne.Do(func() {
						s.setBusy(false)
						skip.Enable()
						skip.SetText(s.t("Continue"))
						bar.Hide()
						if err != nil {
							status.SetText(s.t(err.Error()))
							retry.Show()
							return
						}
						status.SetText(s.t("Hotfix results"))
						items.RemoveAll()
						if len(result.HotfixResults) == 0 {
							items.Add(wrapped(s.t("No hotfixes selected.")))
						}
						for _, r := range result.HotfixResults {
							message := s.t("Hotfix installed: %s", r.File)
							if r.Error != "" {
								message = s.t("Hotfix failed: %s", r.File) + "\n" + s.t("Technical details") + ": " + r.Error
							}
							items.Add(wrapped(message))
						}
						items.Add(wrapped(s.t("Hotfixes will be applied when the console next starts.")))
					})
				}()
			}
		})
	}()
}

func (s *screen) hotfixChecklist(offer *firmware.HotfixOffer) (*fyne.Container, []*widget.Check) {
	items := container.NewVBox()
	checks := make([]*widget.Check, len(offer.Hotfixes))
	for i, h := range offer.Hotfixes {
		check := widget.NewCheck(h.Title, nil)
		check.SetChecked(h.Preselected())
		checks[i] = check
		description := strings.Join(strings.Fields(h.Description), " ")
		items.Add(container.NewVBox(check, wrapped(description), wrapped(s.t("Size: %.1f MiB", float64(h.Size)/(1<<20))), widget.NewSeparator()))
	}
	return items, checks
}
