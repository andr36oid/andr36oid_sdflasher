package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
	"github.com/andr36oid/andr36oid-sdflasher/internal/platform"
)

// A stop request that can't be put into the job's folder is answered at once,
// says why (not "an older helper"), and the write goes on to its end.
func TestStopUnsentSaysWhy(t *testing.T) {
	dev := &blockDevice{}
	var acked atomic.Int64
	stopHelper(t, dev, 30, false, true, &acked)
	old := writeStop
	t.Cleanup(func() { writeStop = old })
	writeStop = func(string) error { return errors.New("open stop: read-only file system") }
	answers := make(chan bool, 2)
	var stop *Stop
	stop = NewStop(func(ok bool) { answers <- ok })
	_, e := RunStoppable(Request{Action: "write", Mode: "install"}, func(p flash.Progress) {
		if p.Phase == "writing" && p.Done >= 2 {
			stop.Request()
		}
	}, stop)
	if e != nil || len(dev.written) != 30 {
		t.Fatal(e, len(dev.written))
	}
	if len(answers) != 1 || <-answers {
		t.Fatal("answer")
	}
	if u := stop.Unsent(); u == nil || !strings.Contains(u.Error(), "read-only") {
		t.Fatal(u)
	}
	// a helper that only does not answer leaves Unsent empty
	dev2 := &blockDevice{}
	stopHelper(t, dev2, 30, false, false, &acked)
	writeStop = old
	stop2 := NewStop(nil)
	RunStoppable(Request{Action: "write", Mode: "install"}, func(p flash.Progress) {
		if p.Phase == "writing" && p.Done >= 2 {
			stop2.Request()
		}
	}, stop2)
	if stop2.Unsent() != nil {
		t.Fatal(stop2.Unsent())
	}
}

// runFake stands in for the helper with run.
func runFake(t *testing.T, run func(exe, job string) error) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	oldInhibit, oldRun, oldPath := inhibitSleep, runHelper, helperPath
	t.Cleanup(func() { inhibitSleep, runHelper, helperPath = oldInhibit, oldRun, oldPath })
	inhibitSleep = func(string) func() { return func() {} }
	helperPath = func(string) (string, error) { return "helper", nil }
	runHelper = run
}

// A cancelled or refused administrator prompt says so, and that nothing was
// written to the card.
func TestElevationRefusedSaysNothingWritten(t *testing.T) {
	runFake(t, func(string, string) error {
		return fmt.Errorf("%w (exit status 126)", platform.ErrElevationRefused)
	})
	_, e := RunStoppable(Request{Action: "write", Mode: "install"}, nil, nil)
	if key, _, _ := i18n.Find(e); key != i18n.CauseElevationRefused.Key() {
		t.Fatal(key, e)
	}
	if !flash.StateUntouched.Is(e) {
		t.Fatal(e)
	}
	// also for a card job that writes (the helper never ran)
	_, e = RunStoppable(Request{Action: "hotfix-apply"}, nil, nil)
	if !flash.StateUntouched.Is(e) {
		t.Fatal(e)
	}
}

// A helper killed before its first word left the card as it was; one killed
// while writing left it incomplete.
func TestHelperKilled(t *testing.T) {
	runFake(t, func(string, string) error { return errors.New("signal: killed") })
	_, e := RunStoppable(Request{Action: "write", Mode: "install"}, nil, nil)
	if !flash.StateUntouched.Is(e) || !i18n.CauseHelperStopped.Is(e) && !strings.Contains(e.Error(), "helper stopped") {
		t.Fatal(e)
	}
	runFake(t, func(exe, job string) error {
		r := Result{Progress: &flash.Progress{Phase: "writing", Name: "System", Done: 1, Total: 10, Length: 1}}
		b, _ := json.Marshal(r)
		// the app reads the status once more when the helper has ended
		os.WriteFile(filepath.Join(filepath.Dir(job), "status.json"), b, 0600)
		return errors.New("signal: killed")
	})
	_, e = RunStoppable(Request{Action: "write", Mode: "install"}, nil, nil)
	if !flash.ErrStoppedMidWrite.Is(e) || flash.StateUntouched.Is(e) {
		t.Fatal(e)
	}
}

// A cache folder that can't be made or written says which folder.
func TestCacheDirUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("the cache sits beside the exe or in Library/Caches")
	}
	file := filepath.Join(t.TempDir(), "not-a-folder")
	os.WriteFile(file, nil, 0600)
	t.Setenv("XDG_CACHE_HOME", file)
	_, e := CacheDir()
	if !ErrCacheFolder.Is(e) || !strings.Contains(e.Error(), file) {
		t.Fatal(e)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, e = CacheDir(); e != nil {
		t.Fatal(e)
	}
}

// Long paths and paths with letters outside ASCII reach the helper unchanged.
func TestRequestPathsUnchanged(t *testing.T) {
	image := filepath.Join(t.TempDir(), "Bilder für die Konsole", strings.Repeat("sehr-lang-", 20), "andr36oid 日本語 ñ.img")
	var got Request
	runFake(t, func(exe, job string) error {
		b, _ := os.ReadFile(job)
		json.Unmarshal(b, &got)
		b, _ = json.Marshal(Result{Done: true})
		return os.WriteFile(filepath.Join(filepath.Dir(job), "status.json"), b, 0600)
	})
	if _, e := RunStoppable(Request{Action: "inspect", Image: image}, nil, nil); e != nil {
		t.Fatal(e)
	}
	if got.Image != image {
		t.Fatalf("%q", got.Image)
	}
}
