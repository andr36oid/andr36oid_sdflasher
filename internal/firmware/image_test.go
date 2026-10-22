package firmware

import (
	"archive/zip"
	"context"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseImage(t *testing.T) {
	p := os.Getenv("SDFLASHER_TEST_IMAGE")
	if p == "" {
		t.Skip("set SDFLASHER_TEST_IMAGE for release-image integration")
	}
	im, e := Inspect(context.Background(), p, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(im.Profiles) < 8 {
		t.Fatal("missing panel profiles")
	}
	for _, p := range im.Profiles {
		if p.ID != "Devices/R46H" {
			continue
		}
		charge := 0
		for _, n := range p.BootFiles {
			if filepath.Base(n) == "battery_0.bmp" || filepath.Base(n) == "battery_fail.bmp" {
				charge++
			}
		}
		if charge != 2 {
			t.Fatal("R46H charge screens not copied", p.BootFiles)
		}
	}
	for _, p := range im.Profiles {
		if p.ID == "Panels/Panel4" {
			for _, update := range []bool{false, true} {
				boot, e := PrepareBoot(im, p.ID, true, update, "", t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				st, e := os.Stat(boot)
				if e != nil || st.Size() != 256<<20 {
					t.Fatal("invalid prepared boot")
				}
			}
			return
		}
	}
	t.Fatal("Panel 4 missing")
}
func TestLegacyImageRejected(t *testing.T) {
	p := os.Getenv("SDFLASHER_TEST_LEGACY")
	if p == "" {
		t.Skip("set SDFLASHER_TEST_LEGACY")
	}
	if _, e := Inspect(context.Background(), p, nil); e == nil {
		t.Fatal("accepted non-Treble image")
	}
}
func TestArchiveRejectsAmbiguousAndDamagedImages(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "two.zip")
	f, _ := os.Create(p)
	w := zip.NewWriter(f)
	for _, n := range []string{"a.img", "b.img"} {
		x, _ := w.Create(n)
		x.Write([]byte("not a disk"))
	}
	w.Close()
	f.Close()
	if _, e := Stage(context.Background(), p, dir, nil); e == nil {
		t.Fatal("accepted multiple images")
	}
	p = filepath.Join(dir, "broken.zip")
	f, _ = os.Create(p)
	w = zip.NewWriter(f)
	x, _ := w.CreateHeader(&zip.FileHeader{Name: "disk.img", Method: zip.Store})
	x.Write([]byte("image payload"))
	w.Close()
	f.Close()
	b, _ := os.ReadFile(p)
	b[38] ^= 1
	os.WriteFile(p, b, 0600)
	if _, e := Stage(context.Background(), p, dir, nil); e == nil {
		t.Fatal("accepted corrupt ZIP")
	}
}
func TestUserdataSizeFile(t *testing.T) {
	p := os.Getenv("SDFLASHER_TEST_IMAGE")
	if p == "" {
		t.Skip("set SDFLASHER_TEST_IMAGE for release-image integration")
	}
	im, e := Inspect(context.Background(), p, nil)
	if e != nil {
		t.Fatal(e)
	}
	read := func(gib int, noROMs, update bool) (string, bool) {
		boot, e := PrepareBoot(im, "Panels/Panel4", noROMs, update, "", t.TempDir(), BootOptions{UserdataGiB: gib})
		if e != nil {
			t.Fatal(e)
		}
		f, e := os.Open(boot)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		st, _ := f.Stat()
		fs, e := fat32.Read(file.New(f, true), st.Size(), 0, 512)
		if e != nil {
			t.Fatal(e)
		}
		b, e := ReadFile(fs, "/userdata-size.txt", 64)
		return string(b), e == nil
	}
	if s, ok := read(32, false, false); !ok || s != "32\n" {
		t.Errorf("install with 32 GiB wrote %q %v", s, ok)
	}
	for _, c := range []struct {
		gib            int
		noROMs, update bool
	}{{0, false, false}, {32, true, false}} {
		if s, ok := read(c.gib, c.noROMs, c.update); ok {
			t.Errorf("%+v wrote %q", c, s)
		}
	}
}

func TestProfileListFromImage(t *testing.T) {
	list := parseProfileList([]byte("# folder|console|name|flags\n" +
		"Experimental/EE-G80-Panel8|G80|G80C — Panel 8|experimental,also=R36S V12 clone\n" +
		"Experimental/EE-V12-Var1-Panel8|R36S V12 clone|R36S V12 clone — Variant 1, Panel 8|\n" +
		"broken line\n"))
	g80info := profileInfoFor(list, "Experimental/EE-G80-Panel8")
	g80 := Profile{ID: "Experimental/EE-G80-Panel8", Console: g80info.Console, AlsoFor: g80info.AlsoFor}
	if !g80info.Experimental || !g80.For("G80") || !g80.For("R36S V12 clone") || g80.For("K36 / HG36") {
		t.Fatalf("EE-G80-Panel8 read as %+v", g80info)
	}
	if v12 := profileInfoFor(list, "Experimental/EE-V12-Var1-Panel8"); v12.Experimental || v12.Name != "R36S V12 clone — Variant 1, Panel 8" {
		t.Fatalf("V12 variant 1 read as %+v", v12)
	}
	// an image from before the list: the folder's name, experimental by folder
	if old := profileInfoFor(nil, "Experimental/EE-K36-Panel1"); old.Name != "EE K36 Panel1" || !old.Experimental {
		t.Fatalf("old image profile read as %+v", old)
	}
	if old := profileInfoFor(nil, "Devices/D-28S"); old.Name != "D 28S" || old.Experimental {
		t.Fatalf("old image device read as %+v", old)
	}
}

// An update writes the user's Kernel Tweaks back; a fresh install starts without them.
func TestPrepareBootKeepsKernelTweaks(t *testing.T) {
	p := os.Getenv("SDFLASHER_TEST_IMAGE")
	if p == "" {
		t.Skip("set SDFLASHER_TEST_IMAGE for release-image integration")
	}
	im, e := Inspect(context.Background(), p, nil)
	if e != nil {
		t.Fatal(e)
	}
	tweaks := []byte("kt_args=panel_simple.extra_rates=30,144\n")
	for _, update := range []bool{true, false} {
		boot, e := PrepareBoot(im, im.Profiles[0].ID, false, update, "", t.TempDir(),
			BootOptions{Keep: map[string][]byte{"kernel-tweaks.txt": tweaks}})
		if e != nil {
			t.Fatal(e)
		}
		f, e := os.Open(boot)
		if e != nil {
			t.Fatal(e)
		}
		st, _ := f.Stat()
		fs, e := fat32.Read(file.New(f, true), st.Size(), 0, 512)
		if e != nil {
			t.Fatal(e)
		}
		b, e := ReadFile(fs, "/kernel-tweaks.txt", 4096)
		f.Close()
		if update && (e != nil || string(b) != string(tweaks)) {
			t.Errorf("update: kernel-tweaks.txt is %q, %v", b, e)
		}
		if !update && e == nil {
			t.Errorf("install kept kernel-tweaks.txt")
		}
	}
}
