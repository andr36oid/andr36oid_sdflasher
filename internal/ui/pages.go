package ui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// stepButtons are a step page's Back and Next.
func (s *screen) stepButtons(step int) (*widget.Button, *widget.Button) {
	var back *widget.Button
	if step > 0 {
		back = widget.NewButton(s.t("Back"), func() { s.showStep(step - 1) })
		s.backs[step] = back
	}
	next := widget.NewButton(s.t("Next"), func() { s.next(step) })
	next.Importance = widget.HighImportance
	s.nexts[step] = next
	return back, next
}

// 1. Start: what the user wants to do.

func (s *screen) startPage() fyne.CanvasObject {
	texts := [3][2]string{
		{"Set up a new card", "Erases the card and installs andr36oid."},
		{"Update a card", "Keeps apps, settings, saves and games."},
		{"Fix or change a card", "Hotfixes, device trees, kernel, boot options, restore an interrupted update."},
	}
	list := container.NewVBox()
	for i, x := range texts {
		task := i
		s.startCards[i] = newChoiceCard(s.t(x[0]), s.t(x[1]), func() { s.setTask(task) })
		list.Add(s.startCards[i])
	}
	s.setTask(s.task)
	_, next := s.stepButtons(stepStart)
	return pageView(title(s.t("What do you want to do?")), wrapped(s.t("Nothing is written to a card before the last step.")), list, nil, next)
}

// setTask chooses between a new card, an update and the fix flows.
func (s *screen) setTask(task int) {
	s.task = task
	for i, c := range s.startCards {
		if c != nil {
			c.setSelected(i == task)
		}
	}
	if task == taskFix {
		s.ready()
		return
	}
	updating := task == taskUpdate
	if updating != s.updating {
		s.eraseOK = false
	}
	s.updating = updating
	if s.updating && s.card != nil {
		s.noROMs.SetChecked(s.card.NoROMs)
	}
	if s.noROMs != nil {
		if s.updating || s.busy {
			s.noROMs.Disable()
		} else {
			s.noROMs.Enable()
		}
	}
	s.refreshCards()
	s.ready()
}

// 2. Release: the newest is chosen; another one or a file from disk on request.

