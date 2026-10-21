package ui

import (
	"context"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var Version = "development"

type screen struct {
	win                                 fyne.Window
	locale                              string
	started                             bool
	update                              *catalog.AppUpdate
	updateBanner                        *fyne.Container
	updateCancel                        context.CancelFunc
	language                            *widget.Button
	drives                              []platform.Drive
	updating                            bool
	home                                fyne.CanvasObject
	im                                  *firmware.Image
	drive                               *platform.Drive
	card                                *flash.Card
	profile                             string
	custom                              string
	customAccepted                      bool
	convert                             bool   // custom is the console's own DTB, converted while writing
	system                              string // a different system image (GSI), "" for andr36oid's
	systemAccepted                      bool
	experimentalOK                      map[string]bool // experimental profiles whose warning was agreed to
	allDrives                           bool            // advanced: fixed disks are listed too
	allCheck                            *widget.Check
	systemLabel                         *widget.Label
	busy                                bool
	releaseLabel, cardLabel, help       *widget.Label
	console, profileSelect, driveSelect *widget.Select
	mode                                *widget.RadioGroup
	noROMs                              *widget.Check
	action, local, online, refresh      *widget.Button
	customLabel                         *widget.Label
	step                                int
	pages                               []*fyne.Container
	stepButtons                         [stepCount]*widget.Button
	back, next                          *widget.Button
	storage                             *fyne.Container
	sizeLabel                           *widget.Label
	sizeSlider                          *widget.Slider
	sizeAuto                            *widget.Button
	sizeSetting                         bool // refreshStorage is moving the slider
	userdataGiB                         int  // Android size for a new card, 0: recommended
	sumValues                           [6]*widget.Label
	modeHelp                            *widget.Label
	progress                            *widget.ProgressBar
	cancel                              context.CancelFunc
}

func Run() {
	a := app.NewWithID("io.github.andr36oid.sdflasher")
	a.SetIcon(appIcon)
	a.Settings().SetTheme(uiTheme())
	w := a.NewWindow("andr36oid SD Flasher")
	s := &screen{win: w, locale: preferredLocale(a)}
	s.localizeDialogs()
	w.Resize(fyne.NewSize(820, 760))
	w.SetCloseIntercept(func() {
		if s.busy {
			s.info(s.t("Operation in progress"), s.t("Wait for the operation to finish before closing. Keep the card connected."), w)
			return
		}
		if s.updateCancel != nil {
			s.updateCancel()
		}
		w.Close()
	})
	if a.Preferences().Bool("sd-card-advice-v1") {
		s.start()
	} else {
		s.showCardAdvice()
	}
	w.ShowAndRun()
}
func (s *screen) t(key string, args ...any) string { return i18n.Text(s.locale, key, args...) }

// busyError: the card's volumes were in use and wouldn't unmount (platform.BusyPrefix)
func busyError(e error) (string, bool) {
	if e == nil {
		return "", false
	}
	return platform.Busy(e.Error())
}

func wrapped(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}
func (s *screen) build() {
	oldProfile, oldUpdate, oldROMs, oldStep := s.profile, s.updating, false, s.step
	if s.noROMs != nil {
		oldROMs = s.noROMs.Checked
	}
	s.action = nil
	s.pages = nil
	s.stepButtons = [stepCount]*widget.Button{}
	s.releaseLabel = wrapped(s.t("Choose a release to see its supported consoles and panels."))
	s.cardLabel = wrapped(s.t("Insert a microSD card, then select it below."))
	s.help = wrapped("")
	s.modeHelp = wrapped("")
	s.local = widget.NewButton(s.t("Choose image from disk…"), s.chooseImage)
	s.online = widget.NewButton(s.t("Download a release…"), s.releases)
	s.online.Importance = widget.HighImportance
	s.refresh = widget.NewButton(s.t("Refresh cards"), s.listDrives)
	s.console = widget.NewSelect(nil, func(name string) {
		s.profile = ""
		var opts []string
		if s.im != nil {
			for _, p := range s.im.Profiles {
				if p.For(name) {
					label := p.Name
					if p.Experimental {
						label += " (" + s.t("experimental") + ")"
					}
					opts = append(opts, label)
				}
			}
		}
		s.profileSelect.SetOptions(opts)
		s.profileSelect.ClearSelected()
		if len(opts) == 1 {
			s.profileSelect.SetSelected(opts[0])
		}
		s.ready()
	})
	s.console.PlaceHolder = s.t("Choose your console / board family")
	s.profileSelect = widget.NewSelect(nil, func(label string) {
		s.profile = ""
		var chosen *firmware.Profile
		if s.im != nil {
			for i, p := range s.im.Profiles {
				name := p.Name
				if p.Experimental {
					name += " (" + s.t("experimental") + ")"
				}
				if name == label {
					chosen = &s.im.Profiles[i]
				}
			}
		}
		if chosen != nil && chosen.Experimental {
			p := *chosen
			s.confirmExperimental(p, func(ok bool) {
				if ok {
					s.profile = p.ID
				} else {
					s.profileSelect.ClearSelected()
				}
				s.ready()
			})
		} else if chosen != nil {
			s.profile = chosen.ID
		}
		s.ready()
	})
	s.profileSelect.PlaceHolder = s.t("Choose your board and screen")
	s.allCheck = widget.NewCheck(s.t("Show all drives (advanced)"), nil)
	s.allCheck.SetChecked(s.allDrives)
	s.allCheck.OnChanged = s.setAllDrives
	s.driveSelect = widget.NewSelect(nil, nil)
	s.driveSelect.PlaceHolder = s.t("Select a microSD card")
	s.noROMs = widget.NewCheck(s.t("Use all available storage for Android"), func(bool) { s.ready() })
	s.buildStorage()
	s.mode = widget.NewRadioGroup([]string{s.t("Install a new card (erase)"), s.t("Update an existing installation")}, func(v string) {
		s.updating = v == s.t("Update an existing installation")
		if s.updating {
			s.noROMs.Disable()
			if s.card != nil {
				s.noROMs.SetChecked(s.card.NoROMs)
			}
			s.modeHelp.SetText(s.t("Apps, settings, saves, and games will be preserved."))
			s.modeHelp.Importance = widget.MediumImportance
			if s.card != nil && !s.card.Installed {
				s.modeHelp.SetText(s.t("New installation required") + ": " + s.card.Reason)
				s.modeHelp.Importance = widget.DangerImportance
			}
		} else {
			s.noROMs.Enable()
			s.modeHelp.SetText(s.t("Everything currently on this card will be erased."))
			if s.tooSmall() {
				s.modeHelp.SetText(s.tooSmallText())
			}
			s.modeHelp.Importance = widget.DangerImportance
		}
		s.modeHelp.Refresh()
		s.ready()
	})
	s.mode.SetSelected(s.t("Install a new card (erase)"))
	s.customLabel = wrapped(s.t("Use the DTB shipped with the selected profile."))
	custom := widget.NewButton(s.t("Choose custom Android DTB…"), s.chooseCustom)
	clear := widget.NewButton(s.t("Use shipped DTB"), func() {
		s.custom = ""
		s.customAccepted = false
		s.convert = false
		s.customLabel.SetText(s.dtbText())
		s.ready()
	})
	advanced := widget.NewAccordion(widget.NewAccordionItem(s.t("Advanced: custom DTB"), container.NewVBox(wrapped(s.t(firmware.CustomWarning)), container.NewHBox(custom, clear))))
	s.action = widget.NewButton(s.t("Continue"), s.confirm)
	s.action.Importance = widget.DangerImportance
	s.action.Disable()
	s.progress = widget.NewProgressBar()
	s.progress.Hide()
	s.sumValues = [6]*widget.Label{}
	s.pages = s.pagesFor(advanced)
	s.populateImage()
	s.profile = oldProfile
	s.selectProfile(oldProfile)
	s.noROMs.SetChecked(oldROMs)
	if oldUpdate {
		s.mode.SetSelected(s.t("Update an existing installation"))
	}
	s.customLabel.SetText(s.dtbText())
	s.populateDrives(false)
	s.cardDescription()
	var bar []fyne.CanvasObject
	for i := range s.stepButtons {
		step := i
		s.stepButtons[i] = widget.NewButton("", func() { s.showStep(step) })
		bar = append(bar, s.stepButtons[i])
	}
	s.back = widget.NewButton(s.t("Back"), func() { s.showStep(s.step - 1) })
	s.next = widget.NewButton(s.t("Next"), func() { s.showStep(s.step + 1) })
	s.next.Importance = widget.HighImportance
	s.language = widget.NewButton(s.languageName(), s.chooseLanguage)
	s.updateBanner = container.NewVBox()
	s.renderAppUpdate()
	header := container.NewVBox(
		container.NewBorder(nil, nil, nil, s.language, widget.NewLabelWithStyle("andr36oid SD Flasher", 0, fyne.TextStyle{Bold: true})),
		s.updateBanner,
		container.NewGridWithColumns(stepCount, bar...),
		widget.NewSeparator())
	var pages []fyne.CanvasObject
	for _, p := range s.pages {
		pages = append(pages, p)
	}
	footer := container.NewVBox(
		widget.NewSeparator(),
		container.NewBorder(nil, nil, s.back, s.next),
		container.NewBorder(nil, nil, widget.NewButton(s.t("Restore an interrupted update…"), s.recover), widget.NewLabel("GPLv3 · "+Version)))
	s.home = container.NewPadded(container.NewBorder(header, footer, nil, nil, container.NewVScroll(container.NewStack(pages...))))
	s.win.SetContent(s.home)
	s.showStep(oldStep)
	s.ready()
}
func (s *screen) ready() {
	if s.action == nil {
		return
	}
	if !s.busy && s.im != nil && s.drive != nil && s.card != nil && s.profile != "" && !(s.updating && !s.card.Installed) && !s.tooSmall() {
		s.action.Enable()
	} else {
		s.action.Disable()
	}
	s.refreshStorage()
	s.refreshSteps()
	if s.step == stepWrite {
		s.summarize()
	}
}
func (s *screen) setBusy(v bool) {
	s.busy = v
	for _, b := range []*widget.Button{s.local, s.online, s.refresh, s.language} {
		if v {
			b.Disable()
		} else {
			b.Enable()
		}
	}
	for _, x := range []*widget.Select{s.console, s.profileSelect, s.driveSelect} {
		if v {
			x.Disable()
		} else {
			x.Enable()
		}
	}
	if v {
		s.mode.Disable()
		s.noROMs.Disable()
	} else {
		s.mode.Enable()
		if !s.updating {
			s.noROMs.Enable()
		}
	}
	s.ready()
}
func (s *screen) chooseImage() {
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
		s.loadImage(p)
	}, s.win)
	// release images come packed: only .img.xz and .img.zip are listed
	d.SetFilter(storage.NewExtensionFileFilter([]string{".xz", ".zip"}))
	directory, _ := os.Getwd()
	if s.im != nil && s.im.Source != "" {
		directory = filepath.Dir(s.im.Source)
	}
	if location, err := storage.ListerForURI(storage.NewFileURI(directory)); err == nil {
		d.SetLocation(location)
	}
	d.Show()
}
func (s *screen) loadImage(p string) {
	// a build too old for this flasher: refused by its name, before it is unpacked
	if date, old := firmware.TooOld(p); old {
		s.releaseLabel.SetText(s.t("Image rejected. Choose an andr36oid Treble release."))
		s.info(s.t("This image is too old"), s.t("This image was built on %s. Builds from before 18 October 2026 don't work with this flasher. Download a current release instead.", date), s.win)
		return
	}
	s.setBusy(true)
	s.im = nil
	s.profile = ""
	s.custom = ""
	s.customAccepted = false
	s.convert = false
	s.customLabel.SetText(s.t("Use the DTB shipped with the selected profile."))
	s.releaseLabel.SetText(s.t("Checking the release image…"))
	s.progress.SetValue(0)
	s.progress.Show()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go func() {
		dir, e := jobs.CacheDir()
		var im *firmware.Image
		if e == nil {
			im, e = firmware.Load(ctx, p, dir, s.progressFn())
		}
		fyne.Do(func() {
			s.progress.Hide()
			s.setBusy(false)
			if e != nil {
				s.releaseLabel.SetText(s.t("Image rejected. Choose an andr36oid Treble release."))
				s.showError(e)
				return
			}
			s.im = im
			s.populateImage()
			s.autoProfile()
			if s.step == stepImage {
				s.showStep(stepCard)
			}
			s.ready()
		})
	}()
}
func (s *screen) progressFn() func(int64, int64) {
	return func(n, total int64) {
		fyne.Do(func() {
			if total > 0 {
				s.progress.SetValue(float64(n) / float64(total))
			}
		})
	}
}
func (s *screen) autoProfile() {
	if s.card != nil && s.card.Profile != "" {
		if !s.selectProfile(s.card.Profile) {
			s.profile = ""
			s.console.ClearSelected()
		}
		return
	}
	if s.profile == "" {
		s.selectProfile("Panels/Panel4")
	}
}
func (s *screen) selectProfile(id string) bool {
	if s.im == nil {
		return false
	}
	for _, p := range s.im.Profiles {
		if p.ID == id {
			s.console.SetSelected(p.Console)
			label := p.Name
			if p.Experimental {
				label += " (" + s.t("experimental") + ")"
			}
			s.profileSelect.SetSelected(label)
			return true
		}
	}
	return false
}
func (s *screen) populateImage() {
	if s.im == nil {
		return
	}
	s.releaseLabel.SetText(s.im.Version + " · " + s.t("%d device profiles", len(s.im.Profiles)) + "\n" + filepath.Base(s.im.Source))
	families := map[string]bool{}
	var opts []string
	for _, p := range s.im.Profiles {
		for _, c := range append([]string{p.Console}, p.AlsoFor...) {
			if !families[c] {
				families[c] = true
				opts = append(opts, c)
			}
		}
	}
	sort.Strings(opts)
	s.console.SetOptions(opts)
	s.console.ClearSelected()
}

