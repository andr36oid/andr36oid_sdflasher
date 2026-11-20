package ui

import (
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

func testProfiles() []firmware.Profile {
	return []firmware.Profile{
		{ID: "Panels/Panel0", Name: "R36S — Panel 0", Console: "R36S"},
		{ID: "Panels/Panel3", Name: "R36S — Panel 3", Console: "R36S"},
		{ID: "Panels/Panel4", Name: "R36S — Panel 4", Console: "R36S"},
		{ID: "Devices/R36Ultra", Name: "R36 Ultra / GR36 — 720 × 720", Console: "R36 Ultra"},
		{ID: "Experimental/EE-G80-Panel8", Name: "G80C / G80CA / G80D — Panel 8", Console: "G80", Experimental: true},
		{ID: "Experimental/EE-V12-Panel4", Name: "R36S clone V12 — Panel 4", Console: "R36S clone", AlsoFor: []string{"R36S"}, Experimental: true},
	}
}

// testScreen is a screen with a release loaded, nothing looked up on the network.
func testScreen(t *testing.T, size fyne.Size) *screen {
	noBackground(t, nil)
	a := test.NewApp()
	t.Cleanup(a.Quit)
	a.Settings().SetTheme(uiTheme())
	w := a.NewWindow("test")
	t.Cleanup(w.Close)
	w.Resize(size)
	s := &screen{win: w, locale: "en", im: &firmware.Image{Version: "andr36oid 2026-11-19", Source: "/home/u/andr36oid-2026-11-19-5.10-vulkan.img.xz", SHA256: "x", BuildIdentity: "andr36oid-2026-11-19|eng.kenny.20261119.101500", Facts: map[string]string{"layout": "prepared-1", "subsystem": "release-1"}, Profiles: testProfiles()}}
	s.guestFor, s.guests = "x", map[string]*guestLookup{}
	s.build()
	return s
}

func TestStartChoosesTask(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	if s.task != taskNew || !s.startCards[taskNew].selected || s.nexts[stepStart].Disabled() {
		t.Fatal("a new card is the first choice")
	}
	s.startCards[taskUpdate].Tapped(nil)
	if !s.updating || !s.startCards[taskUpdate].selected || s.startCards[taskNew].selected {
		t.Fatal("update not chosen")
	}
	s.startCards[taskFix].Tapped(nil)
	s.nexts[stepStart].OnTapped()
	if s.inSteps || s.reachable(stepImage) {
		t.Fatal("fix or change leads to the fix page, not the release")
	}
	s.startCards[taskNew].Tapped(nil)
	s.nexts[stepStart].OnTapped()
	if !s.inSteps || s.step != stepImage || s.updating {
		t.Fatal("new card leads to the release page", s.step)
	}
}

// noBackground lists drives (none unless given) at once, so that no test reads
// this computer's disks or touches the window from another goroutine.
func noBackground(t *testing.T, drives []platform.Drive) {
	oldList, oldBackground := listDrives, background
	t.Cleanup(func() { listDrives, background = oldList, oldBackground })
	listDrives = func(bool) ([]platform.Drive, error) { return drives, nil }
	background = func(f func()) { f() }
}

func TestStepsNeedWhatComesBefore(t *testing.T) {
	noBackground(t, nil)
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("test")
	defer w.Close()
	s := &screen{win: w, locale: "en"}
	s.build()
	s.showStep(stepImage)
	if !s.nexts[stepImage].Disabled() {
		t.Fatal("next enabled without a release")
	}
	for i := stepCard; i < stepCount; i++ {
		if !s.sideSteps[i].Disabled() {
			t.Fatalf("step %d reachable without a release", i)
		}
	}
	// a release offered online: Next downloads it
	s.asset = &catalog.Asset{Name: "x.img.xz", URL: "https://example/x.img.xz"}
	s.ready()
	if s.nexts[stepImage].Disabled() {
		t.Fatal("next disabled with a release to download")
	}
	s.asset = nil
	s.im = &firmware.Image{Profiles: testProfiles()}
	s.populateImage()
	s.autoProfile()
	s.ready()
	if s.nexts[stepImage].Disabled() || s.sideSteps[stepCard].Disabled() || !s.sideSteps[stepConsole].Disabled() {
		t.Fatal("release chosen: only the card step should open up")
	}
	s.drive = &platform.Drive{Path: "x", Size: 8 << 30}
	s.card = &flash.Card{Installed: false}
	s.setTask(taskUpdate)
	if s.stepDone(stepCard) || !s.action.Disabled() {
		t.Fatal("update of a card without andr36oid must not be writable")
	}
	s.setTask(taskNew)
	if s.stepDone(stepCard) || !s.action.Disabled() {
		t.Fatal("a new install on an 8 GB card must not be writable")
	}
	// the update sent the page back to its Card step, which lists the cards again
	// (none here): the card is chosen anew
	s.drive, s.card = &platform.Drive{Path: "x", Size: 16e9}, &flash.Card{Installed: false}
	s.ready()
	if !s.reachable(stepWrite) || s.sideSteps[stepWrite].Disabled() {
		t.Fatal("new install with everything chosen must reach the write page")
	}
	if !s.action.Disabled() {
		t.Fatal("erase allowed without the tick")
	}
	s.eraseCheck.SetChecked(true)
	if s.action.Disabled() {
		t.Fatal("ticked erase not allowed")
	}
	// another card: the tick is asked again
	s.drive = &platform.Drive{Path: "y", Size: 600e9, Name: "Portable SSD"}
	s.ready()
	if !s.action.Disabled() || !strings.Contains(s.eraseText, "is my SD card") {
		t.Fatal("tick kept for another drive", s.eraseText)
	}
}

func TestUpdateNeedsNoTickOnACard(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Size: 64e9, Name: "Card Reader"}
	s.card = &flash.Card{Installed: true, Treble: true, Profile: "Panels/Panel4"}
	s.setTask(taskUpdate)
	s.profile = "Panels/Panel4"
	s.ready()
	if s.needsTick() || s.eraseRow.Visible() || s.action.Disabled() || s.action.Text != "Update this card" {
		t.Fatal("update of a card: no tick box", s.action.Text)
	}
	s.drive = &platform.Drive{Path: "/dev/sdc", Size: 1e12, Name: "Portable SSD", External: true}
	s.ready()
	if !s.needsTick() || !s.eraseRow.Visible() || !s.action.Disabled() {
		t.Fatal("update of a large drive: tick box")
	}
}

