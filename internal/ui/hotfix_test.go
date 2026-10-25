package ui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

func TestHotfixChecklistDefaultsAndToggles(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &screen{locale: "en"}
	offer := &firmware.HotfixOffer{Hotfixes: []firmware.Hotfix{
		{Title: "Audio fix", Description: "Fixes\naudio", Type: "bugfix", Size: 1024},
		{Title: "USB preset", Description: "Optional preset", Type: "extra", Size: 2048},
		{Title: "Future feature", Description: "Optional feature", Type: "future", Size: 4096},
	}}
	content, checks := s.hotfixChecklist(offer)
	if len(content.Objects) != 3 || len(checks) != 3 {
		t.Fatal("incomplete checklist")
	}
	if !checks[0].Checked || checks[1].Checked || checks[2].Checked {
		t.Fatal("incorrect defaults")
	}
	for _, check := range checks {
		test.Tap(check)
	}
	if checks[0].Checked || !checks[1].Checked || !checks[2].Checked {
		t.Fatal("items cannot be toggled")
	}
}
