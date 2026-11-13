package ui

import (
	"context"
	"errors"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"net"
	"path/filepath"
	"strings"
)

const dtbIntro = "Device trees tell the system which screen, buttons and sound chip your console has. A newer set can fix or add support for a console without writing the whole card again. Your games, saves and settings stay."
const dtbOnline = "Look for the set that belongs to this card online"
const dtbOffline = "Use a file I downloaded"
const dtbRestore = "Put the previous files back"
const dtbDone = "Put the card back into the console and start it. If the picture or the buttons are wrong afterwards, open this screen again and choose \"Put the previous files back\"."

func hasNetwork() bool {
	interfaces, _ := net.Interfaces()
	for _, i := range interfaces {
		if i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0 {
			return true
		}
	}
	return false
}
func (s *screen) chooseDTBPackCard() {
	if s.busy {
		return
	}
	s.setBusy(true)
	go func() {
		list := platform.List
		if s.allDrives {
			list = platform.ListAll
		}
		drives, e := list()
		fyne.Do(func() {
			s.setBusy(false)
			if e != nil {
				s.dtbInfo(s.t("Update device trees"), s.t(e.Error()), s.win)
				return
			}
			s.pickDTBPackCard(drives)
		})
	}()
}
func (s *screen) pickDTBPackCard(drives []platform.Drive) {
	if len(drives) == 0 {
		s.dtbInfo(s.t("Update device trees"), s.t("No removable cards found. Insert a card and refresh."), s.win)
		return
	}
	labels := []string{}
	for _, d := range drives {
		labels = append(labels, s.driveLabel(d))
	}
	pick := widget.NewSelect(labels, nil)
	pick.PlaceHolder = s.t("Select a microSD card")
	source := widget.NewRadioGroup([]string{s.t(dtbOnline), s.t(dtbOffline)}, nil)
	source.Required = true
	if hasNetwork() {
		source.SetSelected(s.t(dtbOnline))
	} else {
		source.SetSelected(s.t(dtbOffline))
	}
	file := ""
	var info jobs.Result
	var continueButton *widget.Button
	var popup *dialog.CustomDialog
	generation := 0
	closed := false
	fileLabel := wrapped("")
	status := wrapped("")
	ready := func() {
		if continueButton == nil {
			return
		}
		if !s.busy && info.Card != nil && pick.SelectedIndex() >= 0 && (source.Selected != s.t(dtbOffline) || file != "") {
			continueButton.Enable()
		} else {
			continueButton.Disable()
		}
	}
	choose := widget.NewButton(s.t("Choose file..."), func() {
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, e error) {
			if e != nil {
				s.dtbInfo(s.t("Update device trees"), s.t(e.Error()), s.win)
				return
			}
			if r == nil {
				return
			}
			file = r.URI().Path()
			r.Close()
			source.SetSelected(s.t(dtbOffline))
			fileLabel.SetText(filepath.Base(file))
			ready()
		}, s.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".zip"}))
		d.Show()
	})
	source.OnChanged = func(string) { ready() }
	pick.OnChanged = func(string) {
		generation++
		token := generation
		info = jobs.Result{}
		ready()
		s.setBusy(true)
		status.SetText(s.t("Working…"))
		drive := drives[pick.SelectedIndex()]
		go func() {
			result, e := jobs.Run(jobs.Request{Action: "dtb-info", Drive: drive}, nil)
			fyne.Do(func() {
				if closed || token != generation {
					return
				}
				s.setBusy(false)
				if e != nil {
					status.SetText(s.t(firmware.DTBPackNoBuild))
					ready()
					return
				}
				info = result
				status.SetText(result.BuildIdentity)
				opts := []string{s.t(dtbOnline), s.t(dtbOffline)}
				if result.Setup != nil && len(result.Setup.History) > 0 {
					opts = append(opts, s.t(dtbRestore))
				}
				source.Options = opts
				source.Refresh()
				ready()
			})
		}()
	}
	cancel := widget.NewButton(s.t("Cancel"), func() {
		if s.busy {
			return
		}
		closed = true
		popup.Hide()
	})
	continueButton = widget.NewButton(s.t("Continue"), func() {
		if s.busy || info.Card == nil {
			return
		}
		closed = true
		popup.Hide()
		req := jobs.Request{Action: "dtb-plan", Drive: drives[pick.SelectedIndex()], DTBFile: file, DTBOnline: source.Selected == s.t(dtbOnline), DTBExpected: info.BuildIdentity, CardFingerprint: info.Card.Fingerprint}
		if source.Selected == s.t(dtbRestore) {
			req.Action = "dtb-restore-plan"
			s.previewDTBRestore(req)
			return
		}
		s.previewDTBRequest(req)
	})
	continueButton.Importance = widget.HighImportance
	continueButton.Disable()
	popup = dialog.NewCustomWithoutButtons(s.t("Update device trees"), container.NewVBox(wrapped(s.t(dtbIntro)), widget.NewForm(widget.NewFormItem(s.t("Card"), pick)), source, choose, fileLabel, status, container.NewHBox(cancel, continueButton)), s.win)
	popup.Show()
}
func (s *screen) previewDTBRequest(req jobs.Request) {
	if s.busy {
		return
	}
	s.setBusy(true)
	go func() {
		result, e := jobs.Run(req, nil)
		fyne.Do(func() {
			s.setBusy(false)
			if e != nil {
				s.dtbInfo(s.t("Update device trees"), s.t(e.Error()), s.win)
				return
			}
			if result.DTBPlan == nil || result.DTBPack == nil {
				s.showError(errors.New(firmware.DTBPackInvalid))
				return
			}
			plan := result.DTBPlan
			pack := result.DTBPack
			if plan.Already && !plan.Manual {
				s.dtbInfo(s.t("Update device trees"), s.t(firmware.DTBPackAlready), s.win)
				return
			}
			req.DTBFile = pack.Path
			req.DTBManifest = pack.ManifestPath
			req.DTBOnline = false
			current := s.t("no change")
			if plan.Changed {
				current = s.t("files change")
			}
			if plan.Profile == "" {
				current = s.t("default console (no profile chosen)")
			}
			other := s.t("nothing to update")
			if plan.Updated != 0 || plan.Added != 0 {
				other = s.t("%d updated, %d new", plan.Updated, plan.Added)
			}
			pick := s.dtbProfileChoices(plan, func(profile string) { req.DTBOptions.Profile = profile; s.previewDTBRequest(req) })
			var popup *dialog.CustomDialog
			pick.apply = func(profile string) { req.DTBOptions.Profile = profile; popup.Hide(); s.previewDTBRequest(req) }

			body := container.NewVBox(widget.NewForm(widget.NewFormItem(s.t("Card"), wrapped(plan.Setup.Build+", "+plan.Setup.KernelLine)), widget.NewFormItem(s.t("Console"), pick.box), widget.NewFormItem(s.t("Your console"), wrapped(current)), widget.NewFormItem(s.t("Other consoles"), wrapped(other))), wrapped(s.t("The files in use now are kept on the card, so this can be undone.")))
			if plan.Setup.Revision > 0 && pack.Manifest.Revision > plan.Setup.Revision {
				body.Add(wrapped(s.t("A newer set exists.")))
			}
			apply := func() {
				s.confirmDTBScreen(plan, func() {
					req.Action = "dtb-apply"
					req.DTBOptions.ScreenAccepted = plan.ScreenChanged
					s.writeDTBRequest(req, false, plan)
				})
			}
			button := widget.NewButton(s.t("Apply"), func() {
				popup.Hide()
				if plan.Manual {
					dialog.ShowCustomConfirm(s.t("The console files on this card were changed by hand"), s.t("Replace them"), s.t("Cancel"), wrapped(s.t("The files this card starts with are not the ones its console profile normally uses. Applying the set replaces them with the standard files for %s. The current files are kept on the card.", plan.Profile)), func(ok bool) {
						if ok {
							req.DTBOptions.ReplaceAnyway = true
							apply()
						}
					}, s.win)
				} else {
					apply()
				}
			})
			popup = dialog.NewCustomWithoutButtons(s.t("Device trees for %s", pack.Manifest.Version)+fmt.Sprintf(" · r%d", pack.Manifest.Revision), container.NewVBox(body, container.NewHBox(widget.NewButton(s.t("Cancel"), func() { popup.Hide() }), button)), s.win)
			popup.Show()
		})
	}()
}
func (s *screen) writeDTBRequest(req jobs.Request, restore bool, plan *firmware.DTBPlan) {
	if s.busy {
		return
	}
	s.setBusy(true)
	previous := s.win.Content()
	bar := widget.NewProgressBar()
	s.win.SetContent(container.NewPadded(container.NewVBox(heading(s.t("Update device trees")), wrapped(s.t("Writing device trees...")), bar)))
	go func() {
		result, e := jobs.Run(req, func(p flash.Progress) {
			if p.Total > 0 {
				fyne.Do(func() { bar.SetValue(float64(p.Done) / float64(p.Total)) })
			}
		})
		fyne.Do(func() {
			bar.SetValue(1)
			s.setBusy(false)
			s.win.SetContent(previous)
			if e != nil {
				if strings.Contains(e.Error(), firmware.ErrDTBRollback.Error()) {
					s.dtbInfo(s.t("Update device trees"), s.t(e.Error()), s.win)
				} else {
					s.dtbInfo(s.t("Update device trees"), s.t("Writing stopped: %s. The card still starts with its previous files.", s.t(e.Error())), s.win)
				}
				return
			}
			if result.DTBResult != nil && result.DTBResult.Already {
				s.dtbInfo(s.t("Update device trees"), s.t(firmware.DTBPackAlready), s.win)
				return
			}
			if result.DTBResult != nil && result.DTBResult.ScreenChanged {
				title := "Device trees updated"
				if restore {
					title = "Previous device trees are back"
				}
				s.dtbInfo(s.t(title), s.t(firmware.DTBScreenSuccess), s.win)
			} else if restore {
				s.dtbInfo(s.t("Previous device trees are back"), s.t("The card starts with the files it had before the update."), s.win)
			} else {
				s.dtbInfo(s.t("Device trees updated"), s.t(dtbDone), s.win)
			}
		})
	}()
}
func (s *screen) imageDTBSection() *fyne.Container {
	s.dtbLabel = wrapped("")
	if s.im != nil && s.im.DTBVersion != "" {
		s.dtbLabel.SetText(s.t("Using device trees from %s", s.im.DTBVersion))
	}
	change := widget.NewButton(s.t("Change..."), func() {
		if s.busy || s.im == nil {
			return
		}
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, e error) {
			if e != nil {
				s.dtbInfo(s.t("Update device trees"), s.t(e.Error()), s.win)
				return
			}
			if r == nil {
				return
			}
			file := r.URI().Path()
			r.Close()
			s.loadImageDTBs(file, false)
		}, s.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".zip"}))
		d.Show()
	})
	return container.NewBorder(nil, nil, nil, change, s.dtbLabel)
}
func (s *screen) lookupImageDTBs() {
	if !s.started || s.im == nil {
		return
	}
	key := s.im.SHA256 + s.im.Path
	if key == s.dtbLookup {
		return
	}
	s.dtbLookup = key
	s.dtbFile = ""
	s.dtbManifest = ""
	s.loadImageDTBs("", true)
}
func (s *screen) loadImageDTBs(file string, optional bool) {
	if s.im == nil {
		return
	}
	image := s.im
	go func() {
		var p *firmware.DTBPack
		var e error
		dir, e := jobs.CacheDir()
		if e == nil {
			if file != "" {
				p, e = firmware.ReadDTBPack(file)
			} else {
				setup, err := firmware.ImageDTBSetup(image)
				e = err
				if e == nil {
					p, e = firmware.FetchDTBPack(context.Background(), setup, dir)
				}
			}
		}
		var updated *firmware.Image
		if e == nil {
			updated, e = firmware.ImageWithDTBPack(image, p, dir)
		}
		fyne.Do(func() {
			if s.im != image || s.busy {
				return
			}
			if e != nil {
				if !optional {
					s.dtbInfo(s.t("Update device trees"), s.t(e.Error()), s.win)
				}
				return
			}
			s.im = updated
			s.dtbFile = p.Path
			s.dtbManifest = p.ManifestPath
			if s.dtbLabel != nil {
				s.dtbLabel.SetText(s.t("Using device trees from %s", updated.DTBVersion))
			}
			s.populateImage()
			s.autoProfile()
			s.ready()
		})
	}()
}

