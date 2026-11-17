package mobile

import (
	"context"
	"encoding/json"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func preparedDisplayImage() *firmware.Image {
	return &firmware.Image{Facts: map[string]string{"layout": "prepared-1", "subsystem": "release-1"}, Table: layout.Table{Parts: []layout.Partition{{Name: "AUTOAPK", Start: 5701632, End: 5701632 + 4194304 - 1}}}}
}
func TestGuestStorageDisplayMatchesWriter(t *testing.T) {
	im := preparedDisplayImage()
	data, _ := json.Marshal(im)
	for _, size := range []int64{16 << 30, 32 << 30, 64 << 30, 128 << 30} {
		for _, guest := range []int64{0, 4 << 30, 8 << 30} {
			for _, want := range []int{0, 16, 24, 64} {
				for _, all := range []bool{false, true} {
					a, g, ok, e := flash.PreparedSizes(im, size, want, all, guest)
					if !ok {
						t.Fatal("fixture not prepared")
					}
					raw, gotErr := StorageLayoutForImage(string(data), size, want, all, guest)
					if (e == nil) != (gotErr == nil) {
						t.Fatal(size, guest, want, all, e, gotErr)
					}
					if e != nil {
						continue
					}
					var out map[string]int
					if e = json.Unmarshal([]byte(raw), &out); e != nil {
						t.Fatal(e)
					}
					if out["android"] != flash.RoundGiB(a) || out["games"] != flash.RoundGiB(g) || out["max"] != flash.MaxPreparedGiB(im, size, guest) || out["min"] != flash.MinUserdataGiB {
						t.Fatal(size, guest, want, all, out, a, g)
					}
				}
			}
		}
	}
	// The original static helper ignored this reservation: verify it is visible.
	plain, _ := StorageLayoutForImage(string(data), 64<<30, 24, false, 0)
	with, _ := StorageLayoutForImage(string(data), 64<<30, 24, false, 4<<30)
	var a, b map[string]int
	json.Unmarshal([]byte(plain), &a)
	json.Unmarshal([]byte(with), &b)
	if a["games"]-b["games"] != 4 || a["max"]-b["max"] != 4 {
		t.Fatal(a, b)
	}
	for _, guest := range []int64{-1, 4 << 30} {
		if _, e := StorageLayoutForImage(`{}`, 64<<30, 0, false, guest); e == nil {
			t.Fatal("invalid/unsupported reservation", guest)
		}
	}
	if _, e := StorageLayoutForImage("bad json", 64<<30, 0, false, 0); e == nil {
		t.Fatal("bad metadata")
	}
	for _, size := range []int64{16 << 30, 32 << 30, 128 << 30} {
		old := StorageLayout(size, 0)
		current, e := StorageLayoutForImage(`{}`, size, 0, false, 0)
		if e != nil || old != current {
			t.Fatal("legacy layout changed", old, current, e)
		}
	}
}
func TestGuestPreviewNeverAuthorizesWrite(t *testing.T) {
	g := &catalog.GuestRelease{Darkosen: "11112026", Revision: 1, PartitionBytes: 4 << 30}
	g.Image.Name = "darkosen.erofs.img"
	g.Image.Size = 1 << 20
	g.Image.SHA256 = strings.Repeat("a", 64)
	s := &Session{dir: t.TempDir(), image: &firmware.Image{SHA256: "one"}, guests: map[string]*catalog.GuestRelease{"dArkOSen": g}, guestPaths: map[string]string{}, guestFor: "one"}
	opts := options{Mode: "install", Guest: true}
	preview, e := s.guestForOptions(opts, true)
	if e != nil || preview == nil || preview.Path == "" || preview.PartitionBytes != 4<<30 {
		t.Fatal(preview, e)
	}
	if s.guestPaths["dArkOSen"] != "" {
		t.Fatal("preview saved as download")
	}
	if _, e = s.guestChoice(opts); e == nil {
		t.Fatal("preview authorized strict writer")
	}
	if _, e = s.guestForOptions(options{Mode: "update", Guest: true}, true); e == nil {
		t.Fatal("preview enabled on update")
	}
	if _, e = os.Stat(preview.Path); !os.IsNotExist(e) {
		t.Fatal("preview created payload", e)
	}
	s.guestPaths["dArkOSen"] = filepath.Join(s.dir, "verified-image")
	actual, e := s.guestChoice(opts)
	if e != nil || actual.Path != s.guestPaths["dArkOSen"] {
		t.Fatal(actual, e)
	}
	s.image = &firmware.Image{SHA256: "new"}
	if _, e = s.guestForOptions(opts, true); e == nil {
		t.Fatal("stale offer accepted")
	}
}
func TestGuestReviewPlanAndStrictWriteOnRealRelease(t *testing.T) {
	path := os.Getenv("SDFLASHER_TEST_IMAGE")
	if path == "" {
		t.Skip("set SDFLASHER_TEST_IMAGE to a prepared release")
	}
	dir := t.TempDir()
	im, e := firmware.Load(context.Background(), path, dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !im.Subsystem() {
		t.Skip("release has no partition guest contract")
	}
	d := &fakeDisk{size: 64 << 30}
	dev, e := adapt(d)
	if e != nil {
		t.Fatal(e)
	}
	card, e := flash.InspectCard(dev, dev.size)
	if e != nil {
		t.Fatal(e)
	}
	g := &catalog.GuestRelease{Darkosen: "11112026", Revision: 1, PartitionBytes: 4 << 30}
	g.Image.Name = "darkosen.erofs.img"
	g.Image.Size = 1 << 20
	g.Image.SHA256 = strings.Repeat("a", 64)
	s := &Session{dir: dir, image: im, guests: map[string]*catalog.GuestRelease{"dArkOSen": g}, guestPaths: map[string]string{}, guestFor: im.SHA256}
	raw, _ := json.Marshal(options{Mode: "install", Profile: "Experimental/R36-Ultra", Identity: d.Identity(), Fingerprint: card.Fingerprint, Guest: true})
	plan, e := s.Plan(d, string(raw))
	if e != nil {
		t.Fatal(e)
	}
	var p flash.Plan
	if e = json.Unmarshal([]byte(plan), &p); e != nil {
		t.Fatal(e)
	}
	if p.Guest == nil || p.Guest.PartitionBytes != 4<<30 {
		t.Fatal("review omitted guest")
	}
	if _, ok := p.Table.Find("DARKOS"); !ok {
		t.Fatal("review omitted partition")
	}
	if _, e = s.Write(d, string(raw), nil); e == nil || !strings.Contains(e.Error(), "download dArkOSen first") {
		t.Fatal("write before download", e)
	}
	if d.writes != 0 || d.flushes != 0 || s.guestPaths["dArkOSen"] != "" {
		t.Fatal("review/download refusal changed card or session")
	}
}

// On Android the hints add that some phones can't do this at all; a failure that is
// not the card's or the reader's has none.
func TestAndroidFailureHints(t *testing.T) {
	s := &Session{failure: flash.FailureVerify}
	h := s.LastFailureHints()
	if !strings.HasPrefix(h, "The card may be faulty") || !strings.Contains(h, "Try a different card reader.") || !strings.HasSuffix(h, "If so, use a computer.") {
		t.Fatal(h)
	}
	if s.failure = flash.FailureOther; s.LastFailureHints() != "" {
		t.Fatal("hints for a failure that isn't the card's")
	}
	if r := ReaderFailureHints(); !strings.Contains(r, "Android phones") || strings.Contains(r, "faulty") {
		t.Fatal(r)
	}
	d := &usbDisk{disk: &fakeDisk{size: 64 << 30}, size: 64 << 30}
	if _, e := d.ReadAt(make([]byte, 512), 1); flash.Classify(e) != flash.FailureOther {
		t.Fatal("a wrong range is not the card's fault", e)
	}
}