// driveLabel is a drive as the list shows it: fixed disks are marked
func (s *screen) driveLabel(d platform.Drive) string {
	if d.Fixed {
		return s.t("Not a removable card") + ": " + d.Label()
	}
	return d.Label()
}

// setAllDrives switches listing fixed disks on (after the warning) or off.
func (s *screen) setAllDrives(on bool) {
	if !on {
		s.allDrives = false
		s.listDrives()
		return
	}
	if s.allDrives {
		return
	}
	s.agreeDialog(s.t("Show all drives?"), s.t("This also lists hard drives, SSDs and other disks that are not a USB card reader or an SD slot. Pick the wrong one and everything on it is gone for good: your files, other operating systems, backups.\n\nThe disk your computer runs from is never listed, but every other disk is. andr36oid only supports microSD cards, so nobody can help with a disk written this way.\n\nUse this only if you know exactly which disk is your card, for example a card reader that shows up as an internal disk."), s.t("I understand that choosing the wrong disk erases it, and that this is unsupported."), func(ok bool) {
		s.allDrives = ok
		if !ok {
			s.allCheck.OnChanged = nil
			s.allCheck.SetChecked(false)
			s.allCheck.OnChanged = s.setAllDrives
			return
		}
		s.listDrives()
	})
}

func (s *screen) populateDrives(reset bool) {
	var opts []string
	for _, d := range s.drives {
		opts = append(opts, s.driveLabel(d))
	}
	s.driveSelect.OnChanged = nil
	s.driveSelect.SetOptions(opts)
	if reset {
		s.driveSelect.ClearSelected()
		s.drive = nil
		s.card = nil
	} else if s.drive != nil {
		s.driveSelect.SetSelected(s.driveLabel(*s.drive))
	}
	s.driveSelect.OnChanged = func(label string) {
		for _, d := range s.drives {
			if s.driveLabel(d) == label {
				s.inspectDrive(d)
				break
			}
		}
	}
	if len(opts) == 0 {
		s.cardLabel.SetText(s.t("No removable cards found. Insert a card and refresh."))
	}
	s.ready()
}

