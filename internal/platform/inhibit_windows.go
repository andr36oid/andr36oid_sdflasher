package platform

import (
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

const (
	esSystemRequired = 0x00000001
	esContinuous     = 0x80000000
)

var setThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// InhibitSleep (see inhibit_linux.go) sets SetThreadExecutionState on a thread of
// its own, which keeps the state until it clears it again (the state belongs to the
// thread that set it, and ends with the process).
func InhibitSleep(why string) func() {
	if setThreadExecutionState.Find() != nil {
		return func() {}
	}
	set, done := make(chan struct{}), make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		setThreadExecutionState.Call(esContinuous | esSystemRequired)
		close(set)
		<-done
		setThreadExecutionState.Call(esContinuous)
	}()
	<-set
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}