func (s *screen) releasePage() fyne.CanvasObject {
	s.releaseCard = newChoiceCard("", "", nil)
	s.releaseCard.selected = true
	s.releaseLabel = wrapped("")
	s.progress = widget.NewProgressBar()
	s.progress.Hide()
	other := quiet(s.t("Other releases..."), s.releases)
	local := quiet(s.t("Use a file from this computer..."), s.chooseImage)
	s.stopLoad = widget.NewButton(s.t("Stop"), func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	s.stopLoad.Hide()
	back, next := s.stepButtons(stepImage)
	body := container.NewVBox(s.releaseCard, container.NewHBox(other, layout.NewSpacer(), local), s.releaseLabel, s.progress)
	return pageView(title(s.t("Which release?")), wrapped(s.t("The newest release is chosen for you.")), body, nil, back, s.stopLoad, next)
}

func (s *screen) refreshRelease() {
	if s.releaseCard == nil {
		return
	}
	c := s.releaseCard
	switch {
	case s.im != nil && (s.asset == nil || s.im.Source == s.asset.URL):
		c.setText(shown(s.im.Version), filepath.Base(s.im.Source)+" - "+s.t("%d device profiles", len(s.im.Profiles)))
		c.setTag("")
	case s.asset != nil:
		detail := s.t("%s to download", humanBytes(s.asset.Size))
		if k, g, ok := catalog.Variant(s.asset.Name); ok {
			detail += " - " + s.variantText(k, g)
		}
		c.setText(s.asset.Release, detail+"\n"+s.asset.Name)
		c.setTag("")
		if len(s.assets) > 0 && s.assets[0].URL == s.asset.URL {
			c.setTag(s.t("newest"))
		}
	case s.assetsLooking:
		c.setText(s.t("Checking GitHub releases..."), "")
		c.setTag("")
	case s.assetsErr != nil:
		c.setText(s.t("The list of releases could not be loaded."), s.explain(s.assetsErr)+"\n"+s.t("Use a file from this computer, or try Other releases again."))
		c.setTag("")
	default:
		c.setText(s.t("Choose a release to see its supported consoles and panels."), "")
		c.setTag("")
	}
	// Stop is there while a release is downloaded or checked, nothing is written then
	if s.stopLoad != nil {
		if s.busy && s.cancel != nil && s.progress.Visible() {
			s.stopLoad.Show()
		} else {
			s.stopLoad.Hide()
		}
	}
}

// 3. Card: every drive as a row; what is on the chosen card on its row.

func (s *screen) cardPage() fyne.CanvasObject {
	s.cardRows = container.NewVBox()
	s.cardLabel = wrapped("")
	s.cardNote = container.NewVBox()
	s.refresh = quiet(s.t("Refresh cards"), s.listDrives)
	s.allCheck = widget.NewCheck(s.t("Show all drives (advanced)"), nil)
	s.allCheck.SetChecked(s.allDrives)
	s.allCheck.OnChanged = s.setAllDrives
	back, next := s.stepButtons(stepCard)
	body := container.NewVBox(s.cardRows, s.cardLabel, s.cardNote, container.NewHBox(s.refresh), s.allCheck)
	return pageView(title(s.t("Which card?")), wrapped(s.t("Put the microSD card into this computer.")+" "+s.t("The card is only read now. Nothing is written before the last step.")), body, nil, back, next)
}

// builtInSlot: the computer's own SD slot, which Linux names mmcblk and macOS
// "Built In SDXC Reader".
func builtInSlot(d platform.Drive) bool {
	return strings.HasPrefix(filepath.Base(d.Path), "mmcblk") || strings.Contains(strings.ToLower(d.Name), "built in sd")
}

// driveTitle is a drive in words: what it is and its size.
func (s *screen) driveTitle(d platform.Drive) string {
	name := shown(d.Name)
	if builtInSlot(d) {
		name = s.t("SD card in the built-in slot")
	}
	return fmt.Sprintf("%s - %.1f GB", name, float64(d.Size)/1e9)
}

// driveLabel is a drive with its device path, where one line has to say it all.
func (s *screen) driveLabel(d platform.Drive) string {
	label := s.driveTitle(d) + " - " + d.Path
	if d.Fixed {
		label = s.t("Not a removable card") + ": " + label
	}
	if d.WriteProtected {
		label += " - " + s.t(platform.WriteProtectedReason)
	}
	return label
}

// driveUsable: the row can be chosen.
func (s *screen) driveUsable(d platform.Drive) bool {
	return !d.WriteProtected && (!d.External || s.allDrives)
}

func (s *screen) chosen(d platform.Drive) bool {
	return s.drive != nil && platform.Same(*s.drive, d)
}

// cardText says what is on the inspected card.
func (s *screen) cardText() string {
	c := s.card
	switch {
	case c == nil:
		return ""
	case c.Installed && !c.Treble:
		return s.t("Legacy andr36oid - will migrate to Treble")
	case c.Installed && c.DataCard:
		return s.t("Holds the data of an andr36oid eMMC install")
	case c.Installed:
		name := c.Profile
		if s.im != nil {
			for _, p := range s.im.Profiles {
				if p.ID == c.Profile {
					name = shown(p.Name)
				}
			}
		}
		if name == "" {
			return s.t("Holds andr36oid")
		}
		return s.t("Holds andr36oid for %s", name)
	}
	return s.t("New installation required") + ": " + c.Reason
}

func (s *screen) refreshCards() {
	if s.cardRows == nil {
		return
	}
	s.cardRows.RemoveAll()
	for _, d := range s.drives {
		d := d
		var lines []string
		if d.WriteProtected {
			lines = append(lines, s.t("This card can't be used: %s.", s.t(platform.WriteProtectedReason)))
		}
		if s.chosen(d) {
			if s.inspecting {
				lines = append(lines, s.t("Inspecting the card. Your computer may request administrator access."))
			} else if text := s.cardText(); text != "" {
				lines = append(lines, text)
			}
		}
		// an andr36oid card, known from the partition names of the listing (no
		// administrator rights needed): updating it is the way to keep its data
		ours := !s.updating && s.driveUsable(d) && d.LooksLikeAndr36oid() && !(s.chosen(d) && s.card != nil && !s.card.Installed)
		if ours && !(s.chosen(d) && s.card != nil) {
			lines = append(lines, s.t("Holds andr36oid"))
		}
		lines = append(lines, d.Path)
		row := newChoiceCard(s.driveTitle(d), strings.Join(lines, "\n"), func() { s.pickDrive(d) })
		if ours && !s.busy {
			update := widget.NewButton(s.t("Update it instead"), func() {
				s.setTask(taskUpdate)
				s.pickDrive(d)
			})
			update.Importance = widget.HighImportance
			row.setAction(update)
		}
		switch {
		case d.Fixed:
			row.setTag(s.t("Not a removable card"))
		case d.External:
			row.setTag(s.t("not a card"))
		}
		row.setSelected(s.chosen(d))
		row.setDisabled(s.busy || !s.driveUsable(d))
		s.cardRows.Add(row)
	}
	switch {
	case s.listing:
		s.cardLabel.SetText(s.t("Looking for cards..."))
	case s.listed && len(s.drives) == 0:
		s.cardLabel.SetText(s.t("No removable cards found. Insert a card and refresh."))
	case s.listed && s.drive == nil:
		s.cardLabel.SetText(s.t("Insert a microSD card, then select it below."))
	default:
		s.cardLabel.SetText("")
	}
	s.cardLabel.Importance = widget.MediumImportance
	if s.cardLabel.Text == "" {
		s.cardLabel.Hide()
	} else {
		s.cardLabel.Show()
	}
}

// refreshCardNote says, under the rows, when the chosen card doesn't fit the task,
// with the way on.
func (s *screen) refreshCardNote() {
	if s.cardNote == nil {
		return
	}
	s.cardNote.RemoveAll()
	if s.card == nil || s.drive == nil || s.inspecting {
		return
	}
	switch {
	case s.updating && !s.card.Installed:
		s.cardNote.Add(noteCard(noteWarning, wrapped(s.t("This card has no andr36oid to update.")+" "+s.t("Choose another card, or set this one up as a new card. That erases it.")),
			container.NewHBox(widget.NewButton(s.t("Set it up as a new card"), func() { s.setTask(taskNew) }))))
	case s.tooSmall():
		s.cardNote.Add(noteCard(noteError, wrapped(s.tooSmallText())))
	case !s.updating && s.card.Installed:
		note := []fyne.CanvasObject{wrapped(s.t("This card already holds andr36oid. Setting it up again erases everything on it, including apps, saves and games. To keep them, update it instead."))}
		if !s.drive.LooksLikeAndr36oid() {
			// otherwise the card's row has the button
			note = append(note, container.NewHBox(widget.NewButton(s.t("Update it instead"), func() { s.setTask(taskUpdate) })))
		}
		s.cardNote.Add(noteCard(notePlain, note...))
	case s.updating && s.card.DataCard:
		s.cardNote.Add(noteCard(notePlain, wrapped(s.t("This is the data card of an eMMC install. Updating it makes it bootable again, and the console will start from it instead of the eMMC.")+" "+s.t(dataCardFirstStart))))
	}
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

// listDrives looks for cards again. The chosen card stays chosen while it is there.
func (s *screen) listDrives() {
	if s.listing {
		return
	}
	s.listing = true
	s.refreshCards()
	if s.refresh != nil {
		s.refresh.Disable()
	}
	all := s.allDrives
	background(func() {
		ds, e := listDrives(all)
		fyne.Do(func() {
			s.listing = false
			s.listed = true
			if s.refresh != nil && !s.busy {
				s.refresh.Enable()
			}
			if e != nil {
				s.logf("listing drives failed: %v", e)
				s.refreshCards()
				s.cardLabel.SetText(s.t("Could not list cards") + ": " + e.Error())
				s.cardLabel.Show()
				return
			}
			s.logf("%d drive(s) found", len(ds))
			for _, d := range ds {
				s.logf("  %s (%s, %d bytes, fixed %v, external %v, write-protected %v)", d.Path, d.Name, d.Size, d.Fixed, d.External, d.WriteProtected)
			}
			s.setDrives(ds)
		})
	})
}

// listDrives lists the cards, or every drive with all; background runs slow work
// off the window's goroutine. The tests replace both.
var listDrives = func(all bool) ([]platform.Drive, error) {
	if all {
		return platform.ListAll()
	}
	return platform.List()
}

var background = func(f func()) { go f() }

// setDrives shows a new listing; a chosen card that is gone is no longer chosen.
func (s *screen) setDrives(ds []platform.Drive) {
	s.drives, s.listed = ds, true
	if s.drive != nil {
		still := false
		for _, d := range ds {
			if platform.Same(d, *s.drive) && s.driveUsable(d) {
				still = true
			}
		}
		if !still {
			s.drive, s.card = nil, nil
		}
	}
	s.refreshCards()
	s.ready()
}

func (s *screen) pickDrive(d platform.Drive) {
	if s.busy || !s.driveUsable(d) || (s.chosen(d) && s.card != nil) {
		return
	}
	s.inspectDrive(d)
}

func (s *screen) inspectDrive(d platform.Drive) {
	s.drive = &d
	s.card = nil
	s.eraseOK = false
	s.inspecting = true
	s.logf("inspecting %s", d.Path)
	s.setBusy(true)
	go func() {
		result, e := jobs.Run(jobs.Request{Action: "inspect", Drive: d}, nil)
		fyne.Do(func() {
			s.inspecting = false
			if e != nil {
				s.logf("inspection failed: %v", e)
				s.drive = nil
				s.setBusy(false)
				s.showError(e)
				return
			}
			s.card = result.Card
			if s.card != nil {
				s.logf("card: installed %v, treble %v, profile %q, data card %v", s.card.Installed, s.card.Treble, s.card.Profile, s.card.DataCard)
				if s.card.Installed && s.updating {
					s.noROMs.SetChecked(s.card.NoROMs)
				}
				if s.card.Installed {
					s.autoProfile()
				}
			}
			s.setBusy(false)
		})
	}()
}

// 4. Console: a search box and two groups, tested first.

func (s *screen) consolePage() fyne.CanvasObject {
	s.search = widget.NewEntry()
	s.search.SetPlaceHolder(s.t("Search"))
	s.search.OnChanged = func(string) { s.refreshConsoles() }
	s.consoleLead = wrapped("")
	s.consoleList = container.NewVBox()
	s.expBox = container.NewVBox()
	unknown := quiet(s.t("I don’t know my board or panel"), func() {
		s.info(s.t("Finding your board and panel"), s.t("An existing andr36oid card may identify its selected profile. For a new card, use your console’s board markings and the panel information from its supplier. Similar-looking R36S consoles can have different wiring. Choose the board family first; a panel number alone is not enough. If your console is missing here, this release does not ship its profile."), s.win)
	})
	own := quiet(s.t(ownDTBButton), s.chooseOwnDTB)
	s.customLabel = wrapped(s.dtbText())
	s.customClear = quiet(s.t("Use shipped DTB"), s.clearCustom)
	back, next := s.stepButtons(stepConsole)
	body := container.NewVBox(s.search, s.consoleList, container.NewHBox(unknown, layout.NewSpacer(), own),
		container.NewBorder(nil, nil, nil, s.customClear, s.customLabel))
	// the experimental warning sits above the buttons: its tick box and Next are
	// always in sight, its text scrolls inside the card
	return pageView(title(s.t("Which console?")), s.consoleLead, body, s.expBox, back, next)
}

// consoleGroups are the release's profiles that match the search, tested and
// experimental apart, in the release's order.
func consoleGroups(profiles []firmware.Profile, query string) (tested, experimental []firmware.Profile) {
	words := strings.Fields(strings.ToLower(query))
	for _, p := range profiles {
		text := strings.ToLower(shown(p.Name) + " " + p.Name + " " + p.Console + " " + strings.Join(p.AlsoFor, " ") + " " + p.ID)
		ok := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		if p.Experimental {
			experimental = append(experimental, p)
		} else {
			tested = append(tested, p)
		}
	}
	return
}

func (s *screen) refreshConsoles() {
	if s.consoleList == nil {
		return
	}
	s.consoleLead.SetText(s.t("Choose your console’s board and screen."))
	if s.card != nil && s.card.Profile != "" && s.im != nil {
		for _, p := range s.im.Profiles {
			if p.ID == s.card.Profile {
				s.consoleLead.SetText(s.t("The card was set up for %s. Change it if this card is for another console.", shown(p.Name)))
			}
		}
	}
	s.consoleList.RemoveAll()
	if s.im == nil {
		return
	}
	tested, experimental := consoleGroups(s.im.Profiles, s.search.Text)
	group := func(name string, list []firmware.Profile) {
		if len(list) == 0 {
			return
		}
		s.consoleList.Add(heading(name))
		var items []fyne.CanvasObject
		for _, p := range list {
			id := p.ID
			item := newRadioItem(shown(p.Name), func() { s.chooseProfile(id) })
			item.setSelected(p.ID == s.profile)
			items = append(items, item)
		}
		s.consoleList.Add(container.NewGridWithColumns(3, items...))
	}
	group(s.t("Tested"), tested)
	group(s.t("Experimental (nobody on the team has tried these)"), experimental)
	if len(tested)+len(experimental) == 0 {
		s.consoleList.Add(wrapped(s.t("No console matches your search.")))
	}
	s.refreshExperimental()
}

func (s *screen) chooseProfile(id string) {
	if s.busy {
		return
	}
	s.profile = id
	s.logf("profile %s", id)
	s.refreshConsoles()
	s.ready()
}

// refreshExperimental shows the warning of a chosen experimental profile inside
// the page; Next stays off until it is ticked.
func (s *screen) refreshExperimental() {
	s.expBox.RemoveAll()
	p := s.chosenProfile()
	if p == nil || !p.Experimental {
		return
	}
	id := p.ID
	body := wrapped(s.t("Nobody on the andr36oid team has tested this device tree on real hardware. It can leave the screen black or the buttons dead, and in the worst case put a wrong voltage on part of the board.\n\nSupport for experimental device trees is very limited. If something doesn't work:\n- Report it only in the clone issue tracker: %s\n- Attach all the boot files from a system that works on your console, for example from the SD card it came with: every .dtb file and the other files at the top of its first drive.\n- Answer every question in the issue form.\n\nReports without those files will be closed.", cloneTracker))
	link, _ := url.Parse(cloneTracker)
	check, _, row := wrappedCheck(s.t("I understand that this may not work, that support is very limited, and that I must attach my console's working boot files to any report."), nil)
	check.SetChecked(s.experimentalOK[id])
	check.OnChanged = func(v bool) {
		if s.experimentalOK == nil {
			s.experimentalOK = map[string]bool{}
		}
		s.experimentalOK[id] = v
		s.ready()
	}
	text := container.NewVScroll(container.NewVBox(body, widget.NewHyperlink(cloneTracker, link)))
	text.SetMinSize(fyne.NewSize(0, 120))
	s.expBox.Add(noteCard(noteWarning, heading(s.t("Experimental device tree")+": "+shown(p.Name)), text, row))
}

// 5. Options: storage, the game system, bug fixes; the rest under Advanced.

func (s *screen) optionsPage() fyne.CanvasObject {
	storageCard := noteCard(notePlain, heading(s.t("Storage")), s.storageKept, s.storageBox)
	var fixRow fyne.CanvasObject
	s.fixCheck, s.fixLabel, fixRow = wrappedCheck("", func(v bool) {
		s.applyFixes = v
		s.ready()
	})
	s.fixCheck.SetChecked(s.applyFixes)
	s.fixInfo = wrapped("")
	s.fixBox = noteCard(notePlain, fixRow, s.fixInfo)
	s.fixBox.Hide()
	custom := widget.NewButton(s.t("Choose custom Android DTB..."), s.chooseCustom)
	s.advancedBox = container.NewVBox(
		heading(s.t("Boot options")), s.bootloaderSection(), widget.NewSeparator(),
		heading(s.t("DTB")), s.imageDTBSection(), wrapped(s.t(firmware.CustomWarning)), container.NewHBox(custom), widget.NewSeparator(),
		s.systemSection())
	s.advancedBox.Hide()
	var advanced *widget.Button
	advanced = quiet(s.t("Advanced..."), func() {
		if s.advancedBox.Visible() {
			s.advancedBox.Hide()
		} else {
			s.advancedBox.Show()
		}
	})
	back, next := s.stepButtons(stepOptions)
	body := container.NewVBox(storageCard, s.guestBox, s.fixBox, container.NewHBox(advanced), s.advancedBox)
	return pageView(title(s.t("Options")), nil, body, nil, back, next)
}

// 6. Check and write: everything that will happen; erasing needs the tick box.

func (s *screen) writePage() fyne.CanvasObject {
	s.writeTitle = title("")
	s.summary = container.NewVBox()
	s.writeNotes = container.NewVBox()
	var label *tapLabel
	s.eraseCheck, label, s.eraseRow = wrappedCheck("", func(v bool) {
		s.eraseOK = v
		s.ready()
	})
	label.TextStyle = fyne.TextStyle{Bold: true}
	s.action = widget.NewButton("", s.confirm)
	back, _ := s.stepButtons(stepWrite)
	s.nexts[stepWrite] = nil
	return pageView(s.writeTitle, nil, container.NewVBox(noteCard(notePlain, s.summary), s.writeNotes), s.eraseRow, back, s.action)
}

// needsTick: the write must be confirmed with the tick box (every erase, and any
// write to a drive that may not be the card).
func (s *screen) needsTick() bool {
	return !s.updating || s.drive != nil && (s.drive.Fixed || s.drive.Large())
}

// alsoText lists what happens next to writing andr36oid.
func (s *screen) alsoText() string {
	var parts []string
	if g := s.guestOffer(); g != nil {
		src, _ := catalog.FindGuestSource(g.Distribution)
		parts = append(parts, s.t("%s (%s download)", src.Display, humanBytes(g.DownloadBytes())))
	}
	if s.fixCount > 0 && s.applyFixes {
		parts = append(parts, s.t("bug fixes for this release (%d)", s.fixCount))
	}
	if s.system != "" {
		parts = append(parts, s.systemText())
	}
	if len(parts) == 0 {
		return s.t("Nothing else")
	}
	return strings.Join(parts, ", ")
}

// summarize fills the last page: what will be written where, and what else happens.
// releaseText is the loaded release with its kernel and graphics, as in
// "andr36oid 2026-11-19 - 5.10 - Vulkan".
func (s *screen) releaseText() string {
	text := shown(s.im.Version)
	if k, g, ok := catalog.Variant(filepath.Base(s.im.Source)); ok {
		gpu := "OpenGL ES"
		if g == "vulkan" {
			gpu = "Vulkan"
		}
		text += " - " + k + " - " + gpu
	}
	return text
}

func (s *screen) summarize() {
	if s.summary == nil {
		return
	}
	f := widget.NewForm()
	add := func(key, value string) { f.Append(s.t(key), wrapped(value)) }
	if s.im != nil {
		add("Release", s.releaseText())
	}
	if s.drive != nil {
		// the device path is in the details log
		add("Card", s.driveTitle(*s.drive))
	}
	add("Console", s.profileName())
	if s.updating {
		add("Storage", s.t("Kept as it is"))
	} else {
		add("Storage", s.storageText())
	}
	add("Also", s.alsoText())
	if s.custom != "" {
		add("DTB", s.dtbText())
	}
	// boot options only when they differ from what the image brings
	if s.bootloaderOptions != nil {
		if c := s.bootloaderOptions.selection(); c != nil {
			switch {
			case c.Changed():
				add("Boot options", s.bootloaderOptions.summary.Text)
			case c.Build != nil:
				add("Boot options", s.bootloaderSummary(&firmware.BootloaderRecord{Choice: c.Choice, Version: c.Build.Version, Tag: c.Build.Tag}))
			}
		}
	}
	s.summary.Objects = []fyne.CanvasObject{f}
	s.summary.Refresh()

	// what the user is told before anything happens
	var lines []fyne.CanvasObject
	kind := notePlain
	if s.updating {
		s.writeTitle.SetText(s.t("Ready to update"))
		s.action.SetText(s.t("Update this card"))
		s.action.Importance = widget.HighImportance
		lines = append(lines, wrapped(s.t("BOOT, system, and vendor will be updated together. The dedicated cache will be cleared. Userdata and games stay in place. A recovery backup will be saved before writing.")))
		if k := s.guestKept(); k != "" {
			lines = append(lines, wrapped(k))
		}
		if s.card != nil && s.card.DataCard {
			lines = append(lines, wrapped(s.t("This is the data card of an eMMC install. Updating it makes it bootable again, and the console will start from it instead of the eMMC.")+" "+s.t(dataCardFirstStart)))
		}
	} else {
		s.writeTitle.SetText(s.t("Ready to write"))
		s.action.SetText(s.t("Erase and write"))
		s.action.Importance = widget.DangerImportance
		kind = noteError
		lines = append(lines, heading(s.t("Everything currently on this card will be erased.")))
	}
	tick := s.t("I understand everything on this card is erased")
	if s.drive != nil {
		switch {
		case s.drive.Fixed:
			kind = noteError
			lines = append(lines, heading(s.t("Write to a disk that is not a removable card?")), wrapped(s.t("%s is not a removable card. If it is the wrong disk, what is on it is destroyed and cannot be recovered. Check the name and the size once more before you go on.", s.driveLabel(*s.drive))))
			tick = s.t("This is the right disk. Write to it.")
		case s.drive.Large():
			kind = noteError
			size := fmt.Sprintf("%.0f GB", float64(s.drive.Size)/1e9)
			lines = append(lines, heading(s.t("Is this really your SD card?")), wrapped(s.t("%s (%s) is larger than most SD cards, or it says it is a hard drive. If it is not your SD card, everything on it is lost for good. Check the size and the name once more.", shown(s.drive.Name), size)))
			tick = s.t("This %s drive (%s) is my SD card. Write to it.", size, shown(s.drive.Name))
		}
		if len(s.drive.Volumes) > 0 {
			lines = append(lines, wrapped(s.t("Drives of this card that are open on this computer (%s) are closed first.", strings.Join(s.drive.Volumes, ", "))))
		}
	}
	if s.convert {
		lines = append(lines, wrapped(s.t(ownDTBWarning)))
	} else if s.custom != "" {
		lines = append(lines, wrapped(s.t(firmware.CustomWarning)))
	}
	s.writeNotes.Objects = []fyne.CanvasObject{noteCard(kind, lines...)}
	s.writeNotes.Refresh()
	if tick != s.eraseText {
		// another drive or another task: the tick is asked again
		s.eraseText = tick
		s.eraseOK = false
	}
	if row, ok := s.eraseRow.(*fyne.Container); ok {
		if label, ok := row.Objects[0].(*tapLabel); ok {
			label.SetText(tick)
		}
	}
	s.eraseCheck.OnChanged, s.eraseCheck.Checked = nil, s.eraseOK
	s.eraseCheck.Refresh()
	s.eraseCheck.OnChanged = func(v bool) {
		s.eraseOK = v
		s.ready()
	}
	if s.needsTick() {
		s.eraseRow.Show()
	} else {
		s.eraseRow.Hide()
	}
	s.action.Refresh()
}
