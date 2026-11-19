package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

// fastRetries makes the resume pauses and the stall watchdog short for a test.
func fastRetries(t *testing.T, stall time.Duration) {
	oldPause, oldStall := retryPause, stallTimeout
	retryPause, stallTimeout = time.Millisecond, stall
	t.Cleanup(func() { retryPause, stallTimeout = oldPause, oldStall })
}

func testAsset(data []byte) Asset {
	sum := sha256.Sum256(data)
	return Asset{Name: "andr36oid-20261119-0217-5.10-vulkan.img.xz", Size: int64(len(data)),
		URL:    "https://github.com/" + Repository + "/releases/download/v1/andr36oid-20261119-0217-5.10-vulkan.img.xz",
		Digest: "sha256:" + hex.EncodeToString(sum[:])}
}

// response is an answer of the fake server.
func response(r *http.Request, status int, header http.Header, body io.Reader) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Status: strconv.Itoa(status) + " " + http.StatusText(status), Header: header, Body: io.NopCloser(body), Request: r}
}

// rangeStart is where a request's Range header starts, 0 for none.
func rangeStart(r *http.Request) int64 {
	v := strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-")
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// cutReader gives data and then fails as a broken connection.
type cutReader struct {
	r io.Reader
}

func (c *cutReader) Read(b []byte) (int, error) {
	n, e := c.r.Read(b)
	if e == io.EOF {
		return n, errors.New("read tcp 10.0.0.2:51234->140.82.121.4:443: read: connection reset by peer")
	}
	return n, e
}

// Downloads that break off are resumed where they stopped, by a server that can
// (206) and one that can't (200, the whole file again); what arrives is checked.
func TestDownloadResumes(t *testing.T) {
	fastRetries(t, time.Minute)
	data := bytes.Repeat([]byte("andr36oid release "), 300000)
	for _, ranges := range []bool{true, false} {
		t.Run(fmt.Sprint("ranges ", ranges), func(t *testing.T) {
			var calls atomic.Int32
			withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
				n := calls.Add(1)
				start := rangeStart(r)
				if !ranges {
					start = 0
				}
				rest := data[start:]
				status, h := http.StatusOK, http.Header{}
				if start > 0 {
					status = http.StatusPartialContent
					h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
				}
				// the first two connections break a third of the way in
				if n <= 2 {
					return response(r, status, h, &cutReader{bytes.NewReader(rest[:len(rest)/3])}), nil
				}
				return response(r, status, h, bytes.NewReader(rest)), nil
			}))
			p, e := downloadRelease(context.Background(), testAsset(data), Repository, t.TempDir(), nil)
			if e != nil {
				t.Fatal(e)
			}
			got, _ := os.ReadFile(p)
			if !bytes.Equal(got, data) || calls.Load() != 3 {
				t.Fatal("resumed download differs, calls:", calls.Load())
			}
		})
	}
}

