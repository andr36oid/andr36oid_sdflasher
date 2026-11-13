package ui

import (
	"context"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

const loaderTestWarning = "Test builds are tried on few consoles. If the console no longer starts, write the card again with \"As in the image\"."

type bootloaderPicker struct {
	s                                                    *screen
	box                                                  *fyne.Container
	radios                                               [3]*widget.RadioGroup
	versions                                             [3]*widget.Label
	hint, warning                                        *widget.Label
	builds                                               map[string]*firmware.BootloaderBuild
	choice                                               firmware.BootloaderChoice
	image, unsupported, loading, loaded, failed, setting bool
	cancel                                               context.CancelFunc
	changed                                              func()
	lookup                                               func(context.Context) ([]*firmware.BootloaderBuild, error)
}

var loaderChoices = [3]string{"image", "standard", "new"}

func (s *screen) newBootloaderPicker(choice firmware.BootloaderChoice, changed func()) *bootloaderPicker {
	p := &bootloaderPicker{s: s, choice: choice, changed: changed, lookup: firmware.FetchBootloaderChoices, builds: map[string]*firmware.BootloaderBuild{}}
	if p.choice.Choice == "" {
		p.choice.Choice = "image"
	}
	titles := [3]string{"As in the image", "Standard U-Boot, newest test build", "New U-Boot, newest test build"}
	rows := container.NewVBox(heading(s.t("Bootloader")))
	for n, title := range titles {
		index := n
		p.radios[n] = widget.NewRadioGroup([]string{s.t(title)}, func(selected string) {
			if p.setting || selected == "" {
				return
			}
			p.selectChoice(loaderChoices[index])
			if p.changed != nil {
				p.changed()
			}
		})
		p.radios[n].Required = true
		p.versions[n] = wrapped("")
		rows.Add(container.NewBorder(nil, nil, nil, p.versions[n], p.radios[n]))
	}
	p.hint = wrapped("")
	p.warning = wrapped(s.t(loaderTestWarning))
	rows.Add(p.hint)
	rows.Add(p.warning)
	p.box = rows
	p.selectChoice(p.choice.Choice)
	p.refresh()
	return p
}
func (p *bootloaderPicker) selectChoice(choice string) {
	p.setting = true
	defer func() { p.setting = false }()
	p.choice = firmware.BootloaderChoice{Choice: choice, Build: p.builds[choice]}
	for n, key := range loaderChoices {
		if key == choice {
			p.radios[n].SetSelected(p.radios[n].Options[0])
		} else {
			p.radios[n].SetSelected("")
		}
	}
}
func (p *bootloaderPicker) setTarget(image, unsupported bool) {
	p.image, p.unsupported = image, unsupported
	p.refresh()
}
func (p *bootloaderPicker) refresh() {
	p.versions[0].SetText(p.s.t("recommended"))
	notes := []string{}
	if p.unsupported {
		notes = append(notes, p.s.t(firmware.BootloaderOnlySD))
	} else {
		if !p.image {
			notes = append(notes, p.s.t(firmware.BootloaderNeedImage))
		}
		if p.failed {
			notes = append(notes, p.s.t(firmware.BootloaderFetchFailed))
		}
	}
	for n, key := range loaderChoices {
		enabled := !p.unsupported && (n == 0 && p.image || n > 0 && p.loaded && !p.failed && p.builds[key] != nil)
		if enabled {
			p.radios[n].Enable()
		} else {
			p.radios[n].Disable()
		}
		if n == 0 {
			continue
		}
		if p.loading || !p.loaded {
			p.versions[n].SetText(p.s.t("Looking for bootloader builds…"))
		} else if b := p.builds[key]; b != nil {
			p.versions[n].SetText(b.Version + " · " + b.Tag)
		} else {
			p.versions[n].SetText("")
		}
	}
	p.hint.SetText(strings.Join(notes, "\n"))
	if len(notes) == 0 {
		p.hint.Hide()
	} else {
		p.hint.Show()
	}
	if p.unsupported {
		p.warning.Hide()
	} else {
		p.warning.Show()
	}
}
func (p *bootloaderPicker) selection() *firmware.BootloaderChoice {
	if p.unsupported {
		return nil
	}
	for n, key := range loaderChoices {
		if key == p.choice.Choice && !p.radios[n].Disabled() {
			copy := p.choice
			copy.Build = p.builds[key]
			return &copy
		}
	}
	return nil
}
func (p *bootloaderPicker) load() {
	if p.unsupported || p.loading || p.loaded && !p.failed {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.loading = true
	p.refresh()
	go func() {
		offers, err := p.lookup(ctx)
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			p.loading = false
			p.loaded = true
			p.failed = err != nil
			if err == nil {
				for _, offer := range offers {
					if offer != nil {
						p.builds[offer.Choice] = offer
					}
				}
			} else {
				p.selectChoice("image")
			}
			if p.choice.Choice != "image" {
				p.choice.Build = p.builds[p.choice.Choice]
			}
			p.refresh()
			if p.changed != nil {
				p.changed()
			}
		})
	}()
}
func (s *screen) bootloaderSection() *fyne.Container {
	if s.bootloaderOptions != nil && s.bootloaderOptions.cancel != nil {
		s.bootloaderOptions.cancel()
	}
	var p *bootloaderPicker
	p = s.newBootloaderPicker(s.bootloaderChoice, func() { s.bootloaderChoice = p.choice; s.ready() })
	s.bootloaderOptions = p
	p.setTarget(s.im != nil, s.drive != nil && s.drive.Fixed || s.card != nil && s.card.DataCard)
	return p.box
}
func (s *screen) bootloaderSummary(record *firmware.BootloaderRecord) string {
	if record == nil {
		return ""
	}
	switch record.Choice {
	case "new":
		return s.t("Bootloader: New U-Boot %s (%s)", record.Version, record.Tag)
	case "standard":
		return s.t("Bootloader: Standard U-Boot %s (%s)", record.Version, record.Tag)
	case "file":
		return s.t("Bootloader") + ": " + s.t("File")
	}
	return s.t("Bootloader: as in the image")
}
func (s *screen) chooseBootloaderCard() {
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
			labels := []string{}
			for _, d := range drives {
				labels = append(labels, s.driveLabel(d))
			}
			if len(labels) == 0 {
				s.info(s.t("Bootloader"), s.t("No removable cards found. Insert a card and refresh."), s.win)
				return
			}
			pick := widget.NewSelect(labels, nil)
			pick.PlaceHolder = s.t("Select a microSD card")
			dialog.ShowCustomConfirm(s.t("Bootloader"), s.t("Continue"), s.t("Cancel"), pick, func(ok bool) {
				if ok && pick.SelectedIndex() >= 0 {
					s.editBootloader(drives[pick.SelectedIndex()])
				}
			}, s.win)
		})
	}()
}
func (s *screen) editBootloader(drive platform.Drive) {
	previous := s.win.Content()
	s.setBusy(true)
	go func() {
		info, err := jobs.Run(jobs.Request{Action: "bootloader-info", Drive: drive}, nil)
		fyne.Do(func() {
			s.setBusy(false)
			if err != nil {
				s.showError(err)
				return
			}
			if info.Card == nil {
				return
			}
			image := s.im
			var apply, choose *widget.Button
			var picker *bootloaderPicker
			picker = s.newBootloaderPicker(firmware.BootloaderChoice{Choice: "image"}, func() {
				if apply != nil {
					if picker.selection() != nil {
						apply.Enable()
					} else {
						apply.Disable()
					}
				}
			})
			picker.setTarget(image != nil, drive.Fixed || info.Card.DataCard)
			status := wrapped("")
			if info.Setup != nil {
				status.SetText(s.bootloaderSummary(info.Setup.Bootloader))
			}
			bar := widget.NewProgressBar()
			bar.Hide()
			finish := func() {
				if s.busy {
					return
				}
				if picker.cancel != nil {
					picker.cancel()
				}
				s.win.SetContent(previous)
				s.setBusy(false)
			}
			cancel := widget.NewButton(s.t("Cancel"), finish)
			choose = widget.NewButton(s.t("Choose image from disk…"), func() {
				d := dialog.NewFileOpen(func(reader fyne.URIReadCloser, e error) {
					if e != nil {
						s.showError(e)
						return
					}
					if reader == nil {
						return
					}
					name := reader.URI().Path()
					reader.Close()
					s.setBusy(true)
					choose.Disable()
					apply.Disable()
					go func() {
						im, e := firmware.Inspect(context.Background(), name, nil)
						fyne.Do(func() {
							s.setBusy(false)
							choose.Enable()
							if e != nil {
								s.showError(e)
								return
							}
							image = im
							picker.setTarget(true, drive.Fixed || info.Card.DataCard)
							picker.selectChoice("image")
							apply.Enable()
						})
					}()
				}, s.win)
				d.SetFilter(storage.NewExtensionFileFilter([]string{".img", ".xz", ".gz", ".zip"}))
				d.Show()
			})
			if drive.Fixed || info.Card.DataCard {
				choose.Disable()
			}
			apply = widget.NewButton(s.t("Apply"), func() {
				if s.busy {
					return
				}
				selection := picker.selection()
				if selection == nil {
					return
				}
				s.setBusy(true)
				apply.Disable()
				choose.Disable()
				cancel.Disable()
				bar.SetValue(0)
				bar.Show()
				req := jobs.Request{Action: "bootloader-apply", Drive: drive, Bootloader: selection, BootloaderExpected: info.BuildIdentity, CardFingerprint: info.Card.Fingerprint}
				if image != nil {
					req.Image = image.Path
					req.ImageHash = image.SHA256
				}
				go func() {
					result, e := jobs.Run(req, func(p flash.Progress) {
						fyne.Do(func() {
							if p.Total > 0 {
								bar.SetValue(float64(p.Done) / float64(p.Total))
							}
						})
					})
					fyne.Do(func() {
						s.setBusy(false)
						cancel.Enable()
						cancel.SetText(s.t("Close"))
						bar.Hide()
						if e != nil {
							status.SetText(e.Error())
							return
						}
						if result.Bootloader != nil {
							if result.Bootloader.Warning != "" {
								status.SetText(s.t(result.Bootloader.Warning))
							} else {
								status.SetText(s.bootloaderSummary(result.Bootloader.Record))
							}
						}
					})
				}()
			})
			apply.Importance = widget.HighImportance
			if picker.selection() == nil {
				apply.Disable()
			}
			s.win.SetContent(container.NewPadded(container.NewBorder(container.NewVBox(heading(s.t("Bootloader")), wrapped(drive.Label())), container.NewHBox(cancel, apply), nil, nil, container.NewVScroll(container.NewVBox(picker.box, choose, status, bar)))))
			picker.load()
		})
	}()
}
