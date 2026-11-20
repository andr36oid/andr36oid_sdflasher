package flash

// End-to-end runs of the real plan and Execute against regular sparse files standing
// in for cards of many sizes, and against cards that misbehave: counterfeit cards
// that wrap or drop writes beyond their real size, I/O errors at an offset, short
// writes, a reader that vanishes, a write-protected card. Nothing here opens a disk
// device. SDFLASHER_SCRATCH puts the sparse cards on a file system of your choice
// (default: the test's temp dir).

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
)

func scratchCard(t *testing.T, size int64) *os.File {
	t.Helper()
	dir := os.Getenv("SDFLASHER_SCRATCH")
	if dir == "" {
		dir = t.TempDir()
	}
	f, e := os.CreateTemp(dir, "harness-card-*")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close(); os.Remove(f.Name()) })
	if e = f.Truncate(size); e != nil {
		t.Skip("no sparse files here:", e)
	}
	return f
}

// harnessImage is preparedSample's image and guest without its card.
func harnessImage(t *testing.T) (*firmware.Image, string, *Guest) {
	t.Helper()
	im, _, _, boot, g := preparedSample(t)
	return im, boot, g
}

// realSize is what a card sold as gb GB really holds (about 96.5 % of it, on a 512
// byte boundary), like the cards people buy.
func realSize(gb int64) int64 { return gb * 1e9 * 965 / 1000 / 512 * 512 }

// checkCard reads a written card back the way the console would and checks the GPT,
// the protective MBR, every partition and the guest.
func checkCard(t *testing.T, dev io.ReaderAt, size int64, p *Plan, g *Guest) {
	t.Helper()
	head := make([]byte, 1024)
	if _, e := dev.ReadAt(head, 0); e != nil {
		t.Fatal(e)
	}
	if head[450] != 0xee || head[510] != 0x55 || head[511] != 0xaa {
		t.Fatal("no protective MBR")
	}
	if n := binary.LittleEndian.Uint32(head[458:]); int64(n) != min(size/512-1, 0xffffffff) {
		t.Fatalf("protective MBR covers %d sectors, card has %d", n, size/512-1)
	}
	if alt := binary.LittleEndian.Uint64(head[512+32:]); int64(alt) != size/512-1 {
		t.Fatalf("backup GPT at LBA %d, card ends at %d", alt, size/512-1)
	}
	tab, e := layout.Read(dev, size)
	if e != nil {
		t.Fatal("table:", e)
	}
	for _, q := range tab.Parts {
		if int64(q.End) > size/512-34 || q.Start < 34 {
			t.Fatalf("%s outside the usable area: %d-%d", q.Name, q.Start, q.End)
		}
	}
	if g != nil {
		q, ok := tab.Find(GuestPartition)
		if !ok || int64(q.End) != size/512-34 || q.Start%2048 != 0 || q.Size() < g.PartitionBytes {
			t.Fatalf("guest partition %+v", q)
		}
		raw, _ := os.ReadFile(g.Path)
		got := make([]byte, len(raw))
		if _, e := dev.ReadAt(got, q.Offset()); e != nil || !bytes.Equal(got, raw) {
			t.Fatal("guest image not on its partition", e)
		}
	}
	if roms, ok := tab.Find("EASYROMS"); ok {
		vbr := make([]byte, 512)
		dev.ReadAt(vbr, roms.Offset())
		if string(vbr[3:11]) != "EXFAT   " || vbr[510] != 0x55 {
			t.Fatal("EASYROMS has no exFAT boot sector")
		}
		if total := int64(binary.LittleEndian.Uint64(vbr[72:])) * 512; total > roms.Size() {
			t.Fatalf("exFAT volume %d bytes in a %d byte partition", total, roms.Size())
		}
	}
	_ = p
}

