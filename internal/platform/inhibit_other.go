//go:build !linux && !darwin && !windows

package platform

// InhibitSleep does nothing here (see inhibit_linux.go).
func InhibitSleep(why string) func() { return func() {} }
