package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

// The image names of the tester build of 19 November 2026 and the names people's
// browsers and disks give them.
func TestVariantNamesOfTheTesterBuild(t *testing.T) {
	for name, want := range map[string][2]string{
		"andr36oid-20261119-0217-4.19-gles.img.xz":       {"4.19", "gles"},
		"andr36oid-20261119-0217-4.19-vulkan.img.xz":     {"4.19", "vulkan"},
		"andr36oid-20261119-0217-5.10-gles.img.xz":       {"5.10", "gles"},
		"andr36oid-20261119-0217-5.10-vulkan.img.xz":     {"5.10", "vulkan"},
		"andr36oid-20261119-0217-5.10-vulkan.img":        {"5.10", "vulkan"},
		"andr36oid-20261119-0217-5.10-vulkan.img.gz":     {"5.10", "vulkan"},
		"andr36oid-20261119-0217-rk3326-k510-vulkan.img": {"5.10", "vulkan"},
		"ANDR36OID-20261119-0217-5.10-VULKAN.IMG.XZ":     {"5.10", "vulkan"},
		// a second download in a browser: no variant, shown as it is
		"andr36oid-20261119-0217-5.10-vulkan (1).img.xz": {"", ""},
		"andr36oid-20261119-0217-6.1-vulkan.img.xz":      {"", ""},
		"andr36oid-20261119-0217-5.10-opengl.img.xz":     {"", ""},
		"":                                               {"", ""},
	} {
		k, g, ok := Variant(name)
		if k != want[0] || g != want[1] || ok != (want[0] != "") {
			t.Errorf("%q: %q %q %v", name, k, g, ok)
		}
		if d, ok := firmware.BuildDate(name); name != "" && !strings.Contains(name, "(1)") && (!ok || d.Format("20060102") != "20261119") {
			t.Errorf("%q: build date %v %v", name, d, ok)
		}
		if _, old := firmware.TooOld(name); old {
			t.Errorf("%q counted as too old", name)
		}
	}
}

func FuzzVariant(f *testing.F) {
	for _, s := range []string{"andr36oid-20261119-0217-5.10-vulkan.img.xz", "a-k419-gles.img", "x-k5.10-gles.img.zip", "lineage-18.1-20261011-1531-r36s-android.img.zip", "20250231"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		k, g, ok := Variant(name)
		if ok && (k != "4.19" && k != "5.10" || g != "gles" && g != "vulkan") {
			t.Fatal(name, k, g)
		}
		Recommended(name)
		if d, ok := firmware.BuildDate(name); ok && d.Year() < 2000 {
			t.Fatal(name, d)
		}
		firmware.TooOld(name)
	})
}

// GitHub's release list, whatever its shape: a list or an error, never a panic, and
// only image files with a checkable download address are offered.
func FuzzReleaseList(f *testing.F) {
	f.Add(`[{"name":"r","tag_name":"v1","published_at":"2026-11-19T00:00:00Z","assets":[{"name":"andr36oid-20261119-0217-5.10-vulkan.img.xz","size":5,"browser_download_url":"https://github.com/andr36oid/releases/releases/download/v1/a.img.xz","digest":"sha256:00"}]}]`)
	f.Add(`[{"draft":true}]`)
	f.Add(`{}`)
	f.Fuzz(func(t *testing.T, body string) {
		var rel []release
		if json.Unmarshal([]byte(body), &rel) != nil {
			return
		}
		for _, a := range usable(rel) {
			n := strings.ToLower(a.Name)
			if !strings.HasSuffix(n, ".img") && !strings.HasSuffix(n, ".img.xz") && !strings.HasSuffix(n, ".img.gz") && !strings.HasSuffix(n, ".img.zip") {
				t.Fatal("offered", a.Name)
			}
		}
		Split(usable(rel))
		for _, src := range GuestSources {
			pickGuest(src, rel)
		}
	})
}

// A game system manifest, whatever it says: a release or an error, never a panic.
func FuzzGuestManifest(f *testing.F) {
	manifest, _ := manifestFor("", "dArkOSen_R36", "11112026")
	f.Add(manifest)
	f.Add([]byte(`{"format":1}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, b []byte) {
		g, e := parseGuest(b)
		if e == nil && g == nil {
			t.Fatal("no release and no error")
		}
	})
}
