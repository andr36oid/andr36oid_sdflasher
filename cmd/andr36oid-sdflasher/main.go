package main

import (
	"fmt"
	"os"

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
		fmt.Fprintln(os.Stderr, "Falling back to text mode. Re-run with -cli to use it directly.")
		fmt.Fprintln(os.Stderr)
		os.Exit(cli.Run(ui.Version, args))
	}
}
