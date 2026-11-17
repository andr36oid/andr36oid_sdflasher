package firmware

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Unpacking a release stops before it starts when the disk can't hold the image,
// with how much is needed and how much is free.
func TestStageChecksFreeSpace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "andr36oid-test.img.gz")
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	z.Write(bytes.Repeat([]byte{1}, 3<<20))
	z.Close()
	os.WriteFile(src, b.Bytes(), 0600)
	old := freeBytes
	t.Cleanup(func() { freeBytes = old })
	freeBytes = func(string) (int64, error) { return 100 << 20, nil }
	_, e := Stage(context.Background(), src, filepath.Join(dir, "cache"), nil)
	if !ErrNoSpace.Is(e) || !strings.Contains(e.Error(), "0.5 GB needed, 0.1 GB free") {
		t.Fatal(e)
	}
	freeBytes = func(string) (int64, error) { return 8 << 30, nil }
	if _, e = Stage(context.Background(), src, filepath.Join(dir, "cache"), nil); e != nil {
		t.Fatal(e)
	}
	// can't tell: not in the way
	freeBytes = func(string) (int64, error) { return 0, errors.New("statfs") }
	if e = CheckSpace(dir, 1<<40); e != nil {
		t.Fatal(e)
	}
	if n := gzipSize(bytes.NewReader(b.Bytes()), int64(b.Len())); n != 3<<20 {
		t.Fatal(n)
	}
}
