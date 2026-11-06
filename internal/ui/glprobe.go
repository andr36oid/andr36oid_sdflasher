//go:build !android && !ios

package ui

import (
	"fmt"

	"github.com/go-gl/glfw/v3.4/glfw"
)

// probeGL checks, before Fyne starts, that this machine can open an OpenGL
// window at all. It uses the same GLFW build and the same context version Fyne
// asks for, on a hidden 1x1 window, then tears everything down again so Fyne's
// own glfw.Init starts clean (GLFW supports init/terminate cycles; init hints
// Fyne sets afterwards are unaffected).
//
// Why this exists: when GLFW initialises but no OpenGL context can be created
// (Windows with only the Microsoft Basic Display Adapter and no opengl32.dll
// beside the exe, a VM without GPU passthrough, Remote Desktop, an X server
// without GLX), Fyne 2.8 does not panic. It logs "window creation error" and
// then runs its event loop forever with a window that has no viewport, so the
// process sits invisibly in the background. RunSafe's recover() never fires
// and the text-mode fallback is never reached. A failed probe turns that hang
// into an error RunSafe can return.
func probeGL() error {
	if err := glfw.Init(); err != nil {
		return fmt.Errorf("graphics (GLFW) could not initialise: %w", err)
	}
	defer glfw.Terminate()
	glfw.DefaultWindowHints()
	glfw.WindowHint(glfw.Visible, glfw.False)
	glfw.WindowHint(glfw.ContextVersionMajor, 2)
	glfw.WindowHint(glfw.ContextVersionMinor, 1)
	w, err := glfw.CreateWindow(1, 1, "andr36oid SD Flasher", nil, nil)
	if err != nil {
		return fmt.Errorf("no OpenGL available for a window: %w", err)
	}
	w.Destroy()
	return nil
}
