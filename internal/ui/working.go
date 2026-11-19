package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// After the button on the write page every question has been asked: the game
// system is downloaded, the image checked and the card written without another one.

// the stages of a write
const (
	stageDownload = iota
	stageCheck
	stageBackup
	stageWrite
	stageVerify
	stageDone
)

func (s *screen) mode() string {
	if s.updating {
		return "update"
	}
	return "install"
}

// confirm is the button on the write page. The tick box was the confirmation; the
// other questions that may be needed are asked now, before any waiting.
func (s *screen) confirm() {
	if !s.writable() || s.im == nil || s.card == nil || s.drive == nil {
		return
	}
	g := s.guestOffer()
	opts := flash.Options{}
	if g != nil {
		// the download comes later; the plan is checked with what is known of it now
		opts.Guest = flashGuest(g, "download")
	}
	if _, e := flash.BuildWith(s.im, *s.card, s.drive.Size, s.mode(), s.profile, s.noROMs.Checked, s.userdataGiB, opts); e != nil {
		s.showError(e)
		return
	}
	s.askBeforeWriting(func() {
		var boot *firmware.BootloaderChoice
		if s.bootloaderOptions != nil {
			boot = s.bootloaderOptions.selection()
		}
		s.runWrite(g, boot)
	})
}

// askBeforeWriting asks what only some writes need (boot options that don't go
// together, a device tree that changes the screen), then goes on.
func (s *screen) askBeforeWriting(then func()) {
	if s.bootloaderOptions != nil && !s.bootloaderOptions.accepted {
		s.bootloaderOptions.confirm(func() { s.askBeforeWriting(then) })
		return
	}
	if s.im != nil && s.im.DTBPack != nil && s.custom == "" {
		if p := s.chosenProfile(); p != nil && p.DTBScreenChanged {
			s.confirmDTBScreen(&firmware.DTBPlan{ScreenChanged: true}, then)
			return
		}
	}
	then()
}

// work is the working page of one write.
type work struct {
	s               *screen
	updating        bool
	stages          []int
	stageIcons      []*widget.Icon
	stageLabels     []*widget.Label
	stage           int
	head, lead      *widget.Label
	phase, sub      *widget.Label
	bar             *widget.ProgressBar
	numbers         *widget.Label
	result          *fyne.Container
	strip           *cardStrip
	blocks          *blockMap
	hover           *widget.Label
	empty           *widget.Label
	buttons         *fyne.Container
	stop            *widget.Button
	started         time.Time
	stageAt         time.Time
	stageFrom       int64
	lastPhase, last string
	lastOff         int64
	plan            *flash.Plan
	guestName       string
	done            bool
}

// stageText names a stage while it runs, or once done in the past tense.
func (s *screen) stageText(stage int, guest string, done bool) string {
	if done {
		switch stage {
		case stageDownload:
			return s.t("Downloaded %s", guest)
		case stageCheck:
			return s.t("Checked the image")
		case stageBackup:
			return s.t("Saved a backup")
		case stageWrite:
			return s.t("Wrote the card")
		case stageVerify:
			return s.t("Checked the card")
		}
	}
	switch stage {
	case stageDownload:
		return s.t("Downloading %s...", guest)
	case stageCheck:
		return s.t("Checking the image")
	case stageBackup:
		return s.t("Saving a backup")
	case stageWrite:
		return s.t("Writing")
	case stageVerify:
		return s.t("Checking the card")
	}
	return ""
}

