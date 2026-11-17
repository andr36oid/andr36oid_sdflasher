package jobs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

// fakeHelper stands in for the disk helper: it leaves status (if any) as the
// helper's last word and ends with e.
func fakeHelper(t *testing.T, status *Result, e error) (acquired, released *int) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	a, r := 0, 0
	oldInhibit, oldRun, oldPath := inhibitSleep, runHelper, helperPath
	t.Cleanup(func() { inhibitSleep, runHelper, helperPath = oldInhibit, oldRun, oldPath })
	inhibitSleep = func(string) func() { a++; return func() { r++ } }
	helperPath = func(string) (string, error) { return "helper", nil }
	runHelper = func(exe, job string) error {
		if status != nil {
			b, _ := json.Marshal(status)
			os.WriteFile(filepath.Join(filepath.Dir(job), "status.json"), b, 0600)
		}
		return e
	}
	return &a, &r
}

// A helper that dies after it began to write: the message says the card is
// incomplete, with the hints; the computer may sleep again.
func TestHelperDiesMidWrite(t *testing.T) {
	for _, phase := range []flash.Progress{{Phase: "writing", Name: "Release image"}, {Phase: "preflight", Name: flash.SpeedCheck}, {Phase: "preflight", Name: flash.SizeCheck}} {
		a, r := fakeHelper(t, &Result{Progress: &phase}, errors.New("signal: killed"))
		_, e := Run(Request{Action: "write", Mode: "install"}, nil)
		if !flash.ErrStoppedMidWrite.Is(e) || flash.Classify(e) != flash.FailureDevice || !strings.Contains(e.Error(), "killed") {
			t.Fatalf("%v: %v", phase, e)
		}
		if *a != 1 || *r != 1 {
			t.Fatalf("sleep inhibited %d times, released %d", *a, *r)
		}
	}
	// ended without a word after writing
	fakeHelper(t, &Result{Progress: &flash.Progress{Phase: "verifying"}}, nil)
	if _, e := Run(Request{Action: "write", Mode: "update"}, nil); !flash.ErrStoppedMidWrite.Is(e) {
		t.Fatal(e)
	}
}

// Before the first write the card is as it was: no "incomplete".
func TestHelperDiesBeforeWriting(t *testing.T) {
	_, r := fakeHelper(t, &Result{Progress: &flash.Progress{Phase: "preflight", Name: "Checking source image"}}, errors.New("exit status 126"))
	_, e := Run(Request{Action: "write", Mode: "install"}, nil)
	if e == nil || flash.ErrStoppedMidWrite.Is(e) || !strings.Contains(e.Error(), "administrator access was denied") {
		t.Fatal(e)
	}
	if *r != 1 {
		t.Fatal("sleep not allowed again")
	}
}

// Sleep is held only while the card is changed, and let go on every way out.
func TestSleepInhibitedOnlyForWrites(t *testing.T) {
	a, r := fakeHelper(t, &Result{Done: true}, nil)
	if _, e := Run(Request{Action: "inspect"}, nil); e != nil || *a != 0 {
		t.Fatal(e, *a)
	}
	helperPath = func(string) (string, error) { return "", errors.New("disk helper missing") }
	for _, action := range []string{"write", "restore", "hotfix-apply", "kernel-apply", "dtb-apply", "bootloader-apply"} {
		*a, *r = 0, 0
		if _, e := Run(Request{Action: action, Mode: "update"}, nil); e == nil || *a != 1 || *r != 1 {
			t.Fatalf("%s: %v, inhibited %d, released %d", action, e, *a, *r)
		}
	}
}
