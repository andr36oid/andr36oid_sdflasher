package ui

import (
	"context"
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

func (s *screen) offerHotfixes(drive platform.Drive, expected string) {
	previous := s.current()
	ctx, cancel := context.WithCancel(context.Background())
	s.setBusy(true)
	status := wrapped(s.t("Checking for hotfixes"))
	bar := widget.NewProgressBar()
	items := container.NewVBox()
	skip := widget.NewButton(s.t("Skip hotfixes"), func() { cancel(); s.show(previous); s.setBusy(false) })
	apply := widget.NewButton(s.t("Apply selected hotfixes"), nil)
	apply.Disable()
	apply.Importance = widget.HighImportance
	retry := widget.NewButton(s.t("Try again"), func() { s.show(previous); s.setBusy(false); s.offerHotfixes(drive, expected) })
	retry.Hide()
	s.leave(pageView(title(s.t("Apply hotfixes")), wrapped(s.driveTitle(drive)), container.NewVBox(status, bar, items), nil, skip, retry, apply))
	go func() {
		result, e := jobs.Run(jobs.Request{Action: "hotfix-list", Drive: drive}, nil)
		var offer *firmware.HotfixOffer
		if e == nil && expected != "" && result.BuildIdentity != expected {
			e = errors.New(firmware.HotfixChanged)
		}
		// never on a card whose first setup is still to come or under way
		if e == nil && result.FirstBoot != nil && !result.FirstBoot.Done {
			e = errors.New(s.t("The console has not finished its first setup on this card. Put the card in the console, finish the setup, then apply hotfixes."))
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
							status.SetText(s.t(p.Name))
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

// lookupFixes counts, once per release, the bug fixes published for its build;
// the options page offers them.
func (s *screen) lookupFixes() {
	if !s.started || s.im == nil || s.fixesFor == s.im.BuildIdentity {
		return
	}
	build := s.im.BuildIdentity
	s.fixesFor, s.fixCount = build, -1
	go func() {
		n := -1
		index, e := firmware.FetchHotfixIndex(context.Background(), firmware.HotfixIndexURL)
		if e == nil {
			n = 0
			for _, h := range index.ForBuild(build).Hotfixes {
				if h.Preselected() {
					n++
				}
			}
		}
		fyne.Do(func() {
			if s.fixesFor == build {
				s.fixCount = n
				s.ready()
			}
		})
	}()
}

// refreshFixes shows the bug fix box when there are fixes for the release.
func (s *screen) refreshFixes() {
	if s.fixBox == nil {
		return
	}
	if s.fixCount <= 0 {
		s.fixBox.Hide()
		return
	}
	s.fixLabel.SetText(s.t("Bug fixes for this release (%d)", s.fixCount))
	text := s.t("They are offered once the console has finished its first start from the card; you choose which ones then.")
	if s.updating {
		text = s.t("They are offered right after the update; you choose which ones then.")
	}
	s.fixInfo.SetText(text)
	s.fixBox.Show()
}
