package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

var Version = "development"

// dataCardFirstStart: what the updated data card of an eMMC install does at its first start
const dataCardFirstStart = "At its first start, it offers to update andr36oid on the eMMC to this release too."

type screen struct {
	win     fyne.Window
	locale  string
	started bool
	busy    bool
	// writing: the card write itself runs (it can be stopped, not cut off)
	writing bool
	cancel  context.CancelFunc
	// closeAfter: the window closes when the stopped download has ended
	closeAfter bool
	// activity: what runs while busy (closeprompt.go), for the close prompt
	activity int

	// the frame: side bar on the left, one page at a time on the right
	body      *fyne.Container
	sideSteps [stepCount]*widget.Button
	// sideDims veil the steps a little while they can't be used (a write runs)
	sideDims [stepCount]*canvas.Rectangle
	pages    [stepCount]fyne.CanvasObject
	backs    [stepCount]*widget.Button
	nexts    [stepCount]*widget.Button
	step     int
	inSteps  bool // a step page is shown (not a fix flow, the working page or a result)
	language *widget.Button
	details  *widget.Button
	log      []string

	update       *catalog.AppUpdate
	updateBanner *fyne.Container
	updateCancel context.CancelFunc

	// start: what the user wants to do
	task       int
	startCards [3]*choiceCard

	// release
	im                              *firmware.Image
	assets, otherAssets             []catalog.Asset
	asset                           *catalog.Asset // chosen for download, nil for a file from disk
	assetsLooking, assetsDone       bool
	assetsErr                       error
	releaseCard                     *choiceCard
	releaseLabel                    *widget.Label
	progress                        *widget.ProgressBar
	stopLoad                        *widget.Button
	dtbLabel                        *widget.Label
	dtbFile, dtbManifest, dtbLookup string
	// an update: the kind (kindKey) the user chose on purpose, for the card with
	// the fingerprint kindFor; variantBox says what the card runs (variant.go)
	kindPick, kindFor string
	variantBox        *fyne.Container

	// card
	drives     []platform.Drive
	drive      *platform.Drive
	card       *flash.Card
	listing    bool
	listed     bool
	inspecting bool
	allDrives  bool   // advanced: fixed disks are listed too
	lostDrive  string // the chosen card that is no longer listed, until another is chosen
	allCheck   *widget.Check
	cardRows   *fyne.Container
	cardLabel  *widget.Label
	cardNote   *fyne.Container
	refresh    *widget.Button

	// console
	profile        string
	experimentalOK map[string]bool // experimental profiles whose warning was agreed to
	search         *widget.Entry
	consoleLead    *widget.Label
	consoleList    *fyne.Container
	expBox         *fyne.Container
	custom         string
	customAccepted bool
	convert        bool // custom is the console's own DTB, converted while writing
	customLabel    *widget.Label
	customClear    *widget.Button

	// options
	updating          bool
	noROMs            *widget.Check
	storageBox        *fyne.Container
	storageKept       *widget.Label
	sizeLabel         *widget.Label
	sizeSlider        *widget.Slider
	sizeAuto          *widget.Button
	sizeSetting       bool                    // refreshStorage is moving the slider
	userdataGiB       int                     // Android size for a new card, 0: recommended
	guests            map[string]*guestLookup // by system, for guestFor
	guestFor          string                  // release image the game system lookups were made for
	guestChoice       string                  // a catalog.GuestSources ID or catalog.NoGuest
	guestLast         string                  // the system chosen before the box was unticked
	guestChosen       bool                    // the user picked guestChoice (no more preselection)
	guestLabels       []string                // guestRadio's options, in guestOptions' order
	guestUpdating     bool                    // refreshGuest is setting guestRadio
	guestRadio        *widget.RadioGroup
	guestCheck        *widget.Check
	guestCheckRow     fyne.CanvasObject
	guestChange       *widget.Button
	guestInfo         *widget.Label
	guestBox          *fyne.Container
	guestLookupCancel context.CancelFunc // stops the running lookups
	guestLookupCtx    context.Context    // the running lookups' context
	guestRetry        *widget.Button     // looks again for a system whose lookup failed
	fixCount          int                // bug fixes published for the release, -1 while unknown
	fixesFor          string
	applyFixes        bool
	fixBox            *fyne.Container
	fixCheck          *widget.Check
	fixLabel          *tapLabel
	fixInfo           *widget.Label
	advancedBox       *fyne.Container
	system            string // a different system image (GSI), "" for andr36oid's
	systemAccepted    bool
	systemLabel       *widget.Label
	bootloaderOptions *bootloaderPicker
	bootloaderChoice  firmware.BootloaderChoice

	// write
	writeTitle *widget.Label
	summary    *fyne.Container
	writeNotes *fyne.Container
	eraseCheck *widget.Check
	eraseRow   fyne.CanvasObject
	eraseText  string
	eraseOK    bool
	action     *widget.Button
}

