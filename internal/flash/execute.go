package flash

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

type Progress struct {
	Phase  string `json:"phase"`
	Name   string `json:"name"`
	Done   int64  `json:"done"`
	Total  int64  `json:"total"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	Plan   *Plan  `json:"plan,omitempty"`
	// Speed comes with the "speed" phase just before "complete".
	Speed *CardSpeed `json:"speed,omitempty"`
}
type Device interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}
type backupEntry struct {
	Range
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}
type Backup struct {
	Size      int64         `json:"size"`
	Entries   []backupEntry `json:"entries"`
	Protected []Range       `json:"protected"`
	Anchors   []cardAnchor  `json:"anchors,omitempty"`
}

type cardAnchor struct {
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

// As on Android, sample both ends of every preserved partition. These bytes
// survive the update, unlike the partition table and the backed-up write ranges.
func identityRanges(size int64, protected []Range) ([]Range, error) {
	var samples []Range
	for _, r := range protected {
		if r.Offset < 0 || r.Length <= 0 || r.Offset > size || r.Length > size-r.Offset {
			return nil, errors.New("Recovery refused: the saved card identity is invalid.")
		}
		n := min(r.Length, 64<<10)
		for _, off := range []int64{r.Offset, r.End() - n} {
			samples = append(samples, Range{Offset: off, Length: n})
		}
	}
	return samples, nil
}

func verifyBackupIdentity(ctx context.Context, dev io.ReaderAt, b *Backup) error {
	if len(b.Anchors) == 0 {
		return errors.New("Recovery refused: this backup has no saved card identity, so the original card cannot be confirmed.")
	}
	samples, e := identityRanges(b.Size, b.Protected)
	if e != nil {
		return e
	}
	if len(samples) != len(b.Anchors) {
		return errors.New("Recovery refused: the saved card identity is invalid.")
	}
	for i, sample := range samples {
		anchor := b.Anchors[i]
		if anchor.Offset != sample.Offset || anchor.Length != sample.Length {
			return errors.New("Recovery refused: the saved card identity is invalid.")
		}
		sum, e := firmware.Hash(ctx, io.NewSectionReader(dev, sample.Offset, sample.Length), sample.Length, nil)
		if e != nil {
			return fmt.Errorf("Recovery refused: the original card could not be confirmed because its preserved partitions could not be read: %w", e)
		}
		if sum != anchor.SHA256 {
			return errors.New("Recovery refused: the preserved partitions do not match this backup. Select the original card and its matching backup.")
		}
	}
	return nil
}

func copyRange(ctx context.Context, dst io.WriterAt, src io.ReaderAt, doff, soff, n int64, cb func(int64)) error {
	buf := make([]byte, 4<<20)
	for off := int64(0); off < n; {
		if e := ctx.Err(); e != nil {
			return e
		}
		sz := int64(len(buf))
		if n-off < sz {
			sz = n - off
		}
		if _, e := src.ReadAt(buf[:sz], soff+off); e != nil {
			return e
		}
		w, e := dst.WriteAt(buf[:sz], doff+off)
		if e != nil {
			return e
		}
		if int64(w) != sz {
			return io.ErrShortWrite
		}
		off += sz
		if cb != nil {
			cb(off)
		}
	}
	return nil
}
func verify(ctx context.Context, dst, src io.ReaderAt, doff, soff, n int64, cb func(int64)) error {
	a := make([]byte, 4<<20)
	b := make([]byte, len(a))
	for off := int64(0); off < n; {
		if e := ctx.Err(); e != nil {
			return e
		}
		sz := int64(len(a))
		if n-off < sz {
			sz = n - off
		}
		if _, e := dst.ReadAt(a[:sz], doff+off); e != nil {
			return e
		}
		if _, e := src.ReadAt(b[:sz], soff+off); e != nil {
			return e
		}
		if !bytes.Equal(a[:sz], b[:sz]) {
			return NewFailure(FailureVerify, fmt.Errorf("read-back mismatch at byte %d", doff+off))
		}
		off += sz
		if cb != nil {
			cb(off)
		}
	}
	return nil
}

// forgetCached is dropCached; tests replace it to see that every read-back is preceded
// by it.
var forgetCached = dropCached

func Execute(ctx context.Context, dst Device, im *firmware.Image, p *Plan, bootPath, backupDir string, notify func(Progress)) error {
	if e := p.Validate(); e != nil {
		return e
	}
	if e := checkCompressedUserdata(dst, p, im.Facts["f2fs_compression"]); e != nil {
		return e
	}
	dst = &guardedDevice{Device: deviceErrors{dst}, plan: p}
	src, e := os.Open(im.Path)
	if e != nil {
		return e
	}
	defer src.Close()
	var classicUboot []byte
	for _, w := range p.Writes {
		if !w.Uboot {
			continue
		}
		if classicUboot != nil {
			return errors.New("duplicate classic U-Boot write")
		}
		classicUboot, e = firmware.ImageBootloader(im)
		if e != nil {
			return e
		}

	}
	boot, e := os.Open(bootPath)
	if e != nil {
		return e
	}
	defer boot.Close()
	bst, e := boot.Stat()
	if e != nil {
		return e
	}
	var system *os.File
	var systemSize int64
	if p.SystemImage != "" {
		if system, e = os.Open(p.SystemImage); e != nil {
			return e
		}
		defer system.Close()
		sst, e := system.Stat()
		if e != nil {
			return e
		}
		systemSize = sst.Size()
	}
	var guest *os.File
	if p.Guest != nil {
		if guest, e = os.Open(p.Guest.Path); e != nil {
			return e
		}
		defer guest.Close()
		gst, e := guest.Stat()
		if e != nil {
			return e
		}
		if gst.Size() != p.Guest.Size {
			return errors.New("the game system download is not the size its release says")
		}
	}
	for _, w := range p.Writes {
		if w.Data != nil {
			if int64(len(w.Data)) != w.Length {
				return errors.New("invalid partition table payload")
			}
			continue
		}
		if w.Zero {
			continue
		}
		if w.Guest {
			if guest == nil || w.Length != p.Guest.Size {
				return errors.New("game system download missing")
			}
			continue
		}
		available := im.Size
		off := w.SourceOffset
		if w.Boot {
			available = bst.Size()
			off = 0
		}
		if w.System {
			if system == nil {
				return errors.New("system image missing")
			}
			available = systemSize
			off = 0
		}
		if off < 0 || off > available || w.Length > available-off {
			return errors.New("source range outside prepared image")
		}
	}

	if notify == nil {
		notify = func(Progress) {}
	}
	notify(Progress{Phase: "preflight", Name: "Checking source image", Plan: p})
	sum, e := firmware.Hash(ctx, src, im.Size, func(n, total int64) {
		notify(Progress{Phase: "preflight", Name: "Checking source image", Done: n, Total: total})
	})
	if e != nil {
		return e
	}
	if sum != im.SHA256 {
		return errors.New("source image changed after inspection")
	}
	if guest != nil {
		gsum, e := firmware.Hash(ctx, guest, p.Guest.Size, func(n, total int64) {
			notify(Progress{Phase: "preflight", Name: "Checking the game system", Done: n, Total: total})
		})
		if e != nil {
			return e
		}
		if gsum != p.Guest.SHA256 {
			return errors.New("the game system download is damaged; download it again")
		}
	}
	snap, e := Snapshot(dst, p.Size)
	if e != nil {
		return e
	}
	if snap != p.OldFingerprint {
		return errors.New("card contents changed since preflight; inspect it again")
	}
	// A card or reader that can't read stops here, before the first write. Only the
	// start and end of each range: reading all of it took as long as the verify, and
	// updates read everything they replace into the recovery backup anyway.
	const sample = 4 << 20
	notify(Progress{Phase: "preflight", Name: "Reading the card before writing"})
	for i, w := range p.Writes {
		for _, r := range [][2]int64{{0, min(sample, w.Length)}, {max(0, w.Length-sample), min(sample, w.Length)}} {
			if _, e = firmware.Hash(ctx, io.NewSectionReader(dst, w.Offset+r[0], r[1]), r[1], nil); e != nil {
				return fmt.Errorf("card read check failed: %w; %w", e, StateUntouched.New())
			}
		}
		notify(Progress{Phase: "preflight", Name: "Reading the card before writing", Done: int64(i + 1), Total: int64(len(p.Writes))})
	}
	// A counterfeit card stops here, before the release is written (capacity.go).
	if p.Mode == "install" && len(p.Probes) > 0 {
		if e = checkCapacity(ctx, dst, p, notify); e != nil {
			if wp := WriteProtectedError(e); wp != nil && !dst.(*guardedDevice).wrote.Load() {
				return wp
			}
			if Classify(e) == FailureCapacity {
				return e
			}
			return fmt.Errorf("%w; %w", e, StateIncomplete.New())
		}
	}
	var backup *Backup
	if p.Mode == "update" {
		backup, e = saveBackup(ctx, dst, p, backupDir, notify)
		if e != nil {
			return fmt.Errorf("recovery backup failed: %w; %w", e, StateUntouched.New())
		}
	}
	sourceFor := func(w Write) (io.ReaderAt, int64) {
		if w.Data != nil {
			return bytes.NewReader(w.Data), 0
		}
		if w.Zero {
			return zeros{}, 0
		}
		if w.Guest {
			return guest, 0
		}
		if w.Boot {
			return boot, 0
		}
		if w.System {
			return system, 0
		}
		return src, w.SourceOffset
	}
	var total int64
	for _, w := range p.Writes {
		total += w.Length
	}
	var done int64
	fail := func(cause error) error {
		// a card that refuses the very first write still has all it had
		if wp := WriteProtectedError(cause); wp != nil && !dst.(*guardedDevice).wrote.Load() {
			return wp
		}
		if backup != nil {
			notify(Progress{Phase: "restoring", Name: "Restoring the previous installation"})
			if e := restore(context.Background(), dst, backupDir, backup); e != nil {
				return fmt.Errorf("%w; %w: %v; %w", cause, ErrRestoreFailed, e, StateRestoreFailed.New(backupDir))
			}
			return fmt.Errorf("%w; %w", cause, StateRestored.New())
		}
		return fmt.Errorf("%w; %w", cause, StateIncomplete.New())
	}
	notify(Progress{Phase: "preflight", Name: SpeedCheck})
	speed, e := measureLatency(ctx, dst, p)
	if e != nil {
		return fail(e)
	}
	var bulkBytes int64
	var bulkTime time.Duration
	for _, w := range p.Writes {
		r, off := sourceFor(w)
		started := time.Now()
		if w.Uboot {
			e = firmware.WriteBootloaderSlot(classicWriteDevice{dst}, classicUboot, func(n, totalCopy int64) {
				notify(Progress{Phase: "writing", Name: w.Name, Done: done + n, Total: total, Offset: w.Offset, Length: n})
			})
		} else {
			e = copyRange(ctx, dst, r, w.Offset, off, w.Length, func(n int64) {
				notify(Progress{Phase: "writing", Name: w.Name, Done: done + n, Total: total, Offset: w.Offset, Length: n})
			})
		}
		if e != nil {
			return fail(e)
		}
		if e = dst.Sync(); e != nil {
			return fail(e)
		}
		if w.Length >= 64<<20 {
			bulkBytes += w.Length
			bulkTime += time.Since(started)
		}
		forgetCached(dst, w.Offset, w.Length)
		if e = verify(ctx, dst, r, w.Offset, off, w.Length, func(n int64) {
			notify(Progress{Phase: "verifying", Name: w.Name, Done: done + n, Total: total, Offset: w.Offset, Length: n})
		}); e != nil {
			return fail(e)
		}
		done += w.Length
	}
	// Fresh installs write a prepared BOOT after the source payload; updates already included it.
	var extra []Write
	if p.Mode == "install" {
		part, _ := im.Table.Find("BOOT")
		if e = copyRange(ctx, dst, boot, part.Offset(), 0, part.Size(), func(n int64) {
			notify(Progress{Phase: "writing", Name: "Device setup", Done: n, Total: part.Size(), Offset: part.Offset(), Length: n})
		}); e != nil {
			return fail(e)
		}
		if e = dst.Sync(); e != nil {
			return fail(e)
		}
		forgetCached(dst, part.Offset(), part.Size())
		if e = verify(ctx, dst, boot, part.Offset(), 0, part.Size(), nil); e != nil {
			return fail(e)
		}
		notify(Progress{Phase: "verifying", Name: "Device setup", Done: part.Size(), Total: part.Size(), Offset: part.Offset(), Length: part.Size()})
		extra = append(extra, Write{Range: Range{"Device setup", part.Offset(), part.Size()}, Boot: true})
	}
	// What a later write may have wrapped onto (capacity.go)
	notify(Progress{Phase: "verifying", Name: "Checking the whole card once more"})
	if e = verifyEnds(ctx, dst, p, extra, sourceFor); e != nil {
		return fail(e)
	}
	if bulkTime > 0 {
		speed.WriteMBps = float64(bulkBytes) / (1 << 20) / bulkTime.Seconds()
	}
	speed.judge()
	notify(Progress{Phase: "speed", Name: "Card speed", Speed: speed})
	notify(Progress{Phase: "complete", Name: "Written and verified", Done: total, Total: total})
	return nil
}
func saveBackup(ctx context.Context, dev io.ReaderAt, p *Plan, dir string, notify func(Progress)) (*Backup, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	b := &Backup{Size: p.Size, Protected: p.Protected}
	samples, e := identityRanges(p.Size, p.Protected)
	if e != nil {
		return nil, e
	}
	for _, sample := range samples {
		sum, e := firmware.Hash(ctx, io.NewSectionReader(dev, sample.Offset, sample.Length), sample.Length, nil)
		if e != nil {
			return nil, e
		}
		b.Anchors = append(b.Anchors, cardAnchor{Offset: sample.Offset, Length: sample.Length, SHA256: sum})
	}
	for i, w := range p.Writes {
		name := fmt.Sprintf("region-%03d.bin", i)
		f, e := os.OpenFile(filepath.Join(dir, name), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		e = copyRange(ctx, f, dev, 0, w.Offset, w.Length, func(n int64) { notify(Progress{Phase: "backup", Name: w.Name, Done: n, Total: w.Length}) })
		if e == nil {
			e = f.Sync()
		}
		if e == nil {
			e = verify(ctx, f, dev, 0, w.Offset, w.Length, nil)
		}
		var sum string
		if e == nil {
			sum, e = firmware.Hash(ctx, f, w.Length, nil)
		}
		f.Close()
		if e != nil {
			return nil, e
		}
		b.Entries = append(b.Entries, backupEntry{w.Range, name, sum})
	}
	j, e := json.MarshalIndent(b, "", "  ")
	if e != nil {
		return nil, e
	}
	f, e := os.OpenFile(filepath.Join(dir, "recovery.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if e != nil {
		return nil, e
	}
	_, e = f.Write(j)
	if e == nil {
		e = f.Sync()
	}
	f.Close()
	return b, e
}
func restore(ctx context.Context, dst Device, dir string, b *Backup) error {
	// Check the complete backup before restoring any part of it.
	for _, ent := range b.Entries {
		f, e := os.Open(filepath.Join(dir, ent.File))
		if e != nil {
			return e
		}
		sum, e := firmware.Hash(ctx, f, ent.Length, nil)
		f.Close()
		if e != nil {
			return e
		}
		if sum != ent.SHA256 {
			return errors.New("recovery backup checksum mismatch")
		}
		for _, r := range b.Protected {
			if ent.Offset < r.End() && r.Offset < ent.End() {
				return errors.New("backup overlaps protected data")
			}
		}
	}
	for _, ent := range b.Entries {
		f, e := os.Open(filepath.Join(dir, ent.File))
		if e != nil {
			return e
		}
		if ent.Offset == firmware.BootloaderOffset && ent.Length == firmware.BootloaderSize {
			// Recovery is also a U-Boot update. Keep a complete old/new copy
			// throughout instead of restoring the whole slot in one write.
			data, readErr := io.ReadAll(io.LimitReader(f, firmware.BootloaderSize+1))
			if readErr != nil {
				e = readErr
			} else if firmware.CheckBootloaderContainer(data) != nil {
				// Restore a checksummed damaged backup exactly as saved.
				// It may still have one usable copy, or be a repair fixture.
				e = copyRange(ctx, dst, bytes.NewReader(data), ent.Offset, 0, ent.Length, nil)
			} else {
				e = firmware.WriteBootloaderSlot(classicWriteDevice{dst}, data, nil)
			}
		} else {
			e = copyRange(ctx, dst, f, ent.Offset, 0, ent.Length, nil)
		}
		if e == nil {
			e = dst.Sync()
		}
		if e == nil {
			forgetCached(dst, ent.Offset, ent.Length)
			e = verify(ctx, dst, f, ent.Offset, 0, ent.Length, nil)
		}
		f.Close()
		if e != nil {
			return e
		}
	}
	return nil
}
func Digest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

// The final write boundary is enforced independently of the operation loop.
type guardedDevice struct {
	Device
	plan *Plan
	// wrote: a write reached the card
	wrote atomic.Bool
}

// SpeedCheck is the progress of the speed check, the first step that writes to the
// card (Progress.Writing).
const SpeedCheck = "Checking card speed"

// SizeCheck is the progress of checkCapacity, which writes markers (and puts back
// what was there).
const SizeCheck = "Checking the card size"

// Writing says whether an Execute that sent p has begun to change the card: after a
// failure from here on the card is incomplete (or restored, for an update).
func (p Progress) Writing() bool {
	switch p.Phase {
	case "writing", "verifying", "restoring", "speed", "complete":
		return true
	}
	return p.Name == SpeedCheck || p.Name == SizeCheck
}

func (g *guardedDevice) WriteAt(b []byte, off int64) (int, error) {
	n := int64(len(b))
	if off < 0 || off > g.plan.Size || n > g.plan.Size-off {
		return 0, errors.New("write outside card")
	}
	for _, r := range g.plan.Protected {
		if off < r.End() && r.Offset < off+n {
			return 0, fmt.Errorf("blocked write into %s", r.Name)
		}
	}
	allowed := false
	for _, w := range g.plan.Writes {
		allowed = allowed || off >= w.Offset && off+n <= w.End()
	}
	for _, r := range g.plan.Probes {
		allowed = allowed || off >= r.Offset && off+n <= r.End()
	}
	if !allowed {
		return 0, errors.New("write outside the approved plan")
	}
	w, e := g.Device.WriteAt(b, off)
	if w > 0 {
		g.wrote.Store(true)
	}
	return w, e
}

// RestoreBackup verifies the saved regions completely before opening a write path.
// Android verifies its separately saved card identity before calling this.
func RestoreBackup(ctx context.Context, dst Device, dir string, size int64) error {
	return restoreBackup(ctx, dst, dir, size, false)
}

// RestoreBackupForCard also requires the preserved content to identify the
// original card. Desktop recovery must not rely on reader metadata alone.
func RestoreBackupForCard(ctx context.Context, dst Device, dir string, size int64) error {
	return restoreBackup(ctx, dst, dir, size, true)
}

func restoreBackup(ctx context.Context, dst Device, dir string, size int64, checkIdentity bool) error {
	f, e := os.Open(filepath.Join(dir, "recovery.json"))
	if e != nil {
		return e
	}
	defer f.Close()
	var b Backup
	if e = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&b); e != nil {
		return e
	}
	if b.Size != size || len(b.Entries) == 0 || len(b.Entries) > 32 {
		return errors.New("backup does not match the destination")
	}
	p := &Plan{Size: size, Protected: b.Protected}
	for i, ent := range b.Entries {
		if ent.File != fmt.Sprintf("region-%03d.bin", i) {
			return errors.New("invalid recovery file name")
		}
		p.Writes = append(p.Writes, Write{Range: ent.Range})
	}
	if e = p.Validate(); e != nil {
		return e
	}
	if checkIdentity {
		if e = verifyBackupIdentity(ctx, dst, &b); e != nil {
			return e
		}
	}
	return restore(ctx, &guardedDevice{Device: deviceErrors{dst}, plan: p}, dir, &b)
}

type zeros struct{}

func (zeros) ReadAt(b []byte, _ int64) (int, error) {
	clear(b)
	return len(b), nil
}

// Keep the existing platform read-back flush through the write-boundary wrappers.
type classicWriteDevice struct{ Device }

func (d classicWriteDevice) DiscardReadCache(off, n int64) error {
	forgetCached(d.Device, off, n)
	return nil
}