// Cards sold as 4 to 256 GB: 4 and 8 are refused before anything is written, the rest
// take Android, the games drive and a game system, with the GPT where it belongs.
func TestHarnessCardSizes(t *testing.T) {
	im, boot, g := harnessImage(t)
	MinCardBytes = 14e9
	for _, gb := range []int64{4, 8, 16, 32, 64, 128, 256} {
		for _, withGuest := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dGB/guest=%v", gb, withGuest), func(t *testing.T) {
				size := realSize(gb)
				card := scratchCard(t, size)
				finger, _ := Snapshot(card, size)
				var guest *Guest
				if withGuest {
					guest = g
				}
				p, e := BuildWith(im, Card{Fingerprint: finger, Table: layout.Table{Size: size}}, size, "install", "Panels/Panel4", false, 0, Options{Guest: guest})
				if gb <= 8 {
					if e == nil || !strings.Contains(e.Error(), "too small") {
						t.Fatal("small card accepted:", e)
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				if e = Execute(context.Background(), card, im, p, boot, t.TempDir(), nil); e != nil {
					t.Fatal(e)
				}
				checkCard(t, card, size, p, guest)
				if withGuest {
					files, e := p.BootFiles()
					var rec map[string]any
					if e != nil || json.Unmarshal(files[GuestFile], &rec) != nil {
						t.Fatal("record", e)
					}
				}
			})
		}
	}
}

// The smallest card that takes Android and a game system: one sector less is refused
// with a message that says so, the smallest one is written and correct.
func TestHarnessGuestCardBoundary(t *testing.T) {
	im, boot, g := harnessImage(t)
	g.PartitionBytes = 3 << 30
	fits := func(size int64) bool {
		_, _, _, e := PreparedSizes(im, size, 0, false, g.PartitionBytes)
		return e == nil
	}
	lo, hi := int64(1<<30)/512, int64(64<<30)/512
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if fits(mid * 512) {
			hi = mid
		} else {
			lo = mid
		}
	}
	smallest := hi * 512
	if fits(smallest - 512) {
		t.Fatal("search")
	}
	card := scratchCard(t, smallest-512)
	finger, _ := Snapshot(card, smallest-512)
	_, e := BuildWith(im, Card{Fingerprint: finger, Table: layout.Table{Size: smallest - 512}}, smallest-512, "install", "Panels/Panel4", false, 0, Options{Guest: g})
	if e == nil || !strings.Contains(e.Error(), "too small") {
		t.Fatal("one sector short accepted:", e)
	}
	card = scratchCard(t, smallest)
	finger, _ = Snapshot(card, smallest)
	p, e := BuildWith(im, Card{Fingerprint: finger, Table: layout.Table{Size: smallest}}, smallest, "install", "Panels/Panel4", false, 0, Options{Guest: g})
	if e != nil {
		t.Fatal(e)
	}
	if e = Execute(context.Background(), card, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	checkCard(t, card, smallest, p, g)
	t.Logf("smallest card for Android + a %d MiB system (test image): %d bytes", g.PartitionBytes>>20, smallest)
}

// A card that has an old andr36oid on it, flashed again with install: the old backup
// GPT and partitions don't survive, the card reads as the new layout.
func TestHarnessReflashOverExistingCard(t *testing.T) {
	im, boot, g := harnessImage(t)
	size := realSize(32)
	card := scratchCard(t, size)
	for round := 0; round < 2; round++ {
		finger, _ := Snapshot(card, size)
		guest := g
		if round == 1 {
			guest = nil
		}
		p, e := BuildWith(im, Card{Fingerprint: finger, Table: layout.Table{Size: size}}, size, "install", "Panels/Panel4", false, 0, Options{Guest: guest})
		if e != nil {
			t.Fatal(e)
		}
		if e = Execute(context.Background(), card, im, p, boot, t.TempDir(), nil); e != nil {
			t.Fatal(e)
		}
		tab, e := layout.Read(card, size)
		if e != nil {
			t.Fatal(e)
		}
		if _, ok := tab.Find(GuestPartition); ok != (guest != nil) {
			t.Fatalf("round %d: guest partition %v", round, ok)
		}
	}
}

