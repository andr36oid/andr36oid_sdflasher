// Package cli is the headless text-mode flasher. It drives exactly the same
// pipeline as the graphical app (internal/flash, internal/firmware,
// internal/catalog, internal/jobs, internal/platform) but never touches Fyne,
// OpenGL or GLFW, so it still runs on a machine whose graphics stack can't
// create a GL window - the Windows 11 "won't start" case in issue_tracker#11.
//
// It is selected with the -cli flag or A36_UI=cli, and is also used as an
// automatic fallback by the launcher when the graphical interface fails to
// initialise. Output is plain English on purpose: it goes to a terminal, which
// supplies its own fonts, so it does not go through the UI font subset or the
// i18n catalogue.
//
// Every core action the graphical app offers is reachable here, chosen with
// -action: flash (the default, including the in-place update/repartition that
// makes this tool the "patcher"), hotfix (apply hotfixes to an existing card),
// kernel (check for and install a kernel update), restore (roll an interrupted
// update back from its recovery backup) and list (just show the drives). All of
// them call the same privileged disk helper and the same verified-write code as
// the buttons in the window.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/jobs"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// Requested reports whether text mode was asked for explicitly, via the -cli /
// --cli flag or the A36_UI=cli environment variable.
func Requested(args []string) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("A36_UI")), "cli") {
		return true
	}
	for _, a := range args {
		if a == "-cli" || a == "--cli" {
			return true
		}
	}
	return false
}

// Run executes the text-mode flasher the user asked for (-cli / A36_UI=cli)
// and returns a process exit code.
func Run(version string, args []string) int {
	return run(version, args, func() {
		banner(version)
		fmt.Println("Welcome to text mode. This is the same flasher as the graphical app, with")
		fmt.Println("prompts instead of buttons, and it needs no graphics driver, GPU or OpenGL.")
		fmt.Println("By default it flashes or updates a card; the other jobs the window offers")
		fmt.Println("(apply hotfixes, kernel update, restore an interrupted update, just list the")
		fmt.Println("drives) are reachable with -action. Run with -h to see every option.")
		fmt.Println()
	})
}

// RunFallback executes the text-mode flasher after the graphical interface
// failed to start. cause is the underlying error and hint an optional extra
// troubleshooting line (see main's softwareGLHint); both are shown after a
// plain-language explanation, never first, so the user isn't greeted by a raw
// panic for something that is not their fault.
func RunFallback(version string, args []string, cause error, hint string) int {
	return run(version, args, func() {
		banner(version)
		fmt.Println("The graphical window could not open. It needs graphics (OpenGL) support that")
		fmt.Println("this PC does not have available right now. That is common on a fresh Windows")
		fmt.Println("11, in a virtual machine, or over Remote Desktop.")
		fmt.Println()
		fmt.Println("No problem: we will keep going right here in text mode. It flashes your card")
		fmt.Println("exactly the same way, with prompts instead of buttons. Just follow the steps")
		fmt.Println("below.")
		fmt.Println()
		fmt.Println("Technical details, only needed for troubleshooting:")
		if cause != nil {
			fmt.Println("  Error:", strings.TrimSpace(cause.Error()))
		}
		if hint != "" {
			fmt.Println("  Hint:", hint)
		}
		fmt.Printf("  Next time you can start text mode directly with:  %s -cli\n", exeName())
		fmt.Println()
	})
}

// errInputClosed is raised (via panic, caught in run) when stdin can't deliver
// another line: EOF on piped/closed input, or a broken/absent console. Every
// prompt helper would otherwise have to thread an ok flag back up to run.
var errInputClosed = errors.New("input closed")

// errNoDrives is the shared "nothing to write to" message, used by every action
// that needs a card. It names the -all escape hatch so a fixed-disk target is
// never a dead end.
var errNoDrives = errors.New("no removable card readers found. Insert the card and try again, or pass -all to include fixed disks (advanced)")

// opts carries the parsed flags into the action functions so each one keeps a
// short signature.
type opts struct {
	in          *bufio.Reader
	cache       string
	imagePath   string
	download    bool
	all         bool
	drive       string
	profile     string
	mode        string
	recovery    string
	noROMs      bool
	userdataGiB int
	assumeYes   bool
}

