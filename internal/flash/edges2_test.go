package flash

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/layout"
	// the card-changed message (platform.ErrCardChanged) the app knows
	_ "github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// Another card put in after the card step (here: its partition table changed)
// stops the write before the first byte, and the error says both that the card
// changed and that nothing was written.
func TestExecuteCardSwappedSaysNothingWritten(t *testing.T) {
	im, boot, _, card, _, p := harnessPlan(t)
	if _, e := card.WriteAt([]byte("another card"), 512); e != nil {
		t.Fatal(e)
	}
	dev := newFake(card)
	e := Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
	if e == nil || dev.writes.Load() != 0 {
		t.Fatal(e, dev.writes.Load())
	}
	if !StateUntouched.Is(e) || StateIncomplete.Is(e) {
		t.Fatal("state:", e)
	}
	if key, _, ok := i18n.Find(e); !ok || !strings.Contains(key, "removed or changed after it was chosen") {
		t.Fatal(key, e)
	}
}

// An image file changed after it was checked stops the write before the first
// byte, says so, and says that nothing was written.
func TestExecuteImageChangedSaysNothingWritten(t *testing.T) {
	im, boot, _, card, _, p := harnessPlan(t)
	changed := *im
	changed.SHA256 = strings.Repeat("0", 64)
	dev := newFake(card)
	e := Execute(context.Background(), dev, &changed, p, boot, t.TempDir(), nil)
	if e == nil || dev.writes.Load() != 0 || !StateUntouched.Is(e) {
		t.Fatal(e, dev.writes.Load())
	}
	if key, _, _ := i18n.Find(e); key != CauseImageChanged.Key() {
		t.Fatal(key)
	}
	// gone: the open fails, also before anything is written
	gone := *im
	gone.Path = im.Path + ".gone"
	e = Execute(context.Background(), dev, &gone, p, boot, t.TempDir(), nil)
	if e == nil || !StateUntouched.Is(e) {
		t.Fatal(e)
	}
	if key, _, _ := i18n.Find(e); key != i18n.CauseFileGone.Key() {
		t.Fatal(key, e)
	}
}

// Two factory-formatted cards of one make and size have the same first and last
// sectors; the volume's serial number in the first partition tells them apart.
func TestSnapshotTellsFactoryCardsApart(t *testing.T) {
	const size = 64 << 20
	newCard := func(serial byte) *os.File {
		f := scratchCard(t, size)
		mbr := make([]byte, 512)
		entry := mbr[446:462]
		entry[4] = 0x07              // exFAT
		entry[8], entry[9] = 0, 0x20 // LBA 8192 (4 MiB)
		mbr[510], mbr[511] = 0x55, 0xaa
		f.WriteAt(mbr, 0)
		vbr := make([]byte, 512)
		copy(vbr[3:], "EXFAT   ")
		vbr[100] = serial
		f.WriteAt(vbr, 8192*512)
		return f
	}
	a, b := newCard(1), newCard(2)
	fa, e1 := Snapshot(a, size)
	fb, e2 := Snapshot(b, size)
	if e1 != nil || e2 != nil || fa == fb {
		t.Fatal(e1, e2, fa == fb)
	}
	again, _ := Snapshot(a, size)
	if again != fa {
		t.Fatal("not stable")
	}
	// a partition start past the end, a GPT's protective MBR: start and end only
	if _, ok := mbrFirstPartition(append(make0(446), 0, 0, 0, 0, 0xee, 0, 0, 0, 1, 0, 0, 0), size); ok {
		t.Fatal("protective MBR")
	}
}

func make0(n int) []byte { return make([]byte, n) }

// A card smaller than the image, and one of exactly its size, each get a message
// of their own and never the general line.
func TestCardSizeAgainstImage(t *testing.T) {
	im, _, _ := harnessImage(t)
	_, e := BuildWith(im, Card{Table: layout.Table{Size: im.Size - 512}}, im.Size-512, "install", "Panels/Panel4", false, 0, Options{})
	if key, _, ok := i18n.Find(e); !ok || key != CauseCardTooSmall.Key() {
		t.Fatal(key, e)
	}
	_, e = BuildWith(im, Card{Table: layout.Table{Size: im.Size}}, im.Size, "install", "Panels/Panel4", false, 0, Options{})
	if e != nil {
		if _, _, ok := i18n.Find(e); !ok {
			t.Fatal("no message:", e)
		}
	}
}
