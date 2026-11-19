package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
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

// Text mode prints what failed, what it means and what to do, the error itself as the
// detail, and the hints of a card failure; an error without a message stays as it is.
func TestPrintFailureExplains(t *testing.T) {
	capture := func(f func()) string {
		r, w, _ := os.Pipe()
		old := os.Stderr
		os.Stderr = w
		f()
		os.Stderr = old
		w.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
	e := flash.DeviceFailure(fmt.Errorf("%w; %w", errors.New("write /dev/sdb: input/output error"), flash.StateIncomplete.New()))
	out := capture(func() { printFailure("The write failed.", e) })
	for _, want := range []string{"The write failed.\n", i18n.CauseDeviceGone.Key(), flash.StateIncomplete.Key(), "Details: write /dev/sdb: input/output error", "Try a different card reader."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if out := capture(func() { printFailure("", errors.New("No device profile chosen.")) }); out != "No device profile chosen.\n" {
		t.Fatalf("%q", out)
	}
}
