package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

func TestStorageSlider(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("test")
	defer w.Close()
	s := &screen{win: w, locale: "en", im: &firmware.Image{Profiles: []firmware.Profile{{ID: "Panels/Panel4", Name: "R36S — Panel 4", Console: "R36S"}}}}
	s.build()
	s.drive = &platform.Drive{Size: 128_000_000_000}
	s.card = &flash.Card{}
	s.ready()
	if got := s.storageText(); got != "Android 58 GiB, games 58 GiB" || !s.sizeSlider.Visible() || s.sizeAuto.Disabled() == false {
		t.Fatalf("recommended: %q slider %v", got, s.sizeSlider.Visible())
	}
	s.sizeSlider.SetValue(32)
	if s.userdataGiB != 32 || s.storageText() != "Android 32 GiB, games 84 GiB" {
		t.Fatalf("chosen: %d %q", s.userdataGiB, s.storageText())
	}
	if s.sizeAuto.Disabled() {
		t.Fatal("recommended button stays disabled after a choice")
	}
	s.sizeAuto.OnTapped()
	if s.userdataGiB != 0 {
		t.Fatal("recommended button did not reset the size")
	}
	s.noROMs.SetChecked(true)
	if s.sizeSlider.Visible() || s.storageText() != "Use all available storage for Android" {
		t.Fatal("slider shown with all storage for Android")
	}
	s.noROMs.SetChecked(false)
	s.setTask(taskUpdate)
	if s.sizeSlider.Visible() || s.storageText() != "" || s.storageBox.Visible() || !s.storageKept.Visible() {
		t.Fatal("slider shown for an update")
	}
}

func TestSystemImageSummary(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("test")
	defer w.Close()
	s := &screen{win: w, locale: "en", im: &firmware.Image{Profiles: []firmware.Profile{{ID: "Panels/Panel4", Name: "R36S — Panel 4", Console: "R36S"}}}}
	s.build()
	if strings.Contains(s.alsoText(), "System image") {
		t.Fatal("system image shown without one")
	}
	s.system, s.systemAccepted = "/tmp/lineage-gsi.img", true
	if !strings.HasSuffix(s.alsoText(), "System image: lineage-gsi.img") {
		t.Fatalf("summary %q", s.alsoText())
	}
}
