package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"strings"
)

const loaderTestWarning = firmware.BootChainWarning

// A dropdown with genuinely disabled entries (widget.Select cannot disable an
// individual choice). The menu and keyboard activation use the same callbacks.
type bootMenuOption struct{ id, label, reason string }
type bootDropdown struct {
	*widget.Button
	options []bootMenuOption
	changed func(string)
}

func newBootDropdown(changed func(string)) *bootDropdown {
	d := &bootDropdown{changed: changed}
	d.Button = widget.NewButtonWithIcon("", theme.MenuDropDownIcon(), func() {
		items := []*fyne.MenuItem{}
		for _, o := range d.options {
			o := o
			title := o.label
			if o.reason != "" {
				title += " — " + o.reason
			}
			item := fyne.NewMenuItem(title, func() { d.changed(o.id) })
			item.Disabled = o.reason != ""
			items = append(items, item)
		}
		c := fyne.CurrentApp().Driver().CanvasForObject(d.Button)
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(d.Button).Add(fyne.NewPos(0, d.Size().Height))
		widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", items...), c, pos)
	})
	return d
}

type bootloaderPicker struct {
	s                                                              *screen
	box                                                            *fyne.Container
	rows                                                           *fyne.Container
	toggle                                                         *widget.Button
	controls                                                       [3]*bootDropdown
	hint, warning, summary                                         *widget.Label
	rowNotes                                                       [3]*widget.RichText
	builds                                                         map[string]*firmware.BootloaderBuild
	choice                                                         firmware.BootloaderChoice
	target                                                         firmware.BootTarget
	rules                                                          []firmware.BootRule
	catalog                                                        *firmware.BootCatalog
	resetNotes                                                     []firmware.BootIssue
	image, unsupported, loading, loaded, failed, setting, accepted bool
	cardTarget                                                     bool
	cancel                                                         context.CancelFunc
	changed                                                        func()
	lookup                                                         func(context.Context) ([]*firmware.BootloaderBuild, error)
	lookupOptions                                                  func(context.Context) (*firmware.BootOffers, error)
}

var loaderChoices = [3]string{"image", "standard", "new"}

