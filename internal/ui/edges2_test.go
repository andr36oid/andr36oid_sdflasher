package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// plainText: hyphens only, three plain dots, no "GPLv3".
func plainText(t *testing.T, where, text string) {
	t.Helper()
	if strings.ContainsAny(text, "\u00b7\u2013\u2014\u2026\u2022") || strings.Contains(text, "GPLv3") {
		t.Errorf("%s: %q", where, text)
	}
}

// Closing the window names the step that runs; what can be stopped (nothing is
// written then) is offered to stop, the rest asks to wait.
func TestCloseNamesTheStep(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.busy = true
	for act, want := range map[int]string{
		actDownload:      "Stop the download?",
		actCheckImage:    "Stop checking the image?",
		actUnpackImage:   "Stop unpacking the image?",
		actGuestDownload: "Stop downloading the game system?",
		actGuestUnpack:   "Stop unpacking the game system?",
	} {
		s.activity = act
		s.cancel = func() {}
		title, body, stoppable := s.closePrompt()
		if title != want || !stoppable || !strings.Contains(body, "Nothing was written to the card.") {
			t.Errorf("%d: %q %v", act, title, stoppable)
		}
		plainText(t, title, body)
	}
	s.cancel = nil
	for act, want := range map[int]string{
		actWriteCheck:   "The image is being checked",
		actBackup:       "A backup is being saved",
		actWrite:        "The card is being written",
		actGuestInstall: "The game system is being installed",
		actVerify:       "The card is being verified",
		actBoot:         "Boot options are being applied",
		actReadCard:     "The card is being read",
		actNone:         "Operation in progress",
	} {
		s.activity = act
		title, body, stoppable := s.closePrompt()
		if title != want || stoppable {
			t.Errorf("%d: %q %v", act, title, stoppable)
		}
		plainText(t, title, body)
	}
	// the window stays while busy
	s.activity = actWrite
	s.closeRequested()
	if s.closeAfter {
		t.Fatal("closes during the write")
	}
	if imageActivity("/a/andr36oid.img.xz") != actUnpackImage || imageActivity("/a/b.img") != actCheckImage {
		t.Fatal("image activity")
	}
	// the stages of the working page, and the game system's partition
	s.drive = &platform.Drive{Path: "/dev/sdb", Name: "Card Reader", Size: 64e9}
	w := s.newWork("dArkOS")
	for stage, act := range stageActivity {
		w.enter(stage)
		if s.activity != act {
			t.Errorf("stage %d: %d", stage, s.activity)
		}
	}
	w.plan = &flash.Plan{Size: 64e9, Writes: []flash.Write{{Range: flash.Range{Name: "System", Offset: 0, Length: 10}}, {Range: flash.Range{Name: "dArkOS", Offset: 1 << 30, Length: 10}, Guest: true}}}
	w.progress(flash.Progress{Phase: "writing", Name: "System", Offset: 0, Length: 5, Done: 5, Total: 20})
	if s.activity != actWrite {
		t.Fatal(s.activity)
	}
	w.progress(flash.Progress{Phase: "writing", Name: "dArkOS", Offset: 1 << 30, Length: 5, Done: 15, Total: 20})
	if s.activity != actGuestInstall {
		t.Fatal(s.activity)
	}
	w.progress(flash.Progress{Phase: "Bootloader"})
	if s.activity != actBoot {
		t.Fatal(s.activity)
	}
	s.setBusy(false)
	if s.activity != actNone {
		t.Fatal("activity kept after the work")
	}
}

// A stop request that did not reach the helper says so, not "an older helper";
// both say that writing goes on and what taking the card out means.
func TestStopRefusedSaysWhatHappened(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	title, text := s.stopRefused(errors.New("open /tmp/operation-1/stop: read-only file system"))
	if title != "The stop request did not reach the disk helper" || !strings.Contains(text, "could not pass the stop request") || !strings.Contains(text, "read-only file system") || strings.Contains(text, "older") {
		t.Fatal(title, text)
	}
	title, text = s.stopRefused(nil)
	if title != "Writing can't be stopped" || !strings.Contains(text, "did not answer") {
		t.Fatal(title, text)
	}
	for _, x := range []string{title, text} {
		if !strings.Contains(text, "writing goes on") || !strings.Contains(text, "incomplete") {
			t.Fatal(x)
		}
		plainText(t, "stop", x)
	}
}