func (s *screen) newWork(guest string) *work {
	w := &work{s: s, updating: s.updating, guestName: guest, stage: -1, started: time.Now()}
	s.step = stepWrite
	if guest != "" {
		w.stages = append(w.stages, stageDownload)
	}
	w.stages = append(w.stages, stageCheck)
	if s.updating {
		w.stages = append(w.stages, stageBackup)
	}
	w.stages = append(w.stages, stageWrite, stageVerify)
	row := container.NewGridWithColumns(len(w.stages))
	for _, st := range w.stages {
		icon := widget.NewIcon(theme.RadioButtonIcon())
		label := wrapped(s.stageText(st, guest, false))
		w.stageIcons = append(w.stageIcons, icon)
		w.stageLabels = append(w.stageLabels, label)
		row.Add(container.NewBorder(nil, nil, container.NewVBox(icon), nil, label))
	}
	w.head = title(s.t("Writing the card"))
	if s.updating {
		w.head.SetText(s.t("Updating the card"))
	}
	w.phase = heading(s.t("Preparing to write. Keep the card connected."))
	w.sub = wrapped("")
	w.sub.Hide()
	w.bar = widget.NewProgressBar()
	w.numbers = wrapped("")
	w.result = container.NewVBox()
	w.strip = newCardStrip()
	w.blocks = newBlockMap()
	w.hover = wrapped(" ")
	w.hover.Importance = widget.LowImportance
	w.blocks.hover = func(text string) {
		if text == "" {
			text = " "
		}
		w.hover.SetText(text)
	}
	w.blocks.describe = func(g *blockGroup, b mapBlock) string {
		return g.name + " - " + s.t("%s to %s", gb(b.off), gb(b.off+b.length)) + " - " + s.t(blockNames[b.state])
	}
	w.empty = wrapped("")
	w.empty.Hide()
	// Stop is there while the game system downloads; the write itself is done by
	// the disk helper with administrator rights and has no way to be stopped
	w.stop = widget.NewButton(s.t("Stop"), func() {
		s.confirmDialog(s.t("Stop the download?"), s.t("Nothing was written to the card."), func(ok bool) {
			if ok && s.cancel != nil {
				s.cancel()
			}
		}, s.win)
	})
	w.stop.Hide()
	w.buttons = container.NewHBox(layout.NewSpacer(), w.stop)
	cardSize := s.drive.Size
	what := container.NewBorder(nil, nil, heading(s.t("What is written")), nil, container.NewCenter(w.hover))
	// the blocks take the height that is left, so the legend below never needs scrolling
	body := container.New(fillLast{}, row, w.result, w.phase, w.sub, w.bar, w.numbers, widget.NewSeparator(),
		heading(s.t("The whole card (%s)", fmt.Sprintf("%.0f GB", float64(cardSize)/1e9))), w.strip,
		what, w.empty, w.blocks)
	w.lead = wrapped(s.t("Do not take the card out.") + " " + s.driveTitle(*s.drive))
	top := container.NewVBox(w.head, w.lead)
	bottom := container.NewVBox(container.NewPadded(legendRow(s.t)), widget.NewSeparator(), w.buttons)
	view := container.NewBorder(top, bottom, nil, nil, container.NewVScroll(container.NewPadded(body)))
	s.leave(view)
	return w
}

// enter makes a stage the current one; the ones before it are done.
func (w *work) enter(stage int) {
	if stage == w.stage {
		return
	}
	w.stage = stage
	w.stageAt = time.Now()
	w.stageFrom = -1
	for i, st := range w.stages {
		w.stageLabels[i].SetText(w.s.stageText(st, w.guestName, st < stage))
		switch {
		case st < stage:
			w.stageIcons[i].SetResource(theme.ConfirmIcon())
			w.stageLabels[i].TextStyle = fyne.TextStyle{}
		case st == stage:
			w.stageIcons[i].SetResource(theme.NavigateNextIcon())
			w.stageLabels[i].TextStyle = fyne.TextStyle{Bold: true}
		default:
			w.stageIcons[i].SetResource(theme.RadioButtonIcon())
			w.stageLabels[i].TextStyle = fyne.TextStyle{}
		}
		w.stageLabels[i].Refresh()
	}
	w.phase.SetText(w.s.stageText(stage, w.guestName, false))
	w.bar.Show()
	w.bar.SetValue(0)
	w.numbers.SetText("")
}

// count shows how far the stage is: done of total, speed, time left, time so far.
func (w *work) count(done, total int64) {
	if total <= 0 {
		return
	}
	w.bar.SetValue(float64(done) / float64(total))
	if w.stageFrom < 0 || done < w.stageFrom {
		w.stageFrom, w.stageAt = done, time.Now()
	}
	elapsed := time.Since(w.stageAt)
	// speed, done of total, time so far; the time left once the speed has settled
	speed := w.s.t("measuring speed")
	rate := 0.0
	if elapsed >= time.Second && done > w.stageFrom {
		rate = float64(done-w.stageFrom) / elapsed.Seconds()
		speed = humanBytes(int64(rate)) + "/s"
	}
	parts := []string{speed, w.s.t("%s of %s", humanBytes(done), humanBytes(total)), w.s.t("%s so far", clock(time.Since(w.started)))}
	if rate > 0 && elapsed >= 10*time.Second {
		parts = append(parts, w.s.timeLeft(time.Duration(float64(total-done)/rate*float64(time.Second))))
	}
	w.numbers.SetText(strings.Join(parts, " - "))
}

