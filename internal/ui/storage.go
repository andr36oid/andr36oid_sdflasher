package ui

import (
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

// The storage box on the options page: how much of a new card Android gets. 0 is the
// recommended size the first-boot resizer picks (half of the free space, 8 to 64 GiB).
func (s *screen) buildStorage() {
	s.sizeLabel = widget.NewLabel("")
	s.sizeSlider = widget.NewSlider(flash.MinUserdataGiB, flash.MinUserdataGiB+1)
	s.sizeSlider.Step = 1
	s.sizeSlider.OnChanged = func(v float64) {
		if s.sizeSetting {
			return
		}
		s.userdataGiB = int(v)
		s.ready()
	}
	s.sizeAuto = widget.NewButton(s.t("Recommended size"), func() {
		s.userdataGiB = 0
		s.ready()
	})
	s.storage = container.NewVBox(s.sizeLabel, s.sizeSlider, container.NewHBox(s.sizeAuto))
}

// storageText says how the card will be split, or "" when there is nothing to choose.
func (s *screen) storageText() string {
	if s.drive == nil || s.updating {
		return ""
	}
	if s.noROMs.Checked {
		return s.t("Use all available storage for Android")
	}
	android, games := s.split(s.userdataGiB)
	if games == 0 {
		return s.t("Use all available storage for Android")
	}
	return s.t("Android %d GiB, games %d GiB", flash.RoundGiB(android), flash.RoundGiB(games))
}

func (s *screen) refreshStorage() {
	if s.sizeSlider == nil {
		return
	}
	max := 0
	if s.drive != nil {
		max = flash.MaxUserdataGiB(s.drive.Size)
		if s.im != nil && s.im.Prepared() {
			max = flash.MaxPreparedGiB(s.im, s.drive.Size, s.guestBytes())
		}
	}
	if s.userdataGiB > max {
		s.userdataGiB = 0
	}
	s.sizeLabel.SetText(s.storageText())
	if s.drive == nil || s.updating || s.noROMs.Checked || max <= flash.MinUserdataGiB {
		s.sizeSlider.Hide()
		s.sizeAuto.Hide()
		return
	}
	value := s.userdataGiB
	if value == 0 {
		android, _ := s.split(0)
		value = flash.RoundGiB(android)
	}
	s.sizeSetting = true
	s.sizeSlider.Max = float64(max)
	s.sizeSlider.SetValue(float64(value))
	s.sizeSetting = false
	s.sizeSlider.Show()
	s.sizeAuto.Show()
	if s.userdataGiB == 0 {
		s.sizeAuto.Disable()
	} else {
		s.sizeAuto.Enable()
	}
}

// split is what Android and the games partition get with wantGiB for Android (0: the
// recommended size): by the flasher's own layout for releases it partitions the card
// for, else by the console's first-boot rule.
func (s *screen) split(wantGiB int) (android, games int64) {
	if s.im != nil {
		if a, g, prepared, e := flash.PreparedSizes(s.im, s.drive.Size, wantGiB, false, s.guestBytes()); prepared {
			if e != nil {
				return 0, 0
			}
			return a, g
		}
	}
	return flash.UserdataLayout(s.drive.Size, wantGiB, false)
}
