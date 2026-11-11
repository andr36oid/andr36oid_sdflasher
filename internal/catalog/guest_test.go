package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	uxz "github.com/ulikunitz/xz"
)

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// a small release: the image packed with xz and cut into two parts
func guestFixture(t *testing.T) (*GuestRelease, []string, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	image := bytes.Repeat([]byte("erofs image block "), 4096*512/18+1)[:4096*512]
	var packed bytes.Buffer
	w, e := uxz.NewWriter(&packed)
	if e != nil {
		t.Fatal(e)
	}
	w.Write(image)
	w.Close()
	cut := packed.Len() / 2
	var parts []string
	m := map[string]any{"format": 1, "darkosen": "11112026", "revision": 1, "partition_bytes": 8 << 20, "minimum_andr36oid": "",
		"image": map[string]any{"name": "d.erofs.img", "size": len(image), "sha256": sum(image)}}
	var list []map[string]any
	for i, b := range [][]byte{packed.Bytes()[:cut], packed.Bytes()[cut:]} {
		name := "d.erofs.img.xz.00" + string(rune('1'+i))
		p := filepath.Join(dir, name)
		os.WriteFile(p, b, 0600)
		parts = append(parts, p)
		list = append(list, map[string]any{"name": name, "size": len(b), "sha256": sum(b)})
	}
	m["download"] = map[string]any{"compression": "xz", "name": "d.erofs.img.xz", "size": packed.Len(), "sha256": sum(packed.Bytes()), "parts": list}
	raw, _ := json.Marshal(m)
	g, e := parseGuest(raw)
	if e != nil {
		t.Fatal(e)
	}
	return g, parts, image, dir
}

func TestGuestUnpacksAndChecks(t *testing.T) {
	g, parts, image, dir := guestFixture(t)
	out, e := unpackGuest(context.Background(), g, parts, dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(out)
	if !bytes.Equal(got, image) || filepath.Base(out) != g.Image.SHA256+"-d.erofs.img" {
		t.Fatal("wrong image", out)
	}
	// an image that is already there is used again without the parts
	g.assets = nil
	if again, e := DownloadGuest(context.Background(), g, dir, nil); e != nil || again != out {
		t.Fatal(again, e)
	}
	os.Remove(out)
	// a flipped byte, a missing part and a wrong checksum leave nothing behind
	b, _ := os.ReadFile(parts[1])
	b[len(b)/2] ^= 0xff
	os.WriteFile(parts[1], b, 0600)
	for _, list := range [][]string{parts, parts[:1]} {
		if _, e = unpackGuest(context.Background(), g, list, dir, nil); e == nil {
			t.Fatal("damaged download accepted")
		}
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.img"))
	tmp, _ := filepath.Glob(filepath.Join(dir, "*.part"))
	if len(left)+len(tmp) != 0 {
		t.Fatal("left behind", left, tmp)
	}
}

func TestGuestManifestAndFilesMustAgree(t *testing.T) {
	g, _, _, _ := guestFixture(t)
	var assets []Asset
	for _, p := range g.Download.Parts {
		assets = append(assets, Asset{Name: p.Name, Size: p.Size, Digest: "sha256:" + p.SHA256})
	}
	if e := g.bind(assets); e != nil {
		t.Fatal(e)
	}
	wrong := append([]Asset{}, assets...)
	wrong[1].Digest = "sha256:" + strings.Repeat("0", 64)
	if g.bind(wrong) == nil || g.bind(assets[:1]) == nil {
		t.Fatal("mismatch accepted")
	}
	for _, bad := range []string{
		`{"format":2}`,
		`{"format":1,"darkosen":"x","revision":1,"image":{"name":"../a.img","size":512,"sha256":"` + strings.Repeat("a", 64) + `"},"download":{"compression":"xz","size":1,"sha256":"` + strings.Repeat("a", 64) + `","parts":[{"name":"p","size":1,"sha256":"` + strings.Repeat("a", 64) + `"}]},"partition_bytes":512}`,
		`{"format":1,"darkosen":"x","revision":1,"image":{"name":"a.img","size":512,"sha256":"` + strings.Repeat("a", 64) + `"},"download":{"compression":"xz","size":2,"sha256":"` + strings.Repeat("a", 64) + `","parts":[{"name":"p","size":1,"sha256":"` + strings.Repeat("a", 64) + `"}]},"partition_bytes":512}`,
		`{"format":1,"darkosen":"x","revision":1,"image":{"name":"a.img","size":1024,"sha256":"` + strings.Repeat("a", 64) + `"},"download":{"compression":"xz","size":1,"sha256":"` + strings.Repeat("a", 64) + `","parts":[{"name":"p","size":1,"sha256":"` + strings.Repeat("a", 64) + `"}]},"partition_bytes":512}`,
	} {
		if _, e := parseGuest([]byte(bad)); e == nil {
			t.Fatal("accepted", bad[:40])
		}
	}
}

// The published manifest of the first release is read as it is.
func TestGuestReadsThePublishedManifest(t *testing.T) {
	b, e := os.ReadFile("/array/darkosen-erofs1/manifest.json")
	if e != nil {
		t.Skip("published manifest not on this host")
	}
	g, e := parseGuest(b)
	if e != nil {
		t.Fatal(e)
	}
	if g.Darkosen != "11112026" || g.Image.Size != 4171583488 || len(g.Download.Parts) != 2 || g.PartitionBytes != 5<<30 {
		t.Fatal(g)
	}
}