// RunSafe runs the graphical interface and returns an error instead of hanging
// or crashing when it can't initialise, so the launcher can fall back to text
// mode rather than the app "not starting" at all.
//
// Two failure shapes are covered. With no display at all (GLFW itself fails)
// Fyne panics, which the recover below turns into an error. With a display but
// no usable OpenGL (a fresh Windows 11 with only the Microsoft Basic Display
// adapter and no opengl32.dll beside the exe, a VM, RDP) Fyne 2.8 does not
// panic: it logs "window creation error" and then idles forever in its event
// loop with no window, invisibly. probeGL catches that case up front. A driver
// that aborts the process outright can't be caught here; the -cli flag is the
// guaranteed text-mode path then.
func RunSafe() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	if err := probeGL(); err != nil {
		return err
	}
	Run()
	return nil
}

func Run() {
	// Drop any staging scratch left behind by an earlier run before showing the
	// window, so the cache doesn't keep growing by a full image every flash.
	if dir, e := jobs.CacheDir(); e == nil {
		firmware.CleanWorkDir(dir)
	}
	a := app.NewWithID("io.github.andr36oid.sdflasher")
	a.SetIcon(appIcon)
	a.Settings().SetTheme(uiTheme())
	w := a.NewWindow("andr36oid SD Flasher")
	s := &screen{win: w, locale: preferredLocale(a)}
	s.localizeDialogs()
	w.Resize(fyne.NewSize(980, 680))
	w.CenterOnScreen()
	// closing names what runs and stops what can be stopped (closeprompt.go)
	w.SetCloseIntercept(s.closeRequested)
	go func() {
		for range time.Tick(5 * time.Minute) {
			fyne.Do(s.keepStaged)
		}
	}()
	if e := jobs.HelperAvailable(); e != nil {
		s.showHelperMissing()
		w.ShowAndRun()
		return
	}
	if a.Preferences().Bool("sd-card-advice-v1") {
		s.start()
	} else {
		s.showCardAdvice()
	}
	w.ShowAndRun()
}

// showHelperMissing fills the window with a plain explanation when the disk
// helper isn't beside the app, most often because the executable was run from
// inside the downloaded zip rather than an extracted folder.
func (s *screen) showHelperMissing() {
	body := wrapped(s.t("The disk helper that writes the card is missing. If you launched this from inside the downloaded zip, unzip the whole download first and run the flasher from the extracted folder - keep both andr36oid SD Flasher files together."))
	quit := widget.NewButton(s.t("Quit"), func() { s.win.Close() })
	s.win.SetContent(container.NewPadded(pageView(title(s.t("The flasher can't start")), nil, body, nil, quit)))
}

func (s *screen) t(key string, args ...any) string {
	if len(args) == 0 {
		key, args = firmware.DTBErrorDetails(key)
	}
	return i18n.Text(s.locale, key, args...)
}

// failureHints is the hint block shown after the error of a failed write: "" for
// failures that are not the card's or the reader's (flash.FailureHints).
func (s *screen) failureHints(e error) string {
	out := ""
	for _, line := range flash.FailureHints(flash.Classify(e), false) {
		if out != "" {
			out += "\n"
		}
		out += s.t(line)
	}
	return out
}

// busyError: the card's volumes were in use and wouldn't unmount (platform.BusyPrefix)
func busyError(e error) (string, bool) {
	if e == nil {
		return "", false
	}
	return platform.Busy(e.Error())
}

func wrapped(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}