// Every way a download can fail ends in a message for the user that says what
// happened and what to do, with the technical detail behind it; nothing is left.
func TestDownloadFailuresExplainThemselves(t *testing.T) {
	fastRetries(t, 150*time.Millisecond)
	data := bytes.Repeat([]byte("x"), 1<<20)
	limited := http.Header{"X-Ratelimit-Remaining": {"0"}}
	for _, c := range []struct {
		name string
		rt   func(*http.Request) (*http.Response, error)
		want *i18n.Format
	}{
		{"rate limit 403", func(r *http.Request) (*http.Response, error) {
			return response(r, 403, limited, strings.NewReader("API rate limit exceeded")), nil
		}, ErrRateLimited},
		{"rate limit 429", func(r *http.Request) (*http.Response, error) {
			return response(r, 429, nil, strings.NewReader("")), nil
		}, ErrRateLimited},
		{"forbidden by a proxy", func(r *http.Request) (*http.Response, error) {
			return response(r, 403, nil, strings.NewReader("blocked")), nil
		}, ErrRefused},
		{"404", func(r *http.Request) (*http.Response, error) {
			return response(r, 404, nil, strings.NewReader("Not Found")), nil
		}, ErrNotThere},
		{"503", func(r *http.Request) (*http.Response, error) {
			return response(r, 503, nil, strings.NewReader("")), nil
		}, ErrServer},
		{"no network", func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: lookup github.com: no such host")
		}, i18n.CauseOffline},
		{"TLS inspected by a proxy", func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("tls: failed to verify certificate: x509: certificate signed by unknown authority")
		}, i18n.CauseTLS},
		{"clock wrong", func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("tls: failed to verify certificate: x509: certificate has expired or is not yet valid")
		}, i18n.CauseTLS},
		{"truncated every time", func(r *http.Request) (*http.Response, error) {
			return response(r, 200, nil, bytes.NewReader(data[:len(data)/2])), nil
		}, ErrCutOff},
		{"wrong bytes", func(r *http.Request) (*http.Response, error) {
			return response(r, 200, nil, bytes.NewReader(bytes.Repeat([]byte("y"), len(data)))), nil
		}, ErrDamagedDownload},
		{"too many bytes", func(r *http.Request) (*http.Response, error) {
			return response(r, 200, nil, bytes.NewReader(append(append([]byte{}, data...), 'z'))), nil
		}, ErrDamagedDownload},
		{"stalled", func(r *http.Request) (*http.Response, error) {
			pr, pw := io.Pipe()
			go func() {
				pw.Write(data[:1000])
				<-r.Context().Done()
				pw.CloseWithError(r.Context().Err())
			}()
			return response(r, 200, nil, pr), nil
		}, ErrStalled},
	} {
		t.Run(c.name, func(t *testing.T) {
			withTransport(t, funcTransport(c.rt))
			dir := t.TempDir()
			start := time.Now()
			_, e := downloadRelease(context.Background(), testAsset(data), Repository, dir, nil)
			if e == nil {
				t.Fatal("no error")
			}
			if time.Since(start) > 20*time.Second {
				t.Fatal("took", time.Since(start))
			}
			if left, _ := os.ReadDir(dir); len(left) != 0 {
				t.Fatal("left", left[0].Name())
			}
			key, _, ok := i18n.Find(e)
			if !ok || key != c.want.Key() {
				t.Fatalf("%v\nexplained as %q", e, key)
			}
		})
	}
}

// A stop by the user during a download is a stop, not a failure to explain or retry.
func TestDownloadCancelIsNotRetried(t *testing.T) {
	fastRetries(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	var once sync.Once
	withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		pr, pw := io.Pipe()
		go func() {
			pw.Write([]byte("abc"))
			once.Do(cancel)
			<-r.Context().Done()
			pw.CloseWithError(r.Context().Err())
		}()
		return response(r, 200, nil, pr), nil
	}))
	_, e := downloadRelease(ctx, testAsset(bytes.Repeat([]byte("q"), 1<<20)), Repository, t.TempDir(), nil)
	if !errors.Is(e, context.Canceled) || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
}

// GitHub's release list with an unexpected shape, an error status or no assets: an
// explained error or an empty list, never a panic.
func TestReleaseListAnswers(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   *i18n.Format
		empty  bool
	}{
		{"object instead of list", 200, `{"message":"Moved Permanently"}`, ErrReleaseList, false},
		{"html", 200, `<html>captive portal</html>`, ErrReleaseList, false},
		{"truncated", 200, `[{"name":"x","assets":[`, ErrReleaseList, false},
		{"rate limited", 403, `{"message":"API rate limit exceeded"}`, nil, false},
		{"no assets", 200, `[{"name":"x","tag_name":"v1","published_at":"2026-11-19T00:00:00Z"}]`, nil, true},
		{"assets of another shape", 200, `[{"name":"x","published_at":"2026-11-19T00:00:00Z","assets":[{"name":5}]}]`, ErrReleaseList, false},
		{"null", 200, `null`, nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			if c.status == 403 {
				h.Set("X-RateLimit-Remaining", "0")
			}
			withTransport(t, funcTransport(func(r *http.Request) (*http.Response, error) {
				return response(r, c.status, h, strings.NewReader(c.body)), nil
			}))
			list, e := List(context.Background())
			if c.empty {
				if e != nil || len(list) != 0 {
					t.Fatal(list, e)
				}
				return
			}
			want := c.want
			if want == nil {
				want = ErrRateLimited
			}
			if key, _, _ := i18n.Find(e); key != want.Key() {
				t.Fatalf("%v explained as %q", e, key)
			}
		})
	}
}

// A release without a checksum can't be downloaded by the flasher; it says so and how
// to go on.
func TestDownloadNeedsDigest(t *testing.T) {
	a := testAsset([]byte("abc"))
	a.Digest = ""
	_, e := downloadRelease(context.Background(), a, Repository, t.TempDir(), nil)
	if !ErrNoDigest.Is(e) {
		t.Fatal(e)
	}
}