func (s *screen) confirmDTBScreen(plan *firmware.DTBPlan, done func()) {
	if plan == nil || !plan.ScreenChanged {
		done()
		return
	}
	d := dialog.NewCustomConfirm(s.t(firmware.DTBScreenWarningTitle), s.t("I'll watch the screen"), s.t("Cancel"), container.NewVScroll(wrapped(s.t(firmware.DTBScreenWarning))), func(ok bool) {
		if ok {
			done()
		}
	}, s.win)
	d.Resize(fyne.NewSize(640, 520))
	d.Show()
}
func (s *screen) previewDTBRestore(req jobs.Request) {
	if s.busy {
		return
	}
	s.setBusy(true)
	go func() {
		result, e := jobs.Run(req, nil)
		fyne.Do(func() {
			s.setBusy(false)
			if e != nil {
				s.dtbInfo(s.t("Update device trees"), s.t(e.Error()), s.win)
				return
			}
			if result.DTBPlan == nil {
				return
			}
			s.confirmDTBScreen(result.DTBPlan, func() {
				req.Action = "dtb-restore"
				req.DTBOptions.ScreenAccepted = result.DTBPlan.ScreenChanged
				s.writeDTBRequest(req, true, result.DTBPlan)
			})
		})
	}()
}

type dtbProfilePicker struct {
	box   *fyne.Container
	apply func(string)
}

