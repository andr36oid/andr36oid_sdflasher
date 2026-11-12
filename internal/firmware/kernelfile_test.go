package firmware

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func gzipKernel(t *testing.T) (gz, meta []byte) {
	t.Helper()
	raw := make([]byte, 64<<10) // not compressible, so the gzip file has a real size
	for i, x := 0, uint32(1); i < len(raw); i++ {
		x = x*1664525 + 1013904223
		raw[i] = byte(x >> 24)
	}
	var b bytes.Buffer
	w, _ := gzip.NewWriterLevel(&b, 6)
	w.Write(raw)
	w.Close()
	gz = b.Bytes()
	meta = make([]byte, 52)
	copy(meta, "A36KGZ1\x00")
	binary.LittleEndian.PutUint32(meta[8:], uint32(len(gz)))
	binary.LittleEndian.PutUint32(meta[12:], uint32(len(raw)))
	binary.BigEndian.PutUint32(meta[16:], crc32.ChecksumIEEE(gz))
	binary.BigEndian.PutUint32(meta[20:], crc32.ChecksumIEEE(raw))
	binary.BigEndian.PutUint32(meta[48:], crc32.ChecksumIEEE(meta[:48]))
	return gz, meta
}

// BOOT has the kernel as Image (older releases) or as Image.gz with its
// Image.gz.meta; a pair that does not match is refused before anything is written.
func TestCheckKernel(t *testing.T) {
	gz, meta := gzipKernel(t)
	cut := append([]byte{}, gz[:len(gz)-1]...)
	flipped := append([]byte{}, gz...)
	flipped[len(flipped)/2] ^= 1
	badMeta := append([]byte{}, meta...)
	badMeta[12] ^= 1
	raw := string(bytes.Repeat([]byte("k"), 8192))
	for name, c := range map[string]struct {
		files map[string]string
		ok    bool
	}{
		"raw kernel":        {map[string]string{"Image": raw}, true},
		"compressed kernel": {map[string]string{"Image.gz": string(gz), "Image.gz.meta": string(meta)}, true},
		"no kernel":         {map[string]string{"boot.ini": "x"}, false},
		"short raw kernel":  {map[string]string{"Image": "x"}, false},
		"no meta":           {map[string]string{"Image.gz": string(gz)}, false},
		"meta alone":        {map[string]string{"Image.gz.meta": string(meta)}, false},
		"no meta, raw too":  {map[string]string{"Image.gz": string(gz), "Image": raw}, false},
		"big meta, raw too": {map[string]string{"Image.gz.meta": string(bytes.Repeat([]byte("m"), 8192)), "Image": raw}, false},
		"cut short":         {map[string]string{"Image.gz": string(cut), "Image.gz.meta": string(meta)}, false},
		"changed byte":      {map[string]string{"Image.gz": string(flipped), "Image.gz.meta": string(meta)}, false},
		"changed meta":      {map[string]string{"Image.gz": string(gz), "Image.gz.meta": string(badMeta)}, false},
	} {
		f, tab := bootOnlyImage(t, c.files)
		boot, e := OpenBoot(f, tab, true)
		if e != nil {
			t.Fatal(e)
		}
		if e := checkKernel(boot); (e == nil) != c.ok {
			t.Errorf("%s: %v", name, e)
		}
	}
}
