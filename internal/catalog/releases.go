package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andr36oid/andr36oid-sdflasher/internal/firmware"
	"github.com/andr36oid/andr36oid-sdflasher/internal/i18n"
)

const Repository = "andr36oid/releases"

type Asset struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	URL     string `json:"browser_download_url"`
	Digest  string `json:"digest"`
	Release string `json:"-"`
}
type release struct {
	Name      string    `json:"name"`
	Tag       string    `json:"tag_name"`
	Draft     bool      `json:"draft"`
	Pre       bool      `json:"prerelease"`
	Published time.Time `json:"published_at"`
	Assets    []Asset   `json:"assets"`
}

// releaseList is GitHub's list of releases for req, within lookupTimeout.
func releaseList(req *http.Request) ([]release, error) {
	ctx, cancel := context.WithTimeout(req.Context(), lookupTimeout)
	defer cancel()
	resp, e := client().Do(req.WithContext(ctx))
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, statusError(resp)
	}
	if e := webPage(resp); e != nil {
		return nil, e
	}
	var rel []release
	if e = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rel); e != nil {
		if ctx.Err() != nil {
			return nil, e
		}
		return nil, i18n.Detailed(ErrReleaseList.New(), e)
	}
	return rel, nil
}

func List(ctx context.Context) ([]Asset, error) {
	var out []Asset
	for page := 1; page <= 10; page++ {
		req, e := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100&page=%d", Repository, page), nil)
		if e != nil {
			return nil, e
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "andr36oid-sdflasher")
		rel, e := releaseList(req)
		if e != nil {
			return nil, e
		}
		out = append(out, usable(rel)...)
		if len(rel) < 100 {
			break
		}
	}
	return out, nil
}

// usable: the image files of a page of releases that this flasher can write
func usable(rel []release) []Asset {
	var out []Asset
	for _, r := range rel {
		// builds older than firmware.OldestBuild don't work with this flasher
		if r.Draft || r.Published.Before(firmware.OldestBuild) {
			continue
		}
		names := map[string]bool{}
		for _, a := range r.Assets {
			names[strings.ToLower(a.Name)] = true
		}
		for _, a := range r.Assets {
			n := strings.ToLower(a.Name)
			if _, old := firmware.TooOld(a.Name); old {
				continue
			}
			// a release that offers the image both ways lists only the smaller .img.xz
			if strings.HasSuffix(n, ".img.zip") && names[strings.TrimSuffix(n, ".zip")+".xz"] {
				continue
			}
			// one image file per build, in whichever packing; of several the smaller .img.xz
			if strings.HasSuffix(n, ".img.gz") && (names[strings.TrimSuffix(n, ".gz")+".xz"] || names[strings.TrimSuffix(n, ".gz")+".zip"]) {
				continue
			}
			if strings.HasSuffix(n, ".img.xz") || strings.HasSuffix(n, ".img.zip") || strings.HasSuffix(n, ".img.gz") || strings.HasSuffix(n, ".img") {
				a.Release = r.Name
				if a.Release == "" {
					a.Release = r.Tag
				}
				if r.Pre {
					a.Release += " (pre-release)"
				}
				out = append(out, a)
			}
		}
	}
	return out
}

// spaceFor is firmware.CheckSpace (the tests replace it).
var spaceFor = firmware.CheckSpace

func Download(ctx context.Context, a Asset, dir string, progress func(int64, int64)) (string, error) {
	return downloadRelease(ctx, a, Repository, dir, progress)
}

func validReleaseURL(raw, repository string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && strings.HasPrefix(u.Path, "/"+repository+"/releases/download/")
}