// A failed game system lookup has Try again and says the rest works without it;
// Try again looks once more for the failed systems only.
func TestGuestLookupTryAgain(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Size: 128e9, Name: "Card Reader"}
	s.card = &flash.Card{}
	s.guestChoice, s.guestChosen = catalog.DefaultGuest, true
	for _, src := range catalog.GuestSources {
		s.guests[src.ID] = &guestLookup{done: true}
	}
	s.guests[catalog.DefaultGuest] = &guestLookup{done: true, err: errors.New("dial tcp: lookup api.github.com: no such host")}
	s.ready()
	if !s.guestRetry.Visible() || !strings.Contains(s.guestInfo.Text, "works without it") || !strings.Contains(s.guestInfo.Text, i18n.CauseOffline.Key()) {
		t.Fatal(s.guestRetry.Visible(), s.guestInfo.Text)
	}
	asked := map[string]int{}
	old := latestGuest
	t.Cleanup(func() { latestGuest = old })
	latestGuest = func(_ context.Context, src catalog.GuestSource) (*catalog.GuestRelease, error) {
		asked[src.ID]++
		return nil, nil
	}
	s.guestRetry.OnTapped()
	if asked[catalog.DefaultGuest] != 1 || len(asked) != 1 {
		t.Fatal(asked)
	}
	if st, _ := s.guestState(catalog.DefaultGuest); st != guestNone || s.guestRetry.Visible() {
		t.Fatal(st, s.guestRetry.Visible())
	}
}

// Try again after a failed write goes where the fix is: the card step for a card
// that changed, the release step for an image that is gone or changed.
func TestFailedWriteTryAgainGoesWhereTheFixIs(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Name: "Card Reader", Size: 64e9}
	for name, c := range map[string]struct {
		e     error
		again int
		state string
	}{
		"card swapped":  {errors.New("card changed after inspection; select it again; Nothing was written to the card."), stepCard, flash.StateUntouched.Key()},
		"card gone":     {i18n.Detailed(platform.ErrCardChanged.New(), errors.New("the selected card was removed")), stepCard, ""},
		"image gone":    {errors.New("open /home/u/.cache/andr36oid-sdflasher/image-1.img: no such file or directory; Nothing was written to the card."), stepImage, flash.StateUntouched.Key()},
		"image changed": {errors.New("source image changed after inspection; Nothing was written to the card."), stepImage, flash.StateUntouched.Key()},
		"reader":        {errors.New("write /dev/sdb: input/output error; " + flash.StateIncomplete.Key()), stepWrite, flash.StateIncomplete.Key()},
	} {
		w := s.newWork("")
		w.failed(c.e, "", true)
		if w.again != c.again {
			t.Errorf("%s: again %d", name, w.again)
		}
		if todo := s.whatToDo(c.e); todo == s.t("Check the selected image and card, then try again.") {
			t.Errorf("%s: general advice", name)
		}
		if c.state != "" {
			if key, _, _ := i18n.FindState(c.e); key != c.state {
				t.Errorf("%s: state %q", name, key)
			}
		}
	}
	// the damaged game system download says it was deleted and is downloaded again
	w := s.newWork("dArkOS")
	w.enter(stageDownload)
	w.failed(fmt.Errorf("%w; %w", i18n.Detailed(catalog.ErrDamagedDownload.New(), errors.New("checksum")), flash.StateUntouched.New()), "", false)
	if w.again != stepWrite {
		t.Fatal(w.again)
	}
}

