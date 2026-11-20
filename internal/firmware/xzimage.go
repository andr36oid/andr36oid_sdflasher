package firmware

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xi2/xz"
)

// Release images are .img.xz made with xz -T0: one stream of independent blocks, with
// every block's compressed and uncompressed size in the index at the end. The blocks
// are unpacked in parallel, each straight to its place in the staged file, so a phone
// with several cores isn't held up by the single-threaded LZMA2 decoder. Each block is
// checked by its own checksum (CRC64 with xz's default) and its sizes against the index.
// A file that isn't one plain stream (concatenated streams, stream padding) is unpacked
// in order instead. xi2/xz (a port of xz-embedded) decodes about 30 times faster than
// ulikunitz/xz, which managed little more than 1 MB/s on a release image.

var (
	xzMagic       = []byte{0xfd, '7', 'z', 'X', 'Z', 0}
	xzFooterMagic = []byte{'Y', 'Z'}
)

// at most this many blocks are unpacked at once; each decoder holds the block's
// dictionary (64 MiB at xz -9), so phones stay well inside their memory
var xzWorkers = min(runtime.NumCPU(), 4)

// how often progress is reported while blocks are unpacked
var xzProgressEvery = 200 * time.Millisecond

type xzBlock struct {
	offset       int64 // in the .xz file
	unpadded     int64 // block header + data + check, as in the index
	uncompressed int64
	out          int64 // offset in the image
}

type xzLayout struct {
	header [12]byte // stream header, copied into each block's own stream
	flags  [2]byte  // stream flags (check type)
	blocks []xzBlock
	size   int64 // the image
}

// readVarint reads xz's multibyte integer (7 bits a byte, at most 9 bytes).
func readVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 9; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			if i > 0 && b[i] == 0 {
				return 0, 0, errors.New("xz: index number is not minimal")
			}
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("xz: bad number in index")
}

func putVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// xzLayoutOf reads the stream header, footer and index of a single-stream .xz file.
// errNotSingleStream means the file is valid xz as far as that goes, but has to be
// read in order.
var errNotSingleStream = errors.New("xz: not a single plain stream")

func xzLayoutOf(f io.ReaderAt, size int64) (*xzLayout, error) {
	var l xzLayout
	if size < 32 {
		return nil, errors.New("xz: file too short")
	}
	if _, e := f.ReadAt(l.header[:], 0); e != nil {
		return nil, e
	}
	if !bytes.Equal(l.header[:6], xzMagic) {
		return nil, errors.New("not an xz file")
	}
	if crc32.ChecksumIEEE(l.header[6:8]) != binary.LittleEndian.Uint32(l.header[8:12]) {
		return nil, errors.New("xz: stream header is damaged")
	}
	copy(l.flags[:], l.header[6:8])
	var foot [12]byte
	if _, e := f.ReadAt(foot[:], size-12); e != nil {
		return nil, e
	}
	if !bytes.Equal(foot[10:12], xzFooterMagic) {
		// stream padding after the footer, or not an xz end at all
		return nil, errNotSingleStream
	}
	if crc32.ChecksumIEEE(foot[4:10]) != binary.LittleEndian.Uint32(foot[0:4]) {
		return nil, errors.New("xz: stream footer is damaged")
	}
	if !bytes.Equal(foot[8:10], l.flags[:]) {
		// flags differ: the footer belongs to a later stream
		return nil, errNotSingleStream
	}
	indexSize := (int64(binary.LittleEndian.Uint32(foot[4:8])) + 1) * 4
	indexAt := size - 12 - indexSize
	if indexAt < 12 || indexSize > 16<<20 {
		return nil, errors.New("xz: index size is out of range")
	}
	index := make([]byte, indexSize)
	if _, e := f.ReadAt(index, indexAt); e != nil {
		return nil, e
	}
	if index[0] != 0 {
		return nil, errors.New("xz: index is damaged")
	}
	if crc32.ChecksumIEEE(index[:indexSize-4]) != binary.LittleEndian.Uint32(index[indexSize-4:]) {
		return nil, errors.New("xz: index is damaged")
	}
	b := index[1 : indexSize-4]
	count, n, e := readVarint(b)
	if e != nil {
		return nil, e
	}
	b = b[n:]
	if count > uint64(len(b)/2) {
		return nil, errors.New("xz: index is damaged")
	}
	at := int64(12)
	for i := uint64(0); i < count; i++ {
		unpadded, n, e := readVarint(b)
		if e != nil {
			return nil, e
		}
		b = b[n:]
		uncompressed, n, e := readVarint(b)
		if e != nil {
			return nil, e
		}
		b = b[n:]
		if unpadded < 5 || unpadded > uint64(size) || uncompressed > uint64(MaxImageSize) {
			return nil, errors.New("xz: index is damaged")
		}
		l.blocks = append(l.blocks, xzBlock{offset: at, unpadded: int64(unpadded),
			uncompressed: int64(uncompressed), out: l.size})
		at += (int64(unpadded) + 3) &^ 3
		l.size += int64(uncompressed)
		if l.size > MaxImageSize {
			return nil, errTooLarge()
		}
	}
	// what is left is the index padding, which must be zero
	for _, c := range b {
		if c != 0 {
			return nil, errors.New("xz: index is damaged")
		}
	}
	if at != indexAt {
		// more than one stream: the blocks don't end where this stream's index starts
		return nil, errNotSingleStream
	}
	return &l, nil
}