// fakeCard misbehaves the way bad cards and readers do.
type fakeCard struct {
	f *os.File
	// wrap: the card really holds wrap bytes and writes beyond wrap to off%wrap
	wrap int64
	// drop: writes at or beyond drop are lost, reads there return zeros
	drop int64
	// failAt: writes covering this offset fail with EIO (-1: never)
	failAt int64
	// failReadAt: reads covering this offset fail with EIO (-1: never)
	failReadAt int64
	// short: every write writes one byte less
	short bool
	// vanishAfter: after this many writes, everything fails with ENODEV (0: never)
	vanishAfter int64
	// readOnly: every write fails with EROFS
	readOnly bool
	writes   atomic.Int64
}

func newFake(f *os.File) *fakeCard { return &fakeCard{f: f, failAt: -1, failReadAt: -1} }

func (c *fakeCard) gone() bool { return c.vanishAfter > 0 && c.writes.Load() >= c.vanishAfter }

func (c *fakeCard) ReadAt(b []byte, off int64) (int, error) {
	if c.gone() {
		return 0, syscall.ENODEV
	}
	if c.failReadAt >= 0 && off <= c.failReadAt && c.failReadAt < off+int64(len(b)) {
		return 0, syscall.EIO
	}
	if c.wrap > 0 {
		for n := 0; n < len(b); {
			o := (off + int64(n)) % c.wrap
			k := min(int64(len(b)-n), c.wrap-o)
			if _, e := c.f.ReadAt(b[n:n+int(k)], o); e != nil {
				return n, e
			}
			n += int(k)
		}
		return len(b), nil
	}
	if c.drop > 0 && off+int64(len(b)) > c.drop {
		clear(b)
		if off < c.drop {
			return c.f.ReadAt(b[:c.drop-off], off)
		}
		return len(b), nil
	}
	return c.f.ReadAt(b, off)
}

func (c *fakeCard) WriteAt(b []byte, off int64) (int, error) {
	c.writes.Add(1)
	if c.gone() {
		return 0, syscall.ENODEV
	}
	if c.readOnly {
		return 0, syscall.EROFS
	}
	if c.failAt >= 0 && off <= c.failAt && c.failAt < off+int64(len(b)) {
		return 0, syscall.EIO
	}
	if c.short && len(b) > 1 {
		b = b[:len(b)-1]
	}
	if c.wrap > 0 {
		for n := 0; n < len(b); {
			o := (off + int64(n)) % c.wrap
			k := min(int64(len(b)-n), c.wrap-o)
			if _, e := c.f.WriteAt(b[n:n+int(k)], o); e != nil {
				return n, e
			}
			n += int(k)
		}
		return len(b), nil
	}
	if c.drop > 0 && off+int64(len(b)) > c.drop {
		if off < c.drop {
			c.f.WriteAt(b[:c.drop-off], off)
		}
		return len(b), nil
	}
	return c.f.WriteAt(b, off)
}

func (c *fakeCard) Sync() error {
	if c.gone() {
		return syscall.ENODEV
	}
	return nil
}

// harnessPlan is an install plan with a guest on a fresh 32 GB card.
func harnessPlan(t *testing.T) (*firmware.Image, string, *Guest, *os.File, int64, *Plan) {
	t.Helper()
	im, boot, g := harnessImage(t)
	size := realSize(32)
	card := scratchCard(t, size)
	finger, _ := Snapshot(card, size)
	p, e := BuildWith(im, Card{Fingerprint: finger, Table: layout.Table{Size: size}}, size, "install", "Panels/Panel4", false, 0, Options{Guest: g})
	if e != nil {
		t.Fatal(e)
	}
	return im, boot, g, card, size, p
}

