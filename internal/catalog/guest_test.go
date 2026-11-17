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

// manifestFor is a manifest as the three repositories publish it (with the keys the
// flasher doesn't read), for a system named dist ("" leaves "distribution" out).
func manifestFor(dist, base, version string) ([]byte, []Asset) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	m := map[string]any{"format": 1, "darkosen": version, "revision": 1, "partition_bytes": int64(5 << 30), "minimum_andr36oid": "", "hotfixes": []any{},
		"upstream": map[string]any{"repository": "someone/" + base, "tag": version},
		"image":    map[string]any{"name": base + "_" + version + ".erofs.img", "filesystem": "erofs", "size": int64(3813339136), "sha256": a},
		"download": map[string]any{"compression": "xz", "name": base + "_" + version + ".erofs.img.xz", "size": 3, "sha256": b, "parts": []map[string]any{
			{"name": base + "_" + version + ".erofs.img.xz.001", "size": 2, "sha256": a},
			{"name": base + "_" + version + ".erofs.img.xz.002", "size": 1, "sha256": b}}}}
	if dist != "" {
		m["distribution"] = dist
	}
	raw, _ := json.Marshal(m)
	assets := []Asset{
		{Name: base + "_" + version + ".erofs.img.xz.001", Size: 2, Digest: "sha256:" + a},
		{Name: base + "_" + version + ".erofs.img.xz.002", Size: 1, Digest: "sha256:" + b},
	}
	return raw, assets
}

func TestGuestThreeSourcesParse(t *testing.T) {
	cases := []struct{ id, dist, base, version, tag string }{
		{"dArkOS", "dArkOS", "dArkOS_RG351MP", "11072026", "11072026-RG351MP-erofs1"},
		{"dArkOSRE", "dArkOSRE", "dArkOSRE_R36", "03082026", "03082026-erofs1"},
		{"dArkOSen", "", "dArkOSen_R36", "11112026", "11112026-erofs1"},
	}
	for _, c := range cases {
		src, ok := FindGuestSource(c.id)
		if !ok {
			t.Fatal("no source", c.id)
		}
		raw, assets := manifestFor(c.dist, c.base, c.version)
		g, e := guestFromRelease(src, &release{Tag: c.tag, Assets: assets}, raw)
		if e != nil {
			t.Fatal(c.id, e)
		}
		if g.Distribution != c.id || g.Source.Repository != src.Repository || g.Darkosen != c.version || g.Tag != c.tag ||
			g.PartitionBytes != 5<<30 || g.Image.Name != c.base+"_"+c.version+".erofs.img" || g.DownloadBytes() != 3 || len(g.assets) != 2 {
			t.Fatalf("%s: %+v", c.id, g)
		}
		// a release that describes another system is not taken for this one
		for _, other := range GuestSources {
			if other.ID != c.id {
				if _, e := guestFromRelease(other, &release{Tag: c.tag, Assets: assets}, raw); e == nil {
					t.Fatal(c.id, "accepted as", other.ID)
				}
			}
		}
	}
	if len(GuestSources) != 3 {
		t.Fatal(GuestSources)
	}
}

func TestGuestDistributionDefaultsToDarkosen(t *testing.T) {
	raw, _ := manifestFor("", "dArkOSen_R36", "11112026")
	g, e := parseGuest(raw)
	if e != nil || g.Distribution != "dArkOSen" {
		t.Fatal(g, e)
	}
	raw, _ = manifestFor("darkosre", "dArkOSRE_R36", "03082026")
	if g, e = parseGuest(raw); e != nil || g.Distribution != "dArkOSRE" {
		t.Fatal("case of the name", g, e)
	}
	for _, unknown := range []string{"openwrt", "other", "dArkOS2"} {
		raw, _ = manifestFor(unknown, "x", "11112026")
		if _, e = parseGuest(raw); e == nil || !strings.Contains(e.Error(), "newer flasher") {
			t.Fatal(unknown, e)
		}
	}
}

