package catalog

import (
	"regexp"
	"strings"
)

// Since the hybrid releases each build comes as four images: kernel 4.19 or 5.10,
// each with the OpenGL ES or the Vulkan graphics driver. The file name says which:
// andr36oid-20261120-1200-rk3326-k510-vulkan.img.xz, or with the kernel written out as in
// andr36oid-20261116-2249-5.10-vulkan.img.xz. Older images have no such part.
var variantName = regexp.MustCompile(`-k?(4\.?19|5\.?10)-(gles|vulkan)\.img(\.[a-z0-9]+)?$`)

// Variant is the kernel line ("4.19", "5.10") and graphics driver ("gles", "vulkan") of
// an image file; ok is false for images from before the hybrid releases.
func Variant(name string) (kernel, gpu string, ok bool) {
	m := variantName.FindStringSubmatch(strings.ToLower(name))
	if m == nil {
		return "", "", false
	}
	k := strings.ReplaceAll(m[1], ".", "")
	return k[:1] + "." + k[1:], m[2], true
}

// Recommended: the image to try first. The others are there to fall back on.
func Recommended(name string) bool {
	k, g, ok := Variant(name)
	return !ok || (k == "5.10" && g == "vulkan")
}

// Split sorts a release list into the images to show first (one per release: its
// recommended variant, or the only image of an older release) and the other kernels,
// each in the order of the list.
func Split(assets []Asset) (first, others []Asset) {
	has := map[string]bool{}
	for _, a := range assets {
		if _, _, ok := Variant(a.Name); ok && Recommended(a.Name) {
			has[a.Release] = true
		}
	}
	for _, a := range assets {
		_, _, ok := Variant(a.Name)
		// a release without its recommended image shows what it has
		if !ok || Recommended(a.Name) || !has[a.Release] {
			first = append(first, a)
		} else {
			others = append(others, a)
		}
	}
	return first, others
}

// Of is the images of one release, in the order of the list.
func Of(assets []Asset, release string) []Asset {
	var list []Asset
	for _, a := range assets {
		if a.Release == release {
			list = append(list, a)
		}
	}
	return list
}

// Matching is the image of a release with this kernel line and graphics driver; ok is
// false when the release has none.
func Matching(assets []Asset, release, kernel, gpu string) (Asset, bool) {
	for _, a := range Of(assets, release) {
		if k, g, ok := Variant(a.Name); ok && k == kernel && g == gpu {
			return a, true
		}
	}
	return Asset{}, false
}

// SplitFor is Split for updating a card with this kernel line and graphics driver:
// each release shows its image of that kind first. A release without one shows what
// Split shows, so that the choice can say the kind is missing. With kernel or gpu ""
// it is Split.
func SplitFor(assets []Asset, kernel, gpu string) (first, others []Asset) {
	if kernel == "" || gpu == "" {
		return Split(assets)
	}
	has := map[string]bool{}
	for _, a := range assets {
		if k, g, ok := Variant(a.Name); ok && k == kernel && g == gpu {
			has[a.Release] = true
		}
	}
	plain, _ := Split(assets)
	shown := map[string]bool{}
	for _, a := range plain {
		if !has[a.Release] {
			shown[a.URL+"\x00"+a.Name] = true
		}
	}
	for _, a := range assets {
		k, g, ok := Variant(a.Name)
		if has[a.Release] && ok && k == kernel && g == gpu || !has[a.Release] && shown[a.URL+"\x00"+a.Name] {
			first = append(first, a)
		} else {
			others = append(others, a)
		}
	}
	return first, others
}
