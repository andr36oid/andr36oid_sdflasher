package flash

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"os"
	"path/filepath"
	"testing"
)

func TestClassicPlanRefreshesVendorBeforeUboot(t *testing.T) {
	im, card, _, _ := sample(t)
	im.Facts = map[string]string{"bootloader_chain": "classic"}
	// Replace tiny test boot slots with the real classic layout; move all other
	// fixture partitions beyond it, retaining the sample's preserved data layout.
	im.Size = 64 << 20
	card.Table.Size = 128 << 20
	for i := range im.Table.Parts {
		p := &im.Table.Parts[i]
		switch p.Name {
		case "idbloader":
			p.Start = 64
			p.End = 16383
		case "uboot":
			p.Start = 16384
			p.End = 24575
		case "trust":
			p.Start = 24576
			p.End = 32767
		default:
			p.Start += 32768
			p.End += 32768
		}
	}
	card.Table.Parts = append([]layout.Partition{}, im.Table.Parts...)
	card.Table.Parts = append(card.Table.Parts, layout.Partition{Number: 6, Name: "userdata", Start: 131072, End: 196607, Type: layout.LinuxType, ID: layout.GUID()})
	p, e := Build(im, card, card.Table.Size, "update", "Panels/Panel4", false, 0)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]int{}
	for i, w := range p.Writes {
		if w.Name == "idbloader" || w.Name == "trust" || w.Name == "uboot" {
			seen[w.Name] = i
		}
		if w.Name == "uboot" && (!w.Uboot || w.Offset != firmware.BootloaderOffset || w.Length != firmware.BootloaderSize) {
			t.Fatal("raw U-Boot write", w)
		}
	}
	if len(seen) != 3 || seen["idbloader"] >= seen["uboot"] || seen["trust"] >= seen["uboot"] {
		t.Fatal("vendor refresh ordering", seen)
	}
	for _, r := range p.Protected {
		if r.Name == "idbloader" || r.Name == "trust" {
			t.Fatal("old vendor refresh suppressed", r)
		}
	}
	card.Installed = false
	if _, e := Build(im, card, card.Table.Size, "update", "Panels/Panel4", false, 0); e == nil {
		t.Fatal("unrecognized installation accepted")
	}
	for i := range p.Writes {
		if p.Writes[i].Uboot {
			p.Writes[i].Offset = 64 * 512
			if p.Validate() == nil {
				t.Fatal("malformed copy write accepted")
			}
			break
		}
	}
}

type classicObserved struct {
	*os.File
	copies    []int64
	cut       int
	partial   int
	stop      bool
	preserved bool
}

