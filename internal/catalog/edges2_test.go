package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// GitHub's limit says how long to wait when GitHub says it (a reset time or
// Retry-After), and "an hour" when it does not.
func TestRateLimitSaysHowLong(t *testing.T) {
	fixed := time.Unix(1_800_000_000, 0)
	old := now
	t.Cleanup(func() { now = old })
	now = func() time.Time { return fixed }
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/x/y/releases", nil)
	for name, c := range map[string]struct {
		status int
		header map[string]string
		want   string
	}{
		"reset in 10 min":     {403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(fixed.Add(9*time.Minute+30*time.Second).Unix(), 10)}, "about 10 minutes"},
		"retry-after":         {429, map[string]string{"Retry-After": "120"}, "about 2 minutes"},
		"reset passed":        {403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(fixed.Add(-time.Minute).Unix(), 10)}, "about 2 minutes"},
		"no time":             {429, nil, ErrRateLimited.Key()},
		"reset far away":      {403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(fixed.Add(48*time.Hour).Unix(), 10)}, ErrRateLimited.Key()},
		"forbidden, no limit": {403, nil, ErrRefused.Key()},
	} {
		h := http.Header{}
		for k, v := range c.header {
			h.Set(k, v)
		}
		e := statusError(response(req, c.status, h, nil))
		if !strings.Contains(e.Error(), c.want) {
			t.Errorf("%s: %v", name, e)
		}
	}
}

// A sign-in page or a proxy answering with a web page instead of the release list
// or the file says so, for the list, the game system manifest and a download.
func TestWebPageInsteadOfGitHub(t *testing.T) {
	fastRetries(t, time.Minute)
	page := []byte("<html><body>Please sign in to the hotel Wi-Fi</body></html>")
	withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Content-Type", "text/html; charset=utf-8")
		return response(r, 200, h, bytes.NewReader(page)), nil
	}))
	if _, e := List(context.Background()); !ErrPortal.Is(e) {
		t.Fatal("list:", e)
	}
	dir := t.TempDir()
	if _, e := downloadRelease(context.Background(), testAsset([]byte("release")), Repository, dir, nil); !ErrPortal.Is(e) {
		t.Fatal("download:", e)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatal("left behind:", left[0].Name())
	}
	// JSON that is not a release list stays the release-list message
	withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, nil, strings.NewReader(`{"message":"nope"}`)), nil
	}))
	if _, e := List(context.Background()); !ErrReleaseList.Is(e) {
		t.Fatal(e)
	}
}

// A download resumed after a broken connection sends If-Range with the file's
// ETag; a server whose file changed meanwhile sends the whole new file, which is
// then checked against the release's checksum: the result is never half old, half
// new. A file that changed and does not match is refused as damaged and deleted.
func TestResumeAfterTheFileChanged(t *testing.T) {
	fastRetries(t, time.Minute)
	old := bytes.Repeat([]byte("old release "), 200000)
	fresh := bytes.Repeat([]byte("new release "), 200000)
	for _, honours := range []bool{true, false} {
		t.Run(fmt.Sprint("server honours If-Range ", honours), func(t *testing.T) {
			var calls atomic.Int32
			var ifRange atomic.Value
			ifRange.Store("")
			withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
				n := calls.Add(1)
				h := http.Header{}
				if n == 1 {
					h.Set("ETag", `"old"`)
					return response(r, 200, h, &cutReader{bytes.NewReader(old[:len(old)/3])}), nil
				}
				ifRange.Store(r.Header.Get("If-Range"))
				h.Set("ETag", `"new"`)
				if start := rangeStart(r); start > 0 && (!honours || r.Header.Get("If-Range") == `"new"`) {
					h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(fresh)-1, len(fresh)))
					return response(r, http.StatusPartialContent, h, bytes.NewReader(fresh[start:])), nil
				}
				return response(r, 200, h, bytes.NewReader(fresh)), nil
			}))
			dir := t.TempDir()
			p, e := downloadRelease(context.Background(), testAsset(fresh), Repository, dir, nil)
			if ifRange.Load().(string) != `"old"` {
				t.Fatal("If-Range:", ifRange.Load())
			}
			if honours {
				got, _ := os.ReadFile(p)
				if e != nil || !bytes.Equal(got, fresh) {
					t.Fatal(e)
				}
				return
			}
			// the server glued the new file's end to the old file's start
			if !ErrDamagedDownload.Is(e) {
				t.Fatal(e)
			}
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Fatal("damaged download kept:", left[0].Name())
			}
		})
	}
}

// A disk that fills up while the download is saved says where and how much.
func TestDownloadDiskFullSaysWhereAndHowMuch(t *testing.T) {
	fastRetries(t, time.Minute)
	data := bytes.Repeat([]byte("x"), 3<<20)
	withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, nil, bytes.NewReader(data)), nil
	}))
	dir := t.TempDir()
	e := firmware.SpaceError(dir, 3<<20, fmt.Errorf("%w: %w", errWriteFile, errors.New("write "+dir+"/download-1: no space left on device")))
	if !firmware.ErrNoSpace.Is(e) || !strings.Contains(e.Error(), dir) || !strings.Contains(e.Error(), "GB needed") {
		t.Fatal(e)
	}
	// the check before the download says the same
	oldSpace := spaceFor
	t.Cleanup(func() { spaceFor = oldSpace })
	spaceFor = func(d string, n int64) error { return firmware.ErrNoSpace.New(d, firmware.SizeText(n), "0.0 GB") }
	if _, e := downloadRelease(context.Background(), testAsset(data), Repository, dir, nil); !firmware.ErrNoSpace.Is(e) || !strings.Contains(e.Error(), dir) {
		t.Fatal(e)
	}
	// an error that is not a full disk stays as it is
	other := errors.New("write: input/output error")
	if got := firmware.SpaceError(dir, 1, other); got != other {
		t.Fatal(got)
	}
}

// A wrong system clock makes the certificate check fail; the message says to
// check the date and time.
func TestClockWrongSaysDateAndTime(t *testing.T) {
	for _, raw := range []string{
		`Get "https://api.github.com/repos/andr36oid/releases/releases": tls: failed to verify certificate: x509: certificate has expired or is not yet valid: current time 2019-01-01T00:00:00Z is before 2026-01-01T00:00:00Z`,
		`x509: certificate signed by unknown authority`,
	} {
		key, _, ok := i18n.Find(errors.New(raw))
		if !ok || key != i18n.CauseTLS.Key() || !strings.Contains(key, "date and time") {
			t.Errorf("%q: %q", raw, key)
		}
	}
}
