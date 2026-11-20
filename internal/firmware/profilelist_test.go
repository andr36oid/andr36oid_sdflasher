package firmware

import (
	"os"
	"testing"
)

// The "hidden" flag of andr36oid-profiles.txt (a copy at a board's old folder) is
// read, and such a profile is not offered; a card set up with it is shown as the
// offered profile with the same device tree.
func TestProfileListHidden(t *testing.T) {
	b, e := os.ReadFile("testdata/andr36oid-profiles.txt")
	if e != nil {
		t.Fatal(e)
	}
	byID := map[string]Profile{}
	for _, p := range ProfileListEntries(b) {
		byID[p.ID] = p
	}
	old, now := byID["Experimental/EE-G80-Panel8"], byID["Devices/EE-G80-Panel8"]
	if !old.Hidden || now.Hidden || old.Experimental || now.Experimental || now.Console != "G80" {
		t.Fatalf("%+v %+v", old, now)
	}
	if p := byID["Experimental/R36S-V22"]; !p.Experimental || p.Hidden {
		t.Fatalf("%+v", p)
	}
	old.Hash, now.Hash = "same", "same"
	list := []Profile{byID["Panels/Panel4"], old, now}
	if got := Offered(list); len(got) != 2 || got[1].ID != now.ID {
		t.Fatal(got)
	}
	if ListedAs(list, old.ID) != now.ID || ListedAs(list, "Panels/Panel4") != "Panels/Panel4" || ListedAs(list, "Devices/Gone") != "" {
		t.Fatal("listed as")
	}
}
