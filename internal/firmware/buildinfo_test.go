package firmware

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

const testBuildInfo = "format=1\r\nsystem_fs=erofs\nvendor_fs=ext4\nro.andr36oid.version=v2026-10-18-release\n" +
	"ro.product.build.version.incremental=eng.kenny.20261018.0100\nro.lineage.device=r36s\n" +
	"ro.product.system.device=r36s\nro.treble.enabled=true\nvendor_first_stage_mount=1\nvendor_vintf=1\n"

func TestParseBuildInfo(t *testing.T) {
	m, ok := ParseBuildInfo([]byte(testBuildInfo))
	if !ok || m["system_fs"] != "erofs" || m["ro.andr36oid.version"] != "v2026-10-18-release" {
		t.Fatalf("parsed %v %v", ok, m)
	}
	for _, b := range []string{"", "system_fs=erofs\n", "format=2\nro.treble.enabled=true\n"} {
		if _, ok := ParseBuildInfo([]byte(b)); ok {
			t.Errorf("%q accepted", b)
		}
	}
}

// bootOnlyImage has a FAT BOOT holding the given files, and zeros where system and
// vendor are: anything that tries to read those as ext4 fails.
func bootOnlyImage(t *testing.T, files map[string]string) (*os.File, layout.Table) {
	t.Helper()
	const bootStart, bootSize = 2048, 32 << 20
	const partSize = 4 << 20
	f, e := os.Create(filepath.Join(t.TempDir(), "test.img"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	vendorStart := int64(bootStart + bootSize/512)
	systemStart := vendorStart + partSize/512
	size := (systemStart + partSize/512 + 2048) * 512
	if e := f.Truncate(size); e != nil {
		t.Fatal(e)
	}
	part := func(n int, name string, start, bytes int64) layout.Partition {
		return layout.Partition{Number: n, Name: name, Start: uint64(start), End: uint64(start + bytes/512 - 1), Type: layout.LinuxType, ID: layout.GUID()}
	}
	tab := layout.Table{Size: size, GPT: true, Parts: []layout.Partition{
		part(1, "BOOT", bootStart, bootSize), part(2, "vendor", vendorStart, partSize), part(3, "system", systemStart, partSize),
	}}
	fs, e := fat32.Create(file.New(f, false), bootSize, bootStart*512, 512, "BOOT", true)
	if e != nil {
		t.Fatal(e)
	}
	for n, d := range files {
		w, e := fs.OpenFile(n, os.O_CREATE|os.O_RDWR)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte(d))
		w.Close()
	}
	return f, tab
}

func TestReleaseFactsFromBootInfo(t *testing.T) {
	f, tab := bootOnlyImage(t, map[string]string{BuildInfoFile: testBuildInfo})
	boot, e := OpenBoot(f, tab, true)
	if e != nil {
		t.Fatal(e)
	}
	props, e := releaseFacts(boot)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(props), "ro.treble.enabled=true") || !strings.Contains(string(props), "ro.andr36oid.version=v2026-10-18-release") {
		t.Errorf("props %q", props)
	}
}

func TestReleaseFactsBootInfoChecksVendor(t *testing.T) {
	info := strings.Replace(testBuildInfo, "vendor_vintf=1", "vendor_vintf=0", 1)
	f, tab := bootOnlyImage(t, map[string]string{BuildInfoFile: info})
	boot, e := OpenBoot(f, tab, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := releaseFacts(boot); e == nil || !strings.Contains(e.Error(), "manifest") {
		t.Errorf("error %v", e)
	}
}

// Without the file, or with a format this flasher doesn't know, the image is not
// compatible: older releases are refused instead of read as ext4.
func TestReleaseFactsWithoutBootInfoIncompatible(t *testing.T) {
	for _, files := range []map[string]string{{}, {BuildInfoFile: strings.Replace(testBuildInfo, "format=1", "format=2", 1)}} {
		f, tab := bootOnlyImage(t, files)
		boot, e := OpenBoot(f, tab, true)
		if e != nil {
			t.Fatal(e)
		}
		if _, e := releaseFacts(boot); !errors.Is(e, ErrNoBuildInfo) {
			t.Errorf("%v: error %v", files, e)
		}
	}
}

func TestPrepareBootKeepsBuildInfo(t *testing.T) {
	dir := t.TempDir()
	im := fakeImage(t, dir)
	img, e := os.OpenFile(im.Path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	p, _ := im.Table.Find("BOOT")
	fs, e := fat32.Read(file.New(img, false), p.Size(), p.Offset(), 512)
	if e != nil {
		t.Fatal(e)
	}
	w, e := fs.OpenFile(BuildInfoFile, os.O_CREATE|os.O_RDWR)
	if e != nil {
		t.Fatal(e)
	}
	w.Write([]byte(testBuildInfo))
	w.Close()
	img.Close()
	for _, update := range []bool{false, true} {
		out, e := PrepareBoot(im, "Panels/Panel4", false, update, "", dir)
		if e != nil {
			t.Fatal(e)
		}
		f, e := os.Open(out)
		if e != nil {
			t.Fatal(e)
		}
		st, _ := f.Stat()
		bfs, e := fat32.Read(file.New(f, true), st.Size(), 0, 512)
		if e != nil {
			t.Fatal(e)
		}
		if got := BuildIdentity(bfs); got != "v2026-10-18-release|eng.kenny.20261018.0100" {
			t.Errorf("update %v: build identity %q", update, got)
		}
		for _, n := range []string{"/rk3326-r36s-android.dtb", "/boot.ini-android", "/Panels/Panel4/rk3326-r36s-android.dtb"} {
			if _, e := ReadFile(bfs, n, 4<<20); e != nil {
				t.Errorf("update %v: %s is gone: %v", update, n, e)
			}
		}
		f.Close()
	}
}

// An update writes the user's hotfixes back into BOOT's hotfix folder.
func TestPrepareBootKeepsHotfixes(t *testing.T) {
	dir := t.TempDir()
	im := fakeImage(t, dir)
	keep := map[string][]byte{"hotfix/fix-audio.fix.zip": []byte("zip one"), "hotfix/b.fix.zip": []byte("zip two")}
	for _, update := range []bool{false, true} {
		out, e := PrepareBoot(im, "Panels/Panel4", false, update, "", dir, BootOptions{Keep: keep})
		if e != nil {
			t.Fatal(e)
		}
		f, e := os.Open(out)
		if e != nil {
			t.Fatal(e)
		}
		st, _ := f.Stat()
		bfs, e := fat32.Read(file.New(f, true), st.Size(), 0, 512)
		if e != nil {
			t.Fatal(e)
		}
		for name, want := range keep {
			got, e := ReadFile(bfs, "/"+name, 1<<20)
			if update && (e != nil || string(got) != string(want)) {
				t.Errorf("update: %s is %q, %v", name, got, e)
			}
			if !update && e == nil {
				t.Errorf("fresh install wrote %s", name)
			}
		}
		f.Close()
	}
}
