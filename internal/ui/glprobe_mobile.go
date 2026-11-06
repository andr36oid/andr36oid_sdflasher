//go:build android || ios

package ui

// probeGL is a no-op on mobile: the GLFW desktop driver isn't used there, and
// a GL ES context is always available.
func probeGL() error { return nil }