// build makes every page and the frame around them. It runs again when the
// language changes; everything chosen so far stays.
func (s *screen) build() {
	// nothing is brought up to date while the pages are made
	s.action = nil
	oldROMs := false
	if s.noROMs != nil {
		oldROMs = s.noROMs.Checked
	}
	if s.fixCount == 0 && s.fixesFor == "" {
		s.fixCount = -1
		s.applyFixes = true
	}
	s.noROMs = widget.NewCheck(s.t("Use all available storage for Android"), func(bool) { s.ready() })
	s.noROMs.SetChecked(oldROMs)
	s.buildStorage()
	s.buildGuest()
	s.pages = [stepCount]fyne.CanvasObject{
		s.startPage(), s.releasePage(), s.cardPage(), s.consolePage(), s.optionsPage(), s.writePage(),
	}
	s.populateImage()
	s.selectProfile(s.profile)
	s.customLabel.SetText(s.dtbText())
	s.body = container.NewStack()
	s.win.SetPadded(false)
	s.win.SetContent(container.New(atLeast{fyne.NewSize(900, 620)}, container.NewBorder(nil, nil, s.sideBar(), nil, container.NewPadded(s.body))))
	s.showStep(s.step)
	if !s.inSteps {
		s.showStep(stepStart)
	}
	s.refreshCards()
	s.ready()
}

// ready brings everything that depends on the choices up to date.
func (s *screen) ready() {
	if s.action == nil {
		return
	}
	s.preselect()
	if s.bootloaderOptions != nil {
		s.bootloaderOptions.setTarget(s.im != nil, s.drive != nil && s.drive.Fixed || s.card != nil && s.card.DataCard)
	}
	s.refreshGuest()
	s.refreshStorage()
	s.refreshFixes()
	s.refreshRelease()
	s.refreshCardNote()
	if s.customClear != nil {
		if s.custom == "" {
			s.customClear.Hide()
		} else {
			s.customClear.Show()
		}
	}
	s.refreshSteps()
	s.summarize()
	if s.writable() {
		s.action.Enable()
	} else {
		s.action.Disable()
	}
}

// writable: everything is chosen and, where the page asks for it, ticked.
func (s *screen) writable() bool {
	return !s.busy && s.reachable(stepWrite) && (!s.needsTick() || s.eraseOK)
}

func (s *screen) setBusy(v bool) {
	s.busy = v
	if !v {
		s.activity = actNone
	}
	for _, b := range []*widget.Button{s.refresh, s.language} {
		if b == nil {
			continue
		}
		if v {
			b.Disable()
		} else {
			b.Enable()
		}
	}
	if s.noROMs != nil {
		if v || s.updating {
			s.noROMs.Disable()
		} else {
			s.noROMs.Enable()
		}
	}
	s.refreshCards()
	s.ready()
}

// cacheTotal is the size of the flasher's cache directory, or 0 if it can't be
// read. It walks the cache, so it runs off the UI goroutine for a large cache.
func cacheTotal() int64 {
	dir, e := jobs.CacheDir()
	if e != nil {
		return 0
	}
	total, _, _ := firmware.WorkDirUsage(dir)
	return total
}

// clearCache frees the flasher's cached downloads and unpacked images. If the
// cache holds recovery backups, it asks first whether to delete those too.
func (s *screen) clearCache(done func()) {
	if s.busy {
		return
	}
	dir, e := jobs.CacheDir()
	if e != nil {
		s.showError(e)
		return
	}
	do := func(deleteBackups bool) {
		firmware.ClearWorkDir(dir, deleteBackups)
		s.logf("cleared cached files (backups too: %v)", deleteBackups)
		done()
	}
	if _, _, backups := firmware.WorkDirUsage(dir); backups > 0 {
		var d dialog.Dialog
		keep := widget.NewButton(s.t("Keep backups"), func() { d.Hide(); do(false) })
		del := widget.NewButton(s.t("Delete everything"), func() { d.Hide(); do(true) })
		body := s.t("This frees the space used by downloaded and unpacked images. You also have %d recovery backup(s) you could still restore from. Delete those too?", backups)
		d = dialog.NewCustom(s.t("Clear cached files"), s.t("Cancel"), container.NewVBox(wrapped(body), container.NewHBox(keep, del)), s.win)
		fitDialog(d, body+"\n\n", s.win)
		d.Show()
		return
	}
	do(false)
}

// humanBytes renders a byte count as a short human-readable size.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func (s *screen) chooseImage() {
	if s.busy {
		return
	}
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, e error) {
		if e != nil {
			s.showError(e)
			return
		}
		if r == nil {
			return
		}
		p := r.URI().Path()
		r.Close()
		s.asset = nil
		s.loadImage(p)
	}, s.win)
	// release images come packed: only .img.xz, .img.gz and .img.zip are listed
	d.SetFilter(storage.NewExtensionFileFilter([]string{".xz", ".gz", ".zip"}))
	directory, _ := os.Getwd()
	if s.im != nil && s.im.Source != "" && filepath.IsAbs(s.im.Source) {
		directory = filepath.Dir(s.im.Source)
	}
	if location, err := storage.ListerForURI(storage.NewFileURI(directory)); err == nil {
		d.SetLocation(location)
	}
	d.Show()
}

