package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

func (s *Session) BootloaderOffers() (string, error) {
	offers, e := firmware.FetchBootloaderChoices(context.Background())
	if e != nil {
		return "", e
	}
	return encode(offers)
}
func (s *Session) BootloaderInfo(d Disk) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	card, e := flash.InspectCard(dev, dev.size)
	if e != nil {
		return "", e
	}
	build, tab, e := firmware.CardBuildIdentity(dev, dev.size)
	if e != nil {
		return "", e
	}
	boot, e := firmware.OpenBoot(dev, tab, true)
	if e != nil {
		return "", e
	}
	setup, e := firmware.ReadCardSetup(boot)
	if e != nil {
		return "", e
	}
	target, e := firmware.CardBootTarget(dev, dev.size, s.image)
	if e != nil {
		return "", e
	}
	return encode(struct {
		BootTarget firmware.BootTarget `json:"boot_target"`
		Card       flash.Card          `json:"card"`
		Build      string              `json:"build_identity"`
		Setup      firmware.CardSetup  `json:"setup"`
	}{target, card, build, setup})
}
func (s *Session) ApplyBootloader(d Disk, input string, observer Observer) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	var req struct {
		Identity    string                    `json:"identity"`
		Fingerprint string                    `json:"fingerprint"`
		Build       string                    `json:"build"`
		Bootloader  firmware.BootloaderChoice `json:"bootloader"`
	}
	if e = json.Unmarshal([]byte(input), &req); e != nil {
		return "", e
	}
	if req.Identity == "" || req.Identity != d.Identity() {
		return "", errors.New("the selected card reader changed")
	}
	card, e := flash.InspectCard(dev, dev.size)
	if e != nil {
		return "", e
	}
	if card.DataCard {
		return "", errors.New(firmware.BootloaderOnlySD)
	}
	if req.Fingerprint == "" || req.Fingerprint != card.Fingerprint {
		return "", errors.New("card changed after inspection; select it again")
	}
	build, _, e := firmware.CardBuildIdentity(dev, dev.size)
	if e != nil {
		return "", e
	}
	if req.Build != build {
		return "", errors.New("the card changed; choose it again")
	}
	ctx, done := s.writeContext()
	defer done()
	emit(observer, flash.Progress{Phase: "Bootloader"})
	var image *firmware.Image
	if s.image != nil {
		image = s.image
	}
	target, e := firmware.CardBootTarget(dev, dev.size, image)
	if e != nil {
		return "", e
	}
	data, out, e := firmware.ResolveBootOptions(ctx, req.Bootloader, image, target, s.dir)
	if e != nil {
		return "", e
	}
	if data == nil {
		return encode(out)
	}
	if e = ctx.Err(); e != nil {
		return "", errors.New(Stopped)
	}
	if e = firmware.ApplyResolvedBootOptions(dev, dev.size, data, out, build, s.dir); e != nil {
		return "", e
	}
	return encode(out)
}

// BootOptionsOffers is separate from the legacy method to preserve old callers.
func (s *Session) BootOptionsOffers() (string, error) {
	o, e := firmware.FetchBootOffers(context.Background())
	if e != nil && o == nil {
		return "", e
	}
	return encode(o)
}

// Evaluation uses exactly the same compatibility table as the privileged writer.
// Callers send choices and the selected card/image context; no disk is written.
func (s *Session) CheckBootOptions(input string) (string, error) {
	var req struct {
		Choice  firmware.BootloaderChoice `json:"choice"`
		Target  firmware.BootTarget       `json:"target"`
		Changed string                    `json:"changed"`
		Rules   []firmware.BootRule       `json:"rules"`
	}
	if e := json.Unmarshal([]byte(input), &req); e != nil {
		return "", e
	}
	if len(req.Rules) == 0 {
		req.Rules = firmware.BootRules()
	}
	notes := []firmware.BootIssue{}
	if req.Changed != "" {
		req.Choice, notes = firmware.ResetBootOptions(req.Choice, req.Changed, req.Target, req.Rules)
	}
	issues := firmware.EvaluateBootOptions(req.Choice, req.Target, req.Rules)
	return encode(struct {
		Choice  firmware.BootloaderChoice `json:"choice"`
		Issues  []firmware.BootIssue      `json:"issues"`
		Notes   []firmware.BootIssue      `json:"notes"`
		Summary string                    `json:"summary"`
	}{req.Choice, issues, notes, firmware.BootOptionsSummary(req.Choice, issues)})
}
