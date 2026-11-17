package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

// The game system choice of a new card, as in the window: dArkOS (preselected when it
// can go on the card), dArkOSRE, dArkOSen or none. -game-system picks one without
// asking; -y without -game-system puts none on the card, so that scripts written
// before the choice existed don't start a download of several GB.

// guestOption is one source as it stands for this release and card.
type guestOption struct {
	src    catalog.GuestSource
	g      *catalog.GuestRelease
	reason string // why it can't be chosen, "" when it can
}

func (o guestOption) label() string {
	if o.reason != "" {
		return fmt.Sprintf("%-9s %s", o.src.Display, o.reason)
	}
	return fmt.Sprintf("%-9s %s, %s download", o.src.Display, o.g.Darkosen, humanSize(o.g.DownloadBytes()))
}

// lookupGuests finds the latest release of every source and says which can go on a
// card of size bytes with im and the storage choices.
func lookupGuests(im *firmware.Image, size int64, userdataGiB int, noROMs bool) []guestOption {
	out := make([]guestOption, len(catalog.GuestSources))
	done := make(chan struct{})
	for i, src := range catalog.GuestSources {
		go func() {
			defer func() { done <- struct{}{} }()
			g, e := catalog.LatestGuest(context.Background(), src)
			out[i] = guestOption{src: src, g: g, reason: guestReason(im, size, userdataGiB, noROMs, g, e)}
		}()
	}
	for range catalog.GuestSources {
		<-done
	}
	return out
}

func guestReason(im *firmware.Image, size int64, userdataGiB int, noROMs bool, g *catalog.GuestRelease, lookup error) string {
	if lookup != nil || g == nil {
		return "(not available right now)"
	}
	if e := flash.GuestFits(im, &flash.Guest{Distribution: g.Distribution, Minimum: g.Minimum}); e != nil {
		if errors.Is(e, flash.ErrGuestNeedsNewerRelease) {
			return "(needs a newer andr36oid release than this one)"
		}
		return "(not available right now)"
	}
	if _, _, _, e := flash.PreparedSizes(im, size, userdataGiB, noROMs, g.PartitionBytes); e != nil {
		return "(this card is too small for Android and " + g.Source.Display + " together)"
	}
	return ""
}

// defaultGuest is the index of the preselected option: the first that can go on the
// card (dArkOS when it can), len(options) for none.
func defaultGuest(options []guestOption) int {
	for i, o := range options {
		if o.reason == "" {
			return i
		}
	}
	return len(options)
}

// chooseGuest returns the system to put on the card, nil for none. want is the
// -game-system value ("" when not given).
func chooseGuest(in *bufio.Reader, options []guestOption, want string, assumeYes bool) (*catalog.GuestRelease, error) {
	if want != "" {
		id, ok := catalog.ParseGuestChoice(want)
		if !ok {
			return nil, fmt.Errorf("unknown game system %q; use dArkOS, dArkOSRE, dArkOSen or none", want)
		}
		if id == catalog.NoGuest {
			return nil, nil
		}
		for _, o := range options {
			if o.src.ID == id {
				if o.reason != "" {
					return nil, fmt.Errorf("%s can't go on this card %s", o.src.Display, o.reason)
				}
				return o.g, nil
			}
		}
		return nil, fmt.Errorf("unknown game system %q", want)
	}
	def := defaultGuest(options)
	fmt.Println("Game system: one system can go on the card now; more can be added later on the console.")
	for i, o := range options {
		fmt.Printf("  [%d] %s\n", i+1, o.label())
	}
	fmt.Printf("  [%d] None\n", len(options)+1)
	if assumeYes {
		// -y alone: none, never a surprise download; -game-system chooses one
		fmt.Println("No system is put on the card (-y without -game-system).")
		return nil, nil
	}
	for {
		line := strings.TrimSpace(prompt(in, fmt.Sprintf("Choose [1-%d] (default %d): ", len(options)+1, def+1)))
		n := def + 1
		if line != "" {
			v, e := strconv.Atoi(line)
			if e != nil || v < 1 || v > len(options)+1 {
				fmt.Println("Please enter a number from the list.")
				continue
			}
			n = v
		}
		if n == len(options)+1 {
			return nil, nil
		}
		if o := options[n-1]; o.reason != "" {
			fmt.Println(o.src.Display, "can't go on this card", o.reason+". Choose another one, or none.")
			continue
		}
		return options[n-1].g, nil
	}
}

// downloadGuest fetches and unpacks g into the cache for the writer.
func downloadGuest(g *catalog.GuestRelease, cache string) (*flash.Guest, error) {
	fmt.Printf("Downloading %s %s (%s)...\n", g.Source.Display, g.Darkosen, humanSize(g.DownloadBytes()))
	dl, unpack := barProgress("download"), barProgress("unpack")
	path, e := catalog.DownloadGuest(context.Background(), g, cache, func(phase string, done, total int64) {
		if phase == "unpack" {
			unpack(done, total)
		} else {
			dl(done, total)
		}
	})
	fmt.Println()
	if e != nil {
		return nil, e
	}
	return &flash.Guest{Path: path, Distribution: g.Distribution, Darkosen: g.Darkosen, Revision: g.Revision, Minimum: g.Minimum,
		Name: g.Image.Name, Size: g.Image.Size, SHA256: g.Image.SHA256, PartitionBytes: g.PartitionBytes}, nil
}

// failureHints are the lines printed after a failed write's error (flash.FailureHints).
func failureHints(e error) []string {
	return flash.FailureHints(flash.Classify(e), false)
}
