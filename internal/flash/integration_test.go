package flash

import (
	"bytes"
	"context"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// This test writes only newly created regular files. It never opens a disk device.
func TestReleasedGPTMigration(t *testing.T) {
	src := os.Getenv("SDFLASHER_TEST_IMAGE")
	legacy := os.Getenv("SDFLASHER_TEST_LEGACY")
	if src == "" || legacy == "" {
		t.Skip("set both release-image fixture paths")
	}
	ctx := context.Background()
	im, e := firmware.Inspect(ctx, src, nil)
	if e != nil {
		t.Fatal(e)
	}
	old, e := os.Open(legacy)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	st, _ := old.Stat()
	tab, e := layout.Read(old, st.Size())
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	card, e := os.Create(filepath.Join(dir, "card.img"))
	if e != nil {
		t.Fatal(e)
	}
	defer card.Close()
	if _, e = io.Copy(card, old); e != nil {
		t.Fatal(e)
	}
	const size = int64(32 << 30)
	card.Truncate(size)
	tab.Size = size
	tab.Parts = append(tab.Parts, layout.Partition{Number: 6, Name: "userdata", Start: 5701009, End: 7000000, Type: layout.LinuxType, ID: layout.GUID()}, layout.Partition{Number: 7, Name: "EASYROMS", Start: 7000001, End: uint64(size/512) - 34, Type: layout.LinuxType, ID: layout.GUID()})
	head, tail, e := tab.Encode()
	if e != nil {
		t.Fatal(e)
	}
	card.WriteAt(head, 0)
	card.WriteAt(tail, size-int64(len(tail)))
	u, _ := tab.Find("userdata")
	r, _ := tab.Find("EASYROMS")
	card.WriteAt(bytes.Repeat([]byte{0xba}, 1<<20), u.Offset())
	card.WriteAt([]byte{0x10, 0x20, 0xf5, 0xf2}, u.Offset()+1024)
	card.WriteAt(bytes.Repeat([]byte{0xcf}, 1<<20), r.Offset())
	beforeU, _ := firmware.Hash(ctx, io.NewSectionReader(card, u.Offset(), u.Size()), u.Size(), nil)
	beforeR, _ := firmware.Hash(ctx, io.NewSectionReader(card, r.Offset(), r.Size()), r.Size(), nil)
	info, e := InspectCard(card, size)
	if e != nil || !info.Installed {
		t.Fatalf("inspect: %v %+v", e, info)
	}
	p, e := Build(im, info, size, "update", "Panels/Panel4", false, 0)
	if e != nil {
		t.Fatal(e)
	}
	boot, e := firmware.PrepareBoot(im, p.Profile, false, true, "", dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = Execute(ctx, card, im, p, boot, filepath.Join(dir, "recovery"), nil); e != nil {
		t.Fatal(e)
	}
	afterU, _ := firmware.Hash(ctx, io.NewSectionReader(card, u.Offset(), u.Size()), u.Size(), nil)
	afterR, _ := firmware.Hash(ctx, io.NewSectionReader(card, r.Offset(), r.Size()), r.Size(), nil)
	if beforeU != afterU || beforeR != afterR {
		t.Fatal("migration changed preserved data")
	}
	newtab, e := layout.Read(card, size)
	if e != nil {
		t.Fatal(e)
	}
	fs, e := firmware.OpenBoot(card, newtab, true)
	if e != nil {
		t.Fatal(e)
	}
	script, e := firmware.ReadFile(fs, "/boot.ini", 128<<10)
	if e != nil || !bytes.Contains(script, []byte("ramdisk.uimg")) || bytes.Contains(script, []byte("Image-resizing")) {
		t.Fatal("migration left the formatter boot active")
	}
	// bit 63 (hidden from Windows) is set on andr36oid's own partitions now
	newU, _ := newtab.Find("userdata")
	newU.Flags = u.Flags
	if newU != u {
		t.Fatalf("userdata identity changed: %+v, was %+v", newU, u)
	}
}