func run(version string, args []string, intro func()) (code int) {
	// The GUI-subsystem binary has no console; grab one before any I/O (flag
	// errors and -h usage included) so text mode isn't a silent no-op on
	// Windows. No-op elsewhere.
	ownWindow := AttachConsole()
	in := bufio.NewReader(os.Stdin)
	if ownWindow {
		// A console we created vanishes with the process; let the user read the
		// last message (success or the reason for a failure) first. Deferred
		// before the recover below so it runs after code is final.
		defer holdConsole(in)
	}
	defer func() {
		if r := recover(); r != nil {
			if r != errInputClosed {
				panic(r)
			}
			fmt.Println()
			fmt.Fprintln(os.Stderr, "No more input could be read. Nothing was written.")
			code = 1
		}
	}()

	fs := flag.NewFlagSet(exeName()+" -cli", flag.ContinueOnError)
	var (
		action    = fs.String("action", "", `what to do: "flash" (default), "hotfix", "kernel", "restore" or "list"`)
		imagePath = fs.String("image", "", "path to a release image (.img, .img.xz or .img.zip); flash action")
		download  = fs.Bool("download", false, "download the release image from GitHub instead of a local file; flash action")
		all       = fs.Bool("all", false, "also list fixed disks (advanced, dangerous)")
		modeFlag  = fs.String("mode", "", "install or update; prompted if omitted (flash action)")
		drive     = fs.String("drive", "", "target drive path to preselect, as shown by -action list (skips the drive menu)")
		profile   = fs.String("profile", "", "device profile ID or name to preselect; flash action")
		recovery  = fs.String("recovery", "", "path to the recovery.json (or its folder) from an interrupted update; restore action")
		noROMs    = fs.Bool("no-roms", false, "use all free space for Android, with no EASYROMS games drive (install only)")
		userdata  = fs.Int("userdata", 0, "size of the Android userdata area in GiB (0 keeps the default; install only)")
		assumeYes = fs.Bool("y", false, "skip confirmation prompts where safe (scripting; never for a fixed disk)")
	)
	// -cli / --cli are consumed by Requested; accept and ignore them here.
	fs.Bool("cli", false, "run in text mode (already handled)")
	if err := fs.Parse(stripCLIFlag(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	intro()

	if err := jobs.HelperAvailable(); err != nil {
		fmt.Fprintln(os.Stderr, "Cannot start: the disk helper that writes the card is missing.")
		fmt.Fprintln(os.Stderr, "If you launched this from inside the downloaded zip, unzip the whole")
		fmt.Fprintln(os.Stderr, "download first and run both files together from the extracted folder.")
		return 1
	}

	cache, err := jobs.CacheDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot create a work directory:", err)
		return 1
	}
	// Same housekeeping as the graphical app at start: drop staging scratch an
	// earlier run left behind, so the cache doesn't grow by a full image per flash.
	firmware.CleanWorkDir(cache)

	o := opts{
		in:          in,
		cache:       cache,
		imagePath:   strings.TrimSpace(*imagePath),
		download:    *download,
		all:         *all,
		drive:       strings.TrimSpace(*drive),
		profile:     strings.TrimSpace(*profile),
		mode:        strings.ToLower(strings.TrimSpace(*modeFlag)),
		recovery:    strings.TrimSpace(*recovery),
		noROMs:      *noROMs,
		userdataGiB: *userdata,
		assumeYes:   *assumeYes,
	}

	// "install"/"update" are accepted as actions too, as a shorthand for the
	// flash action with that mode, since that is how most people think of it.
	act := strings.ToLower(strings.TrimSpace(*action))
	if act == "install" || act == "update" {
		o.mode = act
		act = "flash"
	}
	switch act {
	case "", "flash":
		return runFlash(o)
	case "list", "drives":
		return runList(o)
	case "hotfix", "hotfixes":
		return runHotfix(o)
	case "kernel":
		return runKernel(o)
	case "restore":
		return runRestore(o)
	default:
		fmt.Fprintf(os.Stderr, "Unknown action %q. Use flash, hotfix, kernel, restore or list.\n", *action)
		return 2
	}
}

// runList prints the drives exactly as the other actions would show them, then
// stops. It is the headless "what cards does the app see" question, handy for
// finding the path to pass to -drive.
func runList(o opts) int {
	drives, err := listDrives(o.all)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not list drives:", err)
		return 1
	}
	if len(drives) == 0 {
		fmt.Fprintln(os.Stderr, errNoDrives.Error())
		return 1
	}
	fmt.Println("Detected drives:")
	for i, d := range drives {
		fmt.Printf("  [%d] %s\n", i+1, driveLabel(d))
	}
	return 0
}