// tooSmall: a new installation on a card under flash.MinCardBytes
func (s *screen) tooSmall() bool {
	return !s.updating && s.drive != nil && flash.TooSmall(s.drive.Size) != nil
}
func (s *screen) tooSmallText() string {
	return s.t("This card is too small. andr36oid needs a card of at least 16 GB; this one has %.1f GB.", float64(s.drive.Size)/1e9)
}
func (s *screen) cardDescription() {
	if s.card == nil {
		return
	}
	if !s.card.Installed && s.drive != nil && flash.TooSmall(s.drive.Size) != nil {
		s.cardLabel.SetText(s.tooSmallText())
		return
	}
	if s.card.Installed {
		key := s.t("Legacy andr36oid — will migrate to Treble")
		if s.card.Treble {
			key = s.t("Treble installation")
		}
		s.cardLabel.SetText(s.t(key) + ". " + s.t("Apps, settings, saves, and games will be preserved."))
	} else {
		s.cardLabel.SetText(s.t("New installation required") + ": " + s.card.Reason)
	}
}
func (s *screen) listDrives() {
	s.refresh.Disable()
	go func() {
		list := platform.List
		if s.allDrives {
			list = platform.ListAll
		}
		ds, e := list()
		fyne.Do(func() {
			s.refresh.Enable()
			if e != nil {
				s.cardLabel.SetText(s.t("Could not list cards") + ": " + e.Error())
				return
			}
			s.drives = ds
			s.populateDrives(true)
		})
	}()
}
func (s *screen) inspectDrive(d platform.Drive) {
	s.setBusy(true)
	s.drive = &d
	s.card = nil
	s.cardLabel.SetText(s.t("Inspecting the card. Your computer may request administrator access."))
	go func() {
		result, e := jobs.Run(jobs.Request{Action: "inspect", Drive: d}, nil)
		fyne.Do(func() {
			s.setBusy(false)
			if e != nil {
				s.cardLabel.SetText(s.t("Card inspection failed."))
				s.showError(e)
				return
			}
			s.card = result.Card
			if s.card == nil {
				return
			}
			if s.card.Installed {
				s.mode.SetSelected(s.t("Update an existing installation"))
				s.noROMs.SetChecked(s.card.NoROMs)
				s.autoProfile()
			} else {
				s.mode.SetSelected(s.t("Install a new card (erase)"))
			}
			s.cardDescription()
			s.ready()
		})
	}()
}
func (s *screen) chooseCustom() {
	if s.busy {
		return
	}
	s.confirmDialog(s.t("Custom Android DTB"), s.t(firmware.CustomWarning)+"\n\n"+s.t("Only the Android DTB is replaced. The selected shipped profile still supplies the bootloader display files. Continue only if this DTB was made for your console and this release."), func(ok bool) {
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
			s.custom = r.URI().Path()
			r.Close()
			s.customAccepted = true
			s.convert = false
			s.customLabel.SetText(s.t("Custom Android DTB") + ": " + filepath.Base(s.custom))
		}, s.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".dtb"}))
		d.Show()
	}, s.win)
}
func (s *screen) releases() {
	s.setBusy(true)
	s.releaseLabel.SetText(s.t("Checking GitHub releases…"))
	go func() {
		assets, e := catalog.List(context.Background())
		fyne.Do(func() {
			s.setBusy(false)
			if e != nil {
				s.showError(e)
				return
			}
			if len(assets) == 0 {
				s.info(s.t("No image downloads available"), s.t("No disk images are attached to andr36oid/releases yet. You can choose an image from disk."), s.win)
				return
			}
			var opts []string
			for _, a := range assets {
				opts = append(opts, a.Release+" · "+a.Name)
			}
			pick := widget.NewSelect(opts, nil)
			pick.PlaceHolder = s.t("Choose a release image")
			dialog.ShowCustomConfirm(s.t("Download a release"), s.t("Download"), s.t("Cancel"), container.NewVBox(wrapped(s.t("Only Treble images can be installed. Each download is checked before the card can be written.")), pick), func(ok bool) {
				if !ok {
					return
				}
				i := pick.SelectedIndex()
				if i < 0 {
					return
				}
				s.download(assets[i])
			}, s.win)
		})
	}()
}
func (s *screen) download(a catalog.Asset) {
	s.setBusy(true)
	s.progress.Show()
	s.progress.SetValue(0)
	s.releaseLabel.SetText(s.t("downloading") + ": " + a.Name)
	go func() {
		dir, e := jobs.CacheDir()
		var p string
		if e == nil {
			p, e = catalog.Download(context.Background(), a, dir, s.progressFn())
		}
		fyne.Do(func() {
			s.progress.Hide()
			s.setBusy(false)
			if e != nil {
				s.showError(e)
				return
			}
			s.loadImage(p)
		})
	}()
}
func (s *screen) confirm() {
	if s.im == nil || s.card == nil || s.drive == nil {
		return
	}
	mode := "install"
	if s.updating {
		mode = "update"
	}
	p, e := flash.Build(s.im, *s.card, s.drive.Size, mode, s.profile, s.noROMs.Checked, s.userdataGiB)
	if e != nil {
		s.showError(e)
		return
	}
	msg := s.drive.Label() + "\n\n" + s.t("Everything currently on this card will be erased.")
	title := s.t("Erase this card?")
	if mode == "update" {
		title = s.t("Update this card?")
		msg = s.drive.Label() + "\n\n" + s.t("BOOT, system, and vendor will be updated together. The dedicated cache will be cleared. Userdata and games stay in place. A recovery backup will be saved before writing.")
		if s.card.DataCard {
			msg = s.t("This is the data card of an eMMC install. Updating it makes it bootable again, and the console will start from it instead of the eMMC.") + "\n\n" + msg
		}
	}
	if s.convert {
		msg += "\n\n" + s.t(ownDTBWarning)
	} else if s.custom != "" {
		msg += "\n\n" + s.t(firmware.CustomWarning)
	}
	if len(s.drive.Volumes) > 0 {
		msg += "\n\n" + s.t("Drives of this card that are open on this computer (%s) are closed first.", strings.Join(s.drive.Volumes, ", "))
	}
	// a fixed disk: the plain question isn't enough
	if s.drive.Fixed {
		s.agreeDialog(s.t("Write to a disk that is not a removable card?"), s.t("%s is not a removable card. If it is the wrong disk, what is on it is destroyed and cannot be recovered. Check the name and the size once more before you go on.", s.drive.Label())+"\n\n"+msg, s.t("This is the right disk. Write to it."), func(ok bool) {
			if ok {
				s.write(p)
			}
		})
		return
	}
	s.confirmDialog(title, msg, func(ok bool) {
		if ok {
			s.write(p)
		}
	}, s.win)
}
func (s *screen) write(p *flash.Plan) {
	s.setBusy(true)
	m := newDiskMap()
	m.plan(p)
	title := heading(s.t("Writing your andr36oid card"))
	phase := wrapped(s.t("Preparing to write. Keep the card connected."))
	phase.TextStyle = fyne.TextStyle{Bold: true}
	outcome := wrapped("")
	outcome.Hide()
	bar := widget.NewProgressBar()
	details := wrapped(s.t("Each block summarizes a region of the card. Preserved regions are never written."))
	back := widget.NewButton(s.t("Back to setup"), func() {
		s.win.SetContent(s.home)
		s.card = nil
		s.drive = nil
		s.driveSelect.ClearSelected()
		s.showStep(stepCard)
		s.setBusy(false)
		s.listDrives()
	})
	back.Disable()
	back.Importance = widget.HighImportance
	// after a new installation: the card stays mounted for AUTOAPK until this
	drive := *s.drive
	var eject *widget.Button
	eject = widget.NewButton(s.t("Eject card"), func() {
		eject.Disable()
		go func() {
			e := platform.Eject(drive)
			fyne.Do(func() {
				if e != nil {
					eject.Enable()
					details.SetText(s.t("Safely eject the card and insert it into the console’s system card slot. The first boot prepares storage and restarts the console. Keep it powered on until Android setup appears. If the screen stays blank, check the chosen board and panel profile."))
					s.info(s.t("Eject card"), s.t("The card could not be ejected. Eject it with your computer’s own eject, then insert it into the console’s system card slot.")+"\n\n"+s.t("Technical details")+": "+e.Error(), s.win)
					return
				}
				eject.Hide()
				details.SetText(s.t("The card is ejected. Insert it into the console’s system card slot. The first boot prepares storage and restarts the console. Keep it powered on until Android setup appears. If the screen stays blank, check the chosen board and panel profile."))
			})
		}()
	})
	eject.Importance = widget.HighImportance
	eject.Hide()
	s.win.SetContent(container.NewPadded(container.NewBorder(container.NewVBox(title, widget.NewLabel(s.drive.Label()), widget.NewSeparator()), back, nil, nil,
		container.NewVScroll(container.NewVBox(phase, bar, outcome, m.img, legend(s.locale), details, eject)))))
	req := jobs.Request{Action: "write", Drive: *s.drive, Image: s.im.Path, ImageHash: s.im.SHA256, CardFingerprint: s.card.Fingerprint, Mode: p.Mode, Profile: s.profile, NoROMs: p.NoROMs, UserdataGiB: p.UserdataGiB, CustomDTB: s.custom, CustomAccepted: s.customAccepted, ConvertDTB: s.convert, SystemImage: s.system, SystemAccepted: s.systemAccepted}
	req.ImageSource = s.im.Source
	var speed *flash.CardSpeed
	go func() {
		result, e := jobs.Run(req, func(p flash.Progress) {
			fyne.Do(func() {
				if p.Speed != nil {
					speed = p.Speed
					return
				}
				m.update(p)
				phase.SetText(s.t(p.Phase) + ": " + s.t(p.Name))
				if p.Total > 0 {
					bar.SetValue(float64(p.Done) / float64(p.Total))
				}
			})
		})
		fyne.Do(func() {
			s.busy = false
			back.Enable()
			if detail, busy := busyError(e); busy {
				// nothing was written yet: back to the setup, and the user closes what uses the card
				s.win.SetContent(s.home)
				s.setBusy(false)
				s.ask(s.t("The card is in use"), s.t("The card can’t be written while it is open on this computer (%s). Close any windows or programs that show files from the card, or eject its drives, then try again.", detail), s.t("Try again"), func(ok bool) {
					if ok {
						s.write(p)
					}
				})
				return
			}
			if e != nil {
				title.SetText(s.t("Operation could not finish"))
				phase.SetText(s.t("Check the selected image and card, then try again."))
				outcome.SetText(s.t("Technical details") + ": " + e.Error())
				outcome.Importance = widget.DangerImportance
				outcome.Show()
				if result.Backup != "" {
					details.SetText(s.t("Recovery backups") + ": " + result.Backup)
				}
				return
			}
			bar.SetValue(1)
			title.SetText(s.t("Written and verified"))
			phase.SetText(s.t("Your card has been written and verified."))
			if speed != nil && speed.WriteMBps > 0 {
				outcome.SetText(s.t("Card speed: %.0f MB/s writing, %.1f ms per small read, %.1f ms per small write", speed.WriteMBps, speed.ReadLatency, speed.WriteLatency))
				if speed.Slow {
					outcome.SetText(s.t("This card is slow. Android runs from the card, so apps may start slowly and games may stutter. For a smooth experience, use a card from a known brand marked A1 or A2.") + "\n" + outcome.Text)
					outcome.Importance = widget.WarningImportance
				}
				outcome.Show()
			}
			if p.Mode == "install" {
				details.SetText(s.t("Want to add your own apps? Copy APK files to the AUTOAPK drive now, and the console offers them during setup. This only works before the console is set up: its first start moves AUTOAPK, and once the apps are installed, the space goes to Android."))
				eject.Show()
			} else {
				details.SetText(s.t("Your card has been written and verified. Apps, settings, saves, and games were preserved.") + "\n" + s.t("Recovery backups") + ": " + result.Backup)
			}
		})
	}()
}

func (s *screen) recover() {
	if s.busy {
		return
	}
	if s.drive == nil {
		s.info(s.t("Choose a card"), s.t("Select the card that was being updated first."), s.win)
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
		file := r.URI().Path()
		r.Close()
		if filepath.Base(file) != "recovery.json" {
			s.showError(fmt.Errorf("%s", s.t("Choose the recovery.json file saved by the interrupted update.")))
			return
		}
		drive := *s.drive
		s.confirmDialog(s.t("Restore previous installation"), drive.Label()+"\n\n"+s.t("Restore the saved operating system? Userdata and games remain protected."), func(ok bool) {
			if !ok {
				return
			}
			s.setBusy(true)
			s.cardLabel.SetText(s.t("Verifying and restoring the recovery backup…"))
			go func() {
				_, e := jobs.Run(jobs.Request{Action: "restore", Drive: drive, Recovery: filepath.Dir(file)}, nil)
				fyne.Do(func() {
					s.setBusy(false)
					if e != nil {
						s.showError(e)
					} else {
						s.info(s.t("Restored"), s.t("The previous installation was restored and verified."), s.win)
					}
					s.listDrives()
				})
			}()
		}, s.win)
	}, s.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	d.Show()
}
