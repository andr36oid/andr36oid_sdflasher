package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// The messages of download failures (the apps translate them; the HTTP status and
// the host follow as the technical detail).
var (
	// ErrRateLimited: GitHub answered 403 or 429 for too many requests.
	ErrRateLimited = i18n.NewFormat("GitHub is limiting how often this computer may ask it for releases. Wait an hour, or use a different network, then try again.")
	// ErrNotThere: 404 or 410, the file or the release is gone.
	ErrNotThere = i18n.NewFormat("The file is no longer on the download server. The release may have been replaced: open the release list again and choose it there.")
	// ErrServer: a 5xx answer.
	ErrServer = i18n.NewFormat("The download server has a problem right now. Try again in a few minutes.")
	// ErrRefused: any other answer than the file.
	ErrRefused = i18n.NewFormat("The download server did not hand out the file. A proxy, a firewall or a network that blocks GitHub can cause this. Try again, or use a different network.")
	// ErrStalled: no data came for stallTimeout, also after the download was resumed.
	ErrStalled = i18n.NewFormat("The download stopped: no data arrived for a minute. Check the internet connection, then try again. What was downloaded so far is not used.")
	// ErrCutOff: the connection closed before the whole file came, also after resuming.
	ErrCutOff = i18n.NewFormat("The download was cut off before it finished. Check the internet connection, then try again.")
	// ErrDamagedDownload: the file arrived whole but its checksum is not the release's.
	ErrDamagedDownload = i18n.NewFormat("The downloaded file is damaged: its checksum is not the one the release lists. Try again; if it keeps happening, a proxy or security program may be changing downloads.")
	// ErrNoDigest: the release lists no checksum for the file, so it can't be checked.
	ErrNoDigest = i18n.NewFormat("This release does not say what its file's checksum is, so the flasher cannot check the download. Download the image in a web browser and choose it with \"Choose image from disk\".")
	// ErrRateLimitedFor: ErrRateLimited when GitHub says when it answers again (the
	// minutes, rounded up).
	ErrRateLimitedFor = i18n.NewFormat("GitHub is limiting how often this computer may ask it for releases. Try again in about %s minutes, or use a different network.")
	// ErrPortal: a web page answered instead of GitHub: the sign-in page of a
	// hotel or public network, or a proxy.
	ErrPortal = i18n.NewFormat("A web page answered instead of GitHub. This network may need you to sign in first (as in a hotel, a school or on public Wi-Fi): open any web page in your browser, sign in, then try again. A proxy or a security program can also cause this.")
	// ErrReleaseList: GitHub's answer is not a release list.
	ErrReleaseList = i18n.NewFormat("The release list from GitHub could not be read. Try again later, or download the image in a web browser and choose it with \"Choose image from disk\".")
)

// statusError is the error of an HTTP answer that is not the file.
func statusError(resp *http.Response) error {
	host := ""
	if resp.Request != nil && resp.Request.URL != nil {
		host = " from " + resp.Request.URL.Host
	}
	detail := fmt.Errorf("HTTP %s%s", resp.Status, host)
	switch code := resp.StatusCode; {
	case code == http.StatusTooManyRequests || code == http.StatusForbidden && (resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""):
		if wait, ok := rateWait(resp.Header); ok {
			return i18n.Detailed(ErrRateLimitedFor.New(strconv.Itoa(int((wait+time.Minute-1)/time.Minute))), detail)
		}
		return i18n.Detailed(ErrRateLimited.New(), detail)
	case code == http.StatusNotFound || code == http.StatusGone:
		return i18n.Detailed(ErrNotThere.New(), detail)
	case code >= 500:
		return i18n.Detailed(ErrServer.New(), detail)
	}
	return i18n.Detailed(ErrRefused.New(), detail)
}

// now is the clock of rateWait (the tests set it).
var now = time.Now

// rateWait is how long GitHub says to wait: Retry-After (seconds) or the time in
// X-RateLimit-Reset (Unix seconds), at least two minutes; ok false when it says
// neither or a time more than a day away.
func rateWait(h http.Header) (time.Duration, bool) {
	var wait time.Duration
	if s, e := strconv.ParseInt(strings.TrimSpace(h.Get("Retry-After")), 10, 64); e == nil && s >= 0 {
		wait = time.Duration(s) * time.Second
	} else if r, e := strconv.ParseInt(strings.TrimSpace(h.Get("X-RateLimit-Reset")), 10, 64); e == nil && r > 0 {
		wait = time.Unix(r, 0).Sub(now())
	} else {
		return 0, false
	}
	if wait > 24*time.Hour {
		return 0, false
	}
	// at least two minutes: the message counts in minutes
	return max(wait, 2*time.Minute), true
}

