package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
)

// blockDevice is a card for the stop tests: it notes every block written, its
// flushes and whether it was closed.
type blockDevice struct {
	mu      sync.Mutex
	written []int64
	synced  int
	closed  bool
}

func (d *blockDevice) ReadAt(b []byte, off int64) (int, error) { return len(b), nil }
func (d *blockDevice) WriteAt(b []byte, off int64) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return 0, os.ErrClosed
	}
	d.written = append(d.written, off)
	return len(b), nil
}
func (d *blockDevice) Sync() error { d.mu.Lock(); d.synced++; d.mu.Unlock(); return nil }

// stopHelper stands in for a helper that knows the stop request: it writes blocks
// blocks one by one (then checks the card once more when check is set), looks
// for the request between blocks as the real helper does, and ends the way the
// real helper ends. knows false is a helper from before stop: it never looks.
func stopHelper(t *testing.T, dev *blockDevice, blocks int, check, knows bool, ackedAt *atomic.Int64) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	oldInhibit, oldRun, oldPath, oldWait, oldPoll := inhibitSleep, runHelper, helperPath, StopWait, stopPoll
	t.Cleanup(func() {
		inhibitSleep, runHelper, helperPath, StopWait, stopPoll = oldInhibit, oldRun, oldPath, oldWait, oldPoll
	})
	inhibitSleep = func(string) func() { return func() {} }
	helperPath = func(string) (string, error) { return "helper", nil }
	StopWait, stopPoll = 300*time.Millisecond, 10*time.Millisecond
	runHelper = func(exe, job string) error {
		dir := filepath.Dir(job)
		var emitting sync.Mutex
		var stopping atomic.Bool
		emit := func(r Result) {
			emitting.Lock()
			defer emitting.Unlock()
			r.Stopping = r.Stopping || stopping.Load()
			b, _ := json.Marshal(r)
			os.WriteFile(filepath.Join(dir, "status.json.tmp"), b, 0600)
			os.Rename(filepath.Join(dir, "status.json.tmp"), filepath.Join(dir, "status.json"))
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if knows {
			go watchStop(ctx, dir, cancel, func() {
				stopping.Store(true)
				dev.mu.Lock()
				ackedAt.Store(int64(len(dev.written)))
				dev.mu.Unlock()
				emit(Result{Stopping: true})
			})
		}
		var result Result
		closeCard := sync.OnceFunc(func() { dev.mu.Lock(); dev.closed = true; dev.mu.Unlock() })
		work := func() error {
			// before the card: the release is checked
			time.Sleep(50 * time.Millisecond)
			if e := ctx.Err(); e != nil {
				return e
			}
			for i := 0; i < blocks; i++ {
				if e := ctx.Err(); e != nil {
					return fmt.Errorf("%w; %w", e, flash.StateIncomplete.New())
				}
				dev.WriteAt(make([]byte, 8), int64(i))
				emit(Result{Progress: &flash.Progress{Phase: "writing", Name: "System", Done: int64(i + 1), Total: int64(blocks), Offset: 0, Length: int64(i + 1)}})
				time.Sleep(20 * time.Millisecond)
			}
			if check {
				emit(Result{Progress: &flash.Progress{Phase: "verifying", Name: "Checking the whole card once more"}})
				for i := 0; i < 20; i++ {
					if e := ctx.Err(); e != nil {
						return fmt.Errorf("%w; %w", e, flash.StateIncomplete.New())
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			return nil
		}
		e := work()
		if e != nil && stopping.Load() {
			if state, ok := stopOutcome(e, dev, closeCard); ok {
				result.Stopped, e = state, nil
			}
		}
		closeCard()
		if e != nil {
			result.Error = e.Error()
		}
		result.Done = true
		emit(result)
		return nil
	}
}

func TestStopBeforeStart(t *testing.T) {
	dev := &blockDevice{}
	var acked atomic.Int64
	stopHelper(t, dev, 10, false, true, &acked)
	var answers []bool
	stop := NewStop(func(ok bool) { answers = append(answers, ok) })
	stop.Request()
	r, e := RunStoppable(Request{Action: "write", Mode: "install"}, nil, stop)
	if e != ErrStopped || r.Stopped != StoppedUntouched || len(dev.written) != 0 {
		t.Fatal(e, r.Stopped, dev.written)
	}
	if len(answers) != 1 || !answers[0] || !dev.closed {
		t.Fatal(answers, dev.closed)
	}
}

func TestStopMidWrite(t *testing.T) {
	dev := &blockDevice{}
	var acked atomic.Int64
	stopHelper(t, dev, 40, true, true, &acked)
	var answers []bool
	stop := NewStop(func(ok bool) { answers = append(answers, ok) })
	r, e := RunStoppable(Request{Action: "write", Mode: "install"}, func(p flash.Progress) {
		if p.Phase == "writing" && p.Done >= 5 {
			stop.Request()
		}
	}, stop)
	if e != ErrStopped || r.Stopped != StoppedIncomplete {
		t.Fatal(e, r)
	}
	// after the helper took the request, at most the block in progress was written
	if n := int64(len(dev.written)); n >= 40 || n > acked.Load()+1 {
		t.Fatal("written after the stop:", n, "acked at", acked.Load())
	}
	if len(answers) != 1 || !answers[0] || !dev.closed || dev.synced == 0 {
		t.Fatal(answers, dev.closed, dev.synced)
	}
}

func TestStopDuringFinalCheck(t *testing.T) {
	dev := &blockDevice{}
	var acked atomic.Int64
	stopHelper(t, dev, 5, true, true, &acked)
	stop := NewStop(nil)
	r, e := RunStoppable(Request{Action: "write", Mode: "install"}, func(p flash.Progress) {
		if p.Phase == "verifying" {
			stop.Request()
		}
	}, stop)
	if e != ErrStopped || r.Stopped != StoppedIncomplete || len(dev.written) != 5 || !dev.closed {
		t.Fatal(e, r.Stopped, len(dev.written), dev.closed)
	}
}

// A helper from before stop never answers: the app is told after StopWait and
// the write goes on to its end.
func TestStopOldHelper(t *testing.T) {
	dev := &blockDevice{}
	var acked atomic.Int64
	stopHelper(t, dev, 40, false, false, &acked)
	answered := make(chan bool, 2)
	stop := NewStop(func(ok bool) { answered <- ok })
	started := time.Now()
	_, e := RunStoppable(Request{Action: "write", Mode: "install"}, func(p flash.Progress) {
		if p.Phase == "writing" && p.Done >= 2 {
			stop.Request()
		}
	}, stop)
	if e != nil || len(dev.written) != 40 {
		t.Fatal(e, len(dev.written))
	}
	select {
	case ok := <-answered:
		if ok {
			t.Fatal("old helper took the request")
		}
	default:
		t.Fatal("no answer after", time.Since(started))
	}
	if len(answered) != 0 {
		t.Fatal("answered twice")
	}
}

// What the card is like after a stop, from the error the write ended with.
func TestStopOutcome(t *testing.T) {
	dev := &blockDevice{}
	closed := 0
	for _, c := range []struct {
		e     error
		state string
		ok    bool
	}{
		{context.Canceled, StoppedUntouched, true},
		{fmt.Errorf("%w; %w", context.Canceled, flash.StateIncomplete.New()), StoppedIncomplete, true},
		{fmt.Errorf("%w; %w", context.Canceled, flash.StateRestored.New()), StoppedRestored, true},
		{fmt.Errorf("%w; %w: x; %w", context.Canceled, flash.ErrRestoreFailed, flash.StateRestoreFailed.New("/tmp/r")), "", false},
		{fmt.Errorf("input/output error"), "", false},
		{nil, "", false},
	} {
		state, ok := stopOutcome(c.e, dev, func() { closed++ })
		if state != c.state || ok != c.ok {
			t.Fatal(c.e, state, ok)
		}
	}
	if closed != 3 || dev.synced != 3 {
		t.Fatal("closed", closed, "synced", dev.synced)
	}
}
