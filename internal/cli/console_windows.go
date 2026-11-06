//go:build windows

package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachConsole makes stdio usable for the GUI-subsystem binary. The app is
// linked with -H=windowsgui, so a /SUBSYSTEM:WINDOWS process has no console and
// os.Stdin/Stdout/Stderr are invalid handles - which would make the whole text
// mode a silent no-op. Attach to the parent console if we were launched from
// one (`andr36oid-sdflasher.exe -cli` from cmd/PowerShell); otherwise allocate a
// fresh console window (the double-clicked GUI-failure fallback), then rebind
// the Go standard streams onto it so prompts and input work. Must run before
// any console I/O. No-op on every other platform (see console_other.go).
func attachConsole() {
	k32 := windows.NewLazySystemDLL("kernel32.dll")
	attach := k32.NewProc("AttachConsole")
	alloc := k32.NewProc("AllocConsole")

	const attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS = (DWORD)-1
	if r, _, _ := attach.Call(attachParentProcess); r == 0 {
		// No inheritable console (double-clicked): make our own window.
		if r2, _, _ := alloc.Call(); r2 == 0 {
			return // couldn't get a console; leave the streams untouched
		}
	}
	rebind("CONIN$", &os.Stdin, windows.STD_INPUT_HANDLE)
	rebind("CONOUT$", &os.Stdout, windows.STD_OUTPUT_HANDLE)
	rebind("CONOUT$", &os.Stderr, windows.STD_ERROR_HANDLE)
}

// rebind opens a console device and points both the Go std stream and the
// process std handle (so exec'd children inherit it) at it.
func rebind(name string, stream **os.File, std uint32) {
	h, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return
	}
	*stream = h
	_ = windows.SetStdHandle(std, windows.Handle(h.Fd()))
}