func (d *classicObserved) WriteAt(b []byte, off int64) (int, error) {
	if off >= firmware.BootloaderOffset && off < firmware.BootloaderOffset+firmware.BootloaderSize {
		d.copies = append(d.copies, off)
		if len(b) != (1 << 20) {
			return 0, fmt.Errorf("bulk call point bypassed copy writer: %d bytes", len(b))
		}
		if d.cut == len(d.copies) {
			n := d.partial
			if n > len(b) {
				n = len(b)
			}
			_, _ = d.File.WriteAt(b[:n], off)
			d.stop = true
			return n, errors.New("simulated power cut")
		}
	}
	if d.stop {
		return 0, errors.New("device unavailable after power cut")
	}
	return d.File.WriteAt(b, off)
}
func classicExecuteFixture(t *testing.T) (*firmware.Image, *Plan, *os.File, string) {
	im, card, dst, boot := sample(t)
	im.Size = 64 << 20
	im.Table.Size = im.Size
	card.Table.Size = 128 << 20
	im.Facts = map[string]string{"bootloader_chain": "classic"}
	for i := range im.Table.Parts {
		p := &im.Table.Parts[i]
		switch p.Name {
		case "idbloader":
			p.Start = 64
			p.End = 16383
		case "uboot":
			p.Start = 16384
			p.End = 24575
		case "trust":
			p.Start = 24576
			p.End = 32767
		default:
			p.Start += 32768
			p.End += 32768
		}
	}
	card.Table.Parts = append([]layout.Partition{}, im.Table.Parts...)
	card.Table.Parts = append(card.Table.Parts, layout.Partition{Number: 6, Name: "userdata", Start: 131072, End: 196607, Type: layout.LinuxType, ID: layout.GUID()})
	src, e := os.OpenFile(im.Path, os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	if e = src.Truncate(im.Size); e != nil {
		t.Fatal(e)
	}
	if e = dst.Truncate(card.Table.Size); e != nil {
		t.Fatal(e)
	}
	blobs := classicBlobs(t)
	for _, name := range []string{"idbloader", "trust", "uboot"} {
		q, _ := im.Table.Find(name)
		data := blobs[name]
		if _, e = src.WriteAt(data, q.Offset()); e != nil {
			t.Fatal(e)
		}
		if name != "uboot" {
			whole := make([]byte, q.Size())
			if _, e = src.ReadAt(whole, q.Offset()); e != nil {
				t.Fatal(e)
			}
			if _, e = dst.WriteAt(whole, q.Offset()); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e = dst.WriteAt(blobs["olduboot"], firmware.BootloaderOffset); e != nil {
		t.Fatal(e)
	}
	im.SHA256, e = firmware.Hash(context.Background(), src, im.Size, nil)
	if e != nil {
		t.Fatal(e)
	}
	card.Fingerprint, e = Snapshot(dst, card.Table.Size)
	if e != nil {
		t.Fatal(e)
	}
	p, e := Build(im, card, card.Table.Size, "update", "Panels/Panel4", false, 0)
	if e != nil {
		t.Fatal(e)
	}
	return im, p, dst, boot
}
func copyStillValid(f *os.File) bool {
	b := make([]byte, 1<<20)
	for i := 0; i < 4; i++ {
		if _, e := f.ReadAt(b, firmware.BootloaderOffset+int64(i)*(1<<20)); e != nil {
			continue
		}
		all := bytes.Repeat(b, 4)
		if firmware.CheckBootloaderContainer(all) == nil {
			return true
		}
	}
	return false
}
func TestClassicExecuteUsesCopyCallPointAndPreservesChain(t *testing.T) {
	im, p, dst, boot := classicExecuteFixture(t)
	d := &classicObserved{File: dst}
	before := map[string][]byte{}
	for _, r := range p.Protected {
		b := make([]byte, r.Length)
		dst.ReadAt(b, r.Offset)
		before[r.Name] = b
	}
	if e := Execute(context.Background(), d, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	if len(d.copies) != 4 {
		t.Fatal("missing copy writes", d.copies)
	}
	for i, off := range d.copies {
		if off != firmware.BootloaderOffset+int64(3-i)*(1<<20) {
			t.Fatal("wrong call-point order", d.copies)
		}
	}
	for _, r := range p.Protected {
		b := make([]byte, r.Length)
		dst.ReadAt(b, r.Offset)
		if !bytes.Equal(b, before[r.Name]) {
			t.Fatal("protected changed", r.Name)
		}
	}
	if !copyStillValid(dst) {
		t.Fatal("no bootable copy")
	}
}
func TestClassicExecuteInterruptedEachCopy(t *testing.T) {
	for cut := 1; cut <= 4; cut++ {
		for _, partial := range []int{0, 512, 2048, 524288, 1048575} {
			t.Run(fmt.Sprintf("copy%d-byte%d", cut, partial), func(t *testing.T) {
				im, p, dst, boot := classicExecuteFixture(t)
				d := &classicObserved{File: dst, cut: cut, partial: partial}
				if e := Execute(context.Background(), d, im, p, boot, t.TempDir(), nil); e == nil {
					t.Fatal("power cut accepted")
				}
				if !copyStillValid(dst) {
					t.Fatal("power cut lost all old/new copies")
				}
			})
		}
	}
}
func TestClassicExecuteRefreshesOlderVendorPieces(t *testing.T) {
	im, p, dst, boot := classicExecuteFixture(t)
	for _, name := range []string{"idbloader", "trust"} {
		q, _ := im.Table.Find(name)
		var b [1]byte
		dst.ReadAt(b[:], q.Offset())
		b[0] ^= 1
		dst.WriteAt(b[:], q.Offset())
	}
	d := &classicObserved{File: dst}
	if e := Execute(context.Background(), d, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal("older vendor update refused", e)
	}
	src, e := os.Open(im.Path)
	if e != nil {
		t.Fatal(e)
	}
	defer src.Close()
	for _, name := range []string{"idbloader", "trust"} {
		q, _ := im.Table.Find(name)
		if e = verify(context.Background(), dst, src, q.Offset(), q.Offset(), q.Size(), nil); e != nil {
			t.Fatal(name, e)
		}
	}
	if len(d.copies) != 4 {
		t.Fatal("classic copy path lost", d.copies)
	}
}

func TestClassicRestoreUsesRedundantCopies(t *testing.T) {
	im, p, dst, boot := classicExecuteFixture(t)
	dir := t.TempDir()
	saved, e := saveBackup(context.Background(), dst, p, dir, func(Progress) {})
	if e != nil {
		t.Fatal(e)
	}
	if e = Execute(context.Background(), dst, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	d := &classicObserved{File: dst}
	if e = restore(context.Background(), d, dir, saved); e != nil {
		t.Fatal(e)
	}
	if len(d.copies) != 4 {
		t.Fatal("restore bypassed copy writer", d.copies)
	}
	old := classicBlobs(t)["olduboot"]
	got := make([]byte, firmware.BootloaderSize)
	dst.ReadAt(got, firmware.BootloaderOffset)
	if !bytes.Equal(got, old) {
		t.Fatal("restore did not recover BSP U-Boot")
	}
}

func TestClassicRestoreAcceptsChecksummedDamagedSlot(t *testing.T) {
	_, p, dst, _ := classicExecuteFixture(t)
	damaged := make([]byte, firmware.BootloaderSize)
	dst.ReadAt(damaged, firmware.BootloaderOffset)
	for i := 0; i < 512; i++ {
		damaged[i] = 0
	}
	if firmware.CheckBootloaderContainer(damaged) == nil {
		t.Fatal("damage fixture invalid")
	}
	dst.WriteAt(damaged, firmware.BootloaderOffset)
	dir := t.TempDir()
	saved, e := saveBackup(context.Background(), dst, p, dir, func(Progress) {})
	if e != nil {
		t.Fatal(e)
	}
	dst.WriteAt(bytes.Repeat([]byte{0x5a}, len(damaged)), firmware.BootloaderOffset)
	if e = restore(context.Background(), dst, dir, saved); e != nil {
		t.Fatal("damaged backup rejected", e)
	}
	got := make([]byte, len(damaged))
	dst.ReadAt(got, firmware.BootloaderOffset)
	if !bytes.Equal(got, damaged) {
		t.Fatal("damaged backup was not restored byte for byte")
	}
}

// classicBlobs returns the vendor first stage, vendor trust, the image's new
// U-Boot slot and the card's older U-Boot slot. With CLASSIC_VENDOR (the C11
// vendor bootloader directory) and CLASSIC_ASSETS (the classic build output)
// set, the real release files are used; otherwise small synthetic stand-ins
// with valid LOADER containers keep the test self-contained.
func classicBlobs(t *testing.T) map[string][]byte {
	t.Helper()
	vendor, assets := os.Getenv("CLASSIC_VENDOR"), os.Getenv("CLASSIC_ASSETS")
	if vendor != "" && assets != "" {
		out := map[string][]byte{}
		for name, path := range map[string]string{"idbloader": filepath.Join(vendor, "idbloader.img"), "trust": filepath.Join(vendor, "trust.img"), "uboot": filepath.Join(assets, "uboot.img"), "olduboot": filepath.Join(vendor, "uboot.img")} {
			b, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			out[name] = b
		}
		return out
	}
	return map[string][]byte{
		"idbloader": bytes.Repeat([]byte("vendor first stage "), 9000),
		"trust":     bytes.Repeat([]byte("vendor trust "), 330000)[:4<<20],
		"uboot":     classicLoader("mainline classic U-Boot proper payload"),
		"olduboot":  classicLoader("older BSP U-Boot proper payload!"),
	}
}

// classicLoader builds a four-copy Rockchip LOADER container like the one
// loaderimage packs (SHA-256, load address 0x200000).
func classicLoader(text string) []byte {
	b := make([]byte, 1<<20)
	copy(b, []byte("LOADER  "))
	payload := bytes.Repeat([]byte(text), 64)
	for len(payload)%4 != 0 {
		payload = append(payload, 0)
	}
	var crc uint32
	for _, c := range payload {
		v := uint32(byte(crc>>24)^c) << 24
		for bit := 0; bit < 8; bit++ {
			if v&0x80000000 != 0 {
				v = v<<1 ^ 0x04c10db7
			} else {
				v <<= 1
			}
		}
		crc = v ^ (crc << 8)
	}
	binary.LittleEndian.PutUint32(b[16:], 0x200000)
	binary.LittleEndian.PutUint32(b[20:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(b[24:], crc)
	binary.LittleEndian.PutUint32(b[28:], 32)
	copy(b[2048:], payload)
	h := sha256.New()
	h.Write(payload)
	h.Write(b[16:24])
	h.Write(b[28:32])
	copy(b[32:], h.Sum(nil))
	return bytes.Repeat(b, 4)
}

func TestClassicSyntheticLoaderIsValid(t *testing.T) {
	if e := firmware.CheckBootloaderContainer(classicLoader("x")); e != nil {
		t.Fatal(e)
	}
}
