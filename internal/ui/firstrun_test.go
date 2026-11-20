package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// What the first real card run of the redesigned flasher showed (c13-card).

// A release file from this computer shows its name, size, build and kind on the
// Release page, and the sentence under the title says it was chosen, while it is
// checked and after.
func TestLocalReleaseFileIsShown(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	file := filepath.Join(t.TempDir(), "andr36oid-20261120-1538-5.10-vulkan.img.xz")
	os.WriteFile(file, make([]byte, 3<<20), 0o600)
	s.im, s.asset = nil, nil
	s.localFile, s.localSize = file, 3<<20
	s.showStep(stepImage)
	s.refreshRelease()
	c := s.releaseCard
	if c.title.Text != filepath.Base(file) || !strings.Contains(c.detail.Text, "3.0 MiB") || !strings.Contains(c.detail.Text, "built 2026-11-20") || !strings.Contains(c.detail.Text, "kernel 5.10, Vulkan") {
		t.Fatalf("while checked: %q / %q", c.title.Text, c.detail.Text)
	}
	if s.releaseLead.Text != "You chose a file from this computer." {
		t.Fatal(s.releaseLead.Text)
	}
	// checked
	s.im = &firmware.Image{Version: "v2026-11-20-release", Source: file, Profiles: testProfiles()}
	s.refreshRelease()
	if c.title.Text != "v2026-11-20-release" || !strings.Contains(c.detail.Text, filepath.Base(file)) || !strings.Contains(c.detail.Text, "3.0 MiB") || !strings.Contains(c.detail.Text, "6 device profiles") {
		t.Fatalf("checked: %q / %q", c.title.Text, c.detail.Text)
	}
	if strings.Contains(c.title.Text+c.detail.Text, "Choose a release") || strings.Contains(s.releaseLead.Text, "newest") {
		t.Fatal("placeholder or newest sentence for a file from this computer")
	}
	capture(t, s, os.Getenv("SDFLASHER_SCREENSHOTS"), "c13-release-local-file")
}

// The last page names the card as its row on the Card page does, with its device
// path, and every page gives a card's size the same way.
func TestSummaryNamesTheCardAsTheCardPage(t *testing.T) {
	slot := platform.Drive{Path: "/dev/mmcblk0", Name: "mmcblk0", Size: 62552276992}
	s := testScreen(t, fyne.NewSize(980, 680))
	noBackground(t, []platform.Drive{slot})
	s.drives, s.listed = []platform.Drive{slot}, true
	s.drive, s.card = &slot, &flash.Card{Reason: "empty partition table"}
	s.guests["dArkOS"] = &guestLookup{done: true, g: testGuest()}
	s.chooseProfile("Panels/Panel4")
	s.ready()
	s.showStep(stepWrite)
	card := "SD card in the built-in slot - 62.6 GB - /dev/mmcblk0"
	if !strings.Contains(allText(s), card) {
		t.Fatalf("summary does not name %q:\n%s", card, allText(s))
	}
	// the Card page's row: the same name and size, and the path
	s.showStep(stepCard)
	if text := allText(s); !strings.Contains(text, "SD card in the built-in slot - 62.6 GB") || !strings.Contains(text, "/dev/mmcblk0") {
		t.Fatal("card page:", text)
	}
	s.showStep(stepWrite)
	capture(t, s, os.Getenv("SDFLASHER_SCREENSHOTS"), "c13-write-summary")
	s.newWork("")
	if text := allText(s); !strings.Contains(text, "The whole card (62.6 GB)") || strings.Contains(text, "63 GB") {
		t.Fatal("working page size:", text)
	}
}

// allText is every text of the page shown, one per line.
func allText(s *screen) string {
	text := ""
	dtbWidgets(s.current(), func(o fyne.CanvasObject) {
		switch l := o.(type) {
		case *widget.Label:
			text += l.Text + "\n"
		case *tapLabel:
			text += l.Text + "\n"
		case *widget.RichText:
			text += l.String() + "\n"
		}
	})
	return text
}