// Each misbehaving card stops the install with an error of the right kind, the hint
// lines for it, and the message that the card must be written again.
func TestHarnessBadCards(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*fakeCard, *Plan)
		kind  FailureKind
		text  string
		state *i18n.Format
	}{
		{"io error mid image", func(c *fakeCard, p *Plan) { c.failAt = p.Writes[0].Offset + 1<<20 }, FailureDevice, "input/output error", StateIncomplete},
		{"io error in guest", func(c *fakeCard, p *Plan) { c.failAt = p.Writes[len(p.Writes)-3].Offset }, FailureDevice, "input/output error", StateIncomplete},
		// these stop in the card test: only its test blocks were written
		{"short writes", func(c *fakeCard, p *Plan) { c.short = true }, FailureDevice, "short write", StateCardTest},
		{"reader vanishes", func(c *fakeCard, p *Plan) { c.vanishAfter = 6 }, FailureDevice, "no such device", StateCardTest},
	} {
		t.Run(c.name, func(t *testing.T) {
			im, boot, _, card, _, p := harnessPlan(t)
			dev := newFake(card)
			c.setup(dev, p)
			e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
			if e == nil {
				t.Fatal("bad card written without an error; writes:", dev.writes.Load())
			}
			if Classify(e) != c.kind || !strings.Contains(e.Error(), c.text) || !c.state.Is(e) {
				t.Fatalf("%q: %v", Classify(e), e)
			}
			if len(FailureHints(Classify(e), false)) == 0 {
				t.Fatal("no hints")
			}
		})
	}
}

// A card that can't be read stops in the preflight, before anything is written.
func TestHarnessUnreadableCardStopsBeforeWriting(t *testing.T) {
	im, boot, _, card, _, p := harnessPlan(t)
	dev := newFake(card)
	dev.failReadAt = p.Writes[len(p.Writes)-3].Offset // the guest's start
	e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
	if e == nil || Classify(e) != FailureDevice || !strings.Contains(e.Error(), "card read check failed") {
		t.Fatal(e)
	}
	if n := dev.writes.Load(); n != 0 {
		t.Fatalf("%d writes before the read check failed", n)
	}
}

// A write-protected card: the first write fails and nothing is on the card.
func TestHarnessWriteProtectedCard(t *testing.T) {
	im, boot, _, card, size, p := harnessPlan(t)
	dev := newFake(card)
	dev.readOnly = true
	e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
	if e == nil || Classify(e) != FailureDevice {
		t.Fatal(e)
	}
	if fresh, _ := Snapshot(card, size); fresh != p.OldFingerprint {
		t.Fatal("write-protected card changed")
	}
	t.Logf("user sees: %v", e)
}

// Cancel at every stage of an install: the error is the cancel itself (no hints) and
// says the card must be written again.
func TestHarnessCancelAtEveryStage(t *testing.T) {
	for _, phase := range []string{"preflight", "writing", "verifying"} {
		t.Run(phase, func(t *testing.T) {
			im, boot, _, card, _, p := harnessPlan(t)
			ctx, cancel := context.WithCancel(context.Background())
			seen := 0
			e := Execute(ctx, card, im, p, boot, t.TempDir(), func(pr Progress) {
				if pr.Phase == phase && pr.Done > 0 {
					if seen++; seen == 2 {
						cancel()
					}
				}
			})
			cancel()
			if !errors.Is(e, context.Canceled) || Classify(e) != FailureOther {
				t.Fatal(e)
			}
			if phase != "preflight" && !StateIncomplete.Is(e) {
				t.Fatal("cancel mid-write doesn't say the card is incomplete:", e)
			}
		})
	}
}

// A counterfeit card that wraps writes beyond its real size around to its start, at
// a size the size check's markers miss: each range reads back right after it is
// written, so the game system at the end of the card, which lands on the release
// image, reads back as written while the release image under it is gone. The
// read-back of every range's ends after all writes catches it.
func TestHarnessCounterfeitWrapCard(t *testing.T) {
	im, boot, g, card, _, p := harnessPlan(t)
	var guestOff int64
	for _, w := range p.Writes {
		if w.Guest {
			guestOff = w.Offset
		}
	}
	dev := newFake(card)
	// the card holds 1 MiB less than the guest's offset: the guest lands 1 MiB into
	// the release image
	dev.wrap = guestOff - 1<<20
	e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
	if e == nil {
		t.Fatal("counterfeit card written without an error; the release image on it is damaged (guest", g.Size, "bytes wrapped onto it)")
	}
	if Classify(e) != FailureVerify || !StateIncomplete.Is(e) {
		t.Fatalf("%q: %v", Classify(e), e)
	}
}

// recorder notes where a card was written.
type recorder struct {
	Device
	writes []Range
}

