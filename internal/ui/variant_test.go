package ui

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// fourImages is a release as GitHub lists it: every kernel line and graphics driver.
func fourImages(release string, kinds ...string) []catalog.Asset {
	if len(kinds) == 0 {
		kinds = []string{"k510-vulkan", "k510-gles", "k419-vulkan", "k419-gles"}
	}
	var list []catalog.Asset
	for _, k := range kinds {
		name := "andr36oid-20261120-1200-rk3326-" + k + ".img.xz"
		list = append(list, catalog.Asset{Name: name, Release: release, URL: "https://example.invalid/" + release + "/" + name, Size: 2e9})
	}
	return list
}

// updateScreen is the update of a card of this kind, with these releases found and
// each release's recommended image chosen, as lookupReleases leaves it.
func updateScreen(t *testing.T, kernel, gpu string, assets []catalog.Asset) *screen {
	t.Helper()
	s := testScreen(t, fyne.NewSize(980, 680))
	s.assets, s.otherAssets = catalog.Split(assets)
	s.asset = &s.assets[0]
	s.drive = &platform.Drive{Path: "/dev/sdb", Size: 64e9, Name: "Card Reader"}
	s.card = &flash.Card{Installed: true, Treble: true, Profile: "Panels/Panel4", Fingerprint: "card-1", Kernel: kernel, Graphics: gpu}
	s.setTask(taskUpdate)
	return s
}

func noteText(s *screen) string {
	var out []string
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Label:
			out = append(out, x.Text)
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		}
	}
	walk(s.variantBox)
	return strings.Join(out, "\n")
}

func noteSelect(s *screen) *widget.Select {
	var found *widget.Select
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Select:
			found = x
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		}
	}
	walk(s.variantBox)
	return found
}

func noteHasButton(s *screen, text string) bool {
	found := false
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch x := o.(type) {
		case *widget.Button:
			found = found || x.Text == text
		case *fyne.Container:
			for _, c := range x.Objects {
				walk(c)
			}
		}
	}
	walk(s.variantBox)
	return found
}

func TestUpdateKeepsTheCardsKind(t *testing.T) {
	for _, c := range []struct{ kernel, gpu, file, says string }{
		{"4.19", "gles", "k419-gles", "This card runs the 4.19 kernel with OpenGL ES only. The update keeps that."},
		{"4.19", "vulkan", "k419-vulkan", "This card runs the 4.19 kernel with Vulkan. The update keeps that."},
		{"5.10", "gles", "k510-gles", "This card runs the 5.10 kernel with OpenGL ES only. The update keeps that."},
		{"5.10", "vulkan", "k510-vulkan", "This card runs the 5.10 kernel with Vulkan. The update keeps that."},
	} {
		s := updateScreen(t, c.kernel, c.gpu, append(fourImages("v2026.11.20"), fourImages("v2026.11.16")...))
		s.showStep(stepImage)
		if !strings.Contains(s.asset.Name, c.file) || s.asset.Release != "v2026.11.20" {
			t.Fatal(c.kernel, c.gpu, "chose", s.asset.Name, s.asset.Release)
		}
		if got := noteText(s); got != c.says {
			t.Fatalf("%s %s: %q", c.kernel, c.gpu, got)
		}
		if !s.variantOK() || s.nexts[stepImage].Disabled() || noteSelect(s) != nil {
			t.Fatal("matching image not accepted, or a choice offered")
		}
		// an older release from Other releases: its image of the same kind
		older := fourImages("v2026.11.16")[0]
		s.asset = &older
		s.ready()
		if !strings.Contains(s.asset.Name, c.file) || s.asset.Release != "v2026.11.16" {
			t.Fatal("older release:", s.asset.Name)
		}
	}
}

func TestUpdateStepsReadTheCardFirst(t *testing.T) {
	s := updateScreen(t, "4.19", "gles", fourImages("r"))
	if s.after(stepStart) != stepCard || s.after(stepCard) != stepImage || s.after(stepImage) != stepConsole || s.before(stepImage) != stepCard {
		t.Fatal("update order", s.order())
	}
	if s.sideSteps[1].Text != "Card" || s.sideSteps[2].Text != "Release" {
		t.Fatal("side bar", s.sideSteps[1].Text, s.sideSteps[2].Text)
	}
	s.setTask(taskNew)
	if s.after(stepStart) != stepImage || s.sideSteps[1].Text != "Release" {
		t.Fatal("new card order", s.order())
	}
}