// blockStream is the block as a stream of its own: the file's stream header, the
// block, an index with this block's record, and a footer. The xz reader then checks
// the block's checksum and that its real sizes match the record.
func (l *xzLayout) blockStream(f io.ReaderAt, b xzBlock) io.Reader {
	padded := (b.unpadded + 3) &^ 3
	index := putVarint(putVarint(putVarint([]byte{0}, 1), uint64(b.unpadded)), uint64(b.uncompressed))
	for len(index)%4 != 0 {
		index = append(index, 0)
	}
	index = binary.LittleEndian.AppendUint32(index, crc32.ChecksumIEEE(index))
	foot := make([]byte, 12)
	binary.LittleEndian.PutUint32(foot[4:8], uint32(len(index)/4-1))
	copy(foot[8:10], l.flags[:])
	binary.LittleEndian.PutUint32(foot[0:4], crc32.ChecksumIEEE(foot[4:10]))
	copy(foot[10:12], xzFooterMagic)
	return io.MultiReader(bytes.NewReader(l.header[:]), io.NewSectionReader(f, b.offset, padded),
		bytes.NewReader(index), bytes.NewReader(foot))
}

// copyCtx copies until EOF, stopping when ctx is done, and counts into done.
func copyCtx(ctx context.Context, w io.Writer, r io.Reader, limit int64, done *atomic.Int64) (int64, error) {
	buf := make([]byte, 1<<20)
	var n int64
	for {
		if e := ctx.Err(); e != nil {
			return n, e
		}
		m, re := r.Read(buf)
		if m > 0 {
			n += int64(m)
			if n > limit {
				return n, errTooLarge()
			}
			if _, e := w.Write(buf[:m]); e != nil {
				return n, e
			}
			done.Add(int64(m))
		}
		if re == io.EOF {
			return n, nil
		}
		if re != nil {
			return n, fmt.Errorf("damaged archive: %w", re)
		}
	}
}

