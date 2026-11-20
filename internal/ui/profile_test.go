package ui

import (
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

// No console is chosen for the user on a new card: Next waits for a pick. An update
// takes the profile the card was set up with; a new installation on such a card
// only marks it as "last time".
func TestAutomaticProfileSelection(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	for _, tc := range []struct {
		name, selected, detected, want, tagged string
		picked, updating                       bool
	}{
		{"new card", "", "", "", "", false, false},
		{"manual choice", "Panels/Panel3", "", "Panels/Panel3", "", true, false},
		{"update of an installed card", "", "Panels/Panel3", "Panels/Panel3", "", false, true},
		{"new install on an installed card", "", "Panels/Panel3", "", "R36S - Panel 3", false, false},
		{"unsupported installed panel", "Panels/Panel4", "Devices/Other", "", "", false, true},
		{"hidden copy at the old folder", "", "Experimental/EE-G80-Panel8", "Devices/EE-G80-Panel8", "", false, true},
		{"hidden copy, new install", "", "Experimental/EE-G80-Panel8", "", "G80C - Panel 8", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := a.NewWindow("test")
			defer w.Close()
			profiles := []firmware.Profile{
				{ID: "Panels/Panel3", Name: "R36S — Panel 3", Console: "R36S", Hash: "p3"},
				{ID: "Panels/Panel4", Name: "R36S — Panel 4", Console: "R36S", Hash: "p4"},
				{ID: "Devices/EE-G80-Panel8", Name: "G80C — Panel 8", Console: "G80", Hash: "g80"},
				{ID: "Experimental/EE-G80-Panel8", Name: "G80C — Panel 8 (older flashers)", Console: "G80", Hash: "g80", Hidden: true},
			}
			s := &screen{win: w, locale: "en", profile: tc.selected, profilePicked: tc.picked, updating: tc.updating, im: &firmware.Image{Profiles: profiles}, card: &flash.Card{Installed: tc.detected != "", Treble: true, Profile: tc.detected}}
			if tc.updating {
				s.task = taskUpdate
			}
			s.build()
			s.autoProfile()
			if s.profile != tc.want {
				t.Fatalf("selected %q, want %q", s.profile, tc.want)
			}
			if tc.want == "" && s.stepDone(stepConsole) {
				t.Fatal("the console step is done without a pick")
			}
			tagged := ""
			for _, o := range s.consoleList.Objects {
				if grid, ok := o.(*fyne.Container); ok {
					for _, item := range grid.Objects {
						c := item.(*choiceCard)
						if c.selected != (c.title.Text == shown(s.profileName()) && tc.want != "") {
							t.Fatalf("%q shown as selected: %v", c.title.Text, c.selected)
						}
						if c.tag.Text != "" {
							if c.tag.Text != "last time" || tagged != "" {
								t.Fatalf("tag %q on %q", c.tag.Text, c.title.Text)
							}
							tagged = c.title.Text
						}
						if c.title.Text == "G80C - Panel 8 (older flashers)" {
							t.Fatal("hidden profile listed")
						}
					}
				}
			}
			if tagged != tc.tagged {
				t.Fatalf("tagged %q, want %q", tagged, tc.tagged)
			}
		})
	}
}

// The console list of the real profile list of the device tree: no hidden profile
// is offered, and each listed profile is in the group its flags put it in (tested
// unless "experimental", whatever its folder).
func TestConsoleListOfTheRealProfileList(t *testing.T) {
	// a copy of the device tree's BOOT/andr36oid-profiles.txt
	b, e := os.ReadFile("../firmware/testdata/andr36oid-profiles.txt")
	if e != nil {
		t.Fatal(e)
	}
	// what the file says, read here on its own
	want := map[string]string{}
	hidden := 0
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if strings.HasPrefix(line, "#") || len(f) < 4 {
			continue
		}
		switch {
		case strings.Contains(f[3], "hidden"):
			hidden++
		case strings.Contains(f[3], "experimental"):
			want[f[0]] = "experimental"
		default:
			want[f[0]] = "tested"
		}
	}
	if hidden == 0 || len(want) < 50 {
		t.Fatal("fixture", hidden, len(want))
	}
	// what the console page lists, from the profiles as the image reader reads them
	tested, experimental := consoleGroups(firmware.ProfileListEntries(b), "")
	got := map[string]string{}
	for _, p := range tested {
		got[p.ID] = "tested"
	}
	for _, p := range experimental {
		got[p.ID] = "experimental"
	}
	if _, ok := got["Experimental/EE-G80-Panel8"]; ok {
		t.Fatal("hidden profile listed")
	}
	if len(got) != len(want) {
		t.Fatalf("%d listed, want %d", len(got), len(want))
	}
	for id, g := range want {
		if got[id] != g {
			t.Fatalf("%s listed as %q, want %q", id, got[id], g)
		}
	}
	// the G80 is offered once, at its new folder
	g80 := 0
	for _, p := range tested {
		if p.Console == "G80" {
			g80++
		}
	}
	if g80 != 1 {
		t.Fatal("G80 entries under Tested:", g80)
	}
}