func TestCardRows(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.listing = true
	s.refreshCards()
	if s.cardLabel.Text != "Looking for cards..." {
		t.Fatal(s.cardLabel.Text)
	}
	s.listing = false
	s.setDrives(nil)
	if !strings.Contains(s.cardLabel.Text, "No removable cards found") {
		t.Fatal(s.cardLabel.Text)
	}
	slot := platform.Drive{Path: "/dev/mmcblk0", Name: "mmcblk0", Size: 183e9}
	ssd := platform.Drive{Path: "/dev/sdb", Name: "Portable SSD", Size: 954e9, External: true}
	s.setDrives([]platform.Drive{slot, ssd})
	rows := s.cardRows.Objects
	if len(rows) != 2 || s.cardLabel.Visible() && strings.Contains(s.cardLabel.Text, "No removable") {
		t.Fatal("rows", len(rows), s.cardLabel.Text)
	}
	first, second := rows[0].(*choiceCard), rows[1].(*choiceCard)
	if first.title.Text != "SD card in the built-in slot - 183.0 GB" || first.disabled {
		t.Fatal(first.title.Text)
	}
	if !second.disabled || second.tag.Text != "not a card" {
		t.Fatal("a USB disk that is not a card is greyed with the reason")
	}
	s.allDrives = true
	s.refreshCards()
	if s.cardRows.Objects[1].(*choiceCard).disabled {
		t.Fatal("show all drives: the disk can be chosen")
	}
	// what is on the chosen card is on its row
	s.drive, s.card = &slot, &flash.Card{Installed: true, Treble: true, Profile: "Devices/R36Ultra"}
	s.refreshCards()
	if d := s.cardRows.Objects[0].(*choiceCard).detail.Text; !strings.Contains(d, "Holds andr36oid for R36 Ultra / GR36 - 720 × 720") {
		t.Fatal(d)
	}
}

func TestConsoleGroupsAndSearch(t *testing.T) {
	tested, experimental := consoleGroups(testProfiles(), "")
	if len(tested) != 4 || len(experimental) != 2 {
		t.Fatal(len(tested), len(experimental))
	}
	tested, experimental = consoleGroups(testProfiles(), "panel 4")
	if len(tested) != 1 || len(experimental) != 1 || tested[0].ID != "Panels/Panel4" {
		t.Fatal(tested, experimental)
	}
	// a console the profile is also for
	if _, experimental = consoleGroups(testProfiles(), "r36s clone"); len(experimental) != 1 {
		t.Fatal(experimental)
	}
	if tested, experimental = consoleGroups(testProfiles(), "nothing like this"); len(tested)+len(experimental) != 0 {
		t.Fatal("matched")
	}
}

