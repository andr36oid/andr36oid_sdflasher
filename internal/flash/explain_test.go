package flash

import (
	"context"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// What people read after each way a card can fail: a message that says what happened
// (never only the bare system error), then what the failure left the card like, and
// the hints for the card or the reader.
func TestHarnessFailuresAreExplained(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*fakeCard, *Plan)
		want  *i18n.Format
		state *i18n.Format
	}{
		{"io error mid image", func(c *fakeCard, p *Plan) { c.failAt = p.Writes[0].Offset + 1<<20 }, i18n.CauseDeviceGone, StateIncomplete},
		// these fail in the card test, before anything of the release is written
		{"short writes", func(c *fakeCard, p *Plan) { c.short = true }, i18n.CauseDeviceGone, StateCardTest},
		{"reader vanishes", func(c *fakeCard, p *Plan) { c.vanishAfter = 6 }, i18n.CauseDeviceGone, StateCardTest},
		{"write-protected", func(c *fakeCard, p *Plan) { c.readOnly = true }, ErrWriteProtected, nil},
		{"unreadable before writing", func(c *fakeCard, p *Plan) { c.failReadAt = p.Writes[len(p.Writes)-3].Offset }, i18n.CauseDeviceGone, StateUntouched},
		{"fake card that drops writes", func(c *fakeCard, p *Plan) { c.drop = 8 << 30 }, ErrFakeCapacity, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			im, boot, _, card, _, p := harnessPlan(t)
			dev := newFake(card)
			c.setup(dev, p)
			e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
			if e == nil {
				t.Fatal("no error")
			}
			key, _, ok := i18n.Find(e)
			if !ok || key != c.want.Key() {
				t.Fatalf("%v\nexplained as %q", e, key)
			}
			got := i18n.ExplainError("en", e)
			if c.state != nil && !strings.HasSuffix(got, "\n\n"+c.state.Key()) {
				t.Fatalf("no state after the message: %q", got)
			}
			if c.state != StateIncomplete && StateIncomplete.Is(e) {
				t.Fatalf("says incomplete although nothing was written: %v", e)
			}
			if len(FailureHints(Classify(e), false)) == 0 {
				t.Fatal("no hints")
			}
		})
	}
}