func downloadRelease(ctx context.Context, a Asset, repository, dir string, progress func(int64, int64)) (string, error) {
	if !validReleaseURL(a.URL, repository) {
		return "", errors.New("invalid release download URL")
	}
	if a.Size <= 0 || a.Size > 32<<30 {
		return "", errors.New("invalid release size")
	}
	digest := strings.TrimPrefix(a.Digest, "sha256:")
	sum, err := hex.DecodeString(digest)
	if err != nil || len(sum) != sha256.Size {
		return "", i18n.Detailed(ErrNoDigest.New(), errors.New("no SHA-256 digest for "+a.Name))
	}
	digest = strings.ToLower(digest)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	final := filepath.Join(dir, digest+"-"+filepath.Base(a.Name))
	// the cache is named by the digest: a complete file there with that checksum is
	// this asset, so it isn't downloaded again (a damaged one is downloaded over)
	if cached, e := os.Open(final); e == nil {
		st, se := cached.Stat()
		var have string
		if se == nil && st.Size() == a.Size {
			have, _ = firmware.Hash(ctx, cached, a.Size, nil)
		}
		cached.Close()
		if have == digest {
			if progress != nil {
				progress(a.Size, a.Size)
			}
			return final, nil
		}
	}
	if e := spaceFor(dir, a.Size); e != nil {
		return "", e
	}
	f, e := os.CreateTemp(dir, "download-*")
	if e != nil {
		return "", e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	h := sha256.New()
	var n int64
	// a broken or stalled connection is resumed where it stopped (or started again
	// when the server can't resume), up to downloadTries times
	var last error
	validator := ""
	for try := 0; try < downloadTries && n < a.Size; try++ {
		if try > 0 && !pause(ctx) {
			break
		}
		body, resumed, valid, stop, e := fetchRange(ctx, a.URL, n, validator)
		if e != nil {
			if !retryable(ctx, e) {
				return "", e
			}
			last = e
			continue
		}
		if n > 0 && !resumed {
			// the whole file again
			if _, e = f.Seek(0, io.SeekStart); e == nil {
				e = f.Truncate(0)
			}
			if e != nil {
				stop()
				return "", e
			}
			h.Reset()
			n = 0
		}
		validator = valid
		written, e := receive(body, f, h, n, a.Size, progress)
		stop()
		n = written
		if e == nil {
			break
		}
		if errors.Is(e, errTooLong) {
			return "", i18n.Detailed(ErrDamagedDownload.New(), e)
		}
		if errors.Is(e, errWriteFile) {
			return "", firmware.SpaceError(dir, a.Size-written, e)
		}
		if !retryable(ctx, e) {
			return "", e
		}
		last = e
	}
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if n != a.Size {
		if last == nil {
			last = fmt.Errorf("%d of %d bytes", n, a.Size)
		}
		if errors.Is(last, errStallTimer) {
			return "", i18n.Detailed(ErrStalled.New(), last)
		}
		var m *i18n.Error
		if errors.As(last, &m) || n == 0 && !errors.Is(last, io.ErrUnexpectedEOF) {
			// nothing came: the connection's own error says why (i18n's causes)
			return "", last
		}
		return "", i18n.Detailed(ErrCutOff.New(), last)
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		// the damaged file goes (the deferred Remove)
		return "", i18n.Detailed(ErrDamagedDownload.New(), errors.New("download checksum mismatch: "+a.Name))
	}
	if e = f.Sync(); e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(tmp, final); e != nil {
		return "", e
	}
	return final, nil
}

// errWriteFile marks a failed write of the download to its file (a full disk): it is
// not resumed.
var errWriteFile = errors.New("could not save the download")

// errTooLong: the server sent more than the release says the file has.
var errTooLong = errors.New("download exceeds expected size")

// receive appends body to f and h from n bytes on, and returns how many bytes of the
// size there are then.
func receive(body io.Reader, f io.Writer, h io.Writer, n, size int64, progress func(int64, int64)) (int64, error) {
	buf := make([]byte, 1<<20)
	for {
		read, re := body.Read(buf)
		if read > 0 {
			if n+int64(read) > size {
				return n, errTooLong
			}
			if _, e := f.Write(buf[:read]); e != nil {
				return n, fmt.Errorf("%w: %w", errWriteFile, e)
			}
			h.Write(buf[:read])
			n += int64(read)
			if progress != nil {
				progress(n, size)
			}
		}
		if re == io.EOF {
			if n < size {
				return n, io.ErrUnexpectedEOF
			}
			return n, nil
		}
		if re != nil {
			return n, re
		}
	}
}
