package firmware

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDTBFreshNewProfile(t *testing.T) {
	p := packFixture(t)
	f, size, build := dtbCard(t)
	_, tab, _ := CardBuildIdentity(f, size)
	boot, _ := dtbWritable(f, size)
	writeBootFile(boot, "boot.ini-android", []byte("load mmc 1:1 ${fdt_addr_r} rk3326-r36s-android.dtb\nload mmc 1:1 ${ramdisk_addr_r} ramdisk.uimg\n"))
	im := &Image{Path: f.Name(), Size: size, Table: tab, BuildIdentity: build}
	updated, e := ImageWithDTBPack(im, p, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if len(updated.Profiles) != 2 {
		t.Fatal("new profile unavailable", updated.Profiles)
	}
	prepared, e := PrepareBoot(updated, "Panels/Panel3", false, true, "", t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer os.Remove(prepared)
	g, _ := os.Open(prepared)
	defer g.Close()
	part, _ := tab.Find("BOOT")
	out, e := fat32.Read(file.New(g, true), part.Size(), 0, 512)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range DTBRootFiles {
		if !bytes.Equal(dtbRead(out, n, 4<<20), p.Files["4.19/Panels/Panel3/"+n]) {
			t.Fatal("new selected profile root", n)
		}
	}
}
func TestDTBPackFourActualCandidateBOOTs(t *testing.T) {
	root := os.Getenv("A36_DTB_BOOT_ROOT")
	archive := os.Getenv("A36_DTB_PACK")
	if root == "" || archive == "" {
		t.Skip("set candidate BOOT fixtures")
	}
	p, e := ReadDTBPack(archive)
	if e != nil {
		t.Fatal(e)
	}
	for _, variant := range []string{"4.19-gles", "4.19-vulkan", "5.10-gles", "5.10-vulkan"} {
		t.Run(variant, func(t *testing.T) {
			const size = 512 << 20
			f, e := os.CreateTemp(t.TempDir(), "card")
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			f.Truncate(size)
			part := func(n int, name string, off, sz int64) layout.Partition {
				return layout.Partition{Number: n, Name: name, Start: uint64(off / 512), End: uint64((off+sz)/512 - 1), Type: layout.LinuxType, ID: layout.GUID()}
			}
			tab := layout.Table{Size: size, GPT: true, Parts: []layout.Partition{part(1, "BOOT", 16<<20, 256<<20), part(2, "vendor", 272<<20, 16<<20), part(3, "system", 288<<20, 16<<20)}}
			head, tail, e := tab.Encode()
			if e != nil {
				t.Fatal(e)
			}
			f.WriteAt(head, 0)
			f.WriteAt(tail, size-int64(len(tail)))
			fat, e := os.Open(filepath.Join(root, variant+".fat"))
			if e != nil {
				t.Fatal(e)
			}
			defer fat.Close()
			f.Seek(16<<20, io.SeekStart)
			if _, e = io.Copy(f, fat); e != nil {
				t.Fatal(e)
			}
			build, _, e := CardBuildIdentity(f, size)
			if e != nil {
				t.Fatal(e)
			}
			sum := func() string {
				h := sha256.New()
				io.Copy(h, io.NewSectionReader(f, 16<<20, 256<<20))
				return hex.EncodeToString(h.Sum(nil))
			}
			before := sum()
			im := &Image{Path: f.Name(), Size: size, Table: tab, BuildIdentity: build}
			updated, e := ImageWithDTBPack(im, p, t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			if len(updated.Profiles) != 56 {
				t.Fatal("profile matrix count", len(updated.Profiles))
			}
			prepared, e := PrepareBoot(updated, "Experimental/R36-Ultra", false, true, "", t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			defer os.Remove(prepared)
			if sum() != before {
				t.Fatal("source image changed")
			}
			g, e := os.Open(prepared)
			if e != nil {
				t.Fatal(e)
			}
			defer g.Close()
			boot, e := fat32.Read(file.New(g, true), 256<<20, 0, 512)
			if e != nil {
				t.Fatal(e)
			}
			kernel := strings.Split(variant, "-")[0]
			for _, n := range DTBRootFiles {
				if !bytes.Equal(dtbRead(boot, n, 4<<20), p.Files[kernel+"/Experimental/R36-Ultra/"+n]) {
					t.Fatal("packed selected root differs", n)
				}
			}
			set := p.Manifest.Lines[kernel].PictureSets["Experimental/R36-Ultra"]
			for _, n := range PictureFiles {
				if !bytes.Equal(dtbRead(boot, n, 16<<20), dtbRead(boot, "Pictures/"+set+"/"+n, 16<<20)) {
					t.Fatal("picture differs", n)
				}
			}
			g.Seek(0, io.SeekStart)
			f.Seek(16<<20, io.SeekStart)
			if _, e = io.Copy(f, g); e != nil {
				t.Fatal(e)
			}
			result, e := ApplyDTBPack(f, size, p, build, t.TempDir(), DTBOptions{ScreenAccepted: true})
			if e != nil {
				t.Fatal("fresh flash post-write recording", e)
			}
			if result.Setup.Profile != "Experimental/R36-Ultra" || result.Setup.Revision != 1 {
				t.Fatal(result.Setup)
			}
			t.Logf("56 profiles, selected Ultra seven roots + pictures, immutable source and post-write apply PASS; source BOOT %s", before)
		})
	}
}
