package flash

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

func TestDesktopRecoveryRestoresOriginalCard(t *testing.T) {
	im, card, dst, boot := sample(t)
	p, e := Build(im, card, card.Table.Size, "update", "Panels/Panel4", false, 0)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	before, e := firmware.Hash(ctx, dst, p.Size, nil)
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e = Execute(ctx, dst, im, p, boot, dir, nil); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(dir, "recovery.json"))
	if e != nil {
		t.Fatal(e)
	}
	var b Backup
	if e = json.Unmarshal(raw, &b); e != nil {
		t.Fatal(e)
	}
	if len(b.Anchors) != 2*len(p.Protected) || len(b.Anchors) == 0 {
		t.Fatal("backup did not save both ends of every preserved partition")
	}
	for i, r := range p.Protected {
		n := min(r.Length, 64<<10)
		for j, off := range []int64{r.Offset, r.End() - n} {
			a := b.Anchors[2*i+j]
			if a.Offset != off || a.Length != n {
				t.Fatalf("wrong identity sample for %s: %+v", r.Name, a)
			}
		}
	}
	if e = RestoreBackupForCard(ctx, dst, dir, p.Size); e != nil {
		t.Fatal(e)
	}
	after, e := firmware.Hash(ctx, dst, p.Size, nil)
	if e != nil || before != after {
		t.Fatalf("recovery did not restore the exact original card: %v", e)
	}
}

type unreadableIdentity struct {
	Device
	offset int64
}

func (d *unreadableIdentity) ReadAt(p []byte, off int64) (int, error) {
	if off <= d.offset && d.offset-off < int64(len(p)) {
		return 0, io.ErrUnexpectedEOF
	}
	return d.Device.ReadAt(p, off)
}

func TestDesktopRecoveryRefusesUnconfirmedCardWithoutWrites(t *testing.T) {
	for _, scenario := range []string{
		"replacement card", "changed end sample", "legacy backup", "missing sample",
		"negative offset", "oversized sample", "sample outside preserved data",
		"overflowing protected range", "no protected data", "unreadable card",
	} {
		t.Run(scenario, func(t *testing.T) {
			im, card, dst, _ := sample(t)
			p, e := Build(im, card, card.Table.Size, "update", "Panels/Panel4", false, 0)
			if e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			dir := t.TempDir()
			b, e := saveBackup(ctx, dst, p, dir, func(Progress) {})
			if e != nil {
				t.Fatal(e)
			}
			counted := &faulty{File: dst}
			var dev Device = counted
			want := "saved card identity is invalid"
			switch scenario {
			case "replacement card", "changed end sample":
				// Capacity, path and partition-table fingerprint still match.
				// Only preserved filesystem content distinguishes this card.
				i := 0
				if scenario == "changed end sample" {
					i = len(b.Anchors) - 1
				}
				if _, e = dst.WriteAt([]byte{0x12}, b.Anchors[i].Offset); e != nil {
					t.Fatal(e)
				}
				fingerprint, e := Snapshot(dst, p.Size)
				if e != nil || fingerprint != p.OldFingerprint {
					t.Fatalf("fixture changed the partition-table fingerprint: %v", e)
				}
				want = "preserved partitions do not match"
			case "legacy backup":
				b.Anchors = nil
				want = "no saved card identity"
			case "missing sample":
				b.Anchors = b.Anchors[:len(b.Anchors)-1]
			case "negative offset":
				b.Anchors[0].Offset = -1
			case "oversized sample":
				b.Anchors[0].Length = 64<<10 + 1
			case "sample outside preserved data":
				b.Anchors[0].Offset = p.Writes[0].Offset
			case "overflowing protected range":
				b.Protected = []Range{{Offset: p.Size, Length: 1<<63 - 1}}
			case "no protected data":
				b.Protected = nil
			case "unreadable card":
				dev = &unreadableIdentity{Device: counted, offset: b.Anchors[0].Offset}
				want = "preserved partitions could not be read"
			}
			raw, e := json.Marshal(b)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, "recovery.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			e = RestoreBackupForCard(ctx, dev, dir, p.Size)
			if e == nil || !strings.Contains(e.Error(), want) {
				t.Fatalf("expected refusal containing %q, got %v", want, e)
			}
			if counted.writes != 0 {
				t.Fatal("unconfirmed recovery attempted to write to the card")
			}
		})
	}
}

func TestUpdateIdentityReadFailureStopsBeforeWrites(t *testing.T) {
	im, card, dst, boot := sample(t)
	p, e := Build(im, card, card.Table.Size, "update", "Panels/Panel4", false, 0)
	if e != nil {
		t.Fatal(e)
	}
	counted := &faulty{File: dst}
	dev := &unreadableIdentity{Device: counted, offset: p.Protected[0].Offset}
	e = Execute(context.Background(), dev, im, p, boot, t.TempDir(), nil)
	if !errors.Is(e, io.ErrUnexpectedEOF) || !strings.Contains(e.Error(), "no card bytes were written") {
		t.Fatalf("expected backup read failure, got %v", e)
	}
	if counted.writes != 0 {
		t.Fatal("update wrote before saving the card identity")
	}
}
