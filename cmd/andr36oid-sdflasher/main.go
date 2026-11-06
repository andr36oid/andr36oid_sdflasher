package main

import (
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

	// Try the graphical interface. On Windows the release zip ships Mesa's
	// software OpenGL (opengl32.dll beside the exe), so this normally works even
	// without a GPU driver. If it still can't initialise, fall back to text mode
	// so the flasher runs instead of failing to start. The text mode explains
	// why it is being used (and attaches a console first, so the explanation is
	// actually visible from a windowsgui binary; nothing may be printed before
	// cli.Run* on Windows).
	if err := ui.RunSafe(); err != nil {
		os.Exit(cli.RunFallback(ui.Version, args, err, softwareGLHint()))
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
		return dll + " is missing. The release zip ships this software OpenGL renderer beside the exe so the window opens even without a graphics driver; extract the whole zip and keep the files together, then try again."
	}
	return dll + " is present but could not be used. Delete it to try your graphics driver's own OpenGL instead, or keep using text mode."
}