// runFlash is the default action: the full set-up-or-update flow, the same five
// steps the window and the Android app show, ending in a verified write.
func runFlash(o opts) int {
	// 1. Release image.
	section(1, "Release")
	path := o.imagePath
	if path == "" && o.download {
		p, err := downloadRelease(o.in, o.cache)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		path = p
	}
	if path == "" {
		path = strings.Trim(strings.TrimSpace(prompt(o.in, "Path to the release image (.img, .img.xz or .img.zip), or Enter to download one: ")), "\"'")
		if path == "" {
			p, err := downloadRelease(o.in, o.cache)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			path = p
		}
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "No image given.")
		return 1
	}
	fmt.Println("Reading and verifying the image (this can take a while)...")
	ctx := context.Background()
	im, err := firmware.Load(ctx, path, o.cache, barProgress("verify"))
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not read the image:", err)
		return 1
	}
	fmt.Printf("Image: %s  (%s)\n\n", im.Version, humanSize(im.Size))

	// 2. Target drive.
	section(2, "Card")
	drive, err := pickDrive(o.in, o.all, o.drive)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// 3. Inspect the card (privileged read; may prompt for administrator rights).
	elevationNote()
	fmt.Println("Inspecting the card...")
	res, err := jobs.Run(jobs.Request{Action: "inspect", Drive: drive}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not inspect the card:", err)
		return 1
	}
	card := res.Card
	if card == nil {
		fmt.Fprintln(os.Stderr, "The card could not be inspected.")
		return 1
	}
	if card.Installed {
		fmt.Printf("The card already has andr36oid installed (profile %q).\n", card.Profile)
	} else {
		fmt.Println("The card has no andr36oid install yet.")
	}
	fmt.Println()

	// 4. Device profile.
	section(3, "Console")
	profile, err := chooseProfile(o.in, im.Profiles, o.profile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if profile == "" {
		fmt.Fprintln(os.Stderr, "No device profile chosen.")
		return 1
	}

	// 5. Mode and storage.
	section(4, "Options")
	mode := o.mode
	if mode != "install" && mode != "update" {
		mode = chooseMode(o.in, card.Installed)
	}
	if mode == "update" && !card.Installed {
		fmt.Println("Note: this card has no andr36oid install to update. An update keeps the")
		fmt.Println("card's data and only replaces the system; on a blank card an install is")
		fmt.Println("usually what you want.")
		if !o.assumeYes && !yes(o.in, "Continue with an update anyway? [y/N]: ", false) {
			fmt.Println("Aborted. Nothing was written.")
			return 1
		}
	}
	// Storage layout is an install-only choice, like the graphical app, which
	// disables it for an update (an update keeps the existing partitioning).
	noROMs := o.noROMs
	userdataGiB := o.userdataGiB
	if mode == "update" {
		noROMs = false
		userdataGiB = 0
	}

	// 6. Confirm.
	section(5, "Check and write")
	fmt.Println("About to write:")
	fmt.Printf("  Image   : %s\n", im.Version)
	fmt.Printf("  Profile : %s\n", profile)
	fmt.Printf("  Mode    : %s\n", mode)
	fmt.Printf("  Target  : %s\n", driveLabel(drive))
	if mode == "install" {
		if noROMs {
			fmt.Println("  Storage : all free space to Android, no EASYROMS games drive")
		} else if userdataGiB > 0 {
			fmt.Printf("  Storage : %d GiB Android userdata, the rest to EASYROMS\n", userdataGiB)
		}
		fmt.Println("  WARNING : an install ERASES EVERYTHING on this card.")
	}
	if drive.Fixed {
		fmt.Println("  WARNING : this is NOT a removable card. If it is the wrong disk, everything")
		fmt.Println("            on it (files, other operating systems, backups) is gone for good.")
		fmt.Println("            Check the name and the size once more before you go on.")
	}
	// -y is for scripting a card reader; a fixed disk always asks.
	if !o.assumeYes || drive.Fixed {
		fmt.Println()
		fmt.Printf("Type the target path (%s) to confirm, or anything else to abort: ", drive.Path)
		if !strings.EqualFold(strings.TrimSpace(readLine(o.in)), drive.Path) {
			fmt.Println("Aborted. Nothing was written.")
			return 1
		}
	}

	// 7. Write.
	req := jobs.Request{
		Action:          "write",
		Drive:           drive,
		Image:           im.Path,
		ImageHash:       im.SHA256,
		ImageSource:     im.Source,
		CardFingerprint: card.Fingerprint,
		Mode:            mode,
		Profile:         profile,
		NoROMs:          noROMs,
		UserdataGiB:     userdataGiB,
	}
	fmt.Println()
	fmt.Println("Writing. Keep the card connected and the computer awake until this finishes.")
	result, err := jobs.Run(req, func(p flash.Progress) { printProgress(p) })
	fmt.Println()
	if err != nil {
		if what, busy := platform.Busy(err.Error()); busy {
			fmt.Fprintln(os.Stderr, "The card is in use ("+what+"). Close anything using it and try again.")
		} else {
			fmt.Fprintln(os.Stderr, "The write failed:", err)
		}
		if result.Backup != "" {
			fmt.Fprintln(os.Stderr, "Recovery backup, if the card already had an install:", result.Backup)
		}
		return 1
	}
	fmt.Println("Done. The card was written and verified.")
	if mode == "update" && result.Backup != "" {
		fmt.Println("Recovery backup (keep until the console boots):", result.Backup)
	}

	// 8. Safe removal. The GUI offers an Eject button here; mirror it.
	if !o.assumeYes && yes(o.in, "Eject the card now so it is safe to remove? [Y/n]: ", true) {
		ejectDrive(drive)
	} else {
		fmt.Println("Eject the card with your computer's own eject before removing it.")
	}
	fmt.Println("Insert it into the console's system card slot. The first boot prepares storage")
	fmt.Println("and restarts the console; keep it powered on until Android setup appears.")
	return 0
}

