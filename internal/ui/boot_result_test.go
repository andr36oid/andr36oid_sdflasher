package ui

import (
	"errors"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

// The Boot options page says plainly what happened for each outcome.
func TestBootApplyMessages(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &screen{locale: "en"}
	for _, c := range []struct {
		name  string
		out   *firmware.BootloaderOutcome
		err   error
		want  string
		retry bool
	}{
		{"applied", &firmware.BootloaderOutcome{Record: &firmware.BootloaderRecord{Choice: "new", Version: "2026.10", Tag: "20261113-1313"}}, nil, bootApplied, false},
		{"unchanged", &firmware.BootloaderOutcome{Unchanged: true}, nil, bootUnchanged, false},
		{"refused", nil, errors.New(firmware.BootRefusedMark + ": unsupported boot chain slot layout"), bootRefusedText, false},
		{"uboot-stopped", nil, errors.New(firmware.BootUbootStoppedMark + ": written U-Boot copy did not verify"), bootUbootStopped, true},
		{"stage-stopped", nil, errors.New(firmware.BootStageStoppedMark + ": boot firmware readback differs"), bootStageStopped, true},
		{"other", nil, errors.New("the card changed; choose it again"), "the card changed; choose it again", true},
	} {
		got, retry := s.bootApplyMessage(c.out, c.err)
		if !strings.HasPrefix(got, c.want) || retry != c.retry {
			t.Errorf("%s: %q retry=%v", c.name, got, retry)
		}
	}
	if !strings.Contains(s.t(bootSourceImage), "recommended") || !strings.Contains(s.t(bootSourceFirstStage), "first start-up stage stays") {
		t.Fatal("sources help")
	}
	for _, k := range []string{bootSourcesTitle, bootSourceImage, bootSourceStandard, bootSourceNew, bootSourceFirstStage, bootApplied, bootUnchanged, bootRefusedText, bootUbootStopped, bootStageStopped} {
		for _, bad := range []string{"—", "–", "·", "…", "GPLv3"} {
			if strings.Contains(k, bad) {
				t.Fatalf("%q contains %q", k, bad)
			}
		}
	}
}
