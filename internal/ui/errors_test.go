package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// The error dialog leads with what happened and what the failure left the card like,
// keeps the error as the technical detail, and its Copy details button puts the
// report on the clipboard.
func TestErrorDialogExplainsAndCopies(t *testing.T) {
	s := fixScreen(t)
	e := fmt.Errorf("%w; %w", errors.New("write /dev/sdb: input/output error"), flash.StateIncomplete.New())
	got := s.explain(e)
	if got != i18n.CauseDeviceGone.Key()+"\n\n"+flash.StateIncomplete.Key() {
		t.Fatal(got)
	}
	if s.explain(errors.New("something only a developer knows")) != "Check the selected image and card, then try again." {
		t.Fatal("unknown errors lost the general advice")
	}
	s.showError(e)
	b := s.copyDetails(e)
	b.OnTapped()
	clip := fyne.CurrentApp().Clipboard().Content()
	if !strings.Contains(clip, "andr36oid SD Flasher "+Version) || !strings.Contains(clip, "input/output error") || !strings.Contains(clip, i18n.CauseDeviceGone.Key()) {
		t.Fatalf("%q", clip)
	}
	if b.Text != "Copied" {
		t.Fatal(b.Text)
	}
}