func (s *screen) loadImage(p string, origin ...string) {
	// a build too old for this flasher: refused by its name, before it is unpacked
	if date, old := firmware.TooOld(p); old {
		s.releaseLabel.SetText(s.t("Image rejected. Choose an andr36oid Treble release."))
		s.info(s.t("This image is too old"), s.t("This image was built on %s. Builds from before 22 October 2026 don't work with this flasher. Download a current release instead.", date), s.win)
		return
	}
	s.logf("checking image %s", p)
	s.setBusy(true)
	s.activity = imageActivity(p)
	// The previous selection's staged image is now superseded; remove it so a
	// session that tries several releases doesn't pile up decompressed copies.
	if s.im != nil {
		if dir, e := jobs.CacheDir(); e == nil {
			firmware.CleanStagedImage(dir, s.im.Path)
		}
	}
	s.im = nil
	s.profile = ""
	s.custom = ""
	s.customAccepted = false
	s.convert = false
	s.customLabel.SetText(s.dtbText())
	s.releaseLabel.SetText(s.t("Checking the release image..."))
	if s.activity == actUnpackImage {
		s.releaseLabel.SetText(s.t("Unpacking and checking the release image..."))
	}
	s.progress.SetValue(0)
	s.progress.Show()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.ready()
	go func() {
		dir, e := jobs.CacheDir()
		var im *firmware.Image
		if e == nil {
			im, e = firmware.Load(ctx, p, dir, s.progressFn())
		}
		fyne.Do(func() {
			stopped := ctx.Err() != nil
			cancel()
			s.cancel = nil
			s.progress.Hide()
			s.setBusy(false)
			if s.closeAfter {
				s.win.Close()
				return
			}
			if e != nil {
				s.logf("image rejected: %v", e)
				s.releaseLabel.SetText(s.t("Image rejected. Choose an andr36oid Treble release."))
				if !stopped {
					s.showError(e)
				} else {
					s.releaseLabel.SetText("")
				}
				return
			}
			s.releaseLabel.SetText("")
			if len(origin) > 0 {
				im.Source = origin[0]
			}
			s.logf("image %s: %d profiles, build %s", im.Version, len(im.Profiles), im.BuildIdentity)
			s.im = im
			s.populateImage()
			s.autoProfile()
			if s.inSteps && s.step == stepImage {
				s.showStep(s.after(stepImage))
			}
			s.ready()
		})
	}()
}

func (s *screen) progressFn() func(int64, int64) {
	return func(n, total int64) {
		fyne.Do(func() {
			if total > 0 {
				s.progress.SetValue(float64(n) / float64(total))
			}
		})
	}
}

// autoProfile chooses the profile the card was set up with, or Panel 4 for a new
// card. An experimental one is only chosen here: its warning waits on the console page.
func (s *screen) autoProfile() {
	if s.card != nil && s.card.Profile != "" {
		if !s.selectProfile(s.card.Profile) {
			s.profile = ""
			s.refreshConsoles()
		}
		return
	}
	if s.profile == "" {
		s.selectProfile("Panels/Panel4")
	}
}

func (s *screen) selectProfile(id string) bool {
	if s.im == nil {
		return false
	}
	for _, p := range s.im.Profiles {
		if p.ID == id {
			s.profile = id
			s.refreshConsoles()
			return true
		}
	}
	return false
}

func (s *screen) populateImage() {
	if s.im == nil {
		return
	}
	s.refreshConsoles()
	s.lookupGuest()
	s.lookupImageDTBs()
	s.lookupFixes()
	if s.dtbLabel != nil && s.im.DTBVersion != "" {
		s.dtbLabel.SetText(s.t("Using device trees from %s", s.im.DTBVersion))
	}
}

// tooSmall: a new installation on a card under flash.MinCardBytes
func (s *screen) tooSmall() bool {
	return !s.updating && s.drive != nil && flash.TooSmall(s.drive.Size) != nil
}

func (s *screen) tooSmallText() string {
	return s.t("This card is too small. andr36oid needs a card of at least 16 GB; this one has %.1f GB.", float64(s.drive.Size)/1e9)
}