// The release has no image of the card's kind: said, the release's images offered,
// Next waits for a choice.
func TestUpdateReleaseWithoutTheKind(t *testing.T) {
	s := updateScreen(t, "4.19", "gles", fourImages("r", "k510-vulkan", "k510-gles"))
	s.showStep(stepImage)
	if !strings.Contains(s.asset.Name, "k510-vulkan") {
		t.Fatal("switched silently to", s.asset.Name)
	}
	if got := noteText(s); !strings.Contains(got, "This card runs the 4.19 kernel with OpenGL ES only. Release r has no image of that kind.") {
		t.Fatal(got)
	}
	if s.variantOK() || !s.nexts[stepImage].Disabled() {
		t.Fatal("an image of another kind is taken without a choice")
	}
	// a loaded image of another kind does not count as done either
	if s.stepDone(stepImage) {
		t.Fatal("the 5.10 Vulkan image counts as done for a 4.19 card")
	}
	sel := noteSelect(s)
	if sel == nil || len(sel.Options) != 2 || sel.SelectedIndex() != -1 {
		t.Fatal("no choice of the release's images", sel)
	}
	sel.SetSelectedIndex(1)
	if !strings.Contains(s.asset.Name, "k510-gles") || !s.variantOK() || s.nexts[stepImage].Disabled() {
		t.Fatal("choice not taken", s.asset.Name)
	}
	if got := noteText(s); !strings.Contains(got, "You chose the 5.10 kernel with OpenGL ES only instead. The kernel changes from 4.19 to 5.10.") {
		t.Fatal(got)
	}
}

// A card that does not say: said, the recommended image chosen, the others offered.
func TestUpdateCardKindUnknown(t *testing.T) {
	s := updateScreen(t, "", "", fourImages("r"))
	s.showStep(stepImage)
	if !strings.Contains(s.asset.Name, "k510-vulkan") {
		t.Fatal("not the recommended image:", s.asset.Name)
	}
	if got := noteText(s); !strings.HasPrefix(got, "Which kernel and graphics driver this card runs could not be read. Choose the image to install:") {
		t.Fatal(got)
	}
	sel := noteSelect(s)
	if sel == nil || len(sel.Options) != 4 || sel.SelectedIndex() != 0 || !s.variantOK() {
		t.Fatal("recommended not preselected", sel)
	}
	sel.SetSelectedIndex(3)
	if !strings.Contains(s.asset.Name, "k419-gles") || !s.variantOK() {
		t.Fatal("choice not taken", s.asset.Name)
	}
	// only the kernel line known (a card from before the four-image releases)
	s = updateScreen(t, "4.19", "", fourImages("r"))
	s.showStep(stepImage)
	if !strings.Contains(s.asset.Name, "k419-vulkan") {
		t.Fatal("kernel line not kept:", s.asset.Name)
	}
	if got := noteText(s); !strings.HasPrefix(got, "This card runs the 4.19 kernel. Which graphics driver it has could not be read.") {
		t.Fatal(got)
	}
}

// Advanced...: another kind on purpose, for this card only.
func TestUpdateAdvancedSwitch(t *testing.T) {
	s := updateScreen(t, "5.10", "vulkan", fourImages("r"))
	s.showStep(stepImage)
	if !noteHasButton(s, "Advanced...") {
		t.Fatal("no Advanced...")
	}
	s.pick(fourImages("r")[3])
	if !strings.Contains(s.asset.Name, "k419-gles") || !s.variantOK() {
		t.Fatal("switch not kept", s.asset.Name)
	}
	if got := noteText(s); got != "This card runs the 5.10 kernel with Vulkan. You chose the 4.19 kernel with OpenGL ES only instead. The kernel changes from 5.10 to 4.19 and the graphics driver from Vulkan to OpenGL ES only. Apps, settings, saves and games stay." {
		t.Fatal(got)
	}
	// another card: the switch was for the first one only
	s.card = &flash.Card{Installed: true, Treble: true, Profile: "Panels/Panel4", Fingerprint: "card-2", Kernel: "5.10", Graphics: "vulkan"}
	s.ready()
	if !strings.Contains(s.asset.Name, "k510-vulkan") {
		t.Fatal("switch carried to another card:", s.asset.Name)
	}
	if got := s.changeText("4.19", "gles", "4.19", "vulkan"); got != "The graphics driver changes from OpenGL ES only to Vulkan. Apps, settings, saves and games stay." {
		t.Fatal(got)
	}
}

// A new card gets the release's recommended image, whatever the card held before.
func TestFreshInstallTakesTheRecommendedImage(t *testing.T) {
	s := updateScreen(t, "4.19", "gles", fourImages("r"))
	s.setTask(taskNew)
	s.asset = &s.assets[0]
	s.ready()
	if !strings.Contains(s.asset.Name, "k510-vulkan") || s.variantBox.Visible() {
		t.Fatal("a new card:", s.asset.Name)
	}
}

func TestSplitForTheCardsKind(t *testing.T) {
	list := append(fourImages("new"), fourImages("partial", "k510-vulkan")...)
	list = append(list, catalog.Asset{Name: "andr36oid-20261016-2212-rk3326.img.xz", Release: "old"})
	first, others := catalog.SplitFor(list, "4.19", "gles")
	if len(first) != 3 || !strings.Contains(first[0].Name, "k419-gles") || first[1].Release != "partial" || first[2].Release != "old" || len(others) != 3 {
		t.Fatal(first, others)
	}
	if a, ok := catalog.Matching(list, "partial", "4.19", "gles"); ok {
		t.Fatal("partial has no 4.19 image", a)
	}
}
