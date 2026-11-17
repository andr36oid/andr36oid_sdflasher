package flash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

// The console's own plan_userdata, run by sh, has to give the same userdata end as
// PreparedLayout for every card size and wish.
func TestPreparedLayoutFollowsTheConsole(t *testing.T) {
	script, e := os.ReadFile("/andr36oid/src/device/gameconsole/common/firstresize/andr36oid-firstresize.sh")
	if e != nil {
		t.Skip("console source not on this host")
	}
	_, fn, ok := strings.Cut(string(script), "# BEGIN plan_userdata")
	fn, _, ok2 := strings.Cut(fn, "# END plan_userdata")
	if !ok || !ok2 {
		t.Fatal("plan_userdata markers missing")
	}
	apps := int64(2) << 30
	for _, card := range []int64{15931539456, 31914983424, 63864569856, 127865454592, 256060514304, 512110190592} {
		for _, wish := range []string{"", "all", "16", "24", "32", "100"} {
			for _, guest := range []int64{0, 5 << 30} {
				want, noROMs := 0, wish == "all"
				if !noROMs && wish != "" {
					fmt.Sscan(wish, &want)
				}
				p, e := PreparedLayout(card, apps, want, noROMs, guest)
				if e != nil {
					if guest > 0 && card < 20e9 {
						continue
					}
					t.Fatal(card, wish, guest, e)
				}
				// with dArkOSen the console is asked about the card that ends before it
				sectors := card / 512
				if guest > 0 {
					if p.Guest.First%2048 != 0 || p.Guest.Last != card/512-34 || p.Guest.Bytes() < guest {
						t.Fatal("dArkOSen partition", p.Guest)
					}
					sectors = p.Guest.First + 33
				}
				out, e := exec.Command("sh", "-c", fn+fmt.Sprintf("\nNOROMS=; plan_userdata %d '%s'; echo $USERDATA_END ${NOROMS:-0}", sectors, wish)).Output()
				if e != nil {
					t.Fatal(e)
				}
				var end, no int64
				fmt.Sscan(string(out), &end, &no)
				if p.Apps.Last != end || (p.ROMs.Bytes() == 0) != (no == 1) {
					t.Fatalf("card %d wish %q guest %d: flasher ends userdata at %d (games %d), console at %d (no games %d)", card, wish, guest, p.Apps.Last, p.ROMs.Bytes(), end, no)
				}
				// apps_move: the apps right behind userdata, on a megabyte
				if p.Apps.First != (end+1-apps/512)/2048*2048 || p.Userdata.Last+1 != p.Apps.First || p.Userdata.First != 5701632 {
					t.Fatal("apps placement", p)
				}
				if p.ROMs.Bytes() > 0 && (p.ROMs.First != end+1 || p.ROMs.First%2048 != 0 || (guest > 0 && p.ROMs.Last+1 != p.Guest.First) || (guest == 0 && p.ROMs.Last != card/512-34)) {
					t.Fatal("games placement", p)
				}
			}
		}
	}
}

func preparedSample(t *testing.T) (*firmware.Image, Card, *os.File, string, *Guest) {
	t.Helper()
	im, _, _, boot := sample(t)
	old := preparedStart
	preparedStart = 4096
	t.Cleanup(func() { preparedStart = old })
	im.Table.Parts = append(im.Table.Parts, layout.Partition{Number: 12, Name: AutoAPK, Start: 4096, End: 8191, Type: layout.LinuxType, ID: layout.GUID()})
	im.Facts = map[string]string{"layout": "prepared-1", "subsystem": "release-1"}
	raw, _ := os.ReadFile(im.Path)
	copy(raw[4096*512:], bytes.Repeat([]byte("apps"), 2048*512/4))
	os.WriteFile(im.Path, raw, 0600)
	im.SHA256, _ = firmware.Hash(context.Background(), bytes.NewReader(raw), im.Size, nil)
	dir := "/dev/shm"
	if _, e := os.Stat(dir); e != nil {
		dir = ""
	}
	card, e := os.CreateTemp(dir, "prepared-card-*")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { card.Close(); os.Remove(card.Name()) })
	size := int64(40) << 30
	if e = card.Truncate(size); e != nil {
		t.Skip("no sparse files here")
	}
	finger, _ := Snapshot(card, size)
	gpath := filepath.Join(t.TempDir(), "guest.img")
	graw := bytes.Repeat([]byte("erofs!"), (1<<20)/6+1)[:1<<20]
	os.WriteFile(gpath, graw, 0600)
	g := &Guest{Path: gpath, Darkosen: "11112026", Revision: 1, Name: "dArkOSen_R36_11112026.erofs.img", Size: 1 << 20, SHA256: Digest(graw), PartitionBytes: 4 << 20}
	return im, Card{Fingerprint: finger, Table: layout.Table{Size: size}}, card, boot, g
}