// What to do after the failures this pass added.
func TestWhatToDoNewFailures(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	general := s.t("Check the selected image and card, then try again.")
	for name, c := range map[string]struct {
		e    error
		want string
	}{
		"limit with time": {i18n.Detailed(catalog.ErrRateLimitedFor.New("12"), errors.New("HTTP 403")), "Wait as long as GitHub asks"},
		"portal":          {catalog.ErrPortal.New(), "sign in to the network"},
		"cache folder":    {jobs.ErrCacheFolder.New("/ro/cache"), "Make that folder writable"},
		"helper killed":   {flash.DeviceFailure(fmt.Errorf("%w (signal: killed)", flash.ErrStoppedMidWrite.New())), "written again from the start"},
		"elevation":       {i18n.Detailed(i18n.CauseElevationRefused.New(), platform.ErrElevationRefused), "allow administrator access"},
		"file gone":       {errors.New("open /x/image.img: no such file or directory"), "Release page"},
		"long path":       {errors.New("open C:\\very\\long: The filename or extension is too long."), "short path"},
		"disk full":       {firmware.ErrNoSpace.New("/home/u/.cache/andr36oid-sdflasher", "6.0 GB", "1.0 GB"), "Free up space"},
	} {
		got := s.whatToDo(c.e)
		if got == general || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q", name, got)
		}
		plainText(t, name, got)
	}
	// the elevation message itself says what happened
	if !strings.Contains(s.explain(i18n.Detailed(i18n.CauseElevationRefused.New(), platform.ErrElevationRefused)), "was not given") {
		t.Fatal("elevation")
	}
}

// A card that holds no andr36oid that can be updated says why in plain words.
func TestCardReasonIsPlain(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	for reason, want := range map[string]string{
		"The userdata filesystem was not recognized":                          "not formatted",
		"no valid partition table":                                            "no andr36oid partition table",
		"This card has not finished its first boot; use a fresh installation": "first start",
		"BOOT filesystem was not recognized":                                  "BOOT partition could not be read",
		"This is not a recognized andr36oid installation":                     "does not hold andr36oid",
		"Unrecognized legacy MBR layout":                                      "not laid out",
		"The existing Android device tree was not found":                      "no Android device tree",
	} {
		got := s.cardReason(reason)
		if !strings.Contains(got, want) {
			t.Errorf("%q: %q", reason, got)
		}
		plainText(t, reason, got)
	}
	// the update note names the reason
	s.setTask(taskUpdate)
	s.drive = &platform.Drive{Path: "/dev/sdb", Name: "Card Reader", Size: 64e9}
	s.card = &flash.Card{Reason: "The userdata filesystem was not recognized"}
	s.ready()
	text := ""
	for _, o := range s.cardNote.Objects {
		text += fmt.Sprint(labels(o))
	}
	if !strings.Contains(text, "not formatted") || !strings.Contains(text, "no andr36oid to update") {
		t.Fatal(text)
	}
}

// Two cards of the same name and size are told apart by their device path, also
// where only the title is shown (the write page, the working page).
func TestSameNameCardsShowTheirPath(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	a := platform.Drive{Path: "/dev/sdb", Name: "Generic STORAGE DEVICE", Size: 31914983424}
	b := platform.Drive{Path: "/dev/sdc", Name: "Generic STORAGE DEVICE", Size: 31914983424}
	s.setDrives([]platform.Drive{a, b})
	if ta, tb := s.driveTitle(a), s.driveTitle(b); ta == tb || !strings.Contains(ta, "/dev/sdb") || !strings.Contains(tb, "/dev/sdc") {
		t.Fatal(ta, tb)
	}
	s.setDrives([]platform.Drive{a})
	if strings.Contains(s.driveTitle(a), "/dev/sdb") {
		t.Fatal("path shown for a single card")
	}
}

// A chosen card that is no longer listed (taken out, another one in its place) is
// dropped with a line that says so, and nothing was written.
func TestRemovedCardIsSaid(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	a := platform.Drive{Path: "/dev/sdb", Name: "Card Reader", Size: 64e9}
	s.setDrives([]platform.Drive{a})
	s.drive, s.card = &a, &flash.Card{}
	// the same reader, now with a card of another size
	s.setDrives([]platform.Drive{{Path: "/dev/sdb", Name: "Card Reader", Size: 32e9}})
	if s.drive != nil || !strings.Contains(s.cardLabel.Text, "no longer there") || !strings.Contains(s.cardLabel.Text, "Nothing was written") {
		t.Fatal(s.drive, s.cardLabel.Text)
	}
}