func (r *recorder) WriteAt(b []byte, off int64) (int, error) {
	r.writes = append(r.writes, Range{"", off, int64(len(b))})
	return r.Device.WriteAt(b, off)
}

// Counterfeit cards of the common kinds, which wrap at or drop beyond a power of two,
// are found by the size check before the release is written: the only writes are
// the markers and what was under them, and the message says what the card really
// holds. A good card of every size passes the check and is as it was before it.
func TestHarnessFakeCardsStopBeforeWriting(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*fakeCard)
		works string
	}{
		{"wraps at 8 GiB", func(c *fakeCard) { c.wrap = 8 << 30 }, "8.6 GB"},
		{"wraps at 16 GiB", func(c *fakeCard) { c.wrap = 16 << 30 }, "17.2 GB"},
		{"wraps at 1 GiB", func(c *fakeCard) { c.wrap = 1 << 30 }, "1.1 GB"},
		{"drops beyond 8 GiB", func(c *fakeCard) { c.drop = 8 << 30 }, "8.6 GB"},
		{"drops beyond 3 GiB", func(c *fakeCard) { c.drop = 3 << 30 }, "4.3 GB"},
	} {
		t.Run(c.name, func(t *testing.T) {
			im, boot, _, card, size, p := harnessPlan(t)
			fake := newFake(card)
			c.setup(fake)
			dev := &recorder{Device: fake}
			e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
			if e == nil || Classify(e) != FailureCapacity || !ErrFakeCapacity.Is(e) {
				t.Fatalf("%q: %v", Classify(e), e)
			}
			want := "it reports " + gigabytes(size) + ", but only about " + c.works + " works"
			if !strings.Contains(e.Error(), want) || StateIncomplete.Is(e) {
				t.Fatalf("message %q, want %q", e, want)
			}
			if len(FailureHints(Classify(e), false)) == 0 {
				t.Fatal("no hints")
			}
			for _, w := range dev.writes {
				inside := false
				for _, r := range p.Probes {
					inside = inside || w.Offset >= r.Offset && w.End() <= r.End()
				}
				if !inside {
					t.Fatalf("write at %d+%d before the card was found fake", w.Offset, w.Length)
				}
			}
		})
	}
}

// The size check leaves a good card as it was, on every card size.
func TestHarnessSizeCheckLeavesTheCard(t *testing.T) {
	for _, gb := range []int64{16, 64, 256, 512} {
		size := realSize(gb)
		card := scratchCard(t, size)
		probes := capacityProbes(size)
		if len(probes) < 4 || probes[0].Offset != 0 || probes[len(probes)-1].End() != size {
			t.Fatalf("%d GB: probes %v", gb, probes)
		}
		old := map[int64][]byte{}
		for _, r := range probes {
			b := bytes.Repeat([]byte{byte(r.Offset >> 20)}, int(r.Length))
			card.WriteAt(b, r.Offset)
			old[r.Offset] = b
		}
		dev := &guardedDevice{Device: deviceErrors{card}, plan: &Plan{Mode: "install", Size: size, Probes: probes}}
		if e := checkCapacity(context.Background(), dev, dev.plan, func(Progress) {}); e != nil {
			t.Fatal(gb, e)
		}
		for _, r := range probes {
			got := make([]byte, r.Length)
			card.ReadAt(got, r.Offset)
			if !bytes.Equal(got, old[r.Offset]) {
				t.Fatalf("%d GB: bytes at %d changed", gb, r.Offset)
			}
		}
	}
}

// An update never writes the markers: they would land in the user's data.
func TestHarnessUpdateHasNoSizeCheck(t *testing.T) {
	im, c, dst, boot := sample(t)
	p, e := Build(im, c, c.Table.Size, "update", "Panels/Panel4", false, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Probes) != 0 {
		t.Fatal("update plan has a size check", p.Probes)
	}
	p.Probes = capacityProbes(4 << 30)
	if p.Validate() == nil {
		t.Fatal("an update plan with a size check validates")
	}
	p.Probes = nil
	dev := &recorder{Device: dst}
	if e = Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	for _, w := range dev.writes {
		inside := false
		for _, x := range p.Writes {
			inside = inside || w.Offset >= x.Offset && w.End() <= x.End()
		}
		if !inside {
			t.Fatalf("update wrote %d+%d outside its plan", w.Offset, w.Length)
		}
	}
}