// A card that stops answering in the speed check: the strip shows the card test,
// and the error says that the card stopped answering in the card test before
// anything of the system was written, with the low-level error in the details.
func TestCardDiesInTheCardTest(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/mmcblk0", Name: "mmcblk0", Size: 62552276992}
	s.card = &flash.Card{}
	plan := testPlan(false)
	w := s.newWork("dArkOS")
	w.enter(stageDownload)
	for _, name := range []string{"Validating release", flash.OpenCheck, "Preparing the bootloader", "Checking source image", "Checking the game system", flash.ReadCheck, flash.SizeCheck, flash.SpeedCheck} {
		w.progress(flash.Progress{Phase: "preflight", Name: name, Plan: plan})
		want := stageCheck
		if cardTest(name) {
			want = stageCardTest
		}
		if w.stage != want {
			t.Fatalf("%s: stage %d, want %d", name, w.stage, want)
		}
	}
	for i, st := range w.stages {
		if st == stageCardTest && w.stageLabels[i].Text != "Testing the card" {
			t.Fatal(w.stageLabels[i].Text)
		}
	}
	e := fmt.Errorf("%w; %w", flash.DeviceFailure(&os.PathError{Op: "read", Path: "/dev/mmcblk0", Err: syscall.EIO}), flash.StateCardTest.New())
	w.failed(e, "", true)
	text := allText(s)
	for _, want := range []string{
		"The card failed the card test",
		"It stopped at this step: Testing the card: Checking card speed.",
		"The card stopped answering during the card test, before anything of the system was written. This usually means the card is defective or counterfeit.",
		"Nothing of andr36oid was written yet.",
		"Try another card",
		"read /dev/mmcblk0: input/output error",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	for _, wrong := range []string{"The card is incomplete", "Checking the image"} {
		if strings.Contains(text, wrong) {
			t.Fatalf("says %q:\n%s", wrong, text)
		}
	}
	capture(t, s, os.Getenv("SDFLASHER_SCREENSHOTS"), "c13-card-test-failed")
}

// While the game system is unpacked, the step and the line above the bar say so,
// and the time left comes from the unpacked bytes of this phase only: a download of
// some minutes before does not make it look slow.
func TestGuestUnpackSaysUnpackingWithAnHonestTime(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Name: "Card Reader", Size: 64e9}
	w := s.newWork("dArkOS")
	w.enter(stageDownload)
	// downloaded for two minutes, then unpacking for 20 s at 20 MB/s of 3.8 GB
	w.stageAt, w.stageFrom = time.Now().Add(-140*time.Second), 0
	w.count(2200<<20, 2200<<20)
	s.activity = actGuestDownload
	if s.activity != actGuestUnpack {
		s.activity = actGuestUnpack
		w.unpacking()
	}
	w.count(0, 3800e6)
	w.stageAt = time.Now().Add(-20 * time.Second)
	w.count(400e6, 3800e6)
	if w.phase.Text != "Unpacking dArkOS..." || w.stageLabels[0].Text != "Unpacking dArkOS..." {
		t.Fatalf("%q / %q", w.phase.Text, w.stageLabels[0].Text)
	}
	// 3.4 GB left at 20 MB/s: about 3 minutes, not most of an hour
	if !strings.Contains(w.numbers.Text, "about 3 min left") {
		t.Fatal(w.numbers.Text)
	}
}

// A progress event without a name is logged without an empty value.
func TestLogHasNoEmptyValue(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	s.drive = &platform.Drive{Path: "/dev/sdb", Name: "Card Reader", Size: 64e9}
	w := s.newWork("")
	w.progress(flash.Progress{Phase: "Bootloader"})
	for _, line := range s.log {
		if strings.HasSuffix(strings.TrimSpace(line), ":") {
			t.Fatalf("empty value: %q", line)
		}
	}
}
