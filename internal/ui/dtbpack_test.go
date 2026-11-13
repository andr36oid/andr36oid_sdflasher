package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"testing"
)

func dtbWidgets(o fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	visit(o)
	switch c := o.(type) {
	case *fyne.Container:
		for _, child := range c.Objects {
			dtbWidgets(child, visit)
		}
	case *widget.PopUp:
		dtbWidgets(c.Content, visit)
	case *container.Scroll:
		dtbWidgets(c.Content, visit)
	default:
		if w, ok := o.(fyne.Widget); ok {
			for _, child := range test.WidgetRenderer(w).Objects() {
				dtbWidgets(child, visit)
			}
		}
	}
}
func TestDTBScreenWarningOnlyOnByteChange(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("test")
	defer w.Close()
	w.Resize(fyne.NewSize(800, 760))
	w.SetContent(container.NewVBox())
	s := &screen{win: w, locale: "en"}
	calls := 0
	s.confirmDTBScreen(&firmware.DTBPlan{}, func() { calls++ })
	if calls != 1 || w.Canvas().Overlays().Top() != nil {
		t.Fatal("unchanged files showed warning")
	}
	s.confirmDTBScreen(&firmware.DTBPlan{ScreenChanged: true}, func() { calls++ })
	if calls != 1 {
		t.Fatal("write ran before confirmation")
	}
	var accept, cancel *widget.Button
	foundBody, foundTitle := false, false
	dtbWidgets(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok {
			if b.Text == "I'll watch the screen" {
				accept = b
			}
			if b.Text == "Cancel" {
				cancel = b
			}
		}
		if l, ok := o.(*canvas.Text); ok {
			foundTitle = foundTitle || l.Text == firmware.DTBScreenWarningTitle
		}
		if l, ok := o.(*widget.Label); ok {
			foundBody = foundBody || l.Text == firmware.DTBScreenWarning
			foundTitle = foundTitle || l.Text == firmware.DTBScreenWarningTitle
		}
	})
	if accept == nil || cancel == nil || !foundBody || !foundTitle {
		t.Fatal("warning text/buttons mismatch", accept, cancel, foundBody, foundTitle)
	}
	test.Tap(cancel)
	if calls != 1 {
		t.Fatal("cancel started write")
	}
	s.confirmDTBScreen(&firmware.DTBPlan{ScreenChanged: true}, func() { calls++ })
	dtbWidgets(w.Canvas().Overlays().Top(), func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Text == "I'll watch the screen" {
			accept = b
		}
	})
	test.Tap(accept)
	if calls != 2 {
		t.Fatal("confirm did not advance")
	}
}
func TestDTBProfileGroups(t *testing.T) {
	plan := &firmware.DTBPlan{Setup: firmware.CardSetup{Profile: "Panels/Panel4"}, Profiles: []firmware.Profile{{ID: "Panels/Panel4", Console: "R36S"}, {ID: "Panels/Panel3", Console: "R36S"}, {ID: "Devices/R50S", Console: "R50S"}, {ID: "Experimental/Alias", Console: "Other", AlsoFor: []string{"R36S"}, Experimental: true}}}
	known := knownDTBProfiles(plan)
	if len(known) != 3 {
		t.Fatal(known)
	}
	for _, p := range known {
		if p.ID == "Devices/R50S" {
			t.Fatal("foreign console in known group")
		}
	}
	plan.Setup.Profile = ""
	if len(knownDTBProfiles(plan)) != 0 {
		t.Fatal("guessed default console wiring")
	}
}