// A write-protected card: nothing is written, and the message says so and names the
// lock switch, not that the card is incomplete.
func TestHarnessWriteProtectedSaysNothingWritten(t *testing.T) {
	im, boot, _, card, _, p := harnessPlan(t)
	dev := newFake(card)
	dev.readOnly = true
	e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
	if !ErrWriteProtected.Is(e) || Classify(e) != FailureDevice || strings.Contains(e.Error(), "incomplete") || !strings.Contains(e.Error(), "read-only file system") {
		t.Fatal(e)
	}
	// Windows and the Android reader say it in words
	for _, msg := range []string{"The media is write protected.", "Medium is read/write protected (ASC: 39, ASCQ: 0)"} {
		if !WriteProtected(errors.New(msg)) {
			t.Error(msg)
		}
	}
	if WriteProtected(syscall.EIO) {
		t.Error("EIO")
	}
}

// minimum_andr36oid in every form the console takes (RECORD-FORMAT): YYYYMMDD,
// YYYYMMDD.HHMMSS, an eng.NAME.YYYYMMDD.HHMMSS incremental, an identity with one.
func TestHarnessGuestMinimumForms(t *testing.T) {
	im := &firmware.Image{Facts: map[string]string{"layout": "prepared-1", "subsystem": "release-1"}, BuildIdentity: "0.9|eng.kenny.20261117.101500"}
	for _, c := range []struct {
		minimum string
		fits    bool
	}{
		{"20261117", true}, {"20261117.101500", true}, {"20261118", false},
		{"eng.kenny.20261117.101500", true}, {"eng.kenny.20261117.101501", false},
		{"0.9|eng.builder.20261116.000000", true}, {"next week", false},
	} {
		e := GuestFits(im, &Guest{Minimum: c.minimum})
		if (e == nil) != c.fits {
			t.Errorf("%q: %v", c.minimum, e)
		}
	}
}