func (s *screen) timeLeft(d time.Duration) string {
	if d < time.Minute {
		return s.t("less than a minute left")
	}
	return s.t("about %d min left", int((d+time.Minute-1)/time.Minute))
}

// clock is a duration as m:ss or h:mm:ss.
func clock(d time.Duration) string {
	sec := int(d.Seconds())
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// setPlan draws the card and the blocks of a plan.
func (w *work) setPlan(p *flash.Plan) {
	w.plan = p
	strip, groups, empty := w.s.mapPlan(p)
	w.strip.setParts(p.Size, strip)
	w.blocks.setGroups(groups, w.s.t("your data, not touched"))
	if len(empty) > 0 {
		w.empty.SetText(w.s.t("%s: set up empty, nothing to copy", strings.Join(empty, ", ")))
		w.empty.Show()
	} else {
		w.empty.Hide()
	}
}

// progress follows one event of the write job.
func (w *work) progress(p flash.Progress) {
	s := w.s
	if p.Plan != nil {
		w.setPlan(p.Plan)
	}
	if p.Phase != w.lastPhase || p.Name != w.last {
		s.logf("%s: %s", p.Phase, p.Name)
	}
	w.lastPhase, w.last = p.Phase, p.Name
	where := func() string {
		for _, g := range w.blocks.groups {
			for _, b := range g.blocks {
				if p.Offset+p.Length-1 >= b.off && p.Offset+p.Length-1 < b.off+b.length {
					return g.name
				}
			}
		}
		return s.t(p.Name)
	}
	sub := ""
	switch p.Phase {
	case "preflight":
		w.enter(stageCheck)
		sub = s.t(p.Name)
		w.count(p.Done, p.Total)
	case "backup":
		w.enter(stageBackup)
		w.count(p.Done, p.Total)
	case "writing":
		w.enter(stageWrite)
		w.lastOff = p.Offset + p.Length
		w.blocks.mark(p.Offset, p.Length, blockWritten)
		w.strip.setPos(w.lastOff)
		w.phase.SetText(s.stageText(stageWrite, "", false) + ": " + where())
		w.count(p.Done, p.Total)
	case "verifying":
		if p.Length == 0 && p.Offset == 0 {
			// the last read of the whole card says nothing about how far it is
			w.enter(stageVerify)
			sub = s.t(p.Name)
			w.bar.Hide()
			w.numbers.SetText(s.t("%s so far", clock(time.Since(w.started))))
			break
		}
		w.lastOff = p.Offset + p.Length
		w.blocks.mark(p.Offset, p.Length, blockChecked)
		w.strip.setPos(w.lastOff)
		if w.stage == stageVerify {
			w.count(p.Done, p.Total)
		}
	case "restoring":
		sub = s.t(p.Name)
	case "Bootloader":
		w.enter(stageWrite)
		w.phase.SetText(s.stageText(stageWrite, "", false) + ": " + s.t("Bootloader"))
		w.count(p.Done, p.Total)
	}
	if sub != "" {
		w.sub.SetText(sub)
		w.sub.Show()
	} else if p.Phase != "speed" {
		w.sub.Hide()
	}
	w.blocks.update(false)
}

// runWrite takes over the window for the write: the game system download first,
// then the write itself.
func (s *screen) runWrite(g *catalog.GuestRelease, boot *firmware.BootloaderChoice) {
	s.eraseOK = false
	name := ""
	if g != nil {
		src, _ := catalog.FindGuestSource(g.Distribution)
		name = src.Display
	}
	w := s.newWork(name)
	s.logf("write: %s on %s, mode %s, profile %s", s.im.Version, s.drive.Path, s.mode(), s.profile)
	s.setBusy(true)
	if g == nil {
		s.write(w, nil, boot)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	w.stop.Show()
	w.enter(stageDownload)
	go func() {
		dir, e := jobs.CacheDir()
		var path string
		if e == nil {
			path, e = catalog.DownloadGuest(ctx, g, dir, func(phase string, done, total int64) {
				fyne.Do(func() {
					w.count(done, total)
					if phase == "unpack" {
						w.sub.SetText(s.t("Unpacking"))
						w.sub.Show()
					}
				})
			})
		}
		fyne.Do(func() {
			stopped := ctx.Err() != nil
			cancel()
			s.cancel = nil
			w.stop.Hide()
			if s.closeAfter {
				s.win.Close()
				return
			}
			if stopped {
				s.logf("game system download stopped")
				s.setBusy(false)
				s.showStep(stepWrite)
				s.info(s.t("Download stopped"), s.t("The game system was not downloaded. Nothing was written to the card."), s.win)
				return
			}
			if e != nil {
				// the download comes before the card is touched
				w.failed(fmt.Errorf("%w; %w", e, flash.StateUntouched.New()), "", false)
				return
			}
			s.write(w, flashGuest(g, path), boot)
		})
	}()
}

// write runs the write job and follows it on the working page.
func (s *screen) write(w *work, guest *flash.Guest, boot *firmware.BootloaderChoice) {
	p, e := flash.BuildWith(s.im, *s.card, s.drive.Size, s.mode(), s.profile, s.noROMs.Checked, s.userdataGiB, flash.Options{Guest: guest})
	if e != nil {
		w.failed(e, "", false)
		return
	}
	// a write is not stopped halfway
	s.cancel = nil
	w.stop.Hide()
	w.stage = -1
	w.enter(stageCheck)
	w.setPlan(p)
	req := jobs.Request{Action: "write", Drive: *s.drive, Image: s.im.Path, ImageHash: s.im.SHA256, CardFingerprint: s.card.Fingerprint, Mode: p.Mode, Profile: s.profile, NoROMs: p.NoROMs, UserdataGiB: p.UserdataGiB, CustomDTB: s.custom, CustomAccepted: s.customAccepted, ConvertDTB: s.convert, SystemImage: s.system, SystemAccepted: s.systemAccepted}
	req.DTBFile = s.dtbFile
	req.DTBManifest = s.dtbManifest
	req.Bootloader = boot
	req.ImageSource = s.im.Source
	req.Guest = p.Guest
	var speed *flash.CardSpeed
	go func() {
		result, e := jobs.Run(req, func(pr flash.Progress) {
			fyne.Do(func() {
				if pr.Speed != nil {
					speed = pr.Speed
					return
				}
				w.progress(pr)
			})
		})
		fyne.Do(func() {
			s.busy = false
			if detail, busy := busyError(e); busy {
				// nothing was written yet: the user closes what uses the card, then again
				s.logf("card in use: %s", detail)
				w.failedText(s.t("The card is in use"), s.t("The card can’t be written while it is open on this computer (%s). Close any windows or programs that show files from the card, or eject its drives, then try again.", detail),
					s.t(flash.StateUntouched.Key()), s.t("Close what uses the card, then choose Try again."), e, func() {
						s.setBusy(true)
						w.result.RemoveAll()
						s.write(w, guest, boot)
					})
				return
			}
			if e != nil {
				w.failed(e, result.Backup, true)
				return
			}
			s.logf("written and verified")
			w.finished(func() { s.showDone(p, result, speed) })
		})
	}()
}

// finished shows the whole card checked and moves on after a moment or a click.
func (w *work) finished(next func()) {
	w.done = true
	w.stage = stageDone
	for i, st := range w.stages {
		w.stageIcons[i].SetResource(theme.ConfirmIcon())
		w.stageLabels[i].SetText(w.s.stageText(st, w.guestName, true))
		w.stageLabels[i].TextStyle = fyne.TextStyle{}
		w.stageLabels[i].Refresh()
	}
	w.bar.Show()
	w.bar.SetValue(1)
	w.phase.SetText(w.s.t("Your card has been written and verified."))
	w.sub.Hide()
	w.strip.setPos(-1)
	w.blocks.finish()
	w.blocks.update(true)
	once := false
	proceed := func() {
		if !once {
			once = true
			next()
		}
	}
	more := widget.NewButton(w.s.t("Continue"), proceed)
	more.Importance = widget.HighImportance
	w.buttons.Objects = []fyne.CanvasObject{layout.NewSpacer(), more}
	w.buttons.Refresh()
	w.s.setBusy(false)
	time.AfterFunc(1500*time.Millisecond, func() { fyne.Do(proceed) })
}

// failed shows on the working page what went wrong and where, with the ways on.
func (w *work) failed(e error, backup string, wrote bool) {
	s := w.s
	s.logf("write failed: %v", e)
	happened := ""
	if wrote && w.lastOff > 0 {
		name := w.blocks.fail(w.lastOff - 1)
		w.blocks.update(true)
		verb := "It stopped while writing %s at %s."
		if w.lastPhase == "verifying" {
			verb = "It stopped while checking %s at %s."
		}
		if name != "" {
			happened = s.t(verb, name, fmt.Sprintf("%.1f GB", float64(w.lastOff)/1e9)) + " "
		}
	}
	state := ""
	if key, args, ok := i18n.FindState(e); ok {
		state = s.t(key, args...)
	} else if wrote {
		state = s.t(flash.StateIncomplete.Key())
	} else {
		state = s.t(flash.StateUntouched.Key())
	}
	if key, args, ok := i18n.Find(e); ok && key != stateKey(e) {
		happened += s.t(key, args...)
	} else {
		happened += s.t("The card could not be written")
	}
	todo := s.failureHints(e)
	if todo == "" {
		todo = s.t("Check the selected image and card, then try again.")
	}
	if backup != "" {
		todo += "\n\n" + s.t("Recovery backups") + ": " + backup
	}
	w.failedText(s.t("The card could not be written"), happened, state, todo, e, nil)
}

// stateKey is the key of the state an error carries, or "".
func stateKey(e error) string {
	key, _, _ := i18n.FindState(e)
	return key
}

// failedText puts the error card on the working page: what happened, what that
// means for the card and the computer, what to do. retry, when not nil, tries the
// same write again; otherwise Try again goes back to the write page.
func (w *work) failedText(head, happened, state, todo string, e error, retry func()) {
	s := w.s
	w.head.SetText(head)
	w.lead.SetText(s.driveTitle(*s.drive))
	w.phase.Hide()
	w.sub.Hide()
	w.bar.Hide()
	w.numbers.Hide()
	details := widget.NewRichText(&widget.TextSegment{Text: s.t("Technical details") + ": " + e.Error(),
		Style: widget.RichTextStyle{ColorName: theme.ColorNamePlaceHolder, SizeName: theme.SizeNameCaptionText}})
	details.Wrapping = fyne.TextWrapWord
	part := func(name, text string) fyne.CanvasObject {
		return container.New(layout.NewCustomPaddedVBoxLayout(-theme.Padding()), heading(s.t(name)), wrapped(text))
	}
	w.result.Objects = []fyne.CanvasObject{noteCard(noteError,
		part("What happened", happened),
		part("The card and this computer", state+" "+s.t("Nothing on this computer was changed.")),
		part("What to do", todo),
		details)}
	w.result.Refresh()
	again := widget.NewButton(s.t("Try again"), func() {
		if retry != nil {
			w.phase.Show()
			w.bar.Show()
			w.numbers.Show()
			w.head.SetText(s.t("Writing the card"))
			w.lead.SetText(s.t("Do not take the card out.") + " " + s.driveTitle(*s.drive))
			retry()
			return
		}
		s.backToSetup(stepWrite)
	})
	again.Importance = widget.HighImportance
	back := widget.NewButton(s.t("Back to setup"), func() { s.backToSetup(stepCard) })
	w.buttons.Objects = []fyne.CanvasObject{s.copyDetails(e), quiet(s.t("Save log..."), s.saveLog), layout.NewSpacer(), back, again}
	w.buttons.Refresh()
	s.setBusy(false)
}

// backToSetup leaves a finished or failed write. A new card has to be chosen
// again unless the user goes back to the write page to try once more.
func (s *screen) backToSetup(step int) {
	s.busy = false
	if step <= stepCard {
		s.card, s.drive = nil, nil
	}
	s.inSteps = true
	s.step = stepStart
	for i := 0; i <= step; i++ {
		if s.reachable(i) {
			s.step = i
		}
	}
	s.showStep(s.step)
	s.setBusy(false)
}

// showDone is the page after a write that worked.
func (s *screen) showDone(p *flash.Plan, result jobs.Result, speed *flash.CardSpeed) {
	drive := *s.drive
	identity := s.im.BuildIdentity
	waitCtx, stopWaiting := context.WithCancel(context.Background())
	head := title(s.t("The card is ready"))
	lead := wrapped(s.t("Take it out and put it into the console's first slot (TF1)."))
	body := container.NewVBox(container.NewCenter(slotPicture()))
	outcome := []string{}
	warn := false
	if speed != nil && speed.WriteMBps > 0 {
		outcome = append(outcome, s.t("Card speed: %.0f MB/s writing, %.1f ms per small read, %.1f ms per small write", speed.WriteMBps, speed.ReadLatency, speed.WriteLatency))
		if speed.Slow {
			outcome = append([]string{s.t("This card is slow. Android runs from the card, so apps may start slowly and games may stutter. For a smooth experience, use a card from a known brand marked A1 or A2.")}, outcome...)
			warn = true
		}
	}
	if result.Bootloader != nil {
		if result.Bootloader.Warning != "" {
			outcome = append(outcome, s.t(result.Bootloader.Warning))
			warn = true
		}
		outcome = append(outcome, s.bootloaderSummary(result.Bootloader.Record))
	}
	details := wrapped("")
	waiting := heading("")
	waiting.Hide()
	var eject *widget.Button
	if p.Mode == "install" {
		text := s.t(firstBootSteps)
		if p.Prepared && !p.NoROMs {
			text = s.t("The games drive (EASYROMS) is ready: you can copy games to it now, before the first start.") + "\n\n" + text
		}
		text += "\n\n" + s.t("Want to add your own apps? Copy APK files to the AUTOAPK drive now, and the console offers them during setup.")
		details.SetText(text)
		eject = widget.NewButton(s.t("Eject card"), nil)
		eject.OnTapped = func() {
			eject.Disable()
			go func() {
				e := platform.Eject(drive)
				fyne.Do(func() {
					if e != nil {
						eject.Enable()
						s.info(s.t("Eject card"), s.t("The card could not be ejected. Eject it with your computer’s own eject, then insert it into the console’s system card slot.")+"\n\n"+s.t("Technical details")+": "+e.Error(), s.win)
						return
					}
					eject.Hide()
					details.SetText(s.t("The card is ejected. Insert it into the console’s system card slot. The first boot prepares storage and restarts the console. Keep it powered on until Android setup appears. If the screen stays blank, check the chosen board and panel profile."))
				})
			}()
		}
	} else {
		head.SetText(s.t("The card is updated"))
		details.SetText(s.t("Your card has been written and verified. Apps, settings, saves, and games were preserved.") + "\n" + s.t("Recovery backups") + ": " + result.Backup)
	}
	if len(outcome) > 0 {
		kind := notePlain
		if warn {
			kind = noteWarning
		}
		body.Add(noteCard(kind, wrapped(strings.Join(outcome, "\n"))))
	}
	body.Add(details)
	body.Add(waiting)
	another := widget.NewButton(s.t("Write another"), func() {
		stopWaiting()
		s.backToSetup(stepCard)
	})
	closeButton := widget.NewButton(s.t("Close"), func() {
		stopWaiting()
		s.win.Close()
	})
	closeButton.Importance = widget.HighImportance
	s.leave(pageView(head, lead, body, nil, eject, another, closeButton))
	fixes := func(d platform.Drive) {
		after := func() {}
		if s.applyFixes {
			after = func() { s.offerHotfixes(d, identity) }
		}
		s.offerKernelUpdate(d, identity, after)
	}
	if p.Mode == "install" {
		// fixes for this build are offered when the card is back from its first setup
		s.awaitFirstBoot(waitCtx, drive, identity, waiting, fixes)
		return
	}
	fixes(drive)
}
