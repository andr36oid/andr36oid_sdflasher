package firmware

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

// A Boot options error crosses the disk helper as text, so the frontends find
// out what kind of failure it was from these markers at the start of the error.
const (
	// Nothing was written: the card's boot area is not laid out like ours.
	BootRefusedMark = "boot chain refused"
	// Writing U-Boot stopped part way. The copy-by-copy writer leaves at least
	// one complete U-Boot copy, so the card still starts.
	BootUbootStoppedMark = "U-Boot write stopped part way"
	// Writing the first stage, the sleep firmware or a mainline FIT stopped
	// part way. Those are single writes, so the card may not start.
	BootStageStoppedMark = "boot firmware write stopped part way"
)

func bootRefused(e error) error      { return fmt.Errorf("%s: %w", BootRefusedMark, e) }
func bootUbootStopped(e error) error { return fmt.Errorf("%s: %w", BootUbootStoppedMark, e) }
func bootStageStopped(e error) error { return fmt.Errorf("%s: %w", BootStageStoppedMark, e) }

// BootErrorKind tells which marker an error (or its text) carries: "refused",
// "uboot-stopped", "stage-stopped" or "" for any other failure.
func BootErrorKind(e error) string {
	if e == nil {
		return ""
	}
	s := e.Error()
	switch {
	case strings.Contains(s, BootRefusedMark):
		return "refused"
	case strings.Contains(s, BootUbootStoppedMark):
		return "uboot-stopped"
	case strings.Contains(s, BootStageStoppedMark):
		return "stage-stopped"
	}
	return ""
}

// BootOptionsOnCard reports whether every slot that ApplyResolvedBootOptions
// would write already holds exactly those bytes, so applying changes nothing.
// It only reads the card.
func BootOptionsOnCard(d io.ReaderAt, size int64, data []byte, out *BootloaderOutcome) (bool, error) {
	if out == nil || len(data) == 0 {
		return false, nil
	}
	tab, e := layout.Read(d, size)
	if e != nil {
		return false, e
	}
	same := func(off int64, want []byte) (bool, error) {
		got := make([]byte, len(want))
		if _, e := d.ReadAt(got, off); e != nil {
			return false, e
		}
		return bytes.Equal(got, want), nil
	}
	if out.plan == nil {
		if len(data) != BootloaderSize {
			return false, nil
		}
		if size < BootloaderOffset+BootloaderSize {
			return false, nil
		}
		if p, ok := tab.Find("uboot"); ok && (p.Offset() != BootloaderOffset || p.Size() != BootloaderSize) {
			return false, nil
		}
		return same(BootloaderOffset, data)
	}
	slots, e := bootSlots(tab)
	if e != nil {
		return false, nil
	}
	if len(out.plan.Slots) == 0 {
		return false, nil
	}
	for i, s := range out.plan.Slots {
		target, ok := slots[s.Name]
		if !ok || int64(len(out.plan.Data[i])) > target.Size {
			return false, nil
		}
		// The writer pads each slot with zeros; compare the same bytes.
		b := make([]byte, target.Size)
		copy(b, out.plan.Data[i])
		ok, e := same(target.Offset, b)
		if e != nil || !ok {
			return false, e
		}
	}
	return true, nil
}
