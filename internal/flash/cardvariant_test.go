package flash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

// installedCard is a regular file laid out as an andr36oid card after its first
// start: GPT, a FAT BOOT with the given files, an ext4 userdata (its magic only).
func installedCard(t *testing.T, files map[string]string) (*os.File, int64) {
	t.Helper()
	const bootStart, bootSize, partSize = 2048, 32 << 20, 4 << 20
	f, e := os.Create(filepath.Join(t.TempDir(), "card.img"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	vendorStart := int64(bootStart + bootSize/512)
	dataStart := vendorStart + partSize/512
	size := (dataStart + partSize/512 + 2048) * 512
	f.Truncate(size)
	part := func(n int, name string, start, bytes int64) layout.Partition {
		return layout.Partition{Number: n, Name: name, Start: uint64(start), End: uint64(start + bytes/512 - 1), Type: layout.LinuxType, ID: layout.GUID()}
	}
	tab := layout.Table{Size: size, GPT: true, ID: layout.GUID(), Parts: []layout.Partition{
		part(1, "BOOT", bootStart, bootSize), part(2, "vendor", vendorStart, partSize), part(3, "userdata", dataStart, partSize),
	}}
	head, tail, e := tab.Encode()
	if e != nil {
		t.Fatal(e)
	}
	f.WriteAt(head, 0)
	f.WriteAt(tail, size-int64(len(tail)))
	f.WriteAt([]byte{0x53, 0xef}, dataStart*512+1080)
	fs, e := fat32.Create(file.New(f, false), bootSize, bootStart*512, 512, "BOOT", true)
	if e != nil {
		t.Fatal(e)
	}
	files["boot.ini"] = "odroidgoa-uboot-config\n"
	files["rk3326-r36s-android.dtb"] = "\xd0\x0d\xfe\xed dtb"
	for n, d := range files {
		w, e := fs.OpenFile("/"+n, os.O_CREATE|os.O_RDWR)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte(d))
		w.Close()
	}
	return f, size
}

// The card's kind is in the inspection the app gets from the helper.
func TestInspectCardReadsTheKind(t *testing.T) {
	f, size := installedCard(t, map[string]string{firmware.BuildInfoV2File: "format=2\nkernel_line=4.19\ngpu_variant=gles\n"})
	c, e := InspectCard(f, size)
	if e != nil || !c.Installed || c.Kernel != "4.19" || c.Graphics != "gles" || c.VariantFrom != firmware.VariantFromInfo {
		t.Fatalf("%v %+v", e, c)
	}
	f, size = installedCard(t, map[string]string{firmware.KernelImageFile: "....Linux version 5.10.198-rk3326-itskenny0 (x@y) #1 SMP\x00"})
	if c, e = InspectCard(f, size); e != nil || !c.Installed || c.Kernel != "5.10" || c.Graphics != "" || c.VariantFrom != firmware.VariantFromKernel {
		t.Fatalf("%v %+v", e, c)
	}
	f, size = installedCard(t, map[string]string{})
	if c, e = InspectCard(f, size); e != nil || !c.Installed || c.Kernel != "" || c.Graphics != "" || c.VariantFrom != "" {
		t.Fatalf("%v %+v", e, c)
	}
}