func TestExperimentalNeedsAgreement(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Size: 64 << 30, Name: "Generic SD reader"}
	s.card = &flash.Card{Reason: "no andr36oid partitions"}
	s.showStep(stepConsole)
	s.chooseProfile("Experimental/EE-G80-Panel8")
	if s.profile != "Experimental/EE-G80-Panel8" || !s.nexts[stepConsole].Disabled() || len(s.expBox.Objects) != 1 {
		t.Fatal("experimental profile used before the warning was agreed to")
	}
	if s.win.Canvas().Overlays().Top() != nil {
		t.Fatal("the warning is in the page, not a dialog")
	}
	var check *tapLabel
	dtbWidgets(s.expBox, func(o fyne.CanvasObject) {
		if l, ok := o.(*tapLabel); ok {
			check = l
		}
	})
	check.Tapped(nil)
	if !s.experimentalOK["Experimental/EE-G80-Panel8"] || s.nexts[stepConsole].Disabled() {
		t.Fatal("agreed experimental profile not usable")
	}
	s.chooseProfile("Panels/Panel4")
	if len(s.expBox.Objects) != 0 || s.nexts[stepConsole].Disabled() {
		t.Fatal("tested profile shows the warning")
	}
}

// Choosing a card that was set up for an experimental profile shows nothing: the
// warning waits for the console page.
func TestCardWithExperimentalProfileAsksNothing(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.card = &flash.Card{Installed: true, Treble: true, Profile: "Experimental/EE-G80-Panel8"}
	s.autoProfile()
	if s.profile != "Experimental/EE-G80-Panel8" || s.win.Canvas().Overlays().Top() != nil || s.profileOK() {
		t.Fatal("experimental profile of the card")
	}
	if !strings.Contains(s.consoleLead.Text, "G80C / G80CA / G80D - Panel 8") {
		t.Fatal(s.consoleLead.Text)
	}
}

func TestSummarySaysEverything(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Size: 128e9, Name: "Card Reader"}
	s.card = &flash.Card{}
	s.guests["dArkOS"] = &guestLookup{done: true, g: testGuest()}
	s.fixCount = 2
	s.ready()
	also := s.alsoText()
	if !strings.Contains(also, "dArkOS (") || !strings.Contains(also, "bug fixes for this release (2)") {
		t.Fatal(also)
	}
	s.guestCheck.SetChecked(false)
	s.fixCheck.SetChecked(false)
	if s.alsoText() != "Nothing else" {
		t.Fatal(s.alsoText())
	}
}

func testGuest() *catalog.GuestRelease {
	g := &catalog.GuestRelease{Distribution: "dArkOS", Darkosen: "11072026", Revision: 1, PartitionBytes: 8 << 30}
	g.Image.Name, g.Image.Size, g.Image.SHA256 = "darkos.img", 2<<30, strings.Repeat("a", 64)
	g.Download.Size = 2300 << 20
	return g
}

// testPlan is an install (or update) plan of a 64 GB card with every partition.
func testPlan(update bool) *flash.Plan {
	const mib = 2048 // sectors
	parts := []struct {
		name       string
		start, end uint64
	}{{"BOOT", 32 * mib, 288 * mib}, {"system", 288 * mib, 3300 * mib}, {"vendor", 3300 * mib, 3700 * mib}, {"userdata", 3700 * mib, 30000 * mib}, {"EASYROMS", 30000 * mib, 52000 * mib}, {flash.GuestPartition, 52000 * mib, 60000 * mib}}
	p := &flash.Plan{Mode: "install", Size: 64e9}
	for i, x := range parts {
		p.Table.Parts = append(p.Table.Parts, layout.Partition{Number: i + 1, Name: x.name, Start: x.start, End: x.end - 1})
	}
	part := func(n int) flash.Range {
		q := p.Table.Parts[n]
		return flash.Range{Name: q.Name, Offset: q.Offset(), Length: q.Size()}
	}
	if update {
		p.Mode = "update"
		for _, n := range []int{0, 1, 2} {
			p.Writes = append(p.Writes, flash.Write{Range: part(n)})
		}
		p.Protected = []flash.Range{part(3), part(4), part(5)}
		return p
	}
	p.Writes = []flash.Write{
		{Range: flash.Range{Name: "Release image", Offset: 34 * 512, Length: part(2).Offset + part(2).Length - 34*512}},
		{Range: flash.Range{Name: "Android storage", Offset: part(3).Offset, Length: 1 << 20}, Zero: true},
		{Range: flash.Range{Name: "dArkOS", Offset: part(5).Offset, Length: 2 << 30}, Guest: true},
		{Range: flash.Range{Name: "Partition table", Offset: 0, Length: 34 * 512}},
	}
	return p
}