func TestPreparedInstallWritesEveryPartition(t *testing.T) {
	im, card, dst, boot, g := preparedSample(t)
	size := card.Table.Size
	p, e := BuildWith(im, card, size, "install", "Panels/Panel4", false, 0, Options{Guest: g})
	if e != nil {
		t.Fatal(e)
	}
	if !p.Prepared || p.NoROMs {
		t.Fatal("not a prepared plan with games", p.Prepared, p.NoROMs)
	}
	if e = Execute(context.Background(), dst, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	tab, e := layout.Read(dst, size)
	if e != nil {
		t.Fatal(e)
	}
	want := map[string]int{"userdata": 6, "EASYROMS": 7, AutoAPK: 13, GuestPartition: 14}
	for name, number := range want {
		q, ok := tab.Find(name)
		if !ok || q.Number != number {
			t.Fatal(name, q.Number, ok)
		}
	}
	if _, ok := tab.Number(12); ok {
		t.Fatal("the image's own apps partition is still in the table")
	}
	data, _ := tab.Find("userdata")
	if data.Flags != 1<<63 {
		t.Fatalf("userdata attributes %#x, the console sets bit 63", data.Flags)
	}
	apps, _ := tab.Find(AutoAPK)
	roms, _ := tab.Find("EASYROMS")
	guest, _ := tab.Find(GuestPartition)
	if data.End+1 != apps.Start || apps.End+1 != roms.Start || roms.End+1 != guest.Start || int64(guest.End) != size/512-34 {
		t.Fatal("partitions don't follow each other", data, apps, roms, guest)
	}
	read := func(off, n int64) []byte {
		b := make([]byte, n)
		if _, e := dst.ReadAt(b, off); e != nil {
			t.Fatal(e)
		}
		return b
	}
	if !bytes.Equal(read(apps.Offset(), 2048*512), bytes.Repeat([]byte("apps"), 2048*512/4)) {
		t.Fatal("bundled apps not moved behind userdata")
	}
	if !bytes.Equal(read(data.Offset(), 1<<20), make([]byte, 1<<20)) {
		t.Fatal("start of userdata is not empty")
	}
	graw, _ := os.ReadFile(g.Path)
	if !bytes.Equal(read(guest.Offset(), g.Size), graw) {
		t.Fatal("dArkOSen image not on its partition")
	}
	// what was written for the games partition, checked by the system's exFAT checker
	if fsck, e := exec.LookPath("fsck.exfat"); e == nil {
		vol, _ := os.CreateTemp(filepath.Dir(dst.Name()), "prepared-roms-*")
		defer os.Remove(vol.Name())
		vol.Truncate(roms.Size())
		for _, w := range p.Writes {
			if w.Name == "Games partition" {
				vol.WriteAt(read(w.Offset, w.Length), w.Offset-roms.Offset())
			}
		}
		vol.Close()
		if out, e := exec.Command(fsck, "-n", vol.Name()).CombinedOutput(); e != nil || !strings.Contains(string(out), "clean") {
			t.Fatalf("games partition: %v\n%s", e, out)
		}
	}
	files, e := p.BootFiles()
	if e != nil {
		t.Fatal(e)
	}
	wantLayout := fmt.Sprintf("prepared-1\nuserdata %d %d\nAUTOAPK %d %d\nEASYROMS %d %d\nDARKOS %d %d\n", data.Start, data.End, apps.Start, apps.End, roms.Start, roms.End, guest.Start, guest.End)
	if string(files[LayoutFile]) != wantLayout {
		t.Fatalf("layout file:\n%s", files[LayoutFile])
	}
	var j struct {
		Format   int
		Darkosen string
		Revision int
		Image    struct {
			Name, SHA256 string
			Size         int64
		}
	}
	if e = json.Unmarshal(files[GuestFile], &j); e != nil || j.Format != 1 || j.Darkosen != "11112026" || j.Revision != 1 || j.Image.Size != g.Size || j.Image.SHA256 != g.SHA256 || j.Image.Name != g.Name {
		t.Fatalf("release file: %v %s", e, files[GuestFile])
	}
}

func TestPreparedOnlyWhenTheReleaseSaysSo(t *testing.T) {
	im, card, _, _, g := preparedSample(t)
	im.Facts = map[string]string{"layout": "prepared-1"}
	if _, e := BuildWith(im, card, card.Table.Size, "install", "Panels/Panel4", false, 0, Options{Guest: g}); e == nil {
		t.Fatal("dArkOSen accepted for a release without subsystem=release-1")
	}
	im.Facts = nil
	p, e := BuildWith(im, card, card.Table.Size, "install", "Panels/Panel4", false, 0, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if p.Prepared {
		t.Fatal("prepared without layout=prepared-1")
	}
	if _, ok := p.Table.Find("userdata"); ok {
		t.Fatal("legacy install made userdata")
	}
	if q, ok := p.Table.Find(AutoAPK); !ok || q.Number != 12 {
		t.Fatal("legacy install lost the image's apps partition")
	}
	if files, _ := p.BootFiles(); files != nil {
		t.Fatal("legacy install writes layout files")
	}
	if _, e = BuildWith(im, card, card.Table.Size, "install", "Panels/Panel4", false, 0, Options{Guest: g}); e == nil {
		t.Fatal("dArkOSen accepted for a legacy release")
	}
}

func TestPreparedRefusesADamagedGuest(t *testing.T) {
	im, card, dst, boot, g := preparedSample(t)
	g.SHA256 = strings.Repeat("0", 64)
	p, e := BuildWith(im, card, card.Table.Size, "install", "Panels/Panel4", false, 0, Options{Guest: g})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := Snapshot(dst, card.Table.Size)
	if e = Execute(context.Background(), dst, im, p, boot, t.TempDir(), nil); e == nil {
		t.Fatal("damaged image written")
	}
	if after, _ := Snapshot(dst, card.Table.Size); after != before {
		t.Fatal("card changed")
	}
}

func TestGuestOnlyForReleasesThatFit(t *testing.T) {
	im := &firmware.Image{BuildIdentity: "v2026-11-11-release|eng.kenny.20261111.202641", Facts: map[string]string{"layout": "prepared-1", "subsystem": "release-1"}}
	for min, ok := range map[string]bool{"": true, "20261111": true, "20261111.202641": true, "20261111.202642": false, "20261112": false, "20261001.000000": true, "soon": false} {
		if e := GuestFits(im, &Guest{Minimum: min}); (e == nil) != ok {
			t.Fatal(min, e)
		}
	}
	for _, facts := range []map[string]string{nil, {"layout": "prepared-1"}, {"subsystem": "release-1"}, {"layout": "prepared-2", "subsystem": "release-1"}} {
		if GuestFits(&firmware.Image{BuildIdentity: im.BuildIdentity, Facts: facts}, &Guest{}) == nil {
			t.Fatal("offered for", facts)
		}
	}
	// a release that doesn't say when it was built can't meet a minimum
	if GuestFits(&firmware.Image{Facts: im.Facts}, &Guest{Minimum: "20261111"}) == nil {
		t.Fatal("offered without a build date")
	}
}

// The record on BOOT names the system for the console. Its keys are the ones the
// console's partition-pin accepts (format, distribution, darkosen, revision,
// minimum_andr36oid, image). A release whose console reads the field gets it for every
// system; an older one only takes dArkOSen, without it, as before.
func TestGuestRecordNamesTheSystem(t *testing.T) {
	for _, c := range []struct {
		in    string
		reads bool
		want  string
		fails bool
	}{
		{"dArkOS", true, "dArkOS", false}, {"darkosre", true, "dArkOSRE", false}, {"dArkOSen", true, "dArkOSen", false}, {"", true, "dArkOSen", false},
		{"dArkOSen", false, "", false}, {"", false, "", false}, {"dArkOS", false, "", true}, {"dArkOSRE", false, "", true},
	} {
		g := &Guest{Distribution: c.in, Darkosen: "11072026", Revision: 1, Name: "dArkOS_RG351MP_11072026.erofs.img", Size: 1 << 20, SHA256: strings.Repeat("a", 64)}
		files, e := (&Plan{Prepared: true, Guest: g, GuestDistribution: c.reads}).BootFiles()
		if c.fails {
			if e == nil {
				t.Fatal(c.in, "written for a console that can't read it")
			}
			continue
		}
		if e != nil {
			t.Fatal(c.in, e)
		}
		var m map[string]any
		if e = json.Unmarshal(files[GuestFile], &m); e != nil {
			t.Fatal(e)
		}
		got, has := m["distribution"]
		if (c.want == "") == has || (has && got != c.want) {
			t.Fatalf("%q %v: %s", c.in, c.reads, files[GuestFile])
		}
		for k := range m {
			if !strings.Contains("|format|distribution|darkosen|revision|minimum_andr36oid|image|", "|"+k+"|") {
				t.Fatal("key the console doesn't read:", k)
			}
		}
	}
	g := &Guest{Distribution: "openwrt", Darkosen: "11072026", Revision: 1, Name: "x.img", Size: 1 << 20, SHA256: strings.Repeat("a", 64)}
	if _, e := (&Plan{Prepared: true, Guest: g, GuestDistribution: true}).BootFiles(); e == nil {
		t.Fatal("unknown system written")
	}
	if g.DistributionName() != "" || (&Guest{}).DistributionName() != "dArkOSen" {
		t.Fatal("distribution names")
	}
}

// dArkOS and dArkOSRE go only on a card whose release reads "distribution"; the
// build stamp in the image's identity is compared with the minimum.
func TestGuestNeedsAReleaseThatReadsTheSystem(t *testing.T) {
	im := &firmware.Image{Facts: map[string]string{"layout": "prepared-1", "subsystem": "release-1"}, BuildIdentity: "0.9|eng.kenny.20261117.101500"}
	for _, c := range []struct {
		minimum string
		reads   bool
	}{{"", false}, {"20261117", true}, {"20261117.101500", true}, {"20261117.101501", false}, {"20261118", false}, {"2026111", false}} {
		if readsDistribution(im, c.minimum) != c.reads {
			t.Error(c.minimum, !c.reads)
		}
	}
	if readsDistribution(&firmware.Image{BuildIdentity: "no stamp"}, "20261117") {
		t.Fatal("no stamp counted as new")
	}
	// with the placeholder still empty, only dArkOSen fits
	if MinimumDistributionBuild == "" {
		if e := GuestFits(im, &Guest{Distribution: "dArkOS"}); !errors.Is(e, ErrGuestNeedsNewerRelease) {
			t.Fatal(e)
		}
		if e := GuestFits(im, &Guest{}); e != nil {
			t.Fatal(e)
		}
	}
	if e := GuestFits(im, &Guest{Minimum: "20271231"}); !errors.Is(e, ErrGuestNeedsNewerRelease) {
		t.Fatal("old release accepted", e)
	}
}
