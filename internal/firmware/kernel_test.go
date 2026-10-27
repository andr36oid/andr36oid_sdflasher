package firmware

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

const kernelBuild = "v2026-10-18-release|eng.kenny.20261018.0100"

func testKernelBundle(files map[string][]byte) *KernelBundle {
	bundle := &KernelBundle{Manifest: KernelManifest{Format: 1, Version: "v1.1.0", CompatibleBuilds: []string{kernelBuild}}, Files: files}
	for name, data := range files {
		sum := sha256.Sum256(data)
		bundle.Manifest.Files = append(bundle.Manifest.Files, KernelFile{Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])})
	}
	return bundle
}
func kernelZip(t *testing.T, bundle *KernelBundle, extra map[string][]byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "kernel.zip")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	manifest, _ := json.Marshal(bundle.Manifest)
	files := map[string][]byte{"manifest.json": manifest}
	for name, data := range bundle.Files {
		files[name] = data
	}
	for name, data := range extra {
		files[name] = data
	}
	for name, data := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "/") {
			continue
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return name
}
func TestReadKernelArchive(t *testing.T) {
	bundle := testKernelBundle(map[string][]byte{"Image": []byte("kernel"), "rg351mp-kernel.dtb": []byte("dtb"), "rg351mp-uboot.dtb": []byte("uboot"), "boot.ini": []byte("script")})
	got, err := ReadKernelArchive(kernelZip(t, bundle, nil))
	if err != nil || len(got.Files) != 4 {
		t.Fatalf("bundle=%+v err=%v", got, err)
	}
	for name, data := range bundle.Files {
		if !bytes.Equal(got.Files[name], data) {
			t.Fatalf("changed %s", name)
		}
	}
	for _, name := range []string{"../Image", "/Image", `..\Image`, "BOOT/Image", "other.txt", "boot.ini-android", ".noroms", "rk3326-r36s-android.dtb", "Image/", "IMAGE"} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadKernelArchive(kernelZip(t, bundle, map[string][]byte{name: []byte("bad")})); err == nil {
				t.Fatal("accepted unexpected path")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*KernelBundle)
	}{
		{"format", func(b *KernelBundle) { b.Manifest.Format = 2 }},
		{"version", func(b *KernelBundle) { b.Manifest.Version = "bad" }},
		{"no image", func(b *KernelBundle) { b.Manifest.Files = nil }},
		{"sha", func(b *KernelBundle) { b.Manifest.Files[0].SHA256 = strings.Repeat("0", 64) }},
		{"size", func(b *KernelBundle) { b.Manifest.Files[0].Size++ }},
		{"oversize", func(b *KernelBundle) { b.Manifest.Files[0].Size = 96<<20 + 1 }},
		{"duplicate", func(b *KernelBundle) { b.Manifest.Files = append(b.Manifest.Files, b.Manifest.Files[0]) }},
		{"build", func(b *KernelBundle) { b.Manifest.CompatibleBuilds = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testKernelBundle(map[string][]byte{"Image": []byte("kernel")})
			tc.change(b)
			if _, err := ReadKernelArchive(kernelZip(t, b, nil)); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	// Reject the ZIP's expanded size even if its compressed representation is tiny.
	tooBig := testKernelBundle(map[string][]byte{"Image": []byte("kernel"), "rg351mp-kernel.dtb": make([]byte, (4<<20)+1)})
	if _, err := ReadKernelArchive(kernelZip(t, tooBig, nil)); err == nil {
		t.Fatal("accepted expanded oversize file")
	}
	path := filepath.Join(t.TempDir(), "oversize.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(KernelArchiveLimit + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = ReadKernelArchive(path); err == nil {
		t.Fatal("accepted oversized archive")
	}
	// ZIP directories and symlinks cannot be boot files.
	for _, mode := range []os.FileMode{os.ModeSymlink | 0777, os.ModeDir | 0755} {
		var raw bytes.Buffer
		z := zip.NewWriter(&raw)
		h := &zip.FileHeader{Name: "Image"}
		h.SetMode(mode)
		w, _ := z.CreateHeader(h)
		w.Write([]byte("data"))
		w, _ = z.Create("manifest.json")
		w.Write([]byte(`{}`))
		z.Close()
		path := filepath.Join(t.TempDir(), "special.zip")
		os.WriteFile(path, raw.Bytes(), 0600)
		if _, err = ReadKernelArchive(path); err == nil {
			t.Fatal("accepted special ZIP entry")
		}
	}
}
func kernelDisk(t *testing.T, files map[string]string) (*boundedHotfixDisk, layout.Table) {
	t.Helper()
	// diskfs truncates long extensions to three characters without assigning a
	// unique short name. Create the long script last, then give its FAT entry a
	// proper unique alias, as on real cards created by mkfs/mtools. Otherwise
	// boot.ini and boot.ini-android both get BOOT.INI and cannot be tested apart.
	initial := map[string]string{}
	for name, data := range files {
		if name != "boot.ini-android" {
			initial[name] = data
		}
	}
	f, tab := bootOnlyImage(t, initial)
	if data, ok := files["boot.ini-android"]; ok {
		p, _ := tab.Find("BOOT")
		fs, err := fat32.Read(file.New(f, false), p.Size(), p.Offset(), 512)
		if err != nil {
			t.Fatal(err)
		}
		w, err := fs.OpenFile("boot.ini-android", os.O_CREATE|os.O_RDWR)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		raw := make([]byte, p.Size())
		if _, err = f.ReadAt(raw, p.Offset()); err != nil {
			t.Fatal(err)
		}
		fixed := false
		for off := 64; off < len(raw); off += 32 {
			if string(raw[off:off+11]) != "BOOT    INI" || raw[off-32+11] != 0x0f || raw[off-32+20] != '-' {
				continue
			}
			alias := []byte("BOOTAN~1INI")
			copy(raw[off:off+11], alias)
			var checksum byte
			for _, b := range alias {
				checksum = (checksum >> 1) | (checksum << 7)
				checksum += b
			}
			for slot := off - 32; slot >= 0 && raw[slot+11] == 0x0f; slot -= 32 {
				raw[slot+13] = checksum
			}
			fixed = true
		}
		if !fixed {
			t.Fatal("long boot script alias not found")
		}
		if _, err = f.WriteAt(raw, p.Offset()); err != nil {
			t.Fatal(err)
		}
	}
	head, tail, err := tab.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(head, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt(tail, tab.Size-int64(len(tail))); err != nil {
		t.Fatal(err)
	}
	p, _ := tab.Find("BOOT")
	return &boundedHotfixDisk{File: f, start: p.Offset(), end: p.Offset() + p.Size()}, tab
}
func TestKernelIdentitySources(t *testing.T) {
	for _, tc := range []struct {
		name, info, marker, want string
		bad                      bool
	}{
		{name: "unknown", info: testBuildInfo},
		{name: "build property", info: testBuildInfo + "kernel_version=1.0.0\n", want: "1.0.0"},
		{name: "unreliable property", info: testBuildInfo + "kernel_version=Linux 4.4.194\n"},
		{name: "marker wins", info: testBuildInfo + "kernel_version=1.0.0\n", marker: `{"format":1,"build":"` + kernelBuild + `","version":"v1.1.0"}`, want: "v1.1.0"},
		{name: "other build marker", info: testBuildInfo, marker: `{"format":1,"build":"other|build","version":"v9.0.0"}`},
		{name: "corrupt marker", info: testBuildInfo, marker: `bad`, bad: true},
		{name: "no build", info: "format=1\n", bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{BuildInfoFile: tc.info}
			if tc.marker != "" {
				files[KernelMarkerFile] = tc.marker
			}
			d, tab := kernelDisk(t, files)
			state, _, err := CardKernelIdentity(d, tab.Size)
			if (err != nil) != tc.bad || state.Version != tc.want {
				t.Fatalf("state %+v err=%v", state, err)
			}
			if !tc.bad && state.Build != kernelBuild {
				t.Fatalf("build %q", state.Build)
			}
		})
	}
}
func TestApplyKernelUpdatePreservesBOOTAndIsRepeatable(t *testing.T) {
	files := map[string]string{BuildInfoFile: testBuildInfo, "Image": strings.Repeat("old", 100), "rg351mp-kernel.dtb": "long old dtb", "keep.txt": "user file", "boot.ini": "load mmc 1:1 ${fdt_addr_r} selected-android.dtb\nold script\n", "boot.ini-android": "original script", "selected-android.dtb": "selected panel", ".noroms": "", "andr36oid-profile.txt": "Panels/Panel4\n"}
	d, tab := kernelDisk(t, files)
	p, _ := tab.Find("BOOT")
	before := make([]byte, tab.Size)
	if _, err := d.File.ReadAt(before, 0); err != nil {
		t.Fatal(err)
	}
	bundle := testKernelBundle(map[string][]byte{"Image": []byte("new kernel"), "rg351mp-kernel.dtb": []byte("new"), "boot.ini": []byte("load mmc 1:1 ${fdt_addr_r} selected-android.dtb\nnew script\n")})
	for i := 0; i < 2; i++ {
		if err := ApplyKernelUpdate(d, tab.Size, bundle, KernelIdentity{Build: kernelBuild}, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		boot, err := OpenBoot(d, tab, true)
		if err != nil {
			t.Fatal(err)
		}
		for name, old := range files {
			want := []byte(old)
			if data, ok := bundle.Files[name]; ok {
				want = data
			}
			got, err := ReadFile(boot, "/"+name, 1<<20)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("%s: %q want %q err=%v", name, got, want, err)
			}
		}
		state, err := ReadKernelIdentity(boot)
		if err != nil || state.Version != bundle.Manifest.Version {
			t.Fatalf("marker %+v err=%v", state, err)
		}
	}
	after := make([]byte, tab.Size)
	d.File.ReadAt(after, 0)
	if !bytes.Equal(before[:p.Offset()], after[:p.Offset()]) || !bytes.Equal(before[p.Offset()+p.Size():], after[p.Offset()+p.Size():]) {
		t.Fatal("wrote outside BOOT")
	}
	if d.writes == 0 {
		t.Fatal("no writes")
	}
}

type lostKernelWrites struct{ *boundedHotfixDisk }

func (d lostKernelWrites) WriteAt(b []byte, off int64) (int, error) { return len(b), nil }
func TestApplyKernelUpdateRejectsFailuresBeforeMarker(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*KernelBundle, *KernelIdentity)
	}{
		{"changed card", func(b *KernelBundle, e *KernelIdentity) { e.Build = "other|build" }},
		{"incompatible", func(b *KernelBundle, e *KernelIdentity) { b.Manifest.CompatibleBuilds = []string{"other|build"} }},
		{"checksum", func(b *KernelBundle, e *KernelIdentity) { b.Files["Image"] = []byte("bad") }},
		{"downgrade", func(b *KernelBundle, e *KernelIdentity) { b.Manifest.Version = "v0.9.0" }},
		{"stale kernel", func(b *KernelBundle, e *KernelIdentity) { e.Version = "v0.9.0" }},
		{"dtb selection", func(b *KernelBundle, e *KernelIdentity) {
			n := testKernelBundle(map[string][]byte{"Image": []byte("new"), "boot.ini": []byte("load mmc 1:1 ${fdt_addr_r} wrong.dtb\n")})
			*b = *n
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, tab := kernelDisk(t, map[string]string{BuildInfoFile: testBuildInfo + "kernel_version=v1.0.0\n", "Image": "old", "boot.ini": "load mmc 1:1 ${fdt_addr_r} selected.dtb\n"})
			bundle := testKernelBundle(map[string][]byte{"Image": []byte("new")})
			expected := KernelIdentity{Build: kernelBuild, Version: "v1.0.0"}
			tc.change(bundle, &expected)
			if err := ApplyKernelUpdate(d, tab.Size, bundle, expected, t.TempDir()); err == nil || d.writes != 0 {
				t.Fatalf("err=%v writes=%d", err, d.writes)
			}
		})
	}
	d, tab := kernelDisk(t, map[string]string{BuildInfoFile: testBuildInfo, "Image": "old"})
	bundle := testKernelBundle(map[string][]byte{"Image": []byte("new")})
	if err := ApplyKernelUpdate(lostKernelWrites{d}, tab.Size, bundle, KernelIdentity{Build: kernelBuild}, t.TempDir()); err == nil {
		t.Fatal("ignored failed raw readback")
	}
	boot, err := OpenBoot(d, tab, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReadFile(boot, "/"+KernelMarkerFile, 4096); err == nil {
		t.Fatal("recorded kernel before files verified")
	}
}

// Ensure errors from the underlying media are returned rather than success.
type failedKernelSync struct{ *boundedHotfixDisk }

func (d failedKernelSync) Sync() error { return io.ErrClosedPipe }
func TestApplyKernelUpdateSyncFailure(t *testing.T) {
	d, tab := kernelDisk(t, map[string]string{BuildInfoFile: testBuildInfo, "Image": "old"})
	bundle := testKernelBundle(map[string][]byte{"Image": []byte("new")})
	if err := ApplyKernelUpdate(failedKernelSync{d}, tab.Size, bundle, KernelIdentity{Build: kernelBuild}, t.TempDir()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err %v", err)
	}
	boot, _ := OpenBoot(d, tab, true)
	if _, err := ReadFile(boot, "/"+KernelMarkerFile, 4096); err == nil {
		t.Fatal("recorded marker after failed sync")
	}
}
