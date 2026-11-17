package platform

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// InhibitSleep keeps the computer from going to sleep (or idling into it) until the
// returned function is called, which may be called any number of times. On Linux it
// takes a systemd-logind "block" inhibitor for sleep and idle over D-Bus, as
// systemd-inhibit does; the lock is a file descriptor, so it also ends when the
// process does. Without logind (or without permission) it does nothing.
func InhibitSleep(why string) func() {
	conn, e := dbus.ConnectSystemBus()
	if e != nil {
		return func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var fd dbus.UnixFD
	e = conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").CallWithContext(ctx,
		"org.freedesktop.login1.Manager.Inhibit", 0, "sleep:idle", "andr36oid SD Flasher", why, "block").Store(&fd)
	if e != nil {
		conn.Close()
		return func() {}
	}
	lock := os.NewFile(uintptr(fd), "sleep-inhibitor")
	var once sync.Once
	return func() {
		once.Do(func() {
			lock.Close()
			conn.Close()
		})
	}
}
