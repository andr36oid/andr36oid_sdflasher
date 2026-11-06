package mobile

import (
	"errors"
	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDisk struct {
	size                   int64
	reads, writes, flushes int
	short                  bool
	err                    error
	file                   *os.File
}

func (d *fakeDisk) Capacity() int64  { return d.size }
func (d *fakeDisk) Identity() string { return "test-reader" }
func (d *fakeDisk) Read(off, n int64) ([]byte, error) {
	d.reads++
	if d.err != nil {
		return nil, d.err
	}
	if d.short {
		n--
	}
	b := make([]byte, n)
	if d.file != nil {
		_, e := d.file.ReadAt(b, off)
		return b, e
	}
	return b, nil
}
func (d *fakeDisk) Write(off int64, b []byte) error { d.writes++; return d.err }
func (d *fakeDisk) Flush() error                    { d.flushes++; return d.err }
func TestUSBBoundsAndShortReads(t *testing.T) {
	disk := &fakeDisk{size: 1 << 30}
	dev, e := adapt(disk)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		off int64
		n   int
	}{{-512, 512}, {1, 512}, {0, 513}, {1 << 30, 512}, {(1 << 30) - 512, 1024}} {
		if _, e = dev.ReadAt(make([]byte, tc.n), tc.off); e == nil {
			t.Fatal("invalid read accepted")
		}
		if _, e = dev.WriteAt(make([]byte, tc.n), tc.off); e == nil {
			t.Fatal("invalid write accepted")
		}
	}
	if disk.reads != 0 || disk.writes != 0 {
		t.Fatal("invalid range reached device")
	}
	disk.short = true
	if _, e = dev.ReadAt(make([]byte, 512), 0); !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatalf("short USB read: %v", e)
	}
	disk.err = errors.New("disconnected")
	if _, e = dev.WriteAt(make([]byte, 512), 0); !errors.Is(e, disk.err) {
		t.Fatal("lost write error")
	}
	if e = dev.Sync(); !errors.Is(e, disk.err) {
		t.Fatal("lost flush error")
	}
}
func TestFailedPreflightNeverWrites(t *testing.T) {
	session, e := NewSession(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	disk := &fakeDisk{size: 1 << 30}
	if _, e = session.Write(disk, `{}`, nil); e == nil {
		t.Fatal("missing source accepted")
	}
	session.image = &firmware.Image{}
	if _, e = session.Write(disk, `{"identity":"different-reader"}`, nil); e == nil {
		t.Fatal("changed reader accepted")
	}
	if _, e = session.Write(disk, `{"identity":"test-reader","fingerprint":"stale"}`, nil); e == nil {
		t.Fatal("changed card accepted")
	}
	if e = session.Restore(disk, "/outside-private-storage", nil); e == nil {
		t.Fatal("outside recovery accepted")
	}
	if disk.writes != 0 {
		t.Fatal("preflight error wrote the card")
	}
}
func TestReleasedImageThroughUSB(t *testing.T) {
	path := os.Getenv("SDFLASHER_TEST_IMAGE")
	if path == "" {
		t.Skip("set SDFLASHER_TEST_IMAGE for a real image")
	}
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	st, _ := f.Stat()
	disk := &fakeDisk{size: st.Size(), file: f}
	dev, e := adapt(disk)
	if e != nil {
		t.Fatal(e)
	}
	direct, e := flash.InspectCard(f, st.Size())
	if e != nil {
		t.Fatal(e)
	}
	viaUSB, e := flash.InspectCard(dev, st.Size())
	if e != nil {
		t.Fatal(e)
	}
	if direct.Fingerprint != viaUSB.Fingerprint || direct.Profile != viaUSB.Profile {
		t.Fatal("USB inspection differs from file inspection")
	}
	if disk.writes != 0 {
		t.Fatal("inspection wrote the device")
	}
}

func TestRecoveryRejectsAnotherCardInSameReader(t *testing.T) {
	session, e := NewSession(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	disk := &fakeDisk{size: 1 << 30}
	dir, e := os.MkdirTemp(session.dir, "operation-")
	if e != nil {
		t.Fatal(e)
	}
	rec := record{Identity: disk.Identity(), Size: disk.size, Anchors: []anchor{{Offset: 1 << 20, Length: 512, SHA256: "a different card"}}}
	raw, _ := encode(rec)
	if e = os.WriteFile(filepath.Join(dir, "operation.json"), []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	e = session.Restore(disk, dir, nil)
	if e == nil || !strings.Contains(e.Error(), "preserved partitions") {
		t.Fatalf("wrong card check: %v", e)
	}
	if disk.writes != 0 {
		t.Fatal("recovery wrote a different card")
	}
}

func TestCancelReachesOnlyTheArmedWrite(t *testing.T) {
	s := &Session{}
	s.Cancel() // nothing armed: must be a no-op
	ctx, done := s.writeContext()
	if ctx.Err() != nil {
		t.Fatal("fresh write context already cancelled")
	}
	s.Cancel()
	if ctx.Err() == nil {
		t.Fatal("Cancel did not stop the armed write")
	}
	done()
	if s.cancel != nil {
		t.Fatal("write context stayed armed after the write ended")
	}
	s.Cancel() // disarmed again: no-op
	if !strings.HasPrefix(Stopped+"\nRecovery: /x", Stopped) {
		t.Fatal("app matches the stopped error by prefix")
	}
}
