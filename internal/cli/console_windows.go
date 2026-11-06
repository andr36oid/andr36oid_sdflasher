//go:build windows

package cli

import (
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

var (
	consoleOnce      sync.Once
	consoleAllocated bool
)

// AttachConsole makes stdio usable for the GUI-subsystem binary. The app is
// linked with -H=windowsgui, so a /SUBSYSTEM:WINDOWS process has no console and
// os.Stdin/Stdout/Stderr are invalid handles - which would make the whole text
// mode a silent no-op. Attach to the parent console if we were launched from
// one (`andr36oid-sdflasher.exe -cli` from cmd/PowerShell); otherwise allocate a
// fresh console window (the double-clicked GUI-failure fallback), then rebind
// the Go standard streams onto it so prompts and input work.
//
// Only streams that are actually unusable are rebound: a stream the launcher
// redirected to a file or pipe (`-cli > log.txt`, `echo path | ... -cli`) is a
// valid handle already and must keep going where the user pointed it.
//
// It reports whether a brand-new console window was created. That window
// disappears the moment the process exits, so the caller should hold it open
// until the user has read the last message. Idempotent; must run before any
// console I/O. No-op on every other platform (see console_other.go).
func AttachConsole() (allocated bool) {
	consoleOnce.Do(attachConsole)
	return consoleAllocated
}

func attachConsole() {
	needIn := !usable(windows.STD_INPUT_HANDLE)
	needOut := !usable(windows.STD_OUTPUT_HANDLE)
	needErr := !usable(windows.STD_ERROR_HANDLE)
	if !needIn && !needOut && !needErr {
		return // everything was redirected for us; nothing to attach
	}

	k32 := windows.NewLazySystemDLL("kernel32.dll")
	attach := k32.NewProc("AttachConsole")
	alloc := k32.NewProc("AllocConsole")

	const attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS = (DWORD)-1
	if r, _, _ := attach.Call(attachParentProcess); r == 0 {
		// No inheritable console (double-clicked): make our own window.
		if r2, _, _ := alloc.Call(); r2 == 0 {
			return // couldn't get a console; leave the streams untouched
		}
		consoleAllocated = true
	}
	if needIn {
		rebind("CONIN$", &os.Stdin, windows.STD_INPUT_HANDLE)
	}
	if needOut {
		rebind("CONOUT$", &os.Stdout, windows.STD_OUTPUT_HANDLE)
	}
	if needErr {
		rebind("CONOUT$", &os.Stderr, windows.STD_ERROR_HANDLE)
	}
}

// usable reports whether a process standard handle refers to something we can
// do I/O on (a redirected file or pipe, or an inherited console handle).
func usable(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	if err != nil || h == 0 || h == windows.InvalidHandle {
		return false
	}
	t, err := windows.GetFileType(h)
	return err == nil && t != windows.FILE_TYPE_UNKNOWN
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
