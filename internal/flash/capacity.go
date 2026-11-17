package flash

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"syscall"
)

// Counterfeit cards claim more than they hold. Past their real size they either
// wrap around (a write lands at its offset modulo the real size, usually a power of
// two because the high address bits are ignored) or drop the write (reads there give
// zeros or old bytes). Each range is read back right after it is written, which a
// wrapping card passes: the range reads back fine, while what it overwrote further
// down is gone. Two checks catch them:
//
//  1. Before an installation writes anything (checkCapacity), a few small markers go
//     to the start of the card, every power of two GiB, the end of the last
//     partition and the card's last sectors; each must read back as itself. A
//     dropped marker reads back as something else, a wrapped one lands on (and
//     replaces) the marker at offset 0 or a lower one. The bytes under the markers
//     are put back afterwards, so a good card is as it was. Never on an update: there
//     the markers would land in the user's data.
//  2. After all writes (verifyEnds), the start and end of every range and a sample
//     every probeStride inside it are read back again, except what a later write
//     replaced, so a wrap the markers missed still fails the verification.

const (
	markerLen    = 4096
	markerMagic  = "andr36oid-size-check\x00"
	endSample    = 4 << 20
	sampleLen    = 1 << 20
	probeStride  = 512 << 20
	probeMinSize = 2 << 30
)

// capacityProbes are the marker ranges for a card of size bytes, in the order they
// are written: the start, every power of two GiB, the end of the usable area and
// the card's last sectors.
func capacityProbes(size int64) []Range {
	if size < probeMinSize {
		return nil
	}
	var out []Range
	add := func(off int64) {
		off = off / 512 * 512
		if off < 0 || off+markerLen > size {
			return
		}
		for _, r := range out {
			if off < r.End() && r.Offset < off+markerLen {
				return
			}
		}
		out = append(out, Range{"Card size check", off, markerLen})
	}
	add(0)
	usableEnd := size - 34*512
	for g := int64(1 << 30); g+markerLen <= usableEnd; g *= 2 {
		add(g)
	}
	add(usableEnd - markerLen)
	add(size - markerLen)
	return out
}

func marker(off int64) []byte {
	b := make([]byte, markerLen)
	rand.Read(b)
	copy(b, markerMagic)
	binary.LittleEndian.PutUint64(b[len(markerMagic):], uint64(off))
	return b
}

// markerOffset is the offset written into b by marker, ok false for no marker.
func markerOffset(b []byte) (int64, bool) {
	if len(b) < len(markerMagic)+8 || !bytes.HasPrefix(b, []byte(markerMagic)) {
		return 0, false
	}
	return int64(binary.LittleEndian.Uint64(b[len(markerMagic):])), true
}

// checkCapacity is check 1 above. It returns an ErrFakeCapacity failure for a card
// that doesn't hold what it claims.
func checkCapacity(ctx context.Context, dst Device, p *Plan, notify func(Progress)) (err error) {
	if e := ctx.Err(); e != nil {
		return e
	}
	notify(Progress{Phase: "preflight", Name: SizeCheck})
	probes := p.Probes
	old := make([][]byte, len(probes))
	for i, r := range probes {
		old[i] = make([]byte, r.Length)
		if _, e := dst.ReadAt(old[i], r.Offset); e != nil {
			return fmt.Errorf("card read check failed: %w", e)
		}
	}
	written := 0
	// The bytes under the markers go back whatever happens, last first so that a
	// wrapping card ends up as it was too.
	defer func() {
		for i := written - 1; i >= 0; i-- {
			if _, e := dst.WriteAt(old[i], probes[i].Offset); e != nil && err == nil {
				err = e
			}
		}
		if written > 0 {
			if e := dst.Sync(); e != nil && err == nil {
				err = e
			}
		}
	}()
	want := make([][]byte, len(probes))
	for i, r := range probes {
		want[i] = marker(r.Offset)
		if _, e := dst.WriteAt(want[i], r.Offset); e != nil {
			return e
		}
		written = i + 1
	}
	if e := dst.Sync(); e != nil {
		return e
	}
	works := p.Size
	bad := false
	got := make([]byte, markerLen)
	for i, r := range probes {
		forgetCached(dst, r.Offset, r.Length)
		if _, e := dst.ReadAt(got, r.Offset); e != nil {
			return e
		}
		if bytes.Equal(got, want[i]) {
			continue
		}
		bad = true
		if r.Offset > 0 {
			works = min(works, r.Offset)
		}
		// another marker here: the one from that offset wrapped onto this one
		if from, ok := markerOffset(got); ok && from != r.Offset && from > 0 {
			works = min(works, from)
		}
	}
	if !bad {
		return nil
	}
	return NewFailure(FailureCapacity, ErrFakeCapacity.New(gigabytes(p.Size), gigabytes(works)))
}

// verifyEnds is check 2 above: it reads back the parts of each written range that
// no later write replaced: its start and end, and a sample every probeStride.
func verifyEnds(ctx context.Context, dst Device, p *Plan, extra []Write, source func(Write) (io.ReaderAt, int64)) error {
	writes := append(append([]Write{}, p.Writes...), extra...)
	for i, w := range writes {
		pieces := []Range{{w.Name, w.Offset, min(endSample, w.Length)}}
		for off := w.Offset + probeStride; off+sampleLen < w.End()-endSample; off += probeStride {
			pieces = append(pieces, Range{w.Name, off, sampleLen})
		}
		if w.Length > endSample {
			n := min(endSample, w.Length-endSample)
			pieces = append(pieces, Range{w.Name, w.End() - n, n})
		}
		for _, later := range writes[i+1:] {
			pieces = subtract(pieces, later.Range)
		}
		r, soff := source(w)
		for _, piece := range pieces {
			forgetCached(dst, piece.Offset, piece.Length)
			if e := verify(ctx, dst, r, piece.Offset, soff+piece.Offset-w.Offset, piece.Length, nil); e != nil {
				return e
			}
		}
	}
	return nil
}

// subtract cuts cut out of the ranges.
func subtract(rs []Range, cut Range) []Range {
	var out []Range
	for _, r := range rs {
		if cut.End() <= r.Offset || cut.Offset >= r.End() {
			out = append(out, r)
			continue
		}
		if cut.Offset > r.Offset {
			out = append(out, Range{r.Name, r.Offset, cut.Offset - r.Offset})
		}
		if cut.End() < r.End() {
			out = append(out, Range{r.Name, cut.End(), r.End() - cut.End()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}

// WriteProtected says whether e is a card refusing writes: the read-only error of
// the system, Windows' "media is write protected", or the SCSI data protect sense of
// the Android app's USB reader.
func WriteProtected(e error) bool {
	if e == nil {
		return false
	}
	if errors.Is(e, syscall.EROFS) {
		return true
	}
	s := strings.ToLower(e.Error())
	return strings.Contains(s, "write protect") || strings.Contains(s, "write-protect") || strings.Contains(s, "read-only file system")
}

// WriteProtectedError is the error to show for e when e is a write-protected card
// (with e as the detail), nil otherwise.
func WriteProtectedError(e error) error {
	if !WriteProtected(e) {
		return nil
	}
	return &detailed{DeviceFailure(ErrWriteProtected.New()), e}
}

// detailed is a message with the error behind it as its technical detail.
type detailed struct {
	msg, detail error
}

func (d *detailed) Error() string { return d.msg.Error() + " (" + d.detail.Error() + ")" }
func (d *detailed) Unwrap() error { return d.msg }
