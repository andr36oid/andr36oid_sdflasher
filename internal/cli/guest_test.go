package cli

import (
	"bufio"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
)

func guestTestOptions() []guestOption {
	var out []guestOption
	for _, src := range catalog.GuestSources {
		g := &catalog.GuestRelease{Darkosen: "11072026", Source: src, Distribution: src.ID}
		out = append(out, guestOption{src: src, g: g})
	}
	return out
}

// -y without -game-system puts no system on the card: scripts written before the
// choice existed must not start a download of several GB. -game-system still picks
// one with -y, and the prompt still offers dArkOS first.
func TestGuestChoiceWithYes(t *testing.T) {
	options := guestTestOptions()
	in := bufio.NewReader(strings.NewReader(""))
	if g, e := chooseGuest(in, options, "", true); e != nil || g != nil {
		t.Fatal("-y alone chose", g, e)
	}
	if g, e := chooseGuest(in, options, "dArkOSRE", true); e != nil || g == nil || g.Source.ID != "dArkOSRE" {
		t.Fatal("-y -game-system dArkOSRE chose", g, e)
	}
	if g, e := chooseGuest(in, options, "none", true); e != nil || g != nil {
		t.Fatal("-game-system none chose", g, e)
	}
	// interactive: Enter takes the preselection, dArkOS
	g, e := chooseGuest(bufio.NewReader(strings.NewReader("\n")), options, "", false)
	if e != nil || g == nil || g.Source.ID != catalog.DefaultGuest {
		t.Fatal("interactive default", g, e)
	}
	// interactive: dArkOS can't go on the card, the preselection moves on
	options[0].reason = "(needs a newer andr36oid release than this one)"
	g, e = chooseGuest(bufio.NewReader(strings.NewReader("\n")), options, "", false)
	if e != nil || g == nil || g.Source.ID != "dArkOSRE" {
		t.Fatal("interactive default past an unavailable one", g, e)
	}
}
