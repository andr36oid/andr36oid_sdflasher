package ui

import (
	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"testing"
)

func TestBootloaderRows(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &screen{locale: "en"}
	p := s.newBootloaderPicker(firmware.BootloaderChoice{}, nil)
	p.setTarget(true, false)
	if p.selection() == nil || p.selection().Choice != "image" || !p.radios[1].Disabled() || !p.radios[2].Disabled() {
		t.Fatal("default/loading state")
	}
	p.loaded = true
	p.builds["new"] = &firmware.BootloaderBuild{Choice: "new", Version: "2026.10", Tag: "20261113-0351"}
	p.refresh()
	p.selectChoice("new")
	if p.selection() == nil || p.selection().Build == nil {
		t.Fatal("new choice unavailable")
	}
	p.setTarget(false, false)
	if !p.radios[0].Disabled() || p.radios[2].Disabled() {
		t.Fatal("existing card without image")
	}
	p.setTarget(true, true)
	for _, r := range p.radios {
		if !r.Disabled() {
			t.Fatal("eMMC enabled")
		}
	}
	if p.selection() != nil {
		t.Fatal("unsupported serialized")
	}
	p.failed = true
	p.setTarget(true, false)
	p.selectChoice("image")
	if !p.radios[2].Disabled() || p.selection() == nil {
		t.Fatal("offline fallback")
	}
	if p.warning.Text != loaderTestWarning {
		t.Fatal("warning wording")
	}
}
