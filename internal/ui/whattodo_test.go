package ui

import (
	"errors"
	"fmt"
	"testing"

	"fyne.io/fyne/v2"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// A failed write says what to do for its kind of failure, not the general line.
func TestWhatToDoPerFailure(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	general := s.t("Check the selected image and card, then try again.")
	seen := map[string]string{}
	for name, e := range map[string]error{
		"reader":      errors.New("write /dev/mmcblk0: input/output error"),
		"protected":   flash.ErrWriteProtected.New(),
		"too small":   errors.New("the card is too small"),
		"damaged":     errors.New("xz: unexpected end of input; the image file is damaged"),
		"rights":      i18n.Detailed(i18n.CauseHelperStopped.New(), errors.New("administrator access was denied")),
		"in use":      errors.New("open /dev/sdb: device or resource busy"),
		"counterfeit": flash.ErrFakeCapacity.New("32 GB", "8 GB"),
		"verify":      fmt.Errorf("read-back mismatch at 1234"),
	} {
		got := s.whatToDo(e)
		if got == general || got == "" {
			t.Errorf("%s: %q", name, got)
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%s and %s say the same: %q", name, other, got)
		}
		seen[got] = name
	}
}