// runHotfix mirrors the "Apply hotfixes" tool: pick an existing card, read its
// exact build, fetch the matching hotfixes, choose which to copy, and write
// only BOOT/hotfix. It never reflashes and never touches games or data.
func runHotfix(o opts) int {
	section(1, "Card")
	drive, err := pickDrive(o.in, o.all, o.drive)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	section(2, "Hotfixes")
	elevationNote()
	fmt.Println("Reading the card's build and checking for hotfixes...")
	listed, err := jobs.Run(jobs.Request{Action: "hotfix-list", Drive: drive}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not read the card:", err)
		return 1
	}
	ctx := context.Background()
	index, err := firmware.FetchHotfixIndex(ctx, firmware.HotfixIndexURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	offer := index.ForBuild(listed.BuildIdentity)
	if len(offer.Hotfixes) == 0 {
		fmt.Println("No hotfixes are available for this exact build. Nothing to do.")
		return 0
	}
	selected := chooseHotfixes(o.in, offer, o.assumeYes)
	if len(selected) == 0 {
		fmt.Println("No hotfixes selected. Nothing was written.")
		return 0
	}

	section(3, "Apply")
	fmt.Println("Writing hotfixes to BOOT. Keep the card connected until this finishes.")
	applied, err := jobs.Run(jobs.Request{Action: "hotfix-apply", Drive: drive, HotfixOffer: offer, HotfixFiles: selected}, func(p flash.Progress) { printProgress(p) })
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Applying hotfixes failed:", err)
		return 1
	}
	if len(applied.HotfixResults) == 0 {
		fmt.Println("No hotfixes were written.")
		return 0
	}
	failed := false
	for _, r := range applied.HotfixResults {
		if r.Error != "" {
			failed = true
			fmt.Printf("  failed  : %s (%s)\n", r.File, r.Error)
		} else {
			fmt.Printf("  installed: %s\n", r.File)
		}
	}
	fmt.Println("Hotfixes will be applied when the console next starts.")
	if !o.assumeYes {
		ejectDrive(drive)
	}
	if failed {
		return 1
	}
	return 0
}