func (s *screen) chooseCustom() {
	if s.busy {
		return
	}
	s.confirmDialog(s.t("Custom Android DTB"), s.t(firmware.CustomWarning)+"\n\n"+s.t("Only the Android DTB is replaced. The selected shipped profile still supplies the bootloader display files. Continue only if this DTB was made for your console and this release."), func(ok bool) {
		if !ok {
			return
		}
		d := dialog.NewFileOpen(func(r fyne.URIReadCloser, e error) {
			if e != nil {
				s.showError(e)
				return
			}
			if r == nil {
				return
			}
			s.custom = r.URI().Path()
			r.Close()
			s.customAccepted = true
			s.convert = false
			s.customLabel.SetText(s.dtbText())
			s.ready()
		}, s.win)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".dtb"}))
		d.Show()
	}, s.win)
}

func (s *screen) clearCustom() {
	s.custom = ""
	s.customAccepted = false
	s.convert = false
	s.customLabel.SetText(s.dtbText())
	s.ready()
}

// lookupReleases finds the published releases once, so the newest can be offered.
func (s *screen) lookupReleases() {
	if !s.started || s.assetsLooking || s.assetsDone {
		return
	}
	s.assetsLooking = true
	s.refreshRelease()
	go func() {
		assets, e := catalog.List(context.Background())
		fyne.Do(func() {
			s.assetsLooking, s.assetsDone, s.assetsErr = false, e == nil, e
			if e == nil {
				s.assets, s.otherAssets = catalog.Split(assets)
				s.logf("%d release images found", len(assets))
				if s.asset == nil && s.im == nil && len(s.assets) > 0 {
					s.asset = &s.assets[0]
				}
			} else {
				s.logf("release list failed: %v", e)
			}
			s.ready()
		})
	}()
}

// releases is the list of every published release, for "Other releases".
func (s *screen) releases() {
	if s.busy {
		return
	}
	if !s.assetsDone {
		s.assetsDone, s.assetsErr = false, nil
		s.setBusy(true)
		go func() {
			assets, e := catalog.List(context.Background())
			fyne.Do(func() {
				s.setBusy(false)
				if e != nil {
					s.showError(e)
					return
				}
				s.assetsDone = true
				s.assets, s.otherAssets = catalog.Split(assets)
				s.releases()
			})
		}()
		return
	}
	if len(s.assets)+len(s.otherAssets) == 0 {
		s.info(s.t("No image downloads available"), s.t("No disk images are attached to andr36oid/releases yet. You can choose an image from disk."), s.win)
		return
	}
	// each release's recommended image first; the other kernels only on request.
	// An update shows each release's image of the card's kind; another kind is
	// chosen only on purpose, under Advanced... on the Release page.
	first, others := s.assets, s.otherAssets
	ck, cg := s.cardKind()
	if p := s.picked(); p != "" {
		ck, cg, _ = strings.Cut(p, "-")
	}
	if ck != "" && cg != "" {
		first, others = catalog.SplitFor(s.allAssets(), ck, cg)
		others = nil
	}
	shown := first
	labels := func(list []catalog.Asset) []string {
		var opts []string
		for _, a := range list {
			opts = append(opts, s.assetLabel(a))
		}
		return opts
	}
	pick := widget.NewSelect(labels(shown), nil)
	pick.PlaceHolder = s.t("Choose a release image")
	box := container.NewVBox(wrapped(s.t("Only Treble images can be installed. Each download is checked before the card can be written.")), pick)
	if len(first) > 0 {
		pick.SetSelectedIndex(0)
	}
	if len(others) > 0 {
		more := widget.NewCheck(s.t("Other kernels"), func(on bool) {
			shown = first
			if on {
				shown = append(append([]catalog.Asset{}, first...), others...)
			}
			pick.SetOptions(labels(shown))
			pick.SetSelectedIndex(0)
		})
		box.Add(more)
		box.Add(wrapped(s.t("Try the recommended image first. If something does not work on your console, flash the same release with kernel 4.19: your apps, saves and games stay when you choose Update.")))
	}
	d := dialog.NewCustomConfirm(s.t("Download a release"), s.t("Download"), s.t("Cancel"), box, func(ok bool) {
		if !ok {
			return
		}
		i := pick.SelectedIndex()
		if i < 0 || i >= len(shown) {
			return
		}
		a := shown[i]
		s.asset = &a
		s.download(a)
	}, s.win)
	d.Resize(fyne.NewSize(600, 360))
	d.Show()
}

// assetLabel is a release image as the lists show it.
func (s *screen) assetLabel(a catalog.Asset) string {
	text := a.Release + " - " + a.Name
	if k, g, ok := catalog.Variant(a.Name); ok {
		text = a.Release + " - " + s.variantText(k, g)
		if catalog.Recommended(a.Name) {
			text += " - " + s.t("recommended")
		}
	}
	return text
}

