package flash

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"
)

type syncCard struct {
	badCard
	err error
}

func (s *syncCard) Sync() error { return s.err }

// A flush the reader turns down is taken as done where the read-back reads the card
// (macOS, Windows); a real failure, and any failure on Linux, still ends the write.
func TestFlushRefusals(t *testing.T) {
	sync := func(errno syscall.Errno) error { return &os.PathError{Op: "sync", Path: "/dev/card", Err: errno} }
	for _, c := range []struct {
		goos string
		err  error
		ok   bool
	}{
		{"darwin", nil, true},
		{"darwin", sync(22), true},     // EINVAL
		{"darwin", sync(25), true},     // ENOTTY
		{"darwin", sync(45), true},     // ENOTSUP
		{"darwin", sync(102), true},    // EOPNOTSUPP
		{"darwin", sync(5), false},     // EIO
		{"darwin", sync(6), false},     // ENXIO
		{"darwin", sync(19), false},    // ENODEV
		{"darwin", sync(28), false},    // ENOSPC
		{"windows", sync(1), true},     // ERROR_INVALID_FUNCTION
		{"windows", sync(50), true},    // ERROR_NOT_SUPPORTED
		{"windows", sync(87), true},    // ERROR_INVALID_PARAMETER
		{"windows", sync(31), false},   // ERROR_GEN_FAILURE
		{"windows", sync(21), false},   // ERROR_NOT_READY
		{"windows", sync(23), false},   // ERROR_CRC
		{"windows", sync(1117), false}, // ERROR_IO_DEVICE
		{"windows", sync(1167), false}, // ERROR_DEVICE_NOT_CONNECTED
		{"linux", sync(22), false},     // the page cache: never skipped
		{"linux", sync(95), false},     // EOPNOTSUPP
		{"linux", sync(5), false},
		{"android", sync(22), false},
		{"darwin", errors.New("invalid argument"), false}, // no errno: not judged by its words
		{"windows", fmt.Errorf("wrapped: %w", sync(1)), true},
	} {
		e := flush(&syncCard{err: c.err}, c.goos)
		if (e == nil) != c.ok {
			t.Errorf("%s %v: got %v", c.goos, c.err, e)
		}
		if e != nil && e != c.err {
			t.Errorf("%s: error changed to %v", c.goos, e)
		}
	}
}

// A real flush failure reaches the apps as a device failure, which gets the hint to
// try another card reader or USB port.
func TestFlushFailureIsDeviceFailure(t *testing.T) {
	defer func(old string) { hostOS = old }(hostOS)
	hostOS = "darwin"
	eio := &os.PathError{Op: "sync", Path: "/dev/rdisk4", Err: syscall.Errno(5)}
	if e := (deviceErrors{&syncCard{err: eio}}).Sync(); Classify(e) != FailureDevice || !errors.Is(e, eio) {
		t.Fatalf("%v: %q", e, Classify(e))
	}
	refused := &os.PathError{Op: "sync", Path: "/dev/rdisk4", Err: syscall.Errno(25)}
	if e := (deviceErrors{&syncCard{err: refused}}).Sync(); e != nil {
		t.Fatal(e)
	}
}
