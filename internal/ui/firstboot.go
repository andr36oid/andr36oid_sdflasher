package ui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// After a new card is written the flasher stays on this screen: hotfixes go on the card
// only after the console has finished its first setup on it, so the user takes the card
// to the console and brings it back. Nothing here can be forced.

const firstBootSteps = "1. Eject the card and put it in the console's system card slot.\n" +
	"2. Let the console start and follow its setup to the end. The first start takes a few minutes.\n" +
	"3. When the setup offers to add games, or once you see the home screen, shut the console down and put the card back in this computer.\n" +
	"This window can stay open. It notices the card and checks for fixes for your build."

func sameCard(a, b platform.Drive) bool {
	if a.Serial != "" && b.Serial != "" {
		return a.Serial == b.Serial && a.Size == b.Size
	}
	return a.Size == b.Size && a.Name == b.Name
}

// awaitFirstBoot waits for the card to leave and come back, then looks at it. ready is
// called once, with the drive as it is now, when its first setup is finished.
func (s *screen) awaitFirstBoot(ctx context.Context, card platform.Drive, expected string, status *widget.Label, ready func(platform.Drive)) {
	say := func(text string) {
		fyne.Do(func() {
			status.SetText(text)
			status.Show()
		})
	}
	go func() {
		say(s.t("Waiting for the card to come back from the console..."))
		present, looked := true, true
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			drives, e := platform.List()
			if e != nil {
				continue
			}
			var found *platform.Drive
			for i := range drives {
				if sameCard(drives[i], card) {
					found = &drives[i]
				}
			}
			if found == nil {
				present, looked = false, false
				continue
			}
			if present && looked {
				continue
			}
			present, looked = true, true
			result, e := jobs.Run(jobs.Request{Action: "hotfix-list", Drive: *found}, nil)
			if ctx.Err() != nil {
				return
			}
			switch {
			case e != nil || result.BuildIdentity != expected:
				say(s.t("This is not the card that was just written. Waiting for that card..."))
			case result.FirstBoot != nil && result.FirstBoot.Done:
				d := *found
				fyne.Do(func() {
					status.SetText(s.t("The console has finished its setup on this card."))
					ready(d)
				})
				return
			case result.FirstBoot != nil && result.FirstBoot.Started:
				say(s.t("The console has started from this card but has not finished its setup. Put the card back in the console, finish the setup, then bring it back."))
			default:
				say(s.t("The console has not started from this card yet. Put it in the console and finish the setup, then bring it back."))
			}
		}
	}()
}
