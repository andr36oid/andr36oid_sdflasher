package ui

import (
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
)

// An update installs the image of the kind the card runs: the same kernel line
// (4.19, 5.10) and graphics driver (OpenGL ES only, Vulkan). Another kind is chosen
// only on purpose, under Advanced... on the Release page, or from the release's images
// when it has none of the card's kind or the card does not say what it runs.

func kindKey(kernel, gpu string) string {
	if kernel == "" || gpu == "" {
		return ""
	}
	return kernel + "-" + gpu
}

// cardKind is what the card being updated runs, "" where it does not say.
func (s *screen) cardKind() (kernel, gpu string) {
	if !s.updating || s.card == nil || !s.card.Installed {
		return "", ""
	}
	return s.card.Kernel, s.card.Graphics
}

// picked is the kind the user chose on purpose for this card, "" for none.
func (s *screen) picked() string {
	if s.card == nil || s.kindFor != s.card.Fingerprint {
		return ""
	}
	return s.kindPick
}

// pick installs a on purpose, though it may be of another kind than the card runs.
func (s *screen) pick(a catalog.Asset) {
	k, g, _ := catalog.Variant(a.Name)
	s.kindPick, s.kindFor = kindKey(k, g), ""
	if s.card != nil {
		s.kindFor = s.card.Fingerprint
	}
	s.asset = &a
	s.ready()
}

// imageKind is the kind of a checked image: its build file, else its file name.
func imageKind(im *firmware.Image) (kernel, gpu string, ok bool) {
	if im == nil {
		return "", "", false
	}
	k, g := im.Facts["kernel_line"], im.Facts["gpu_variant"]
	if (k == "4.19" || k == "5.10") && (g == "gles" || g == "vulkan") {
		return k, g, true
	}
	return catalog.Variant(filepath.Base(im.Source))
}

// targetKind is the kind of what would be installed: the release image chosen for
// download, or the checked file.
func (s *screen) targetKind() (kernel, gpu string, ok bool) {
	if s.asset != nil {
		return catalog.Variant(s.asset.Name)
	}
	return imageKind(s.im)
}

// kindOK: installing this kind keeps the rule. A card that does not say what it runs
// takes what the user leaves chosen; so does an image that does not say (made before
// the four-image releases).
func (s *screen) kindOK(kernel, gpu string, known bool) bool {
	ck, cg := s.cardKind()
	if ck == "" || cg == "" || !known {
		return true
	}
	return kernel == ck && gpu == cg || s.picked() == kindKey(kernel, gpu)
}

// variantOK: what Next on the Release page would install keeps the rule.
func (s *screen) variantOK() bool {
	return s.kindOK(s.targetKind())
}

// imageOK: the checked image keeps the rule (the Release step is done).
func (s *screen) imageOK() bool {
	return s.kindOK(imageKind(s.im))
}

// allAssets is every release image found.
func (s *screen) allAssets() []catalog.Asset {
	return append(append([]catalog.Asset{}, s.assets...), s.otherAssets...)
}

// preselect chooses, for an update, the image of the chosen release that is of the
// card's kind (or of the kind the user picked). When the card says only its kernel
// line, the release's image with that kernel line and Vulkan, else with OpenGL ES.
// A file from this computer and a release without such an image stay as they are.
func (s *screen) preselect() {
	if !s.updating || s.busy || s.card == nil || !s.card.Installed || s.asset == nil {
		return
	}
	all := s.allAssets()
	k, g := s.card.Kernel, s.card.Graphics
	if p := s.picked(); p != "" {
		k, g, _ = strings.Cut(p, "-")
	}
	if k == "" {
		return
	}
	gpus := []string{g}
	if g == "" {
		gpus = []string{"vulkan", "gles"}
	}
	for _, gpu := range gpus {
		if a, ok := catalog.Matching(all, s.asset.Release, k, gpu); ok {
			if a.URL != s.asset.URL || a.Name != s.asset.Name {
				s.asset = &a
			}
			return
		}
	}
}

// kindText names a kind as the card runs it: "the 5.10 kernel with Vulkan".
func (s *screen) kindText(kernel, gpu string) string {
	if gpu == "vulkan" {
		return s.t("the %s kernel with Vulkan", kernel)
	}
	return s.t("the %s kernel with OpenGL ES only", kernel)
}

func (s *screen) gpuName(gpu string) string {
	if gpu == "vulkan" {
		return "Vulkan"
	}
	return s.t("OpenGL ES only")
}

// changeText is the one sentence on what a switch of kind changes.
func (s *screen) changeText(fromK, fromG, toK, toG string) string {
	switch {
	case fromK != toK && fromG != toG:
		return s.t("The kernel changes from %s to %s and the graphics driver from %s to %s. Apps, settings, saves and games stay.", fromK, toK, s.gpuName(fromG), s.gpuName(toG))
	case fromK != toK:
		return s.t("The kernel changes from %s to %s. Apps, settings, saves and games stay.", fromK, toK)
	case fromG != toG:
		return s.t("The graphics driver changes from %s to %s. Apps, settings, saves and games stay.", s.gpuName(fromG), s.gpuName(toG))
	}
	return s.t("This is what the card runs now.")
}