func TestGuestDefaultChoiceAndNone(t *testing.T) {
	if DefaultGuest != "dArkOS" || GuestSources[0].ID != DefaultGuest {
		t.Fatal("dArkOS must be the default and come first")
	}
	if s, ok := FindGuestSource(DefaultGuest); !ok || s.Repository != "andr36oid/dArkOS-erofs" || s.Device != "RG351MP" {
		t.Fatal(s)
	}
	for in, want := range map[string]string{"": NoGuest, "none": NoGuest, "None": NoGuest, "darkos": "dArkOS", "DARKOSRE": "dArkOSRE", " dArkOSen ": "dArkOSen"} {
		if got, ok := ParseGuestChoice(in); !ok || got != want {
			t.Fatal(in, got, ok)
		}
	}
	if _, ok := ParseGuestChoice("ArkOS"); ok {
		t.Fatal("unknown system accepted")
	}
	if _, ok := FindGuestSource(NoGuest); ok {
		t.Fatal("none is not a source")
	}
	if _, e := LatestGuest(context.Background(), GuestSource{}); e == nil {
		t.Fatal("looked up no source")
	}
}

func TestGuestTagForms(t *testing.T) {
	dk, _ := FindGuestSource("dArkOS")
	re, _ := FindGuestSource("dArkOSRE")
	en, _ := FindGuestSource("dArkOSen")
	for _, c := range []struct {
		src  GuestSource
		tag  string
		fits bool
	}{
		{dk, "11072026-RG351MP-erofs1", true},
		{dk, "11072026-rg351mp-erofs12", true},
		{dk, "11072026-RG353V-erofs1", false},
		{dk, "11072026-erofs1", false},
		{re, "03082026-erofs1", true},
		{re, "03082026-RG351MP-erofs1", false},
		{en, "11112026-erofs1", true},
		{en, "11112026-erofs", false},
		{en, "v11112026-erofs1", false},
		{en, "1111202-erofs1", false},
		{en, "11112026", false},
	} {
		if c.src.tagFits(c.tag) != c.fits {
			t.Error(c.src.ID, c.tag, !c.fits)
		}
	}
	// another device's newer release is passed over for the R36S build
	list := []release{{Tag: "11202026-RG353V-erofs1"}, {Tag: "11072026-RG351MP-erofs1"}}
	if r := pickGuest(dk, list); r == nil || r.Tag != "11072026-RG351MP-erofs1" {
		t.Fatal(r)
	}
}

func TestGuestPrereleases(t *testing.T) {
	en, _ := FindGuestSource("dArkOSen")
	for _, s := range GuestSources {
		if s.Prereleases {
			t.Fatal(s.ID, "offers pre-releases")
		}
	}
	full := release{Tag: "11112026-erofs1"}
	pre := release{Tag: "11202026-erofs1", Pre: true}
	draft := release{Tag: "11252026-erofs1", Draft: true}
	if r := pickGuest(en, []release{draft, pre, full}); r == nil || r.Tag != full.Tag {
		t.Fatal("full release not chosen", r)
	}
	if r := pickGuest(en, []release{draft, pre}); r != nil {
		t.Fatal("pre-release offered", r.Tag)
	}
	if pickGuest(en, nil) != nil {
		t.Fatal("empty list")
	}
	// a source that allows them takes one only when there is no full release
	en.Prereleases = true
	if r := pickGuest(en, []release{draft, pre, full}); r == nil || r.Tag != full.Tag {
		t.Fatal(r)
	}
	if r := pickGuest(en, []release{draft, pre}); r == nil || r.Tag != pre.Tag {
		t.Fatal(r)
	}
}

// The published manifests of the three sources are read as they are.
func TestGuestReadsThePublishedManifests(t *testing.T) {
	dir := os.Getenv("SDFLASHER_GUEST_MANIFESTS")
	if dir == "" {
		t.Skip("set SDFLASHER_GUEST_MANIFESTS to a folder with dArkOS.json, dArkOSRE.json and dArkOSen.json")
	}
	for _, s := range GuestSources {
		b, e := os.ReadFile(filepath.Join(dir, s.ID+".json"))
		if e != nil {
			t.Fatal(e)
		}
		g, e := parseGuest(b)
		if e != nil || g.Distribution != s.ID || !strings.HasPrefix(g.Image.Name, s.ID+"_") || g.PartitionBytes < g.Image.Size {
			t.Fatal(s.ID, g, e)
		}
	}
}
