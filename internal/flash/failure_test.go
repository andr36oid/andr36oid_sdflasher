package flash

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Which failures get which hints, on the desktop and on Android.
func TestFailureHints(t *testing.T) {
	// a phone is not a computer: no "different computer" on Android, but that some
	// phones can't do this at all and a computer must be used
	common := []string{"If it keeps failing:", "Try a different card reader."}
	computer := "Try a different computer."
	android := "Some Android phones and tablets cannot write SD cards this way at all. If so, use a computer."
	faulty := "The card may be faulty or not its stated size."
	desktop := append(append([]string{}, common...), computer)
	phone := append(append([]string{}, common...), android)
	for _, c := range []struct {
		kind    FailureKind
		android bool
		want    []string
	}{
		{FailureOther, false, nil},
		{FailureOther, true, nil},
		{FailureDevice, false, desktop},
		{FailureDevice, true, phone},
		{FailureVerify, false, append([]string{faulty}, desktop...)},
		{FailureVerify, true, append([]string{faulty}, phone...)},
		{FailureCapacity, false, desktop},
		{FailureCapacity, true, phone},
	} {
		if got := FailureHints(c.kind, c.android); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%q android=%v: %q", c.kind, c.android, got)
		}
	}
}

func TestFailureKinds(t *testing.T) {
	device := DeviceFailure(errors.New("input/output error"))
	verify := NewFailure(FailureVerify, errors.New("read-back mismatch at byte 0"))
	for _, c := range []struct {
		e    error
		want FailureKind
	}{
		{nil, FailureOther},
		{context.Canceled, FailureOther},
		{errors.New("download checksum mismatch"), FailureOther},
		{errors.New("source image changed after inspection"), FailureOther},
		{device, FailureDevice},
		{fmt.Errorf("card read check failed: %w", device), FailureDevice},
		{verify, FailureVerify},
		// the cause wins when an update's restore wraps it
		{fmt.Errorf("%w; the previous installation was restored and verified", verify), FailureVerify},
		{DeviceFailure(fmt.Errorf("x: %w", verify)), FailureVerify},
		// what came back from the disk helper
		{NewFailure(FailureDevice, errors.New("write /dev/sdb: input/output error")), FailureDevice},
		{NewFailure(FailureOther, errors.New("x")), FailureOther},
	} {
		if got := Classify(c.e); got != c.want {
			t.Errorf("%v: %q, want %q", c.e, got, c.want)
		}
	}
	if device.Error() != "input/output error" || NewFailure(FailureDevice, nil) != nil {
		t.Fatal("message or nil changed")
	}
}

type badCard struct {
	data      []byte
	failWrite error
	short     bool
}

func (b *badCard) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(b.data)) {
		return 0, io.EOF
	}
	return copy(p, b.data[off:]), nil
}
func (b *badCard) WriteAt(p []byte, off int64) (int, error) {
	if b.failWrite != nil {
		return 0, b.failWrite
	}
	if b.short {
		return len(p) - 1, nil
	}
	return copy(b.data[off:], p), nil
}
func (b *badCard) Sync() error { return nil }

// The card's own errors become device failures; reading back other bytes is a verify
// failure; io.EOF stays io.EOF.
func TestCardErrorsAreClassified(t *testing.T) {
	src := bytes.NewReader(bytes.Repeat([]byte{1}, 4096))
	for _, c := range []struct {
		card *badCard
		want FailureKind
	}{
		{&badCard{data: make([]byte, 4096), failWrite: errors.New("no such device")}, FailureDevice},
		{&badCard{data: make([]byte, 4096), short: true}, FailureDevice},
	} {
		e := copyRange(context.Background(), deviceErrors{c.card}, src, 0, 0, 4096, nil)
		if Classify(e) != c.want {
			t.Errorf("%v: %q", e, Classify(e))
		}
	}
	card := &badCard{data: make([]byte, 4096)}
	e := verify(context.Background(), deviceErrors{card}, src, 0, 0, 4096, nil)
	if Classify(e) != FailureVerify || !strings.Contains(e.Error(), "mismatch") {
		t.Fatal(e)
	}
	if _, e = (deviceErrors{card}).ReadAt(make([]byte, 1), 8192); e != io.EOF {
		t.Fatal("EOF wrapped", e)
	}
}
