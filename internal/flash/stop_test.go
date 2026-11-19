package flash

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// stopRecorder notes the writes and whether one came after the stop.
type stopRecorder struct {
	Device
	mu      sync.Mutex
	stopped bool
	after   int
	writes  int
}

func (r *stopRecorder) WriteAt(b []byte, off int64) (int, error) {
	r.mu.Lock()
	r.writes++
	if r.stopped {
		r.after++
	}
	r.mu.Unlock()
	return r.Device.WriteAt(b, off)
}

// A write stopped by the user (its context cancelled, as the helper does on the
// app's stop request) ends after the block in progress: nothing more is written,
// and the error says the card is incomplete. Also when it is stopped in the last
// check of the whole card.
func TestStopEndsAfterTheBlockInProgress(t *testing.T) {
	for _, at := range []string{"writing", "Checking the whole card once more"} {
		t.Run(at, func(t *testing.T) {
			im, boot, _, card, _, p := harnessPlan(t)
			dev := &stopRecorder{Device: newFake(card)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := Execute(ctx, dev, im, p, boot, t.TempDir(), func(pr Progress) {
				if pr.Phase == at && pr.Done > 0 || pr.Name == at {
					dev.mu.Lock()
					dev.stopped = true
					dev.mu.Unlock()
					cancel()
				}
			})
			if !errors.Is(e, context.Canceled) || !StateIncomplete.Is(e) {
				t.Fatal(e)
			}
			if !dev.stopped || dev.after != 0 || dev.writes == 0 {
				t.Fatalf("stopped %v, %d of %d writes after the stop", dev.stopped, dev.after, dev.writes)
			}
		})
	}
}
