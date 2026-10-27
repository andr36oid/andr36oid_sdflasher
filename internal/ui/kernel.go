package ui

import (
	"context"
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

func (s *screen) chooseKernelCard() {
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
			s.pickKernelCard(drives)
		})
	}()
}
func (s *screen) pickKernelCard(drives []platform.Drive) {
	labels := []string{}
	for _, d := range drives {
		labels = append(labels, s.driveLabel(d))
	}
	if len(labels) == 0 {
		s.info(s.t("Check for kernel update"), s.t("No removable cards found. Insert a card and refresh."), s.win)
		return
	}
	pick := widget.NewSelect(labels, nil)
	pick.PlaceHolder = s.t("Select a microSD card")
	dialog.ShowCustomConfirm(s.t("Check for kernel update"), s.t("Continue"), s.t("Cancel"), container.NewVBox(wrapped(s.t("Choose an existing andr36oid card to check its kernel.")), pick), func(ok bool) {
		if ok && pick.SelectedIndex() >= 0 {
			s.offerKernelUpdate(drives[pick.SelectedIndex()], "", nil)
		}
	}, s.win)
}

// A nil offer (or unreadable/incompatible build) never produces an Apply page.
func (s *screen) kernelOfferDetails(offer *catalog.KernelOffer) *fyne.Container {
	if offer == nil || !firmware.KernelCompatible(offer.CompatibleBuilds, offer.Current.Build) {
		return nil
	}
	return container.NewVBox(heading(s.t("Kernel update %s", offer.Version)), wrapped(offer.Title), wrapped(strings.Join(strings.Fields(offer.Description), " ")), wrapped(s.t("Size: %.1f MiB", float64(offer.Size)/(1<<20))), wrapped(s.t("The kernel files in BOOT will be updated. Apps, settings, saves, and games stay in place. Keep the card connected until verification finishes.")))
}

// after advances the post-flash flow to hotfixes. Standalone checks pass nil.
func (s *screen) offerKernelUpdate(drive platform.Drive, expected string, after func()) {
	previous := s.win.Content()
	ctx, cancel := context.WithCancel(context.Background())
	finish := func() {
		cancel()
		s.win.SetContent(previous)
		s.setBusy(false)
		if after != nil {
			after()
		}
	}
	s.setBusy(true)
	status := wrapped(s.t("Checking for kernel update..."))
	bar := widget.NewProgressBar()
	items := container.NewVBox()
	skip := widget.NewButton(s.t("Skip kernel update"), finish)
	apply := widget.NewButton(s.t("Apply kernel update"), nil)
	apply.Importance = widget.HighImportance
	apply.Disable()
	apply.Hide()
	retry := widget.NewButton(s.t("Try again"), func() {
		cancel()
		s.win.SetContent(previous)
		s.setBusy(false)
		s.offerKernelUpdate(drive, expected, after)
	})
	retry.Hide()
	s.win.SetContent(container.NewPadded(container.NewBorder(container.NewVBox(heading(s.t("Check for kernel update")), wrapped(drive.Label()), status, bar), container.NewHBox(skip, apply, retry), nil, nil, container.NewVScroll(items))))
	go func() {
		result, err := jobs.Run(jobs.Request{Action: "kernel-list", Drive: drive}, nil)
		var offer *catalog.KernelOffer
		if err == nil && (result.KernelIdentity == nil || (expected != "" && result.KernelIdentity.Build != expected)) {
			err = errors.New(firmware.KernelChanged)
		}
		if err == nil {
			offer, err = catalog.CheckKernelUpdate(ctx, *result.KernelIdentity)
		}
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			s.setBusy(false)
			bar.Hide()
			if err != nil {
				if after != nil && err.Error() == firmware.KernelNoBuild {
					finish()
					return
				}
				status.SetText(s.t("Could not check for kernel updates.") + "\n" + s.t("Technical details") + ": " + s.t(err.Error()))
				retry.Show()
				return
			}
			details := s.kernelOfferDetails(offer)
			if details == nil {
				if after != nil {
					finish()
					return
				}
				status.SetText(s.t("No kernel update is available for this card."))
				skip.SetText(s.t("Continue"))
				return
			}
			status.SetText(s.t("A kernel update is available for this card."))
			items.Add(details)
			apply.Show()
			apply.Enable()
			apply.OnTapped = func() {
				if s.busy {
					return
				}
				apply.Disable()
				skip.Disable()
				retry.Hide()
				bar.SetValue(0)
				bar.Show()
				s.setBusy(true)
				go func() {
					_, err := jobs.Run(jobs.Request{Action: "kernel-apply", Drive: drive, KernelOffer: offer}, func(p flash.Progress) {
						fyne.Do(func() {
							status.SetText(s.t(p.Phase))
							if p.Total > 0 {
								bar.SetValue(float64(p.Done) / float64(p.Total))
							}
						})
					})
					fyne.Do(func() {
						s.setBusy(false)
						skip.Enable()
						bar.Hide()
						if err != nil {
							status.SetText(s.t("Kernel update could not finish.") + "\n" + s.t("Technical details") + ": " + s.t(err.Error()))
							retry.Show()
							return
						}
						status.SetText(s.t("Kernel update installed and verified."))
						skip.SetText(s.t("Continue"))
					})
				}()
			}
		})
	}()
}
