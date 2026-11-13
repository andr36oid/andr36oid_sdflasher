package firmware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func testBootloader() []byte {
	b := make([]byte, bootloaderCopySize)
	copy(b, []byte("LOADER  "))
	payload := []byte("small packer fixture payload!!!!")
	for len(payload)%4 != 0 {
		payload = append(payload, 0)
	}
	binary.LittleEndian.PutUint32(b[16:], 0x200000)
	binary.LittleEndian.PutUint32(b[20:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(b[24:], loaderCRC(payload))
	binary.LittleEndian.PutUint32(b[28:], 32)
	copy(b[bootloaderHeaderSize:], payload)
	h := sha256.New()
	h.Write(payload)
	h.Write(b[16:24])
	h.Write(b[28:32])
	copy(b[32:], h.Sum(nil))
	return bytes.Repeat(b, 4)
}
func TestBootloaderContainer(t *testing.T) {
	good := testBootloader()
	if e := CheckBootloaderContainer(good); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"size", func(b []byte) []byte { return b[:len(b)-1] }},
		{"magic-copy4", func(b []byte) []byte { b[3*bootloaderCopySize] = 0; return b }},
		{"address", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[16:], 0x400000); return b }},
		{"payload-bound", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[20:], bootloaderCopySize); return b }},
		{"payload", func(b []byte) []byte { b[bootloaderHeaderSize] ^= 1; return b }},
		{"hash", func(b []byte) []byte { b[32] ^= 1; return b }},
		{"hashless", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[28:], 0); return b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if CheckBootloaderContainer(tc.edit(append([]byte{}, good...))) == nil {
				t.Fatal("accepted corrupt container")
			}
		})
	}
}
func TestBootloaderRealReleases(t *testing.T) {
	dir := os.Getenv("A36_BOOTLOADER_FIXTURES")
	if dir == "" {
		t.Skip("release assets kept outside the repo")
	}
	for _, repo := range []string{"u-boot", "uboot-mainline"} {
		b, e := os.ReadFile(filepath.Join(dir, repo+"-uboot.img"))
		if e != nil {
			t.Fatal(e)
		}
		if e = CheckBootloaderContainer(b); e != nil {
			t.Fatal(repo, e)
		}
	}
}
func TestBootloaderSlotBounds(t *testing.T) {
	f, e := os.CreateTemp(t.TempDir(), "card")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = f.Truncate(32 << 20); e != nil {
		t.Fatal(e)
	}
	canary := bytes.Repeat([]byte{0xa5}, 512)
	for _, off := range []int64{64 * 512, BootloaderOffset - 512, BootloaderOffset + BootloaderSize, 24576 * 512} {
		if _, e = f.WriteAt(canary, off); e != nil {
			t.Fatal(e)
		}
	}
	data := testBootloader()
	if e = WriteBootloaderSlot(f, data, nil); e != nil {
		t.Fatal(e)
	}
	for _, off := range []int64{64 * 512, BootloaderOffset - 512, BootloaderOffset + BootloaderSize, 24576 * 512} {
		b := make([]byte, 512)
		if _, e = f.ReadAt(b, off); e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(b, canary) {
			t.Fatal("changed outside slot", off)
		}
	}
	got := make([]byte, BootloaderSize)
	if _, e = f.ReadAt(got, BootloaderOffset); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("slot differs")
	}
}

type loaderWriteOrder struct {
	*os.File
	offsets []int64
	fail    int
}

func (d *loaderWriteOrder) WriteAt(b []byte, off int64) (int, error) {
	d.offsets = append(d.offsets, off)
	if len(d.offsets) == d.fail {
		return 0, os.ErrPermission
	}
	return d.File.WriteAt(b, off)
}
func TestBootloaderSoleOldCopySurvivesWriteFailures(t *testing.T) {
	for sole := 0; sole < 4; sole++ {
		for fail := 1; fail <= 4; fail++ {
			f, e := os.CreateTemp(t.TempDir(), "card")
			if e != nil {
				t.Fatal(e)
			}
			f.Truncate(32 << 20)
			data := testBootloader()
			f.WriteAt(data[:bootloaderCopySize], BootloaderOffset+int64(sole*bootloaderCopySize))
			d := &loaderWriteOrder{File: f, fail: fail}
			if WriteBootloaderSlot(d, data, nil) == nil {
				t.Fatal("fault ignored")
			}
			if len(d.offsets) == 4 && d.offsets[3] != BootloaderOffset+int64(sole*bootloaderCopySize) {
				t.Fatal("last old valid copy overwritten early")
			}
			// At every injected write boundary at least one complete validated copy remains.
			valid := 0
			for n := 0; n < 4; n++ {
				b := make([]byte, bootloaderCopySize)
				f.ReadAt(b, BootloaderOffset+int64(n*bootloaderCopySize))
				if checkLoaderCopy(b) == nil {
					valid++
				}
			}
			if valid == 0 {
				t.Fatal("all loader copies lost", sole, fail)
			}
			f.Close()
		}
	}
}
func TestBootloaderImageMissing(t *testing.T) {
	if _, _, e := ResolveBootloader(context.Background(), BootloaderChoice{Choice: "image"}, nil, t.TempDir()); e == nil {
		t.Fatal("missing image accepted")
	}
}
