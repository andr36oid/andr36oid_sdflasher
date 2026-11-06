//go:build !windows

package cli

// AttachConsole is a no-op off Windows: a text-mode binary there is a normal
// console program and already has a working controlling terminal when launched
// from one. (A GUI launch from Finder/.desktop still has no terminal, but that
// is outside the Windows "won't start" case this mode targets.) It never
// allocates a window, so it always reports false.
func AttachConsole() (allocated bool) { return false }
