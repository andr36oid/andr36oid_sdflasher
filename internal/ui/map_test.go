package ui

import (
	"testing"

	"fyne.io/fyne/v2"
)

// Kept regions are never drawn as written, only what is written gets blocks, and a
// failure marks its block and names the partition.
func TestBlockViewKeepsUserData(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	p := testPlan(true)
	strip, groups, empty := s.mapPlan(p)
	if len(empty) != 0 {
		t.Fatal("update: nothing is set up empty", empty)
	}
	var written int64
	for _, g := range groups {
		if g.kept && len(g.blocks) != 0 {
			t.Fatal("kept region has blocks")
		}
		for _, b := range g.blocks {
			written += b.length
		}
	}
	var want int64
	for _, w := range p.Writes {
		want += w.Length
	}
	if written != want {
		t.Fatal("blocks are not exactly what is written", written, want)
	}
	m := newBlockMap()
	m.setGroups(groups, "kept")
	m.mark(0, p.Size, blockWritten)
	for _, g := range m.groups {
		for _, b := range g.blocks {
			if b.state != blockWritten {
				t.Fatal("block not written")
			}
		}
	}
	kept := 0
	for _, sp := range strip {
		if sp.state == blockKept {
			kept++
		}
	}
	if kept != 3 {
		t.Fatal("strip: kept partitions", kept)
	}
	if name := m.fail(p.Writes[1].Offset + 5); name != "System" {
		t.Fatal(name)
	}
}

// A release image that spans several partitions is shown per partition; the
// partitions that are only set up are named, not drawn.
func TestBlockViewGroupsByPartition(t *testing.T) {
	s := testScreen(t, fyne.NewSize(980, 680))
	_, groups, empty := s.mapPlan(testPlan(false))
	names := map[string]bool{}
	for _, g := range groups {
		names[g.name] = true
	}
	for _, want := range []string{"Bootloader", "Boot", "System", "Vendor", "Game system", "Partition table"} {
		if !names[want] {
			t.Fatal("missing group", want, names)
		}
	}
	if names["Android"] || len(empty) != 2 || empty[0] != "Android" || empty[1] != "Games" {
		t.Fatal(empty)
	}
}