// A card set up with a profile that the chosen release does not carry says so on
// the console page.
func TestMissingProfileIsSaid(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.card = &flash.Card{Installed: true, Treble: true, Profile: "Devices/SomeCloneNotInThisRelease"}
	s.autoProfile()
	s.refreshConsoles()
	if s.profile != "" || !strings.Contains(s.consoleLead.Text, "does not carry") {
		t.Fatal(s.profile, s.consoleLead.Text)
	}
}

// The image file the write reads is checked before the administrator prompt: a
// file that is gone or of another size is said plainly, and the release is
// chosen once more.
func TestImageGoneBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "andr36oid 日本 ñ.img")
	os.WriteFile(p, make([]byte, 4096), 0600)
	im := &firmware.Image{Path: p, Size: 4096}
	if e := imageStillThere(im); e != nil {
		t.Fatal(e)
	}
	im.Size = 8192
	if e := imageStillThere(im); !errImageGone.Is(e) {
		t.Fatal(e)
	}
	os.Remove(p)
	im.Size = 4096
	e := imageStillThere(im)
	s := testScreen(t, fyne.NewSize(980, 680))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if got := s.explain(e); !strings.Contains(got, "no longer where it was") || !strings.Contains(got, p) {
		t.Fatal(got)
	}
	s.dropImage()
	if s.im != nil || s.reachable(stepCard) {
		t.Fatal("image kept")
	}
}

// Switching the language keeps every choice; only the tick before erasing is
// asked again (its text changed).
func TestLanguageSwitchKeepsChoices(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	d := platform.Drive{Path: "/dev/sdb", Name: "Card Reader", Size: 64e9}
	s.setDrives([]platform.Drive{d})
	s.drive, s.card = &d, &flash.Card{}
	s.chooseProfile("Panels/Panel3")
	s.guestChoice, s.guestChosen = catalog.NoGuest, true
	s.locale = "de"
	s.build()
	if s.drive == nil || s.card == nil || s.profile != "Panels/Panel3" || s.im == nil || s.guestChoice != catalog.NoGuest {
		t.Fatal(s.drive, s.card, s.profile, s.guestChoice)
	}
	s.locale = "en"
	s.build()
	if s.profile != "Panels/Panel3" {
		t.Fatal(s.profile)
	}
}

// A release download that failed offers to download again, except where that
// can't help.
func TestDownloadRetryable(t *testing.T) {
	for e, want := range map[error]bool{
		catalog.ErrDamagedDownload.New():                  true,
		catalog.ErrCutOff.New():                           true,
		errors.New("dial tcp: no such host"):              true,
		catalog.ErrNotThere.New():                         false,
		firmware.ErrNoSpace.New("/c", "6.0 GB", "1.0 GB"): false,
		errors.New("write /c/x: no space left on device"): false,
		jobs.ErrCacheFolder.New("/c"):                     false,
	} {
		if downloadRetryable(e) != want {
			t.Errorf("%v: %v", e, !want)
		}
	}
}

// labels are the texts of the labels in o, depth first.
func labels(o fyne.CanvasObject) []string {
	var out []string
	switch x := o.(type) {
	case *widget.Label:
		out = append(out, x.Text)
	case *fyne.Container:
		for _, c := range x.Objects {
			out = append(out, labels(c)...)
		}
	}
	return out
}

// A second flasher started while this one has a release unpacked does not clear
// that image: this one keeps it fresh (keepStaged) while it is chosen.
func TestSecondInstanceKeepsTheChosenImage(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, e := jobs.CacheDir()
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "image-123.img")
	os.WriteFile(p, []byte("image"), 0600)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	s.im.Path = p
	s.keepStaged()
	firmware.CleanWorkDir(dir)
	if _, e := os.Stat(p); e != nil {
		t.Fatal("cleared by the second instance:", e)
	}
	// once it is no longer chosen, it goes
	s.im = nil
	os.Chtimes(p, old, old)
	s.keepStaged()
	firmware.CleanWorkDir(dir)
	if _, e := os.Stat(p); e == nil {
		t.Fatal("kept")
	}
}
