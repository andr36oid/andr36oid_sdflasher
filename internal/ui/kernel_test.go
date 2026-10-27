package ui

import (
	"encoding/json"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

func TestKernelOfferRequiresCompatibleReadableBuild(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &screen{locale: "en"}
	if s.kernelOfferDetails(nil) != nil {
		t.Fatal("offered absent update")
	}
	offer := &catalog.KernelOffer{KernelRelease: catalog.KernelRelease{Version: "v1.1.0", Title: "Btrfs", Description: "Adds\nbtrfs", Size: 1 << 20, CompatibleBuilds: []string{"release|incremental"}}}
	if s.kernelOfferDetails(offer) != nil {
		t.Fatal("offered update without build")
	}
	offer.Current = firmware.KernelIdentity{Build: "wrong|build"}
	if s.kernelOfferDetails(offer) != nil {
		t.Fatal("offered incompatible update")
	}
	offer.Current.Build = "release|incremental"
	details := s.kernelOfferDetails(offer)
	if details == nil {
		t.Fatal("compatible offer missing")
	}
	if details.Objects[0].(*widget.Label).Text != "Kernel update v1.1.0" || details.Objects[2].(*widget.Label).Text != "Adds btrfs" {
		t.Fatal("missing version or description")
	}
}
func TestKernelTranslationKeys(t *testing.T) {
	raw, err := i18n.Files.ReadFile("locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	translations := map[string]string{}
	if err = json.Unmarshal(raw, &translations); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"Check for kernel update", "Choose an existing andr36oid card to check its kernel.",
		"Checking for kernel update...", "Skip kernel update", "Apply kernel update", "Kernel update %s",
		"The kernel files in BOOT will be updated. Apps, settings, saves, and games stay in place. Keep the card connected until verification finishes.",
		"Could not check for kernel updates.", "No kernel update is available for this card.", "A kernel update is available for this card.",
		"Kernel update could not finish.", "Kernel update installed and verified.", "Downloading kernel update", "Writing and verifying kernel update",
		catalog.KernelNewFormat, firmware.KernelNoBuild, firmware.KernelChanged,
	} {
		if translations[key] == "" {
			t.Errorf("missing English translation %q", key)
		}
	}
}
