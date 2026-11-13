package firmware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func loaderCard(t *testing.T, extra map[string]string) (*os.File, int64) {
	t.Helper()
	const size = 64 << 20
	const start = 32768
	const bootSize = 32 << 20
	f, e := os.CreateTemp(t.TempDir(), "card")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	if e = f.Truncate(size); e != nil {
		t.Fatal(e)
	}
	part := func(n int, name string, off, bytes int64) layout.Partition {
		return layout.Partition{Number: n, Name: name, Start: uint64(off / 512), End: uint64((off+bytes)/512 - 1), Type: layout.LinuxType, ID: layout.GUID()}
	}
	tab := layout.Table{Size: size, GPT: true, Parts: []layout.Partition{part(1, "BOOT", start*512, bootSize), part(2, "vendor", 48<<20, 4<<20), part(3, "system", 52<<20, 4<<20)}}
	head, tail, e := tab.Encode()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(head, 0); e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(tail, size-int64(len(tail))); e != nil {
		t.Fatal(e)
	}
	boot, e := fat32.Create(file.New(f, false), bootSize, start*512, 512, "BOOT", true)
	if e != nil {
		t.Fatal(e)
	}
	files := map[string]string{BuildInfoFile: testBuildInfo, "keep.txt": "preserve this"}
	for n, v := range extra {
		files[n] = v
	}
	for n, v := range files {
		if e = writeBootFile(boot, n, []byte(v)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = f.WriteAt(testBootloader(), BootloaderOffset); e != nil {
		t.Fatal(e)
	}
	return f, size
}
func loaderRecord(data []byte, choice string) BootloaderRecord {
	h := sha256.Sum256(data)
	return BootloaderRecord{Choice: choice, Repository: NewBootloaderRepo, Tag: "20261113-0351", Version: "2026.10", SHA256: hex.EncodeToString(h[:])}
}
func TestBootloaderApplyHistoryAndSDGate(t *testing.T) {
	f, size := loaderCard(t, nil)
	build, tab, e := CardBuildIdentity(f, size)
	if e != nil {
		t.Fatal(e)
	}
	data := testBootloader()
	record := loaderRecord(data, "new")
	for n := 0; n < 7; n++ {
		if e = ApplyBootloader(f, size, data, record, build, t.TempDir()); e != nil {
			t.Fatalf("iteration %d: %v", n, e)
		}
	}
	boot, e := OpenBoot(f, tab, true)
	if e != nil {
		t.Fatal(e)
	}
	s, e := ReadCardSetup(boot)
	if e != nil {
		t.Fatal(e)
	}
	if s.Bootloader == nil || s.Bootloader.SHA256 != record.SHA256 || s.Bootloader.Repository != NewBootloaderRepo || s.Bootloader.Tag != record.Tag || s.Bootloader.Time == "" || len(s.BootloaderHistory) != 5 {
		t.Fatalf("history %+v", s)
	}
	keep, e := ReadFile(boot, "/keep.txt", 100)
	if e != nil || string(keep) != "preserve this" {
		t.Fatal("unrelated file changed", e)
	}
	f2, size2 := loaderCard(t, map[string]string{"andr36oid-datacard.txt": "emmc=1\n"})
	if e = ApplyBootloader(f2, size2, data, record, build, t.TempDir()); e == nil || e.Error() != BootloaderOnlySD {
		t.Fatal("eMMC data card was not refused", e)
	}
	if e = ApplyBootloader(f, size, data, record, "other|build", t.TempDir()); e == nil {
		t.Fatal("changed card accepted")
	}
}
func TestBootloaderResolveOfflineFallsBack(t *testing.T) {
	data := testBootloader()
	raw := filepath.Join(t.TempDir(), "image.img")
	f, e := os.Create(raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(BootloaderOffset + BootloaderSize); e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt(data, BootloaderOffset); e != nil {
		t.Fatal(e)
	}
	f.Close()
	im := &Image{Path: raw, Size: BootloaderOffset + BootloaderSize}
	old := hotfixHTTP
	hotfixHTTP = &http.Client{Transport: loaderTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}
	defer func() { hotfixHTTP = old }()
	got, out, e := ResolveBootloader(context.Background(), BootloaderChoice{Choice: "new"}, im, t.TempDir())
	if e != nil || !bytes.Equal(got, data) || out.Warning != BootloaderFetchFailed || out.Record.Choice != "image" {
		t.Fatal(out, e)
	}
	got, out, e = ResolveBootloader(context.Background(), BootloaderChoice{Choice: "standard"}, nil, t.TempDir())
	if e != nil || got != nil || out.Record != nil || out.Warning != BootloaderFetchFailed {
		t.Fatal("existing card fallback", out, e)
	}
	got, out, e = ResolveBootloader(context.Background(), BootloaderChoice{Choice: "file", File: filepath.Join(t.TempDir(), "missing")}, im, t.TempDir())
	if e != nil || !bytes.Equal(got, data) || out.Warning != BootloaderCheckFailed {
		t.Fatal("file fallback", out, e)
	}
}

type loaderBoundedDisk struct {
	*os.File
	writes int
}

func (d *loaderBoundedDisk) WriteAt(b []byte, off int64) (int, error) {
	if off < BootloaderOffset || off+int64(len(b)) > BootloaderOffset+BootloaderSize {
		return 0, errors.New("write outside U-Boot slot")
	}
	d.writes++
	return d.File.WriteAt(b, off)
}
func TestBootloaderEveryWriteWithinSlot(t *testing.T) {
	f, e := os.CreateTemp(t.TempDir(), "slot")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = f.Truncate(32 << 20); e != nil {
		t.Fatal(e)
	}
	d := &loaderBoundedDisk{File: f}
	if e = WriteBootloaderSlot(d, testBootloader(), nil); e != nil {
		t.Fatal(e)
	}
	if d.writes != 4 {
		t.Fatal(d.writes)
	}
	bad := testBootloader()
	bad[0] = 0
	d.writes = 0
	if e = WriteBootloaderSlot(d, bad, nil); e == nil || d.writes != 0 {
		t.Fatal("invalid file wrote card")
	}
}