// webPage: the answer is a web page (text/html), not the JSON or file asked for.
func webPage(resp *http.Response) error {
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return nil
	}
	host := ""
	if resp.Request != nil && resp.Request.URL != nil {
		host = " from " + resp.Request.URL.Host
	}
	return i18n.Detailed(ErrPortal.New(), fmt.Errorf("HTTP %s%s: %s", resp.Status, host, resp.Header.Get("Content-Type")))
}

// headerTimeout: a server that has not begun to answer by then is given up on. The
// downloads themselves have no time limit (a release takes long on a slow line); a
// stalled one is stopped by stallTimeout instead.
var headerTimeout = time.Minute

// lookupTimeout bounds a request for a release list or another small file.
var lookupTimeout = time.Minute

// stallTimeout: a download that receives nothing for this long is stopped (and
// resumed, up to downloadTries times).
var stallTimeout = time.Minute

// downloadTries: how often one download is started or resumed before it fails.
const downloadTries = 4

// retryPause is the wait before a download is resumed (the tests shorten it).
var retryPause = 2 * time.Second

// client is the HTTP client of all requests: the system's proxy settings (the
// environment's), and no total time limit (headerTimeout, stallTimeout).
func client() *http.Client {
	rt := http.DefaultTransport
	if t, ok := rt.(*http.Transport); ok {
		t = t.Clone()
		t.ResponseHeaderTimeout = headerTimeout
		rt = t
	}
	return &http.Client{Transport: rt}
}

// errStallTimer: the stall watchdog stopped the request.
var errStallTimer = errors.New("no data received")

// watched is resp's body, which cancel stops when nothing arrives for stallTimeout;
// stalled then says so.
type watched struct {
	body    io.Reader
	timer   *time.Timer
	stalled *atomic.Bool
}

func (w watched) Read(b []byte) (int, error) {
	n, e := w.body.Read(b)
	if n > 0 {
		w.timer.Reset(stallTimeout)
	}
	if e != nil && e != io.EOF && w.stalled.Load() {
		e = fmt.Errorf("%w: %w", errStallTimer, e)
	}
	return n, e
}

// fetchRange asks for url from offset on (the whole file for 0). resumed is true
// when the server sends the rest (206); for false the body is the whole file again.
// validator is the file's ETag or Last-Modified from the first answer: on a resume
// it goes as If-Range, so a server whose file changed since sends the whole new
// file instead of the rest of another one. stop cancels the request and its watchdog.
func fetchRange(ctx context.Context, url string, offset int64, validator string) (body io.Reader, resumed bool, valid string, stop func(), err error) {
	ctx, cancel := context.WithCancel(ctx)
	stalled := &atomic.Bool{}
	timer := time.AfterFunc(stallTimeout, func() { stalled.Store(true); cancel() })
	end := func() { timer.Stop(); cancel() }
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		end()
		return nil, false, "", nil, e
	}
	req.Header.Set("User-Agent", "andr36oid-sdflasher")
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
		if validator != "" {
			req.Header.Set("If-Range", validator)
		}
	}
	resp, e := client().Do(req)
	if e != nil {
		end()
		if stalled.Load() {
			e = fmt.Errorf("%w: %w", errStallTimer, e)
		}
		return nil, false, "", nil, e
	}
	close := func() { end(); resp.Body.Close() }
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusPartialContent && offset > 0 && strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes "+strconv.FormatInt(offset, 10)+"-"):
		resumed = true
	default:
		close()
		return nil, false, "", nil, statusError(resp)
	}
	if e := webPage(resp); e != nil {
		close()
		return nil, false, "", nil, e
	}
	valid = validator
	if !resumed {
		// a strong ETag, else the date; a weak ETag can't go into If-Range
		valid = ""
		if tag := resp.Header.Get("ETag"); tag != "" && !strings.HasPrefix(tag, "W/") {
			valid = tag
		} else if date := resp.Header.Get("Last-Modified"); date != "" {
			valid = date
		}
	}
	return watched{resp.Body, timer, stalled}, resumed, valid, close, nil
}

// retryable: a download that failed with e is worth resuming: the connection broke
// or stalled. Not when the user stopped it, nor for the server's or the disk's errors.
func retryable(ctx context.Context, e error) bool {
	if ctx.Err() != nil || e == nil {
		return false
	}
	var m *i18n.Error
	return !errors.As(e, &m)
}

// pause waits retryPause, false when ctx ends first.
func pause(ctx context.Context) bool {
	t := time.NewTimer(retryPause)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
