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
	return encode(struct {
		Card  flash.Card         `json:"card"`
		Build string             `json:"build_identity"`
		Setup firmware.CardSetup `json:"setup"`
	}{card, build, setup})
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
	if req.Bootloader.Choice == "image" {
		image = s.image
	}
	data, out, e := firmware.ResolveBootloader(ctx, req.Bootloader, image, s.dir)
	if e != nil {
		return "", e
	}
	if data == nil {
		return encode(out)
	}
	if e = ctx.Err(); e != nil {
		return "", errors.New(Stopped)
	}
	if e = firmware.ApplyBootloader(dev, dev.size, data, *out.Record, build, s.dir); e != nil {
		return "", e
	}
	return encode(out)
}