// runKernel mirrors the "Check for kernel update" tool: pick a card, read its
// kernel build, offer a newer compatible kernel if one exists, and install it
// into BOOT with verification. Apps, settings, saves and games stay in place.
func runKernel(o opts) int {
	section(1, "Card")
	drive, err := pickDrive(o.in, o.all, o.drive)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	section(2, "Kernel")
	elevationNote()
	fmt.Println("Reading the card's kernel and checking for updates...")
	listed, err := jobs.Run(jobs.Request{Action: "kernel-list", Drive: drive}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not read the card's kernel:", err)
		return 1
	}
	if listed.KernelIdentity == nil {
		fmt.Fprintln(os.Stderr, firmware.KernelNoBuild)
		return 1
	}
	ctx := context.Background()
	offer, err := catalog.CheckKernelUpdate(ctx, *listed.KernelIdentity)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not check for kernel updates:", err)
		return 1
	}
	if offer == nil || !firmware.KernelCompatible(offer.CompatibleBuilds, offer.Current.Build) {
		fmt.Println("No kernel update is available for this card. Nothing to do.")
		return 0
	}
	fmt.Printf("Kernel update available: %s\n", offer.Version)
	fmt.Printf("  %s\n", offer.Title)
	if offer.Description != "" {
		fmt.Printf("  %s\n", strings.Join(strings.Fields(offer.Description), " "))
	}
	fmt.Printf("  Download size: %.1f MiB\n", float64(offer.Size)/(1<<20))
	fmt.Println("The kernel files in BOOT will be updated and verified. Games and data stay in place.")
	if !o.assumeYes && !yes(o.in, "Install this kernel update? [y/N]: ", false) {
		fmt.Println("Skipped. Nothing was written.")
		return 0
	}

	section(3, "Apply")
	fmt.Println("Downloading and writing the kernel update. Keep the card connected.")
	_, err = jobs.Run(jobs.Request{Action: "kernel-apply", Drive: drive, KernelOffer: offer}, func(p flash.Progress) { printProgress(p) })
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Kernel update could not finish:", err)
		return 1
	}
	fmt.Println("Kernel update installed and verified.")
	if !o.assumeYes {
		ejectDrive(drive)
	}
	return 0
}

// runRestore mirrors the "Restore an interrupted update" tool: given the
// recovery.json an interrupted update left behind, it verifies the backup
// belongs to the chosen card and restores the previous operating system.
func runRestore(o opts) int {
	section(1, "Card")
	drive, err := pickDrive(o.in, o.all, o.drive)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	section(2, "Recovery backup")
	dir, err := resolveRecovery(o.in, o.recovery)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("Restore the saved operating system? Userdata and games remain protected.")
	if !o.assumeYes && !yes(o.in, "Continue with the restore? [y/N]: ", false) {
		fmt.Println("Aborted. Nothing was written.")
		return 1
	}

	section(3, "Restore")
	elevationNote()
	fmt.Println("Verifying and restoring the recovery backup. Keep the card connected.")
	_, err = jobs.Run(jobs.Request{Action: "restore", Drive: drive, Recovery: dir}, func(p flash.Progress) { printProgress(p) })
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "The restore failed:", err)
		return 1
	}
	fmt.Println("The previous installation was restored and verified.")
	if !o.assumeYes {
		ejectDrive(drive)
	}
	return 0
}

