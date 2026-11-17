package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

func (s *Session) DTBInfo(d Disk) (string, error) {
	v, e := s.BootloaderInfo(d)
	if e != nil {
		return "", errors.New(firmware.DTBPackNoBuild)
	}
	return v, nil
}

type dtbRequest struct {
	Identity    string              `json:"identity"`
	Fingerprint string              `json:"fingerprint"`
	Build       string              `json:"build"`
	File        string              `json:"file"`
	Manifest    string              `json:"manifest"`
	Online      bool                `json:"online"`
	Restore     bool                `json:"restore"`
	Options     firmware.DTBOptions `json:"options"`
}

func (s *Session) dtbOperation(d Disk, input string, observer Observer, apply bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dev, e := adapt(d)
	if e != nil {
		return "", e
	}
	var req dtbRequest
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
	build, tab, e := firmware.CardBuildIdentity(dev, dev.size)
	if e != nil {
		return "", e
	}
	if req.Fingerprint == "" || req.Fingerprint != card.Fingerprint || req.Build != build {
		return "", errors.New("the card changed; choose it again")
	}
	if req.Restore {
		if !apply {
			plan, e := firmware.PlanRestoreDTB(dev, dev.size, build, s.dir)
			if e != nil {
				return "", e
			}
			return encode(struct {
				Plan *firmware.DTBPlan `json:"plan"`
			}{plan})
		}
		result, e := firmware.RestoreDTBSetup(dev, dev.size, build, s.dir, req.Options)
		if e != nil {
			return "", e
		}
		return encode(result)
	}
	var p *firmware.DTBPack
	if req.Online {
		boot, e := firmware.OpenBoot(dev, tab, true)
		if e != nil {
			return "", e
		}
		setup, e := firmware.ReadCardSetup(boot)
		if e != nil {
			return "", e
		}
		p, e = firmware.FetchDTBPack(context.Background(), setup, s.dir)
		if e != nil {
			return "", e
		}
	} else {
		p, e = firmware.ReadDTBPackReference(req.File, req.Manifest)
		if e != nil {
			return "", e
		}
	}
	if !apply {
		plan, e := firmware.PlanDTBPack(dev, dev.size, p, build, s.dir, req.Options)
		if e != nil {
			return "", e
		}
		return encode(struct {
			Pack *firmware.DTBPack `json:"pack"`
			Plan *firmware.DTBPlan `json:"plan"`
		}{p, plan})
	}
	emit(observer, flash.Progress{Phase: "Writing device trees..."})
	req.Options.Progress = func(done, total int64) {
		emit(observer, flash.Progress{Phase: "Writing device trees...", Done: done, Total: total})
	}
	result, e := firmware.ApplyDTBPack(dev, dev.size, p, build, s.dir, req.Options)
	if e != nil {
		return "", e
	}
	return encode(result)
}
func (s *Session) DTBPlan(d Disk, input string) (string, error) {
	return s.dtbOperation(d, input, nil, false)
}
func (s *Session) ApplyDTBPack(d Disk, input string, observer Observer) (string, error) {
	return s.dtbOperation(d, input, observer, true)
}

// imageDTBSetup and fetchDTBPack are firmware's (the tests replace them).
var (
	imageDTBSetup = firmware.ImageDTBSetup
	fetchDTBPack  = firmware.FetchDTBPack
)

// Select an offline set, or look up the current image's release when file is empty.
func (s *Session) ImageDTBs(file string) (string, error) {
	s.mu.Lock()
	im := s.image
	s.mu.Unlock()
	if im == nil {
		return "", errors.New("choose an image first")
	}
	var p *firmware.DTBPack
	var e error
	if file == "" {
		setup, err := imageDTBSetup(im)
		if err != nil {
			return "", err
		}
		p, e = fetchDTBPack(context.Background(), setup, s.dir)
	} else {
		p, e = firmware.ReadDTBPack(file)
	}
	if e != nil {
		return "", e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image != im {
		return "", errors.New("image selection changed")
	}
	updated, e := firmware.ImageWithDTBPack(im, p, s.dir)
	if e != nil {
		return "", e
	}
	s.image = updated
	return encode(updated)
}

func (s *Session) FindImageDTBs() (string, error) {
	s.mu.Lock()
	im := s.image
	s.mu.Unlock()
	if im == nil {
		return "", errors.New("choose an image first")
	}
	setup, e := firmware.ImageDTBSetup(im)
	if e != nil {
		return "", e
	}
	p, e := firmware.FetchDTBPack(context.Background(), setup, s.dir)
	if e != nil {
		return "", e
	}
	return encode(p)
}
func (s *Session) ImageDTBReference(file, reference string) (string, error) {
	p, e := firmware.ReadDTBPackReference(file, reference)
	if e != nil {
		return "", e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.image == nil {
		return "", errors.New("choose an image first")
	}
	im, e := firmware.ImageWithDTBPack(s.image, p, s.dir)
	if e != nil {
		return "", e
	}
	s.image = im
	return encode(im)
}

func DTBScreenWarning() string { return firmware.DTBScreenWarning }
func DTBScreenSuccess() string { return firmware.DTBScreenSuccess }
