package i18n

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// The bare errors of the systems, the network and the card reader that reach people
// are explained by a cause, by the text they cross the disk helper with.
func TestCausesExplainSystemErrors(t *testing.T) {
	for raw, want := range map[string]*Format{
		"write /dev/sdb: input/output error": CauseDeviceGone,
		"read \\\\.\\PhysicalDrive2: The request could not be performed because of an I/O device error.": CauseDeviceGone,
		"open /dev/rdisk4: no such device":                                                                                  CauseDeviceGone,
		"write /dev/rdisk4: device not configured":                                                                          CauseDeviceGone,
		"read \\\\.\\PhysicalDrive2: The device is not ready.":                                                              CauseDeviceGone,
		"read \\\\.\\PhysicalDrive2: Data error (cyclic redundancy check).":                                                 CauseDeviceGone,
		"The semaphore timeout period has expired.":                                                                         CauseDeviceGone,
		"write BOOT/hotfix/a.fix.zip: unable to allocate clusters for file: no space left on device":                        CauseBootFull,
		"write /dev/sdb: no space left on device":                                                                           CauseCardEnded,
		"read /dev/sdb at 31914983424: unexpected EOF":                                                                      CauseCardEnded,
		"write /home/u/.cache/andr36oid/image-1.img: no space left on device":                                               CauseDiskFull,
		"write C:\\Users\\u\\AppData\\Local\\x: There is not enough space on the disk.":                                     CauseDiskFull,
		"open /dev/sdb: device or resource busy":                                                                            CauseLocked,
		"resource temporarily unavailable":                                                                                  CauseLocked,
		"write /dev/sdb: read-only file system":                                                                             CauseWriteProtected,
		"The media is write protected.":                                                                                     CauseWriteProtected,
		"open /dev/sdb: permission denied":                                                                                  CausePermission,
		"open \\\\.\\PhysicalDrive2: Access is denied.":                                                                     CausePermission,
		"operation not permitted":                                                                                           CausePermission,
		"open C:\\x\\y.img.xz: The filename or extension is too long.":                                                      CauseNameTooLong,
		"open /a/b.img.xz: file name too long":                                                                              CauseNameTooLong,
		"open /dev/sdb: no such file or directory":                                                                          CauseDeviceGone,
		"open /home/u/Downloads/a.img.xz: no such file or directory":                                                        CauseFileGone,
		"open C:\\x.img.xz: The system cannot find the file specified.":                                                     CauseFileGone,
		"x509: certificate has expired or is not yet valid":                                                                 CauseTLS,
		"net/http: TLS handshake timeout":                                                                                   CauseTimeout,
		"context deadline exceeded (Client.Timeout exceeded while awaiting headers)":                                        CauseTimeout,
		"dial tcp 140.82.121.4:443: i/o timeout":                                                                            CauseTimeout,
		"dial tcp: lookup api.github.com: no such host":                                                                     CauseOffline,
		"dial tcp 140.82.121.4:443: connectex: No connection could be made because the target machine actively refused it.": CauseOffline,
		"read tcp 1.2.3.4:5->6.7.8.9:443: wsarecv: An existing connection was forcibly closed by the remote host.":          CauseOffline,
		"proxyconnect tcp: dial tcp 10.0.0.1:3128: connect: connection refused":                                             CauseOffline,
		"unexpected EOF":                       CauseDamaged,
		"damaged archive: xz: data is corrupt": CauseDamaged,
		"gzip: invalid header":                 CauseDamaged,
		"zip: not a valid zip file":            CauseDamaged,
		"flate: corrupt input before offset 5": CauseDamaged,
		"context canceled":                     CauseCanceled,
		"administrator access was denied or the helper stopped: exit status 126":    CauseHelperStopped,
		"disk helper ended without a result; recovery files, if present, are in /x": CauseHelperStopped,
	} {
		key, _, ok := FindText(raw)
		if !ok || key != want.Key() {
			t.Errorf("%q: explained as %q, want %q", raw, key, want.Key())
		}
	}
}

// A message of its own wins over any cause in the same text; a state message comes
// after it; an error without a message or a cause stays unexplained (the apps then
// show their general advice), and nothing panics on odd text.
func TestFindPrefersMessagesAndAddsStates(t *testing.T) {
	msg := NewFormat("Test message %s for find.")
	state := NewState("Test state for find.")
	e := fmt.Errorf("%w; %w", Detailed(msg.New("one"), errors.New("write /dev/sdb: input/output error")), state.New())
	if key, args, ok := Find(e); !ok || key != msg.Key() || fmt.Sprint(args) != "[one]" {
		t.Fatal(key, args)
	}
	if key, _, _ := FindState(e); key != state.Key() {
		t.Fatal(key)
	}
	got := Explain("en", e.Error())
	if !strings.HasPrefix(got, "Test message one for find.") || !strings.HasSuffix(got, "\n\nTest state for find.") {
		t.Fatal(got)
	}
	if ExplainError("en", e) != got {
		t.Fatal(ExplainError("en", e))
	}
	// only a cause and a state: the cause first
	e = fmt.Errorf("%w; %w", errors.New("write /dev/sdb: input/output error"), state.New())
	if key, _, _ := Find(e); key != CauseDeviceGone.Key() {
		t.Fatal(key)
	}
	// only a state
	if key, _, _ := Find(state.New()); key != state.Key() {
		t.Fatal(key)
	}
	for _, raw := range []string{"", "%", "%!s(MISSING)", "\x00\xff", "something only a developer knows"} {
		if _, _, ok := FindText(raw); ok {
			t.Errorf("%q explained", raw)
		}
	}
}

// Detailed keeps both errors for errors.Is and errors.As, and its text has both.
func TestDetailed(t *testing.T) {
	f := NewFormat("Test detailed.")
	e := Detailed(f.New(), io.ErrUnexpectedEOF)
	if !errors.Is(e, io.ErrUnexpectedEOF) || !f.Is(e) || e.Error() != "Test detailed. (unexpected EOF)" {
		t.Fatal(e)
	}
	var pe *os.PathError
	if errors.As(Detailed(f.New(), &os.PathError{Op: "open", Path: "x", Err: os.ErrNotExist}), &pe) == false {
		t.Fatal("PathError lost")
	}
	if Detailed(f.New(), nil).Error() != "Test detailed." {
		t.Fatal("nil detail")
	}
	if !errors.Is(Detailed(f.New(), context.Canceled), context.Canceled) {
		t.Fatal("cancel lost")
	}
}

// A translation whose formatting verbs are not the English ones prints the English
// text, never %!s(MISSING) or %!d(string=...).
func TestWrongVerbsInTranslationFallBack(t *testing.T) {
	old := catalogs["de"]
	t.Cleanup(func() { catalogs["de"] = old })
	m := map[string]string{}
	for k, v := range old {
		m[k] = v
	}
	m["%d device profiles"] = "%s Geräteprofile"
	m["Downloading %s…"] = "Lade %s %s herunter…"
	catalogs["de"] = m
	if got := Text("de", "%d device profiles", 38); got != "38 device profiles" {
		t.Fatal(got)
	}
	if got := Text("de", "Downloading %s…", "dArkOS"); strings.Contains(got, "%!") {
		t.Fatal(got)
	}
}