// downloadRelease lists the release images on GitHub, lets the user pick one,
// downloads it into the cache (verified by size and digest by the catalogue),
// and returns its local path. It is the headless twin of the window's
// "Download a release..." button.
func downloadRelease(in *bufio.Reader, cache string) (string, error) {
	ctx := context.Background()
	fmt.Println("Checking GitHub releases...")
	assets, err := catalog.List(ctx)
	if err != nil {
		return "", fmt.Errorf("could not list releases: %w", err)
	}
	if len(assets) == 0 {
		return "", errors.New("no disk images are attached to the releases yet; choose an image from disk instead")
	}
	fmt.Println("Release images available to download:")
	for i, a := range assets {
		fmt.Printf("  [%d] %s  %s  (%s)\n", i+1, a.Release, a.Name, humanSize(a.Size))
	}
	a := assets[chooseIndex(in, "Choose a release image to download", len(assets))]
	fmt.Printf("Downloading %s ...\n", a.Name)
	p, err := catalog.Download(ctx, a, cache, barProgress("download"))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	return p, nil
}

// resolveRecovery turns a user-supplied path into the recovery folder the
// helper expects (the folder that holds recovery.json, whose parent holds the
// original request.json). It accepts either the recovery.json file or its
// folder, and prompts if nothing was given.
func resolveRecovery(in *bufio.Reader, given string) (string, error) {
	p := given
	if p == "" {
		p = strings.Trim(strings.TrimSpace(prompt(in, "Path to the recovery.json from the interrupted update (or its folder): ")), "\"'")
	}
	if p == "" {
		return "", errors.New("no recovery backup given")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("could not open that path: %w", err)
	}
	if st.IsDir() {
		if _, err := os.Stat(filepath.Join(p, "recovery.json")); err != nil {
			return "", errors.New("that folder has no recovery.json; choose the folder the interrupted update saved")
		}
		return p, nil
	}
	if filepath.Base(p) != "recovery.json" {
		return "", errors.New("choose the recovery.json file saved by the interrupted update, or its folder")
	}
	return filepath.Dir(p), nil
}

func banner(version string) {
	title := "andr36oid SD Flasher " + version
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", len(title)))
	fmt.Println()
}

// section prints a step header. The flash action uses the same five steps the
// graphical app and the Android app show in their step bar; the other actions
// number their own, shorter sequences the same way so every surface reads alike.
func section(n int, name string) {
	fmt.Println()
	title := fmt.Sprintf("Step %d: %s", n, name)
	fmt.Println(title)
	fmt.Println(strings.Repeat("-", len(title)))
}

// elevationNote warns, on Windows only, that the privileged disk helper may ask
// for administrator rights. Harmless and silent everywhere else.
func elevationNote() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Println("Windows may ask for administrator permission so the flasher can reach the card.")
	fmt.Println("Choose Yes if it does; the flasher needs it to read and write the card.")
}

// ejectDrive tries a safe eject and explains the outcome, the same courtesy the
// window's Eject button gives.
func ejectDrive(d platform.Drive) {
	if e := platform.Eject(d); e != nil {
		fmt.Println("The card could not be ejected here:", e)
		fmt.Println("Eject it with your computer's own eject before removing it.")
		return
	}
	fmt.Println("The card is ejected. You can remove it.")
}

