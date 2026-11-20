package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/flash"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// Stopping a write. The app asks by putting an empty file named "stop" into the
// job's folder, the folder the helper already reads its request from and writes
// its status to. That one channel works the same under every way the helper is
// started with administrator rights (pkexec on Linux, UAC on Windows, the
// administrator prompt on macOS): none of them passes the app's stdin or its
// signals on to the helper, and an unprivileged app may not signal an elevated
// process at all. The helper looks for the file a few times a second, says it
// took the request (Result.Stopping), finishes the block in progress, flushes the
// card, closes it and ends with Result.Stopped. A helper from before this never
// says Stopping; the app then gives up waiting after StopWait.

// stopFile is the name of the request in the job's folder.
const stopFile = "stop"

// StopWait is how long the app waits for the helper to take a stop request.
var StopWait = 5 * time.Second

// stopPoll is how often the helper looks for the request.
var stopPoll = 200 * time.Millisecond

// What a stopped write left the card like (Result.Stopped).
const (
	// StoppedUntouched: stopped before the first byte went to the card.
	StoppedUntouched = "untouched"
	// StoppedIncomplete: the card was partly written and has to be written again.
	StoppedIncomplete = "incomplete"
	// StoppedRestored: an update was stopped and the card was put back as it was.
	StoppedRestored = "restored"
)

// ErrStopped is the error of a write the user stopped; Result.Stopped says what
// the card is like.
var ErrStopped = errors.New("stopped by the user")

// Stop is the app's side of a stop request for RunStoppable.
type Stop struct {
	once sync.Once
	ch   chan struct{}
	// Answer is called once after Request: true when the helper took the request,
	// false when it did not answer within StopWait (a helper without stop) or the
	// request could not be put into the job's folder (Unsent says why).
	Answer func(ok bool)
	mu     sync.Mutex
	unsent error
}

func NewStop(answer func(ok bool)) *Stop {
	return &Stop{ch: make(chan struct{}), Answer: answer}
}

// Request asks the running write to stop; asking twice is asking once.
func (s *Stop) Request() { s.once.Do(func() { close(s.ch) }) }

// Unsent is why the request could not be put into the job's folder, nil when it
// was (the helper then only did not answer). It is set before Answer(false).
func (s *Stop) Unsent() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unsent
}

func (s *Stop) setUnsent(e error) {
	s.mu.Lock()
	s.unsent = e
	s.mu.Unlock()
}

// watchStop is the helper's side: it cancels the work when the request appears in
// dir and calls took once, first thing. It ends with ctx.
func watchStop(ctx context.Context, dir string, cancel context.CancelFunc, took func()) {
	t := time.NewTicker(stopPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, e := os.Lstat(filepath.Join(dir, stopFile)); e == nil {
				took()
				cancel()
				return
			}
		}
	}
}

// stopOutcome is what the helper does after the work ended while a stop was
// asked: the card is flushed and closed, and the result says what the card is
// like. ok is false when the work did not end because of the stop (it ended
// before, or failed on its own) or the card could not be put back.
func stopOutcome(e error, dev flash.Device, closeCard func()) (state string, ok bool) {
	if e == nil || !errors.Is(e, context.Canceled) || errors.Is(e, flash.ErrRestoreFailed) {
		return "", false
	}
	if dev != nil {
		dev.Sync()
	}
	if closeCard != nil {
		closeCard()
	}
	key, _, found := i18n.FindState(e)
	switch {
	case !found:
		return StoppedUntouched, true
	case key == flash.StateRestored.Key():
		return StoppedRestored, true
	case key == flash.StateUntouched.Key():
		return StoppedUntouched, true
	}
	return StoppedIncomplete, true
}

// writeStop puts the request into the job's folder (the tests make it fail).
var writeStop = func(path string) error { return os.WriteFile(path, nil, 0644) }