// stageXZ unpacks src into a new file in dir. digest, if given, gets the image's
// bytes in order.
func stageXZ(ctx context.Context, src, dir string, progress func(int64, int64), digest hash.Hash) (string, error) {
	in, e := os.Open(src)
	if e != nil {
		return "", e
	}
	defer in.Close()
	st, e := in.Stat()
	if e != nil {
		return "", e
	}
	layout, e := xzLayoutOf(in, st.Size())
	if e != nil && !errors.Is(e, errNotSingleStream) {
		return "", fmt.Errorf("damaged archive: %w", e)
	}
	// the index says how large the image is; an archive of several streams
	// doesn't, at least the packed size then
	need := st.Size()
	if layout != nil {
		need = layout.size
	}
	if e = CheckSpace(dir, need); e != nil {
		return "", e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	dst, e := os.CreateTemp(dir, "image-*.img")
	if e != nil {
		return "", e
	}
	name := dst.Name()
	ok := false
	defer func() {
		dst.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	if layout == nil {
		e = unpackXZInOrder(ctx, in, dst, progress, digest)
	} else {
		e = unpackXZBlocks(ctx, in, dst, layout, progress, digest)
	}
	if e != nil {
		return "", SpaceError(dir, need, e)
	}
	if e = dst.Sync(); e != nil {
		return "", e
	}
	ok = true
	return name, nil
}

func unpackXZInOrder(ctx context.Context, in io.Reader, dst io.Writer, progress func(int64, int64), digest hash.Hash) error {
	r, e := xz.NewReader(in, 0)
	if e != nil {
		return fmt.Errorf("damaged archive: %w", e)
	}
	out := dst
	if digest != nil {
		out = io.MultiWriter(dst, digest)
	}
	var done atomic.Int64
	stop := reportProgress(&done, 0, progress)
	_, e = copyCtx(ctx, out, r, MaxImageSize, &done)
	stop()
	return e
}

func unpackXZBlocks(ctx context.Context, in io.ReaderAt, dst *os.File, l *xzLayout, progress func(int64, int64), digest hash.Hash) error {
	if e := dst.Truncate(l.size); e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		done     atomic.Int64
		mu       sync.Mutex
		firstErr error
	)
	fail := func(e error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = e
		}
		mu.Unlock()
		cancel()
	}
	finished := make([]chan struct{}, len(l.blocks))
	for i := range finished {
		finished[i] = make(chan struct{})
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < max(1, xzWorkers); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				b := l.blocks[i]
				e := func() error {
					r, e := xz.NewReader(l.blockStream(in, b), 0)
					if e != nil {
						return fmt.Errorf("damaged archive: %w", e)
					}
					r.Multistream(false)
					n, e := copyCtx(ctx, io.NewOffsetWriter(dst, b.out), r, b.uncompressed, &done)
					if e != nil {
						return e
					}
					if n != b.uncompressed {
						return errors.New("damaged archive: a block is shorter than its index entry")
					}
					return nil
				}()
				if e != nil {
					fail(e)
					return
				}
				close(finished[i])
			}
		}()
	}
	// the digest reads each block back once it is written, in order, while later
	// blocks are still being unpacked
	hashed := make(chan error, 1)
	go func() {
		if digest == nil {
			hashed <- nil
			return
		}
		buf := make([]byte, 4<<20)
		for i, b := range l.blocks {
			select {
			case <-finished[i]:
			case <-ctx.Done():
				hashed <- ctx.Err()
				return
			}
			if _, e := io.CopyBuffer(digest, io.NewSectionReader(dst, b.out, b.uncompressed), buf); e != nil {
				hashed <- e
				return
			}
		}
		hashed <- nil
	}()
	stop := reportProgress(&done, l.size, progress)
feed:
	for i := range l.blocks {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	he := <-hashed
	stop()
	if firstErr != nil {
		return firstErr
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	return he
}

// reportProgress calls progress from one goroutine every xzProgressEvery and once at
// the end; total 0 means unknown, reported as the bytes so far.
func reportProgress(done *atomic.Int64, total int64, progress func(int64, int64)) (stop func()) {
	if progress == nil {
		return func() {}
	}
	quit := make(chan struct{})
	exited := make(chan struct{})
	report := func() {
		n := done.Load()
		t := total
		if t == 0 {
			t = n
		}
		progress(n, t)
	}
	go func() {
		defer close(exited)
		tick := time.NewTicker(xzProgressEvery)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				report()
			case <-quit:
				report()
				return
			}
		}
	}()
	return func() {
		close(quit)
		<-exited
	}
}

// UnpackXZ unpacks the .xz archive in (size bytes long) into dst the way a release
// image is unpacked: its blocks in parallel when it is one plain stream with an
// index (xz -T0), in order otherwise. want is the size the unpacked file must have:
// an archive whose index says otherwise is refused before anything is unpacked, and
// the in-order way stops past it. progress counts the unpacked bytes against want,
// so a time left can be worked out from it; digest, if given, gets the bytes in
// order. A damaged archive gives an error starting "damaged archive".
func UnpackXZ(ctx context.Context, in io.ReaderAt, size int64, dst *os.File, want int64, progress func(int64, int64), digest hash.Hash) error {
	layout, e := xzLayoutOf(in, size)
	if e != nil && !errors.Is(e, errNotSingleStream) {
		return fmt.Errorf("damaged archive: %w", e)
	}
	if layout != nil {
		if layout.size != want {
			return fmt.Errorf("damaged archive: it unpacks to %d bytes, not %d", layout.size, want)
		}
		return unpackXZBlocks(ctx, in, dst, layout, progress, digest)
	}
	r, e := xz.NewReader(io.NewSectionReader(in, 0, size), 0)
	if e != nil {
		return fmt.Errorf("damaged archive: %w", e)
	}
	var out io.Writer = dst
	if digest != nil {
		out = io.MultiWriter(dst, digest)
	}
	var done atomic.Int64
	stop := reportProgress(&done, want, progress)
	_, e = copyCtx(ctx, out, r, want, &done)
	stop()
	return e
}