func profileCaption(s *screen, p firmware.Profile) string {
	label := p.Name
	if p.Experimental {
		label += " (" + s.t("experimental") + ")"
	}
	return label
}
func knownDTBProfiles(plan *firmware.DTBPlan) []firmware.Profile {
	console := ""
	for _, p := range plan.Profiles {
		if p.ID == plan.Setup.Profile {
			console = p.Console
			break
		}
	}
	result := []firmware.Profile{}
	if console == "" {
		return result
	}
	for _, p := range plan.Profiles {
		if p.For(console) {
			result = append(result, p)
		}
	}
	return result
}
func (s *screen) dtbProfileChoices(plan *firmware.DTBPlan, apply func(string)) *dtbProfilePicker {
	out := &dtbProfilePicker{apply: apply}
	makeSelect := func(profiles []firmware.Profile) *widget.Select {
		labels := []string{}
		selected := -1
		for i, p := range profiles {
			labels = append(labels, profileCaption(s, p))
			if p.ID == plan.Profile {
				selected = i
			}
		}
		pick := widget.NewSelect(labels, nil)
		if selected >= 0 {
			pick.SetSelectedIndex(selected)
		}
		if len(labels) == 0 {
			pick.Disable()
		}
		pick.OnChanged = func(string) {
			if n := pick.SelectedIndex(); n >= 0 && out.apply != nil {
				out.apply(profiles[n].ID)
			}
		}
		return pick
	}
	known := knownDTBProfiles(plan)
	all := container.NewVBox(heading(s.t("All screens")), makeSelect(plan.Profiles))
	all.Hide()
	for _, p := range plan.Profiles {
		if p.ID == plan.Profile {
			inKnown := false
			for _, k := range known {
				if k.ID == p.ID {
					inKnown = true
				}
			}
			if !inKnown && plan.Profile != plan.Setup.Profile {
				all.Show()
			}
		}
	}
	show := widget.NewButton(s.t("Show all screens"), func() { all.Show() })
	out.box = container.NewVBox(wrapped(plan.Profile), heading(s.t("Known to fit this console")), makeSelect(known), show, wrapped(s.t("Only choose from here if you know which screen your console has.")), all)
	return out
}

func (s *screen) dtbInfo(title, message string, parent fyne.Window) {
	d := dialog.NewCustom(title, s.t("Close"), wrapped(message), parent)
	fitDialog(d, message, parent)
	d.Show()
}
func (s *screen) writeWithDTBWarning(p *flash.Plan) {
	if s.im != nil && s.im.DTBPack != nil && s.custom == "" {
		for _, profile := range s.im.Profiles {
			if profile.ID == s.profile && profile.DTBScreenChanged {
				s.confirmDTBScreen(&firmware.DTBPlan{ScreenChanged: true}, func() { s.write(p) })
				return
			}
		}
	}
	s.write(p)
}
