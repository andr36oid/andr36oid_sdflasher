package ui

import (
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"testing"
)

func TestMapNeverPaintsPreservedRegionsAsWritten(t *testing.T) {
	m := newDiskMap()
	p := &flash.Plan{Size: 100000, Protected: []flash.Range{{Name: "userdata", Offset: 50000, Length: 50000}}}
	m.plan(p)
	m.update(flash.Progress{Phase: "writing", Offset: 0, Length: 100000})
	m.update(flash.Progress{Phase: "verifying", Offset: 0, Length: 100000})
	for i := len(m.state) / 2; i < len(m.state); i++ {
		if m.state[i] != 2 {
			t.Fatal("preserved region displayed as written")
		}
	}
}

func TestMapSquashesUnassignedTailAsFirstBoot(t *testing.T) {
	const GiB = 1 << 30
	m := newDiskMap()
	// A fresh 64 GiB card: ~2 GiB of content at the start, backup table pinned to the end.
	p := &flash.Plan{Size: 64 * GiB, Writes: []flash.Write{
		{Range: flash.Range{Name: "Release image", Offset: 0, Length: 2 * GiB}},
		{Range: flash.Range{Name: "Backup partition table", Offset: 64*GiB - 8192, Length: 8192}},
	}}
	m.plan(p)
	content, firstBoot := 0, 0
	for _, s := range m.state {
		switch s {
		case 1:
			content++
		case 6:
			firstBoot++
		}
	}
	if firstBoot == 0 {
		t.Fatal("unassigned tail was not shown as an assigned-on-first-boot band")
	}
	// Without squashing, 2 GiB of 64 would be ~3% of the grid; squashing must give the
	// real content a visible share so it is not a sliver.
	if content < len(m.state)/5 {
		t.Fatalf("content squashed too small: %d of %d cells", content, len(m.state))
	}
}

func TestMapNoFirstBootBandWhenCardIsFull(t *testing.T) {
	m := newDiskMap()
	// An update: preserved userdata already fills the end, so nothing is "assigned later".
	p := &flash.Plan{Size: 100000, Protected: []flash.Range{{Name: "userdata", Offset: 20000, Length: 80000}}}
	m.plan(p)
	for _, s := range m.state {
		if s == 6 {
			t.Fatal("a full card should have no assigned-on-first-boot band")
		}
	}
}
