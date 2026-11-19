package firmware

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// Every file people may pick by mistake, or that arrives damaged, is refused with a
// message that says what is wrong with it and what to do, never with the bare error of
// the unpacker or the partition reader; and nothing is left in the work folder.
func TestLoadExplainsUnusableImages(t *testing.T) {
	payload := xzPayload(3 << 20)
	stream := xzStream(t, payload, 1<<20)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(payload)
	zw.Close()
	flipped := append([]byte(nil), stream...)
	flipped[len(flipped)/2] ^= 0x55
	gzFlipped := append([]byte(nil), gz.Bytes()...)
	gzFlipped[len(gzFlipped)/2] ^= 0x55
	twoImages := func(p string) {
		f, _ := os.Create(p)
		w := zip.NewWriter(f)
		for _, n := range []string{"a.img", "b.img"} {
			e, _ := w.Create(n)
			e.Write([]byte("x"))
		}
		w.Close()
		f.Close()
	}
	noImage := func(p string) {
		f, _ := os.Create(p)
		w := zip.NewWriter(f)
		e, _ := w.Create("update.zip")
		e.Write([]byte("ota"))
		w.Close()
		f.Close()
	}
	// longer than Windows' MAX_PATH (260) altogether, each folder name within limits
	long := strings.Repeat("a long folder name with spaces ", 3)
	long = long + "/" + long + "/" + long
	for _, c := range []struct {
		name  string
		file  string
		data  []byte
		make  func(string)
		want  *i18n.Format
		inner string
	}{
		{name: "empty xz", file: "andr36oid-20261119-0217-5.10-vulkan.img.xz", data: []byte{}, want: i18n.CauseDamaged},
		{name: "xz with the wrong magic", file: "release.img.xz", data: []byte("PK\x03\x04 this is a zip"), want: i18n.CauseDamaged},
		{name: "truncated xz", file: "release.img.xz", data: stream[:len(stream)/2], want: i18n.CauseDamaged},
		{name: "xz damaged mid-way", file: "release.img.xz", data: flipped, want: i18n.CauseDamaged},
		{name: "empty gz", file: "release.img.gz", data: []byte{}, want: i18n.CauseDamaged},
		{name: "gz with the wrong magic", file: "release.img.gz", data: []byte("not gzip at all, sorry"), want: i18n.CauseDamaged},
		{name: "truncated gz", file: "release.img.gz", data: gz.Bytes()[:gz.Len()/2], want: i18n.CauseDamaged},
		{name: "gz damaged mid-way", file: "release.img.gz", data: gzFlipped, want: i18n.CauseDamaged},
		{name: "zip that is no zip", file: "release.img.zip", data: []byte("garbage"), want: i18n.CauseDamaged},
		{name: "zip with two images", file: "two.img.zip", make: twoImages, want: ErrNotRelease},
		{name: "zip with an OTA package", file: "ota.img.zip", make: noImage, want: ErrNotRelease},
		{name: "empty raw image", file: "release.img", data: []byte{}, want: ErrNotRelease},
		{name: "another system's raw image", file: "other.img", data: bytes.Repeat([]byte{0xeb, 0x3c, 0x90}, 1<<16), want: ErrNotRelease},
		{name: "xz of something that is not ours", file: "ArkOS R36S.img.xz", data: stream, want: ErrNotRelease},
		{name: "an ISO", file: "linux.iso", data: []byte("CD001"), want: ErrImageType},
		{name: "spaces and unicode", file: "Mein Ordner ü 日本/andr36oid (1).img.xz", data: stream[:100], want: i18n.CauseDamaged},
		{name: "very long path", file: long + "/" + long + "/andr36oid.img.gz", data: []byte{1, 2, 3}, want: i18n.CauseDamaged},
		{name: "file that is gone", file: "gone.img.xz", want: i18n.CauseFileGone},
		{name: "raw file that is gone", file: "gone.img", want: i18n.CauseFileGone},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, c.file)
			if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
				t.Skip("cannot make the folder here:", e)
			}
			switch {
			case c.make != nil:
				c.make(p)
			case c.data != nil:
				if e := os.WriteFile(p, c.data, 0600); e != nil {
					t.Fatal(e)
				}
			}
			work := t.TempDir()
			im, e := Load(context.Background(), p, work, nil)
			if e == nil || im != nil {
				t.Fatal("accepted", c.file)
			}
			key, _, ok := i18n.Find(e)
			if !ok || key != c.want.Key() {
				t.Fatalf("%v\nexplained as %q", e, key)
			}
			if !i18n.Has(key) {
				t.Fatalf("%q is not in en.json", key)
			}
			if left, _ := os.ReadDir(work); len(left) != 0 {
				t.Fatal("left", left[0].Name())
			}
		})
	}
}

// A file the flasher can't read is explained by the system's error (i18n's causes), a
// stop stays a stop, and the message of a known problem is kept as it is.
func TestImageErrorKeepsWhatAlreadySays(t *testing.T) {
	_, pathErr := os.Open(filepath.Join(t.TempDir(), "missing.img"))
	for _, c := range []struct {
		in   error
		same bool
	}{
		{context.Canceled, true},
		{pathErr, true},
		{ErrNoSpace.New("1 GB", "0 GB"), true},
		{ErrNoProfileList, true},
		{errors.New("xz: index is damaged"), false},
	} {
		got := imageError(c.in, true)
		if (got == c.in) != c.same {
			t.Errorf("%v became %v", c.in, got)
		}
		if !errors.Is(got, c.in) {
			t.Errorf("%v lost its detail: %v", c.in, got)
		}
	}
	if !errors.Is(imageError(ErrNoProfileList, false), ErrNoProfileList) {
		t.Fatal("ErrNoProfileList no longer matches itself")
	}
}
