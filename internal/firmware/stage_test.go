package firmware

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func imageArchive(t *testing.T, method uint16, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.img.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	entry, err := w.CreateHeader(&zip.FileHeader{Name: "release.img", Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = entry.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStageHashesUncompressedImage(t *testing.T) {
	payload := bytes.Repeat([]byte("image payload\x00"), 400000)
	expected := sha256.Sum256(payload)
	for _, method := range []uint16{zip.Store, zip.Deflate} {
		t.Run(map[uint16]string{zip.Store: "stored", zip.Deflate: "deflated"}[method], func(t *testing.T) {
			source := imageArchive(t, method, payload)
			digest := sha256.New()
			var done int64
			path, err := stage(context.Background(), source, t.TempDir(), func(n, total int64) {
				if n <= done || total != int64(len(payload)) {
					t.Fatalf("unexpected progress %d/%d after %d", n, total, done)
				}
				done = n
			}, digest)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("staged data differs: %v", err)
			}
			if !bytes.Equal(digest.Sum(nil), expected[:]) || done != int64(len(payload)) {
				t.Fatal("digest or progress does not cover the entire image")
			}
		})
	}
}

func TestLoadRejectsDamagedArchive(t *testing.T) {
	source := imageArchive(t, zip.Store, []byte("image payload"))
	z, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := z.File[0].DataOffset()
	z.Close()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	data[offset] ^= 1
	if err = os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err = Load(context.Background(), source, dir, nil); err == nil || !strings.Contains(err.Error(), "damaged archive") {
		t.Fatalf("expected ZIP checksum rejection, got %v", err)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("failed extraction left files: %v %v", files, err)
	}
}

func TestLoadCleansUpRejectedImage(t *testing.T) {
	source := imageArchive(t, zip.Deflate, []byte("not a disk image"))
	dir := t.TempDir()
	if _, err := Load(context.Background(), source, dir, nil); err == nil {
		t.Fatal("accepted an invalid release")
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("rejected image left files: %v %v", files, err)
	}
	path := filepath.Join(dir, "original.img")
	if err := os.WriteFile(path, []byte("not a disk image"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), path, dir, nil); err == nil {
		t.Fatal("accepted an invalid raw image")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("removed the original raw image: %v", err)
	}
}

func TestLoadCancellationRemovesStagingFile(t *testing.T) {
	source := imageArchive(t, zip.Deflate, []byte("image payload"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	if _, err := Load(ctx, source, dir, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if files, err := os.ReadDir(dir); err != nil || len(files) != 0 {
		t.Fatalf("cancelled load left files: %v %v", files, err)
	}
}

func TestInspectForWriteRequiresSelectedDigest(t *testing.T) {
	for _, digest := range []string{"", "abc", strings.Repeat("g", 64), strings.Repeat("a", 62)} {
		if _, err := InspectForWrite(context.Background(), "missing.img", digest); err == nil || !strings.Contains(err.Error(), "checksum") {
			t.Fatalf("invalid digest %q was not rejected: %v", digest, err)
		}
	}
}

func TestLoadReleaseArchive(t *testing.T) {
	archive, raw := os.Getenv("SDFLASHER_TEST_ARCHIVE"), os.Getenv("SDFLASHER_TEST_IMAGE")
	if archive == "" || raw == "" {
		t.Skip("set SDFLASHER_TEST_ARCHIVE and SDFLASHER_TEST_IMAGE for a release ZIP integration check")
	}
	ctx := context.Background()
	expected, err := Inspect(ctx, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	var done int64
	im, err := Load(ctx, archive, t.TempDir(), func(n, total int64) {
		if n <= done || total != expected.Size {
			t.Fatalf("release progress restarted or has wrong size: %d/%d after %d", n, total, done)
		}
		done = n
	})
	if err != nil {
		t.Fatal(err)
	}
	if im.SHA256 != expected.SHA256 || im.Size != expected.Size || im.Version != expected.Version || len(im.Profiles) != len(expected.Profiles) || done != expected.Size {
		t.Fatal("streamed release inspection differs from raw-image inspection")
	}
	// The helper rechecks the release structure but leaves the full digest check
	// to Execute. A deliberately different digest must survive this step.
	selected := hex.EncodeToString(make([]byte, sha256.Size))
	forWrite, err := InspectForWrite(ctx, im.Path, selected)
	if err != nil || forWrite.SHA256 != selected || len(forWrite.Profiles) != len(im.Profiles) {
		t.Fatalf("write inspection did not retain the selected digest: %v", err)
	}
}

func TestStageGzip(t *testing.T) {
	dir := t.TempDir()
	raw := bytes.Repeat([]byte("andr36oid image "), 1<<16)
	var packed bytes.Buffer
	z := gzip.NewWriter(&packed)
	z.Write(raw)
	z.Close()
	src := filepath.Join(dir, "release.img.gz")
	os.WriteFile(src, packed.Bytes(), 0600)
	digest := sha256.New()
	out, e := stage(context.Background(), src, dir, nil, digest)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, raw) || hex.EncodeToString(digest.Sum(nil)) != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatal("unpacked image differs")
	}
	// a cut-off file is refused and leaves nothing behind
	os.WriteFile(src, packed.Bytes()[:packed.Len()/2], 0600)
	before, _ := os.ReadDir(dir)
	if _, e = stage(context.Background(), src, dir, nil, nil); e == nil {
		t.Fatal("truncated file accepted")
	}
	if after, _ := os.ReadDir(dir); len(after) != len(before) {
		t.Fatal("partial image left behind")
	}
}
