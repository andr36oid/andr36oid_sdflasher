package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

func TestBootloaderRows(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &screen{locale: "en"}
	p := s.newBootloaderPicker(firmware.BootloaderChoice{}, nil)
	p.setTarget(true, false)
	if p.selection() == nil || p.selection().Choice != "image" || p.controls[0].options[1].reason == "" || p.controls[0].options[2].reason == "" {
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
	if p.controls[0].options[0].reason == "" || p.controls[0].options[2].reason != "" {
		t.Fatal("existing card without image")
	}
	p.setTarget(true, true)
	for _, r := range p.controls {
		if !r.Disabled() {
			t.Fatal("eMMC enabled")
		}
	}
	called := false
	p.confirm(func() { called = true })
	if !called {
		t.Fatal("disabled boot options blocked ordinary data-card flash")
	}
	if p.selection() != nil {
		t.Fatal("unsupported serialized")
	}
	p.failed = true
	p.setTarget(true, false)
	p.selectChoice("image")
	if p.controls[0].options[2].reason == "" || p.selection() == nil {
		t.Fatal("offline fallback")
	}
	// the test-build warning is the boot chain's help, under its control
	found := false
	dtbWidgets(p.rows, func(o fyne.CanvasObject) {
		if l, ok := o.(*widget.Label); ok && strings.Contains(l.Text, loaderTestWarning) {
			found = true
		}
	})
	if p.rows.Visible() || !found {
		t.Fatal("warning wording")
	}
}

func TestAdvancedBootResetAndNotes(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &screen{locale: "en"}
	p := s.newBootloaderPicker(firmware.BootloaderChoice{}, nil)
	p.setTarget(true, false)
	p.loaded = true
	p.builds["standard"] = &firmware.BootloaderBuild{Choice: "standard"}
	p.builds["new"] = &firmware.BootloaderBuild{Choice: "new"}
	p.rules = append(p.rules, firmware.BootRule{Kind: "sleep", ID: "vendor", Label: "Vendor sleep firmware", WorksWith: firmware.BootWorksWith{Chains: []string{"standard"}, Storage: []string{"sd"}}, TestedOn: []string{"R36 Ultra"}, Broken: []firmware.BootBrokenPair{{Field: "bootloader", Value: "new", Reason: "Fixture broken pair"}}})
	p.catalog = &firmware.BootCatalog{Format: 1, Entries: []firmware.BootFirmwareEntry{{BootRule: p.rules[len(p.rules)-1]}}}
	p.target.Console = "Other"
	p.choice = firmware.BootloaderChoice{Choice: "new", Sleep: "vendor"}
	p.refresh()
	broken, untested := false, false
	for _, i := range p.issues() {
		broken = broken || i.Level == "broken"
		untested = untested || i.Level == "untested"
	}
	if !broken || !untested || p.selection() == nil {
		t.Fatal("warnings must allow explicit confirmation")
	}
	p.choice.Chain = "mainline"
	p.updateChoice("chain")
	if p.choice.LoaderID() != "image" || p.choice.SleepID() != "image" || len(p.resetNotes) != 2 || !p.controls[0].Disabled() || !p.controls[1].Disabled() {
		t.Fatal(p.choice, p.resetNotes)
	}
	if p.summary.Text == "" || p.rows.Visible() {
		t.Fatal("nondefault summary hidden")
	}
	p.choice.Chain = "standard"
	p.updateChoice("chain")
	if p.controls[0].Disabled() || p.controls[1].Disabled() {
		t.Fatal("rows did not recover")
	}
}

func TestLaterBootloaderResetsRestrictedSleep(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &screen{locale: "en"}
	p := s.newBootloaderPicker(firmware.BootloaderChoice{}, nil)
	p.setTarget(true, false)
	p.loaded = true
	p.builds["new"] = &firmware.BootloaderBuild{Choice: "new"}
	p.builds["standard"] = &firmware.BootloaderBuild{Choice: "standard"}
	r := firmware.BootRule{Kind: "sleep", ID: "limited", Label: "Limited", WorksWith: firmware.BootWorksWith{Chains: []string{"standard"}, Bootloaders: []string{"standard"}, Storage: []string{"sd"}}}
	p.rules = append(p.rules, r)
	p.catalog = &firmware.BootCatalog{Format: 1, Entries: []firmware.BootFirmwareEntry{{BootRule: r}}}
	p.choice = firmware.BootloaderChoice{Choice: "standard", Sleep: "limited"}
	p.refresh()
	if p.controls[0].options[2].reason != "" {
		t.Fatal("valid later choice disabled instead of resetting other row")
	}
	p.selectChoice("new")
	if p.choice.SleepID() != "image" || len(p.resetNotes) != 1 {
		t.Fatal(p.choice, p.resetNotes)
	}
}