func exeName() string {
	name := filepath.Base(os.Args[0])
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// holdConsole keeps a console window we created ourselves open until the user
// dismisses it; otherwise the window closes with the process and the last
// message is never seen. Read errors are ignored: if input is broken too,
// there is nothing left to wait for.
func holdConsole(in *bufio.Reader) {
	fmt.Println()
	fmt.Print("Press Enter to close this window.")
	in.ReadString('\n')
}

func listDrives(all bool) ([]platform.Drive, error) {
	if all {
		return platform.ListAll()
	}
	return platform.List()
}

// pickDrive lists the drives and returns the chosen one. With want set (from
// -drive) it selects the matching path without prompting, so every action can
// be scripted; without it, it prints the numbered menu the flash flow has
// always shown and asks.
func pickDrive(in *bufio.Reader, all bool, want string) (platform.Drive, error) {
	drives, err := listDrives(all)
	if err != nil {
		return platform.Drive{}, fmt.Errorf("could not list drives: %w", err)
	}
	if len(drives) == 0 {
		return platform.Drive{}, errNoDrives
	}
	if want != "" {
		for _, d := range drives {
			if strings.EqualFold(strings.TrimSpace(d.Path), want) {
				fmt.Printf("Selected: %s\n\n", driveLabel(d))
				return d, nil
			}
		}
		return platform.Drive{}, fmt.Errorf("no listed drive has the path %q; run with -action list to see the paths", want)
	}
	fmt.Println("Detected drives:")
	for i, d := range drives {
		fmt.Printf("  [%d] %s\n", i+1, driveLabel(d))
	}
	d := drives[chooseIndex(in, "Choose the card", len(drives))]
	fmt.Printf("Selected: %s\n\n", driveLabel(d))
	return d, nil
}

// driveLabel is a drive as the list shows it: a fixed disk (only listed with
// -all) is marked, as in the graphical app, so it can't be mistaken for a card.
func driveLabel(d platform.Drive) string {
	if d.Fixed {
		return "NOT a removable card: " + d.Label()
	}
	return d.Label()
}

// chooseProfile selects a device profile. A preselect (from -profile) matching
// a profile ID or name wins without prompting; otherwise it prompts, exactly as
// before.
func chooseProfile(in *bufio.Reader, profiles []firmware.Profile, want string) (string, error) {
	if len(profiles) == 0 {
		return "", errors.New("the image has no device profiles")
	}
	if want != "" {
		for _, p := range profiles {
			if strings.EqualFold(p.ID, want) || strings.EqualFold(p.Name, want) {
				fmt.Printf("Device profile: %s\n", p.Name)
				if p.Experimental {
					fmt.Println("This profile is experimental and may not work on every unit.")
				}
				return p.ID, nil
			}
		}
		return "", fmt.Errorf("no profile in this image matches %q; omit -profile to choose from the list", want)
	}
	if len(profiles) == 1 {
		fmt.Printf("Device profile: %s\n", profiles[0].Name)
		return profiles[0].ID, nil
	}
	fmt.Println("Device profiles in this image:")
	for i, p := range profiles {
		tag := ""
		if p.Experimental {
			tag = " (experimental)"
		}
		fmt.Printf("  [%d] %s%s\n", i+1, p.Name, tag)
	}
	idx := chooseIndex(in, "Choose the profile that matches your console", len(profiles))
	if profiles[idx].Experimental {
		fmt.Println("This profile is experimental and may not work on every unit.")
	}
	return profiles[idx].ID, nil
}

// chooseHotfixes shows the offered hotfixes, bug fixes marked and selected by
// default like the graphical checklist, and returns the chosen files. With -y
// it takes the bug-fix default without prompting.
func chooseHotfixes(in *bufio.Reader, offer *firmware.HotfixOffer, assumeYes bool) []string {
	fmt.Println("Hotfixes for this build:")
	for i, h := range offer.Hotfixes {
		mark, tag := " ", "optional extra"
		if h.Preselected() {
			mark, tag = "x", "bug fix"
		}
		fmt.Printf("  [%s] %d. %s  (%s, %.1f MiB)\n", mark, i+1, h.Title, tag, float64(h.Size)/(1<<20))
		if h.Description != "" {
			fmt.Printf("        %s\n", strings.Join(strings.Fields(h.Description), " "))
		}
	}
	fmt.Println("Bug fixes [x] are installed by default. Optional extras [ ] are not.")
	if assumeYes {
		return preselectedHotfixes(offer)
	}
	for {
		line := strings.TrimSpace(prompt(in, "Numbers to install (space-separated), Enter for bug fixes only, or 'none': "))
		switch {
		case line == "":
			return preselectedHotfixes(offer)
		case strings.EqualFold(line, "none"):
			return nil
		}
		files, ok := parseHotfixSelection(line, offer)
		if !ok {
			fmt.Println("Please enter the numbers shown above, separated by spaces.")
			continue
		}
		return files
	}
}

func preselectedHotfixes(offer *firmware.HotfixOffer) []string {
	files := []string{}
	for _, h := range offer.Hotfixes {
		if h.Preselected() {
			files = append(files, h.File)
		}
	}
	return files
}

// parseHotfixSelection turns "1 3 4" into the matching hotfix files, in list
// order and without duplicates. It reports false on any out-of-range or
// non-numeric token so the caller can ask again.
func parseHotfixSelection(line string, offer *firmware.HotfixOffer) ([]string, bool) {
	picked := make([]bool, len(offer.Hotfixes))
	for _, tok := range strings.Fields(line) {
		v, err := strconv.Atoi(tok)
		if err != nil || v < 1 || v > len(offer.Hotfixes) {
			return nil, false
		}
		picked[v-1] = true
	}
	files := []string{}
	for i, p := range picked {
		if p {
			files = append(files, offer.Hotfixes[i].File)
		}
	}
	return files, true
}

func chooseMode(in *bufio.Reader, installed bool) string {
	fmt.Println("Operation:")
	fmt.Println("  [1] install - fresh install, ERASES the whole card")
	fmt.Println("  [2] update  - keep your games and data, replace the system")
	def := 1
	if installed {
		def = 2
	}
	for {
		line := strings.TrimSpace(prompt(in, fmt.Sprintf("Choose [1-2] (default %d): ", def)))
		switch line {
		case "1":
			return "install"
		case "2":
			return "update"
		case "":
			if def == 2 {
				return "update"
			}
			return "install"
		}
		fmt.Println("Please enter 1 or 2.")
	}
}

func chooseIndex(in *bufio.Reader, label string, n int) int {
	for {
		line := strings.TrimSpace(prompt(in, fmt.Sprintf("%s [1-%d]: ", label, n)))
		v, err := strconv.Atoi(line)
		if err == nil && v >= 1 && v <= n {
			return v - 1
		}
		fmt.Println("Please enter a number between 1 and", n, ".")
	}
}

// yes asks a yes/no question; an empty answer gives def.
func yes(in *bufio.Reader, msg string, def bool) bool {
	for {
		switch strings.ToLower(strings.TrimSpace(prompt(in, msg))) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		case "":
			return def
		}
		fmt.Println("Please answer y or n.")
	}
}

