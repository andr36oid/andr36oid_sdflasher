package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/catalog"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// Stopped by the user: a new card that had begun to change is incomplete and says
// so; before that, or an update (restored), it is a plain stop; a failed restore of
// a stopped update stays the error.
func TestStopReason(t *testing.T) {
	cause := fmt.Errorf("x: %w", context.Canceled)
	for _, c := range []struct {
		cancelled, began bool
		mode             string
		e                error
		want             string
	}{
		{true, true, "install", cause, StoppedIncomplete},
		{true, false, "install", cause, Stopped},
		{true, true, "update", cause, Stopped},
		{false, true, "install", errors.New("input/output error"), "input/output error"},
		{true, true, "update", fmt.Errorf("%w; %w: boom", cause, flash.ErrRestoreFailed), "x: context canceled; automatic restore failed: boom"},
	} {
		if got := stopReason(c.cancelled, c.e, c.began, c.mode).Error(); !strings.HasPrefix(got, c.want) {
			t.Errorf("%+v: %q", c, got)
		}
	}
	if !strings.HasPrefix(StoppedIncomplete, Stopped) {
		t.Fatal("the app matches stops by the Stopped prefix")
	}
}

// Android hints never say "a different computer": a phone is not a computer.
func TestAndroidHintsNoComputer(t *testing.T) {
	for _, kind := range []flash.FailureKind{flash.FailureDevice, flash.FailureVerify, flash.FailureCapacity} {
		s := &Session{failure: kind}
		if h := s.LastFailureHints(); strings.Contains(h, "different computer") || !strings.Contains(h, "use a computer") {
			t.Errorf("%s: %q", kind, h)
		}
	}
	if strings.Contains(ReaderFailureHints(), "different computer") {
		t.Fatal(ReaderFailureHints())
	}
}

// Each reason a system can't be offered is its own state.
func TestGuestOfferStates(t *testing.T) {
	old := latestGuest
	t.Cleanup(func() { latestGuest = old })
	im := &firmware.Image{SHA256: "one", Facts: map[string]string{"layout": "prepared-1", "subsystem": "release-1"}, BuildIdentity: "x|eng.kenny.20261117.101500"}
	ready := &catalog.GuestRelease{Distribution: "dArkOSen", Darkosen: "11112026", Revision: 1}
	newer := &catalog.GuestRelease{Distribution: "dArkOSen", Darkosen: "11112026", Revision: 1, Minimum: "20991231"}
	odd := &catalog.GuestRelease{Distribution: "dArkOSen", Darkosen: "11112026", Revision: 1, Minimum: "soon"}
	for _, c := range []struct {
		g    *catalog.GuestRelease
		e    error
		want string
	}{
		{nil, nil, "none"},
		{nil, errors.New("GitHub returned 502"), "missing"},
		{nil, catalog.ErrGuestNeedsNewerFlasher, "flasher"},
		{newer, nil, "too_old"},
		{odd, nil, "flasher"},
		{ready, nil, "ready"},
	} {
		latestGuest = func(context.Context, catalog.GuestSource) (*catalog.GuestRelease, error) { return c.g, c.e }
		s := &Session{image: im}
		src, _ := catalog.FindGuestSource("dArkOSen")
		if o, _ := s.lookupGuest(im, src); o.State != c.want {
			t.Errorf("%v %v: %q, want %q", c.g, c.e, o.State, c.want)
		}
	}
}

// Freeing space removes downloads and unpacked files, never a recovery backup.
func TestClearCacheKeepsBackups(t *testing.T) {
	dir := t.TempDir()
	s, e := NewSession(dir)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "abc-andr36oid.img.xz"), make([]byte, 3000), 0600)
	os.WriteFile(filepath.Join(dir, "def-dArkOS.erofs.img"), make([]byte, 2000), 0600)
	backup := filepath.Join(dir, "operation-1", "recovery")
	os.MkdirAll(backup, 0700)
	os.WriteFile(filepath.Join(backup, "recovery.json"), make([]byte, 100), 0600)
	os.WriteFile(filepath.Join(dir, "operation-1", "operation.json"), make([]byte, 10), 0600)
	s.image = &firmware.Image{}
	var u map[string]int64
	raw, _ := s.CacheUsage()
	json.Unmarshal([]byte(raw), &u)
	if u["total"] != 5110 || u["free"] != 5000 || u["backups"] != 110 || u["backup_count"] != 1 {
		t.Fatal(u)
	}
	if e = s.ClearCache(); e != nil || s.image != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(backup, "recovery.json")); e != nil {
		t.Fatal("recovery backup removed")
	}
	for _, n := range []string{"abc-andr36oid.img.xz", "def-dArkOS.erofs.img"} {
		if _, e := os.Stat(filepath.Join(dir, n)); e == nil {
			t.Fatal(n, "kept")
		}
	}
}

// Every message the apps show translated is in en.json.
func TestMessagesAreTranslatable(t *testing.T) {
	for _, key := range i18n.Formats() {
		if !i18n.Has(key) {
			t.Errorf("%q is not in en.json", key)
		}
	}
	msg := flash.ErrFakeCapacity.New("31.9 GB", "8.6 GB").Error() + "\nRecovery: /x"
	if got := Explain("en", "write failed: "+msg); got != "This card is not the size it claims (it reports 31.9 GB, but only about 8.6 GB works). It cannot be used." {
		t.Fatal(got)
	}
	// a bare system error is explained by its cause (i18n.NewCause), never shown alone
	if got := Explain("en", "write /dev/block/sda: input/output error"); got != i18n.CauseDeviceGone.Key() {
		t.Fatal("a system error left unexplained:", got)
	}
	if Explain("de", "something only a developer knows") != "" {
		t.Fatal("an unknown error explained")
	}
	// what a failed write left the card like follows the message
	state := fmt.Errorf("%w; %w", errors.New("read /dev/sdb: input/output error"), flash.StateIncomplete.New()).Error()
	if got := Explain("en", state); got != i18n.CauseDeviceGone.Key()+"\n\n"+flash.StateIncomplete.Key() {
		t.Fatal(got)
	}
}