// variantText names an image's kernel and graphics driver.
func (s *screen) variantText(kernel, gpu string) string {
	if gpu == "vulkan" {
		return s.t("kernel %s, Vulkan", kernel)
	}
	return s.t("kernel %s, OpenGL ES only", kernel)
}

func (s *screen) download(a catalog.Asset) {
	if s.busy {
		return
	}
	s.logf("downloading %s", a.URL)
	s.setBusy(true)
	s.activity = actDownload
	s.progress.Show()
	s.progress.SetValue(0)
	s.releaseLabel.SetText(s.t("downloading") + ": " + a.Name)
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.ready()
	go func() {
		dir, e := jobs.CacheDir()
		var p string
		if e == nil {
			p, e = catalog.Download(ctx, a, dir, s.progressFn())
		}
		fyne.Do(func() {
			stopped := ctx.Err() != nil
			cancel()
			s.cancel = nil
			s.progress.Hide()
			s.setBusy(false)
			if s.closeAfter {
				s.win.Close()
				return
			}
			if stopped {
				s.logf("download stopped")
				s.releaseLabel.SetText("")
				return
			}
			if e != nil {
				s.logf("download failed: %v", e)
				s.releaseLabel.SetText("")
				s.downloadFailed(a, e)
				return
			}
			s.loadImage(p, a.URL)
		})
	}()
}

// downloadFailed says why a release download did not finish, that nothing was
// written, and offers to download it again (a damaged download was deleted).
func (s *screen) downloadFailed(a catalog.Asset, e error) {
	text := s.explain(e)
	if catalog.ErrDamagedDownload.Is(e) {
		text += " " + s.t("The damaged file was deleted.")
	}
	if _, _, ok := i18n.FindState(e); !ok {
		text += "\n\n" + s.t(flash.StateUntouched.Key())
	}
	text += "\n\n" + s.t("Technical details") + ":\n" + e.Error()
	if !downloadRetryable(e) {
		s.info(s.t("The download did not finish"), text, s.win)
		return
	}
	s.ask(s.t("The download did not finish"), text, s.t("Download again"), func(ok bool) {
		if ok {
			s.download(a)
		}
	})
}

// downloadRetryable: downloading the same file again can work (not for a file
// that is gone, one without a checksum, or a full or unwritable disk).
func downloadRetryable(e error) bool {
	for _, f := range []*i18n.Format{catalog.ErrNotThere, catalog.ErrNoDigest, firmware.ErrNoSpace, jobs.ErrCacheFolder, i18n.CauseDiskFull} {
		if f.Is(e) {
			return false
		}
	}
	if key, _, ok := i18n.Find(e); ok && key == i18n.CauseDiskFull.Key() {
		return false
	}
	return true
}

// restoreBackup brings back the system an interrupted update saved, on drive.
func (s *screen) restoreBackup(drive platform.Drive) {
	if s.busy {
		return
	}
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, e error) {
		if e != nil {
			s.showError(e)
			return
		}
		if r == nil {
			return
		}
		file := r.URI().Path()
		r.Close()
		if filepath.Base(file) != "recovery.json" {
			s.showError(fmt.Errorf("%s", s.t("Choose the recovery.json file saved by the interrupted update.")))
			return
		}
		s.confirmDialog(s.t("Restore previous installation"), s.driveTitle(drive)+"\n\n"+s.t("Restore the saved operating system? Userdata and games remain protected."), func(ok bool) {
			if !ok {
				return
			}
			s.setBusy(true)
			status := wrapped(s.t("Verifying and restoring the recovery backup..."))
			bar := widget.NewProgressBarInfinite()
			s.show(pageView(title(s.t("Restore previous installation")), wrapped(s.driveTitle(drive)), container.NewVBox(status, bar), nil))
			s.logf("restoring %s on %s", file, drive.Path)
			go func() {
				_, e := jobs.Run(jobs.Request{Action: "restore", Drive: drive, Recovery: filepath.Dir(file)}, nil)
				fyne.Do(func() {
					s.setBusy(false)
					s.showFix()
					if e != nil {
						s.logf("restore failed: %v", e)
						s.showError(e)
					} else {
						s.info(s.t("Restored"), s.t("The previous installation was restored and verified."), s.win)
					}
				})
			}()
		}, s.win)
	}, s.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	d.Show()
}