func (s *screen) newBootloaderPicker(choice firmware.BootloaderChoice, changed func()) *bootloaderPicker {
	p := &bootloaderPicker{s: s, choice: choice, changed: changed, builds: map[string]*firmware.BootloaderBuild{}, rules: firmware.BootRules(), lookupOptions: firmware.FetchBootOffers}
	if p.choice.Choice == "" {
		p.choice.Choice = "image"
	}
	p.controls[0] = newBootDropdown(func(id string) { p.choice.Choice = id; p.choice.Build = p.builds[id]; p.updateChoice("bootloader") })
	p.controls[1] = newBootDropdown(func(id string) { p.choice.Sleep = id; p.updateChoice("sleep") })
	p.controls[2] = newBootDropdown(func(id string) { p.choice.Chain = id; p.updateChoice("chain") })
	p.rows = container.NewVBox()
	for n, title := range []string{"Bootloader", "Sleep firmware", "Boot chain"} {
		p.rowNotes[n] = widget.NewRichText()
		p.rowNotes[n].Wrapping = fyne.TextWrapWord
		p.rows.Add(container.NewVBox(container.NewBorder(nil, nil, wrapped(s.t(title)), nil, p.controls[n]), p.rowNotes[n]))
		if n == 1 {
			p.rows.Add(wrapped(s.t("Runs while the console sleeps and switches it off.")))
		}
		if n == 2 {
			p.rows.Add(wrapped(s.t("\"Fully mainline\" replaces the first start-up stage too.")))
		}
	}
	p.hint = wrapped("")
	p.warning = wrapped(s.t(loaderTestWarning))
	p.rows.Add(p.hint)
	p.rows.Add(p.warning)
	p.rows.Hide()
	p.toggle = widget.NewButton(s.t("Advanced boot options"), func() {
		if p.rows.Visible() {
			p.rows.Hide()
			p.toggle.SetIcon(theme.MenuExpandIcon())
		} else {
			p.rows.Show()
			p.toggle.SetIcon(theme.MenuDropDownIcon())
		}
	})
	p.toggle.SetIcon(theme.MenuExpandIcon())
	p.summary = wrapped("")
	p.box = container.NewVBox(p.toggle, wrapped(s.t(firmware.BootOptionsHint)), p.summary, p.rows)
	p.refresh()
	return p
}
func (p *bootloaderPicker) updateChoice(changed string) {
	p.choice, p.resetNotes = firmware.ResetBootOptions(p.choice, changed, p.target, p.rules)
	p.accepted = false
	p.refresh()
	if p.changed != nil {
		p.changed()
	}
}
func (p *bootloaderPicker) selectChoice(choice string) {
	p.choice.Choice = choice
	p.choice.Build = p.builds[choice]
	p.updateChoice("bootloader")
}
func (p *bootloaderPicker) setTarget(image, unsupported bool) {
	p.accepted = false
	p.choice.Force = false
	p.image, p.unsupported = image, unsupported
	if !p.cardTarget {
		p.target = firmware.ImageBootTarget(p.s.im, p.s.profile)
	}
	p.target.ImageAvailable = image
	p.target.Storage = "sd"
	if unsupported {
		p.target.Storage = "emmc"
	}
	p.choice, p.resetNotes = firmware.ResetBootOptions(p.choice, "target", p.target, p.rules)
	p.refresh()
}
func (p *bootloaderPicker) issues() []firmware.BootIssue {
	return firmware.EvaluateBootOptions(p.choice, p.target, p.rules)
}
func (p *bootloaderPicker) reason(i firmware.BootIssue) string {
	if strings.HasPrefix(i.Reason, "Reset: doesn't work with ") {
		other := strings.TrimPrefix(i.Reason, "Reset: doesn't work with ")
		if other == "mainline" {
			other = p.s.t("Fully mainline")
		}
		return p.s.t("Reset: doesn't work with %s", other)
	}
	if strings.HasPrefix(i.Reason, "Doesn't work with ") {
		return p.s.t("Doesn't work with %s.", strings.TrimSuffix(strings.TrimPrefix(i.Reason, "Doesn't work with "), "."))
	}
	return p.s.t(i.Reason)
}
func (p *bootloaderPicker) refresh() {
	p.setting = true
	defer func() { p.setting = false }()
	ids := [][]string{{"image", "standard", "new"}, {"image"}, {"standard", "mainline"}}
	labels := map[string]string{"image": p.s.t("As in the image"), "standard": p.s.t("Standard U-Boot, newest test build"), "new": p.s.t("New U-Boot, newest test build")}
	if p.catalog != nil {
		for _, e := range p.catalog.Entries {
			ids[1] = append(ids[1], e.ID)
			labels["sleep:"+e.ID] = p.s.t(e.Label)
		}
	}
	values := []string{p.choice.LoaderID(), p.choice.SleepID(), p.choice.ChainID()}
	for n, list := range ids {
		p.controls[n].options = nil
		for _, id := range list {
			title := labels[id]
			if n == 1 && id != "image" {
				title = labels["sleep:"+id]
			}
			if n == 2 {
				title = p.s.t("Standard")
				if id == "mainline" {
					title = p.s.t("Fully mainline")
				}
			}
			c := p.choice
			switch n {
			case 0:
				c.Choice = id
			case 1:
				c.Sleep = id
			case 2:
				c.Chain = id
			}
			reason := ""
			// Evaluate the result of the reset rule, so a valid later choice is
			// not disabled just because it invalidates a different row.
			c, _ = firmware.ResetBootOptions(c, []string{"bootloader", "sleep", "chain"}[n], p.target, p.rules)
			for _, i := range firmware.EvaluateBootOptions(c, p.target, p.rules) {
				if i.Level == "impossible" {
					reason = p.reason(i)
					break
				}
			}
			if n == 0 && id == "image" && !p.image {
				reason = p.s.t(firmware.BootloaderNeedImage)
			}
			if n == 0 && id != "image" && (p.builds[id] == nil || p.failed) {
				reason = p.s.t("Looking for bootloader builds…")
			}
			if n == 2 && id == "mainline" && (p.builds["new"] == nil || p.failed) {
				reason = p.s.t("Looking for bootloader builds…")
			}
			if p.unsupported {
				reason = p.s.t(firmware.BootloaderOnlySD)
			}
			p.controls[n].options = append(p.controls[n].options, bootMenuOption{id, title, reason})
			if id == values[n] {
				p.controls[n].SetText(title)
			}
		}
		p.controls[n].Enable()
		if p.unsupported {
			p.controls[n].Disable()
		}
		if n < 2 && p.choice.ChainID() == "mainline" {
			p.controls[n].SetText(p.s.t(firmware.BootOptionsEmbedded))
			p.controls[n].Disable()
		}
	}
	issues := p.issues()
	all := append(append([]firmware.BootIssue{}, p.resetNotes...), issues...)
	for n, row := range []string{"bootloader", "sleep", "chain"} {
		seg := []widget.RichTextSegment{}
		for _, i := range all {
			if i.Row != row {
				continue
			}
			style := widget.RichTextStyle{Inline: false}
			if i.Level == "broken" || i.Level == "impossible" {
				style.ColorName = theme.ColorNameWarning
			}
			seg = append(seg, &widget.TextSegment{Text: p.reason(i), Style: style})
		}
		p.rowNotes[n].Segments = seg
		p.rowNotes[n].Refresh()
	}
	notes := []string{}
	if p.unsupported {
		notes = append(notes, p.s.t(firmware.BootloaderOnlySD))
	} else if !p.image {
		notes = append(notes, p.s.t(firmware.BootloaderNeedImage))
	}
	if p.failed {
		notes = append(notes, p.s.t(firmware.BootloaderFetchFailed))
	}
	p.hint.SetText(strings.Join(notes, "\n"))

	if p.choice.Changed() {
		parts := []string{}
		for n, pair := range []struct{ key, id, def string }{{"Bootloader", p.choice.LoaderID(), "image"}, {"Sleep firmware", p.choice.SleepID(), "image"}, {"Boot chain", p.choice.ChainID(), "standard"}} {
			if pair.id != pair.def {
				label := pair.id
				for _, o := range p.controls[n].options {
					if o.id == pair.id {
						label = o.label
					}
				}
				parts = append(parts, p.s.t(pair.key)+": "+label)
			}
		}
		list := strings.Join(parts, ", ")
		for _, i := range issues {
			if i.Level == "untested" {
				list += " " + p.s.t("(untested combination)")
				break
			}
		}
		p.summary.SetText(p.s.t("Advanced boot options changed: %s", list))
		p.summary.Show()
	} else {
		p.summary.SetText("")
		p.summary.Hide()
	}

}
func (p *bootloaderPicker) selection() *firmware.BootloaderChoice {
	if p.unsupported {
		return nil
	}
	for _, i := range p.issues() {
		if i.Level == "impossible" {
			return nil
		}
	}
	c := p.choice
	c.Build = p.builds[c.Choice]
	if p.cardTarget || c.Changed() {
		c.Chain = c.ChainID()
	}
	if !p.cardTarget && !c.Changed() && p.target.CurrentChain != "mainline" {
		c.Chain = ""
		c.Sleep = ""
	}
	if c.ChainID() == "mainline" {
		if p.builds["new"] == nil || p.failed {
			return nil
		}
	} else if c.LoaderID() != "image" && c.Build == nil {
		return nil
	} else if c.LoaderID() == "image" && !p.image {
		return nil
	}
	return &c
}
func (p *bootloaderPicker) confirm(next func()) {
	if p.unsupported && !p.choice.Changed() {
		p.accepted = true
		next()
		return
	}
	reasons := []string{}
	for _, i := range p.issues() {
		if i.Level == "impossible" {
			p.s.showError(fmt.Errorf("%s", i.Reason))
			return
		}
		if i.Level == "broken" {
			reasons = append(reasons, p.reason(i))
		}
	}
	if len(reasons) == 0 {
		p.accepted = true
		next()
		return
	}
	dialog.ShowCustomConfirm(p.s.t(firmware.BootOptionsConflict), p.s.t("Flash anyway"), p.s.t("Go back"), wrapped(p.s.t(firmware.BootOptionsConflictBody, strings.Join(reasons, "\n"))), func(ok bool) {
		if ok {
			p.choice.Force = true
			p.accepted = true
			next()
		}
	}, p.s.win)
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
		var offers *firmware.BootOffers
		var e error
		if p.lookup != nil {
			b, err := p.lookup(ctx)
			e = err
			offers = &firmware.BootOffers{Builds: b, Rules: firmware.BootRules()}
		} else {
			offers, e = p.lookupOptions(ctx)
		}
		fyne.Do(func() {
			if ctx.Err() != nil {
				return
			}
			p.loading = false
			p.loaded = true
			p.failed = e != nil
			if offers != nil {
				p.catalog = offers.Catalog
				p.rules = offers.Rules
				for _, b := range offers.Builds {
					if b != nil {
						p.builds[b.Choice] = b
					}
				}
			}
			if e != nil {
				p.choice = firmware.BootloaderChoice{Choice: "image"}
			}
			p.choice, p.resetNotes = firmware.ResetBootOptions(p.choice, "catalog", p.target, p.rules)
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
		return s.t("Boot options") + ": " + s.t("File")
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
				s.info(s.t("Boot options"), s.t("No removable cards found. Insert a card and refresh."), s.win)
				return
			}
			pick := widget.NewSelect(labels, nil)
			pick.PlaceHolder = s.t("Select a microSD card")
			dialog.ShowCustomConfirm(s.t("Boot options"), s.t("Continue"), s.t("Cancel"), pick, func(ok bool) {
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
			picker.cardTarget = true
			picker.target = firmware.BootTarget{Storage: "sd", ImageAvailable: image != nil}
			if info.Setup != nil {
				picker.target.KernelLine = info.Setup.KernelLine
				picker.target.Profile = info.Setup.Profile
				if info.Setup.BootOptions != nil {
					picker.target.CurrentChain = info.Setup.BootOptions.Chain
				}
			}
			if info.BootTarget != nil {
				picker.target = *info.BootTarget
			}
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
				if !picker.accepted {
					picker.confirm(func() { apply.OnTapped() })
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
								if result.Bootloader.Options != nil {
									status.SetText(firmware.BootOptionsSummary(firmware.BootloaderChoice{Choice: result.Bootloader.Options.Bootloader, Chain: result.Bootloader.Options.Chain, Sleep: result.Bootloader.Options.Sleep}, result.Bootloader.Issues))
								} else {
									status.SetText(s.bootloaderSummary(result.Bootloader.Record))
								}
							}
						}
					})
				}()
			})
			apply.Importance = widget.HighImportance
			if picker.selection() == nil {
				apply.Disable()
			}
			s.win.SetContent(container.NewPadded(container.NewBorder(container.NewVBox(heading(s.t("Boot options")), wrapped(drive.Label())), container.NewHBox(cancel, apply), nil, nil, container.NewVScroll(container.NewVBox(picker.box, choose, status, bar)))))
			picker.load()
		})
	}()
}
