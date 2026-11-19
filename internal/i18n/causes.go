package i18n

import (
	"strings"
	"sync"
)

// A cause explains a low-level error (of the system, the network, the card reader)
// that carries no message of its own: "unexpected EOF", "permission denied",
// "no such host". The text of such an error is known, but it says nothing to the
// people who use the apps. Find falls back to the first cause whose words are in
// an error's text when the text has no registered message (NewFormat); the message
// of the cause says what happened, the likely cause and what to do. The error's own
// text stays the technical detail.
//
// Causes match on the text because errors cross from the disk helper and into the
// Android app as text.
type cause struct {
	format *Format
	match  func(lower string) bool
}

var (
	causesMu sync.Mutex
	causes   []cause
)

// NewCause registers key (an en.json key without verbs) as the message of the errors
// whose lower-cased text match says yes to. Causes are tried in the order they are
// registered, so the more particular ones come first.
func NewCause(key string, match func(lower string) bool) *Format {
	f := NewFormat(key)
	causesMu.Lock()
	causes = append(causes, cause{f, match})
	causesMu.Unlock()
	return f
}

// anyOf is a match for the errors that contain one of words (lower case).
func anyOf(words ...string) func(string) bool {
	return func(s string) bool {
		for _, w := range words {
			if strings.Contains(s, w) {
				return true
			}
		}
		return false
	}
}

// onCard: the text names a raw disk (the card) rather than a file.
func onCard(s string) bool {
	return anyOf("/dev/", `\\.\physicaldrive`, "physicaldrive", "rdisk", "usb reader", "card reader")(s)
}

// deviceGone: the words of a card or reader that failed or went away.
var deviceGone = anyOf("input/output error", "i/o device error", "i/o error", "no such device", "no medium", "device not configured", "device is not ready", "device does not recognize the command", "cyclic redundancy check", "semaphore timeout", "device has been removed", "device was removed", "media changed", "medium changed", "reader stopped answering", "short write")

// The causes, the more particular first. Their texts are en.json keys.
var (
	// The card filled up before the image did: the card is smaller than it says, or
	// the reader cut it short.
	CauseCardEnded = NewCause("The card ended before everything was written: it holds less than it reports, or the card reader cannot reach all of it. Try a different card reader; if that does not help, the card cannot be used.",
		func(s string) bool {
			return onCard(s) && anyOf("no space left", "not enough space", "end of file", "unexpected eof", "short write")(s)
		})
	// The card's BOOT partition (FAT) filled up while files were added to it.
	CauseBootFull = NewCause("The card's BOOT partition has no room left for these files. Choose fewer hotfixes, or write the card again with a new installation, then try again.",
		func(s string) bool {
			return strings.Contains(s, "boot/") && anyOf("no space left", "unable to allocate", "could not allocate", "failed to allocate")(s)
		})
	CauseDiskFull = NewCause("This computer's disk is full. The flasher needs room for the download and the unpacked image. Free up space, then try again.",
		anyOf("no space left on device", "not enough space on the disk", "there is not enough space", "disk full", "disk quota exceeded"))
	CauseLocked = NewCause("Another program is using the card (perhaps a second copy of the flasher, a backup or disk tool). Close it, then try again.",
		anyOf("resource temporarily unavailable", "device or resource busy", "resource busy", "being used by another process", "sharing violation", "lock violation"))
	CauseDeviceGone = NewCause("The card or the card reader stopped responding. It may have been pulled out or have a loose contact, or the card or the reader may be faulty. Connect it again, then try again.",
		func(s string) bool {
			return deviceGone(s) || onCard(s) && anyOf("no such file or directory", "cannot find the file")(s)
		})
	CauseWriteProtected = NewCause("The card refused to be written. Its lock switch (on the card or the adapter) may be on, or the card has worn out and turned itself read-only. Slide the switch the other way and try again, or use a different card.",
		anyOf("write protect", "write-protect", "read-only file system", "readonly file system"))
	CauseNameTooLong = NewCause("A file path is too long for this computer. Move the image to a folder with a short path (for example C:\\andr36oid or your Downloads folder), then choose it again.",
		anyOf("file name too long", "filename or extension is too long", "name too long", "path too long"))
	CausePermission = NewCause("The system did not let the flasher open a file or the card. Allow administrator access when you are asked. On a Mac, allow the flasher access to removable volumes in Privacy & Security; on Windows, security software (Controlled folder access or an antivirus) can block it. Then try again.",
		anyOf("permission denied", "access is denied", "operation not permitted", "access denied", "not authorized", "authorization failed"))
	CauseFileGone = NewCause("A file the flasher needs is no longer there: it may have been moved, renamed or deleted, or the drive it was on was removed. Choose it again.",
		anyOf("no such file or directory", "cannot find the file", "cannot find the path", "file does not exist", "the system cannot find"))
	CauseTLS = NewCause("The secure connection to the download server could not be checked. Make sure this computer's date and time are right; a proxy or a security program that inspects web traffic can also cause this. Then try again.",
		anyOf("x509:", "tls:", "certificate", "handshake failure"))
	CauseTimeout = NewCause("The connection timed out: the network is slow or unreliable, or the server is busy. Try again; a cable or a different network can help.",
		anyOf("timeout", "timed out", "deadline exceeded"))
	CauseOffline = NewCause("The flasher could not connect to the internet. Check that this computer is online and that no firewall, proxy or security program blocks the flasher, then try again.",
		anyOf("no such host", "server misbehaving", "network is unreachable", "no route to host", "connection refused", "connection reset", "forcibly closed", "dial tcp", "dial udp", "connectex", "wsarecv", "wsasend", "proxyconnect", "http: server closed", "stopped after 10 redirects"))
	CauseDamaged = NewCause("The image file is damaged or incomplete, most often because its download did not finish. Download it again, then choose it.",
		anyOf("unexpected eof", "damaged archive", "truncated archive", "gzip: invalid", "zip: not a valid", "flate: corrupt", "xz: ", "not an xz file", "gzip: checksum", "checksum error", "corrupt input"))
	CauseCanceled = NewCause("The operation was stopped before it finished.",
		anyOf("context canceled", "operation was canceled", "operation canceled"))
	CauseHelperStopped = NewCause("The part of the flasher that writes the card could not run, or it stopped. If you were asked for administrator access, allow it: the card cannot be written without it. Then try again.",
		anyOf("the helper stopped", "disk helper ended", "administrator access was denied"))
)

// findCause is the first cause in raw, ok false for none.
func findCause(raw string) (*Format, bool) {
	lower := strings.ToLower(raw)
	causesMu.Lock()
	list := append([]cause(nil), causes...)
	causesMu.Unlock()
	for _, c := range list {
		if c.match(lower) {
			return c.format, true
		}
	}
	return nil, false
}

// Detailed is msg (a message for the user, usually a Format's error) with detail,
// the error behind it, as its technical detail: its text is "msg (detail)", Find
// reads msg, and errors.Is and errors.As see both.
func Detailed(msg, detail error) error {
	if detail == nil {
		return msg
	}
	return &detailed{msg, detail}
}

type detailed struct{ msg, detail error }

func (d *detailed) Error() string   { return d.msg.Error() + " (" + d.detail.Error() + ")" }
func (d *detailed) Unwrap() []error { return []error{d.msg, d.detail} }
