package catalog

import "testing"

func TestVariants(t *testing.T) {
	for name, want := range map[string][3]string{
		"andr36oid-20261120-1200-rk3326-k510-vulkan.img.xz": {"5.10", "vulkan", "yes"},
		"andr36oid-20261120-1200-rk3326-k419-gles.img.gz":   {"4.19", "gles", ""},
		"andr36oid-20261120-1200-rk3326-K510-GLES.img":      {"5.10", "gles", ""},
		"andr36oid-20261016-2212-rk3326.img.xz":             {"", "", "yes"},
		"andr36oid-20261120-1200-rk3326-k612-vulkan.img.xz": {"", "", "yes"},
	} {
		k, g, _ := Variant(name)
		rec := ""
		if Recommended(name) {
			rec = "yes"
		}
		if k != want[0] || g != want[1] || rec != want[2] {
			t.Fatal(name, k, g, rec)
		}
	}
	list := []Asset{
		{Name: "a-k419-gles.img.xz", Release: "new"}, {Name: "a-k510-vulkan.img.xz", Release: "new"}, {Name: "a-k510-gles.img.xz", Release: "new"},
		{Name: "b-k419-vulkan.img.xz", Release: "partial"},
		{Name: "c.img.xz", Release: "old"},
	}
	first, others := Split(list)
	if len(first) != 3 || first[0].Name != "a-k510-vulkan.img.xz" || first[1].Release != "partial" || first[2].Release != "old" || len(others) != 2 {
		t.Fatal(first, others)
	}
}
