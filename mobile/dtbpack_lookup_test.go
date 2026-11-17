package mobile

import (
	"context"
	"errors"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

// A failed lookup of the release's device trees is an error, not a crash on the set
// that never came.
func TestImageDTBsLookupFails(t *testing.T) {
	s, e := NewSession(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	s.image = &firmware.Image{Path: "x"}
	oldSetup, oldFetch := imageDTBSetup, fetchDTBPack
	t.Cleanup(func() { imageDTBSetup, fetchDTBPack = oldSetup, oldFetch })
	imageDTBSetup = func(*firmware.Image) (firmware.CardSetup, error) { return firmware.CardSetup{}, nil }
	fetchDTBPack = func(context.Context, firmware.CardSetup, string) (*firmware.DTBPack, error) {
		return nil, errors.New("GitHub returned 502")
	}
	if _, e = s.ImageDTBs(""); e == nil || e.Error() != "GitHub returned 502" {
		t.Fatal(e)
	}
}
