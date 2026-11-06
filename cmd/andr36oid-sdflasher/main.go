package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/andr36oid/andr36oid-sdflasher/internal/cli"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
	"github.com/andr36oid/andr36oid-sdflasher/internal/ui"
)

func main() {
	args := os.Args[1:]

	// Text mode, explicitly requested with -cli / --cli or A36_UI=cli. This path
	// never touches Fyne/OpenGL, so it runs where the graphical interface can't
	// start (issue_tracker#11 on Windows 11 without a usable OpenGL driver). The
	// disk helper elevates per job on its own, so we don't ElevateAtStart here.
	if cli.Requested(args) {
		os.Exit(cli.Run(ui.Version, args))
	}

	// Windows: ask for administrator rights once, here, instead of for every card job
	if platform.ElevateAtStart() {
		return
	}

	// Try the graphical interface. If it can't initialise (most often no usable
	// OpenGL driver), fall back to text mode so the flasher still runs instead of
	// failing to start.
	if err := ui.RunSafe(); err != nil {
		fmt.Fprintln(os.Stderr, "The graphical interface could not start:", err)
		if hint := softwareGLHint(); hint != "" {
			fmt.Fprintln(os.Stderr, hint)
		}
		fmt.Fprintln(os.Stderr, "Falling back to text mode. Re-run with -cli to use it directly.")
		fmt.Fprintln(os.Stderr)
		os.Exit(cli.Run(ui.Version, args))
	}
}

// softwareGLHint explains the most likely cause of a GUI start failure on
// Windows: the release zip ships Mesa's llvmpipe as opengl32.dll beside the exe
// (see third_party/mesa) so the window opens even without a GPU driver; when
// only the exe was copied out of the zip that safety net is gone.
func softwareGLHint() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dll := filepath.Join(filepath.Dir(exe), "opengl32.dll")
	if _, err := os.Stat(dll); err != nil {
		return "Hint: " + dll + " is missing. The release zip ships this software OpenGL renderer beside the exe; extract the whole zip and keep the files together."
	}
	return ""
}