// Linux keeps what was written to a block device in its page cache; a read-back
// right after the write reads that memory, not the card, and a card that lost the
// bytes passes. Every range is dropped from the cache before it is read back.
func TestHarnessReadBackSkipsTheCache(t *testing.T) {
	im, boot, _, card, _, p := harnessPlan(t)
	type span struct{ off, n int64 }
	dropped := map[span]bool{}
	old := forgetCached
	forgetCached = func(d Device, off, n int64) { dropped[span{off, n}] = true; old(d, off, n) }
	t.Cleanup(func() { forgetCached = old })
	if e := Execute(context.Background(), card, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	for _, w := range p.Writes {
		if !dropped[span{w.Offset, w.Length}] {
			t.Errorf("%s (%d+%d) read back from the cache", w.Name, w.Offset, w.Length)
		}
	}
	part, _ := im.Table.Find("BOOT")
	if !dropped[span{part.Offset(), part.Size()}] {
		t.Error("device setup read back from the cache")
	}
}

// An update of a card that carries a game system (DARKOS, partition 14): Android is
// updated, userdata, games and the game system stay where they are, the partition
// with its number, place and identity, the disk with its identity, and the two BOOT
// records byte for byte (RECORD-FORMAT: the console checks all of them).
func TestHarnessUpdateKeepsTheGameSystem(t *testing.T) {
	im, boot, g, card, size, p := harnessPlan(t)
	if e := Execute(context.Background(), card, im, p, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	tab, e := layout.Read(card, size)
	if e != nil {
		t.Fatal(e)
	}
	files, e := p.BootFiles()
	if e != nil {
		t.Fatal(e)
	}
	q, _ := tab.Find(GuestPartition)
	before := make([]byte, q.Size())
	card.ReadAt(before, q.Offset())
	finger, _ := Snapshot(card, size)
	kept := map[string][]byte{GuestFile: files[GuestFile], LayoutFile: files[LayoutFile]}
	installed := Card{Installed: true, Treble: true, Table: tab, Fingerprint: finger, Kept: kept, Guest: cardGuest(kept[GuestFile])}
	if installed.Guest.Distribution != g.DistributionName() || installed.Guest.Version != g.Darkosen {
		t.Fatalf("card's game system read as %+v", installed.Guest)
	}
	up, e := Build(im, installed, size, "update", "Panels/Panel4", false, 0)
	if e != nil {
		t.Fatal("a card with a game system can't be updated:", e)
	}
	protected := false
	for _, r := range up.Protected {
		protected = protected || r.Offset == q.Offset() && r.Length == q.Size()
	}
	if !protected {
		t.Fatal("the game system partition is not protected by the update")
	}
	for name, b := range kept {
		if !bytes.Equal(up.Keep[name], b) {
			t.Fatalf("%s not kept as it was", name)
		}
	}
	if e = Execute(context.Background(), card, im, up, boot, t.TempDir(), nil); e != nil {
		t.Fatal(e)
	}
	after, e := layout.Read(card, size)
	if e != nil {
		t.Fatal(e)
	}
	nq, ok := after.Find(GuestPartition)
	if !ok || nq != q || after.ID != tab.ID {
		t.Fatalf("game system partition %+v, was %+v; disk %v, was %v", nq, q, after.ID, tab.ID)
	}
	got := make([]byte, q.Size())
	card.ReadAt(got, q.Offset())
	if !bytes.Equal(got, before) {
		t.Fatal("the update changed the game system")
	}
}

// A record without "distribution" is dArkOSen's; a partition without a record is a
// system the flasher can't name.
func TestCardGuestRecord(t *testing.T) {
	for _, c := range []struct {
		record string
		want   CardGuest
	}{
		{`{"format":1,"darkosen":"11072026","revision":1}`, CardGuest{"dArkOSen", "11072026"}},
		{`{"format":1,"distribution":"darkos","darkosen":"11072026"}`, CardGuest{"dArkOS", "11072026"}},
		{`{"format":1,"distribution":"dArkOSRE","darkosen":"01022027"}`, CardGuest{"dArkOSRE", "01022027"}},
		{``, CardGuest{}},
		{`not json`, CardGuest{}},
	} {
		if got := cardGuest([]byte(c.record)); *got != c.want {
			t.Errorf("%q: %+v", c.record, *got)
		}
	}
}

// When the new release's own partitions would reach into the game system, the update
// is refused before anything is written.
func TestUpdateRefusesWhenTheGameSystemIsInTheWay(t *testing.T) {
	im, card, _, _ := sample(t)
	sys, _ := card.Table.Find("system")
	card.Table.Parts = append(card.Table.Parts, layout.Partition{Number: 14, Name: GuestPartition, Start: sys.Start, End: sys.End, Type: layout.LinuxType, ID: layout.GUID()})
	// the old layout had system elsewhere
	for i := range card.Table.Parts {
		if card.Table.Parts[i].Name == "system" {
			card.Table.Parts[i].Start, card.Table.Parts[i].End = 30001, 30100
		}
	}
	_, e := Build(im, card, card.Table.Size, "update", "Panels/Panel4", false, 0)
	if !ErrGuestInTheWay.Is(e) {
		t.Fatal(e)
	}
}

// A card that stops answering in the card tests (as the C13 card did in the speed
// check) is not called incomplete: nothing of the release was written, only test
// blocks, and the error says so.
func TestHarnessCardDiesInCardTest(t *testing.T) {
	im, boot, _, card, _, p := harnessPlan(t)
	dev := newFake(card)
	// every write of the size check (marker and restore) works, then the card is gone
	dev.vanishAfter = int64(2 * len(p.Probes))
	var last string
	e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), func(pr Progress) {
		if pr.Phase == "preflight" {
			last = pr.Name
		}
	})
	if e == nil {
		t.Fatal("no error")
	}
	if last != SizeCheck && last != SpeedCheck {
		t.Fatal("failed after the card test:", last)
	}
	if !StateCardTest.Is(e) || StateIncomplete.Is(e) || Classify(e) != FailureDevice {
		t.Fatalf("%q: %v", Classify(e), e)
	}
}
