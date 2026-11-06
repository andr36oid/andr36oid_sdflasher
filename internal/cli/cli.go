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
	"flag"
	"fmt"
	"os"
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

// Run executes the text-mode flasher and returns a process exit code.
func Run(version string, args []string) int {
	fs := flag.NewFlagSet("andr36oid-sdflasher -cli", flag.ContinueOnError)
	var (
		imagePath = fs.String("image", "", "path to a release image (.img, .img.xz or .img.zip)")
		all       = fs.Bool("all", false, "also list fixed disks (advanced, dangerous)")
		modeFlag  = fs.String("mode", "", "install or update; prompted if omitted")
		assumeYes = fs.Bool("y", false, "skip the final confirmation prompt (scripting)")
	)
	// -cli / --cli are consumed by Requested; accept and ignore them here.
	fs.Bool("cli", false, "run in text mode (already handled)")
	if err := fs.Parse(stripCLIFlag(args)); err != nil {
		return 2
	}

	// The GUI-subsystem binary has no console; grab one before any I/O so text
	// mode isn't a silent no-op on Windows (no-op elsewhere).
	attachConsole()

	in := bufio.NewReader(os.Stdin)
	fmt.Printf("andr36oid SD Flasher %s - text mode\n", version)
	fmt.Println("No graphical interface is used in this mode.")
	fmt.Println()

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

	// 1. Release image.
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
		fmt.Printf("  [%d] %s\n", i+1, d.Label())
	}
	drive := drives[chooseIndex(in, "Choose the card to write", len(drives))]
	fmt.Printf("Selected: %s\n\n", drive.Label())

	// 3. Inspect the card (privileged read; may prompt for administrator rights).
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
	profile := chooseProfile(in, im.Profiles)
	if profile == "" {
		fmt.Fprintln(os.Stderr, "No device profile chosen.")
		return 1
	}

	// 5. Mode.
	mode := strings.ToLower(strings.TrimSpace(*modeFlag))
	if mode != "install" && mode != "update" {
		mode = chooseMode(in, card.Installed)
	}

	// 6. Confirm.
	fmt.Println()
	fmt.Println("About to write:")
	fmt.Printf("  Image   : %s\n", im.Version)
	fmt.Printf("  Profile : %s\n", profile)
	fmt.Printf("  Mode    : %s\n", mode)
	fmt.Printf("  Target  : %s\n", drive.Label())
	if mode == "install" {
		fmt.Println("  WARNING : an install ERASES EVERYTHING on this card.")
	}
	if !*assumeYes {
		fmt.Println()
		fmt.Printf("Type the target path (%s) to confirm, or anything else to abort: ", drive.Path)
		if strings.TrimSpace(readLine(in)) != drive.Path {
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
	fmt.Println("Done. The card is ready. You can remove it.")
	return 0
}

func listDrives(all bool) ([]platform.Drive, error) {
	if all {
		return platform.ListAll()
	}
	return platform.List()
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
	default:
		return chooseMode(in, installed)
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
		// at 100% CPU, since each retry re-read returns immediately.
		fmt.Println()
		os.Exit(1)
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
