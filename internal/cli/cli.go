// Package cli is the headless text-mode flasher. It drives exactly the same
// flash pipeline as the graphical app (internal/flash, internal/firmware,
// internal/jobs, internal/platform) but never touches Fyne, OpenGL or GLFW, so
// it still runs on a machine whose graphics stack can't create a GL window —
// the Windows 11 "won't start" case in issue_tracker#11.
//
// It is selected with the -cli flag or A36_UI=cli, and is also used as an
// automatic fallback by the launcher when the graphical interface fails to
// initialise. Output is plain English on purpose: it goes to a terminal, which
// supplies its own fonts, so it does not go through the UI font subset or the
// i18n catalogue.
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
		fmt.Println("Welcome to text mode. This is the same flasher as the graphical app, just")
		fmt.Println("with prompts instead of buttons. It walks you through the same steps: choose")
		fmt.Println("the image, choose the card, confirm, then write and verify.")
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
		imagePath = fs.String("image", "", "path to a release image (.img, .img.xz or .img.zip)")
		all       = fs.Bool("all", false, "also list fixed disks (advanced, dangerous)")
		modeFlag  = fs.String("mode", "", "install or update; prompted if omitted")
		assumeYes = fs.Bool("y", false, "skip the final confirmation prompt (scripting; never for a fixed disk)")
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

	// 1. Release image.
	section(1, "Release")
	path := strings.TrimSpace(*imagePath)
	if path == "" {
		path = prompt(in, "Path to the release image (.img, .img.xz or .img.zip): ")
		path = strings.Trim(strings.TrimSpace(path), "\"'")
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "No image given.")
		return 1
	}
	fmt.Println("Reading and verifying the image (this can take a while)...")
	ctx := context.Background()
	im, err := firmware.Load(ctx, path, cache, barProgress("verify"))
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not read the image:", err)
		return 1
	}
	fmt.Printf("Image: %s  (%s)\n\n", im.Version, humanSize(im.Size))

	// 2. Target drive.
	section(2, "Card")
	drives, err := listDrives(*all)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Could not list drives:", err)
		return 1
	}
	if len(drives) == 0 {
		fmt.Fprintln(os.Stderr, "No removable card readers found. Insert the card and try again,")
		fmt.Fprintln(os.Stderr, "or pass -all to include fixed disks (advanced).")
		return 1
	}
	fmt.Println("Detected drives:")
	for i, d := range drives {
		fmt.Printf("  [%d] %s\n", i+1, driveLabel(d))
	}
	drive := drives[chooseIndex(in, "Choose the card to write", len(drives))]
	fmt.Printf("Selected: %s\n\n", driveLabel(drive))

	// 3. Inspect the card (privileged read; may prompt for administrator rights).
	if runtime.GOOS == "windows" {
		fmt.Println("Windows may ask for administrator permission, once to inspect the card and")
		fmt.Println("once to write it. Choose Yes both times; the flasher needs it for the card.")
	}
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
	profile := chooseProfile(in, im.Profiles)
	if profile == "" {
		fmt.Fprintln(os.Stderr, "No device profile chosen.")
		return 1
	}

	// 5. Mode.
	section(4, "Options")
	mode := strings.ToLower(strings.TrimSpace(*modeFlag))
	if mode != "install" && mode != "update" {
		mode = chooseMode(in, card.Installed)
	}
	if mode == "update" && !card.Installed {
		fmt.Println("Note: this card has no andr36oid install to update. An update keeps the")
		fmt.Println("card's data and only replaces the system; on a blank card an install is")
		fmt.Println("usually what you want.")
		if !*assumeYes && !yes(in, "Continue with an update anyway? [y/N]: ", false) {
			fmt.Println("Aborted. Nothing was written.")
			return 1
		}
	}

	// 6. Confirm.
	section(5, "Check and write")
	fmt.Println("About to write:")
	fmt.Printf("  Image   : %s\n", im.Version)
	fmt.Printf("  Profile : %s\n", profile)
	fmt.Printf("  Mode    : %s\n", mode)
	fmt.Printf("  Target  : %s\n", driveLabel(drive))
	if mode == "install" {
		fmt.Println("  WARNING : an install ERASES EVERYTHING on this card.")
	}
	if drive.Fixed {
		fmt.Println("  WARNING : this is NOT a removable card. If it is the wrong disk, everything")
		fmt.Println("            on it (files, other operating systems, backups) is gone for good.")
		fmt.Println("            Check the name and the size once more before you go on.")
	}
	// -y is for scripting a card reader; a fixed disk always asks.
	if !*assumeYes || drive.Fixed {
		fmt.Println()
		fmt.Printf("Type the target path (%s) to confirm, or anything else to abort: ", drive.Path)
		if !strings.EqualFold(strings.TrimSpace(readLine(in)), drive.Path) {
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
		CardFingerprint: card.Fingerprint,
		Mode:            mode,
		Profile:         profile,
	}
	fmt.Println()
	fmt.Println("Writing. Keep the card connected and the computer awake until this finishes.")
	_, err = jobs.Run(req, func(p flash.Progress) { printProgress(p) })
	fmt.Println()
	if err != nil {
		if what, busy := platform.Busy(err.Error()); busy {
			fmt.Fprintln(os.Stderr, "The card is in use ("+what+"). Close anything using it and try again.")
		} else {
			fmt.Fprintln(os.Stderr, "The write failed:", err)
		}
		return 1
	}
	fmt.Println("Done. The card was written and verified.")

	// 8. Safe removal. The GUI offers an Eject button here; mirror it.
	if !*assumeYes && yes(in, "Eject the card now so it is safe to remove? [Y/n]: ", true) {
		if e := platform.Eject(drive); e != nil {
			fmt.Println("The card could not be ejected here:", e)
			fmt.Println("Eject it with your computer's own eject before removing it.")
		} else {
			fmt.Println("The card is ejected. You can remove it.")
		}
	} else {
		fmt.Println("Eject the card with your computer's own eject before removing it.")
	}
	fmt.Println("Insert it into the console's system card slot. The first boot prepares storage")
	fmt.Println("and restarts the console; keep it powered on until Android setup appears.")
	return 0
}

func banner(version string) {
	title := "andr36oid SD Flasher " + version
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", len(title)))
	fmt.Println()
}

// section prints a step header, the same five steps the graphical app and the
// Android app show in their step bar, so all three surfaces read alike.
func section(n int, name string) {
	fmt.Println()
	title := fmt.Sprintf("Step %d of 5: %s", n, name)
	fmt.Println(title)
	fmt.Println(strings.Repeat("-", len(title)))
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

// driveLabel is a drive as the list shows it: a fixed disk (only listed with
// -all) is marked, as in the graphical app, so it can't be mistaken for a card.
func driveLabel(d platform.Drive) string {
	if d.Fixed {
		return "NOT a removable card: " + d.Label()
	}
	return d.Label()
}

func chooseProfile(in *bufio.Reader, profiles []firmware.Profile) string {
	if len(profiles) == 0 {
		return ""
	}
	if len(profiles) == 1 {
		fmt.Printf("Device profile: %s\n", profiles[0].Name)
		return profiles[0].ID
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
	return profiles[idx].ID
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
