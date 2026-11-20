package firmware

import (
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

const MaxImageSize int64 = 32 << 30

// Load checks a release, hashing ZIP and XZ contents during extraction rather than
// reading the expanded image a second time. Direct .img files are hashed normally.
func Load(ctx context.Context, src, dir string, progress func(int64, int64)) (*Image, error) {
	digest := sha256.New()
	staged, err := stage(ctx, src, dir, progress, digest)
	if err != nil {
		return nil, imageError(err, true)
	}
	if staged == src {
		im, err := Inspect(ctx, src, progress)
		return im, imageError(err, false)
	}
	im, err := inspect(ctx, staged, nil, hex.EncodeToString(digest.Sum(nil)))
	if err != nil {
		os.Remove(staged)
		return nil, imageError(err, false)
	}
	im.Source = src
	return im, nil
}

// Stage expands exactly one image. ZIP CRC and XZ block checks complete before the file is accepted.
func Stage(ctx context.Context, src, dir string, progress func(int64, int64)) (string, error) {
	return stage(ctx, src, dir, progress, nil)
}

func stage(ctx context.Context, src, dir string, progress func(int64, int64), digest hash.Hash) (string, error) {
	if strings.EqualFold(filepath.Ext(src), ".img") {
		return src, nil
	}
	if strings.EqualFold(filepath.Ext(src), ".xz") {
		return stageXZ(ctx, src, dir, progress, digest)
	}
	if strings.EqualFold(filepath.Ext(src), ".gz") {
		return stageGzip(ctx, src, dir, progress, digest)
	}
	if !strings.EqualFold(filepath.Ext(src), ".zip") {
		return "", i18n.Detailed(ErrImageType.New(), errors.New(filepath.Base(src)))
	}
	z, e := zip.OpenReader(src)
	if e != nil {
		return "", e
	}
	defer z.Close()
	var entry *zip.File
	for _, f := range z.File {
		if strings.EqualFold(filepath.Ext(f.Name), ".img") {
			if entry != nil {
				return "", i18n.Detailed(ErrNotRelease.New(), errors.New("archive contains multiple images"))
			}
			entry = f
		}
	}
	if entry == nil {
		return "", i18n.Detailed(ErrNotRelease.New(), errors.New("archive contains no disk image; recovery OTA packages are not supported"))
	}
	if entry.UncompressedSize64 > uint64(MaxImageSize) {
		return "", errTooLarge()
	}
	if e = CheckSpace(dir, int64(entry.UncompressedSize64)); e != nil {
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
	var out io.Writer = dst
	if digest != nil {
		out = io.MultiWriter(dst, digest)
	}
	r, e := entry.Open()
	if e != nil {
		return "", e
	}
	defer r.Close()
	buf := make([]byte, 4<<20)
	var written int64
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		n, re := r.Read(buf)
		if n > 0 {
			written += int64(n)
			if written > MaxImageSize {
				return "", errTooLarge()
			}
			if _, e = out.Write(buf[:n]); e != nil {
				return "", SpaceError(dir, int64(entry.UncompressedSize64)-written+int64(n), e)
			}
			if progress != nil {
				progress(written, int64(entry.UncompressedSize64))
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return "", fmt.Errorf("damaged archive: %w", re)
		}
	}
	if e = dst.Sync(); e != nil {
		return "", e
	}
	if written != int64(entry.UncompressedSize64) {
		return "", errors.New("truncated archive")
	}
	ok = true
	return name, nil
}

// counted counts what was read of the packed file, for the progress of a packing that
// doesn't say how large the image is.
type counted struct {
	r io.Reader
	n int64
}

func (c *counted) Read(b []byte) (int, error) {
	n, e := c.r.Read(b)
	c.n += int64(n)
	return n, e
}

// stageGzip unpacks an .img.gz, the packing of test builds.
func stageGzip(ctx context.Context, src, dir string, progress func(int64, int64), digest hash.Hash) (string, error) {
	f, e := os.Open(src)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return "", e
	}
	need := gzipSize(f, st.Size())
	if e = CheckSpace(dir, need); e != nil {
		return "", e
	}
	in := &counted{r: f}
	z, e := gzip.NewReader(bufio.NewReaderSize(in, 1<<20))
	if e != nil {
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
	var out io.Writer = dst
	if digest != nil {
		out = io.MultiWriter(dst, digest)
	}
	buf := make([]byte, 4<<20)
	var written int64
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		n, re := z.Read(buf)
		if n > 0 {
			if written += int64(n); written > MaxImageSize {
				return "", errTooLarge()
			}
			if _, e = out.Write(buf[:n]); e != nil {
				return "", SpaceError(dir, need-written+int64(n), e)
			}
			if progress != nil {
				progress(min(in.n, st.Size()), st.Size())
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return "", re
		}
	}
	if e = dst.Sync(); e != nil {
		return "", e
	}
	ok = true
	return name, nil
}

// gzipSize is the least an .img.gz of size bytes unpacks to: its trailer has the
// unpacked size modulo 4 GiB, and an image is never smaller than its packed file.
func gzipSize(f io.ReaderAt, size int64) int64 {
	var b [4]byte
	if size < 18 {
		return 0
	}
	if _, e := f.ReadAt(b[:], size-4); e != nil {
		return 0
	}
	n := int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16 | int64(b[3])<<24
	for n < size && n+1<<32 <= MaxImageSize {
		n += 1 << 32
	}
	return n
}