// feed sends the progress of a write up to fraction of the bytes.
func feed(w *work, p *flash.Plan, fraction float64, verify bool) {
	var total int64
	for _, x := range p.Writes {
		total += x.Length
	}
	w.progress(flash.Progress{Phase: "preflight", Name: "Checking source image", Plan: p, Done: 1, Total: 1})
	limit := int64(fraction * float64(total))
	var done int64
	for _, x := range p.Writes {
		n := min(x.Length, limit-done)
		if n <= 0 {
			break
		}
		w.progress(flash.Progress{Phase: "writing", Name: x.Name, Done: done + n, Total: total, Offset: x.Offset, Length: n})
		if verify || n == x.Length {
			w.progress(flash.Progress{Phase: "verifying", Name: x.Name, Done: done + n/2, Total: total, Offset: x.Offset, Length: n / 2})
		}
		done += n
	}
	if done > 0 && !verify {
		// as on a real card: some minutes in, writing at about 20 MB/s
		took := time.Duration(float64(done) / 20e6 * float64(time.Second))
		w.started = time.Now().Add(-took - 40*time.Second)
		w.stageAt, w.stageFrom = time.Now().Add(-took), 0
		w.count(done, total)
	}
	w.blocks.update(true)
}

// captureBottom saves the page scrolled to its end.
func captureBottom(t *testing.T, s *screen, dir, name string) {
	var scrolls []*container.Scroll
	dtbWidgets(s.current(), func(o fyne.CanvasObject) {
		if sc, ok := o.(*container.Scroll); ok {
			scrolls = append(scrolls, sc)
		}
	})
	for _, sc := range scrolls {
		sc.ScrollToBottom()
	}
	capture(t, s, dir, name+"-bottom")
	for _, sc := range scrolls {
		sc.ScrollToTop()
	}
}

func capture(t *testing.T, s *screen, dir, name string) {
	if dir == "" {
		return
	}
	os.MkdirAll(dir, 0o755)
	f, e := os.Create(filepath.Join(dir, name+".png"))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	// what the window does every frame: lay out again what wrapped text made taller
	for i := 0; i < 4; i++ {
		s.win.Content().Refresh()
	}
	if e = png.Encode(f, s.win.Canvas().Capture()); e != nil {
		t.Fatal(e)
	}
}