// variantNote is the Release page's text on what the card runs and what the update
// installs, and whether the release's images are offered to choose from.
func (s *screen) variantNote() (text string, choose bool) {
	if !s.updating || s.card == nil || !s.card.Installed {
		return "", false
	}
	ck, cg := s.cardKind()
	tk, tg, known := s.targetKind()
	release := ""
	if s.asset != nil {
		release = s.asset.Release
	}
	switch {
	case ck != "" && cg != "":
		runs := s.kindText(ck, cg)
		switch {
		case !known:
			return s.t("This card runs %s. The chosen image does not say which kernel it has: it was made before the releases with four images.", runs), false
		case tk == ck && tg == cg:
			return s.t("This card runs %s. The update keeps that.", runs), false
		case s.picked() == kindKey(tk, tg):
			return s.t("This card runs %s. You chose %s instead.", runs, s.kindText(tk, tg)) + " " + s.changeText(ck, cg, tk, tg), false
		case release != "":
			return s.t("This card runs %s. Release %s has no image of that kind. Choose one of its images below, or choose another release.", runs, release), true
		}
		return s.t("This card runs %s. The chosen file has %s. To install it anyway, choose Advanced...", runs, s.kindText(tk, tg)), false
	case ck != "":
		text = s.t("This card runs the %s kernel. Which graphics driver it has could not be read.", ck)
		if release == "" {
			return text, false
		}
		return text + " " + s.t("Choose the image to install:"), true
	}
	text = s.t("Which kernel and graphics driver this card runs could not be read.")
	if release == "" {
		return text, false
	}
	return text + " " + s.t("Choose the image to install:"), true
}

// refreshVariant fills the Release page's box under the release.
func (s *screen) refreshVariant() {
	if s.variantBox == nil {
		return
	}
	s.variantBox.RemoveAll()
	text, choose := s.variantNote()
	if text == "" {
		s.variantBox.Hide()
		return
	}
	kind := notePlain
	if !s.variantOK() {
		kind = noteWarning
	}
	objects := []fyne.CanvasObject{wrapped(text)}
	if choose {
		list := catalog.Of(s.allAssets(), s.asset.Release)
		var labels []string
		chosen := -1
		for i, a := range list {
			labels = append(labels, s.assetLabel(a))
			if a.URL == s.asset.URL && a.Name == s.asset.Name {
				chosen = i
			}
		}
		sel := widget.NewSelect(labels, nil)
		sel.PlaceHolder = s.t("Choose a release image")
		if chosen >= 0 && s.variantOK() {
			sel.SetSelectedIndex(chosen)
		}
		sel.OnChanged = func(string) {
			if i := sel.SelectedIndex(); i >= 0 && i < len(list) {
				s.pick(list[i])
			}
		}
		objects = append(objects, sel)
	}
	if ck, cg := s.cardKind(); ck != "" && cg != "" && (s.asset != nil || s.im != nil) {
		objects = append(objects, container.NewHBox(quiet(s.t("Advanced..."), s.switchKind)))
	}
	s.variantBox.Add(noteCard(kind, objects...))
	s.variantBox.Show()
}

// switchKind is Advanced... on the Release page of an update: another kind than the
// card runs, on purpose, with what that changes.
func (s *screen) switchKind() {
	ck, cg := s.cardKind()
	if s.busy || ck == "" || cg == "" {
		return
	}
	type option struct {
		label  string
		k, g   string
		known  bool
		asset  *catalog.Asset
		isFile bool
	}
	var options []option
	if s.asset != nil {
		for _, a := range catalog.Of(s.allAssets(), s.asset.Release) {
			a := a
			k, g, ok := catalog.Variant(a.Name)
			options = append(options, option{label: s.assetLabel(a), k: k, g: g, known: ok, asset: &a})
		}
	} else if s.im != nil {
		k, g, ok := imageKind(s.im)
		options = append(options, option{label: filepath.Base(s.im.Source), k: k, g: g, known: ok, isFile: true})
	}
	if len(options) == 0 {
		return
	}
	var labels []string
	for _, o := range options {
		labels = append(labels, o.label)
	}
	change := wrapped("")
	sel := widget.NewSelect(labels, func(string) {})
	sel.OnChanged = func(string) {
		i := sel.SelectedIndex()
		if i < 0 || i >= len(options) || !options[i].known {
			change.SetText("")
			return
		}
		change.SetText(s.changeText(ck, cg, options[i].k, options[i].g))
	}
	sel.SetSelectedIndex(0)
	for i, o := range options {
		if s.asset != nil && o.asset != nil && o.asset.URL == s.asset.URL && o.asset.Name == s.asset.Name {
			sel.SetSelectedIndex(i)
		}
	}
	box := container.NewVBox(wrapped(s.t("This card runs %s. An update normally keeps that. Choose another kind only if you want to change it.", s.kindText(ck, cg))), sel, change)
	d := dialog.NewCustomConfirm(s.t("Advanced..."), s.t("Use this image"), s.t("Cancel"), box, func(ok bool) {
		i := sel.SelectedIndex()
		if !ok || i < 0 || i >= len(options) {
			return
		}
		o := options[i]
		if o.isFile {
			s.kindPick, s.kindFor = kindKey(o.k, o.g), s.card.Fingerprint
			s.ready()
			return
		}
		if o.k == ck && o.g == cg {
			// back to the card's own kind: no choice on purpose any more
			s.kindPick = ""
			s.asset = o.asset
			s.ready()
			return
		}
		s.pick(*o.asset)
	}, s.win)
	d.Resize(fyne.NewSize(600, 320))
	d.Show()
}
