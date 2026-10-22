package firmware

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/dtbconv"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

// arkosLike gives one of our DTBs ArkOS key codes, so the converter takes
// it for a stock DTB of that board.
func arkosLike(t *testing.T, b []byte) []byte {
	t.Helper()
	tr, e := dtbconv.Parse(b)
	if e != nil {
		t.Fatal(e)
	}
	ark := map[uint32]uint32{103: 0x220, 108: 0x221, 105: 0x222, 106: 0x223, 304: 0x131, 305: 0x130, 307: 0x133, 308: 0x134,
		310: 0x136, 311: 0x137, 312: 0x138, 313: 0x139, 314: 0x2c0, 315: 0x2c1, 158: 0x2c2, 125: 0x2c3, 172: 0x2c4}
	tr.Walk(func(n *dtbconv.Node) {
		if !n.Compatible("odroidgo3-joypad") {
			return
		}
		for _, ch := range n.Children {
			if c, ok := ch.U32("linux,code"); ok {
				ch.SetU32("linux,code", ark[c])
			}
		}
	})
	tr.Root.SetString("model", "Test EE board")
	return tr.Bytes()
}

// fakeImage makes a small image with a FAT BOOT partition holding two
// device profiles, Panel 4 and EE G80 Panel 8.
func fakeImage(t *testing.T, dir string) *Image {
	t.Helper()
	const bootStart, bootSize = 2048, 64 << 20
	p := filepath.Join(dir, "test.img")
	f, e := os.Create(p)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	size := int64(bootStart*512 + bootSize + 1<<20)
	if e := f.Truncate(size); e != nil {
		t.Fatal(e)
	}
	tab := layout.Table{Size: size, Parts: []layout.Partition{{Number: 1, Name: "BOOT", Start: bootStart, End: bootStart + bootSize/512 - 1, Type: layout.LinuxType, ID: layout.GUID()}}}
	fs, e := fat32.Create(file.New(f, false), bootSize, bootStart*512, 512, "BOOT", true)
	if e != nil {
		t.Fatal(e)
	}
	b := dtbconv.EmbeddedBases()
	files := map[string][]byte{
		"boot.ini-android":                                   []byte("load mmc 1:1 ${dtb_loadaddr} rk3326-r36s-android.dtb\nload mmc 1:1 ${ramdisk_addr_r} ramdisk.uimg\n"),
		"Panels/Panel4/rk3326-r36s-android.dtb":              b.Genuine,
		"Panels/Panel4/rg351mp-kernel.dtb":                   b.UBoot,
		"Experimental/EE-G80-Panel8/rk3326-r36s-android.dtb": b.EE,
		"Experimental/EE-G80-Panel8/rg351mp-kernel.dtb":      b.UBoot,
		ProfileList: []byte("Panels/Panel4|R36S|R36S — Panel 4|\n" +
			"Experimental/EE-G80-Panel8|G80|G80C / G80CA / G80D — Panel 8|experimental\n"),
	}
	for _, d := range []string{"Panels", "Panels/Panel4", "Experimental", "Experimental/EE-G80-Panel8"} {
		if e := fs.Mkdir(d); e != nil {
			t.Fatal(e)
		}
	}
	for n, d := range files {
		w, e := fs.OpenFile(n, os.O_CREATE|os.O_RDWR)
		if e != nil {
			t.Fatal(e)
		}
		w.Write(d)
		w.Close()
	}
	profiles, e := Profiles(fs)
	if e != nil {
		t.Fatal(e)
	}
	return &Image{Path: p, Size: size, Table: tab, Profiles: profiles}
}

func TestPrepareBootConvertsConsoleDTB(t *testing.T) {
	dir := t.TempDir()
	im := fakeImage(t, dir)
	b := dtbconv.EmbeddedBases()
	custom := filepath.Join(dir, "rk3326-r36s-linux.dtb")
	if e := os.WriteFile(custom, arkosLike(t, b.EE), 0o600); e != nil {
		t.Fatal(e)
	}
	conv, e := ConvertCustom(im, custom)
	if e != nil {
		t.Fatal(e)
	}
	if conv.Profile != "Experimental/EE-G80-Panel8" {
		t.Errorf("profile %q", conv.Profile)
	}
	// the user picked Panel 4; the conversion moves to the family profile
	out, e := PrepareBoot(im, "Panels/Panel4", false, false, custom, dir, BootOptions{ConvertDTB: true})
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(out)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	st, _ := f.Stat()
	fs, e := fat32.Read(file.New(f, true), st.Size(), 0, 512)
	if e != nil {
		t.Fatal(e)
	}
	read := func(n string) []byte {
		d, e := ReadFile(fs, n, 4<<20)
		if e != nil {
			t.Fatalf("%s: %v", n, e)
		}
		return d
	}
	if !bytes.Equal(read("/rk3326-r36s-android.dtb"), conv.Kernel) {
		t.Errorf("kernel DTB is not the converted one")
	}
	for _, n := range UBootDTBNames {
		if !bytes.Equal(read("/"+n), conv.UBoot) {
			t.Errorf("%s is not the converted boot logo DTB", n)
		}
	}
	if p := strings.TrimSpace(string(read("/andr36oid-profile.txt"))); p != "Experimental/EE-G80-Panel8" {
		t.Errorf("profile file says %q", p)
	}
	if !bytes.Equal(read("/"+BackupDir+"/original-rk3326-r36s-linux.dtb"), conv.Input) {
		t.Errorf("original not kept")
	}
	if !bytes.Equal(read("/"+BackupDir+"/replaced-rk3326-r36s-android.dtb"), b.EE) {
		t.Errorf("shipped DTB not kept")
	}
	read("/" + BackupDir + "/replaced-rg351mp-kernel.dtb")
	if !strings.Contains(string(read("/"+BackupDir+"/conversion-report.txt")), "Test EE board") {
		t.Errorf("report missing")
	}
}

func TestPrepareBootRefusesUnknownDTB(t *testing.T) {
	dir := t.TempDir()
	im := fakeImage(t, dir)
	custom := filepath.Join(dir, "ours.dtb")
	os.WriteFile(custom, dtbconv.EmbeddedBases().EE, 0o600)
	_, e := PrepareBoot(im, "Panels/Panel4", false, false, custom, dir, BootOptions{ConvertDTB: true})
	if e == nil || !dtbconv.IsRefusal(e) {
		t.Fatalf("want a refusal, got %v", e)
	}
}

// An image from before andr36oid-profiles.txt is refused, not listed by folder names.
func TestProfilesNeedTheImagesList(t *testing.T) {
	dir := t.TempDir()
	im := fakeImage(t, dir)
	f, e := os.OpenFile(im.Path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	p, _ := im.Table.Find("BOOT")
	fs, e := fat32.Read(file.New(f, false), p.Size(), p.Offset(), 512)
	if e != nil {
		t.Fatal(e)
	}
	if e = fs.Remove("/" + ProfileList); e != nil {
		t.Fatal(e)
	}
	if _, e = Profiles(fs); !errors.Is(e, ErrNoProfileList) {
		t.Fatalf("Profiles without the list: %v", e)
	}
}