func prompt(in *bufio.Reader, msg string) string {
	fmt.Print(msg)
	return readLine(in)
}

func readLine(in *bufio.Reader) string {
	line, err := in.ReadString('\n')
	if err != nil && strings.TrimRight(line, "\r\n") == "" {
		// Any read error with nothing usable is terminal: EOF (piped/closed
		// input) or a broken/absent stdin (e.g. a console we couldn't attach).
		// Treating only io.EOF as terminal let a non-EOF error spin chooseIndex
		// at 100% CPU, since each retry re-read returns immediately. Unwind to
		// run, which reports it and still holds a self-made console window open.
		panic(errInputClosed)
	}
	return strings.TrimRight(line, "\r\n")
}

// stripCLIFlag removes -cli/--cli so the FlagSet doesn't trip over them when it
// also has to handle a bare -cli with no value.
func stripCLIFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "-cli" || a == "--cli" || a == "--started-elevated" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func printProgress(p flash.Progress) {
	label := p.Phase
	if p.Name != "" {
		label = p.Phase + ": " + p.Name
	}
	if p.Total > 0 {
		pct := int(float64(p.Done) / float64(p.Total) * 100)
		fmt.Printf("\r%-60.60s %3d%%", label, pct)
		return
	}
	fmt.Printf("\r%-60.60s     ", label)
}

func barProgress(phase string) func(int64, int64) {
	return func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(float64(done) / float64(total) * 100)
		fmt.Printf("\r%-12.12s %3d%%", phase, pct)
	}
}

func humanSize(n int64) string {
	const g = 1 << 30
	if n >= g {
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(g))
	}
	return fmt.Sprintf("%.0f MiB", float64(n)/float64(1<<20))
}