// Every page renders, in every language and at the smallest window. With
// SDFLASHER_SCREENSHOTS set, each one is saved as a PNG for a look.
func TestEveryPageRenders(t *testing.T) {
	dir := os.Getenv("SDFLASHER_SCREENSHOTS")
	sizes := map[string]fyne.Size{"980x680": fyne.NewSize(980, 680), "900x620": fyne.NewSize(900, 620)}
	for _, l := range i18n.Languages {
		for sizeName, size := range sizes {
			if dir == "" && sizeName == "980x680" {
				continue
			}
			full := l.Code == "en" || l.Code == "de" || l.Code == "ja"
			s := testScreen(t, size)
			s.locale = l.Code
			slot := platform.Drive{Path: "/dev/mmcblk0", Name: "mmcblk0", Size: 183e9, Labels: []string{"BOOT", "system", "vendor", "userdata", "EASYROMS"}}
			s.drives = []platform.Drive{slot, {Path: "/dev/sdb", Name: "Portable SSD", Size: 954e9, External: true}, {Path: "/dev/sdc", Name: "Card Reader", Size: 31.9e9, WriteProtected: true}}
			s.listed = true
			noBackground(t, s.drives)
			s.drive = &slot
			s.card = &flash.Card{Installed: true, Treble: true, Profile: "Experimental/EE-G80-Panel8"}
			s.guests["dArkOS"] = &guestLookup{done: true, g: testGuest()}
			s.guests["dArkOSRE"] = &guestLookup{done: true}
			s.guests["dArkOSen"] = &guestLookup{done: true, err: errors.New("offline")}
			s.fixCount = 2
			s.asset = &catalog.Asset{Name: "andr36oid-2026-11-19-5.10-vulkan.img.xz", Release: "andr36oid 2026-11-19", Size: 1900 << 20, URL: "https://example/x"}
			s.assets = []catalog.Asset{*s.asset}
			s.build()
			s.autoProfile()
			name := func(page string) string { return fmt.Sprintf("%s-%s-%s", sizeName, l.Code, page) }
			for step := 0; step < stepCount; step++ {
				s.showStep(step)
				if s.step != step {
					// the experimental profile is not agreed to yet
					if step != stepOptions || s.step != stepConsole {
						t.Fatalf("%s: step %d shows %d", l.Code, step, s.step)
					}
					capture(t, s, dir, name("3-console-experimental"))
					captureBottom(t, s, dir, name("3-console-experimental"))
					s.experimentalOK = map[string]bool{s.profile: true}
					s.refreshConsoles()
					s.showStep(step)
				}
				if step == stepOptions && full {
					s.advancedBox.Show()
					capture(t, s, dir, name("4-options-advanced"))
					captureBottom(t, s, dir, name("4-options-advanced"))
					s.advancedBox.Hide()
				}
				capture(t, s, dir, name(fmt.Sprintf("%d-%s", step, strings.ToLower(stepNames[step]))))
				if !full {
					continue
				}
			}
			if s.action.Disabled() == false {
				t.Fatal("erase allowed without the tick")
			}
			s.eraseCheck.SetChecked(true)
			if s.action.Disabled() {
				t.Fatalf("%s: write button disabled with everything chosen", l.Code)
			}
			capture(t, s, dir, name("5-write-ticked"))
			if !full {
				continue
			}
			s.setTask(taskUpdate)
			s.showStep(stepWrite)
			capture(t, s, dir, name("5-write-update"))
			s.setTask(taskNew)
			s.showFix()
			capture(t, s, dir, name("fix"))
			captureBottom(t, s, dir, name("fix"))
			s.pickCard(s.t("Apply hotfixes"), s.t("Choose an existing andr36oid card. Only BOOT/hotfix will be updated."), func(platform.Drive) {})
			capture(t, s, dir, name("fix-card"))

			// the working page
			plan := testPlan(false)
			w := s.newWork("dArkOS")
			w.enter(stageDownload)
			w.stop.Show()
			w.stageAt, w.stageFrom, w.started = time.Now().Add(-35*time.Second), 0, time.Now().Add(-36*time.Second)
			w.count(700<<20, 2300<<20)
			capture(t, s, dir, name("6-working-download"))
			w.stop.Hide()
			w.stage = -1
			feed(w, plan, 0, false)
			capture(t, s, dir, name("6-working-start"))
			feed(w, plan, 0.41, false)
			capture(t, s, dir, name("6-working-mid"))
			captureBottom(t, s, dir, name("6-working-mid"))
			feed(w, plan, 1, true)
			w.progress(flash.Progress{Phase: "verifying", Name: "Checking the whole card once more"})
			capture(t, s, dir, name("6-working-checking"))
			w.finished(func() {})
			capture(t, s, dir, name("6-working-done"))
			s.showDone(plan, jobs.Result{Backup: ""}, &flash.CardSpeed{WriteMBps: 18, ReadLatency: 0.8, WriteLatency: 2.1})
			capture(t, s, dir, name("7-done"))

			w = s.newWork("")
			feed(w, plan, 0.3, false)
			w.failed(errors.New("write /dev/mmcblk0: input/output error"), "", true)
			capture(t, s, dir, name("8-failed"))
			captureBottom(t, s, dir, name("8-failed"))

			w = s.newWork("")
			feed(w, plan, 0.3, false)
			w.stop.Show()
			capture(t, s, dir, name("6-working-stoppable"))
			w.stopped(jobs.StoppedIncomplete)
			capture(t, s, dir, name("8-stopped"))

			s.setTask(taskUpdate)
			s.card = &flash.Card{Installed: true, Treble: true, Profile: "Panels/Panel4"}
			update := testPlan(true)
			w = s.newWork("")
			w.progress(flash.Progress{Phase: "backup", Name: "BOOT", Done: 1, Total: 2})
			feed(w, update, 0.6, false)
			capture(t, s, dir, name("6-working-update"))
			s.setTask(taskNew)
		}
	}
}
